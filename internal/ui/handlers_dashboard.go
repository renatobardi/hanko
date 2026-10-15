package ui

import (
	"net/http"
	"sort"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func (s *server) handleDashboard(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	stats, err := s.be.Dashboard(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	tasks, err := s.be.ListTasks(ctx, co.ID, service.TaskFilter{Limit: 500})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	acts, err := s.be.ListActivity(ctx, co.ID, service.ActivityFilter{Limit: 5})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	runs, err := s.be.ListRuns(ctx, co.ID, service.RunFilter{Limit: 1})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	pending, err := s.be.ListGates(ctx, co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}, Limit: 5})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc := loadLoc(co.Timezone)
	now := s.now()
	base := "/c/" + co.Slug
	names := s.lookup(r.Context(), personas, tasks)

	vm := views.DashboardVM{
		ActivePersonas:  stats.ActivePersonas,
		TasksInProgress: stats.TasksInProgress,
		MonthCost:       fmtUSDCtx(ctx, stats.MonthCostUSD),
		PendingGates:    stats.PendingGates,
		HasPersonas:     len(personas) > 0,
		OpenTasks:       stats.OpenTasks,
		Gates:           gateRows(ctx, pending, base, now, loc, names),
		Notice:          i18n.T(ctx, gateNotices[r.URL.Query().Get("ok")]),
	}
	if stats.MonthBudgetCents != nil {
		b := budgetBox(ctx, i18n.T(ctx, "Orçamento do mês"), service.BudgetStatus{BudgetCents: stats.MonthBudgetCents, SpentUSD: stats.MonthCostUSD,
			Percent: stats.BudgetPercent, State: stats.BudgetState}, "", "")
		vm.Budget = &b
	}
	switch {
	case stats.RunningRuns == 1:
		vm.Subtitle = co.Name + " · " + i18n.T(ctx, "1 execução em andamento.")
	case stats.RunningRuns > 1:
		vm.Subtitle = co.Name + " · " + i18n.Tf(ctx, "%d execuções em andamento.", stats.RunningRuns)
	default:
		vm.Subtitle = co.Name + " · " + i18n.T(ctx, "nada está rodando agora.")
	}

	// first steps: done flags derived from what exists
	hasTask := len(tasks) > 0
	hasRun := len(runs) > 0
	reviewed := false
	for _, t := range tasks {
		if t.Status == domain.TaskInReview || t.Status == domain.TaskDone {
			reviewed = true
		}
	}
	steps := []views.Step{
		{Label: i18n.T(ctx, "Criar a empresa"), Done: true},
		{Label: i18n.T(ctx, "Contratar a primeira persona"), Done: len(personas) > 0, Href: base + "/personas/nova"},
		{Label: i18n.T(ctx, "Criar a primeira task"), Done: hasTask, Href: base + "/tasks/nova"},
		{Label: i18n.T(ctx, "Atribuir a task e ver a execução"), Done: hasRun, Href: base + "/tasks"},
		{Label: i18n.T(ctx, "Revisar o resultado e comentar na task"), Done: reviewed, Href: base + "/tasks"},
	}
	firstUndone := true
	for i := range steps {
		steps[i].N = i + 1
		if steps[i].Done {
			vm.StepsDone++
		} else if firstUndone {
			steps[i].Now = true
			firstUndone = false
		}
	}
	vm.Steps = steps
	vm.StepsPct = vm.StepsDone * 100 / len(steps)

	// status bars
	counts := map[domain.TaskStatus]int{}
	max := 0
	for _, t := range tasks {
		if t.Status.Open() {
			counts[t.Status]++
			if counts[t.Status] > max {
				max = counts[t.Status]
			}
		}
	}
	for _, st := range []domain.TaskStatus{domain.TaskInProgress, domain.TaskInReview, domain.TaskTodo, domain.TaskBlocked, domain.TaskBacklog} {
		if counts[st] == 0 {
			continue
		}
		vm.Bars = append(vm.Bars, views.Bar{Label: taskStatusLabel(ctx, st), Class: string(st), Count: counts[st], Pct: counts[st] * 100 / max})
	}

	for _, a := range acts {
		vm.Activity = append(vm.Activity, s.activityItem(ctx, a, base, names, now, loc, true))
	}

	recent := append([]service.TaskView(nil), tasks...)
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].UpdatedAt.After(recent[j].UpdatedAt) })
	if len(recent) > 5 {
		recent = recent[:5]
	}
	for _, t := range recent {
		vm.Tasks = append(vm.Tasks, taskRow(ctx, t, base, now, loc))
	}

	sh := s.shell(co, "painel", i18n.T(ctx, "Painel"), views.Crumb{Label: i18n.T(ctx, "Painel")})
	sh.Action = &views.Action{Label: i18n.T(ctx, "Contratar persona"), Href: base + "/personas/nova", Icon: "plus"}
	s.render(w, r, http.StatusOK, views.DashboardPage(sh, vm))
}
