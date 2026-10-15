package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
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
	// ProjectID keeps only the runs of that project's tasks. Subscription fees (paid cost) are not attributable
	// to a project, so a filtered summary reports no paid cost.
	ProjectID *domain.ID
}

// CostRow is one bucket of a CostSummary. A run has up to four costs:
//   - real (ReportedUSD): the figure the harness reported;
//   - estimated (EstimatedUSD): tokens x price table, used when the harness reported none;
//   - list (ListUSD): tokens x the list price in force when the run finished, always;
//   - paid (PaidUSD): the share of a subscription fee charged to the bucket.
//
// CostUSD is real + estimated: what counts against budgets.
type CostRow struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	CostUSD      float64 `json:"cost_usd"`
	ReportedUSD  float64 `json:"reported_usd"`
	EstimatedUSD float64 `json:"estimated_usd"`
	ListUSD      float64 `json:"list_usd"`
	PaidUSD      float64 `json:"paid_usd"`
	// IdleUSD is the fee of a day nobody used a subscription (day rows only); Unused flags a day
	// whose whole accrual was idle: the UI says "sem uso".
	IdleUSD      float64 `json:"idle_usd"`
	Unused       bool    `json:"unused"`
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
	ListUSD      float64   `json:"list_usd"`
	// PaidUSD + IdleUSD is the subscription fees accrued over the period, up to today: fee ÷ days of
	// the month per day; the paid part went to runs, the idle part to days without use. Over a
	// complete month it sums to the monthly fees.
	PaidUSD       float64            `json:"paid_usd"`
	IdleUSD       float64            `json:"idle_usd"`
	Subscriptions []SubscriptionCost `json:"subscriptions"`
	Runs          int                `json:"runs"`
	InputTokens   int64              `json:"input_tokens"`
	OutputTokens  int64              `json:"output_tokens"`
	CacheTokens   int64              `json:"cache_tokens"`
	Rows          []CostRow          `json:"rows"`
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
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return CostSummary{}, err
	}
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
		FromTs: from.UTC(), ToTs: to.UTC(), ProjectID: f.ProjectID})
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost breakdown: %w", err)
	}
	if f.ProjectID != nil {
		if _, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *f.ProjectID}); err != nil {
			return CostSummary{}, mapErr(err, "get project")
		}
	}
	out := CostSummary{From: from.UTC(), To: to.UTC(), Timezone: co.Timezone, GroupBy: group, Rows: []CostRow{}}
	for _, r := range rows {
		row := CostRow{Key: r.Key, Label: r.Label, CostUSD: r.CostUsd, ReportedUSD: r.CostUsd - r.EstimatedUsd,
			EstimatedUSD: r.EstimatedUsd, ListUSD: r.ListUsd, Runs: int(r.Runs),
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheTokens: r.CacheTokens}
		out.TotalUSD += r.CostUsd
		out.EstimatedUSD += r.EstimatedUsd
		out.ListUSD += r.ListUsd
		out.Runs += int(r.Runs)
		out.InputTokens += r.InputTokens
		out.OutputTokens += r.OutputTokens
		out.CacheTokens += r.CacheTokens
		out.Rows = append(out.Rows, row)
	}
	out.ReportedUSD = out.TotalUSD - out.EstimatedUSD
	alloc := &paidAllocation{}
	if f.ProjectID == nil {
		if alloc, err = s.allocatePaid(ctx, q, co, from.UTC(), to.UTC()); err != nil {
			return CostSummary{}, err
		}
	}
	out.PaidUSD, out.IdleUSD = alloc.paid, alloc.idle
	out.Subscriptions = alloc.subs
	if out.Subscriptions == nil {
		out.Subscriptions = []SubscriptionCost{}
	}
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
	for i := range out.Rows {
		r := &out.Rows[i]
		r.PaidUSD = alloc.byGroup[group][r.Key]
		if group == GroupDay {
			r.IdleUSD = alloc.idleDay[r.Key]
			r.Unused = r.IdleUSD > 0 && r.PaidUSD == 0
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
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return 0, err
	}
	return s.monthSpend(ctx, companyID, nil)
}

