package ui

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/renatobardi/hanko/internal/domain"
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
			title += " · reporta a " + nameOf[*p.ReportsTo]
		}
		vm.Rows = append(vm.Rows, views.PersonaRow{
			Name: p.Name, Initials: initialsOf(p.Name), Title: title, Href: base + "/personas/" + p.Slug,
			Badge: personaBadge(p.Status), Agent: p.Agent, Model: p.Model, OpenTasks: open[p.ID], Heartbeat: heartbeatLabel(p.HeartbeatIntervalSec),
		})
	}
	switch len(personas) {
	case 0:
		vm.Subtitle = "Nenhuma persona ainda · qualquer harness, qualquer modelo. Se é acordada para trabalhar, está contratada."
	case 1:
		vm.Subtitle = "1 persona · qualquer harness, qualquer modelo. Se é acordada para trabalhar, está contratada."
	default:
		vm.Subtitle = fmtInt(len(personas)) + " personas · qualquer harness, qualquer modelo. Se é acordada para trabalhar, está contratada."
	}
	sh := s.shell(co, "personas", "Personas", views.Crumb{Label: "Personas"})
	sh.Action = &views.Action{Label: "Contratar persona", Href: base + "/personas/nova", Icon: "plus"}
	s.render(w, r, http.StatusOK, views.PersonasPage(sh, vm))
}

func (s *server) handleNewPersona(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderHire(w, r, co, http.StatusOK, views.HirePersonaVM{Agent: "claude", Heartbeat: "15", FieldErr: map[string]string{}})
}

func (s *server) renderHire(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.HirePersonaVM) {
	if personas, err := s.be.ListPersonas(r.Context(), co.ID); err == nil {
		var sel *domain.ID
		if id, err := domain.ParseID(trimForm(r, "reports_to")); err == nil {
			sel = &id
		}
		vm.ReportsTo = reportsOptions(personas, nil, sel)
	}
	base := "/c/" + co.Slug
	sh := s.shell(co, "personas", "Contratar persona", views.Crumb{Label: "Personas", Href: base + "/personas"}, views.Crumb{Label: "Contratar"})
	s.render(w, r, status, views.HirePersonaPage(sh, vm))
}

