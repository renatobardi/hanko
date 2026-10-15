// Package heartbeat runs Hanko's background work on river (ADR-0012): waking personas,
// executing runs through adapters, delivering memory facts. It talks only to
// internal/service and internal/adapter.
package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// Options configures the heartbeat.
type Options struct {
	Workers          int           // concurrent jobs per queue; default 4
	PublicURL        string        // base URL the agent uses to reach Hanko (MCP); default http://127.0.0.1:7420
	RunTimeout       time.Duration // per-run limit; default 30 min
	PeriodicInterval time.Duration // heartbeat_tick, flow_tick and alert_tick period; default 1 min
	Logger           *slog.Logger

	// MemorySweepInterval is the memory_sweep period; default 5 min.
	MemorySweepInterval time.Duration

	// WakeSnooze is how long a wake waits before re-checking a task that still has an
	// active run; default 30 s.
	WakeSnooze time.Duration
}

func (o *Options) defaults() {
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.PublicURL == "" {
		o.PublicURL = "http://127.0.0.1:7420"
	}
	if o.RunTimeout <= 0 {
		o.RunTimeout = 30 * time.Minute
	}
	if o.PeriodicInterval <= 0 {
		o.PeriodicInterval = time.Minute
	}
	if o.MemorySweepInterval <= 0 {
		o.MemorySweepInterval = 5 * time.Minute
	}
	if o.WakeSnooze <= 0 {
		o.WakeSnooze = wakeSnooze
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
}

// backend is the subset of *service.Service the heartbeat uses.
type backend interface {
	CheckoutRun(ctx context.Context, w service.Wake) (domain.Run, error)
	LoadRunContext(ctx context.Context, companyID, runID domain.ID) (service.RunContext, error)
	MarkRunStarted(ctx context.Context, companyID, runID domain.ID, traceID, handoffIn string) (domain.Run, error)
	AppendRunEvent(ctx context.Context, companyID, runID domain.ID, e adapter.Event) (domain.RunEvent, error)
	FinishRun(ctx context.Context, companyID, runID domain.ID, in service.FinishRunInput) (domain.Run, error)
	SavePendingResult(ctx context.Context, companyID, runID domain.ID, in service.FinishRunInput) error
	InterruptOrphanRuns(ctx context.Context) (int, error)
	WakeCandidates(ctx context.Context, limit int) ([]service.WakeCandidate, error)
	WakeSeq(ctx context.Context, personaID, taskID domain.ID) (int64, error)
	ResumeBudgetPaused(ctx context.Context) (int, error)
	GetMemoryItem(ctx context.Context, id domain.ID) (service.MemoryItem, error)
	MarkMemorySent(ctx context.Context, id domain.ID) error
	MarkMemoryFailed(ctx context.Context, id domain.ID, errMsg string, permanent bool) error
	PendingMemory(ctx context.Context, limit int) ([]domain.ID, error)
	DueFlows(ctx context.Context, now time.Time) ([]domain.Flow, error)
	FireFlow(ctx context.Context, flowID domain.ID, scheduledFor time.Time) (service.FlowFireResult, error)
	EvaluateAlerts(ctx context.Context) (service.AlertEval, error)
	StartReview(ctx context.Context, companyID, reviewID domain.ID) error
	FailReview(ctx context.Context, companyID, reviewID domain.ID, reason string) error
	ResolveStaleReviews(ctx context.Context) (int, error)
	DueMemorySettings(ctx context.Context, now time.Time) ([]domain.MemorySettings, error)
	FireMemoryConsolidation(ctx context.Context, companyID domain.ID, scheduledFor time.Time) ([]domain.MemoryConsolidation, error)
	ResolveStaleConsolidations(ctx context.Context) (int, error)
	LoadMemoryProposalForApply(ctx context.Context, companyID, id domain.ID) (service.MemoryApplyItem, error)
	MarkMemoryProposalApplied(ctx context.Context, companyID, id domain.ID) error
	MarkMemoryProposalFailed(ctx context.Context, companyID, id domain.ID, errMsg string) (time.Duration, error)
	DueMemoryProposals(ctx context.Context, limit int) ([]service.MemoryProposalRef, error)
}

var _ backend = (*service.Service)(nil)
var _ service.Jobs = (*Heartbeat)(nil)
var _ service.ProposalJobs = (*Heartbeat)(nil)

// Heartbeat owns the river client and the workers.
type Heartbeat struct {
	opts   Options
	log    *slog.Logger
	client *river.Client[pgx.Tx]

	svc    backend
	reg    adapter.Registry
	mem    memory.Provider
	broker *events.Broker
	tokens auth.RunTokens

	// Enqueue seams outside a transaction (periodic scans); tests replace them.
	enqueueWake    func(context.Context, WakeArgs) error
	enqueueDeliver func(context.Context, DeliverMemoryArgs) error
	enqueueApply   func(context.Context, ApplyMemoryProposalArgs) error

	// finishBackoff are the waits between local FinishRun attempts (len+1 attempts); tests shrink it.
	finishBackoff []time.Duration
}

// Migrate brings the river schema up to the latest version.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}
	if _, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("river migrate up: %w", err)
	}
	return nil
}

