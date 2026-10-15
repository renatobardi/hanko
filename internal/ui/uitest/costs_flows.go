package uitest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// LedgerEntry is one costed run, as the cost ledger sees it.
type LedgerEntry struct {
	At        time.Time
	PersonaID domain.ID
	Project   string // project name; "" = none
	Model     string
	Harness   string
	CostUSD   float64
	Estimated bool
	In, Out   int64
}

func (f *Fake) companyTZ() *time.Location {
	if len(f.Companies) > 0 {
		if loc, err := time.LoadLocation(f.Companies[0].Timezone); err == nil {
			return loc
		}
	}
	return time.UTC
}

func (f *Fake) CostSummary(_ context.Context, _ domain.ID, in service.CostFilter) (service.CostSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CostSummary"); err != nil {
		return service.CostSummary{}, err
	}
	loc := f.companyTZ()
	group := in.GroupBy
	if group == "" {
		group = service.GroupDay
	}
	from, to := in.From, in.To
	if from.IsZero() || to.IsZero() {
		n := time.Now().In(loc)
		from = time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
		to = from.AddDate(0, 1, 0)
	}
	out := service.CostSummary{From: from.UTC(), To: to.UTC(), Timezone: loc.String(), GroupBy: group, Rows: []service.CostRow{}}
	idx := map[string]int{}
	for _, e := range f.Ledger {
		if e.At.Before(from) || !e.At.Before(to) {
			continue
		}
		var key, label string
		switch group {
		case service.GroupDay:
			key = e.At.In(loc).Format("2006-01-02")
			label = key
		case service.GroupPersona:
			key = e.PersonaID.String()
			for _, p := range f.Personas {
				if p.ID == e.PersonaID {
					label = p.Name
				}
			}
		case service.GroupProject:
			key, label = e.Project, e.Project
			if key == "" {
				label = "Sem projeto"
			}
		case service.GroupModel:
			key, label = e.Model, e.Model
			if key == "" {
				label = "Não informado"
			}
		case service.GroupHarness:
			key, label = e.Harness, e.Harness
		default:
			return service.CostSummary{}, domain.Invalid("group_by", "inválido")
		}
		i, ok := idx[key]
		if !ok {
			i = len(out.Rows)
			idx[key] = i
			out.Rows = append(out.Rows, service.CostRow{Key: key, Label: label})
		}
		r := &out.Rows[i]
		r.CostUSD += e.CostUSD
		r.Runs++
		r.InputTokens += e.In
		r.OutputTokens += e.Out
		out.TotalUSD += e.CostUSD
		out.Runs++
		out.InputTokens += e.In
		out.OutputTokens += e.Out
		r.ListUSD += e.CostUSD
		out.ListUSD += e.CostUSD
		if e.Estimated {
			r.EstimatedUSD += e.CostUSD
			out.EstimatedUSD += e.CostUSD
		}
	}
	out.ReportedUSD = out.TotalUSD - out.EstimatedUSD
	for i := range out.Rows {
		out.Rows[i].ReportedUSD = out.Rows[i].CostUSD - out.Rows[i].EstimatedUSD
	}
	out.Subscriptions = []service.SubscriptionCost{}
	for _, s := range f.Subs {
		out.Subscriptions = append(out.Subscriptions, service.SubscriptionCost{ID: s.ID, Name: s.Name, Agent: s.Agent, MonthlyUSD: s.MonthlyUSD})
	}
	if group == service.GroupDay {
		by := map[string]service.CostRow{}
		for _, r := range out.Rows {
			by[r.Key] = r
		}
		out.Rows = out.Rows[:0]
		d := from.In(loc)
		d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
		for d.Before(to) {
			k := d.Format("2006-01-02")
			r, ok := by[k]
			if !ok {
				r = service.CostRow{Key: k, Label: k}
			}
			if len(f.Subs) > 0 && !d.After(time.Now().In(loc)) {
				daily := 0.0
				for _, s := range f.Subs {
					daily += s.MonthlyUSD / float64(time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, loc).Day())
				}
				if r.CostUSD > 0 {
					r.PaidUSD = daily
					out.PaidUSD += daily
				} else {
					r.IdleUSD, r.Unused = daily, true
					out.IdleUSD += daily
				}
			}
			out.Rows = append(out.Rows, r)
			d = d.AddDate(0, 0, 1)
		}
	} else {
		sort.SliceStable(out.Rows, func(i, j int) bool { return out.Rows[i].CostUSD > out.Rows[j].CostUSD })
	}
	return out, nil
}

