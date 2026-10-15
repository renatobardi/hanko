package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Padrões dos limites de alerta (HANKO_ALERT_SILENT_FACTOR, HANKO_ALERT_RUN_STUCK_AFTER).
const (
	DefaultAlertSilentFactor  = 3
	DefaultAlertRunStuckAfter = 15 * time.Minute
)

// AlertConfig são os limites dos alertas operacionais. Valores zero usam os padrões.
//
// Critérios (docs/RUNBOOK.md §Alertas):
//   - persona_silent: persona active/idle com heartbeat_interval_sec > 0 e ao menos uma task
//     trabalhável (todo/in_progress) sem run ativa nem gate pendente, cuja última atividade
//     (criação, fim da última run ou última mudança humana numa task dela) passou de
//     SilentFactor × o intervalo dela;
//   - run_stuck: run em running sem nenhum evento (ou, sem eventos, desde o início) há mais de RunStuckAfter;
//   - budget_near_limit: gasto do mês em 80% ou mais do teto da empresa ou da persona (critical a partir de 100%).
type AlertConfig struct {
	SilentFactor  int
	RunStuckAfter time.Duration
}

func (c AlertConfig) withDefaults() AlertConfig {
	if c.SilentFactor <= 0 {
		c.SilentFactor = DefaultAlertSilentFactor
	}
	if c.RunStuckAfter <= 0 {
		c.RunStuckAfter = DefaultAlertRunStuckAfter
	}
	return c
}

// AlertEval resume uma avaliação: quantos alertas abriu, fechou e quantos seguem abertos.
type AlertEval struct {
	Opened, Closed, Open int
}

// alertCond é uma condição observada agora, antes de virar (ou manter) um alerta.
type alertCond struct {
	companyID   domain.ID
	kind        domain.AlertKind
	subjectType string
	subjectID   domain.ID
	severity    domain.AlertSeverity
	message     string
	data        map[string]any
}

type alertKey struct {
	company, subject domain.ID
	kind             domain.AlertKind
}

func (c alertCond) key() alertKey { return alertKey{c.companyID, c.subjectID, c.kind} }

// EvaluateAlerts compara as condições de agora com os alertas abertos: abre o que apareceu,
// atualiza o que continua e fecha o que sumiu. É idempotente (um alerta aberto por condição) e
// roda numa transação só, serializada por lock, com activity alert.opened / alert.closed.
func (s *Service) EvaluateAlerts(ctx context.Context) (AlertEval, error) {
	cfg := s.Alerts.withDefaults()
	var ev AlertEval
	err := s.withTx(ctx, func(t *txc) error {
		ev = AlertEval{}
		if err := t.q.LockAlerts(ctx); err != nil {
			return fmt.Errorf("lock alerts: %w", err)
		}
		conds, err := t.alertConditions(cfg)
		if err != nil {
			return err
		}
		open, err := t.q.ListOpenAlerts(ctx)
		if err != nil {
			return fmt.Errorf("list open alerts: %w", err)
		}
		byKey := make(map[alertKey]store.Alert, len(open))
		for _, a := range open {
			byKey[alertKey{a.CompanyID, a.SubjectID, domain.AlertKind(a.Kind)}] = a
		}
		seen := make(map[alertKey]bool, len(conds))
		for _, c := range conds {
			k := c.key()
			if seen[k] {
				continue
			}
			seen[k] = true
			raw, err := json.Marshal(c.data)
			if err != nil {
				return fmt.Errorf("marshal alert data: %w", err)
			}
			if cur, ok := byKey[k]; ok {
				if err := t.q.TouchAlert(ctx, store.TouchAlertParams{ID: cur.ID, Severity: string(c.severity),
					Message: c.message, Data: raw, Now: t.now}); err != nil {
					return fmt.Errorf("touch alert: %w", err)
				}
				continue
			}
			row, err := t.q.InsertAlert(ctx, store.InsertAlertParams{ID: domain.NewID(), CompanyID: c.companyID,
				Kind: string(c.kind), SubjectType: c.subjectType, SubjectID: c.subjectID, Severity: string(c.severity),
				Message: c.message, Data: raw, Now: t.now})
			if isNoRows(err) {
				continue // outra avaliação abriu primeiro
			}
			if err != nil {
				return fmt.Errorf("insert alert: %w", err)
			}
			a := alertFromRow(row)
			if err := t.activity(domain.SystemActor, a.CompanyID, "alert.opened", "alert", a.ID, alertActivity(a), ""); err != nil {
				return err
			}
			t.publish(companyTopic(a.CompanyID), "alert.updated", a)
			ev.Opened++
		}
		for k, cur := range byKey {
			if seen[k] {
				continue
			}
			row, err := t.q.CloseAlert(ctx, store.CloseAlertParams{ID: cur.ID, Now: &t.now})
			if isNoRows(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("close alert: %w", err)
			}
			a := alertFromRow(row)
			if err := t.activity(domain.SystemActor, a.CompanyID, "alert.closed", "alert", a.ID, alertActivity(a), ""); err != nil {
				return err
			}
			t.publish(companyTopic(a.CompanyID), "alert.updated", a)
			ev.Closed++
		}
		ev.Open = len(seen)
		return nil
	})
	if err != nil {
		return AlertEval{}, err
	}
	return ev, nil
}

