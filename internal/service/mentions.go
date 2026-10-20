package service

import (
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Menções em comentários de task (ADR-0042 §A.5): @slug-da-persona e @handle-do-membro. Humano citado vai para a
// Caixa dele (e recebe aviso); persona citada é acordada, mas só quando o autor do comentário é humano.

// notifyRunFailed avisa os membros de que uma execução falhou.
func (t *txc) notifyRunFailed(companyID domain.ID, v RunView, errText string) error {
	c, err := t.q.GetCompanyByID(t.ctx, companyID)
	if err != nil {
		return mapErr(err, "company")
	}
	title := "Execução falhou"
	if v.TaskKey != "" {
		title += " em " + v.TaskKey
	}
	body := errText
	if body == "" {
		body = v.PersonaName
	}
	return t.notifyMembers(companyID, domain.NotifyRunFailed, nil, Notice{Title: title, Body: body, TaskKey: v.TaskKey,
		URL: "/c/" + c.Slug + "/execucoes/" + v.ID.String()})
}

// recordMentions resolve as menções do texto do comentário, grava uma linha por alvo e um fato, acorda as personas
// citadas por um humano e avisa os humanos citados. Roda na transação do comentário.
func (t *txc) recordMentions(actor domain.Actor, companyID domain.ID, task store.Task, comment store.TaskComment, body string) error {
	handles := domain.ParseMentions(body)
	if len(handles) == 0 {
		return nil
	}
	personas, err := t.q.ListPersonas(t.ctx, companyID)
	if err != nil {
		return fmt.Errorf("mentions: list personas: %w", err)
	}
	members, err := t.q.ListMemberHandles(t.ctx, companyID)
	if err != nil {
		return fmt.Errorf("mentions: list members: %w", err)
	}
	pc := make([]domain.MentionCandidate, len(personas))
	for i, p := range personas {
		pc[i] = domain.MentionCandidate{ID: p.ID, Handle: p.Slug}
	}
	mc := make([]domain.MentionCandidate, len(members))
	for i, m := range members {
		mc[i] = domain.MentionCandidate{ID: m.UserID, Handle: domain.MentionHandle(m.Email)}
	}
	var self *domain.ID
	if actor.Type == domain.ActorHuman {
		self = actor.ID
	}
	targets, _ := domain.ResolveMentions(handles, pc, mc, self)
	if len(targets) == 0 {
		return nil
	}
	var users []domain.ID
	for _, tg := range targets {
		p := store.CreateCommentMentionParams{ID: domain.NewID(), CompanyID: companyID, CommentID: comment.ID, TaskID: task.ID,
			AuthorType: string(actor.Type), AuthorID: actor.ID, Now: t.now}
		data := map[string]any{"comment_id": comment.ID, "handle": tg.Handle}
		switch {
		case tg.PersonaID != nil:
			p.TargetType, p.PersonaID = "persona", tg.PersonaID
			data["persona_id"] = tg.PersonaID
		case tg.UserID != nil:
			p.TargetType, p.UserID = "user", tg.UserID
			data["user_id"] = tg.UserID
			users = append(users, *tg.UserID)
		}
		if err := t.q.CreateCommentMention(t.ctx, p); err != nil {
			return fmt.Errorf("create mention: %w", err)
		}
		if err := t.activity(actor, companyID, "comment.mentioned", "task", task.ID, data, ""); err != nil {
			return err
		}
		// persona citada por um humano acorda (a task já acorda a responsável pelo comentário humano: o wake coalesce)
		if tg.PersonaID != nil && actor.Type == domain.ActorHuman {
			if err := t.wake(companyID, task.ID, tg.PersonaID, domain.TaskStatus(task.Status), domain.TriggerComment, false); err != nil {
				return err
			}
		}
	}
	if len(users) > 0 {
		c, err := t.q.GetCompanyByID(t.ctx, companyID)
		if err != nil {
			return mapErr(err, "company")
		}
		key := domain.TaskKey(c.TaskPrefix, task.Number)
		t.notify(companyID, domain.NotifyMention, users, Notice{
			Title: "Você foi mencionado em " + key, Body: clipRunes(comment.Body, 100), URL: "/c/" + c.Slug + "/tasks/" + key, TaskKey: key})
	}
	return nil
}
