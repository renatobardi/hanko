package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
)

// Consolidação de memória governada (ADR-0030): o tick periódico que dispara as rodadas de curadoria e o
// job que aplica no provedor de memória as propostas aprovadas.

const applyProposalTimeout = 60 * time.Second

type memoryTickWorker struct {
	river.WorkerDefaults[MemoryTickArgs]
	h *Heartbeat
}

func (w *memoryTickWorker) Work(ctx context.Context, _ *river.Job[MemoryTickArgs]) error {
	return w.h.memoryTick(ctx, time.Now().UTC())
}

// memoryTick dispara a rodada de curadoria de cada empresa devida. Disparar é idempotente por (workspace,
// horário agendado); uma empresa falhar não impede as outras, e a falha é refeita no próximo tick.
func (h *Heartbeat) memoryTick(ctx context.Context, now time.Time) error {
	if h.svc == nil {
		return errNotAttached
	}
	due, err := h.svc.DueMemorySettings(ctx, now)
	if err != nil {
		return fmt.Errorf("memory_consolidation_tick: %w", err)
	}
	var errs []error
	for _, s := range due {
		if s.NextFireAt == nil {
			continue
		}
		cs, err := h.svc.FireMemoryConsolidation(ctx, s.CompanyID, *s.NextFireAt)
		if err != nil {
			h.log.Warn("memory_consolidation_tick: disparo falhou", "company_id", s.CompanyID, "err", err)
			errs = append(errs, fmt.Errorf("company %s: %w", s.CompanyID, err))
			continue
		}
		if len(cs) > 0 {
			h.log.Info("memory_consolidation_tick: curadoria disparada", "company_id", s.CompanyID, "workspaces", len(cs))
		}
	}
	return errors.Join(errs...)
}

// ---- apply_memory_proposal ----

type applyProposalWorker struct {
	river.WorkerDefaults[ApplyMemoryProposalArgs]
	h *Heartbeat
}

func (w *applyProposalWorker) Work(ctx context.Context, job *river.Job[ApplyMemoryProposalArgs]) error {
	return w.h.applyMemoryProposal(ctx, job.Args)
}

// ApplyMemoryProposalTx enqueues the application of an approved proposal inside tx.
func (h *Heartbeat) ApplyMemoryProposalTx(ctx context.Context, tx pgx.Tx, companyID, proposalID domain.ID) error {
	if _, err := h.client.InsertTx(ctx, tx, ApplyMemoryProposalArgs{CompanyID: companyID, ProposalID: proposalID}, nil); err != nil {
		return fmt.Errorf("enqueue apply_memory_proposal: %w", err)
	}
	return nil
}

// applyMemoryProposal repete no provedor de memória a chamada aprovada pelo humano. Provedor fora do ar (ou
// que recusa) deixa a proposta falha, com o erro e a próxima tentativa agendada: nenhum dado se perde e a
// decisão humana segue valendo. Já aplicada, rejeitada ou fora do prazo = no-op.
func (h *Heartbeat) applyMemoryProposal(ctx context.Context, a ApplyMemoryProposalArgs) error {
	if h.svc == nil {
		return errNotAttached
	}
	item, err := h.svc.LoadMemoryProposalForApply(ctx, a.CompanyID, a.ProposalID)
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrConflict):
		h.log.Debug("apply_memory_proposal: nada a aplicar", "proposal_id", a.ProposalID, "reason", err)
		return nil
	case err != nil:
		return fmt.Errorf("apply_memory_proposal: load: %w", err)
	}
	p := item.Proposal
	if p.Status == domain.ProposalFailed && p.NextAttemptAt == nil {
		return nil // desistiu depois de muitas tentativas; só o retry manual recoloca na fila
	}
	if p.NextAttemptAt != nil {
		if wait := time.Until(*p.NextAttemptAt); wait > time.Second {
			return river.JobSnooze(wait)
		}
	}
	caller, ok := h.mem.(memory.ToolCaller)
	if !ok {
		wait, merr := h.svc.MarkMemoryProposalFailed(ctx, a.CompanyID, a.ProposalID, "o provedor de memória não aplica propostas")
		if merr != nil {
			return fmt.Errorf("apply_memory_proposal: mark failed: %w", merr)
		}
		return river.JobSnooze(wait)
	}
	scope := memory.Scope{Workspace: item.CompanySlug, Project: p.WorkspaceSlug}
	rctx, cancel := context.WithTimeout(ctx, applyProposalTimeout)
	defer cancel()
	if _, err := caller.CallTool(rctx, scope, item.Tool, item.Arguments); err != nil {
		wait, merr := h.svc.MarkMemoryProposalFailed(ctx, a.CompanyID, a.ProposalID, err.Error())
		if merr != nil {
			return fmt.Errorf("apply_memory_proposal: mark failed: %w", merr)
		}
		h.log.Warn("apply_memory_proposal: aplicação falhou, nova tentativa agendada", "proposal_id", a.ProposalID, "err", err)
		return river.JobSnooze(wait)
	}
	if err := h.svc.MarkMemoryProposalApplied(ctx, a.CompanyID, a.ProposalID); err != nil {
		return fmt.Errorf("apply_memory_proposal: mark applied: %w", err)
	}
	h.log.Info("apply_memory_proposal: proposta aplicada", "proposal_id", a.ProposalID, "kind", p.Kind)
	return nil
}

// sweepProposals re-enqueues the proposals whose application is due (the job was lost, or the backoff elapsed).
func (h *Heartbeat) sweepProposals(ctx context.Context) error {
	refs, err := h.svc.DueMemoryProposals(ctx, scanLimit)
	if err != nil {
		return fmt.Errorf("memory_sweep: proposals: %w", err)
	}
	for _, r := range refs {
		if err := h.enqueueApply(ctx, ApplyMemoryProposalArgs{CompanyID: r.CompanyID, ProposalID: r.ID}); err != nil {
			return fmt.Errorf("memory_sweep: enqueue apply: %w", err)
		}
	}
	return nil
}
