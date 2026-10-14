package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

var errNotAttached = errors.New("heartbeat: not attached")

// ---- wake ----

type wakeWorker struct {
	river.WorkerDefaults[WakeArgs]
	h *Heartbeat
}

func (w *wakeWorker) Work(ctx context.Context, job *river.Job[WakeArgs]) error {
	return w.h.wake(ctx, job.Args)
}

func (h *Heartbeat) wake(ctx context.Context, a WakeArgs) error {
	if h.svc == nil {
		return errNotAttached
	}
	run, err := h.svc.CheckoutRun(ctx, service.Wake{CompanyID: a.CompanyID, PersonaID: a.PersonaID, TaskID: a.TaskID, Trigger: a.Trigger})
	switch {
	case err == nil:
		h.log.Debug("wake: run enfileirada", "run_id", run.ID, "task_id", a.TaskID, "trigger", a.Trigger)
		return nil
	case errors.Is(err, domain.ErrActiveRun), errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrNotFound):
		h.log.Debug("wake: nada a fazer", "task_id", a.TaskID, "persona_id", a.PersonaID, "reason", err)
		return nil
	}
	return fmt.Errorf("wake: %w", err)
}

// ---- execute_run ----

type executeRunWorker struct {
	river.WorkerDefaults[ExecuteRunArgs]
	h *Heartbeat
}

// Timeout leaves room after RunTimeout for finishing the run and memory finalization.
func (w *executeRunWorker) Timeout(*river.Job[ExecuteRunArgs]) time.Duration {
	return w.h.opts.RunTimeout + 2*time.Minute
}

func (w *executeRunWorker) Work(ctx context.Context, job *river.Job[ExecuteRunArgs]) error {
	return w.h.executeRun(ctx, job.Args)
}

// ---- deliver_memory ----

type deliverMemoryWorker struct {
	river.WorkerDefaults[DeliverMemoryArgs]
	h *Heartbeat
}

func (w *deliverMemoryWorker) Work(ctx context.Context, job *river.Job[DeliverMemoryArgs]) error {
	return w.h.deliverMemory(ctx, job.Args)
}

func (h *Heartbeat) deliverMemory(ctx context.Context, a DeliverMemoryArgs) error {
	if h.svc == nil {
		return errNotAttached
	}
	item, err := h.svc.GetMemoryItem(ctx, a.OutboxID)
	if errors.Is(err, domain.ErrNotFound) {
		h.log.Debug("deliver_memory: item inexistente", "outbox_id", a.OutboxID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("deliver_memory: load: %w", err)
	}
	var fact memory.Fact
	if err := json.Unmarshal(item.Payload, &fact); err != nil {
		_ = h.svc.MarkMemoryFailed(ctx, item.ID, "payload inválido: "+err.Error())
		return river.JobCancel(fmt.Errorf("deliver_memory: payload inválido: %w", err))
	}
	if fact.Kind == "" {
		fact.Kind = item.Kind
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := h.mem.Record(rctx, scopeFor(item.CompanySlug, item.Workspace), fact); err != nil {
		if merr := h.svc.MarkMemoryFailed(ctx, item.ID, err.Error()); merr != nil {
			h.log.Warn("deliver_memory: marcar falha", "outbox_id", item.ID, "err", merr)
		}
		return fmt.Errorf("deliver_memory: %w", err)
	}
	if err := h.svc.MarkMemorySent(ctx, item.ID); err != nil {
		return fmt.Errorf("deliver_memory: mark sent: %w", err)
	}
	return nil
}

// scopeFor maps a company slug and optional workspace to a memory scope.
func scopeFor(companySlug string, ws *domain.Workspace) memory.Scope {
	s := memory.Scope{Workspace: companySlug, Project: "default"}
	if ws != nil {
		if ws.Slug != "" {
			s.Project = ws.Slug
		}
		s.Cwd = ws.Path
	}
	return s
}

// ---- heartbeat_tick / memory_sweep ----

const scanLimit = 100

type tickWorker struct {
	river.WorkerDefaults[HeartbeatTickArgs]
	h *Heartbeat
}

func (w *tickWorker) Work(ctx context.Context, _ *river.Job[HeartbeatTickArgs]) error {
	h := w.h
	if h.svc == nil {
		return errNotAttached
	}
	cands, err := h.svc.WakeCandidates(ctx, scanLimit)
	if err != nil {
		return fmt.Errorf("heartbeat_tick: %w", err)
	}
	for _, c := range cands {
		if err := h.enqueueWake(ctx, WakeArgs{CompanyID: c.CompanyID, PersonaID: c.PersonaID, TaskID: c.TaskID, Trigger: domain.TriggerHeartbeat}); err != nil {
			return fmt.Errorf("heartbeat_tick: enqueue wake: %w", err)
		}
	}
	if len(cands) > 0 {
		h.log.Debug("heartbeat_tick", "candidates", len(cands))
	}
	return nil
}

type sweepWorker struct {
	river.WorkerDefaults[MemorySweepArgs]
	h *Heartbeat
}

func (w *sweepWorker) Work(ctx context.Context, _ *river.Job[MemorySweepArgs]) error {
	h := w.h
	if h.svc == nil {
		return errNotAttached
	}
	ids, err := h.svc.PendingMemory(ctx, scanLimit)
	if err != nil {
		return fmt.Errorf("memory_sweep: %w", err)
	}
	for _, id := range ids {
		if err := h.enqueueDeliver(ctx, DeliverMemoryArgs{OutboxID: id}); err != nil {
			return fmt.Errorf("memory_sweep: enqueue: %w", err)
		}
	}
	if len(ids) > 0 {
		h.log.Debug("memory_sweep", "pending", len(ids))
	}
	return nil
}
