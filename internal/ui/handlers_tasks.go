package ui

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

type tabDef struct {
	key, label string
	statuses   []domain.TaskStatus
}

var taskTabs = []tabDef{
	{"ativas", i18n.N("Ativas"), []domain.TaskStatus{domain.TaskTodo, domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked}},
	{"backlog", i18n.N("Backlog"), []domain.TaskStatus{domain.TaskBacklog}},
	{"concluidas", i18n.N("Concluídas"), []domain.TaskStatus{domain.TaskDone, domain.TaskCancelled}},
	{"todas", i18n.N("Todas"), nil},
}

var groupOrder = []domain.TaskStatus{
	domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked, domain.TaskTodo,
	domain.TaskBacklog, domain.TaskDone, domain.TaskCancelled,
}

var groupLabel = map[domain.TaskStatus]string{
	domain.TaskInProgress: i18n.N("Em andamento"), domain.TaskInReview: i18n.N("Em revisão"), domain.TaskBlocked: i18n.N("Bloqueadas"),
	domain.TaskTodo: i18n.N("A fazer"), domain.TaskBacklog: i18n.N("Backlog"), domain.TaskDone: i18n.N("Concluídas"), domain.TaskCancelled: i18n.N("Canceladas"),
}

func inStatuses(st domain.TaskStatus, list []domain.TaskStatus) bool {
	if list == nil {
		return true
	}
	for _, x := range list {
		if x == st {
			return true
		}
	}
	return false
}

func (s *server) handleTasks(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	tab := r.URL.Query().Get("aba")
	cur := taskTabs[0]
	for _, t := range taskTabs {
		if t.key == tab {
			cur = t
		}
	}
	filter := service.TaskFilter{Query: q, Limit: 500}
	var proj *service.ProjectView
	if ref := strings.TrimSpace(r.URL.Query().Get("projeto")); ref != "" {
		pv, err := s.be.GetProjectView(ctx, co.ID, ref)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		proj = &pv
		filter.ProjectID = &pv.ID
	}
	all, err := s.be.ListTasks(ctx, co.ID, filter)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()

	vm := views.TasksVM{Query: q, Tab: cur.key, NewHref: base + "/tasks/nova"}
	projQS := ""
	if proj != nil {
		projQS = "&projeto=" + url.QueryEscape(proj.Slug)
		vm.Project, vm.ProjectName, vm.ProjectHref = proj.Slug, proj.Name, base+"/projetos/"+proj.Slug
		vm.ClearHref = base + "/tasks?aba=" + cur.key
		vm.NewHref += "?projeto=" + url.QueryEscape(proj.Slug)
		if q != "" {
			vm.ClearHref += "&q=" + url.QueryEscape(q)
		}
	}
	for _, t := range taskTabs {
		n := 0
		for _, tv := range all {
			if inStatuses(tv.Status, t.statuses) {
				n++
			}
		}
		href := base + "/tasks?aba=" + t.key + projQS
		if q != "" {
			href += "&q=" + url.QueryEscape(q)
		}
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: href, Count: n, On: t.key == cur.key})
	}
	byStatus := map[domain.TaskStatus][]service.TaskView{}
	for _, tv := range all {
		if inStatuses(tv.Status, cur.statuses) {
			byStatus[tv.Status] = append(byStatus[tv.Status], tv)
			vm.Total++
		}
	}
	for _, st := range groupOrder {
		list := byStatus[st]
		if len(list) == 0 {
			continue
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
		g := views.TaskGroup{Label: i18n.T(ctx, groupLabel[st]), Count: len(list)}
		for _, tv := range list {
			g.Rows = append(g.Rows, taskRow(ctx, tv, base, now, loc))
		}
		vm.Groups = append(vm.Groups, g)
	}
	open := 0
	for _, tv := range all {
		if tv.Status.Open() {
			open++
		}
	}
	vm.Subtitle = i18n.Tf(ctx, "%d abertas · agrupadas por status. Uma task guarda dono, prioridade, histórico e execuções.", open)

	if isHX(r) {
		s.render(w, r, http.StatusOK, views.TaskTable(vm))
		return
	}
	sh := s.shell(r, co, "tasks", i18n.T(ctx, "Tasks"), views.Crumb{Label: i18n.T(ctx, "Tasks")})
	if sh.CanWrite() {
		sh.Action = &views.Action{Label: i18n.T(ctx, "Nova task"), Href: vm.NewHref, Icon: "plus"}
	}
	s.render(w, r, http.StatusOK, views.TasksPage(sh, vm))
}

