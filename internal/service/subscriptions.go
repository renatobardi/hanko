package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// defaultGuardPct is the weekly guard threshold when none is given.
const defaultGuardPct = 90

// CreateSubscriptionInput is the body of CreateSubscription.
type CreateSubscriptionInput struct {
	Name           string
	Agent          string // claude | codex | fake
	MonthlyUSD     float64
	ModelPrefixes  []string
	WeeklyLimitUSD *float64
	WeeklyGuardPct int  // default 90
	Inactive       bool // created switched off
}

// UpdateSubscriptionInput changes only the fields that are set.
type UpdateSubscriptionInput struct {
	Name             *string
	Agent            *string
	MonthlyUSD       *float64
	ModelPrefixes    *[]string
	WeeklyLimitUSD   *float64
	ClearWeeklyLimit bool
	WeeklyGuardPct   *int
	Active           *bool
}

// SubscriptionStatus is a subscription with this week's estimated usage against its guard.
type SubscriptionStatus struct {
	domain.Subscription
	WeekStart    time.Time `json:"week_start"`
	WeekEnd      time.Time `json:"week_end"`
	WeekUsageUSD float64   `json:"week_usage_usd"`
	WeekPercent  float64   `json:"week_percent"` // of the weekly limit; 0 without a limit
	Blocked      bool      `json:"blocked"`      // the guard tripped: new covered runs are refused until the week turns
}

func validateSubscription(name, agent string, monthly float64, limit *float64, pct int) error {
	if strings.TrimSpace(name) == "" {
		return domain.Invalid("name", "required")
	}
	if !domain.ValidAgent(agent) {
		return domain.Invalid("agent", fmt.Sprintf("unknown agent %q (want one of %v)", agent, domain.KnownAgents))
	}
	if math.IsNaN(monthly) || math.IsInf(monthly, 0) || monthly < 0 {
		return domain.Invalid("monthly_usd", "must be >= 0")
	}
	if limit != nil && (math.IsNaN(*limit) || math.IsInf(*limit, 0) || *limit <= 0) {
		return domain.Invalid("weekly_limit_usd", "must be > 0")
	}
	if pct < 1 || pct > 100 {
		return domain.Invalid("weekly_guard_pct", "must be between 1 and 100")
	}
	return nil
}

func normalizePrefixes(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// weekWindow returns the Monday-to-Monday week of the company time zone containing now, and its key
// (the Monday date).
func weekWindow(tz string, now time.Time) (from, to time.Time, key string) {
	loc := loadLocation(tz)
	l := now.In(loc)
	back := (int(l.Weekday()) + 6) % 7
	from = time.Date(l.Year(), l.Month(), l.Day()-back, 0, 0, 0, 0, loc)
	return from.UTC(), time.Date(l.Year(), l.Month(), l.Day()-back+7, 0, 0, 0, 0, loc).UTC(), from.Format("2006-01-02")
}

func subscriptionStatus(ctx context.Context, q *store.Queries, co store.Company, sub domain.Subscription, now time.Time) (SubscriptionStatus, error) {
	from, to, _ := weekWindow(co.Timezone, now)
	st := SubscriptionStatus{Subscription: sub, WeekStart: from, WeekEnd: to}
	usage, err := q.SubscriptionWeekUsage(ctx, store.SubscriptionWeekUsageParams{SubscriptionID: &sub.ID, FromTs: from, ToTs: to})
	if err != nil {
		return st, fmt.Errorf("subscription week usage: %w", err)
	}
	st.WeekUsageUSD = usage
	if sub.WeeklyLimitUSD != nil {
		st.WeekPercent = usage / *sub.WeeklyLimitUSD * 100
		st.Blocked = sub.Active && guardTripped(usage, *sub.WeeklyLimitUSD, sub.WeeklyGuardPct)
	}
	return st, nil
}

// guardTripped: the week's usage reached guardPct% of the weekly limit.
func guardTripped(usage, limit float64, guardPct int) bool {
	return usage+reserveEpsilon >= limit*float64(guardPct)/100
}

// CreateSubscription registers a plan of a harness for the company.
func (s *Service) CreateSubscription(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateSubscriptionInput) (domain.Subscription, error) {
	if in.WeeklyGuardPct == 0 {
		in.WeeklyGuardPct = defaultGuardPct
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := validateSubscription(in.Name, in.Agent, in.MonthlyUSD, in.WeeklyLimitUSD, in.WeeklyGuardPct); err != nil {
		return domain.Subscription{}, err
	}
	var out domain.Subscription
	err := s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		row, err := t.q.CreateSubscription(ctx, store.CreateSubscriptionParams{ID: domain.NewID(), CompanyID: companyID,
			Name: in.Name, Agent: in.Agent, MonthlyUsd: in.MonthlyUSD, ModelPrefixes: normalizePrefixes(in.ModelPrefixes),
			WeeklyLimitUsd: in.WeeklyLimitUSD, WeeklyGuardPct: i32(in.WeeklyGuardPct), Active: !in.Inactive, Now: t.now})
		if err != nil {
			return mapErr(err, "create subscription")
		}
		out = subscriptionFromRow(row)
		return t.activity(actor, companyID, "subscription.created", "subscription", out.ID,
			map[string]any{"name": out.Name, "agent": out.Agent, "monthly_usd": out.MonthlyUSD, "weekly_limit_usd": out.WeeklyLimitUSD}, "")
	})
	return out, err
}

