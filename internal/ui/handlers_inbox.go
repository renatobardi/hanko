package ui

import (
	"net/http"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

var inboxNotices = map[string]string{
	"aprovado":    gateNotices["aprovado"],
	"rejeitado":   gateNotices["rejeitado"],
	"mudancas":    gateNotices["mudancas"],
	"desbloqueou": "Task devolvida para A fazer.",
	"acordou":     "Persona acordada: a execução entra na fila.",
	"reativou":    "Persona reativada: volta a ser acordada normalmente.",
}

type inboxTab struct{ key, label string }

var inboxTabs = []inboxTab{{"tudo", "Tudo"}, {"gates", "Gates"}, {"bloqueadas", "Bloqueadas"}, {"falhas", "Falhas"}, {"personas", "Personas"}}

func (s *server) handleInbox(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	in, err := s.be.Inbox(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	decided, err := s.be.ListGates(ctx, co.ID, service.GateFilter{
		Statuses: []domain.GateStatus{domain.GateApproved, domain.GateRejected, domain.GateChangesRequested}, Limit: 200})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	names := s.lookup(r.Context(), personas, nil)
	base := "/c/" + co.Slug
	loc, now := loadLoc(co.Timezone), s.now()

	cur := "tudo"
	for _, t := range inboxTabs {
		if t.key == r.URL.Query().Get("aba") {
			cur = t.key
		}
	}
	vm := views.InboxVM{
		Notice:       inboxNotices[r.URL.Query().Get("ok")],
		Pending:      len(in.Gates),
		DecisionsURL: base + "/decisoes",
		GatesURL:     base + "/gates",
	}
	for _, g := range in.Gates {
		if now.Sub(g.CreatedAt) > gateAged {
			vm.Aged++
		}
	}
	for _, g := range decided {
		if g.DecidedAt != nil && sameDay(*g.DecidedAt, now, loc) {
			vm.DecidedToday++
		}
	}
	counts := map[string]int{"tudo": in.Count(), "gates": len(in.Gates), "bloqueadas": len(in.BlockedTasks), "falhas": len(in.FailedRuns), "personas": len(in.ErrorPersonas)}
	for _, t := range inboxTabs {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: t.label, Href: base + "/caixa?aba=" + t.key, Count: counts[t.key], On: t.key == cur})
	}
	show := func(tab string) bool { return cur == "tudo" || cur == tab }

	if show("gates") {
		vm.Gates = gateRows(in.Gates, base, now, loc, names)
	}
	if show("bloqueadas") {
		for _, t := range in.BlockedTasks {
			vm.Blocked = append(vm.Blocked, views.InboxTask{
				Key: t.Key, Href: base + "/tasks/" + t.Key, Title: t.Title, Persona: t.PersonaName, When: ago(t.UpdatedAt, now, loc),
				ActionHref: base + "/caixa/tasks/" + t.ID.String() + "/desbloquear", ActionLabel: "Voltar para A fazer",
			})
		}
	}
	if show("falhas") {
		for _, rv := range in.FailedRuns {
			vm.Failed = append(vm.Failed, views.InboxRun{
				Short: shortID(rv.ID), Href: base + "/execucoes/" + rv.ID.String(), TaskKey: rv.TaskKey, TaskHref: base + "/tasks/" + rv.TaskKey,
				TaskTitle: rv.TaskTitle, Persona: rv.PersonaName, Error: excerpt(rv.Error, 200), When: ago(rv.CreatedAt, now, loc),
				ActionHref: base + "/caixa/tasks/" + rv.TaskID.String() + "/acordar",
			})
		}
	}
	if show("personas") {
		for _, a := range in.ErrorPersonas {
			p := views.InboxPersona{
				Name: a.Persona.Name, Href: base + "/personas/" + a.Persona.Slug, Title: a.Persona.Title, Error: excerpt(a.LastError, 200),
				Failures: a.ConsecutiveFailures, ActionHref: base + "/caixa/personas/" + a.Persona.Slug + "/reativar",
			}
			if a.LastErrorAt != nil {
				p.When = ago(*a.LastErrorAt, now, loc)
			}
			vm.Personas = append(vm.Personas, p)
		}
	}
	vm.Total = len(vm.Gates) + len(vm.Blocked) + len(vm.Failed) + len(vm.Personas)
	switch n := in.Count(); n {
	case 0:
		vm.Subtitle = "Gates, bloqueios, falhas e personas com erro que esperam por você. Hoje, nada."
	case 1:
		vm.Subtitle = "1 item espera por você: gates para decidir, bloqueios, falhas e personas com erro."
	default:
		vm.Subtitle = fmtInt(n) + " itens esperam por você: gates para decidir, bloqueios, falhas e personas com erro."
	}
	sh := s.shell(co, "caixa", "Caixa de entrada", views.Crumb{Label: "Caixa de entrada"})
	s.render(w, r, http.StatusOK, views.InboxPage(sh, vm))
}

func (s *server) inboxDone(w http.ResponseWriter, r *http.Request, co domain.Company, ok string) {
	redirect(w, r, "/c/"+co.Slug+"/caixa?ok="+ok)
}

// handleInboxUnblock sends a blocked task without a gate back to "A fazer".
func (s *server) handleInboxUnblock(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	st := domain.TaskTodo
	if _, err := s.be.UpdateTask(r.Context(), actorFrom(r.Context()), co.ID, id, service.UpdateTaskInput{Status: &st}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.inboxDone(w, r, co, "desbloqueou")
}

// handleInboxWake wakes the persona of a task whose run failed.
func (s *server) handleInboxWake(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.be.WakeTask(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.inboxDone(w, r, co, "acordou")
}

// handleInboxReactivate puts a persona in error back to active.
func (s *server) handleInboxReactivate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	p, err := s.be.GetPersona(r.Context(), co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if p.Status != domain.PersonaError && p.Status != domain.PersonaPaused {
		s.fail(w, r, &co, domain.ErrConflict)
		return
	}
	to := domain.PersonaActive
	if _, err := s.be.UpdatePersona(r.Context(), actorFrom(r.Context()), co.ID, p.ID, service.UpdatePersonaInput{Status: &to}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.inboxDone(w, r, co, "reativou")
}
