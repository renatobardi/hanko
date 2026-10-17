package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func (s *server) handlePersonas(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
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
	open := map[domain.ID]int{}
	for _, t := range tasks {
		if t.PersonaID != nil && t.Status.Open() {
			open[*t.PersonaID]++
		}
	}
	base := "/c/" + co.Slug
	vm := views.PersonasVM{}
	nameOf := map[domain.ID]string{}
	for _, p := range personas {
		nameOf[p.ID] = p.Name
	}
	for _, p := range personas {
		switch p.Status {
		case domain.PersonaActive, domain.PersonaIdle:
			vm.Active++
		case domain.PersonaRunning:
			vm.Active++
			vm.Running++
		case domain.PersonaPaused:
			vm.Paused++
		case domain.PersonaAwaitingGate:
			vm.Awaiting++
		}
		title := p.Title
		if p.ReportsTo != nil && nameOf[*p.ReportsTo] != "" {
			title += " · " + i18n.Tf(ctx, "reporta a %s", nameOf[*p.ReportsTo])
		}
		vm.Rows = append(vm.Rows, views.PersonaRow{
			Name: p.Name, Initials: initialsOf(p.Name), Title: title, Href: base + "/personas/" + p.Slug,
			Badge: personaBadge(ctx, p.Status), Agent: p.Agent, Model: p.Model, OpenTasks: open[p.ID], Heartbeat: heartbeatLabelCtx(ctx, p.HeartbeatIntervalSec),
		})
	}
	switch len(personas) {
	case 0:
		vm.Subtitle = i18n.T(ctx, "Nenhuma persona ainda · qualquer harness, qualquer modelo. Se é acordada para trabalhar, está contratada.")
	case 1:
		vm.Subtitle = i18n.T(ctx, "1 persona · qualquer harness, qualquer modelo. Se é acordada para trabalhar, está contratada.")
	default:
		vm.Subtitle = i18n.Tf(ctx, "%d personas · qualquer harness, qualquer modelo. Se é acordada para trabalhar, está contratada.", len(personas))
	}
	sh := s.shell(r, co, "personas", i18n.T(ctx, "Personas"), views.Crumb{Label: i18n.T(ctx, "Personas")})
	if sh.CanAdmin() {
		sh.Action = &views.Action{Label: i18n.T(ctx, "Contratar persona"), Href: base + "/personas/nova", Icon: "plus"}
	}
	s.render(w, r, http.StatusOK, views.PersonasPage(sh, vm))
}

func (s *server) handleNewPersona(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderHire(w, r, co, http.StatusOK, views.HirePersonaVM{Agent: "claude", Heartbeat: "15", FieldErr: map[string]string{}})
}

func (s *server) renderHire(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.HirePersonaVM) {
	vm.AcpPermission = acpPermissionOptions(r.Context(), vm.AcpPermission)
	if personas, err := s.be.ListPersonas(r.Context(), co.ID); err == nil {
		var sel *domain.ID
		if id, err := domain.ParseID(trimForm(r, "reports_to")); err == nil {
			sel = &id
		}
		vm.ReportsTo = reportsOptions(r.Context(), personas, nil, sel)
	}
	base := "/c/" + co.Slug
	ctx := r.Context()
	sh := s.shell(r, co, "personas", i18n.T(ctx, "Contratar persona"), views.Crumb{Label: i18n.T(ctx, "Personas"), Href: base + "/personas"}, views.Crumb{Label: i18n.T(ctx, "Contratar")})
	s.render(w, r, status, views.HirePersonaPage(sh, vm))
}

