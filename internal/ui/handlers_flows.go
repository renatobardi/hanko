package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

var flowNotices = map[string]string{
	"criado":            i18n.N("Fluxo criado."),
	"salvo":             i18n.N("Fluxo atualizado."),
	"executado":         i18n.N("Disparo criado: a task foi atribuída à persona e a execução entra na fila."),
	"duplicado":         i18n.N("Já existe um disparo deste fluxo neste minuto; nada novo foi criado."),
	"ativado":           i18n.N("Fluxo ativado."),
	"desativado":        i18n.N("Fluxo desativado: não dispara na agenda até ser ativado de novo."),
	"arquivado":         i18n.N("Fluxo arquivado: saiu da lista e não dispara mais."),
	"desarquivado":      i18n.N("Fluxo desarquivado. Ele volta desativado; ative quando quiser."),
	"gatilho-ligado":    i18n.N("Gatilho ligado."),
	"gatilho-desligado": i18n.N("Gatilho desligado."),
	"gatilho-removido":  i18n.N("Gatilho removido."),
	"restaurado":        i18n.N("Revisão restaurada como uma edição nova."),
	"gatilho-criado":    i18n.N("Gatilho criado."),
}

func (s *server) flowRow(ctx context.Context, f service.FlowView, base string, now time.Time, loc *time.Location) views.FlowRow {
	fl := base + "/fluxos/" + f.Slug
	toggle := fl + "/desativar"
	if !f.Enabled {
		toggle = fl + "/ativar"
	}
	row := views.FlowRow{Name: f.Name, Href: fl, Description: f.Description, Persona: f.PersonaName, Project: f.ProjectName,
		Schedule: describeCronCtx(ctx, f.Cron), Cron: f.Cron, Enabled: f.Enabled, ToggleHref: toggle, RunHref: fl + "/executar", Last: i18n.T(ctx, "nunca"),
		Archived: f.ArchivedAt != nil, RunLink: len(f.Variables) > 0}
	if row.Schedule == "" {
		row.Schedule = f.Cron
	}
	if f.Cron == "" {
		row.Schedule = i18n.T(ctx, "Sem agenda")
	}
	if f.NextFireAt != nil {
		row.Next = fmtWhen(ctx, *f.NextFireAt, now, loc)
	}
	if f.LastFiredAt != nil {
		row.Last = agoCtx(ctx, *f.LastFiredAt, now, loc)
	}
	return row
}

func (s *server) handleFlows(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	archived := r.URL.Query().Get("arquivados") != ""
	var flows []service.FlowView
	var err error
	if archived {
		flows, err = s.be.ListFlowsFiltered(ctx, co.ID, true)
	} else {
		flows, err = s.be.ListFlows(ctx, co.ID)
	}
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc := loadLoc(co.Timezone)
	now := s.now()
	base := "/c/" + co.Slug
	vm := views.FlowsVM{NewHref: base + "/fluxos/novo", Notice: i18n.T(ctx, flowNotices[r.URL.Query().Get("ok")]), Next: "—", Last: "—",
		Subtitle: i18n.T(ctx, "Trabalho recorrente definido uma vez. Cada disparo vira uma task atribuída à persona, com sua execução."),
		Archived: archived, ViewHref: base + "/fluxos?arquivados=1", ViewLabel: i18n.T(ctx, "Arquivados")}
	if archived {
		vm.ViewHref, vm.ViewLabel = base+"/fluxos", i18n.T(ctx, "Ativos")
		vm.Subtitle = i18n.T(ctx, "Fluxos arquivados não disparam por nenhum caminho; as tasks e o histórico ficam. Abra um fluxo para desarquivá-lo.")
	}
	var next, last *time.Time
	for _, f := range flows {
		if f.Enabled {
			vm.Active++
			if f.NextFireAt != nil && (next == nil || f.NextFireAt.Before(*next)) {
				next = f.NextFireAt
			}
		} else {
			vm.Disabled++
		}
		if f.LastFiredAt != nil && (last == nil || f.LastFiredAt.After(*last)) {
			last = f.LastFiredAt
		}
		vm.Rows = append(vm.Rows, s.flowRow(ctx, f, base, now, loc))
	}
	if next != nil {
		vm.Next = fmtWhen(ctx, *next, now, loc)
	}
	if last != nil {
		vm.Last = agoCtx(ctx, *last, now, loc)
	}
	sort.SliceStable(vm.Rows, func(i, j int) bool { return vm.Rows[i].Enabled && !vm.Rows[j].Enabled })
	sh := s.shell(r, co, "fluxos", i18n.T(ctx, "Fluxos"), views.Crumb{Label: i18n.T(ctx, "Fluxos")})
	s.render(w, r, http.StatusOK, views.FlowsPage(sh, vm))
}