func alertActivity(a domain.Alert) map[string]any {
	return map[string]any{"kind": a.Kind, "subject_type": a.SubjectType, "subject_id": a.SubjectID,
		"severity": a.Severity, "message": a.Message}
}

func alertFromRow(r store.Alert) domain.Alert {
	return domain.Alert{ID: r.ID, CompanyID: r.CompanyID, Kind: domain.AlertKind(r.Kind), SubjectType: r.SubjectType,
		SubjectID: r.SubjectID, Severity: domain.AlertSeverity(r.Severity), Message: r.Message, Data: r.Data,
		OpenedAt: r.OpenedAt, LastSeenAt: r.LastSeenAt, ClosedAt: r.ClosedAt}
}

// alertConditions reúne as condições operacionais de todas as empresas.
func (t *txc) alertConditions(cfg AlertConfig) ([]alertCond, error) {
	var out []alertCond

	silent, err := t.q.ListSilentPersonas(t.ctx, store.ListSilentPersonasParams{Now: t.now, Factor: float64(cfg.SilentFactor)})
	if err != nil {
		return nil, fmt.Errorf("list silent personas: %w", err)
	}
	for _, p := range silent {
		interval := time.Duration(p.HeartbeatIntervalSec) * time.Second
		out = append(out, alertCond{companyID: p.CompanyID, kind: domain.AlertPersonaSilent, subjectType: "persona", subjectID: p.ID,
			severity: domain.AlertWarning,
			message: fmt.Sprintf("Persona %s sem heartbeat há %s (intervalo %s; limite %d×)", p.Name,
				humanDuration(t.now.Sub(p.LastActivity)), humanDuration(interval), cfg.SilentFactor),
			data: map[string]any{"persona_slug": p.Slug, "persona_name": p.Name, "interval_sec": p.HeartbeatIntervalSec,
				"factor": cfg.SilentFactor, "last_activity": p.LastActivity}})
	}

	stuck, err := t.q.ListStuckRuns(t.ctx, t.now.Add(-cfg.RunStuckAfter))
	if err != nil {
		return nil, fmt.Errorf("list stuck runs: %w", err)
	}
	for _, r := range stuck {
		out = append(out, alertCond{companyID: r.CompanyID, kind: domain.AlertRunStuck, subjectType: "run", subjectID: r.ID,
			severity: domain.AlertCritical,
			message: fmt.Sprintf("Execução %s (%s) sem eventos há %s (limite %s)", r.TaskKey, r.PersonaName,
				humanDuration(t.now.Sub(r.LastEventAt)), humanDuration(cfg.RunStuckAfter)),
			data: map[string]any{"run_id": r.ID, "task_key": r.TaskKey, "persona_name": r.PersonaName,
				"last_event_at": r.LastEventAt, "limit_sec": int(cfg.RunStuckAfter.Seconds())}})
	}

	budget, err := t.budgetConditions()
	if err != nil {
		return nil, err
	}
	return append(out, budget...), nil
}

