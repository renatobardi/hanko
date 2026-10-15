package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// SubscriptionCost is what one subscription cost over a period: the paid share (monthly fee
// ÷ days of the month, per day, spread over that day's runs in proportion to their list cost),
// the idle share (days with no use) and the list cost of the runs it covered.
type SubscriptionCost struct {
	ID         domain.ID `json:"id"`
	Name       string    `json:"name"`
	Agent      string    `json:"agent"`
	MonthlyUSD float64   `json:"monthly_usd"`
	PaidUSD    float64   `json:"paid_usd"`
	IdleUSD    float64   `json:"idle_usd"`
	ListUSD    float64   `json:"list_usd"`
	Runs       int       `json:"runs"`
}

// paidAllocation is the result of spreading the subscriptions' monthly fees over a period.
type paidAllocation struct {
	byGroup map[string]map[string]float64 // group dimension -> row key -> paid USD
	idleDay map[string]float64            // local day -> USD of the fee of days nobody used
	paid    float64
	idle    float64
	subs    []SubscriptionCost
}

func monthIndex(t time.Time) int { return t.Year()*12 + int(t.Month()) }

func daysInMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

// allocatePaid spreads each subscription's monthly fee over the local days of [from, to) up to
// today: fee ÷ days of the month per day, shared among that day's runs of the subscription in
// proportion to their list cost. A day with no use (or whose runs have no list cost) is idle and
// does not go to any run, so paid + idle over a full month equals the fee. Accrual runs from the
// month the subscription was created to the month it was switched off.
func (s *Service) allocatePaid(ctx context.Context, q *store.Queries, co store.Company, from, to time.Time) (*paidAllocation, error) {
	loc := loadLocation(co.Timezone)
	subs, err := q.ListSubscriptions(ctx, co.ID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	out := &paidAllocation{byGroup: map[string]map[string]float64{}, idleDay: map[string]float64{}}
	for _, g := range []string{GroupDay, GroupPersona, GroupProject, GroupModel, GroupHarness} {
		out.byGroup[g] = map[string]float64{}
	}
	if len(subs) == 0 {
		return out, nil
	}
	runs, err := q.SubscriptionRunsInPeriod(ctx, store.SubscriptionRunsInPeriodParams{Tz: co.Timezone, CompanyID: co.ID, FromTs: from, ToTs: to})
	if err != nil {
		return nil, fmt.Errorf("subscription runs: %w", err)
	}
	type dayKey struct {
		sub domain.ID
		day string
	}
	byDay := map[dayKey][]store.SubscriptionRunsInPeriodRow{}
	for _, r := range runs {
		if r.SubscriptionID != nil {
			k := dayKey{*r.SubscriptionID, r.Day}
			byDay[k] = append(byDay[k], r)
		}
	}
	today := s.Now().In(loc)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	for _, sub := range subs {
		sc := SubscriptionCost{ID: sub.ID, Name: sub.Name, Agent: sub.Agent, MonthlyUSD: sub.MonthlyUsd}
		first := monthIndex(sub.CreatedAt.In(loc))
		last := 1 << 30
		if !sub.Active {
			if sub.EndedAt == nil {
				last = -1
			} else {
				last = monthIndex(sub.EndedAt.In(loc))
			}
		}
		d := from.In(loc)
		d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
		for ; d.Before(to) && !d.After(today); d = d.AddDate(0, 0, 1) {
			rs := byDay[dayKey{sub.ID, d.Format("2006-01-02")}]
			for _, r := range rs {
				sc.Runs++
				sc.ListUSD += r.Weight
			}
			if mi := monthIndex(d); mi < first || mi > last {
				continue
			}
			daily := sub.MonthlyUsd / float64(daysInMonth(d))
			total := 0.0
			for _, r := range rs {
				total += r.Weight
			}
			if total <= 0 {
				out.idleDay[d.Format("2006-01-02")] += daily
				out.idle += daily
				sc.IdleUSD += daily
				continue
			}
			for _, r := range rs {
				share := daily * r.Weight / total
				out.byGroup[GroupDay][r.Day] += share
				out.byGroup[GroupPersona][r.PersonaKey] += share
				out.byGroup[GroupProject][r.ProjectKey] += share
				out.byGroup[GroupModel][r.ModelKey] += share
				out.byGroup[GroupHarness][r.HarnessKey] += share
			}
			out.paid += daily
			sc.PaidUSD += daily
		}
		out.subs = append(out.subs, sc)
	}
	return out, nil
}
