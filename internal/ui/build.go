package ui

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/md"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// lookup resolves ids to display names for activity and threads.
type lookup struct {
	ctx      context.Context // language of the names shown ("Sistema", "Você"…)
	personas map[domain.ID]domain.Persona
	tasks    map[domain.ID]service.TaskView
	users    map[domain.ID]domain.User
}

// lookup builds the name resolver for a request. Users are read only in authenticated mode.
func (s *server) lookup(ctx context.Context, personas []domain.Persona, tasks []service.TaskView) lookup {
	l := newLookup(ctx, personas, tasks)
	if s.auth != nil {
		if us, err := s.auth.be.ListUsers(ctx); err == nil {
			for _, u := range us {
				l.users[u.ID] = u
			}
		} else {
			s.log.Warn("ui: listar usuários para nomes", "err", err)
		}
	}
	return l
}

func newLookup(ctx context.Context, personas []domain.Persona, tasks []service.TaskView) lookup {
	l := lookup{ctx: ctx, personas: map[domain.ID]domain.Persona{}, tasks: map[domain.ID]service.TaskView{}, users: map[domain.ID]domain.User{}}
	for _, p := range personas {
		l.personas[p.ID] = p
	}
	for _, t := range tasks {
		l.tasks[t.ID] = t
	}
	return l
}

func (l lookup) personaName(id *domain.ID) string {
	if id == nil {
		return ""
	}
	if p, ok := l.personas[*id]; ok {
		return p.Name
	}
	return i18n.T(l.ctx, "Persona")
}

func (l lookup) actorName(a domain.Actor) string {
	switch a.Type {
	case domain.ActorPersona:
		return l.personaName(a.ID)
	case domain.ActorSystem:
		return i18n.T(l.ctx, "Sistema")
	}
	if a.ID != nil {
		if u, ok := l.users[*a.ID]; ok {
			return u.DisplayName()
		}
		return i18n.T(l.ctx, "Usuário")
	}
	return i18n.T(l.ctx, "Você") // == ownerName
}

func taskRow(ctx context.Context, t service.TaskView, base string, now time.Time, loc *time.Location) views.TaskRow {
	return views.TaskRow{
		Key:           t.Key,
		Href:          base + "/tasks/" + t.Key,
		Title:         t.Title,
		Persona:       t.PersonaName,
		Project:       t.ProjectName,
		Badge:         taskBadge(ctx, t),
		Priority:      string(t.Priority),
		PriorityLabel: priorityLabel(ctx, t.Priority),
		Updated:       relTimeCtx(ctx, t.UpdatedAt, now, loc),
	}
}

func runRow(ctx context.Context, r service.RunView, base string, now time.Time, loc *time.Location) views.RunRow {
	return views.RunRow{
		Name:      r.Name,
		Short:     shortID(r.ID),
		Href:      base + "/execucoes/" + r.ID.String(),
		Persona:   r.PersonaName,
		TaskKey:   r.TaskKey,
		TaskHref:  base + "/tasks/" + r.TaskKey,
		TaskTitle: r.TaskTitle,
		Badge:     runBadge(ctx, r.Status),
		Agent:     agentModel(r.Agent, r.Model),
		Duration:  runDuration(r.Run, now),
		Cost:      fmtCostPtrCtx(ctx, r.CostUSD),
		Tokens:    fmtUsageCtx(ctx, r.Usage),
		When:      relTimeCtx(ctx, r.CreatedAt, now, loc),
	}
}

func agentModel(agent, model string) string {
	if model == "" {
		return agent
	}
	return agent + " · " + model
}

func initialsOf(name string) string { return views.Initials(name) }