// New builds the river client with all workers and periodic jobs. Call Attach, then Start.
func New(pool *pgxpool.Pool, opts Options) (*Heartbeat, error) {
	opts.defaults()
	h := &Heartbeat{opts: opts, log: opts.Logger, mem: memory.Noop{},
		finishBackoff: []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}}

	workers := river.NewWorkers()
	if err := errors.Join(
		river.AddWorkerSafely(workers, &wakeWorker{h: h}),
		river.AddWorkerSafely(workers, &executeRunWorker{h: h}),
		river.AddWorkerSafely(workers, &deliverMemoryWorker{h: h}),
		river.AddWorkerSafely(workers, &tickWorker{h: h}),
		river.AddWorkerSafely(workers, &sweepWorker{h: h}),
		river.AddWorkerSafely(workers, &flowTickWorker{h: h}),
		river.AddWorkerSafely(workers, &alertTickWorker{h: h}),
		river.AddWorkerSafely(workers, &startReviewWorker{h: h}),
		river.AddWorkerSafely(workers, &memoryTickWorker{h: h}),
		river.AddWorkerSafely(workers, &applyProposalWorker{h: h}),
	); err != nil {
		return nil, fmt.Errorf("heartbeat workers: %w", err)
	}

	rescue := opts.RunTimeout + time.Hour
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:  slog.New(minLevel{opts.Logger.Handler(), slog.LevelWarn}),
		Workers: workers,
		Queues: map[string]river.QueueConfig{
			QueueDefault: {MaxWorkers: opts.Workers},
			QueueRuns:    {MaxWorkers: opts.Workers},
		},
		Middleware: []rivertype.Middleware{otelriver.NewMiddleware(&otelriver.MiddlewareConfig{
			EnableWorkSpanJobKindSuffix: true,
			EnableTracePropagation:      true,
		})},
		RescueStuckJobsAfter: rescue,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(river.PeriodicInterval(opts.PeriodicInterval),
				func() (river.JobArgs, *river.InsertOpts) { return HeartbeatTickArgs{}, nil },
				&river.PeriodicJobOpts{ID: "heartbeat_tick"}),
			river.NewPeriodicJob(river.PeriodicInterval(opts.MemorySweepInterval),
				func() (river.JobArgs, *river.InsertOpts) { return MemorySweepArgs{}, nil },
				&river.PeriodicJobOpts{ID: "memory_sweep"}),
			river.NewPeriodicJob(river.PeriodicInterval(opts.PeriodicInterval),
				func() (river.JobArgs, *river.InsertOpts) { return FlowTickArgs{}, nil },
				&river.PeriodicJobOpts{ID: "flow_tick"}),
			river.NewPeriodicJob(river.PeriodicInterval(opts.PeriodicInterval),
				func() (river.JobArgs, *river.InsertOpts) { return AlertTickArgs{}, nil },
				&river.PeriodicJobOpts{ID: "alert_tick"}),
			river.NewPeriodicJob(river.PeriodicInterval(opts.PeriodicInterval),
				func() (river.JobArgs, *river.InsertOpts) { return MemoryTickArgs{}, nil },
				&river.PeriodicJobOpts{ID: "memory_consolidation_tick"}),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	h.client = client
	h.enqueueWake = func(ctx context.Context, a WakeArgs) error { _, err := client.Insert(ctx, a, nil); return err }
	h.enqueueDeliver = func(ctx context.Context, a DeliverMemoryArgs) error { _, err := client.Insert(ctx, a, nil); return err }
	h.enqueueApply = func(ctx context.Context, a ApplyMemoryProposalArgs) error { _, err := client.Insert(ctx, a, nil); return err }
	return h, nil
}

