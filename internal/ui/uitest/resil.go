package uitest

import (
	"context"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Resiliência de run (ADR-0039): ações do operador, steer e fila de follow-ups em memória.

// RunActionCall records the last RunAction so tests can assert what the UI sent.
type RunActionCall struct {
	RunID  domain.ID
	Action domain.RunDecision
	Note   string
}

func (f *Fake) RunAction(_ context.Context, _ domain.Actor, _, runID domain.ID, in service.RunActionInput) (service.RunView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RunAction")
	if err := f.fail("RunAction"); err != nil {
		return service.RunView{}, err
	}
	if !f.canWrite() {
		return service.RunView{}, domain.ErrForbidden
	}
	if !in.Action.Valid() {
		return service.RunView{}, domain.Invalid("action", "ação desconhecida")
	}
	for i := range f.Runs {
		if f.Runs[i].ID == runID {
			f.RunActions = append(f.RunActions, RunActionCall{RunID: runID, Action: in.Action, Note: in.Note})
			f.Runs[i].Decision, f.Runs[i].DecisionNote, f.Runs[i].DecisionPending = in.Action, in.Note, false
			return f.Runs[i], nil
		}
	}
	return service.RunView{}, domain.ErrNotFound
}

// SteerCall records the last SteerRun.
type SteerCall struct {
	RunID domain.ID
	Body  string
}

func (f *Fake) SteerRun(_ context.Context, actor domain.Actor, companyID, runID domain.ID, body string) (service.SteerResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SteerRun")
	if err := f.fail("SteerRun"); err != nil {
		return service.SteerResult{}, err
	}
	if !f.canWrite() {
		return service.SteerResult{}, domain.ErrForbidden
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return service.SteerResult{}, domain.Invalid("body", "escreva algo antes de enviar")
	}
	for _, r := range f.Runs {
		if r.ID != runID {
			continue
		}
		if !r.Status.Active() {
			return service.SteerResult{}, domain.Invalid("run", "a execução já terminou")
		}
		mode := domain.SteerModeFor(r.Agent)
		delivery, wait := domain.DeliverySteer, domain.WaitSteerPending
		if mode == domain.SteerRestart {
			delivery, wait = domain.DeliveryRestart, domain.WaitRestartPending
		}
		fu := domain.Followup{ID: domain.NewID(), CompanyID: companyID, Kind: domain.FollowupTask, Body: body, Author: actor,
			Delivery: delivery, WaitReason: wait, CreatedAt: Now()}
		if r.TaskID != (domain.ID{}) {
			tid := r.TaskID
			fu.TaskID = &tid
		}
		f.Followups = append(f.Followups, fu)
		f.Steers = append(f.Steers, SteerCall{RunID: runID, Body: body})
		return service.SteerResult{Mode: mode, Followup: fu}, nil
	}
	return service.SteerResult{}, domain.ErrNotFound
}

func (f *Fake) ListFollowups(_ context.Context, _ domain.ID, target service.FollowupTarget) ([]domain.Followup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListFollowups"); err != nil {
		return nil, err
	}
	var out []domain.Followup
	for _, fu := range f.Followups {
		switch {
		case target.TaskID != nil && fu.TaskID != nil && *fu.TaskID == *target.TaskID:
		case target.ConversationID != nil && fu.ConversationID != nil && *fu.ConversationID == *target.ConversationID:
		default:
			continue
		}
		fu.Position = len(out) + 1
		out = append(out, fu)
	}
	return out, nil
}

func (f *Fake) QueueTaskFollowup(_ context.Context, actor domain.Actor, companyID, taskID domain.ID, body string) (domain.Followup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("QueueTaskFollowup")
	if err := f.fail("QueueTaskFollowup"); err != nil {
		return domain.Followup{}, err
	}
	if !f.canWrite() {
		return domain.Followup{}, domain.ErrForbidden
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return domain.Followup{}, domain.Invalid("body", "escreva algo antes de enviar")
	}
	tid := taskID
	fu := domain.Followup{ID: domain.NewID(), CompanyID: companyID, Kind: domain.FollowupTask, TaskID: &tid, Body: body, Author: actor,
		Delivery: domain.DeliveryQueue, WaitReason: domain.WaitRunActive, CreatedAt: Now()}
	f.Followups = append(f.Followups, fu)
	return fu, nil
}

func (f *Fake) EditFollowup(_ context.Context, _ domain.Actor, _, id domain.ID, body string) (domain.Followup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("EditFollowup")
	if err := f.fail("EditFollowup"); err != nil {
		return domain.Followup{}, err
	}
	if !f.canWrite() {
		return domain.Followup{}, domain.ErrForbidden
	}
	for i := range f.Followups {
		if f.Followups[i].ID == id {
			now := Now()
			f.Followups[i].Body, f.Followups[i].EditedAt = strings.TrimSpace(body), &now
			return f.Followups[i], nil
		}
	}
	return domain.Followup{}, domain.ErrNotFound
}

func (f *Fake) DiscardFollowup(_ context.Context, _ domain.Actor, _, id domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DiscardFollowup")
	if err := f.fail("DiscardFollowup"); err != nil {
		return err
	}
	if !f.canWrite() {
		return domain.ErrForbidden
	}
	for i := range f.Followups {
		if f.Followups[i].ID == id {
			f.Followups = append(f.Followups[:i], f.Followups[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

// StopRun leaves the run interrupted with a diagnosis, waiting for the operator (what the service does after a crash
// with an external effect in flight, or when the continuations ran out).
func (f *Fake) StopRun(runID domain.ID, d domain.RunDiagnosis) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Runs {
		if f.Runs[i].ID == runID {
			now := Now()
			f.Runs[i].Status, f.Runs[i].FinishedAt = domain.RunInterrupted, &now
			f.Runs[i].Diagnosis, f.Runs[i].DecisionPending = &d, d.NeedsDecision()
		}
	}
}
