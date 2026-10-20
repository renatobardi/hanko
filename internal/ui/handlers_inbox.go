package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
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

var inboxTabs = []inboxTab{{"tudo", i18n.N("Tudo")}, {"gates", i18n.N("Gates")}, {"bloqueadas", i18n.N("Bloqueadas")}, {"falhas", i18n.N("Falhas")}, {"pausadas", i18n.N("Pausadas")}, {"personas", i18n.N("Personas")}, {"alertas", i18n.N("Alertas")}, {"assinaturas", i18n.N("Assinaturas")}, {"tools", i18n.N("Tools")}}

// Abas por pessoa (ADR-0042): as visões Não lidos e Arquivados da Caixa.
const (
	tabUnread   = "nao-lidos"
	tabArchived = "arquivados"
)

// inboxBackend é o que a Caixa por pessoa pede ao service (ADR-0042); o Backend de teste pode não ter.
type inboxBackend interface {
	InboxView(ctx context.Context, companyID domain.ID, view domain.InboxView) (service.Inbox, error)
	SetInboxState(ctx context.Context, actor domain.Actor, companyID domain.ID, action domain.InboxAction, refs []service.InboxRef) (int, error)
}

func (s *server) inboxView(ctx context.Context, companyID domain.ID, view domain.InboxView) (service.Inbox, error) {
	if b, ok := s.be.(inboxBackend); ok {
		return b.InboxView(ctx, companyID, view)
	}
	return s.be.Inbox(ctx, companyID)
}

