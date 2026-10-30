package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/renatobardi/hanko/internal/domain"
)

type alertTickWorker struct {
	river.WorkerDefaults[AlertTickArgs]
	h *Heartbeat
}

func (w *alertTickWorker) Work(ctx context.Context, _ *river.Job[AlertTickArgs]) error {
	return w.h.alertTick(ctx)
}

// alertTick evaluates the operational alerts: opens the conditions that appeared, keeps the ones
// that last and closes the ones that are gone. Evaluation is idempotent, so a repeated or
// overlapping tick changes nothing.
func (h *Heartbeat) alertTick(ctx context.Context) error {
	if h.svc == nil {
		return errNotAttached
	}
	ev, err := h.svc.EvaluateAlerts(ctx)
	if err != nil {
		return fmt.Errorf("alert_tick: %w", err)
	}
	if ev.Opened > 0 || ev.Closed > 0 {
		h.log.Info("alert_tick", "opened", ev.Opened, "closed", ev.Closed, "open", ev.Open)
	}
	// A classificação das conversas externas (ADR-0079) roda no mesmo tick; uma falha dela não derruba os alertas.
	if n, err := h.svc.ClassifyExternalConversations(ctx); err != nil {
		h.log.Warn("classify_external: falhou", "err", err)
	} else if n > 0 {
		h.log.Info("classify_external", "changed", n)
	}
	return nil
}

// ---- start_review ----

type startReviewWorker struct {
	river.WorkerDefaults[StartReviewArgs]
	h *Heartbeat
}

func (w *startReviewWorker) Work(ctx context.Context, job *river.Job[StartReviewArgs]) error {
	return w.h.startReview(ctx, job.Args, time.Since(job.CreatedAt), job.Attempt >= job.MaxAttempts)
}

// startReview opens the review run. While the task has an active run it waits (snooze); after
// wakeGiveUp, or when the last attempt still fails, the gate opens without a verdict so the task
// is never left in review with nobody to decide.
func (h *Heartbeat) startReview(ctx context.Context, a StartReviewArgs, age time.Duration, final bool) error {
	if h.svc == nil {
		return errNotAttached
	}
	err := h.svc.StartReview(ctx, a.CompanyID, a.ReviewID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrActiveRun):
		if age <= wakeGiveUp {
			return river.JobSnooze(h.opts.WakeSnooze)
		}
		return h.failReview(ctx, a, "A revisão não conseguiu iniciar: a task manteve uma execução ativa por tempo demais.")
	case final:
		h.log.Warn("start_review: desistindo", "review_id", a.ReviewID, "err", err)
		return h.failReview(ctx, a, "A revisão não conseguiu iniciar: "+err.Error())
	}
	return fmt.Errorf("start_review: %w", err)
}

func (h *Heartbeat) failReview(ctx context.Context, a StartReviewArgs, reason string) error {
	if err := h.svc.FailReview(ctx, a.CompanyID, a.ReviewID, reason); err != nil {
		return fmt.Errorf("start_review: fail review: %w", err)
	}
	return nil
}

// ---- readiness_tick ----

type readinessTickWorker struct {
	river.WorkerDefaults[ReadinessTickArgs]
	h *Heartbeat
}

func (w *readinessTickWorker) Timeout(*river.Job[ReadinessTickArgs]) time.Duration {
	return 30 * time.Minute
}

func (w *readinessTickWorker) Work(ctx context.Context, _ *river.Job[ReadinessTickArgs]) error {
	return w.h.readinessTick(ctx)
}

// readinessBackend é opcional, como environmentBackend: sem ele a checagem periódica não existe.
type readinessBackend interface {
	ReadinessTick(ctx context.Context) (int, error)
}

// readinessTick testa as personas sem teste ou com teste velho (ADR-0055). Um erro numa persona é registrado e não
// derruba o job: o alerta e o bloqueio dependem do resultado gravado, não do job.
func (h *Heartbeat) readinessTick(ctx context.Context) error {
	if h.svc == nil {
		return errNotAttached
	}
	rb, ok := h.svc.(readinessBackend)
	if !ok {
		return nil
	}
	n, err := rb.ReadinessTick(ctx)
	if n > 0 {
		h.log.Info("readiness_tick", "tested", n)
	}
	if err != nil {
		h.log.Warn("readiness_tick", "err", err)
	}
	return nil
}