// ---------- detail ----------

func statusOptions(ctx context.Context, sel domain.TaskStatus, only ...domain.TaskStatus) []views.Option {
	list := []domain.TaskStatus{domain.TaskBacklog, domain.TaskTodo, domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked, domain.TaskDone, domain.TaskCancelled}
	if len(only) > 0 {
		list = only
	}
	var out []views.Option
	for _, st := range list {
		out = append(out, views.Option{Value: string(st), Label: taskStatusLabel(ctx, st), Selected: st == sel})
	}
	return out
}

func priorityOptions(ctx context.Context, sel domain.Priority) []views.Option {
	var out []views.Option
	for _, p := range []domain.Priority{domain.PriorityCritical, domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow} {
		out = append(out, views.Option{Value: string(p), Label: priorityLabel(ctx, p), Selected: p == sel})
	}
	return out
}

func personaOptions(personas []domain.Persona, sel *domain.ID, emptyLabel string) []views.Option {
	out := []views.Option{{Value: "", Label: emptyLabel, Selected: sel == nil}}
	for _, p := range personas {
		out = append(out, views.Option{Value: p.ID.String(), Label: p.Name, Selected: sel != nil && *sel == p.ID})
	}
	return out
}

var notices = map[string]string{
	"acordada":  i18n.N("Persona acordada: a execução entra na fila."),
	"criada":    i18n.N("Task criada."),
	"aprovado":  gateNotices["aprovado"],
	"rejeitado": gateNotices["rejeitado"],
	"mudancas":  gateNotices["mudancas"],
}

// buildTaskVM loads everything the task page and its fragments need.
func (s *server) buildTaskVM(r *http.Request, co domain.Company, t service.TaskView) (views.TaskVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return views.TaskVM{}, err
	}
	comments, err := s.be.ListComments(ctx, co.ID, t.ID)
	if err != nil {
		return views.TaskVM{}, err
	}
	runs, err := s.be.ListRuns(ctx, co.ID, service.RunFilter{TaskID: &t.ID, Limit: 20})
	if err != nil {
		return views.TaskVM{}, err
	}
	names := s.lookup(r.Context(), personas, nil)
	pending, err := s.be.ListGates(ctx, co.ID, service.GateFilter{TaskID: &t.ID, Statuses: []domain.GateStatus{domain.GatePending}, Limit: 20})
	if err != nil {
		return views.TaskVM{}, err
	}

	vm := views.TaskVM{
		Gates:           gateRows(ctx, pending, base, now, loc, names),
		Key:             t.Key,
		Model:           t.Model,
		Title:           t.Title,
		Description:     renderMD(t.Description),
		HasDescription:  strings.TrimSpace(t.Description) != "",
		Badge:           taskBadge(ctx, t),
		Priority:        string(t.Priority),
		PriorityLabel:   priorityLabel(ctx, t.Priority),
		PersonaName:     t.PersonaName,
		ProjectName:     t.ProjectName,
		ProjectOptions:  s.taskProjectOptions(r, co, t.ProjectID),
		Updated:         fmtDateTime(ctx, t.UpdatedAt, loc),
		StatusOptions:   statusOptions(ctx, t.Status),
		PriorityOptions: priorityOptions(ctx, t.Priority),
		PersonaOptions:  personaOptions(personas, t.PersonaID, i18n.T(ctx, "Sem dono")),
		Notice:          i18n.T(ctx, notices[r.URL.Query().Get("ok")]),
		TaskKey:         t.Key,
		DescDoc:         docOf(t.Description),
		DescAnchors:     anchorViewsJSON(ctx, comments, names, loc, domain.AnchorTaskDescription, nil),
		IslandAPI:       islandAPI(co),
		AnchorMsgs:      anchorMsgs(ctx),
	}
	vm.Created = fmtDateTime(ctx, t.CreatedAt, loc)
	if t.PersonaID != nil {
		for _, p := range personas {
			if p.ID == *t.PersonaID {
				vm.PersonaHref = base + "/personas/" + p.Slug
			}
		}
	}
	if t.ProjectID != nil {
		for _, o := range vm.ProjectOptions {
			if o.Selected {
				vm.ProjectHref = base + "/projetos/" + o.Slug
			}
		}
	}
	vm.CanWake = t.PersonaID != nil && t.Status.Open() && t.ActiveRunID == nil && !t.System

	sort.SliceStable(comments, func(i, j int) bool { return comments[i].CreatedAt.Before(comments[j].CreatedAt) })
	for _, c := range comments {
		m := views.Msg{
			Author:   names.actorName(c.Author),
			Human:    c.Author.Type == domain.ActorHuman,
			Body:     renderMD(c.Body),
			When:     fmtDateTime(ctx, c.CreatedAt, loc),
			Initials: initialsOf(names.actorName(c.Author)),
		}
		if c.RunID != nil {
			m.RunHref = base + "/execucoes/" + c.RunID.String()
		}
		m.Quote, m.QuoteLost = quoteOf(c)
		vm.Thread = append(vm.Thread, m)
	}

	var cost float64
	for _, rv := range runs {
		if rv.CostUSD != nil {
			cost += *rv.CostUSD
		}
		mini := views.RunMini{
			Name:   rv.Name,
			Short:  shortID(rv.ID),
			Href:   base + "/execucoes/" + rv.ID.String(),
			Label:  runLabel(ctx, rv.Status),
			When:   runDuration(rv.Run, now),
			Dot:    runDot(rv.Status),
			Active: rv.Status.Active(),
		}
		if rv.Status.Active() && vm.ActiveRun == nil {
			m := mini
			vm.ActiveRun = &m
		}
		vm.Runs = append(vm.Runs, mini)
	}
	vm.Cost = fmtUSDCtx(ctx, cost)
	return vm, nil
}