// ListSubscriptions returns the company's subscriptions with this week's usage.
func (s *Service) ListSubscriptions(ctx context.Context, companyID domain.ID) ([]SubscriptionStatus, error) {
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil, mapErr(err, "get company")
	}
	rows, err := q.ListSubscriptions(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	out := make([]SubscriptionStatus, 0, len(rows))
	for _, r := range rows {
		st, err := subscriptionStatus(ctx, q, co, subscriptionFromRow(r), s.Now().UTC())
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// GetSubscription returns one subscription, found by id or by name, with this week's usage.
func (s *Service) GetSubscription(ctx context.Context, companyID domain.ID, ref string) (SubscriptionStatus, error) {
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return SubscriptionStatus{}, mapErr(err, "get company")
	}
	var row store.Subscription
	if id, perr := uuid.Parse(ref); perr == nil {
		row, err = q.GetSubscriptionByID(ctx, store.GetSubscriptionByIDParams{CompanyID: companyID, ID: id})
	} else {
		row, err = q.GetSubscriptionByName(ctx, store.GetSubscriptionByNameParams{CompanyID: companyID, Name: ref})
	}
	if err != nil {
		return SubscriptionStatus{}, mapErr(err, "get subscription")
	}
	return subscriptionStatus(ctx, q, co, subscriptionFromRow(row), s.Now().UTC())
}

// UpdateSubscription changes a subscription. Switching it off records when (the paid-cost accrual
// stops after that month); switching it back on clears it.
func (s *Service) UpdateSubscription(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in UpdateSubscriptionInput) (domain.Subscription, error) {
	if in.ClearWeeklyLimit && in.WeeklyLimitUSD != nil {
		return domain.Subscription{}, domain.Invalid("weekly_limit_usd", "cannot set and clear at once")
	}
	var out domain.Subscription
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetSubscriptionByIDForUpdate(ctx, store.GetSubscriptionByIDForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get subscription")
		}
		p := store.UpdateSubscriptionParams{CompanyID: companyID, ID: id, Name: cur.Name, Agent: cur.Agent,
			MonthlyUsd: cur.MonthlyUsd, ModelPrefixes: cur.ModelPrefixes, WeeklyLimitUsd: cur.WeeklyLimitUsd,
			WeeklyGuardPct: cur.WeeklyGuardPct, Active: cur.Active, EndedAt: cur.EndedAt, Now: t.now}
		var changed []string
		if in.Name != nil && strings.TrimSpace(*in.Name) != cur.Name {
			p.Name = strings.TrimSpace(*in.Name)
			changed = append(changed, "name")
		}
		if in.Agent != nil && *in.Agent != cur.Agent {
			p.Agent = *in.Agent
			changed = append(changed, "agent")
		}
		if in.MonthlyUSD != nil && *in.MonthlyUSD != cur.MonthlyUsd {
			p.MonthlyUsd = *in.MonthlyUSD
			changed = append(changed, "monthly_usd")
		}
		if in.ModelPrefixes != nil {
			if np := normalizePrefixes(*in.ModelPrefixes); strings.Join(np, "\x00") != strings.Join(cur.ModelPrefixes, "\x00") {
				p.ModelPrefixes = np
				changed = append(changed, "model_prefixes")
			}
		}
		if in.ClearWeeklyLimit && cur.WeeklyLimitUsd != nil {
			p.WeeklyLimitUsd = nil
			changed = append(changed, "weekly_limit_usd")
		} else if in.WeeklyLimitUSD != nil && (cur.WeeklyLimitUsd == nil || *cur.WeeklyLimitUsd != *in.WeeklyLimitUSD) {
			p.WeeklyLimitUsd = in.WeeklyLimitUSD
			changed = append(changed, "weekly_limit_usd")
		}
		if in.WeeklyGuardPct != nil && i32(*in.WeeklyGuardPct) != cur.WeeklyGuardPct {
			p.WeeklyGuardPct = i32(*in.WeeklyGuardPct)
			changed = append(changed, "weekly_guard_pct")
		}
		if in.Active != nil && *in.Active != cur.Active {
			p.Active = *in.Active
			if *in.Active {
				p.EndedAt = nil
			} else {
				p.EndedAt = &t.now
			}
			changed = append(changed, "active")
		}
		if len(changed) == 0 {
			out = subscriptionFromRow(cur)
			return nil
		}
		if err := validateSubscription(p.Name, p.Agent, p.MonthlyUsd, p.WeeklyLimitUsd, int(p.WeeklyGuardPct)); err != nil {
			return err
		}
		row, err := t.q.UpdateSubscription(ctx, p)
		if err != nil {
			return mapErr(err, "update subscription")
		}
		out = subscriptionFromRow(row)
		return t.activity(actor, companyID, "subscription.updated", "subscription", id, map[string]any{"fields": changed}, "")
	})
	return out, err
}

