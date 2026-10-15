package ui

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

type tabDef struct {
	key, label string
	statuses   []domain.TaskStatus
}

var taskTabs = []tabDef{
	{"ativas", "Ativas", []domain.TaskStatus{domain.TaskTodo, domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked}},
	{"backlog", "Backlog", []domain.TaskStatus{domain.TaskBacklog}},
	{"concluidas", "Concluídas", []domain.TaskStatus{domain.TaskDone, domain.TaskCancelled}},
	{"todas", "Todas", nil},
}

var groupOrder = []domain.TaskStatus{
	domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked, domain.TaskTodo,
	domain.TaskBacklog, domain.TaskDone, domain.TaskCancelled,
}

var groupLabel = map[domain.TaskStatus]string{
	domain.TaskInProgress: "Em andamento", domain.TaskInReview: "Em revisão", domain.TaskBlocked: "Bloqueadas",
	domain.TaskTodo: "A fazer", domain.TaskBacklog: "Backlog", domain.TaskDone: "Concluídas", domain.TaskCancelled: "Canceladas",
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
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	tab := r.URL.Query().Get("aba")
	cur := taskTabs[0]
	for _, t := range taskTabs {
		if t.key == tab {
			cur = t
		}
	}
	all, err := s.be.ListTasks(r.Context(), co.ID, service.TaskFilter{Query: q, Limit: 500})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()

	vm := views.TasksVM{Query: q, Tab: cur.key, NewHref: base + "/tasks/nova"}
	for _, t := range taskTabs {
		n := 0
		for _, tv := range all {
			if inStatuses(tv.Status, t.statuses) {
				n++
			}
		}
		href := base + "/tasks?aba=" + t.key
		if q != "" {
			href += "&q=" + url.QueryEscape(q)
		}
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: t.label, Href: href, Count: n, On: t.key == cur.key})
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
		g := views.TaskGroup{Label: groupLabel[st], Count: len(list)}
		for _, tv := range list {
			g.Rows = append(g.Rows, taskRow(tv, base, now, loc))
		}
		vm.Groups = append(vm.Groups, g)
	}
	open := 0
	for _, tv := range all {
		if tv.Status.Open() {
			open++
		}
	}
	vm.Subtitle = fmtInt(open) + " abertas · agrupadas por status. Uma task guarda dono, prioridade, histórico e execuções."

	if isHX(r) {
		s.render(w, r, http.StatusOK, views.TaskTable(vm))
		return
	}
	sh := s.shell(co, "tasks", "Tasks", views.Crumb{Label: "Tasks"})
	sh.Action = &views.Action{Label: "Nova task", Href: base + "/tasks/nova", Icon: "plus"}
	s.render(w, r, http.StatusOK, views.TasksPage(sh, vm))
}

// ---------- detail ----------

func statusOptions(sel domain.TaskStatus, only ...domain.TaskStatus) []views.Option {
	list := []domain.TaskStatus{domain.TaskBacklog, domain.TaskTodo, domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked, domain.TaskDone, domain.TaskCancelled}
	if len(only) > 0 {
		list = only
	}
	var out []views.Option
	for _, st := range list {
		out = append(out, views.Option{Value: string(st), Label: taskStatusLabel(st), Selected: st == sel})
	}
	return out
}

