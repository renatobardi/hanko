package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/pricecheck"
)

// Conferência diária de preços (ADR-0082 item 7). Confere os modelos que a empresa usou nos últimos 30 dias (chamada externa
// ou run) e os que já têm preço vindo das fontes, contra o models.dev e o OpenRouter, e só troca o preço quando as duas
// fontes concordam. O preço novo entra como linha nova de `model_prices` (origem `sources`); a trava manual é a marca
// `locked` da linha vigente. Cada conferência grava o resultado por fonte e por modelo; a falha de uma fonte guarda só o
// código, nunca o corpo. A tabela de preços é global: a conferência a mantém, não a empresa.

// PriceCheckConfig é a saída à internet da conferência. Em produção o cliente é o do netguard (ADR-0029).
type PriceCheckConfig struct {
	Client    pricecheck.HTTPClient
	URLs      map[string]string // vazio = as URLs fixas das fontes
	AllowHTTP bool              // só os testes, com servidor local
}

// priceInUseDays é a janela em que um modelo conta como em uso.
const priceInUseDays = 30

var priceModelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,99}$`)

// canonicalModel é a chave de preço do modelo: minúsculas e sem o prefixo do provedor.
func canonicalModel(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return m
}

// PriceCheckSummary é o resultado de uma conferência.
type PriceCheckSummary struct {
	RunID    domain.ID
	Changes  int
	Failures map[string]string // fonte -> código
}

// priceTargets são os modelos a conferir: os em uso nos últimos 30 dias e os que já têm preço vindo das fontes.
func (s *Service) priceTargets(ctx context.Context, now time.Time) ([]string, error) {
	since := now.AddDate(0, 0, -priceInUseDays)
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT lower(model) FROM model_calls WHERE time_unix_nano >= $1 AND model <> ''
		UNION SELECT DISTINCT lower(model) FROM runs WHERE created_at >= $2 AND model <> ''
		UNION SELECT DISTINCT lower(model) FROM model_prices WHERE origin IN ('sources', 'migrated')`, since.UnixNano(), since)
	if err != nil {
		return nil, fmt.Errorf("price check: modelos: %w", err)
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("price check: modelos: %w", err)
		}
		if priceModelName.MatchString(m) {
			set[canonicalModel(m)] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}

// readPriceSources busca as duas fontes, sem segurar o banco. Falha de uma fonte não derruba a outra.
func (s *Service) readPriceSources(ctx context.Context) map[string]pricecheck.SourceResult {
	cfg := s.PriceCheck
	urls := cfg.URLs
	if len(urls) == 0 {
		urls = pricecheck.DefaultURLs
	}
	results := map[string]pricecheck.SourceResult{}
	for _, src := range pricecheck.Sources {
		data, err := pricecheck.ReadSource(ctx, cfg.Client, src, urls[src], cfg.AllowHTTP)
		if err != nil {
			results[src] = pricecheck.SourceResult{Err: pricecheck.CodeOf(err)}
			continue
		}
		results[src] = pricecheck.SourceResult{Data: data}
	}
	return results
}

// RunPriceCheck faz uma conferência inteira. Nunca troca preço de fonte que falhou; o preço vigente continua.
func (s *Service) RunPriceCheck(ctx context.Context, reason string) (PriceCheckSummary, error) {
	if s.PriceCheck.Client == nil {
		return PriceCheckSummary{}, errors.New("price check: sem cliente HTTP configurado")
	}
	now := s.Now().UTC()
	targets, err := s.priceTargets(ctx, now)
	if err != nil {
		return PriceCheckSummary{}, err
	}
	results := s.readPriceSources(ctx)
	sum := PriceCheckSummary{RunID: domain.NewID(), Failures: map[string]string{}}
	for src, r := range results {
		if r.Err != "" {
			sum.Failures[src] = r.Err
		}
	}
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.tx.Exec(ctx, `INSERT INTO price_check_run (id, reason, started_at, finished_at) VALUES ($1, $2, $3, $3)`, sum.RunID, reason, now); err != nil {
			return fmt.Errorf("price check: gravar a conferência: %w", err)
		}
		for _, src := range pricecheck.Sources {
			r := results[src]
			status := "ok"
			if r.Err != "" {
				status = "down"
			}
			if _, err := t.tx.Exec(ctx, `INSERT INTO price_check_source (run_id, source, status, error_code) VALUES ($1, $2, $3, $4)`, sum.RunID, src, status, r.Err); err != nil {
				return fmt.Errorf("price check: gravar a fonte: %w", err)
			}
		}
		for _, model := range targets {
			changed, err := applyPriceDecision(t, sum.RunID, now, model, results)
			if err != nil {
				return err
			}
			if changed {
				sum.Changes++
			}
		}
		return nil
	})
	return sum, err
}

// currentExactPrice é o preço vigente do modelo pela chave exata (o prefixo não vale aqui: a conferência troca o preço do
// modelo, não o da família), e se a linha está travada.
func currentExactPrice(t *txc, model string, now time.Time) (*pricecheck.Price, domain.ID, bool, error) {
	var p pricecheck.Price
	var id domain.ID
	var locked bool
	err := t.tx.QueryRow(t.ctx, `SELECT id, input_per_mtok::float8, output_per_mtok::float8, cache_read_per_mtok::float8, cache_creation_per_mtok::float8, locked
		FROM model_prices WHERE lower(model) = $1 AND effective_at <= $2 ORDER BY effective_at DESC, id DESC LIMIT 1`, model, now).
		Scan(&id, &p.Input, &p.Output, &p.CacheRead, &p.CacheCreation, &locked)
	if err != nil {
		if isNoRows(err) {
			return nil, domain.ID{}, false, nil
		}
		return nil, domain.ID{}, false, fmt.Errorf("price check: preço vigente: %w", err)
	}
	return &p, id, locked, nil
}