func (s *server) handleHirePersona(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm := views.HirePersonaVM{
		Name: trimForm(r, "name"), Title: trimForm(r, "title"), Agent: trimForm(r, "agent"), Model: trimForm(r, "model"),
		Instructions: r.PostFormValue("instructions"), Heartbeat: trimForm(r, "heartbeat"), FieldErr: map[string]string{},
		RequireReview: r.PostFormValue("require_review") != "",
	}
	var reportsTo *domain.ID
	if v := trimForm(r, "reports_to"); v != "" {
		id, err := domain.ParseID(v)
		if err != nil {
			vm.FieldErr["reports_to"] = "Superior inválido."
			vm.Error = "Superior inválido."
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
		bad("name", "Dê um nome à persona.")
		return
	}
	switch vm.Agent {
	case "claude", "codex", "fake":
	default:
		bad("agent", "Harness desconhecido: use claude, codex ou fake.")
		return
	}
	minutes := 15
	if vm.Heartbeat != "" {
		n, err := strconv.Atoi(vm.Heartbeat)
		if err != nil || n < 0 || n > 1440 {
			bad("heartbeat", "O intervalo de verificação deve ser um número de minutos entre 0 e 1440.")
			return
		}
		minutes = n
	}
	p, err := s.be.HirePersona(r.Context(), actorFrom(r.Context()), co.ID, service.HirePersonaInput{
		Name: vm.Name, Slug: domain.Slugify(vm.Name), Title: vm.Title, Instructions: vm.Instructions,
		Agent: vm.Agent, Model: vm.Model, HeartbeatIntervalSec: minutes * 60, ReportsTo: reportsTo, RequireReview: vm.RequireReview,
	})
	if err != nil {
		_, title, msg := friendly(err)
		vm.Error = title + ". " + msg
		s.renderHire(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug)
}

func (s *server) handlePersona(w http.ResponseWriter, r *http.Request, co domain.Company) {
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
		Notice: personaNotices[r.URL.Query().Get("ok")], Controls: personaControls(p, base),
		Name: p.Name, Initials: initialsOf(p.Name), Title: p.Title, Badge: personaBadge(p.Status),
		Agent: p.Agent, Model: p.Model, Heartbeat: heartbeatLabel(p.HeartbeatIntervalSec), Instructions: p.Instructions,
		Hired: fmtDateTime(p.CreatedAt, loc), Slug: p.Slug,
		RequireReview: p.RequireReview, OrgAction: base + "/personas/" + p.Slug + "/organizacao",
	}
	all, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.OrgOptions = reportsOptions(all, &p, p.ReportsTo)
	vm.SubAction = base + "/personas/" + p.Slug + "/assinatura"
	vm.SubOptions = []views.Option{{Value: "", Label: "Sem assinatura", Selected: p.SubscriptionID == nil}}
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
			vm.LastErrorAt = fmtDateTime(*health.LastErrorAt, loc)
		}
	}
	if pb, err := s.be.PersonaBudget(ctx, co.ID, p.ID); err == nil {
		b := budgetBox("Orçamento do mês", pb, base+"/personas/"+p.Slug+"/orcamento", base+"/custos")
		if pb.State == service.BudgetExceeded {
			b.StateText = "Limite atingido: a persona foi pausada, não abre execuções novas e volta sozinha quando o limite subir ou o mês virar."
		}
		vm.Budget = &b
	}
	for _, t := range tasks {
		vm.Tasks = append(vm.Tasks, taskRow(t, base, now, loc))
	}
	for _, rv := range runs {
		vm.Runs = append(vm.Runs, runRow(rv, base, now, loc))
	}
	sh := s.shell(co, "personas", p.Name, views.Crumb{Label: "Personas", Href: base + "/personas"}, views.Crumb{Label: p.Name})
	sh.Action = &views.Action{Label: "Nova task", Href: base + "/tasks/nova?persona=" + p.ID.String(), Icon: "plus"}
	s.render(w, r, http.StatusOK, views.PersonaPage(sh, vm))
}

var personaNotices = map[string]string{
	"pausada":     "Persona pausada: ela não será mais acordada.",
	"retomada":    "Persona retomada.",
	"reativada":   "Persona reativada: volta a ser acordada normalmente.",
	"organizacao": "Organização atualizada.",
	"orcamento":   "Orçamento da persona atualizado.",
	"assinatura":  "Assinatura da persona atualizada.",
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
	switch v := trimForm(r, "reports_to"); {
	case v == "":
		in.ClearReportsTo = p.ReportsTo != nil
	default:
		id, err := domain.ParseID(v)
		if err != nil {
			s.fail(w, r, &co, domain.Invalid("reporta a", "persona inválida"))
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
			s.fail(w, r, &co, domain.Invalid("assinatura", "assinatura inválida"))
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
func personaControls(p domain.Persona, base string) []views.PersonaControl {
	href := func(action string) string { return base + "/personas/" + p.Slug + "/" + action }
	switch p.Status {
	case domain.PersonaError:
		return []views.PersonaControl{{Label: "Reativar", Href: href("reativar"), Icon: "zap", Hint: "Zera as falhas e volta a acordar a persona"}}
	case domain.PersonaPaused, domain.PersonaOff:
		return []views.PersonaControl{{Label: "Retomar", Href: href("retomar"), Icon: "play", Hint: "Volta a acordar a persona"}}
	case domain.PersonaActive, domain.PersonaIdle, domain.PersonaRunning:
		return []views.PersonaControl{{Label: "Pausar", Href: href("pausar"), Icon: "alert", Hint: "Para de acordar a persona"}}
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
		if len(personaControls(p, "")) == 0 || p.Status == to {
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