func (f *Fake) monthSpend(personaID *domain.ID) float64 {
	loc := f.companyTZ()
	n := time.Now().In(loc)
	from := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
	var sum float64
	for _, e := range f.Ledger {
		if e.At.Before(from) || (personaID != nil && e.PersonaID != *personaID) {
			continue
		}
		sum += e.CostUSD
	}
	return sum
}

func (f *Fake) CompanyBudget(_ context.Context, _ domain.ID) (service.BudgetStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CompanyBudget"); err != nil {
		return service.BudgetStatus{}, err
	}
	var b *int64
	if len(f.Companies) > 0 {
		b = f.Companies[0].BudgetMonthlyCents
	}
	return service.BudgetFor(b, f.monthSpend(nil)), nil
}

func (f *Fake) PersonaBudget(_ context.Context, _, personaID domain.ID) (service.BudgetStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("PersonaBudget"); err != nil {
		return service.BudgetStatus{}, err
	}
	for _, p := range f.Personas {
		if p.ID == personaID {
			return service.BudgetFor(p.BudgetMonthlyCents, f.monthSpend(&personaID)), nil
		}
	}
	return service.BudgetStatus{}, domain.ErrNotFound
}

func (f *Fake) SetCompanyBudget(_ context.Context, _ domain.Actor, _ domain.ID, cents *int64) (domain.Company, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetCompanyBudget")
	if err := f.fail("SetCompanyBudget"); err != nil {
		return domain.Company{}, err
	}
	if cents != nil && *cents < 0 {
		return domain.Company{}, domain.Invalid("budget_monthly_cents", "must be >= 0")
	}
	f.Companies[0].BudgetMonthlyCents = cents
	return f.Companies[0], nil
}

// ---- flows ----

func (f *Fake) flowView(fl domain.Flow) service.FlowView {
	v := service.FlowView{Flow: fl}
	for _, p := range f.Personas {
		if p.ID == fl.PersonaID {
			v.PersonaName = p.Name
		}
	}
	if fl.ProjectID != nil {
		for _, p := range f.Projects {
			if p.ID == *fl.ProjectID {
				v.ProjectName = p.Name
			}
		}
	}
	return v
}

func (f *Fake) ListFlows(_ context.Context, _ domain.ID) ([]service.FlowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListFlows"); err != nil {
		return nil, err
	}
	out := make([]service.FlowView, 0, len(f.Flows))
	for _, v := range f.Flows {
		out = append(out, f.flowView(v.Flow))
	}
	return out, nil
}

func (f *Fake) GetFlow(_ context.Context, _ domain.ID, ref string) (service.FlowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetFlow"); err != nil {
		return service.FlowView{}, err
	}
	for _, v := range f.Flows {
		if v.Slug == ref || v.ID.String() == ref {
			return f.flowView(v.Flow), nil
		}
	}
	return service.FlowView{}, domain.ErrNotFound
}

