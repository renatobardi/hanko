package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

var inboxNotices = map[string]string{
	"aprovado":    gateNotices["aprovado"],
	"rejeitado":   gateNotices["rejeitado"],
	"mudancas":    gateNotices["mudancas"],
	"desbloqueou": i18n.N("Task devolvida para A fazer."),
	"acordou":     i18n.N("Persona acordada: a execução entra na fila."),
	"reativou":    i18n.N("Persona reativada: volta a ser acordada normalmente."),
}

type inboxTab struct{ key, label string }

var inboxTabs = []inboxTab{{"tudo", i18n.N("Tudo")}, {"gates", i18n.N("Gates")}, {"bloqueadas", i18n.N("Bloqueadas")}, {"falhas", i18n.N("Falhas")}, {"personas", i18n.N("Personas")}, {"alertas", i18n.N("Alertas")}, {"assinaturas", i18n.N("Assinaturas")}}

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
		Notice:       i18n.T(ctx, inboxNotices[r.URL.Query().Get("ok")]),
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
	counts := map[string]int{"tudo": in.Count(), "gates": len(in.Gates), "bloqueadas": len(in.BlockedTasks), "falhas": len(in.FailedRuns), "personas": len(in.ErrorPersonas), "alertas": len(in.Alerts), "assinaturas": len(in.GuardedSubscriptions)}
	for _, t := range inboxTabs {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: base + "/caixa?aba=" + t.key, Count: counts[t.key], On: t.key == cur})
	}
	show := func(tab string) bool { return cur == "tudo" || cur == tab }

	if show("gates") {
		vm.Gates = gateRows(ctx, in.Gates, base, now, loc, names)
	}
	if show("gates") {
		for _, rv := range in.UnderReview {
			h := rv.Agent
			if rv.Model != "" {
				h += " · " + rv.Model
			}
			vm.Reviewing = append(vm.Reviewing, views.InboxReview{Key: rv.Task.Key, Href: base + "/tasks/" + rv.Task.Key, Title: rv.Task.Title,
				Persona: rv.Task.PersonaName, Reviewer: h, Waiting: rv.Status == "pending", When: agoCtx(ctx, rv.CreatedAt, now, loc)})
		}
	}
	if show("bloqueadas") {
		for _, t := range in.BlockedTasks {
			vm.Blocked = append(vm.Blocked, views.InboxTask{
				Key: t.Key, Href: base + "/tasks/" + t.Key, Title: t.Title, Persona: t.PersonaName, When: agoCtx(ctx, t.UpdatedAt, now, loc),
				ActionHref: base + "/caixa/tasks/" + t.ID.String() + "/desbloquear", ActionLabel: i18n.T(ctx, "Voltar para A fazer"),
			})
		}
	}
	if show("falhas") {
		for _, rv := range in.FailedRuns {
			vm.Failed = append(vm.Failed, views.InboxRun{
				Name: rv.Name, Short: shortID(rv.ID), Href: base + "/execucoes/" + rv.ID.String(), TaskKey: rv.TaskKey, TaskHref: base + "/tasks/" + rv.TaskKey,
				TaskTitle: rv.TaskTitle, Persona: rv.PersonaName, Error: excerpt(rv.Error, 200), When: agoCtx(ctx, rv.CreatedAt, now, loc),
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
				p.When = agoCtx(ctx, *a.LastErrorAt, now, loc)
			}
			vm.Personas = append(vm.Personas, p)
		}
	}
	if show("alertas") {
		for _, a := range in.Alerts {
			vm.Alerts = append(vm.Alerts, alertRow(ctx, a, base, now, loc))
		}
	}
	if show("assinaturas") {
		for _, g := range in.GuardedSubscriptions {
			limit, pct := "—", ""
			if g.WeeklyLimitUSD != nil {
				limit, pct = fmtUSDCtx(ctx, *g.WeeklyLimitUSD), fmt.Sprintf("%d%%", int(math.Round(g.WeekPercent)))
			}
			vm.Subs = append(vm.Subs, views.InboxSub{Name: g.Name, Href: base + "/custos#assinaturas", Usage: fmtUSDCtx(ctx, g.WeekUsageUSD),
				Limit: limit, Percent: pct, Guard: g.WeeklyGuardPct, Week: weekLabel(ctx, g.WeekStart.In(loc))})
		}
	}
	vm.Total = len(vm.Gates) + len(vm.Blocked) + len(vm.Failed) + len(vm.Personas) + len(vm.Alerts) + len(vm.Subs)
	switch n := in.Count(); n {
	case 0:
		vm.Subtitle = i18n.T(ctx, "Gates, bloqueios, falhas, personas com erro e alertas que esperam por você. Hoje, nada.")
	case 1:
		vm.Subtitle = i18n.T(ctx, "1 item espera por você: gates para decidir, bloqueios, falhas, personas com erro e alertas.")
	default:
		vm.Subtitle = i18n.Tf(ctx, "%d itens esperam por você: gates para decidir, bloqueios, falhas, personas com erro e alertas.", n)
	}
	sh := s.shell(r, co, "caixa", i18n.T(ctx, "Caixa de entrada"), views.Crumb{Label: i18n.T(ctx, "Caixa de entrada")})
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

var alertKindText = map[domain.AlertKind]string{
	domain.AlertPersonaSilent:   i18n.N("Persona sem heartbeat"),
	domain.AlertRunStuck:        i18n.N("Execução presa"),
	domain.AlertBudgetNearLimit: i18n.N("Orçamento"),
}

// weekLabel is the first day of a subscription week: "02/01" / "Jan 2".
func weekLabel(ctx context.Context, t time.Time) string {
	if isEN(ctx) {
		return shortDate(ctx, t)
	}
	return t.Format("02/01")
}

// alertRow prepara um alerta operacional para a Caixa de entrada, com o link para o sujeito dele.
func alertRow(ctx context.Context, a domain.Alert, base string, now time.Time, loc *time.Location) views.InboxAlert {
	row := views.InboxAlert{Message: a.Message, KindText: i18n.T(ctx, alertKindText[a.Kind]), Critical: a.Severity == domain.AlertCritical,
		When: agoCtx(ctx, a.OpenedAt, now, loc)}
	var d struct {
		PersonaSlug string `json:"persona_slug"`
		ProjectSlug string `json:"project_slug"`
	}
	_ = json.Unmarshal(a.Data, &d)
	switch {
	case a.SubjectType == "run":
		row.Href, row.Link = base+"/execucoes/"+a.SubjectID.String(), i18n.T(ctx, "Ver execução")
	case a.SubjectType == "persona" && d.PersonaSlug != "":
		row.Href, row.Link = base+"/personas/"+d.PersonaSlug, i18n.T(ctx, "Abrir persona")
	case a.SubjectType == "project" && d.ProjectSlug != "":
		row.Href, row.Link = base+"/projetos/"+d.ProjectSlug, i18n.T(ctx, "Abrir projeto")
	case a.SubjectType == "company":
		row.Href, row.Link = base+"/custos", i18n.T(ctx, "Ver custos")
	}
	return row
}
