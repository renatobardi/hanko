package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Nova sessão na task (#16, ADR-0054): o pedido vale para o próximo run da task, que não retoma a sessão do harness
// e vive só do briefing (a task, a thread, o projeto) e da memória. O pedido é registrado na atividade quando feito e
// de novo quando um run o aplica. Hoje as runs de task começam sempre uma sessão nova do harness; o marcador é o
// contrato que a continuação de runs (#12) consulta com domain.SessionUsable antes de retomar uma sessão.

func sessionResetFromRow(r store.TaskSessionReset) domain.TaskSessionReset {
	return domain.TaskSessionReset{ID: r.ID, TaskID: r.TaskID, Reason: r.Reason, By: actorOf(r.RequestedByType, r.RequestedByID),
		CreatedAt: r.CreatedAt, ConsumedRunID: r.ConsumedRunID, ConsumedAt: r.ConsumedAt}
}

// RequestTaskNewSession pede que o próximo run da task comece uma sessão nova (membro). A persona dona da task não
// pede por si: o pedido é de quem acompanha o trabalho.
func (s *Service) RequestTaskNewSession(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, reason string) (domain.TaskSessionReset, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.TaskSessionReset{}, err
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > domain.MaxResetReason {
		return domain.TaskSessionReset{}, domain.Invalid("reason", fmt.Sprintf("muito longo (máx %d caracteres)", domain.MaxResetReason))
	}
	if actor.Type == domain.ActorPersona {
		return domain.TaskSessionReset{}, domain.ErrNotAllowed
	}
	ctx, end := coordSpan(ctx, "hanko.task.session_reset")
	defer end()
	var out domain.TaskSessionReset
	err := s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if domain.TaskStatus(task.Status) == domain.TaskDone || domain.TaskStatus(task.Status) == domain.TaskCancelled {
			return domain.Invalid("task", "a task está encerrada")
		}
		row, err := t.q.CreateTaskSessionReset(ctx, store.CreateTaskSessionResetParams{ID: domain.NewID(), CompanyID: companyID, TaskID: taskID,
			Reason: reason, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create session reset")
		}
		out = sessionResetFromRow(row)
		return t.activity(actor, companyID, "task.session_reset", "task", taskID, map[string]any{"reason": reason}, "")
	})
	if err == nil {
		countCoord(ctx, "session", "reset")
	}
	return out, err
}

// consumeSessionReset aplica os pedidos pendentes da task ao run recém-aberto (mesma transação do despacho).
func (t *txc) consumeSessionReset(companyID, taskID, runID domain.ID) error {
	rows, err := t.q.ConsumeTaskSessionResets(t.ctx, store.ConsumeTaskSessionResetsParams{CompanyID: companyID, TaskID: taskID, RunID: &runID, Now: &t.now})
	if err != nil {
		return fmt.Errorf("consume session resets: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	return t.activity(domain.SystemActor, companyID, "run.session_reset_applied", "run", runID,
		map[string]any{"task_id": taskID, "requests": len(rows), "reason": rows[len(rows)-1].Reason}, "")
}

// LastSessionReset devolve o pedido de nova sessão mais recente da task (nil se nunca houve). É o que a
// continuação de runs consulta: uma sessão só é retomada se começou depois dele (domain.SessionUsable).
func (s *Service) LastSessionReset(ctx context.Context, companyID, taskID domain.ID) (*domain.TaskSessionReset, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	row, err := s.read().LatestTaskSessionReset(ctx, store.LatestTaskSessionResetParams{CompanyID: companyID, TaskID: taskID})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest session reset: %w", err)
	}
	r := sessionResetFromRow(row)
	return &r, nil
}