func (f *Fake) CreateFlow(_ context.Context, _ domain.Actor, companyID domain.ID, in service.CreateFlowInput) (service.FlowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateFlow")
	if err := f.fail("CreateFlow"); err != nil {
		return service.FlowView{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return service.FlowView{}, domain.Invalid("name", "obrigatório")
	}
	tz := in.Timezone
	if tz == "" {
		tz = f.Companies[0].Timezone
	}
	next, err := service.NextCron(in.Cron, tz, time.Now())
	if err != nil {
		return service.FlowView{}, err
	}
	slug := in.Slug
	if slug == "" {
		slug = domain.Slugify(in.Name)
	}
	for _, v := range f.Flows {
		if v.Slug == slug {
			return service.FlowView{}, fmt.Errorf("flow %q: %w", slug, domain.ErrConflict)
		}
	}
	enabled := in.Enabled == nil || *in.Enabled
	prio := in.Priority
	if prio == "" {
		prio = domain.PriorityMedium
	}
	now := time.Now().UTC()
	fl := domain.Flow{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: in.Name, Description: in.Description, Cron: in.Cron, Timezone: tz,
		Enabled: enabled, PersonaID: in.PersonaID, ProjectID: in.ProjectID, Priority: prio, TaskTitle: in.TaskTitle, TaskDescription: in.TaskDescription,
		NextFireAt: &next, CreatedAt: now, UpdatedAt: now}
	v := f.flowView(fl)
	f.Flows = append(f.Flows, v)
	return v, nil
}

func (f *Fake) UpdateFlow(_ context.Context, _ domain.Actor, _, flowID domain.ID, in service.UpdateFlowInput) (service.FlowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateFlow")
	if err := f.fail("UpdateFlow"); err != nil {
		return service.FlowView{}, err
	}
	for i, v := range f.Flows {
		if v.ID != flowID {
			continue
		}
		fl := v.Flow
		if in.Cron != nil {
			tz := fl.Timezone
			if in.Timezone != nil {
				tz = *in.Timezone
			}
			next, err := service.NextCron(*in.Cron, tz, time.Now())
			if err != nil {
				return service.FlowView{}, err
			}
			fl.Cron, fl.NextFireAt = *in.Cron, &next
		}
		if in.Timezone != nil {
			fl.Timezone = *in.Timezone
		}
		if in.Name != nil {
			if strings.TrimSpace(*in.Name) == "" {
				return service.FlowView{}, domain.Invalid("name", "obrigatório")
			}
			fl.Name = *in.Name
		}
		if in.Description != nil {
			fl.Description = *in.Description
		}
		if in.TaskTitle != nil {
			fl.TaskTitle = *in.TaskTitle
		}
		if in.TaskDescription != nil {
			fl.TaskDescription = *in.TaskDescription
		}
		if in.Enabled != nil {
			fl.Enabled = *in.Enabled
		}
		if in.PersonaID != nil {
			fl.PersonaID = *in.PersonaID
		}
		if in.ClearProject {
			fl.ProjectID = nil
		} else if in.ProjectID != nil {
			fl.ProjectID = in.ProjectID
		}
		if in.Priority != nil {
			fl.Priority = *in.Priority
		}
		f.Flows[i] = f.flowView(fl)
		return f.Flows[i], nil
	}
	return service.FlowView{}, domain.ErrNotFound
}

func (f *Fake) ListFlowFires(_ context.Context, _, flowID domain.ID, limit int) ([]service.FlowFireView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListFlowFires"); err != nil {
		return nil, err
	}
	var out []service.FlowFireView
	for _, fe := range f.Fires {
		if fe.FlowID == flowID {
			out = append(out, fe)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ScheduledFor.After(out[j].ScheduledFor) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *Fake) RunFlowNow(_ context.Context, _ domain.Actor, companyID, flowID domain.ID) (service.FlowFireResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RunFlowNow")
	if err := f.fail("RunFlowNow"); err != nil {
		return service.FlowFireResult{}, err
	}
	for i, v := range f.Flows {
		if v.ID != flowID {
			continue
		}
		now := time.Now().UTC().Truncate(time.Minute)
		f.nextTask++
		key := domain.TaskKey("HNK", f.nextTask)
		t := domain.Task{ID: ID("task:" + key), CompanyID: companyID, Number: f.nextTask, Key: key, Title: v.Name, Status: domain.TaskTodo,
			Priority: v.Priority, CreatedBy: domain.HumanActor, CreatedAt: now, UpdatedAt: now}
		tv := service.TaskView{Task: t}
		pid := v.PersonaID
		tv.PersonaID, tv.PersonaName = &pid, v.PersonaName
		f.Tasks = append(f.Tasks, tv)
		fire := domain.FlowFire{ID: domain.NewID(), FlowID: flowID, CompanyID: companyID, ScheduledFor: now, TaskID: &t.ID, CreatedAt: now}
		f.Fires = append(f.Fires, service.FlowFireView{FlowFire: fire, TaskKey: key, TaskTitle: t.Title, TaskStatus: string(t.Status)})
		f.Flows[i].LastFiredAt = &now
		return service.FlowFireResult{Fire: fire, Task: &tv}, nil
	}
	return service.FlowFireResult{}, domain.ErrNotFound
}

func (f *Fake) ListSubscriptions(_ context.Context, _ domain.ID) ([]service.SubscriptionStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListSubscriptions"); err != nil {
		return nil, err
	}
	return append([]service.SubscriptionStatus(nil), f.Subs...), nil
}

func (f *Fake) CreateSubscription(_ context.Context, _ domain.Actor, companyID domain.ID, in service.CreateSubscriptionInput) (domain.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateSubscription")
	if err := f.fail("CreateSubscription"); err != nil {
		return domain.Subscription{}, err
	}
	if in.WeeklyGuardPct == 0 {
		in.WeeklyGuardPct = 90
	}
	if strings.TrimSpace(in.Name) == "" {
		return domain.Subscription{}, domain.Invalid("name", "required")
	}
	now := time.Now().UTC()
	sub := domain.Subscription{ID: ID("sub:" + in.Name), CompanyID: companyID, Name: in.Name, Agent: in.Agent, MonthlyUSD: in.MonthlyUSD,
		ModelPrefixes: in.ModelPrefixes, WeeklyLimitUSD: in.WeeklyLimitUSD, WeeklyGuardPct: in.WeeklyGuardPct, Active: !in.Inactive,
		CreatedAt: now, UpdatedAt: now}
	f.Subs = append(f.Subs, service.SubscriptionStatus{Subscription: sub})
	return sub, nil
}

func (f *Fake) UpdateSubscription(_ context.Context, _ domain.Actor, _, id domain.ID, in service.UpdateSubscriptionInput) (domain.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateSubscription")
	if err := f.fail("UpdateSubscription"); err != nil {
		return domain.Subscription{}, err
	}
	for i := range f.Subs {
		s := &f.Subs[i]
		if s.ID != id {
			continue
		}
		if in.MonthlyUSD != nil {
			s.MonthlyUSD = *in.MonthlyUSD
		}
		if in.ClearWeeklyLimit {
			s.WeeklyLimitUSD = nil
		} else if in.WeeklyLimitUSD != nil {
			s.WeeklyLimitUSD = in.WeeklyLimitUSD
		}
		if in.WeeklyGuardPct != nil {
			s.WeeklyGuardPct = *in.WeeklyGuardPct
		}
		if in.ModelPrefixes != nil {
			s.ModelPrefixes = *in.ModelPrefixes
		}
		if in.Active != nil {
			s.Active = *in.Active
		}
		return s.Subscription, nil
	}
	return domain.Subscription{}, domain.ErrNotFound
}

func (f *Fake) DeleteSubscription(_ context.Context, _ domain.Actor, _, id domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeleteSubscription")
	if err := f.fail("DeleteSubscription"); err != nil {
		return err
	}
	for i := range f.Subs {
		if f.Subs[i].ID == id {
			f.Subs = append(f.Subs[:i], f.Subs[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}