func (s *server) handleHirePersona(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm := views.HirePersonaVM{
		Name: trimForm(r, "name"), Title: trimForm(r, "title"), Agent: trimForm(r, "agent"), Model: trimForm(r, "model"),
		Instructions: r.PostFormValue("instructions"), Heartbeat: trimForm(r, "heartbeat"), FieldErr: map[string]string{},
		RequireReview: r.PostFormValue("require_review") != "", Icon: trimForm(r, "icon"),
		AcpCommand: trimForm(r, "acp_command"), AcpArgs: r.PostFormValue("acp_args"),
		AcpPermission: []views.Option{{Value: trimForm(r, "acp_permission"), Selected: true}},
	}
	ctx := r.Context()
	var reportsTo *domain.ID
	if v := trimForm(r, "reports_to"); v != "" {
		id, err := domain.ParseID(v)
		if err != nil {
			vm.FieldErr["reports_to"] = i18n.T(ctx, "Superior inválido.")
			vm.Error = i18n.T(ctx, "Superior inválido.")
			s.renderHire(w, r, co, http.StatusUnprocessableEntity, vm)
			return
		}
		reportsTo = &id
	}
	if vm.Agent == "" {
		vm.Agent = "claude"
	}
	bad := func(field, msg string) {
		vm.FieldErr[field] = msg
		vm.Error = msg
		s.renderHire(w, r, co, http.StatusUnprocessableEntity, vm)
	}
	if vm.Name == "" {
		bad("name", i18n.T(ctx, "Dê um nome à persona."))
		return
	}
	if vm.Icon == "" {
		vm.Icon = domain.DefaultPersonaIcon
	}
	if !domain.ValidPersonaIcon(vm.Icon) {
		bad("icon", i18n.T(ctx, "Escolha um dos ícones da lista."))
		return
	}
	if !domain.ValidAgent(vm.Agent) {
		bad("agent", i18n.T(ctx, "Harness desconhecido: use claude, codex, acp ou fake."))
		return
	}
	var adapterCfg json.RawMessage
	if vm.Agent == "acp" {
		switch {
		case vm.AcpCommand == "":
			bad("acp_command", i18n.T(ctx, "Informe o comando do harness ACP."))
			return
		case strings.ContainsAny(vm.AcpCommand, " \t") && !strings.HasPrefix(vm.AcpCommand, "/"):
			bad("acp_command", i18n.T(ctx, "O comando deve ser só o executável; ponha os argumentos no campo de argumentos."))
			return
		}
		cfg := map[string]any{"command": vm.AcpCommand}
		if p := selectedValue(vm.AcpPermission); p != "" {
			cfg["permission"] = p
		}
		if args := splitLines(vm.AcpArgs); len(args) > 0 {
			cfg["args"] = args
		}
		b, err := json.Marshal(cfg)
		if err != nil {
			bad("acp_command", i18n.T(ctx, "Informe o comando do harness ACP."))
			return
		}
		adapterCfg = b
	}
	minutes := 15
	if vm.Heartbeat != "" {
		n, err := strconv.Atoi(vm.Heartbeat)
		if err != nil || n < 0 || n > 1440 {
			bad("heartbeat", i18n.T(ctx, "O intervalo de verificação deve ser um número de minutos entre 0 e 1440."))
			return
		}
		minutes = n
	}
	p, err := s.be.HirePersona(r.Context(), actorFrom(r.Context()), co.ID, service.HirePersonaInput{
		Name: vm.Name, Slug: domain.Slugify(vm.Name), Title: vm.Title, Icon: vm.Icon, Instructions: vm.Instructions,
		Agent: vm.Agent, Model: vm.Model, AdapterConfig: adapterCfg, HeartbeatIntervalSec: minutes * 60, ReportsTo: reportsTo, RequireReview: vm.RequireReview,
	})
	if err != nil {
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
		var ve *domain.ValidationError
		if errors.As(err, &ve) && ve.Field == "adapter_config" && strings.Contains(ve.Reason, "HANKO_ACP_") {
			// the operator's gate (ADR-0023): say who decides, in the reader's language
			vm.Error = i18n.T(ctx, "O harness ACP não está liberado neste servidor para este comando: quem opera a instalação define HANKO_ACP_ALLOW (e HANKO_ACP_COMMANDS, se houver lista).")
		}
		s.renderHire(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug)
}

func (s *server) handlePersona(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderPersona(w, r, co, http.StatusOK, nil)
}

// renderPersona draws the persona page. A non-nil rv is a reviewer form that failed validation:
// it replaces the saved values so the person sees what they typed next to the error.
func (s *server) renderPersona(w http.ResponseWriter, r *http.Request, co domain.Company, status int, rv *views.ReviewerVM) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	tasks, err := s.be.ListTasks(ctx, co.ID, service.TaskFilter{PersonaID: &p.ID, Limit: 100})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	runs, err := s.be.ListRuns(ctx, co.ID, service.RunFilter{PersonaID: &p.ID, Limit: 20})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()
	health, err := s.be.GetPersonaHealth(ctx, co.ID, p.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := views.PersonaVM{
		Notice: i18n.T(ctx, personaNotices[r.URL.Query().Get("ok")]), Controls: personaControls(ctx, p, base),
		Name: p.Name, Initials: initialsOf(p.Name), Title: p.Title, Badge: personaBadge(ctx, p.Status),
		Agent: p.Agent, Model: p.Model, Heartbeat: heartbeatLabelCtx(ctx, p.HeartbeatIntervalSec), Instructions: p.Instructions,
		Hired: fmtDateTime(ctx, p.CreatedAt, loc), Slug: p.Slug,
		RequireReview: p.RequireReview, OrgAction: base + "/personas/" + p.Slug + "/organizacao",
		Icon: p.Icon, IconAction: base + "/personas/" + p.Slug + "/icone",
	}
	all, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.OrgOptions = reportsOptions(ctx, all, &p, p.ReportsTo)
	vm.Reviewer = reviewerVM(ctx, all, p, base)
	if rv != nil {
		vm.Reviewer.Mode, vm.Reviewer.Error = rv.Mode, rv.Error
		vm.Reviewer.Model = rv.Model
		vm.Reviewer.Persona = reviewerPersonaOptions(ctx, all, p.ID, rv.Persona)
		vm.Reviewer.Agent = reviewerAgentOptions(ctx, rv.Agent)
	}
	vm.Extras = s.personaExtras(ctx, co, p)
	vm.SubAction = base + "/personas/" + p.Slug + "/assinatura"
	vm.SubOptions = []views.Option{{Value: "", Label: i18n.T(ctx, "Sem assinatura"), Selected: p.SubscriptionID == nil}}
	if subs, err := s.be.ListSubscriptions(ctx, co.ID); err == nil {
		for _, sub := range subs {
			isCur := p.SubscriptionID != nil && *p.SubscriptionID == sub.ID
			if sub.Active || isCur {
				vm.SubOptions = append(vm.SubOptions, views.Option{Value: sub.ID.String(), Label: sub.Name + " · " + sub.Agent, Selected: isCur})
			}
			if isCur {
				vm.Subscription = sub.Name
			}
		}
	}
	for _, o := range all {
		if p.ReportsTo != nil && o.ID == *p.ReportsTo {
			vm.ReportsTo, vm.ReportsHref = o.Name, base+"/personas/"+o.Slug
		}
		if o.ReportsTo != nil && *o.ReportsTo == p.ID {
			vm.Reports = append(vm.Reports, views.PersonaRef{Name: o.Name, Href: base + "/personas/" + o.Slug})
		}
	}
	// The last error stays visible while the persona is failing (in error, or with failures counted).
	if health.LastError != "" && (p.Status == domain.PersonaError || health.ConsecutiveFailures > 0) {
		vm.LastError, vm.Failures = health.LastError, health.ConsecutiveFailures
		if health.LastErrorAt != nil {
			vm.LastErrorAt = fmtDateTime(ctx, *health.LastErrorAt, loc)
		}
	}
	if pb, err := s.be.PersonaBudget(ctx, co.ID, p.ID); err == nil {
		b := budgetBox(ctx, i18n.T(ctx, "Orçamento do mês"), pb, base+"/personas/"+p.Slug+"/orcamento", base+"/custos")
		if pb.State == service.BudgetExceeded {
			b.StateText = i18n.T(ctx, "Limite atingido: a persona foi pausada, não abre execuções novas e volta sozinha quando o limite subir ou o mês virar.")
		}
		vm.Budget = &b
	}
	for _, t := range tasks {
		vm.Tasks = append(vm.Tasks, taskRow(ctx, t, base, now, loc))
	}
	for _, rv := range runs {
		vm.Runs = append(vm.Runs, runRow(ctx, rv, base, now, loc))
	}
	chats, err := s.be.ListConversations(ctx, co.ID, service.ConversationFilter{PersonaID: &p.ID, Kind: domain.ConversationDirect, Limit: 10})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Chats = chatRows(ctx, chats, base, now, loc)
	rooms, err := s.be.ListConversations(ctx, co.ID, service.ConversationFilter{PersonaID: &p.ID, Kind: domain.ConversationRoom, Limit: 10})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Rooms = roomRows(ctx, rooms, base, now, loc) // sala (ADR-0033)
	if views.RoleFrom(ctx).AtLeast(domain.MemberMember) {
		vm.NewChatHref = base + "/chat?persona=" + p.Slug
	}
	sh := s.shell(r, co, "personas", p.Name, views.Crumb{Label: i18n.T(ctx, "Personas"), Href: base + "/personas"}, views.Crumb{Label: p.Name})
	if sh.CanWrite() {
		sh.Action = &views.Action{Label: i18n.T(ctx, "Nova task"), Href: base + "/tasks/nova?persona=" + p.ID.String(), Icon: "plus"}
	}
	s.render(w, r, status, views.PersonaPage(sh, vm))
}

var personaNotices = map[string]string{
	"icone":       i18n.N("Ícone atualizado."),
	"pausada":     i18n.N("Persona pausada: ela não será mais acordada."),
	"retomada":    i18n.N("Persona retomada."),
	"reativada":   i18n.N("Persona reativada: volta a ser acordada normalmente."),
	"organizacao": i18n.N("Organização atualizada."),
	"revisor":     i18n.N("Revisor atualizado."),
	"orcamento":   i18n.N("Orçamento da persona atualizado."),
	"assinatura":  i18n.N("Assinatura da persona atualizada."),
}

// handlePersonaOrg changes who the persona reports to and whether its work needs review.
func (s *server) handlePersonaOrg(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var in service.UpdatePersonaInput
	switch v := trimForm(r, "reports_to"); v {
	case "":
		in.ClearReportsTo = p.ReportsTo != nil
	default:
		id, err := domain.ParseID(v)
		if err != nil {
			s.fail(w, r, &co, domain.Invalid(i18n.T(ctx, "reporta a"), i18n.T(ctx, "persona inválida")))
			return
		}
		if p.ReportsTo == nil || *p.ReportsTo != id {
			in.ReportsTo = &id
		}
	}
	if rr := r.PostFormValue("require_review") != ""; rr != p.RequireReview {
		in.RequireReview = &rr
	}
	if in.ReportsTo != nil || in.ClearReportsTo || in.RequireReview != nil {
		if _, err := s.be.UpdatePersona(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
			s.fail(w, r, &co, err)
			return
		}
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=organizacao")
}

// handlePersonaIcon troca o glifo da persona (catálogo fechado em domain.PersonaIcons).
func (s *server) handlePersonaIcon(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	icon := trimForm(r, "icon")
	if !domain.ValidPersonaIcon(icon) {
		s.fail(w, r, &co, domain.Invalid("icon", i18n.T(ctx, "Escolha um dos ícones da lista.")))
		return
	}
	if _, err := s.be.UpdatePersona(ctx, actorFrom(ctx), co.ID, p.ID, service.UpdatePersonaInput{Icon: &icon}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=icone")
}

// handlePersonaSubscription links the persona to a subscription (or unlinks it).
func (s *server) handlePersonaSubscription(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var in service.UpdatePersonaInput
	if v := trimForm(r, "subscription"); v == "" {
		in.ClearSubscription = true
	} else {
		id, err := domain.ParseID(v)
		if err != nil {
			s.fail(w, r, &co, domain.Invalid(i18n.T(ctx, "assinatura"), i18n.T(ctx, "assinatura inválida")))
			return
		}
		in.SubscriptionID = &id
	}
	if _, err := s.be.UpdatePersona(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=assinatura")
}

// personaControls lists the actions that make sense for the persona's current status.
func personaControls(ctx context.Context, p domain.Persona, base string) []views.PersonaControl {
	href := func(action string) string { return base + "/personas/" + p.Slug + "/" + action }
	switch p.Status {
	case domain.PersonaError:
		return []views.PersonaControl{{Label: i18n.T(ctx, "Reativar"), Href: href("reativar"), Icon: "zap", Hint: i18n.T(ctx, "Zera as falhas e volta a acordar a persona")}}
	case domain.PersonaPaused, domain.PersonaOff:
		return []views.PersonaControl{{Label: i18n.T(ctx, "Retomar"), Href: href("retomar"), Icon: "play", Hint: i18n.T(ctx, "Volta a acordar a persona")}}
	case domain.PersonaActive, domain.PersonaIdle, domain.PersonaRunning:
		return []views.PersonaControl{{Label: i18n.T(ctx, "Pausar"), Href: href("pausar"), Icon: "alert", Hint: i18n.T(ctx, "Para de acordar a persona")}}
	}
	return nil
}

// handlePersonaControl moves the persona to a status chosen by the route (pause, resume,
// reactivate) and returns to its page.
func (s *server) handlePersonaControl(to domain.PersonaStatus, notice string) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		ctx := r.Context()
		p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		if len(personaControls(ctx, p, "")) == 0 || p.Status == to {
			s.fail(w, r, &co, fmt.Errorf("persona %q já está %s: %w", p.Name, p.Status, domain.ErrConflict))
			return
		}
		if _, err := s.be.UpdatePersona(ctx, actorFrom(ctx), co.ID, p.ID, service.UpdatePersonaInput{Status: &to}); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok="+notice)
	}
}

// reviewerVM is the reviewer block as saved: the summary line and the form pre-filled with it.
func reviewerVM(ctx context.Context, all []domain.Persona, p domain.Persona, base string) views.ReviewerVM {
	rv := views.ReviewerVM{Action: base + "/personas/" + p.Slug + "/revisor", Mode: "none", Model: p.ReviewerModel}
	var sel *domain.ID
	switch {
	case p.ReviewerPersonaID != nil:
		rv.Mode, sel = "persona", p.ReviewerPersonaID
		for _, o := range all {
			if o.ID == *p.ReviewerPersonaID {
				rv.Summary, rv.Href = o.Name, base+"/personas/"+o.Slug
			}
		}
	case p.ReviewerAgent != "":
		rv.Mode = "harness"
		rv.Summary = p.ReviewerAgent
		if p.ReviewerModel != "" {
			rv.Summary += " · " + p.ReviewerModel
		}
		rv.Summary += " (" + i18n.T(ctx, "mesmo papel") + ")"
	}
	rv.Persona = reviewerPersonaOptions(ctx, all, p.ID, sel)
	rv.Agent = reviewerAgentOptions(ctx, p.ReviewerAgent)
	return rv
}

func reviewerPersonaOptions(ctx context.Context, all []domain.Persona, self domain.ID, sel any) []views.Option {
	out := []views.Option{{Value: "", Label: i18n.T(ctx, "Escolha uma persona")}}
	var selID string
	switch v := sel.(type) {
	case *domain.ID:
		if v != nil {
			selID = v.String()
		}
	case []views.Option:
		for _, o := range v {
			if o.Selected {
				selID = o.Value
			}
		}
	}
	for _, o := range all {
		if o.ID == self {
			continue
		}
		out = append(out, views.Option{Value: o.ID.String(), Label: o.Name, Selected: o.ID.String() == selID})
	}
	return out
}

func reviewerAgentOptions(ctx context.Context, sel any) []views.Option {
	var selAgent string
	switch v := sel.(type) {
	case string:
		selAgent = v
	case []views.Option:
		for _, o := range v {
			if o.Selected {
				selAgent = o.Value
			}
		}
	}
	out := []views.Option{{Value: "", Label: i18n.T(ctx, "Escolha um harness"), Selected: selAgent == ""}}
	for _, a := range domain.ReviewerAgents {
		out = append(out, views.Option{Value: a, Label: a, Selected: a == selAgent})
	}
	return out
}

// handlePersonaReviewer saves the reviewer of the persona from the form: no reviewer, another
// persona, or the same role on another harness/model. Form-level mistakes get a message in the
// language of the screen; the rules that need data (self review, same harness as the author,
// unknown persona) are the service's and come back as its validation error, on the page.
func (s *server) handlePersonaReviewer(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	mode := trimForm(r, "mode")
	rv := views.ReviewerVM{Mode: mode, Model: trimForm(r, "reviewer_model")}
	agent := trimForm(r, "reviewer_agent")
	var rvID *domain.ID
	if v := trimForm(r, "reviewer_persona"); v != "" {
		if id, err := domain.ParseID(v); err == nil {
			rvID = &id
		}
	}
	// What the person typed goes back into the form on error.
	rv.Persona = []views.Option{{Value: trimForm(r, "reviewer_persona"), Selected: true}}
	rv.Agent = []views.Option{{Value: agent, Selected: true}}
	bad := func(st int, msg string) {
		rv.Error = msg
		s.renderPersona(w, r, co, st, &rv)
	}
	var in service.UpdatePersonaInput
	switch mode {
	case "none":
		if !domain.HasReviewer(p.ReviewerPersonaID, p.ReviewerAgent) {
			redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=revisor")
			return
		}
		in.ClearReviewer = true
	case "persona":
		if rvID == nil {
			bad(http.StatusUnprocessableEntity, i18n.T(ctx, "Escolha a persona revisora."))
			return
		}
		in.ReviewerPersona = rvID
	case "harness":
		switch {
		case agent == "":
			bad(http.StatusUnprocessableEntity, i18n.T(ctx, "Escolha o harness do revisor."))
			return
		case agent == "acp":
			bad(http.StatusUnprocessableEntity, i18n.T(ctx, "O ACP não pode ser o harness alternativo do revisor; use uma persona revisora que rode em ACP."))
			return
		case !domain.ValidReviewerAgent(agent):
			bad(http.StatusUnprocessableEntity, i18n.T(ctx, "Harness desconhecido: use claude, codex ou fake."))
			return
		}
		in.ReviewerAgent, in.ReviewerModel = &agent, &rv.Model
	default:
		bad(http.StatusUnprocessableEntity, i18n.T(ctx, "Escolha como a revisão é feita."))
		return
	}
	if _, err := s.be.UpdatePersona(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		st, title, msg := friendly(ctx, err)
		if st >= 500 {
			s.fail(w, r, &co, err)
			return
		}
		bad(st, title+". "+msg)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=revisor")
}

// acpPermissionOptions lists the permission policies of the ACP harness; sel is the option
// chosen so far (the default policy, read_only, when none).
func acpPermissionOptions(ctx context.Context, sel []views.Option) []views.Option {
	cur := selectedValue(sel)
	if cur == "" {
		cur = "read_only"
	}
	return []views.Option{
		{Value: "read_only", Label: i18n.T(ctx, "Somente leitura (padrão)"), Selected: cur == "read_only"},
		{Value: "allow", Label: i18n.T(ctx, "Permitir tudo"), Selected: cur == "allow"},
		{Value: "reject", Label: i18n.T(ctx, "Recusar tudo"), Selected: cur == "reject"},
	}
}

func selectedValue(opts []views.Option) string {
	for _, o := range opts {
		if o.Selected {
			return o.Value
		}
	}
	return ""
}

// splitLines returns the non-empty trimmed lines of s.
func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
