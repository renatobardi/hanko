package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Cost grouping dimensions.
const (
	GroupDay     = "day"
	GroupPersona = "persona"
	GroupProject = "project"
	GroupModel   = "model"
	GroupHarness = "harness"
)

// Budget states.
const (
	BudgetNone     = "none"     // no ceiling set
	BudgetOK       = "ok"       // below 80%
	BudgetWarning  = "warning"  // 80% or more
	BudgetExceeded = "exceeded" // 100% or more: hard stop
)

const budgetWarnRatio = 0.8

// CostFilter selects the period [From, To) and the dimension of CostSummary.
// Zero From/To mean the current calendar month of the company; empty GroupBy means day.
type CostFilter struct {
	From, To time.Time
	GroupBy  string
}

// CostRow is one bucket of a CostSummary.
type CostRow struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	CostUSD      float64 `json:"cost_usd"`
	EstimatedUSD float64 `json:"estimated_usd"`
	Runs         int     `json:"runs"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
}

// CostSummary is the cost ledger of a company over a period, grouped by one dimension.
type CostSummary struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	Timezone     string    `json:"timezone"`
	GroupBy      string    `json:"group_by"`
	TotalUSD     float64   `json:"total_usd"`
	ReportedUSD  float64   `json:"reported_usd"`
	EstimatedUSD float64   `json:"estimated_usd"`
	Runs         int       `json:"runs"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CacheTokens  int64     `json:"cache_tokens"`
	Rows         []CostRow `json:"rows"`
}

// BudgetStatus is spend versus ceiling for the current month.
type BudgetStatus struct {
	BudgetCents *int64  `json:"budget_monthly_cents,omitempty"`
	SpentUSD    float64 `json:"spent_usd"`
	Percent     float64 `json:"percent"` // 0 when there is no ceiling
	State       string  `json:"state"`   // none | ok | warning | exceeded
}

func budgetStatus(budget *int64, spentUSD float64) BudgetStatus {
	st := BudgetStatus{BudgetCents: budget, SpentUSD: spentUSD, State: BudgetNone}
	if budget == nil {
		return st
	}
	spentCents := spentUSD * 100
	switch {
	case *budget <= 0:
		st.State = BudgetExceeded
	case spentCents >= float64(*budget):
		st.State = BudgetExceeded
	case spentCents >= budgetWarnRatio*float64(*budget):
		st.State = BudgetWarning
	default:
		st.State = BudgetOK
	}
	if *budget > 0 {
		st.Percent = spentCents / float64(*budget) * 100
	}
	return st
}

func loadLocation(tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// monthWindow returns the calendar month of the company time zone containing now.
func monthWindow(tz string, now time.Time) (from, to time.Time, key string) {
	loc := loadLocation(tz)
	l := now.In(loc)
	from = time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, loc)
	return from.UTC(), from.AddDate(0, 1, 0).UTC(), from.Format("2006-01")
}

// CostSummary aggregates the run ledger of a company. Days follow the company time zone.
func (s *Service) CostSummary(ctx context.Context, companyID domain.ID, f CostFilter) (CostSummary, error) {
	group := f.GroupBy
	if group == "" {
		group = GroupDay
	}
	switch group {
	case GroupDay, GroupPersona, GroupProject, GroupModel, GroupHarness:
	default:
		return CostSummary{}, domain.Invalid("group_by", "want one of day, persona, project, model, harness")
	}
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return CostSummary{}, mapErr(err, "get company")
	}
	from, to := f.From, f.To
	if from.IsZero() && to.IsZero() {
		from, to, _ = monthWindow(co.Timezone, s.Now())
	} else if from.IsZero() || to.IsZero() {
		return CostSummary{}, domain.Invalid("from", "from and to must be given together")
	}
	if !to.After(from) {
		return CostSummary{}, domain.Invalid("to", "must be after from")
	}
	if to.Sub(from) > 400*24*time.Hour {
		return CostSummary{}, domain.Invalid("to", "period must not exceed 400 days")
	}
	rows, err := q.CostBreakdown(ctx, store.CostBreakdownParams{CompanyID: companyID, GroupBy: group, Tz: co.Timezone,
		FromTs: from.UTC(), ToTs: to.UTC()})
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost breakdown: %w", err)
	}
	out := CostSummary{From: from.UTC(), To: to.UTC(), Timezone: co.Timezone, GroupBy: group, Rows: []CostRow{}}
	for _, r := range rows {
		row := CostRow{Key: r.Key, Label: r.Label, CostUSD: r.CostUsd, EstimatedUSD: r.EstimatedUsd, Runs: int(r.Runs),
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheTokens: r.CacheTokens}
		out.TotalUSD += r.CostUsd
		out.EstimatedUSD += r.EstimatedUsd
		out.Runs += int(r.Runs)
		out.InputTokens += r.InputTokens
		out.OutputTokens += r.OutputTokens
		out.CacheTokens += r.CacheTokens
		out.Rows = append(out.Rows, row)
	}
	out.ReportedUSD = out.TotalUSD - out.EstimatedUSD
	if group == GroupDay {
		out.Rows = fillDays(out.Rows, from, to, loadLocation(co.Timezone))
	} else {
		// biggest spender first
		sortRowsByCost(out.Rows)
		if group == GroupProject {
			for i := range out.Rows {
				if out.Rows[i].Key == "" {
					out.Rows[i].Label = "Sem projeto"
				}
			}
		}
		if group == GroupModel {
			for i := range out.Rows {
				if out.Rows[i].Key == "" {
					out.Rows[i].Label = "Não informado"
				}
			}
		}
	}
	return out, nil
}

