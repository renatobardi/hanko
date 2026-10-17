package service

import (
	"context"
	"encoding/json"
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
			anchor, anchorJSON, state, err := prepareAnchor(ctx, t.q, companyID, task, in.Anchor)
			if err != nil {
				return err
			}
			row, err := t.q.CreateComment(ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: companyID,
				TaskID: taskID, AuthorType: string(actor.Type), AuthorID: actor.ID, RunID: in.RunID, Body: body,
				EventID: eid, Anchor: anchorJSON, Now: t.now})
			if err != nil {
				return mapErr(err, "add comment")
			}
			out = commentFromRow(row)
			if anchor != nil {
				out.AnchorState = &state
			}
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
			if err := t.recordMentions(actor, companyID, task, row, body); err != nil {
				return err
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
	task, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
	if err != nil {
		return nil, mapErr(err, "get task")
	}
	rows, err := q.ListComments(ctx, store.ListCommentsParams{CompanyID: companyID, TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	out := make([]domain.Comment, len(rows))
	docs := map[string]string{} // documents already loaded in this call
	for i, r := range rows {
		out[i] = commentFromRow(r)
		if a := out[i].Anchor; a != nil {
			doc, err := anchorDoc(ctx, q, companyID, task, *a, docs)
			st := domain.AnchorState{Status: domain.AnchorLost}
			if err == nil {
				st = a.Locate(doc)
			}
			out[i].AnchorState = &st
		}
	}
	return out, nil
}

// anchorDoc loads the current text of the document an anchor points into. A run summary of another task,
// or a run that no longer exists, is an error (the caller treats it as a lost anchor when listing).
func anchorDoc(ctx context.Context, q *store.Queries, companyID domain.ID, task store.Task, a domain.Anchor, cache map[string]string) (string, error) {
	switch a.Target {
	case domain.AnchorTaskDescription:
		return task.Description, nil
	case domain.AnchorRunSummary:
		if a.RunID == nil {
			return "", domain.Invalid("anchor.run_id", "required when target is run_summary")
		}
		key := a.RunID.String()
		if d, ok := cache[key]; ok {
			return d, nil
		}
		r, err := q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: *a.RunID})
		if err != nil {
			if isNoRows(err) {
				return "", domain.Invalid("anchor.run_id", "run not found")
			}
			return "", fmt.Errorf("anchor run: %w", err)
		}
		if r.TaskID == nil || *r.TaskID != task.ID {
			return "", domain.Invalid("anchor.run_id", "run belongs to another task")
		}
		cache[key] = r.Summary
		return r.Summary, nil
	}
	return "", domain.Invalid("anchor.target", "must be task_description or run_summary")
}

// prepareAnchor validates an anchor against the target document and returns it canonical (offsets and
// hash set by the server) together with its JSON and state. A nil anchor yields zero values.
func prepareAnchor(ctx context.Context, q *store.Queries, companyID domain.ID, task store.Task, in *domain.Anchor) (*domain.Anchor, []byte, domain.AnchorState, error) {
	if in == nil {
		return nil, nil, domain.AnchorState{}, nil
	}
	a := *in
	if n := utf8.RuneCountInString(a.Quote); n < 1 || n > domain.MaxAnchorQuoteRunes {
		return nil, nil, domain.AnchorState{}, domain.Invalid("anchor.quote", fmt.Sprintf("must have 1-%d characters", domain.MaxAnchorQuoteRunes))
	}
	if a.Start < 0 {
		a.Start = 0
	}
	if a.Target != domain.AnchorRunSummary {
		a.RunID = nil
	}
	doc, err := anchorDoc(ctx, q, companyID, task, a, map[string]string{})
	if err != nil {
		return nil, nil, domain.AnchorState{}, err
	}
	st := a.Locate(doc)
	if st.Status == domain.AnchorLost {
		return nil, nil, domain.AnchorState{}, domain.Invalid("anchor.quote", "text not found in the target document")
	}
	a.Start, a.End, a.DocHash = st.Start, st.End, domain.DocHash(doc)
	st.Status = domain.AnchorExact
	b, err := json.Marshal(a)
	if err != nil {
		return nil, nil, domain.AnchorState{}, fmt.Errorf("marshal anchor: %w", err)
	}
	return &a, b, st, nil
}
