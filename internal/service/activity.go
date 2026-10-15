package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

func (s *Service) ListActivity(ctx context.Context, companyID domain.ID, f ActivityFilter) ([]domain.ActivityEvent, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListActivityParams{CompanyID: companyID, SubjectID: f.SubjectID, Before: f.Before,
		Lim: i32(clamp(f.Limit, 50, 500))}
	if f.SubjectType != "" {
		p.SubjectType = &f.SubjectType
	}
	rows, err := s.read().ListActivity(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	out := make([]domain.ActivityEvent, len(rows))
	for i, r := range rows {
		out[i] = activityFromRow(r)
	}
	return out, nil
}

// Dashboard aggregates the Painel numbers. Month cost covers runs created in the
// current calendar month of the company's time zone.
func (s *Service) Dashboard(ctx context.Context, companyID domain.ID) (DashboardStats, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return DashboardStats{}, err
	}
	q := s.read()
	company, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return DashboardStats{}, mapErr(err, "get company")
	}
	loc, err := time.LoadLocation(company.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := s.Now().In(loc)
	from := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 1, 0)

	c, err := q.DashboardCounts(ctx, companyID)
	if err != nil {
		return DashboardStats{}, fmt.Errorf("dashboard counts: %w", err)
	}
	cost, err := q.MonthCost(ctx, store.MonthCostParams{CompanyID: companyID, FromTs: from.UTC(), ToTs: to.UTC()})
	if err != nil {
		return DashboardStats{}, fmt.Errorf("month cost: %w", err)
	}
	gates, err := q.CountPendingGates(ctx, companyID)
	if err != nil {
		return DashboardStats{}, fmt.Errorf("pending gates: %w", err)
	}
	b := budgetStatus(company.BudgetMonthlyCents, cost)
	return DashboardStats{ActivePersonas: int(c.ActivePersonas), TasksInProgress: int(c.TasksInProgress),
		OpenTasks: int(c.OpenTasks), MonthCostUSD: cost, PendingGates: int(gates), RunningRuns: int(c.RunningRuns),
		MonthBudgetCents: b.BudgetCents, BudgetPercent: b.Percent, BudgetState: b.State}, nil
}