func sortRowsByCost(rows []CostRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].CostUSD > rows[j-1].CostUSD; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// fillDays returns one row per local calendar day in [from, to), zero where nothing ran.
func fillDays(rows []CostRow, from, to time.Time, loc *time.Location) []CostRow {
	byDay := make(map[string]CostRow, len(rows))
	for _, r := range rows {
		byDay[r.Key] = r
	}
	var out []CostRow
	d := from.In(loc)
	d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	for d.Before(to) {
		k := d.Format("2006-01-02")
		r, ok := byDay[k]
		if !ok {
			r = CostRow{Key: k, Label: k}
		}
		out = append(out, r)
		d = d.AddDate(0, 0, 1)
	}
	return out
}

// MonthSpend is the company spend (USD) in the current month of its time zone.
func (s *Service) MonthSpend(ctx context.Context, companyID domain.ID) (float64, error) {
	return s.monthSpend(ctx, companyID, nil)
}

// MonthSpendPersona is the spend (USD) of one persona in the current month.
func (s *Service) MonthSpendPersona(ctx context.Context, companyID, personaID domain.ID) (float64, error) {
	return s.monthSpend(ctx, companyID, &personaID)
}

func (s *Service) monthSpend(ctx context.Context, companyID domain.ID, personaID *domain.ID) (float64, error) {
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return 0, mapErr(err, "get company")
	}
	from, to, _ := monthWindow(co.Timezone, s.Now())
	v, err := q.SpendBetween(ctx, store.SpendBetweenParams{CompanyID: companyID, PersonaID: personaID, FromTs: from, ToTs: to})
	if err != nil {
		return 0, fmt.Errorf("month spend: %w", err)
	}
	return v, nil
}

// CompanyBudget is the company's spend versus its monthly ceiling.
func (s *Service) CompanyBudget(ctx context.Context, companyID domain.ID) (BudgetStatus, error) {
	co, err := s.read().GetCompanyByID(ctx, companyID)
	if err != nil {
		return BudgetStatus{}, mapErr(err, "get company")
	}
	spent, err := s.MonthSpend(ctx, companyID)
	if err != nil {
		return BudgetStatus{}, err
	}
	return budgetStatus(co.BudgetMonthlyCents, spent), nil
}

// PersonaBudget is a persona's spend versus its monthly ceiling.
func (s *Service) PersonaBudget(ctx context.Context, companyID, personaID domain.ID) (BudgetStatus, error) {
	p, err := s.read().GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
	if err != nil {
		return BudgetStatus{}, mapErr(err, "get persona")
	}
	spent, err := s.MonthSpendPersona(ctx, companyID, personaID)
	if err != nil {
		return BudgetStatus{}, err
	}
	return budgetStatus(p.BudgetMonthlyCents, spent), nil
}

// SetCompanyBudget sets (or, with nil, removes) the company's monthly ceiling in USD cents.
func (s *Service) SetCompanyBudget(ctx context.Context, actor domain.Actor, companyID domain.ID, cents *int64) (domain.Company, error) {
	if cents != nil && *cents < 0 {
		return domain.Company{}, domain.Invalid("budget_monthly_cents", "must be >= 0")
	}
	var out domain.Company
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetCompanyByID(ctx, companyID)
		if err != nil {
			return mapErr(err, "get company")
		}
		if sameCents(cur.BudgetMonthlyCents, cents) {
			out = companyFromRow(cur)
			return nil
		}
		row, err := t.q.SetCompanyBudget(ctx, store.SetCompanyBudgetParams{ID: companyID, BudgetMonthlyCents: cents, Now: t.now})
		if err != nil {
			return mapErr(err, "set company budget")
		}
		out = companyFromRow(row)
		return t.activity(actor, companyID, "budget.updated", "company", companyID,
			map[string]any{"scope": "company", "from": cur.BudgetMonthlyCents, "to": cents}, "")
	})
	return out, err
}

