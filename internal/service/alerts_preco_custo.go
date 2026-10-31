package service

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Alertas de preço e de custo de lista de agente externo (ADR-0083 itens 1, 4 e 5; `price_alerts.py` e `cost_alerts.py@845764f`).
// Os de preço leem a conferência mais recente (`price_check_*`) e a tabela de preços, que são globais: o alerta abre em cada
// empresa que usou o modelo nos últimos 30 dias (ou, para a fonte de preço, em cada empresa com uso). Os de custo leem as
// chamadas da empresa. A linha fecha sozinha quando a condição some, pelo motor (EvaluateAlerts).

const (
	priceChangedDays    = 7
	priceSourceDownDays = 2
	costDays            = 7
	usageDays           = 30
)

// modelCompanies devolve, por modelo canônico, as empresas que o usaram nos últimos 30 dias (chamada externa ou run).
func (t *txc) modelCompanies() (map[string][]domain.ID, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT DISTINCT lower(model), company_id FROM model_calls WHERE time_unix_nano >= $1 AND model <> ''
		UNION SELECT DISTINCT lower(model), company_id FROM runs WHERE created_at >= $2 AND model <> ''`,
		t.now.AddDate(0, 0, -usageDays).UnixNano(), t.now.AddDate(0, 0, -usageDays))
	if err != nil {
		return nil, fmt.Errorf("alert conditions: modelos em uso: %w", err)
	}
	defer rows.Close()
	out := map[string][]domain.ID{}
	seen := map[string]map[domain.ID]bool{}
	for rows.Next() {
		var m string
		var c domain.ID
		if err := rows.Scan(&m, &c); err != nil {
			return nil, fmt.Errorf("alert conditions: modelos em uso: %w", err)
		}
		m = canonicalModel(m)
		if seen[m] == nil {
			seen[m] = map[domain.ID]bool{}
		}
		if !seen[m][c] {
			seen[m][c] = true
			out[m] = append(out[m], c)
		}
	}
	return out, rows.Err()
}

func modelCond(company domain.ID, kind domain.AlertKind, model, msg string, data map[string]any) alertCond {
	data["model"] = model
	return alertCond{companyID: company, kind: kind, subjectType: domain.AlertSubjectModel,
		subjectID: domain.AlertSubjectID(company, kind, model), severity: domain.ExternalAlertSeverity(kind), message: msg, data: data}
}

// priceCostConditions reúne os 5 alertas de preço e os 2 de custo.
func (t *txc) priceCostConditions() ([]alertCond, error) {
	users, err := t.modelCompanies()
	if err != nil {
		return nil, err
	}
	var out []alertCond
	for _, f := range []func(map[string][]domain.ID) ([]alertCond, error){t.priceChangedConds, t.priceCheckConds, t.priceSourceDownConds} {
		conds, err := f(users)
		if err != nil {
			return nil, err
		}
		out = append(out, conds...)
	}
	cost, err := t.costConds()
	if err != nil {
		return nil, err
	}
	return append(out, cost...), nil
}

// price_changed: a conferência trocou o preço de um modelo nos últimos 7 dias; um alerta por modelo e eixo (informativo).
func (t *txc) priceChangedConds(users map[string][]domain.ID) ([]alertCond, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT m.model, m.effective_at,
		m.input_per_mtok::float8, p.input_per_mtok::float8, m.output_per_mtok::float8, p.output_per_mtok::float8,
		m.cache_read_per_mtok::float8, p.cache_read_per_mtok::float8, m.cache_creation_per_mtok::float8, p.cache_creation_per_mtok::float8
		FROM model_prices m JOIN LATERAL (SELECT * FROM model_prices x WHERE x.model = m.model AND x.effective_at < m.effective_at
		  ORDER BY x.effective_at DESC LIMIT 1) p ON true
		WHERE m.origin = 'sources' AND m.effective_at > $1 AND m.effective_at <= $2`, t.now.AddDate(0, 0, -priceChangedDays), t.now)
	if err != nil {
		return nil, fmt.Errorf("alert conditions: preço trocado: %w", err)
	}
	defer rows.Close()
	axes := []string{"input", "output", "cache_read", "cache_creation"}
	var out []alertCond
	for rows.Next() {
		var model string
		var at time.Time
		var v [8]float64
		if err := rows.Scan(&model, &at, &v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7]); err != nil {
			return nil, fmt.Errorf("alert conditions: preço trocado: %w", err)
		}
		model = canonicalModel(model)
		for i, axis := range axes {
			now, old := v[2*i], v[2*i+1]
			if math.Abs(now-old) <= 1e-9*math.Max(1, math.Max(math.Abs(now), math.Abs(old))) {
				continue
			}
			for _, c := range users[model] {
				cond := modelCond(c, domain.AlertPriceChanged, model, fmt.Sprintf("Preço de %s trocado (%s): %g → %g USD por 1M tokens", model, axis, old, now),
					map[string]any{"field": axis, "old": old, "new": now, "changed_at": at, "level": "info"})
				cond.subjectID = domain.AlertSubjectID(c, domain.AlertPriceChanged, model, axis)
				out = append(out, cond)
			}
		}
	}
	return out, rows.Err()
}

