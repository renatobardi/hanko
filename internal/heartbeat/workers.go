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
	return w.h.wake(ctx, job.Args, time.Since(job.CreatedAt))
}

const (
	wakeSnooze   = 30 * time.Second // re-check interval while the task has an active run
	wakeGiveUp   = 2 * time.Hour    // a wake older than this stops waiting for the active run
	finishSnooze = time.Minute      // re-check interval while a run could not be finalized
	finishGiveUp = 24 * time.Hour
)

// wake checks the task out. While another run is active the wake is snoozed (not dropped),
// so a comment or reassignment that arrives mid-run still produces a run afterwards; it
// stays one job per (persona, task), so further wakes keep coalescing. age is how long the
// wake has existed.
func (h *Heartbeat) wake(ctx context.Context, a WakeArgs, age time.Duration) error {
	if h.svc == nil {
		return errNotAttached
	}
	run, err := h.svc.CheckoutRun(ctx, service.Wake{CompanyID: a.CompanyID, PersonaID: a.PersonaID, TaskID: a.TaskID, Trigger: a.Trigger})
	switch {
	case err == nil:
		h.log.Debug("wake: run enfileirada", "run_id", run.ID, "task_id", a.TaskID, "trigger", a.Trigger)
		return nil
	case errors.Is(err, domain.ErrActiveRun):
		if age > wakeGiveUp {
			h.log.Warn("wake: run ativa por tempo demais, desistindo", "task_id", a.TaskID, "persona_id", a.PersonaID, "age", age)
			return nil
		}
		h.log.Debug("wake: task com run ativa, tentando de novo", "task_id", a.TaskID, "persona_id", a.PersonaID)
		return river.JobSnooze(h.opts.WakeSnooze)
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrNotFound):
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
	return w.h.opts.RunTimeout + 6*time.Minute
}

func (w *executeRunWorker) Work(ctx context.Context, job *river.Job[ExecuteRunArgs]) error {
	return w.h.executeRunJob(ctx, job.Args, jobInfo{final: job.Attempt >= job.MaxAttempts, age: time.Since(job.CreatedAt)})
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
	if item.Status == "sent" || item.Status == "failed" {
		h.log.Debug("deliver_memory: item já encerrado", "outbox_id", item.ID, "status", item.Status)
		return nil
	}
	var fact memory.Fact
	if err := json.Unmarshal(item.Payload, &fact); err != nil {
		_ = h.svc.MarkMemoryFailed(ctx, item.ID, "payload inválido: "+err.Error(), true)
		return river.JobCancel(fmt.Errorf("deliver_memory: payload inválido: %w", err))
	}
	if fact.Kind == "" {
		fact.Kind = item.Kind
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := h.mem.Record(rctx, scopeFor(item.CompanySlug, item.Workspace), fact); err != nil {
		// Transient: the fact stays pending and is retried later, never dropped.
		if merr := h.svc.MarkMemoryFailed(ctx, item.ID, err.Error(), false); merr != nil {
			h.log.Warn("deliver_memory: marcar falha", "outbox_id", item.ID, "err", merr)
		}
		h.log.Warn("deliver_memory: entrega falhou, nova tentativa agendada", "outbox_id", item.ID, "err", err)
		return river.JobSnooze(service.MemoryBackoff(item.Attempts))
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
