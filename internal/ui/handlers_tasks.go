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
	mine       bool // só as tasks em que quem está vendo participa (ADR-0042); sem pessoa autenticada, as que ele criou
}

var taskTabs = []tabDef{
	{"ativas", i18n.N("Ativas"), []domain.TaskStatus{domain.TaskTodo, domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked}, false},
	{"minhas", i18n.N("Minhas"), []domain.TaskStatus{domain.TaskBacklog, domain.TaskTodo, domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked}, true},
	{"backlog", i18n.N("Backlog"), []domain.TaskStatus{domain.TaskBacklog}, false},
	{"concluidas", i18n.N("Concluídas"), []domain.TaskStatus{domain.TaskDone, domain.TaskCancelled}, false},
	{"todas", i18n.N("Todas"), nil, false},
}

var groupOrder = []domain.TaskStatus{
	domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked, domain.TaskTodo,
	domain.TaskBacklog, domain.TaskDone, domain.TaskCancelled,
}

// boardOrder is the column order of the board view (?visao=quadro): the flow, left to right.
var boardOrder = []domain.TaskStatus{
	domain.TaskBacklog, domain.TaskTodo, domain.TaskInProgress, domain.TaskInReview, domain.TaskBlocked,
	domain.TaskDone, domain.TaskCancelled,
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

// taskScope diz onde a lista de tasks é desenhada: a tela /tasks ou a aba Tasks de um projeto (Path,
// Fixed com os parâmetros da página, Project travando o filtro de projeto).
type taskScope struct {
	Path    string
	Fixed   url.Values
	Project *service.ProjectView
	// Match restringe a lista (ex.: tasks que rodam num workspace); nil = todas.
	Match func(service.TaskView) bool
}

func (s *server) handleTasks(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	vm, err := s.buildTasksVM(w, r, co, taskScope{Path: "/c/" + co.Slug + "/tasks"})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
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

// buildTasksVM monta a lista (ou o quadro) de tasks com abas, visão salva, agrupamento e filtros.
func (s *server) buildTasksVM(w http.ResponseWriter, r *http.Request, co domain.Company, scope taskScope) (views.TasksVM, error) {
	ctx := r.Context()
	qs := r.URL.Query()
	q := strings.TrimSpace(qs.Get("q"))
	opts := s.taskViewOpts(w, r)
	cur := taskTabs[0]
	for _, t := range taskTabs {
		if t.key == qs.Get("aba") {
			cur = t
		}
	}
	filter := service.TaskFilter{Query: q, Limit: 500}
	proj := scope.Project
	if ref := strings.TrimSpace(qs.Get("projeto")); ref != "" && proj == nil {
		pv, err := s.be.GetProjectView(ctx, co.ID, ref)
		if err != nil {
			return views.TasksVM{}, err
		}
		proj = &pv
	}
	if proj != nil {
		filter.ProjectID = &proj.ID
	}
	var goal *service.GoalView
	if ref := strings.TrimSpace(qs.Get("meta")); ref != "" {
		gv, err := s.be.GetGoalView(ctx, co.ID, ref)
		if err != nil {
			return views.TasksVM{}, err
		}
		goal = &gv
		filter.GoalID = &gv.ID
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return views.TasksVM{}, err
	}
	personaRef := strings.TrimSpace(qs.Get("persona"))
	for _, p := range personas {
		if p.Slug == personaRef {
			id := p.ID
			filter.PersonaID = &id
		}
	}
	prio := domain.Priority(qs.Get("prioridade"))
	if !prio.Valid() {
		prio = ""
	}
	all, err := s.be.ListTasks(ctx, co.ID, filter)
	if err != nil {
		return views.TasksVM{}, err
	}
	if prio != "" || scope.Match != nil {
		kept := all[:0:0]
		for _, tv := range all {
			if (prio == "" || tv.Priority == prio) && (scope.Match == nil || scope.Match(tv)) {
				kept = append(kept, tv)
			}
		}
		all = kept
	}
	me := actorFrom(ctx)
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()

	vm := views.TasksVM{Query: q, Tab: cur.key, NewHref: base + "/tasks/nova", Board: opts.Board, Path: scope.Path, Embedded: scope.Project != nil,
		GroupBy: opts.Group, SortBy: opts.Sort, PersonaFilter: personaRef, PriorityFilter: string(prio)}
	// filtros que viajam entre abas, visões e a busca (projeto, meta, persona, prioridade)
	keep := url.Values{}
	if goal != nil {
		keep.Set("meta", goal.Slug)
		vm.Goal, vm.GoalTitle, vm.GoalHref = goal.Slug, goal.Title, base+"/metas/"+goal.Slug
		vm.GoalClear = base + "/tasks?aba=" + cur.key
	}
	for k, vs := range scope.Fixed {
		keep[k] = vs
	}
	if scope.Project != nil {
		vm.NewHref += "?projeto=" + url.QueryEscape(proj.Slug)
	} else if proj != nil {
		keep.Set("projeto", proj.Slug)
		vm.Project, vm.ProjectName, vm.ProjectHref = proj.Slug, proj.Name, base+"/projetos/"+proj.Slug
		vm.ClearHref = base + "/tasks?aba=" + cur.key
		vm.NewHref += "?projeto=" + url.QueryEscape(proj.Slug)
		if q != "" {
			vm.ClearHref += "&q=" + url.QueryEscape(q)
		}
	}
	if personaRef != "" {
		keep.Set("persona", personaRef)
	}
	if prio != "" {
		keep.Set("prioridade", string(prio))
	}
	if q != "" {
		keep.Set("q", q)
	}
	link := func(tab string, board bool) string {
		v := url.Values{}
		for k, vs := range keep {
			v[k] = vs
		}
		v.Set("aba", tab)
		if board {
			v.Set("visao", "quadro")
		} else {
			v.Set("visao", "lista")
		}
		return scope.Path + "?" + v.Encode()
	}
	// "Minhas" = participação (ADR-0042): criei, atribuí, fui mencionado, comentei, audiência de gate. Sem pessoa
	// autenticada (modo trusted) vale o de antes: as que o humano local criou.
	var mineSet map[domain.ID][]string
	if personal(ctx) {
		mf := filter
		mf.Mine = true
		mine, err := s.be.ListTasks(ctx, co.ID, mf)
		if err != nil {
			return views.TasksVM{}, err
		}
		mineSet = make(map[domain.ID][]string, len(mine))
		for _, tv := range mine {
			mineSet[tv.ID] = tv.Participation
		}
	}
	inTab := func(t tabDef, tv service.TaskView) bool {
		if t.mine {
			if mineSet != nil {
				if _, ok := mineSet[tv.ID]; !ok {
					return false
				}
			} else if !sameActor(tv.CreatedBy, me) {
				return false
			}
		}
		return inStatuses(tv.Status, t.statuses)
	}
	for _, t := range taskTabs {
		n := 0
		for _, tv := range all {
			if inTab(t, tv) {
				n++
			}
		}
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: link(t.key, opts.Board), Count: n, On: t.key == cur.key})
	}
	vm.ListHref, vm.BoardHref = link(cur.key, false), link(cur.key, true)
	var shown []service.TaskView
	for _, tv := range all {
		if inTab(cur, tv) {
			shown = append(shown, tv)
		}
	}
	vm.Total = len(shown)
	sortTasks(shown, opts.Sort)
	row := func(tv service.TaskView) views.TaskRow {
		r := taskRow(ctx, tv, base, now, loc)
		if cur.mine && mineSet != nil {
			r.Note = participationText(ctx, mineSet[tv.ID])
		}
		return r
	}
	vm.Groups = groupTasks(ctx, shown, opts.Group, row)
	if opts.Board {
		// Quadro: uma coluna por status da aba, na ordem do fluxo, inclusive as vazias.
		byStatus := map[domain.TaskStatus][]service.TaskView{}
		for _, tv := range shown {
			byStatus[tv.Status] = append(byStatus[tv.Status], tv)
		}
		for _, st := range boardOrder {
			if !inStatuses(st, cur.statuses) {
				continue
			}
			col := views.TaskGroup{Label: i18n.T(ctx, groupLabel[st]), Count: len(byStatus[st]), Status: string(st)}
			for _, tv := range byStatus[st] {
				col.Rows = append(col.Rows, row(tv))
			}
			vm.Columns = append(vm.Columns, col)
		}
	}
	vm.GroupOptions = optionList(opts.Group, groupChoices(ctx))
	vm.SortOptions = optionList(opts.Sort, sortChoices(ctx))
	vm.PersonaOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Todas as personas"), Selected: personaRef == ""}}
	for _, p := range personas {
		vm.PersonaOptions = append(vm.PersonaOptions, views.Option{Value: p.Slug, Label: p.Name, Selected: p.Slug == personaRef})
	}
	vm.PriorityOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Todas as prioridades"), Selected: prio == ""}}
	for _, pr := range []domain.Priority{domain.PriorityCritical, domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow} {
		vm.PriorityOptions = append(vm.PriorityOptions, views.Option{Value: string(pr), Label: priorityLabel(ctx, pr), Selected: pr == prio})
	}
	open := 0
	for _, tv := range all {
		if tv.Status.Open() {
			open++
		}
	}
	vm.Subtitle = i18n.Tf(ctx, "%d abertas · agrupadas por status. Uma task guarda dono, prioridade, histórico e execuções.", open)
	vm.Hidden = scope.Fixed
	return vm, nil
}