func (s *server) handleTask(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, err := s.be.GetTask(r.Context(), co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.buildTaskVM(r, co, t)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "tasks", t.Key+" · "+t.Title, views.Crumb{Label: i18n.T(r.Context(), "Tasks"), Href: base + "/tasks"}, views.Crumb{Label: t.Key})
	sh.Flush = true
	s.render(w, r, http.StatusOK, views.TaskPage(sh, vm, base))
}

func (s *server) handleUpdateTask(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	t, err := s.be.GetTask(ctx, co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var in service.UpdateTaskInput
	changed := false
	if v := trimForm(r, "status"); v != "" && domain.TaskStatus(v) != t.Status {
		st := domain.TaskStatus(v)
		if !st.Valid() {
			s.fail(w, r, &co, domain.Invalid("status", i18n.T(ctx, "valor desconhecido")))
			return
		}
		in.Status, changed = &st, true
	}
	if v := trimForm(r, "priority"); v != "" && domain.Priority(v) != t.Priority {
		p := domain.Priority(v)
		if !p.Valid() {
			s.fail(w, r, &co, domain.Invalid("priority", i18n.T(ctx, "valor desconhecido")))
			return
		}
		in.Priority, changed = &p, true
	}
	if _, present := r.PostForm["persona"]; present {
		v := trimForm(r, "persona")
		switch {
		case v == "" && t.PersonaID != nil:
			in.Unassign, changed = true, true
		case v != "":
			id, err := domain.ParseID(v)
			if err != nil {
				s.fail(w, r, &co, domain.Invalid("persona", i18n.T(ctx, "id inválido")))
				return
			}
			if t.PersonaID == nil || *t.PersonaID != id {
				in.PersonaID, changed = &id, true
			}
		}
	}
	if _, present := r.PostForm["project"]; present {
		v := trimForm(r, "project")
		switch {
		case v == "" && t.ProjectID != nil:
			in.ClearProject, changed = true, true
		case v != "":
			id, err := domain.ParseID(v)
			if err != nil {
				s.fail(w, r, &co, domain.Invalid("project", i18n.T(ctx, "id inválido")))
				return
			}
			if t.ProjectID == nil || *t.ProjectID != id {
				in.ProjectID, changed = &id, true
			}
		}
	}
	if changed {
		t, err = s.be.UpdateTask(ctx, actorFrom(ctx), co.ID, t.ID, in)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
	}
	base := "/c/" + co.Slug
	if !isHX(r) {
		redirect(w, r, base+"/tasks/"+t.Key)
		return
	}
	vm, err := s.buildTaskVM(r, co, t)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.TaskSideUpdate(vm, base))
}

func (s *server) handleComment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	t, err := s.be.GetTask(ctx, co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	body := strings.TrimSpace(r.PostFormValue("body"))
	if body == "" {
		s.fail(w, r, &co, domain.Invalid(i18n.T(ctx, "comentário"), i18n.T(ctx, "escreva algo antes de enviar")))
		return
	}
	if _, err := s.be.AddComment(ctx, actorFrom(ctx), co.ID, t.ID, service.AddCommentInput{Body: body}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	if !isHX(r) {
		redirect(w, r, base+"/tasks/"+t.Key+"#thread")
		return
	}
	vm, err := s.buildTaskVM(r, co, t)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.Thread(vm, base))
}