// DeleteSubscription removes a subscription that no run was ever charged to; one with history is
// switched off instead (the paid-cost history stays readable). Personas linked to it are unlinked.
func (s *Service) DeleteSubscription(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error {
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetSubscriptionByIDForUpdate(ctx, store.GetSubscriptionByIDForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get subscription")
		}
		has, err := t.q.SubscriptionHasRuns(ctx, &id)
		if err != nil {
			return fmt.Errorf("subscription has runs: %w", err)
		}
		if has {
			return fmt.Errorf("subscription has runs charged to it; switch it off instead: %w", domain.ErrConflict)
		}
		if err := t.q.DeleteSubscription(ctx, store.DeleteSubscriptionParams{CompanyID: companyID, ID: id}); err != nil {
			return fmt.Errorf("delete subscription: %w", err)
		}
		return t.activity(actor, companyID, "subscription.deleted", "subscription", id, map[string]any{"name": cur.Name}, "")
	})
}

// GuardedSubscriptions lists the subscriptions whose weekly guard is tripped right now
// (what the inbox shows).
func (s *Service) GuardedSubscriptions(ctx context.Context, companyID domain.ID) ([]SubscriptionStatus, error) {
	all, err := s.ListSubscriptions(ctx, companyID)
	if err != nil {
		return nil, err
	}
	var out []SubscriptionStatus
	for _, st := range all {
		if st.Blocked {
			out = append(out, st)
		}
	}
	return out, nil
}

// subscriptionFor returns the subscription that covers a run of persona p with model, or nil.
func (t *txc) subscriptionFor(p store.Persona, model string) (*domain.Subscription, error) {
	if p.SubscriptionID == nil {
		return nil, nil
	}
	row, err := t.q.GetSubscriptionByID(t.ctx, store.GetSubscriptionByIDParams{CompanyID: p.CompanyID, ID: *p.SubscriptionID})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	sub := subscriptionFromRow(row)
	if !sub.Covers(p.Agent, model) {
		return nil, nil
	}
	return &sub, nil
}

// checkWeeklyGuard refuses a new run when the subscription's estimated usage of the week reached
// its guard (weekly_guard_pct of the weekly limit). Same hard-stop mechanism as the budget: the
// error wraps domain.ErrSubscriptionGuard and the caller commits the transaction anyway. The first
// refusal of a week records subscription.guard_blocked (shown in the inbox).
func (t *txc) checkWeeklyGuard(co store.Company, sub domain.Subscription) error {
	if sub.WeeklyLimitUSD == nil {
		return nil
	}
	from, to, week := weekWindow(co.Timezone, t.now)
	usage, err := t.q.SubscriptionWeekUsage(t.ctx, store.SubscriptionWeekUsageParams{SubscriptionID: &sub.ID, FromTs: from, ToTs: to})
	if err != nil {
		return fmt.Errorf("subscription week usage: %w", err)
	}
	if !guardTripped(usage, *sub.WeeklyLimitUSD, sub.WeeklyGuardPct) {
		return nil
	}
	_, err = t.q.InsertSubscriptionGuardAlert(t.ctx, store.InsertSubscriptionGuardAlertParams{SubscriptionID: sub.ID, Week: week, Now: t.now})
	if err != nil && !isNoRows(err) {
		return fmt.Errorf("insert guard alert: %w", err)
	}
	if err == nil {
		data := map[string]any{"name": sub.Name, "week": week, "weekly_limit_usd": *sub.WeeklyLimitUSD,
			"weekly_guard_pct": sub.WeeklyGuardPct, "usage_usd": usage}
		if err := t.activity(domain.SystemActor, co.ID, "subscription.guard_blocked", "subscription", sub.ID, data, ""); err != nil {
			return err
		}
		t.publish(companyTopic(co.ID), "subscription.guard_blocked", data)
	}
	return fmt.Errorf("trava semanal da assinatura %q: uso %.2f de %.2f (%d%%): %w", sub.Name, usage, *sub.WeeklyLimitUSD, sub.WeeklyGuardPct, domain.ErrSubscriptionGuard)
}