// MonthSpendPersona is the spend (USD) of one persona in the current month.
func (s *Service) MonthSpendPersona(ctx context.Context, companyID, personaID domain.ID) (float64, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return 0, err
	}
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
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return BudgetStatus{}, err
	}
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
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return BudgetStatus{}, err
	}
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
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Company{}, err
	}
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
		if err := t.activity(actor, companyID, "budget.updated", "company", companyID,
			map[string]any{"scope": "company", "from": cur.BudgetMonthlyCents, "to": cents}, ""); err != nil {
			return err
		}
		// a higher (or removed) ceiling lifts the budget pause and re-arms the alerts
		if err := t.resetBudgetAlerts(row, companyID, cents, nil, nil); err != nil {
			return err
		}
		if err := t.q.ClearPersonaBudgetRefused(ctx, store.ClearPersonaBudgetRefusedParams{CompanyID: &companyID}); err != nil {
			return fmt.Errorf("clear budget refused: %w", err)
		}
		_, err = t.resumeBudgetPaused(&companyID, nil)
		return err
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

// costResult is what costFor decides for a finished run.
type costResult struct {
	cost     *float64          // the figure that counts against budgets (reported, else estimated)
	source   domain.CostSource // where cost came from
	list     *float64          // tokens x list price in force at the fact time; nil when nothing to price
	fallback bool              // cost was estimated with the fallback price (model without its own)
}

// costFor decides a run's costs at the instant of the fact (at = when the run finished): the
// figure informed by the harness when there is one, else an estimate from the price in force at
// `at`, else unknown. The list cost (tokens x list price in force at `at`) is computed in both
// cases. Prices are an append-only history and the result is stored once on the run, so a price
// inserted later never changes a cost already recorded. A model with no price of its own is
// priced with the fallback price (empty prefix) so it still counts against budgets; fallback
// reports that, and the caller records a cost.unknown_model notice.
func (t *txc) costFor(model string, res adapter.Result, at time.Time) (costResult, error) {
	u := res.Usage
	price, err := t.q.GetModelPrice(t.ctx, store.GetModelPriceParams{Model: model, At: at})
	hasPrice := err == nil
	if err != nil && !isNoRows(err) {
		return costResult{}, fmt.Errorf("get model price: %w", err)
	}
	var out costResult
	tokens := u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheCreationTokens
	if hasPrice && (tokens > 0 || price.Fixed != 0) {
		c := (float64(u.InputTokens)*price.InputPerMtok+float64(u.OutputTokens)*price.OutputPerMtok+
			float64(u.CacheReadTokens)*price.CacheReadPerMtok+float64(u.CacheCreationTokens)*price.CacheCreationPerMtok)/1e6 + price.Fixed
		out.list = &c
	}
	switch {
	case res.CostUSD != nil:
		out.cost, out.source = res.CostUSD, domain.CostReported
	case out.list != nil:
		out.cost, out.source = out.list, domain.CostEstimated
		out.fallback = price.Model == "" && tokens > 0
	default:
		out.source = domain.CostNone
	}
	return out, nil
}

// noteUnknownModel records cost.unknown_model once per company, model and month.
func (t *txc) noteUnknownModel(company store.Company, model string, runID domain.ID) error {
	_, _, month := monthWindow(company.Timezone, t.now)
	label := model
	if label == "" {
		label = "(não informado)"
	}
	_, err := t.q.InsertCostNotice(t.ctx, store.InsertCostNoticeParams{CompanyID: company.ID, Model: model, Month: month, Now: t.now})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert cost notice: %w", err)
	}
	return t.activity(domain.SystemActor, company.ID, "cost.unknown_model", "run", runID,
		map[string]any{"model": label, "month": month, "note": "modelo sem preço na tabela: custo estimado com o preço de reserva"}, "")
}

// reserveEpsilon absorbs float noise when comparing projected spend with a ceiling.
const reserveEpsilon = 1e-9