func (s *server) projectOptions(r *http.Request, co domain.Company, sel *domain.ID) []views.Option {
	projects, _ := s.be.ListProjects(r.Context(), co.ID) // optional
	out := []views.Option{{Value: "", Label: i18n.T(r.Context(), "Nenhum"), Selected: sel == nil}}
	for _, p := range projects {
		out = append(out, views.Option{Value: p.ID.String(), Label: p.Name, Selected: sel != nil && *sel == p.ID})
	}
	return out
}

func (s *server) blankFlowForm(r *http.Request, co domain.Company) (views.FlowFormVM, error) {
	personas, err := s.be.ListPersonas(r.Context(), co.ID)
	if err != nil {
		return views.FlowFormVM{}, err
	}
	base := "/c/" + co.Slug
	ctx := r.Context()
	vm := views.FlowFormVM{
		Action: base + "/fluxos/novo", CancelHref: base + "/fluxos", HintHref: base + "/fluxos/cron",
		Timezone: co.Timezone, Enabled: true, Cron: "0 7 * * 5", FieldErr: map[string]string{}, Presets: presetOptions(ctx),
		PersonaOptions:  personaOptions(personas, nil, i18n.T(ctx, "Escolha a persona")),
		ProjectOptions:  s.projectOptions(r, co, nil),
		PriorityOptions: priorityOptions(ctx, domain.PriorityMedium),
	}
	vm.Hint = cronHint(ctx, vm.Cron, vm.Timezone, s.now(), loadLoc(co.Timezone))
	return vm, nil
}

// presetOptions are the schedule templates of the flow form, in the language of ctx.
func presetOptions(ctx context.Context) []views.Option {
	out := make([]views.Option, len(cronPresets))
	for i, o := range cronPresets {
		o.Label = i18n.T(ctx, o.Label)
		out[i] = o
	}
	return out
}

func (s *server) handleNewFlow(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm, err := s.blankFlowForm(r, co)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderNewFlow(w, r, co, http.StatusOK, vm)
}

func (s *server) renderNewFlow(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.FlowFormVM) {
	base := "/c/" + co.Slug
	ctx := r.Context()
	sh := s.shell(r, co, "fluxos", i18n.T(ctx, "Novo fluxo"), views.Crumb{Label: i18n.T(ctx, "Fluxos"), Href: base + "/fluxos"}, views.Crumb{Label: i18n.T(ctx, "Novo")})
	s.render(w, r, status, views.NewFlowPage(sh, vm))
}

// flowFormValues reads the submitted form back into the view model (and selects the chosen options).
func (s *server) flowFormValues(r *http.Request, vm *views.FlowFormVM) {
	vm.Name = trimForm(r, "name")
	vm.Description = trimForm(r, "description")
	vm.Cron = trimForm(r, "cron")
	vm.Timezone = trimForm(r, "timezone")
	vm.TaskTitle = trimForm(r, "task_title")
	vm.TaskDescription = r.PostFormValue("task_description")
	vm.Variables = r.PostFormValue("variables")
	vm.Enabled = r.PostFormValue("enabled") != ""
	selectOption(vm.PersonaOptions, trimForm(r, "persona"))
	selectOption(vm.ProjectOptions, trimForm(r, "project"))
	selectOption(vm.PriorityOptions, trimForm(r, "priority"))
}

