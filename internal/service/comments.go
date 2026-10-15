package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/store"
)

// AddComment appends to the thread. A human comment on an assigned open task enqueues
// a Wake (trigger comment). Same EventID twice → returns the existing comment.
func (s *Service) AddComment(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in AddCommentInput) (domain.Comment, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.Comment{}, err
	}
	body := strings.TrimSpace(in.Body)
	if n := utf8.RuneCountInString(body); n < 1 || n > 100000 {
		return domain.Comment{}, domain.Invalid("body", "must have 1-100000 characters")
	}
	var out domain.Comment
	run := func() error {
		return s.withTx(ctx, func(t *txc) error {
			task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
			if err != nil {
				return mapErr(err, "get task")
			}
			var eid *string
			if in.EventID != "" {
				eid = &in.EventID
				ex, err := t.q.GetCommentByEventID(ctx, store.GetCommentByEventIDParams{CompanyID: companyID, EventID: eid})
				if err == nil {
					out = commentFromRow(ex)
					return nil
				}
				if !isNoRows(err) {
					return fmt.Errorf("lookup event: %w", err)
				}
			}
			row, err := t.q.CreateComment(ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: companyID,
				TaskID: taskID, AuthorType: string(actor.Type), AuthorID: actor.ID, RunID: in.RunID, Body: body,
				EventID: eid, Now: t.now})
			if err != nil {
				return mapErr(err, "add comment")
			}
			out = commentFromRow(row)
			if err := t.activity(actor, companyID, "comment.added", "task", taskID,
				map[string]any{"comment_id": row.ID}, ""); err != nil {
				return err
			}
			if actor.Type == domain.ActorHuman {
				c, err := t.q.GetCompanyByID(ctx, companyID)
				if err != nil {
					return mapErr(err, "company")
				}
				key := domain.TaskKey(c.TaskPrefix, task.Number)
				f := memory.Fact{Kind: "comment.human", Path: fmt.Sprintf("hanko/tasks/%s-comment-%s.md", key, row.ID.String()[:8]),
					Title: "Comentário em " + key, Body: fmt.Sprintf("# Comentário em %s — %s\n\n%s\n", key, task.Title, body),
					Tags: []string{"hanko", "comment"}}
				if err := t.memoryFact(companyID, task.WorkspaceID, f, "comment.human:"+row.ID.String()); err != nil {
					return err
				}
				if err := t.wake(companyID, taskID, task.PersonaID, domain.TaskStatus(task.Status), domain.TriggerComment, false); err != nil {
					return err
				}
			}
			if tv, err := loadTaskView(ctx, t.q, companyID, taskID); err == nil {
				t.publish(companyTopic(companyID), "task.updated", tv)
			}
			return nil
		})
	}
	err := run()
	if err != nil && in.EventID != "" && isUniqueOn(err, "task_comments_event_uq") {
		err = run()
	}
	if err != nil {
		return domain.Comment{}, err
	}
	return out, nil
}

func (s *Service) ListComments(ctx context.Context, companyID, taskID domain.ID) ([]domain.Comment, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID}); err != nil {
		return nil, mapErr(err, "get task")
	}
	rows, err := q.ListComments(ctx, store.ListCommentsParams{CompanyID: companyID, TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	out := make([]domain.Comment, len(rows))
	for i, r := range rows {
		out[i] = commentFromRow(r)
	}
	return out, nil
}