// Attach wires the collaborators. Must be called before Start.
func (h *Heartbeat) Attach(svc *service.Service, reg adapter.Registry, mem memory.Provider, broker *events.Broker, tokens auth.RunTokens) {
	h.attach(svc, reg, mem, broker, tokens)
}

func (h *Heartbeat) attach(svc backend, reg adapter.Registry, mem memory.Provider, broker *events.Broker, tokens auth.RunTokens) {
	if mem == nil {
		mem = memory.Noop{}
	}
	h.svc, h.reg, h.mem, h.broker, h.tokens = svc, reg, mem, broker, tokens
}

// Start interrupts orphaned runs (single instance, ADR-0008) and starts the river client.
func (h *Heartbeat) Start(ctx context.Context) error {
	if h.svc == nil {
		return errors.New("heartbeat: Attach must be called before Start")
	}
	n, err := h.svc.InterruptOrphanRuns(ctx)
	if err != nil {
		return fmt.Errorf("heartbeat: interrupt orphan runs: %w", err)
	}
	h.log.Info("heartbeat: runs órfãs interrompidas", "count", n)
	if err := h.client.Start(ctx); err != nil {
		return fmt.Errorf("heartbeat: start: %w", err)
	}
	return nil
}

// Stop waits for running jobs until ctx ends, then cancels what is left.
func (h *Heartbeat) Stop(ctx context.Context) error {
	err := h.client.Stop(ctx)
	if err == nil || ctx.Err() == nil {
		return err
	}
	// Grace period elapsed: cancel in-flight work so executors can mark their runs.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := h.client.StopAndCancel(cctx); err != nil {
		return fmt.Errorf("heartbeat: stop: %w", err)
	}
	return nil
}

// ---- service.Jobs ----

// WakeTx enqueues a coalesced wake inside tx. Every request also bumps wake_requests (same tx),
// even when the queue coalesces it into a job that is already running: the running worker sees
// the counter move and retries instead of losing the request.
func (h *Heartbeat) WakeTx(ctx context.Context, tx pgx.Tx, w service.Wake) error {
	if err := service.BumpWakeRequestTx(ctx, tx, w); err != nil {
		return err
	}
	_, err := h.client.InsertTx(ctx, tx, WakeArgs{CompanyID: w.CompanyID, PersonaID: w.PersonaID, TaskID: w.TaskID, Trigger: w.Trigger}, nil)
	if err != nil {
		return fmt.Errorf("enqueue wake: %w", err)
	}
	return nil
}

// ExecuteRunTx enqueues the execution of a queued run inside tx.
func (h *Heartbeat) ExecuteRunTx(ctx context.Context, tx pgx.Tx, companyID, runID domain.ID) error {
	if _, err := h.client.InsertTx(ctx, tx, ExecuteRunArgs{CompanyID: companyID, RunID: runID}, nil); err != nil {
		return fmt.Errorf("enqueue execute_run: %w", err)
	}
	return nil
}

// DeliverMemoryTx enqueues the delivery of a memory_outbox row inside tx.
func (h *Heartbeat) DeliverMemoryTx(ctx context.Context, tx pgx.Tx, outboxID domain.ID) error {
	if _, err := h.client.InsertTx(ctx, tx, DeliverMemoryArgs{OutboxID: outboxID}, nil); err != nil {
		return fmt.Errorf("enqueue deliver_memory: %w", err)
	}
	return nil
}

// StartReviewTx enqueues the start of a pending review inside tx.
func (h *Heartbeat) StartReviewTx(ctx context.Context, tx pgx.Tx, companyID, reviewID domain.ID) error {
	if _, err := h.client.InsertTx(ctx, tx, StartReviewArgs{CompanyID: companyID, ReviewID: reviewID}, nil); err != nil {
		return fmt.Errorf("enqueue start_review: %w", err)
	}
	return nil
}

// minLevel filtra registros abaixo de min (a fila loga contagens a cada minuto em INFO).
type minLevel struct {
	slog.Handler
	min slog.Level
}

func (h minLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h minLevel) WithAttrs(a []slog.Attr) slog.Handler {
	return minLevel{h.Handler.WithAttrs(a), h.min}
}
func (h minLevel) WithGroup(n string) slog.Handler { return minLevel{h.Handler.WithGroup(n), h.min} }