// budgetConditions usa a mesma conta do painel de custos (budgetStatus, janela do mês no fuso da
// empresa), então um teto alterado ou a virada do mês fecham o alerta sozinhos. O livro
// budget_alerts continua sendo o registro único de "já avisei neste mês" que gera os fatos
// budget.warning / budget.exceeded; o alerta aberto é o estado atual, que o livro não guarda.
func (t *txc) budgetConditions() ([]alertCond, error) {
	companies, err := t.q.ListCompanies(t.ctx)
	if err != nil {
		return nil, fmt.Errorf("list companies: %w", err)
	}
	var out []alertCond
	add := func(c store.Company, scope string, id domain.ID, name, slug string, pid *domain.ID, budget int64) error {
		from, to, month := monthWindow(c.Timezone, t.now)
		spent, err := t.q.SpendBetween(t.ctx, store.SpendBetweenParams{CompanyID: c.ID, PersonaID: pid, FromTs: from, ToTs: to})
		if err != nil {
			return fmt.Errorf("month spend: %w", err)
		}
		st := budgetStatus(&budget, spent)
		if st.State != BudgetWarning && st.State != BudgetExceeded {
			return nil
		}
		what, sev := "da empresa", domain.AlertWarning
		if scope == "persona" {
			what = "da persona " + name
		}
		if st.State == BudgetExceeded {
			sev = domain.AlertCritical
		}
		out = append(out, alertCond{companyID: c.ID, kind: domain.AlertBudgetNearLimit, subjectType: scope, subjectID: id, severity: sev,
			message: fmt.Sprintf("Orçamento mensal %s em %.0f%% (US$ %.2f de US$ %.2f)", what, st.Percent, spent, float64(budget)/100),
			data: map[string]any{"scope": scope, "name": name, "persona_slug": slug, "month": month, "percent": st.Percent,
				"spent_usd": spent, "budget_cents": budget, "state": st.State}})
		return nil
	}
	for _, c := range companies {
		if c.BudgetMonthlyCents != nil {
			if err := add(c, "company", c.ID, c.Name, "", nil, *c.BudgetMonthlyCents); err != nil {
				return nil, err
			}
		}
	}
	personas, err := t.q.ListBudgetedPersonas(t.ctx)
	if err != nil {
		return nil, fmt.Errorf("list budgeted personas: %w", err)
	}
	byID := make(map[domain.ID]store.Company, len(companies))
	for _, c := range companies {
		byID[c.ID] = c
	}
	for _, p := range personas {
		c, ok := byID[p.CompanyID]
		if !ok || p.BudgetMonthlyCents == nil {
			continue
		}
		pid := p.ID
		if err := add(c, "persona", p.ID, p.Name, p.Slug, &pid, *p.BudgetMonthlyCents); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// humanDuration arredonda para minutos (ou segundos, abaixo de 1 min) em pt-BR curto.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
	return fmt.Sprintf("%d d", int(d.Hours()/24))
}

// ListOpenAlerts devolve os alertas abertos da empresa, críticos primeiro.
func (s *Service) ListOpenAlerts(ctx context.Context, companyID domain.ID, limit int) ([]domain.Alert, error) {
	rows, err := s.read().ListOpenAlertsForCompany(ctx, store.ListOpenAlertsForCompanyParams{CompanyID: companyID, Lim: int32(clamp(limit, 100, 500))})
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	out := make([]domain.Alert, len(rows))
	for i, r := range rows {
		out[i] = alertFromRow(r)
	}
	return out, nil
}

// AlertCounts conta os alertas abertos de todas as empresas por tipo (todos os tipos presentes, com zero);
// alimenta o gauge hanko_alerts_open de /metrics.
func (s *Service) AlertCounts(ctx context.Context) (map[domain.AlertKind]int, error) {
	rows, err := s.read().CountOpenAlertsByKind(ctx)
	if err != nil {
		return nil, fmt.Errorf("count alerts: %w", err)
	}
	out := make(map[domain.AlertKind]int, len(domain.AlertKinds))
	for _, k := range domain.AlertKinds {
		out[k] = 0
	}
	for _, r := range rows {
		out[domain.AlertKind(r.Kind)] = int(r.N)
	}
	return out, nil
}