func (s *server) handleWake(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, err := s.be.GetTask(r.Context(), co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := s.be.WakeTask(r.Context(), actorFrom(r.Context()), co.ID, t.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/tasks/"+t.Key+"?ok=acordada")
}

// ---------- new ----------

func (s *server) newTaskVM(r *http.Request, co domain.Company) (views.NewTaskVM, error) {
	personas, err := s.be.ListPersonas(r.Context(), co.ID)
	if err != nil {
		return views.NewTaskVM{}, err
	}
	ctx := r.Context()
	vm := views.NewTaskVM{
		PriorityOptions: priorityOptions(ctx, domain.PriorityMedium),
		StatusOptions:   statusOptions(ctx, domain.TaskTodo, domain.TaskBacklog, domain.TaskTodo),
		PersonaOptions:  personaOptions(personas, nil, i18n.T(ctx, "Sem dono (backlog)")),
		FieldErr:        map[string]string{},
	}
	vm.ProjectOptions = s.taskProjectOptions(r, co, nil)
	return vm, nil
}

func (s *server) handleNewTask(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm, err := s.newTaskVM(r, co)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	// ?persona=<id> pre-selects the owner (links from a persona page)
	if p := r.URL.Query().Get("persona"); p != "" {
		for i := range vm.PersonaOptions {
			vm.PersonaOptions[i].Selected = vm.PersonaOptions[i].Value == p
		}
	}
	// ?projeto=<slug> pre-selects the project (links from the project page)
	if p := r.URL.Query().Get("projeto"); p != "" {
		for i := range vm.ProjectOptions {
			vm.ProjectOptions[i].Selected = vm.ProjectOptions[i].Slug == p
		}
	}
	s.renderNewTask(w, r, co, http.StatusOK, vm)
}

func (s *server) renderNewTask(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.NewTaskVM) {
	base := "/c/" + co.Slug
	ctx := r.Context()
	sh := s.shell(r, co, "tasks", i18n.T(ctx, "Nova task"), views.Crumb{Label: i18n.T(ctx, "Tasks"), Href: base + "/tasks"}, views.Crumb{Label: i18n.T(ctx, "Nova")})
	s.render(w, r, status, views.NewTaskPage(sh, vm))
}

func (s *server) handleCreateTask(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm, err := s.newTaskVM(r, co)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Title = trimForm(r, "title")
	vm.Description = r.PostFormValue("description")
	vm.Model = trimForm(r, "model")
	in := service.CreateTaskInput{Title: vm.Title, Description: vm.Description, Model: vm.Model}
	status := domain.TaskStatus(trimForm(r, "status"))
	if status == "" {
		status = domain.TaskTodo
	}
	prio := domain.Priority(trimForm(r, "priority"))
	if prio == "" {
		prio = domain.PriorityMedium
	}
	in.Status, in.Priority = status, prio
	ctx := r.Context()
	vm.PriorityOptions = priorityOptions(ctx, prio)
	vm.StatusOptions = statusOptions(ctx, status, domain.TaskBacklog, domain.TaskTodo)
	selectOption(vm.PersonaOptions, trimForm(r, "persona"))
	selectOption(vm.ProjectOptions, trimForm(r, "project"))

	if vm.Title == "" {
		vm.FieldErr["title"] = i18n.T(ctx, "Dê um título à task.")
		vm.Error = i18n.T(ctx, "Faltou o título.")
		s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
		return
	}
	if v := trimForm(r, "persona"); v != "" {
		id, err := domain.ParseID(v)
		if err != nil {
			vm.Error = i18n.T(ctx, "Persona inválida.")
			s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
			return
		}
		in.PersonaID = &id
	}
	if v := trimForm(r, "project"); v != "" {
		id, err := domain.ParseID(v)
		if err != nil {
			vm.Error = i18n.T(ctx, "Projeto inválido.")
			s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
			return
		}
		in.ProjectID = &id
	}
	t, err := s.be.CreateTask(r.Context(), actorFrom(r.Context()), co.ID, in)
	if err != nil {
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
		s.renderNewTask(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/tasks/"+t.Key+"?ok=criada")
}

func selectOption(opts []views.Option, v string) {
	for i := range opts {
		opts[i].Selected = opts[i].Value == v
	}
}