// runReserve is the floor of the per-run reservation (USD), HANKO_RUN_RESERVE_USD.
func (s *Service) runReserve() float64 {
	if s.RunReserveUSD > 0 {
		return s.RunReserveUSD
	}
	return DefaultRunReserveUSD
}

// DefaultRunReserveUSD is the reservation floor per open run when none is configured.
const DefaultRunReserveUSD = 1.0

// reservationRefused is the checkBudgets refusal caused only by the reservation of open runs (nothing is
// paused). It is a domain.ErrBudgetExceeded for errors.Is. scope is the ceiling that refused it.
type reservationRefused struct{ msg, scope string }

func (e *reservationRefused) Error() string { return e.msg }
func (e *reservationRefused) Unwrap() error { return domain.ErrBudgetExceeded }

// BudgetBlockedError is the refusal of a run because a project's monthly budget is spent. It names the project so
// callers (UI, logs) can say why; it is a domain.ErrBudgetExceeded for errors.Is.
type BudgetBlockedError struct {
	Scope, Name string
	msg         string
}

func (e *BudgetBlockedError) Error() string { return e.msg }
func (e *BudgetBlockedError) Unwrap() error { return domain.ErrBudgetExceeded }

// budgetTarget is one ceiling checkBudgets looks at: the persona's, the project's of the task, the company's.
type budgetTarget struct {
	scope     string
	id        domain.ID
	name      string
	budget    *int64
	personaID *domain.ID // spend filter by persona (nil = no filter)
	projectID *domain.ID // spend filter by project (nil = no filter)
}

func (t *txc) spendOf(company store.Company, tg budgetTarget, from, to time.Time) (float64, error) {
	if tg.projectID != nil {
		v, err := t.q.SpendBetweenProject(t.ctx, store.SpendBetweenProjectParams{CompanyID: company.ID, ProjectID: tg.projectID, FromTs: from, ToTs: to})
		if err != nil {
			return 0, fmt.Errorf("project spend: %w", err)
		}
		return v, nil
	}
	v, err := t.q.SpendBetween(t.ctx, store.SpendBetweenParams{CompanyID: company.ID, PersonaID: tg.personaID, FromTs: from, ToTs: to})
	if err != nil {
		return 0, fmt.Errorf("month spend: %w", err)
	}
	return v, nil
}