// participationText diz por que uma task é da pessoa: "criei, comentei".
func participationText(ctx context.Context, reasons []string) string {
	labels := map[string]string{"created": i18n.N("criei"), "assigned": i18n.N("atribuí"), "mentioned": i18n.N("fui mencionado"),
		"commented": i18n.N("comentei"), "gate": i18n.N("audiência de gate")}
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if l, ok := labels[r]; ok {
			out = append(out, i18n.T(ctx, l))
		}
	}
	return strings.Join(out, ", ")
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

	atts, err := s.taskAttachments(ctx, co, t.ID) // anexos da task e de cada comentário (ADR-0052)
	if err != nil {
		return views.TaskVM{}, err
	}
	byComment := map[domain.ID][]service.ArtifactView{}
	for _, a := range atts {
		if a.CommentID != nil {
			byComment[*a.CommentID] = append(byComment[*a.CommentID], a)
		}
	}
	sort.SliceStable(comments, func(i, j int) bool { return comments[i].CreatedAt.Before(comments[j].CreatedAt) })
	for _, c := range comments {
		m := views.Msg{
			Attachments: attachmentRows(ctx, byComment[c.ID], base, "", s.now(), loc),
			Author:      names.actorName(c.Author),
			Human:       c.Author.Type == domain.ActorHuman,
			Body:        renderMD(c.Body),
			When:        fmtDateTime(ctx, c.CreatedAt, loc),
			Initials:    initialsOf(names.actorName(c.Author)),
		}
		if c.RunID != nil {
			m.RunHref = base + "/execucoes/" + c.RunID.String()
		}
		m.Quote, m.QuoteLost = quoteOf(c)
		vm.Thread = append(vm.Thread, m)
	}

	var cost float64
	var activeRun *service.RunView
	for i, rv := range runs {
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
			activeRun = &runs[i]
		}
		vm.Runs = append(vm.Runs, mini)
	}
	vm.Cost = fmtUSDCtx(ctx, cost)
	if t.Status.Open() && !t.System {
		fs, err := s.be.ListFollowups(ctx, co.ID, service.FollowupTarget{TaskID: &t.ID})
		if err != nil {
			return views.TaskVM{}, err
		}
		var steer *views.SteerVM
		if activeRun != nil {
			steer = steerVM(ctx, base, *activeRun)
			if steer != nil {
				steer.Back = base + "/tasks/" + t.Key + "#followups"
			}
		}
		vm.Followups = followupsVM(ctx, base, fs, base+"/tasks/"+t.Key+"#followups", steer)
		vm.Followups.AddHref = base + "/tasks/" + t.Key + "/followups"
	}

	vm.GoalOptions = s.taskGoalOptions(r, co, t.GoalID)
	arts, err := s.be.ListArtifacts(ctx, co.ID, service.ArtifactFilter{TaskID: &t.ID, Limit: 8})
	if err != nil {
		return views.TaskVM{}, err
	}
	vm.Artifacts = artifactMinis(ctx, arts, base)
	usage, err := s.be.GetArtifactUsage(ctx, co.ID)
	if err != nil {
		return views.TaskVM{}, err
	}
	vm.Attach = s.taskAttachBox(ctx, co, t, atts, usage.MaxFileBytes)
	vm.UploadHint = uploadHint(ctx, usage.MaxFileBytes)
	vm.DraftF = s.draftFields(ctx, co, base+"/tasks/"+t.Key+"/rascunho", domain.DraftTask, t.ID)
	vm.ArtifactsHref = base + "/artefatos?task=" + url.QueryEscape(t.Key)
	if vm.Coord, err = s.coordVM(ctx, co, t, personas, names, loc); err != nil {
		return views.TaskVM{}, err
	}
	vm.Cases = s.taskCases(ctx, co, t.ID) // ADR-0034
	vm.Stage = s.taskStageVM(ctx, co, t)  // ADR-0048
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
	if _, present := r.PostForm["goal"]; present {
		v := trimForm(r, "goal")
		switch {
		case v == "" && t.GoalID != nil:
			in.ClearGoal, changed = true, true
		case v != "":
			id, err := domain.ParseID(v)
			if err != nil {
				s.fail(w, r, &co, domain.Invalid("goal", i18n.T(ctx, "id inválido")))
				return
			}
			if t.GoalID == nil || *t.GoalID != id {
				in.GoalID, changed = &id, true
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
	files, _, cleanup, err := s.readForm(w, r, co) // multipart com anexos (ADR-0052) ou urlencoded
	defer cleanup()
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	body := strings.TrimSpace(r.PostFormValue("body"))
	if body == "" && len(files) == 0 {
		s.fail(w, r, &co, domain.Invalid(i18n.T(ctx, "comentário"), i18n.T(ctx, "escreva algo antes de enviar")))
		return
	}
	// os arquivos sobem antes do comentário e passam a pertencer a ele na mesma transação: a persona acordada pelo
	// comentário já os vê no briefing. Só arquivos, sem texto, ficam como anexos da task.
	ids, err := s.attachAll(ctx, co, files, service.PutArtifactInput{TaskID: &t.ID})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if body != "" {
		if _, err := s.be.AddComment(ctx, actorFrom(ctx), co.ID, t.ID, service.AddCommentInput{Body: body, AttachmentIDs: ids}); err != nil {
			s.fail(w, r, &co, err)
			return
		}
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
	s.render(w, r, http.StatusOK, views.ThreadAndAttachments(vm, base))
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
	vm.GoalOptions = s.taskGoalOptions(r, co, nil)
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
	// ?pai=<chave> abre a nova task como subtask (link "Nova subtask" da task pai)
	vm.Parent = strings.TrimSpace(r.URL.Query().Get("pai"))
	// ?meta=<slug> pre-selects the goal (links from the goal page)
	if g := r.URL.Query().Get("meta"); g != "" {
		for i := range vm.GoalOptions {
			vm.GoalOptions[i].Selected = vm.GoalOptions[i].Slug == g
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
	selectOption(vm.GoalOptions, trimForm(r, "goal"))

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
	if v := trimForm(r, "goal"); v != "" {
		id, err := domain.ParseID(v)
		if err != nil {
			vm.Error = i18n.T(ctx, "Meta inválida.")
			s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
			return
		}
		in.GoalID = &id
	}
	vm.Parent, vm.DependsOn = trimForm(r, "parent"), trimForm(r, "depends_on")
	if vm.Parent != "" {
		p, err := s.be.GetTask(ctx, co.ID, vm.Parent)
		if err != nil {
			vm.FieldErr["parent"] = i18n.T(ctx, "task pai não encontrada")
			vm.Error = i18n.T(ctx, "Task pai não encontrada.")
			s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
			return
		}
		in.ParentID = &p.ID
	}
	if vm.DependsOn != "" {
		ids, err := s.parseTaskKeys(ctx, co, vm.DependsOn)
		if err != nil {
			_, title, msg := friendly(ctx, err)
			vm.Error = title + ". " + msg
			s.renderNewTask(w, r, co, http.StatusUnprocessableEntity, vm)
			return
		}
		in.DependsOn = ids
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