// priceCheckConds lê a conferência mais recente: fontes divergentes (modelo travado não alerta), preço travado que difere das
// fontes e modelo em uso sem preço.
func (t *txc) priceCheckConds(users map[string][]domain.ID) ([]alertCond, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT DISTINCT r.model, r.status, c.started_at, COALESCE((SELECT p.locked FROM model_prices p WHERE lower(p.model) = r.model
		  AND p.effective_at <= $1 ORDER BY p.effective_at DESC, p.id DESC LIMIT 1), false)
		FROM price_check_result r JOIN price_check_run c ON c.id = r.run_id
		WHERE c.id = (SELECT id FROM price_check_run WHERE started_at <= $1 ORDER BY started_at DESC LIMIT 1)
		  AND r.status IN ('diverges', 'locked_differs')`, t.now)
	if err != nil {
		return nil, fmt.Errorf("alert conditions: conferência de preços: %w", err)
	}
	defer rows.Close()
	var out []alertCond
	for rows.Next() {
		var model, status string
		var since time.Time
		var locked bool
		if err := rows.Scan(&model, &status, &since, &locked); err != nil {
			return nil, fmt.Errorf("alert conditions: conferência de preços: %w", err)
		}
		kind, msg := domain.AlertPriceSourcesDiverge, fmt.Sprintf("As fontes de preço divergem sobre %s", model)
		if status == "locked_differs" {
			kind, msg = domain.AlertPriceLockedDiffers, fmt.Sprintf("O preço travado de %s difere do que as duas fontes concordam", model)
		} else if locked {
			continue // a divergência não importa para um preço travado
		}
		for _, c := range users[model] {
			out = append(out, modelCond(c, kind, model, msg, map[string]any{"since": since, "level": "problem"}))
		}
	}
	return out, rows.Err()
}

// price_source_down: a fonte falhou em conferências de 2 dias UTC diferentes seguidos, desde o último sucesso dela.
func (t *txc) priceSourceDownConds(users map[string][]domain.ID) ([]alertCond, error) {
	var out []alertCond
	companies := map[domain.ID]bool{}
	for _, cs := range users {
		for _, c := range cs {
			companies[c] = true
		}
	}
	ids := make([]domain.ID, 0, len(companies))
	for c := range companies {
		ids = append(ids, c)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, source := range []string{"models.dev", "openrouter"} {
		var latestDown bool
		err := t.tx.QueryRow(t.ctx, `SELECT s.status = 'down' FROM price_check_source s JOIN price_check_run r ON r.id = s.run_id
			WHERE s.source = $1 AND r.started_at <= $2 ORDER BY r.started_at DESC LIMIT 1`, source, t.now).Scan(&latestDown)
		if isNoRows(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("alert conditions: fonte de preço: %w", err)
		}
		if !latestDown {
			continue
		}
		var days int
		var since time.Time
		var reason string
		if err := t.tx.QueryRow(t.ctx, `SELECT count(DISTINCT date_trunc('day', r.started_at AT TIME ZONE 'UTC')), min(r.started_at), COALESCE(max(s.error_code), '')
			FROM price_check_source s JOIN price_check_run r ON r.id = s.run_id
			WHERE s.source = $1 AND s.status = 'down' AND r.started_at <= $2
			  AND r.started_at > COALESCE((SELECT max(r2.started_at) FROM price_check_source s2 JOIN price_check_run r2 ON r2.id = s2.run_id
			    WHERE s2.source = $1 AND s2.status = 'ok' AND r2.started_at <= $2), 'epoch')`, source, t.now).Scan(&days, &since, &reason); err != nil {
			return nil, fmt.Errorf("alert conditions: fonte de preço: %w", err)
		}
		if days < priceSourceDownDays {
			continue
		}
		for _, c := range ids {
			kind := domain.AlertPriceSourceDown
			out = append(out, alertCond{companyID: c, kind: kind, subjectType: domain.AlertSubjectPriceSource,
				subjectID: domain.AlertSubjectID(c, kind, source), severity: domain.ExternalAlertSeverity(kind),
				message: fmt.Sprintf("A fonte de preço %s está fora do ar há %d dias (%s)", source, days, reason),
				data:    map[string]any{"source": source, "days": days, "reason": reason, "since": since, "level": "problem"}})
		}
	}
	return out, nil
}

// costConds: modelo sem preço (price_model_unpriced e cost_subscription_unpriced) e o custo do Claude que difere da tabela.
func (t *txc) costConds() ([]alertCond, error) {
	var out []alertCond
	book, err := loadPriceBook(t.ctx, t)
	if err != nil {
		return nil, err
	}
	rows, err := t.tx.Query(t.ctx, `SELECT m.company_id, lower(m.model), count(*), min(m.time_unix_nano),
		bool_or(m.subscription_key IN ('claude', 'zai', 'codex') OR lower(a.name) IN ('claude', 'codex'))
		FROM model_calls m JOIN external_agent a ON a.id = m.agent_id
		WHERE m.cost_class = 'unpriced' AND m.model <> '' AND m.time_unix_nano >= $1 GROUP BY 1, 2`, t.now.AddDate(0, 0, -usageDays).UnixNano())
	if err != nil {
		return nil, fmt.Errorf("alert conditions: modelo sem preço: %w", err)
	}
	for rows.Next() {
		var company domain.ID
		var model string
		var calls, first int64
		var subscription bool
		if err := rows.Scan(&company, &model, &calls, &first, &subscription); err != nil {
			rows.Close()
			return nil, fmt.Errorf("alert conditions: modelo sem preço: %w", err)
		}
		model = canonicalModel(model)
		if _, priced := book.lookup(model, t.now.UnixNano()); priced {
			continue // a chamada ficou sem preço quando entrou, mas a tabela conhece o modelo agora
		}
		out = append(out, modelCond(company, domain.AlertPriceModelUnpriced, model, fmt.Sprintf("%s: chamadas sem custo real e sem preço na tabela", model),
			map[string]any{"calls": calls, "since": time.Unix(0, first).UTC(), "level": "problem"}))
		if subscription {
			out = append(out, modelCond(company, domain.AlertCostSubscriptionUnpriced, model,
				fmt.Sprintf("%s: chamadas de assinatura sem preço na tabela (fora da soma)", model),
				map[string]any{"calls": calls, "since": time.Unix(0, first).UTC(), "level": "problem"}))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	diff, err := t.claudeCostDiff()
	if err != nil {
		return nil, err
	}
	return append(out, diff...), nil
}

// claudeCostDiff compara, por modelo, o custo que o Claude Code informou com a tabela de preços aplicada aos tokens das mesmas
// chamadas (o preço da hora de cada uma): diferença acima do limite da empresa (`limit.claude_cost_diff_pct`) liga o alerta.
func (t *txc) claudeCostDiff() ([]alertCond, error) {
	limits, err := t.companyLimit("limit.claude_cost_diff_pct")
	if err != nil {
		return nil, err
	}
	rows, err := t.tx.Query(t.ctx, `SELECT m.company_id, lower(m.model), count(*), sum(m.cost_real_usd)::float8, sum(m.cost_list_usd)::float8, min(m.time_unix_nano)
		FROM model_calls m JOIN external_agent a ON a.id = m.agent_id
		WHERE m.cost_class = 'real' AND m.cost_list_usd > 0 AND m.model <> '' AND m.time_unix_nano >= $1
		  AND (m.subscription_key = 'claude' OR (m.subscription_key = '' AND lower(a.name) = 'claude')) GROUP BY 1, 2`,
		t.now.AddDate(0, 0, -costDays).UnixNano())
	if err != nil {
		return nil, fmt.Errorf("alert conditions: custo do claude: %w", err)
	}
	defer rows.Close()
	var out []alertCond
	for rows.Next() {
		var company domain.ID
		var model string
		var calls, first int64
		var reported, table float64
		if err := rows.Scan(&company, &model, &calls, &reported, &table, &first); err != nil {
			return nil, fmt.Errorf("alert conditions: custo do claude: %w", err)
		}
		pct := (reported - table) / table * 100
		limit := limits(company)
		if math.Abs(pct) <= limit {
			continue
		}
		model = canonicalModel(model)
		out = append(out, modelCond(company, domain.AlertCostClaudeDiff, model,
			fmt.Sprintf("%s: informado US$ %.4f × tabela US$ %.4f em %d chamadas (%+.1f%%)", model, reported, table, calls, pct),
			map[string]any{"calls": calls, "reported_usd": reported, "table_usd": table, "pct": math.Round(pct*100) / 100, "limit_pct": limit,
				"since": time.Unix(0, first).UTC(), "level": "problem"}))
	}
	return out, rows.Err()
}

// companyLimit devolve o valor em vigor de um limite por empresa: o da empresa ou o padrão.
func (t *txc) companyLimit(key string) (func(domain.ID) float64, error) {
	spec, ok := domain.AlertSettingSpecOf(key)
	if !ok {
		return nil, fmt.Errorf("alert conditions: limite desconhecido %q", key)
	}
	rows, err := t.tx.Query(t.ctx, `SELECT company_id, value::float8 FROM company_alert_settings WHERE key = $1`, key)
	if err != nil {
		return nil, fmt.Errorf("alert conditions: %s: %w", key, err)
	}
	defer rows.Close()
	over := map[domain.ID]float64{}
	for rows.Next() {
		var c domain.ID
		var v float64
		if err := rows.Scan(&c, &v); err != nil {
			return nil, fmt.Errorf("alert conditions: %s: %w", key, err)
		}
		over[c] = v
	}
	return func(c domain.ID) float64 {
		if v, ok := over[c]; ok {
			return v
		}
		return spec.Default
	}, rows.Err()
}