// checkBudgets looks at the persona, project (of the task, may be nil) and company ceilings for the current
// month. Crossing 80% records a budget.warning and 100% a budget.exceeded (each at most once per month and
// target); a new budget.exceeded also asks the target's running runs to stop (run.cancel
// after commit). With enforce it also holds the line before a run opens:
//   - spent at or over the persona or company ceiling pauses the persona (persona.paused with the reason);
//   - spent at or over the project ceiling refuses the run (BudgetBlockedError) without pausing anyone: the
//     persona keeps working on tasks of other projects;
//   - spent plus the reservation of every queued/running run plus the reservation of the run
//     about to open over the ceiling refuses it without pausing (it fits again once runs end).
//
// A reservation is max(average cost of the persona's last 10 runs with cost, run reserve
// floor). Both refusals return an error wrapping domain.ErrBudgetExceeded; the caller must
// commit the transaction anyway. Enforcing takes a per-company lock, so concurrent
// checkouts see each other's reservations.
func (t *txc) checkBudgets(company store.Company, persona store.Persona, project *store.Project, enforce bool) error {
	from, to, month := monthWindow(company.Timezone, t.now)
	targets := []budgetTarget{{"persona", persona.ID, persona.Name, persona.BudgetMonthlyCents, &persona.ID, nil}}
	if project != nil {
		targets = append(targets, budgetTarget{"project", project.ID, project.Name, project.BudgetMonthlyCents, nil, &project.ID})
	}
	targets = append(targets, budgetTarget{"company", company.ID, company.Name, company.BudgetMonthlyCents, nil, nil})
	var (
		blocked  error
		reserves map[domain.ID]float64 // active reservations per persona, loaded on demand
		newRes   float64
		pReserve map[domain.ID]float64 // same, restricted to the project's runs
	)
	anyBudget := false
	for _, tg := range targets {
		anyBudget = anyBudget || tg.budget != nil
	}
	if enforce && anyBudget {
		if err := t.q.LockBudget(t.ctx, company.ID.String()); err != nil {
			return fmt.Errorf("lock budget: %w", err)
		}
	}
	for _, tg := range targets {
		if tg.budget == nil {
			continue
		}
		spent, err := t.spendOf(company, tg, from, to)
		if err != nil {
			return err
		}
		st := budgetStatus(tg.budget, spent)
		if st.State == BudgetWarning || st.State == BudgetExceeded {
			if err := t.budgetAlert(company, tg, month, spent, st); err != nil {
				return err
			}
		}
		if !enforce || blocked != nil {
			continue
		}
		if st.State == BudgetExceeded {
			if tg.scope == "project" {
				reason := fmt.Sprintf("Orçamento mensal do projeto %q estourado (gasto US$ %.2f de US$ %.2f)", tg.name, spent, float64(*tg.budget)/100)
				blocked = &BudgetBlockedError{Scope: "project", Name: tg.name, msg: reason + ": " + domain.ErrBudgetExceeded.Error()}
				continue
			}
			reason := "Orçamento mensal da persona estourado"
			if tg.scope == "company" {
				reason = "Orçamento mensal da empresa estourado"
			}
			n, err := t.q.PausePersonaForBudget(t.ctx, store.PausePersonaForBudgetParams{ID: persona.ID, Reason: &reason, Now: t.now})
			if err != nil {
				return fmt.Errorf("pause persona: %w", err)
			}
			if n > 0 {
				data := map[string]any{"reason": reason, "scope": tg.scope, "month": month, "budget_cents": *tg.budget, "spent_usd": spent}
				if err := t.activity(domain.SystemActor, company.ID, "persona.paused", "persona", persona.ID, data, ""); err != nil {
					return err
				}
				t.publish(companyTopic(company.ID), "persona.paused", data)
			}
			blocked = fmt.Errorf("%s: %w", reason, domain.ErrBudgetExceeded)
			continue
		}
		if reserves == nil {
			if reserves, newRes, err = t.reservations(company.ID, persona.ID); err != nil {
				return err
			}
		}
		active := 0.0
		if tg.projectID != nil {
			if pReserve == nil {
				if pReserve, err = t.projectReservations(company.ID, *tg.projectID); err != nil {
					return err
				}
			}
			for _, v := range pReserve {
				active += v
			}
		} else {
			for pid, v := range reserves {
				if tg.personaID == nil || *tg.personaID == pid {
					active += v
				}
			}
		}
		if spent+active+newRes > float64(*tg.budget)/100+reserveEpsilon {
			what := map[string]string{"persona": "da persona", "project": "do projeto", "company": "da empresa"}[tg.scope]
			blocked = &reservationRefused{scope: tg.scope, msg: fmt.Sprintf("orçamento mensal %s não comporta mais uma execução (gasto %.2f + reservas %.2f + nova %.2f > teto %.2f): %s",
				what, spent, active, newRes, float64(*tg.budget)/100, domain.ErrBudgetExceeded)}
		}
	}
	return blocked
}

// reservations loads the USD reserved by the open runs of each persona of the company and
// the reservation a new run of persona would add.
func (t *txc) reservations(companyID, personaID domain.ID) (map[domain.ID]float64, float64, error) {
	floor := t.s.runReserve()
	rows, err := t.q.ActiveRunReserves(t.ctx, store.ActiveRunReservesParams{CompanyID: companyID, DefaultReserve: floor})
	if err != nil {
		return nil, 0, fmt.Errorf("active run reserves: %w", err)
	}
	m := make(map[domain.ID]float64, len(rows))
	for _, r := range rows {
		m[r.PersonaID] = float64(r.Active) * r.ReserveUsd
	}
	nr, err := t.q.PersonaRunReserve(t.ctx, store.PersonaRunReserveParams{PersonaID: personaID, DefaultReserve: floor})
	if err != nil {
		return nil, 0, fmt.Errorf("persona run reserve: %w", err)
	}
	return m, nr, nil
}