func priorityOptions(sel domain.Priority) []views.Option {
	var out []views.Option
	for _, p := range []domain.Priority{domain.PriorityCritical, domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow} {
		out = append(out, views.Option{Value: string(p), Label: priorityLabel(p), Selected: p == sel})
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
	"acordada":  "Persona acordada: a execução entra na fila.",
	"criada":    "Task criada.",
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
		Gates:           gateRows(pending, base, now, loc, names),
		Key:             t.Key,
		Model:           t.Model,
		Title:           t.Title,
		Description:     renderMD(t.Description),
		HasDescription:  strings.TrimSpace(t.Description) != "",
		Badge:           taskBadge(t),
		Priority:        string(t.Priority),
		PriorityLabel:   priorityLabel(t.Priority),
		PersonaName:     t.PersonaName,
		ProjectName:     t.ProjectName,
		Created:         ago(t.CreatedAt, now, loc),
		Updated:         fmtDateTime(t.UpdatedAt, loc),
		StatusOptions:   statusOptions(t.Status),
		PriorityOptions: priorityOptions(t.Priority),
		PersonaOptions:  personaOptions(personas, t.PersonaID, "Sem dono"),
		Notice:          notices[r.URL.Query().Get("ok")],
	}
	vm.Created = fmtDateTime(t.CreatedAt, loc)
	if t.PersonaID != nil {
		for _, p := range personas {
			if p.ID == *t.PersonaID {
				vm.PersonaHref = base + "/personas/" + p.Slug
			}
		}
	}
	vm.CanWake = t.PersonaID != nil && t.Status.Open() && t.ActiveRunID == nil

	sort.SliceStable(comments, func(i, j int) bool { return comments[i].CreatedAt.Before(comments[j].CreatedAt) })
	for _, c := range comments {
		m := views.Msg{
			Author:   names.actorName(c.Author),
			Human:    c.Author.Type == domain.ActorHuman,
			Body:     renderMD(c.Body),
			When:     fmtDateTime(c.CreatedAt, loc),
			Initials: initialsOf(names.actorName(c.Author)),
		}
		if c.RunID != nil {
			m.RunHref = base + "/execucoes/" + c.RunID.String()
		}
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
			Label:  runLabel(rv.Status),
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
	vm.Cost = fmtUSD(cost)
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
	sh := s.shell(co, "tasks", t.Key+" · "+t.Title, views.Crumb{Label: "Tasks", Href: base + "/tasks"}, views.Crumb{Label: t.Key})
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
			s.fail(w, r, &co, domain.Invalid("status", "valor desconhecido"))
			return
		}
		in.Status, changed = &st, true
	}
	if v := trimForm(r, "priority"); v != "" && domain.Priority(v) != t.Priority {
		p := domain.Priority(v)
		if !p.Valid() {
			s.fail(w, r, &co, domain.Invalid("priority", "valor desconhecido"))
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
				s.fail(w, r, &co, domain.Invalid("persona", "id inválido"))
				return
			}
			if t.PersonaID == nil || *t.PersonaID != id {
				in.PersonaID, changed = &id, true
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
		s.fail(w, r, &co, domain.Invalid("comentário", "escreva algo antes de enviar"))
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
	projects, _ := s.be.ListProjects(r.Context(), co.ID) // optional: the form works without projects
	vm := views.NewTaskVM{
		PriorityOptions: priorityOptions(domain.PriorityMedium),
		StatusOptions:   statusOptions(domain.TaskTodo, domain.TaskBacklog, domain.TaskTodo),
		PersonaOptions:  personaOptions(personas, nil, "Sem dono (backlog)"),
		FieldErr:        map[string]string{},
	}
	vm.ProjectOptions = []views.Option{{Value: "", Label: "Nenhum", Selected: true}}
	for _, p := range projects {
		vm.ProjectOptions = append(vm.ProjectOptions, views.Option{Value: p.ID.String(), Label: p.Name})
	}
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
	s.renderNewTask(w, r, co, http.StatusOK, vm)
}

func (s *server) renderNewTask(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.NewTaskVM) {
	base := "/c/" + co.Slug
	sh := s.shell(co, "tasks", "Nova task", views.Crumb{Label: "Tasks", Href: base + "/tasks"}, views.Crumb{Label: "Nova"})
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
	vm.PriorityOptions = priorityOptions(prio)
	vm.StatusOptions = statusOptions(status, domain.TaskBacklog, domain.TaskTodo)
	selectOption(vm.PersonaOptions, trimForm(r, "persona"))
	selectOption(vm.ProjectOptions, trimForm(r, "project"))

	if vm.Title == "" {
		vm.FieldErr["title"] = "Dê um título à task."
		vm.Error = "Faltou o título."
		s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
		return
	}
	if v := trimForm(r, "persona"); v != "" {
		id, err := domain.ParseID(v)
		if err != nil {
			vm.Error = "Persona inválida."
			s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
			return
		}
		in.PersonaID = &id
	}
	if v := trimForm(r, "project"); v != "" {
		id, err := domain.ParseID(v)
		if err != nil {
			vm.Error = "Projeto inválido."
			s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
			return
		}
		in.ProjectID = &id
	}
	t, err := s.be.CreateTask(r.Context(), actorFrom(r.Context()), co.ID, in)
	if err != nil {
		_, title, msg := friendly(err)
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