func sameCents(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ListModelPrices returns the price table (newest validity first per model prefix).
func (s *Service) ListModelPrices(ctx context.Context) ([]store.ModelPrice, error) {
	rows, err := s.read().ListModelPrices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list model prices: %w", err)
	}
	return rows, nil
}

// costFor decides a run's cost and its source: the harness figure when informed, else an
// estimate from the price in force at t.now, else unknown.
func (t *txc) costFor(model string, res adapter.Result) (*float64, domain.CostSource, error) {
	if res.CostUSD != nil {
		return res.CostUSD, domain.CostReported, nil
	}
	u := res.Usage
	if model == "" {
		return nil, domain.CostNone, nil
	}
	price, err := t.q.GetModelPrice(t.ctx, store.GetModelPriceParams{Model: model, At: t.now})
	if isNoRows(err) {
		return nil, domain.CostNone, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get model price: %w", err)
	}
	if u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheCreationTokens == 0 && price.Fixed == 0 {
		return nil, domain.CostNone, nil
	}
	cost := (float64(u.InputTokens)*price.InputPerMtok+float64(u.OutputTokens)*price.OutputPerMtok+
		float64(u.CacheReadTokens)*price.CacheReadPerMtok+float64(u.CacheCreationTokens)*price.CacheCreationPerMtok)/1e6 + price.Fixed
	return &cost, domain.CostEstimated, nil
}

// checkBudgets looks at the persona and company ceilings for the current month. Crossing
// 80% records a budget.warning and 100% a budget.exceeded (each at most once per month and
// target). With enforce, an exceeded ceiling also pauses the persona and returns an error
// wrapping domain.ErrBudgetExceeded; the caller must commit the transaction anyway.
func (t *txc) checkBudgets(company store.Company, persona store.Persona, enforce bool) error {
	from, to, month := monthWindow(company.Timezone, t.now)
	targets := []struct {
		scope  string
		id     domain.ID
		name   string
		budget *int64
		pid    *domain.ID
	}{
		{"persona", persona.ID, persona.Name, persona.BudgetMonthlyCents, &persona.ID},
		{"company", company.ID, company.Name, company.BudgetMonthlyCents, nil},
	}
	var blocked error
	for _, tg := range targets {
		if tg.budget == nil {
			continue
		}
		spent, err := t.q.SpendBetween(t.ctx, store.SpendBetweenParams{CompanyID: company.ID, PersonaID: tg.pid, FromTs: from, ToTs: to})
		if err != nil {
			return fmt.Errorf("month spend: %w", err)
		}
		st := budgetStatus(tg.budget, spent)
		if st.State != BudgetWarning && st.State != BudgetExceeded {
			continue
		}
		verb, level := "budget.warning", "warning"
		if st.State == BudgetExceeded {
			verb, level = "budget.exceeded", "exceeded"
		}
		_, err = t.q.InsertBudgetAlert(t.ctx, store.InsertBudgetAlertParams{ID: domain.NewID(), CompanyID: company.ID,
			Scope: tg.scope, TargetID: tg.id, Month: month, Level: level, Now: t.now})
		if err == nil {
			data := map[string]any{"scope": tg.scope, "name": tg.name, "month": month, "budget_cents": *tg.budget,
				"spent_usd": spent, "percent": st.Percent}
			if err := t.activity(domain.SystemActor, company.ID, verb, tg.scope, tg.id, data, ""); err != nil {
				return err
			}
			t.publish(companyTopic(company.ID), verb, data)
		} else if !isNoRows(err) {
			return fmt.Errorf("insert budget alert: %w", err)
		}
		if enforce && st.State == BudgetExceeded && blocked == nil {
			reason := "Orçamento mensal da persona estourado"
			if tg.scope == "company" {
				reason = "Orçamento mensal da empresa estourado"
			}
			if err := t.q.PausePersonaForBudget(t.ctx, store.PausePersonaForBudgetParams{ID: persona.ID, Reason: &reason, Now: t.now}); err != nil {
				return fmt.Errorf("pause persona: %w", err)
			}
			blocked = fmt.Errorf("%s: %w", reason, domain.ErrBudgetExceeded)
		}
	}
	return blocked
}
