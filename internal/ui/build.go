package ui

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/md"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// lookup resolves ids to display names for activity and threads.
type lookup struct {
	personas map[domain.ID]domain.Persona
	tasks    map[domain.ID]service.TaskView
	users    map[domain.ID]domain.User
}

// lookup builds the name resolver for a request. Users are read only in authenticated mode.
func (s *server) lookup(ctx context.Context, personas []domain.Persona, tasks []service.TaskView) lookup {
	l := newLookup(personas, tasks)
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

func newLookup(personas []domain.Persona, tasks []service.TaskView) lookup {
	l := lookup{personas: map[domain.ID]domain.Persona{}, tasks: map[domain.ID]service.TaskView{}, users: map[domain.ID]domain.User{}}
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
	return "Persona"
}

func (l lookup) actorName(a domain.Actor) string {
	switch a.Type {
	case domain.ActorPersona:
		return l.personaName(a.ID)
	case domain.ActorSystem:
		return "Sistema"
	}
	if a.ID != nil {
		if u, ok := l.users[*a.ID]; ok {
			return u.DisplayName()
		}
		return "Usuário"
	}
	return ownerName
}

func taskRow(t service.TaskView, base string, now time.Time, loc *time.Location) views.TaskRow {
	return views.TaskRow{
		Key:           t.Key,
		Href:          base + "/tasks/" + t.Key,
		Title:         t.Title,
		Persona:       t.PersonaName,
		Project:       t.ProjectName,
		Badge:         taskBadge(t),
		Priority:      string(t.Priority),
		PriorityLabel: priorityLabel(t.Priority),
		Updated:       relTime(t.UpdatedAt, now, loc),
	}
}

func runRow(r service.RunView, base string, now time.Time, loc *time.Location) views.RunRow {
	return views.RunRow{
		Name:      r.Name,
		Short:     shortID(r.ID),
		Href:      base + "/execucoes/" + r.ID.String(),
		Persona:   r.PersonaName,
		TaskKey:   r.TaskKey,
		TaskHref:  base + "/tasks/" + r.TaskKey,
		TaskTitle: r.TaskTitle,
		Badge:     runBadge(r.Status),
		Agent:     agentModel(r.Agent, r.Model),
		Duration:  runDuration(r.Run, now),
		Cost:      fmtCostPtr(r.CostUSD),
		Tokens:    fmtUsage(r.Usage),
		When:      relTime(r.CreatedAt, now, loc),
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
func (s *server) activityItem(a domain.ActivityEvent, base string, names lookup, now time.Time, loc *time.Location, relative bool) views.ActivityItem {
	it := views.ActivityItem{
		Actor:   names.actorName(a.Actor),
		Persona: a.Actor.Type == domain.ActorPersona,
		Detail:  subjectLabel(a.SubjectType) + " · " + verbTail(a.Verb),
	}
	if relative {
		it.When = ago(a.OccurredAt, now, loc)
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
	it.Text = verbPhrase(a.Verb)
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
		it.Subject, it.Href = "execução "+shortID(a.SubjectID), base+"/execucoes/"+a.SubjectID.String()
	case "company":
		it.Subject = str("name")
	case "gate":
		it.Subject, it.Href = str("title"), base+"/gates/"+a.SubjectID.String()
		if it.Subject == "" {
			it.Subject = "gate " + shortID(a.SubjectID)
		}
		if d := str("decision"); d != "" {
			it.Text = "decidiu (" + decisionWord(d) + ")"
		}
	default:
		it.Subject = str("name", "title")
	}
	return it
}

func verbTail(v string) string {
	tail := v
	if _, after, ok := strings.Cut(v, "."); ok {
		tail = after
	}
	if pt, ok := map[string]string{
		"created": "criada", "updated": "atualizada", "assigned": "atribuída", "status_changed": "status",
		"closed": "encerrada", "added": "adicionado", "hired": "contratada", "queued": "na fila",
		"started": "iniciada", "finished": "concluída", "cancelled": "cancelada", "requested": "pedido", "decided": "decidido",
	}[tail]; ok {
		return pt
	}
	return tail
}

func decisionWord(d string) string {
	switch d {
	case "approved":
		return "aprovado"
	case "rejected":
		return "rejeitado"
	case "changes_requested":
		return "mudanças pedidas"
	}
	return d
}

func subjectLabel(t string) string {
	if pt, ok := map[string]string{"comment": "comentário", "run": "execução", "project": "projeto", "company": "empresa", "gate": "gate", "alert": "alerta", "review": "revisão"}[t]; ok {
		return pt
	}
	return t
}

func verbPhrase(v string) string {
	switch v {
	case "company.created":
		return "criou a empresa"
	case "workspace.created":
		return "criou o workspace"
	case "project.created":
		return "criou o projeto"
	case "persona.hired":
		return "contratou"
	case "persona.updated":
		return "atualizou a persona"
	case "task.created":
		return "criou"
	case "task.updated":
		return "atualizou"
	case "task.assigned":
		return "atribuiu"
	case "task.status_changed":
		return "mudou o status de"
	case "task.closed":
		return "encerrou"
	case "comment.added":
		return "comentou em"
	case "run.queued":
		return "enfileirou a"
	case "run.started":
		return "iniciou a"
	case "run.finished":
		return "concluiu a"
	case "run.cancelled":
		return "cancelou a"
	case "gate.requested":
		return "pediu a decisão"
	case "gate.decided":
		return "decidiu"
	case "review.requested":
		return "pediu a revisão automática"
	case "review.started":
		return "iniciou a revisão automática"
	case "review.finished":
		return "concluiu a revisão automática"
	case "review.failed":
		return "não concluiu a revisão automática"
	case "review.cancelled":
		return "cancelou a revisão automática"
	case "alert.opened":
		return "abriu o alerta"
	case "alert.closed":
		return "fechou o alerta"
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