// projectReservations is reservations restricted to the open runs of the project's tasks.
func (t *txc) projectReservations(companyID, projectID domain.ID) (map[domain.ID]float64, error) {
	rows, err := t.q.ActiveRunReservesProject(t.ctx, store.ActiveRunReservesProjectParams{CompanyID: companyID, ProjectID: &projectID, DefaultReserve: t.s.runReserve()})
	if err != nil {
		return nil, fmt.Errorf("active project run reserves: %w", err)
	}
	m := make(map[domain.ID]float64, len(rows))
	for _, r := range rows {
		m[r.PersonaID] = float64(r.Active) * r.ReserveUsd
	}
	return m, nil
}

// budgetAlert records budget.warning / budget.exceeded once per (target, month, level). A new
// exceeded alert also asks the running runs of the target to stop.
func (t *txc) budgetAlert(company store.Company, tg budgetTarget, month string, spent float64, st BudgetStatus) error {
	verb, level := "budget.warning", "warning"
	if st.State == BudgetExceeded {
		verb, level = "budget.exceeded", "exceeded"
	}
	_, err := t.q.InsertBudgetAlert(t.ctx, store.InsertBudgetAlertParams{ID: domain.NewID(), CompanyID: company.ID,
		Scope: tg.scope, TargetID: tg.id, Month: month, Level: level, Now: t.now})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert budget alert: %w", err)
	}
	data := map[string]any{"scope": tg.scope, "name": tg.name, "month": month, "budget_cents": *tg.budget,
		"spent_usd": spent, "percent": st.Percent}
	if err := t.activity(domain.SystemActor, company.ID, verb, tg.scope, tg.id, data, ""); err != nil {
		return err
	}
	t.publish(companyTopic(company.ID), verb, data)
	if st.State != BudgetExceeded {
		return nil
	}
	return t.stopRunning(company.ID, tg.personaID, tg.projectID, "budget_exceeded")
}

// stopRunning asks every running run of the persona, of the project's tasks, or (both nil) of the company to
// stop: run.cancel is published after commit and the request is recorded. The runs are not locked, so
// this cannot deadlock with runs finishing; cancelling twice is harmless.
func (t *txc) stopRunning(companyID domain.ID, personaID, projectID *domain.ID, reason string) error {
	var ids []domain.ID
	var err error
	if projectID != nil {
		ids, err = t.q.ListRunningRunIDsForProject(t.ctx, store.ListRunningRunIDsForProjectParams{CompanyID: companyID, ProjectID: projectID})
	} else {
		ids, err = t.q.ListRunningRunIDs(t.ctx, store.ListRunningRunIDsParams{CompanyID: companyID, PersonaID: personaID})
	}
	if err != nil {
		return fmt.Errorf("list running runs: %w", err)
	}
	for _, id := range ids {
		if err := t.activity(domain.SystemActor, companyID, "run.cancelled", "run", id,
			map[string]any{"was": "running", "requested": true, "reason": reason}, ""); err != nil {
			return err
		}
		t.publish(events.RunTopic(id.String()), "run.cancel", id)
	}
	return nil
}

// resetBudgetAlerts forgets the alerts of a target whose ceiling just changed and that no longer
// reach their level, so crossing the new ceiling later in the month alerts (and stops runs) again.
// pid and projectID say whose spend counts (both nil = the company's).
func (t *txc) resetBudgetAlerts(company store.Company, id domain.ID, budget *int64, pid, projectID *domain.ID) error {
	from, to, month := monthWindow(company.Timezone, t.now)
	var st BudgetStatus
	if budget != nil {
		spent, err := t.spendOf(company, budgetTarget{personaID: pid, projectID: projectID}, from, to)
		if err != nil {
			return err
		}
		st = budgetStatus(budget, spent)
	} else {
		st = BudgetStatus{State: BudgetNone}
	}
	if st.State != BudgetExceeded {
		if err := t.q.DeleteBudgetAlert(t.ctx, store.DeleteBudgetAlertParams{TargetID: id, Month: month, Level: "exceeded"}); err != nil {
			return fmt.Errorf("reset budget alert: %w", err)
		}
	}
	if st.State != BudgetExceeded && st.State != BudgetWarning {
		if err := t.q.DeleteBudgetAlert(t.ctx, store.DeleteBudgetAlertParams{TargetID: id, Month: month, Level: "warning"}); err != nil {
			return fmt.Errorf("reset budget alert: %w", err)
		}
	}
	return nil
}