func (s *server) flowFormError(ctx context.Context, vm *views.FlowFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		field := ve.Field
		switch field {
		case "persona_id":
			field = "persona"
		case "timezone":
			field = "cron"
		case "task_title", "task_description":
			field = "variables" // modelo que cita uma variável não declarada: o remédio é declarar
		}
		msg := map[string]string{
			"name":    i18n.T(ctx, "Dê um nome ao fluxo."),
			"cron":    i18n.T(ctx, "Agenda inválida: use cinco campos (minuto hora dia mês dia-da-semana) e um fuso conhecido."),
			"persona": i18n.T(ctx, "Escolha a persona responsável."),
		}[field]
		if msg == "" {
			msg = ve.Field + ": " + ve.Reason + "."
		}
		vm.FieldErr[field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func parseID(v string) (*domain.ID, bool) {
	if v == "" {
		return nil, true
	}
	id, err := domain.ParseID(v)
	if err != nil {
		return nil, false
	}
	return &id, true
}

func (s *server) handleCreateFlow(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm, err := s.blankFlowForm(r, co)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.flowFormValues(r, &vm)
	vm.Hint = cronHint(r.Context(), vm.Cron, vm.Timezone, s.now(), loadLoc(co.Timezone))
	bad := func(err error) {
		s.flowFormError(r.Context(), &vm, err)
		s.renderNewFlow(w, r, co, statusFor(err), vm)
	}
	persona, ok := parseID(trimForm(r, "persona"))
	if !ok || persona == nil {
		bad(domain.Invalid("persona", i18n.T(r.Context(), "obrigatória")))
		return
	}
	project, ok := parseID(trimForm(r, "project"))
	if !ok {
		bad(domain.Invalid("project", i18n.T(r.Context(), "inválido")))
		return
	}
	enabled := vm.Enabled
	vars := parseVarLines(vm.Variables)
	f, err := s.be.CreateFlow(r.Context(), actorFrom(r.Context()), co.ID, service.CreateFlowInput{
		Name: vm.Name, Description: vm.Description, Cron: vm.Cron, Timezone: vm.Timezone, Enabled: &enabled,
		PersonaID: *persona, ProjectID: project, Priority: domain.Priority(trimForm(r, "priority")),
		TaskTitle: vm.TaskTitle, TaskDescription: vm.TaskDescription, Variables: vars,
	})
	if err != nil {
		bad(err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/fluxos/"+f.Slug+"?ok=criado")
}

func (s *server) handleCronHint(w http.ResponseWriter, r *http.Request, co domain.Company) {
	tz := r.URL.Query().Get("timezone")
	if tz == "" {
		tz = co.Timezone
	}
	s.render(w, r, http.StatusOK, views.CronHint(cronHint(r.Context(), r.URL.Query().Get("cron"), tz, s.now(), loadLoc(co.Timezone))))
}

func (s *server) buildFlowVM(r *http.Request, co domain.Company, f service.FlowView) (views.FlowVM, error) {
	ctx := r.Context()
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return views.FlowVM{}, err
	}
	fires, err := s.be.ListFlowFires(ctx, co.ID, f.ID, 30)
	if err != nil {
		return views.FlowVM{}, err
	}
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()
	row := s.flowRow(ctx, f, base, now, loc)
	vm := views.FlowVM{
		Name: f.Name, Enabled: f.Enabled, ToggleHref: row.ToggleHref + "?de=detalhe", RunHref: row.RunHref,
		Schedule: row.Schedule, Cron: f.Cron, Timezone: f.Timezone, Next: "—", Last: row.Last, Fired: len(fires),
		Notice: i18n.T(ctx, flowNotices[r.URL.Query().Get("ok")]),
	}
	if f.Enabled {
		vm.Badge = views.Badge{Label: i18n.T(ctx, "Ativo"), Class: "run"}
		vm.Next = row.Next
	} else {
		vm.Badge = views.Badge{Label: i18n.T(ctx, "Desativado"), Class: "soft"}
	}
	meta := i18n.T(ctx, "responsável") + " " + f.PersonaName
	if f.ProjectName != "" {
		meta = i18n.T(ctx, "projeto") + " " + f.ProjectName + " · " + meta
	}
	vm.Meta = meta + " · " + i18n.T(ctx, "prioridade") + " " + priorityLabel(ctx, f.Priority)
	vm.Form = views.FlowFormVM{
		Action: base + "/fluxos/" + f.Slug, Edit: true, CancelHref: base + "/fluxos", HintHref: base + "/fluxos/cron",
		Name: f.Name, Description: f.Description, Cron: f.Cron, Timezone: f.Timezone, Enabled: f.Enabled,
		TaskTitle: f.TaskTitle, TaskDescription: f.TaskDescription, Variables: formatVarLines(f.Variables), FieldErr: map[string]string{}, Presets: presetOptions(ctx),
		PersonaOptions:  personaOptions(personas, &f.PersonaID, i18n.T(ctx, "Escolha a persona")),
		ProjectOptions:  s.projectOptions(r, co, f.ProjectID),
		PriorityOptions: priorityOptions(ctx, f.Priority),
		Hint:            cronHint(ctx, f.Cron, f.Timezone, now, loc),
	}
	vm.M19 = s.flowM19VM(r, co, f, fires, base, loc, now)
	for _, fe := range fires {
		fr := views.FireRow{When: fmtDateTime(ctx, fe.ScheduledFor, loc), TaskKey: fe.TaskKey, Title: fe.TaskTitle,
			Source: fireSourceLabel(ctx, fe.Source, fe.TriggerName), Values: fireValuesText(fe.Variables)}
		if fe.TaskKey != "" {
			fr.TaskHref = base + "/tasks/" + fe.TaskKey
			st := domain.TaskStatus(fe.TaskStatus)
			fr.Badge = views.Badge{Label: taskStatusLabel(ctx, st), Class: taskBadge(ctx, service.TaskView{Task: domain.Task{Status: st}}).Class}
		}
		vm.Fires = append(vm.Fires, fr)
	}
	return vm, nil
}

func (s *server) renderFlow(w http.ResponseWriter, r *http.Request, co domain.Company, status int, f service.FlowView, vm views.FlowVM) {
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "fluxos", f.Name, views.Crumb{Label: i18n.T(r.Context(), "Fluxos"), Href: base + "/fluxos"}, views.Crumb{Label: f.Name})
	s.render(w, r, status, views.FlowPage(sh, vm))
}

func (s *server) handleFlow(w http.ResponseWriter, r *http.Request, co domain.Company) {
	f, err := s.be.GetFlow(r.Context(), co.ID, r.PathValue("flow"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.buildFlowVM(r, co, f)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderFlow(w, r, co, http.StatusOK, f, vm)
}

func (s *server) handleUpdateFlow(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	f, err := s.be.GetFlow(ctx, co.ID, r.PathValue("flow"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.buildFlowVM(r, co, f)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.flowFormValues(r, &vm.Form)
	vm.Form.Hint = cronHint(ctx, vm.Form.Cron, vm.Form.Timezone, s.now(), loadLoc(co.Timezone))
	vm.Notice = ""
	bad := func(err error) {
		s.flowFormError(ctx, &vm.Form, err)
		s.renderFlow(w, r, co, statusFor(err), f, vm)
	}
	persona, ok := parseID(trimForm(r, "persona"))
	if !ok || persona == nil {
		bad(domain.Invalid("persona", i18n.T(r.Context(), "obrigatória")))
		return
	}
	project, ok := parseID(trimForm(r, "project"))
	if !ok {
		bad(domain.Invalid("project", i18n.T(r.Context(), "inválido")))
		return
	}
	prio := domain.Priority(trimForm(r, "priority"))
	fm := vm.Form
	vars := parseVarLines(fm.Variables)
	in := service.UpdateFlowInput{Name: &fm.Name, Description: &fm.Description, Cron: &fm.Cron, Timezone: &fm.Timezone,
		TaskTitle: &fm.TaskTitle, TaskDescription: &fm.TaskDescription, Enabled: &fm.Enabled, PersonaID: persona, Priority: &prio,
		Variables: &vars}
	if project == nil {
		in.ClearProject = true
	} else {
		in.ProjectID = project
	}
	up, err := s.be.UpdateFlow(ctx, actorFrom(ctx), co.ID, f.ID, in)
	if err != nil {
		bad(err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/fluxos/"+up.Slug+"?ok=salvo")
}

// handleFlowEnabled turns a flow on or off and returns to the list (or to the flow page).
func (s *server) handleFlowEnabled(on bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		ctx := r.Context()
		f, err := s.be.GetFlow(ctx, co.ID, r.PathValue("flow"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		if f.Enabled != on {
			if _, err := s.be.UpdateFlow(ctx, actorFrom(ctx), co.ID, f.ID, service.UpdateFlowInput{Enabled: &on}); err != nil {
				s.fail(w, r, &co, err)
				return
			}
		}
		ok := "desativado"
		if on {
			ok = "ativado"
		}
		to := "/c/" + co.Slug + "/fluxos?ok=" + ok
		if r.URL.Query().Get("de") == "detalhe" {
			to = "/c/" + co.Slug + "/fluxos/" + url.PathEscape(f.Slug) + "?ok=" + ok
		}
		redirect(w, r, to)
	}
}

func (s *server) handleRunFlow(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	f, err := s.be.GetFlow(ctx, co.ID, r.PathValue("flow"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var res service.FlowFireResult
	if len(f.Variables) > 0 {
		vals := map[string]string{}
		for _, v := range f.Variables {
			vals[v.Name] = trimForm(r, "var_"+v.Name)
		}
		res, err = s.be.RunFlowNowWith(ctx, actorFrom(ctx), co.ID, f.ID, vals)
		if errors.Is(err, domain.ErrInvalid) { // variável obrigatória faltando: volta ao formulário com o que foi digitado
			vm, verr := s.buildFlowVM(r, co, f)
			if verr != nil {
				s.fail(w, r, &co, verr)
				return
			}
			for i := range vm.M19.Run.Fields {
				vm.M19.Run.Fields[i].Value = vals[vm.M19.Run.Fields[i].Name]
			}
			vm.M19.Run.Error = runErrorText(ctx, err)
			vm.Notice = ""
			s.renderFlow(w, r, co, http.StatusUnprocessableEntity, f, vm)
			return
		}
	} else {
		res, err = s.be.RunFlowNow(ctx, actorFrom(ctx), co.ID, f.ID)
	}
	if err != nil {
		s.fail(w, r, &co, fmt.Errorf("rodar fluxo: %w", err))
		return
	}
	ok := "executado"
	if res.Duplicate {
		ok = "duplicado"
	}
	redirect(w, r, "/c/"+co.Slug+"/fluxos/"+url.PathEscape(f.Slug)+"?ok="+ok)
}