func applyPriceDecision(t *txc, runID domain.ID, now time.Time, model string, results map[string]pricecheck.SourceResult) (bool, error) {
	current, _, locked, err := currentExactPrice(t, model, now)
	if err != nil {
		return false, err
	}
	d := pricecheck.Decide(current, locked, pricecheck.ReadsFor(model, results))
	var priceID *domain.ID
	if d.New != nil {
		id := domain.NewID()
		tag, err := t.tx.Exec(t.ctx, `INSERT INTO model_prices (id, model, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_creation_per_mtok, effective_at, origin)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'sources') ON CONFLICT (model, effective_at) DO NOTHING`, id, model, d.New.Input, d.New.Output, d.New.CacheRead, d.New.CacheCreation, now)
		if err != nil {
			return false, fmt.Errorf("price check: gravar o preço novo: %w", err)
		}
		if tag.RowsAffected() > 0 {
			priceID = &id
		}
	}
	// um resultado por fonte e por modelo; o preço lido de cada uma vai junto (a divergência fica visível)
	for _, src := range pricecheck.Sources {
		r := results[src]
		var in, out, cr, cc *float64
		if id, ok := pricecheck.SourceIDs(model)[src]; ok && r.Err == "" {
			if p, found := r.Data[id]; found {
				in, out, cr, cc = ptrOf(p["input"]), ptrOf(p["output"]), optF(p, "cache_read"), optF(p, "cache_creation")
			}
		}
		status := d.Status
		if r.Err != "" {
			status = pricecheck.StatusSourceDown
		}
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO price_check_result (run_id, model, source, status, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_creation_per_mtok, price_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, runID, model, src, status, in, out, cr, cc, priceID); err != nil {
			return false, fmt.Errorf("price check: gravar o resultado: %w", err)
		}
	}
	return priceID != nil && d.Status == pricecheck.StatusChanged, nil
}

func ptrOf(v float64) *float64 { return &v }

func optF(p pricecheck.Partial, k string) *float64 {
	if v, ok := p[k]; ok {
		return &v
	}
	return nil
}

// ---------- leitura ----------

// PriceCheckSourceView é o resultado de uma fonte numa conferência.
type PriceCheckSourceView struct {
	Source    string
	Status    string
	ErrorCode string
}

// PriceCheckResultView é o resultado de um modelo numa fonte.
type PriceCheckResultView struct {
	Model                                   string
	Source                                  string
	Status                                  string
	Input, Output, CacheRead, CacheCreation *float64
	PriceID                                 *domain.ID
}

// PriceCheckView é uma conferência de preços.
type PriceCheckView struct {
	ID         domain.ID
	Reason     string
	StartedAt  time.Time
	FinishedAt *time.Time
	Sources    []PriceCheckSourceView
	Results    []PriceCheckResultView
}

func (s *Service) loadPriceCheckSources(ctx context.Context, v *PriceCheckView) error {
	rows, err := s.Pool.Query(ctx, `SELECT source, status, error_code FROM price_check_source WHERE run_id = $1 ORDER BY source`, v.ID)
	if err != nil {
		return fmt.Errorf("price checks: %w", err)
	}
	defer rows.Close()
	v.Sources = []PriceCheckSourceView{}
	for rows.Next() {
		var x PriceCheckSourceView
		if err := rows.Scan(&x.Source, &x.Status, &x.ErrorCode); err != nil {
			return fmt.Errorf("price checks: %w", err)
		}
		v.Sources = append(v.Sources, x)
	}
	return rows.Err()
}

// ListPriceChecks lista as conferências de preços, da mais recente para a mais antiga (qualquer membro).
func (s *Service) ListPriceChecks(ctx context.Context, companyID domain.ID, limit, offset int) ([]PriceCheckView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, reason, started_at, finished_at FROM price_check_run ORDER BY started_at DESC, id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("price checks: %w", err)
	}
	var out []PriceCheckView
	for rows.Next() {
		var v PriceCheckView
		if err := rows.Scan(&v.ID, &v.Reason, &v.StartedAt, &v.FinishedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("price checks: %w", err)
		}
		out = append(out, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadPriceCheckSources(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetPriceCheck devolve uma conferência com o resultado de cada modelo (qualquer membro).
func (s *Service) GetPriceCheck(ctx context.Context, companyID, id domain.ID) (PriceCheckView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return PriceCheckView{}, err
	}
	var v PriceCheckView
	if err := s.Pool.QueryRow(ctx, `SELECT id, reason, started_at, finished_at FROM price_check_run WHERE id = $1`, id).
		Scan(&v.ID, &v.Reason, &v.StartedAt, &v.FinishedAt); err != nil {
		return v, mapErr(err, "price check")
	}
	if err := s.loadPriceCheckSources(ctx, &v); err != nil {
		return v, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT model, source, status, input_per_mtok::float8, output_per_mtok::float8, cache_read_per_mtok::float8,
		cache_creation_per_mtok::float8, price_id FROM price_check_result WHERE run_id = $1 ORDER BY model, source`, id)
	if err != nil {
		return v, fmt.Errorf("price checks: %w", err)
	}
	defer rows.Close()
	v.Results = []PriceCheckResultView{}
	for rows.Next() {
		var r PriceCheckResultView
		if err := rows.Scan(&r.Model, &r.Source, &r.Status, &r.Input, &r.Output, &r.CacheRead, &r.CacheCreation, &r.PriceID); err != nil {
			return v, fmt.Errorf("price checks: %w", err)
		}
		v.Results = append(v.Results, r)
	}
	return v, rows.Err()
}