// resumeBudgetPaused brings back personas paused by a budget (pause_reason set) whose company
// and own ceilings are no longer exceeded, and wakes their open tasks. companyID and personaID
// narrow the scan (nil = everything). Returns how many were resumed.
func (t *txc) resumeBudgetPaused(companyID, personaID *domain.ID) (int, error) {
	rows, err := t.q.ListBudgetPausedPersonas(t.ctx, store.ListBudgetPausedPersonasParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return 0, fmt.Errorf("list budget-paused personas: %w", err)
	}
	companies := map[domain.ID]store.Company{}
	over := map[domain.ID]bool{} // company ceiling exceeded
	n := 0
	for _, p := range rows {
		co, ok := companies[p.CompanyID]
		if !ok {
			if co, err = t.q.GetCompanyByID(t.ctx, p.CompanyID); err != nil {
				return n, mapErr(err, "get company")
			}
			companies[p.CompanyID] = co
			from, to, _ := monthWindow(co.Timezone, t.now)
			spent, err := t.q.SpendBetween(t.ctx, store.SpendBetweenParams{CompanyID: co.ID, FromTs: from, ToTs: to})
			if err != nil {
				return n, fmt.Errorf("month spend: %w", err)
			}
			over[co.ID] = budgetStatus(co.BudgetMonthlyCents, spent).State == BudgetExceeded
		}
		if over[co.ID] {
			continue
		}
		if p.BudgetMonthlyCents != nil {
			from, to, _ := monthWindow(co.Timezone, t.now)
			spent, err := t.q.SpendBetween(t.ctx, store.SpendBetweenParams{CompanyID: co.ID, PersonaID: &p.ID, FromTs: from, ToTs: to})
			if err != nil {
				return n, fmt.Errorf("month spend: %w", err)
			}
			if budgetStatus(p.BudgetMonthlyCents, spent).State == BudgetExceeded {
				continue
			}
		}
		rows, err := t.q.ResumePersonaFromBudget(t.ctx, store.ResumePersonaFromBudgetParams{ID: p.ID, Now: t.now})
		if err != nil {
			return n, fmt.Errorf("resume persona: %w", err)
		}
		if rows == 0 {
			continue
		}
		n++
		reason := ""
		if p.PauseReason != nil {
			reason = *p.PauseReason
		}
		if err := t.activity(domain.SystemActor, co.ID, "persona.resumed", "persona", p.ID,
			map[string]any{"reason": "budget_ok", "was": reason}, ""); err != nil {
			return n, err
		}
		tasks, err := t.q.ListWorkableTasksForPersona(t.ctx, &p.ID)
		if err != nil {
			return n, fmt.Errorf("list persona tasks: %w", err)
		}
		for _, tk := range tasks {
			if err := t.wake(co.ID, tk.ID, &p.ID, domain.TaskStatus(tk.Status), domain.TriggerHeartbeat, true); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// ResumeBudgetPaused resumes every persona paused by a budget whose ceilings are no longer
// exceeded (the ceiling went up, or a new month began) and wakes their open tasks. The
// heartbeat calls it on every tick, which covers the first tick of a new month.
func (s *Service) ResumeBudgetPaused(ctx context.Context) (int, error) {
	n := 0
	err := s.withTx(ctx, func(t *txc) error {
		var err error
		n, err = t.resumeBudgetPaused(nil, nil)
		return err
	})
	return n, err
}

// BudgetFor computes spend versus ceiling for callers that already know both
// (previews and tests use it to mirror the service).
func BudgetFor(budgetCents *int64, spentUSD float64) BudgetStatus {
	return budgetStatus(budgetCents, spentUSD)
}