func (s *server) handleInbox(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cur := "tudo"
	for _, t := range append(inboxTabs, inboxTab{tabUnread, ""}, inboxTab{tabArchived, ""}) {
		if t.key == r.URL.Query().Get("aba") {
			cur = t.key
		}
	}
	view := domain.InboxViewAll
	switch cur {
	case tabUnread:
		view = domain.InboxViewUnread
	case tabArchived:
		view = domain.InboxViewArchived
	}
	in, err := s.inboxView(ctx, co.ID, view)
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

	vm := views.InboxVM{
		Notice:       i18n.T(ctx, inboxNotices[r.URL.Query().Get("ok")]),
		Pending:      len(in.Gates),
		DecisionsURL: base + "/decisoes",
		GatesURL:     base + "/gates",
		StateURL:     base + "/caixa/estado",
		CanMarkAll:   in.Personal && in.Unread > 0,
	}
	// ctl são os controles de lido/arquivado de um item (só com pessoa autenticada)
	ctl := func(kind domain.InboxItemKind, id domain.ID) views.InboxCtl {
		if !in.Personal {
			return views.InboxCtl{}
		}
		st := in.StateOf(kind, id)
		return views.InboxCtl{Show: true, Ref: string(kind) + ":" + id.String(), Unread: !st.Read && !st.Archived, Archived: st.Archived, Action: base + "/caixa/estado"}
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
	counts := map[string]int{"tudo": in.Count(), "gates": len(in.Gates), "bloqueadas": len(in.BlockedTasks), "pausadas": len(in.HeldTasks), "falhas": len(in.FailedRuns) + len(in.StoppedRuns), "personas": len(in.ErrorPersonas), "alertas": len(in.Alerts), "assinaturas": len(in.GuardedSubscriptions), "tools": countToolGates(in.Gates) + len(in.QuarantinedTools)}
	if view != domain.InboxViewAll { // fora da visão padrão os números por seção não valem como contagem das abas
		counts = map[string]int{}
	}
	for _, t := range inboxTabs {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: base + "/caixa?aba=" + t.key, Count: counts[t.key], On: t.key == cur})
	}
	if tab, ok := s.reviewInboxTab(ctx, co); ok { // fila de revisão dos pipelines (ADR-0034)
		vm.Tabs = append(vm.Tabs, tab)
	}
	if in.Personal { // visões por pessoa (ADR-0042)
		vm.Tabs = append(vm.Tabs,
			views.TabLink{Key: tabUnread, Label: i18n.T(ctx, "Não lidos"), Href: base + "/caixa?aba=" + tabUnread, Count: in.Unread, On: cur == tabUnread},
			views.TabLink{Key: tabArchived, Label: i18n.T(ctx, "Arquivados"), Href: base + "/caixa?aba=" + tabArchived, Count: in.ArchivedCount, On: cur == tabArchived})
	}
	show := func(tab string) bool { return cur == "tudo" || cur == tabUnread || cur == tabArchived || cur == tab }

	if show("gates") || cur == "tools" {
		vm.Gates = gateRows(ctx, inboxGates(cur, in.Gates), base, now, loc, names)
		for i := range vm.Gates {
			if id, err := domain.ParseID(vm.Gates[i].ID); err == nil {
				vm.Gates[i].Ctl = ctl(domain.InboxGate, id)
			}
		}
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
				Ctl: ctl(domain.InboxTaskBlocked, t.ID),
			})
		}
	}
	if show("pausadas") {
		for _, h := range in.HeldTasks {
			vm.Held = append(vm.Held, views.InboxHeld{Key: h.Task.Key, Href: base + "/tasks/" + h.Task.Key, Title: h.Task.Title, Persona: h.Task.PersonaName,
				Reason: h.Hold.Reason, By: names.actorName(h.Hold.By), When: agoCtx(ctx, h.Hold.At, now, loc),
				ResumeHref: base + "/caixa/tasks/" + h.Task.ID.String() + "/retomar", Ctl: ctl(domain.InboxTaskHeld, h.Task.ID)})
		}
	}
	if show("falhas") {
		for _, rv := range in.FailedRuns {
			row := views.InboxRun{
				Name: rv.Name, Short: shortID(rv.ID), Href: base + "/execucoes/" + rv.ID.String(), TaskKey: rv.TaskKey, TaskHref: base + "/tasks/" + rv.TaskKey,
				TaskTitle: rv.TaskTitle, Persona: rv.PersonaName, Error: excerpt(rv.Error, 200), When: agoCtx(ctx, rv.CreatedAt, now, loc),
				Ctl: ctl(domain.InboxRunFailed, rv.ID),
			}
			row.Diag = inboxRunDiag(ctx, co, rv)
			if rv.ConversationID != nil && rv.TaskID == (domain.ID{}) {
				// run de chat: sem task, o título e o link são da conversa; a retomada é a próxima mensagem humana
				row.TaskKey, row.TaskHref, row.TaskTitle = i18n.T(ctx, "Chat"), base+"/chat/"+rv.ConversationID.String(), rv.ConversationTitle
			} else {
				row.ActionHref = base + "/caixa/tasks/" + rv.TaskID.String() + "/acordar"
			}
			vm.Failed = append(vm.Failed, row)
		}
	}
	if show("falhas") {
		for _, rv := range in.StoppedRuns {
			row := views.InboxRun{
				Name: rv.Name, Short: shortID(rv.ID), Href: base + "/execucoes/" + rv.ID.String(), TaskKey: rv.TaskKey, TaskHref: base + "/tasks/" + rv.TaskKey,
				TaskTitle: rv.TaskTitle, Persona: rv.PersonaName, When: agoCtx(ctx, rv.CreatedAt, now, loc), Diag: inboxRunDiag(ctx, co, rv),
			}
			if rv.ConversationID != nil && rv.TaskID == (domain.ID{}) {
				row.TaskKey, row.TaskHref, row.TaskTitle = i18n.T(ctx, "Chat"), base+"/chat/"+rv.ConversationID.String(), rv.ConversationTitle
			}
			vm.Stopped = append(vm.Stopped, row)
		}
	}
	if show("personas") {
		for _, a := range in.ErrorPersonas {
			p := views.InboxPersona{
				Name: a.Persona.Name, Href: base + "/personas/" + a.Persona.Slug, Title: a.Persona.Title, Error: excerpt(a.LastError, 200),
				Failures: a.ConsecutiveFailures, ActionHref: base + "/caixa/personas/" + a.Persona.Slug + "/reativar",
				Ctl: ctl(domain.InboxPersonaError, a.Persona.ID),
			}
			if a.LastErrorAt != nil {
				p.When = agoCtx(ctx, *a.LastErrorAt, now, loc)
			}
			vm.Personas = append(vm.Personas, p)
		}
	}
	if show("alertas") {
		for _, a := range in.Alerts {
			row := alertRow(ctx, a, base, now, loc)
			row.Ctl = ctl(domain.InboxAlert, a.ID)
			vm.Alerts = append(vm.Alerts, row)
		}
	}
	if show("tools") {
		for _, q := range in.QuarantinedTools { // tools novas que esperam a revisão de um admin (ADR-0053)
			vm.Quarantined = append(vm.Quarantined, views.InboxQuarantine{Integration: q.Tool.IntegrationSlug, Tool: q.Tool.Name,
				Risk: riskBadge(ctx, q.Tool.Risk), Excerpt: excerpt(q.Tool.Description, 160), When: agoCtx(ctx, q.Tool.FirstSeenAt, now, loc),
				Href: base + "/ferramentas?integracao=" + url.QueryEscape(q.Tool.IntegrationSlug)})
		}
	}
	if show("assinaturas") {
		for _, g := range in.GuardedSubscriptions {
			limit, pct := "—", ""
			if g.WeeklyLimitUSD != nil {
				limit, pct = fmtUSDCtx(ctx, *g.WeeklyLimitUSD), fmt.Sprintf("%d%%", int(math.Round(g.WeekPercent)))
			}
			vm.Subs = append(vm.Subs, views.InboxSub{Name: g.Name, Href: base + "/custos#assinaturas", Usage: fmtUSDCtx(ctx, g.WeekUsageUSD),
				Limit: limit, Percent: pct, Guard: g.WeeklyGuardPct, Week: weekLabel(ctx, g.WeekStart.In(loc)), Ctl: ctl(domain.InboxSubscription, g.ID)})
		}
	}
	if cur == "tudo" || cur == tabUnread || cur == tabArchived {
		for _, m := range in.Mentions {
			vm.Mentions = append(vm.Mentions, views.InboxMention{Key: m.TaskKey, Href: base + "/tasks/" + m.TaskKey, Title: m.TaskTitle, Author: m.Author,
				Excerpt: excerpt(m.Excerpt, 240), When: agoCtx(ctx, m.At, now, loc), Ctl: ctl(domain.InboxMention, m.ID)})
		}
		for _, g := range in.AchievedGoals {
			at := g.UpdatedAt
			if g.AchievedAt != nil {
				at = *g.AchievedAt
			}
			vm.Goals = append(vm.Goals, views.AchievedGoalRow{Title: g.Title, Href: base + "/metas/" + g.Slug,
				Text: i18n.Tf(ctx, "A meta chegou ao alvo: %s.", goalValues(ctx, g.Goal)), When: agoCtx(ctx, at, now, loc), Ctl: ctl(domain.InboxGoalAchieved, g.ID)})
		}
	}
	vm.Total = len(vm.Gates) + len(vm.Held) + len(vm.Blocked) + len(vm.Failed) + len(vm.Stopped) + len(vm.Personas) + len(vm.Alerts) + len(vm.Subs) + len(vm.Mentions) + len(vm.Quarantined)
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