// activityItem turns a domain fact into one feed line. Subject labels come from
// what the UI already knows (tasks, personas); event data is only a fallback.
func (s *server) activityItem(ctx context.Context, a domain.ActivityEvent, base string, names lookup, now time.Time, loc *time.Location, relative bool) views.ActivityItem {
	it := views.ActivityItem{
		Actor:   names.actorName(a.Actor),
		Persona: a.Actor.Type == domain.ActorPersona,
		System:  a.Actor.Type == domain.ActorSystem,
		Detail:  subjectLabel(ctx, a.SubjectType) + " · " + verbTailCtx(ctx, a.Verb),
	}
	if relative {
		it.When = agoCtx(ctx, a.OccurredAt, now, loc)
	} else {
		it.When = fmtClock(a.OccurredAt, loc)
	}
	var data map[string]any
	if len(a.Data) > 0 {
		_ = json.Unmarshal(a.Data, &data)
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := data[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	it.Text = verbPhraseCtx(ctx, a.Verb)
	switch a.SubjectType {
	case "task", "comment":
		if t, ok := names.tasks[a.SubjectID]; ok && a.SubjectType == "task" {
			it.Subject, it.Href = t.Key, base+"/tasks/"+t.Key
		} else if key := str("task_key", "key"); key != "" {
			it.Subject, it.Href = key, base+"/tasks/"+key
		} else if tid := str("task_id"); tid != "" {
			if id, err := domain.ParseID(tid); err == nil {
				if t, ok := names.tasks[id]; ok {
					it.Subject, it.Href = t.Key, base+"/tasks/"+t.Key
				}
			}
		}
		if it.Subject == "" && a.SubjectType == "task" {
			it.Subject, it.Href = shortID(a.SubjectID), base+"/tasks/"+a.SubjectID.String()
		}
	case "persona":
		if p, ok := names.personas[a.SubjectID]; ok {
			it.Subject, it.Href = p.Name, base+"/personas/"+p.Slug
		} else {
			it.Subject = str("name")
		}
	case "run":
		it.Subject, it.Href = i18n.T(ctx, "execução")+" "+shortID(a.SubjectID), base+"/execucoes/"+a.SubjectID.String()
	case "company":
		it.Subject = str("name")
	case "gate":
		it.Subject, it.Href = str("title"), base+"/gates/"+a.SubjectID.String()
		if it.Subject == "" {
			it.Subject = "gate " + shortID(a.SubjectID)
		}
		if d := str("decision"); d != "" {
			it.Text = i18n.T(ctx, "decidiu") + " (" + decisionWord(ctx, d) + ")"
		}
	default:
		it.Subject = str("name", "title")
	}
	return it
}

// verbTails names the end of an activity verb ("task.created" -> "criada").
var verbTails = map[string]string{
	"created": i18n.N("criada"), "updated": i18n.N("atualizada"), "assigned": i18n.N("atribuída"), "status_changed": i18n.N("status"),
	"closed": i18n.N("encerrada"), "added": i18n.N("adicionado"), "hired": i18n.N("contratada"), "queued": i18n.N("na fila"),
	"started": i18n.N("iniciada"), "finished": i18n.N("concluída"), "cancelled": i18n.N("cancelada"), "requested": i18n.N("pedido"), "decided": i18n.N("decidido"),
	"guard_blocked": i18n.N("travada"), "deleted": i18n.N("removida"),
}

func verbTail(v string) string { return verbTailCtx(context.Background(), v) }

func verbTailCtx(ctx context.Context, v string) string {
	tail := v
	if _, after, ok := strings.Cut(v, "."); ok {
		tail = after
	}
	if pt, ok := verbTails[tail]; ok {
		return i18n.T(ctx, pt)
	}
	return tail
}

func decisionWord(ctx context.Context, d string) string {
	switch d {
	case "approved":
		return i18n.T(ctx, "aprovado")
	case "rejected":
		return i18n.T(ctx, "rejeitado")
	case "changes_requested":
		return i18n.T(ctx, "mudanças pedidas")
	}
	return d
}

var subjectLabels = map[string]string{"comment": i18n.N("comentário"), "run": i18n.N("execução"), "project": i18n.N("projeto"), "company": i18n.N("empresa"),
	"gate": i18n.N("gate"), "alert": i18n.N("alerta"), "review": i18n.N("revisão")}

func subjectLabel(ctx context.Context, t string) string {
	if pt, ok := subjectLabels[t]; ok {
		return i18n.T(ctx, pt)
	}
	return t
}

func verbPhrase(v string) string { return verbPhraseCtx(context.Background(), v) }

func verbPhraseCtx(ctx context.Context, v string) string {
	switch v {
	case "company.created":
		return i18n.T(ctx, "criou a empresa")
	case "workspace.created":
		return i18n.T(ctx, "criou o workspace")
	case "project.created":
		return i18n.T(ctx, "criou o projeto")
	case "persona.hired":
		return i18n.T(ctx, "contratou")
	case "persona.updated":
		return i18n.T(ctx, "atualizou a persona")
	case "task.created":
		return i18n.T(ctx, "criou")
	case "task.updated":
		return i18n.T(ctx, "atualizou")
	case "task.assigned":
		return i18n.T(ctx, "atribuiu")
	case "task.status_changed":
		return i18n.T(ctx, "mudou o status de")
	case "task.closed":
		return i18n.T(ctx, "encerrou")
	case "comment.added":
		return i18n.T(ctx, "comentou em")
	case "run.queued":
		return i18n.T(ctx, "enfileirou a")
	case "run.started":
		return i18n.T(ctx, "iniciou a")
	case "run.finished":
		return i18n.T(ctx, "concluiu a")
	case "run.cancelled":
		return i18n.T(ctx, "cancelou a")
	case "gate.requested":
		return i18n.T(ctx, "pediu a decisão")
	case "gate.decided":
		return i18n.T(ctx, "decidiu")
	case "review.requested":
		return i18n.T(ctx, "pediu a revisão automática")
	case "review.started":
		return i18n.T(ctx, "iniciou a revisão automática")
	case "review.finished":
		return i18n.T(ctx, "concluiu a revisão automática")
	case "review.failed":
		return i18n.T(ctx, "não concluiu a revisão automática")
	case "review.cancelled":
		return i18n.T(ctx, "cancelou a revisão automática")
	case "alert.opened":
		return i18n.T(ctx, "abriu o alerta")
	case "alert.closed":
		return i18n.T(ctx, "fechou o alerta")
	case "subscription.created":
		return i18n.T(ctx, "criou a assinatura")
	case "subscription.updated":
		return i18n.T(ctx, "atualizou a assinatura")
	case "subscription.deleted":
		return i18n.T(ctx, "removeu a assinatura")
	case "subscription.guard_blocked":
		return i18n.T(ctx, "travou a assinatura")
	}
	return v
}

// ---------- logs ----------

const maxLogText = 4000

func logLine(e domain.RunEvent, loc *time.Location) views.LogLine {
	t := e.Text
	if len(t) > maxLogText {
		t = strings.ToValidUTF8(t[:maxLogText], "") + "…"
	}
	return views.LogLine{
		Seq:   e.Seq,
		Time:  fmtClock(e.OccurredAt, loc),
		Kind:  string(e.Kind),
		Label: logLabel(e.Kind),
		Text:  strings.TrimRight(t, "\n"),
	}
}

func renderMD(src string) views.HTML { return views.HTML(md.Render(src)) }
