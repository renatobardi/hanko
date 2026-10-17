package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Plugins de terceiros (ADR-0038): entrega de eventos, rodadas de job e o tick do host.

// PluginEventArgs entrega um fato de atividade a um plugin (job "plugin_event"). Pelo menos uma vez, com retry
// curto; o plugin deduplica pelo id do evento.
type PluginEventArgs struct {
	PluginID   domain.ID `json:"plugin_id"`
	CompanyID  domain.ID `json:"company_id"`
	ActivityID domain.ID `json:"activity_id"`
}

func (PluginEventArgs) Kind() string { return "plugin_event" }

func (PluginEventArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: service.PluginEventMaxAttempts}
}

// PluginJobArgs executa uma rodada de job de plugin (job "plugin_run_job"). Sem retry: a rodada seguinte é o
// próximo ciclo do job.
type PluginJobArgs struct {
	JobRunID domain.ID `json:"job_run_id" river:"unique"`
}

func (PluginJobArgs) Kind() string { return "plugin_run_job" }

func (PluginJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
		rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateScheduled, rivertype.JobStateRetryable, rivertype.JobStateRunning,
	}}}
}

// PluginTickArgs é o tick periódico dos plugins (job "plugin_tick").
type PluginTickArgs struct{}

func (PluginTickArgs) Kind() string { return "plugin_tick" }

func (PluginTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// pluginTickJob é o job periódico do tick de plugins.
func pluginTickJob(every time.Duration) *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(every),
		func() (river.JobArgs, *river.InsertOpts) { return PluginTickArgs{}, nil },
		&river.PeriodicJobOpts{ID: "plugin_tick"})
}

// addPluginWorkers registra os workers de plugin.
func addPluginWorkers(workers *river.Workers, h *Heartbeat) error {
	return errors.Join(
		river.AddWorkerSafely(workers, &pluginEventWorker{h: h}),
		river.AddWorkerSafely(workers, &pluginJobWorker{h: h}),
		river.AddWorkerSafely(workers, &pluginTickWorker{h: h}),
	)
}

var _ service.PluginJobs = (*Heartbeat)(nil)

// DeliverPluginEventTx enfileira a entrega do fato ao plugin, na transação do fato.
func (h *Heartbeat) DeliverPluginEventTx(ctx context.Context, tx pgx.Tx, pluginID, companyID, activityID domain.ID) error {
	if _, err := h.client.InsertTx(ctx, tx, PluginEventArgs{PluginID: pluginID, CompanyID: companyID, ActivityID: activityID}, nil); err != nil {
		return fmt.Errorf("enqueue plugin_event: %w", err)
	}
	return nil
}

// RunPluginJobTx enfileira a rodada de job, na transação que a abriu.
func (h *Heartbeat) RunPluginJobTx(ctx context.Context, tx pgx.Tx, jobRunID domain.ID) error {
	if _, err := h.client.InsertTx(ctx, tx, PluginJobArgs{JobRunID: jobRunID}, nil); err != nil {
		return fmt.Errorf("enqueue plugin_run_job: %w", err)
	}
	return nil
}

// pluginBackend é o que o service oferece para plugins; opcional como os demais.
type pluginBackend interface {
	DeliverPluginEvent(ctx context.Context, pluginID, companyID, activityID domain.ID) error
	LogPluginEventGiveUp(ctx context.Context, pluginID, companyID domain.ID, cause error)
	RunPluginJob(ctx context.Context, jobRunID domain.ID) error
	PluginTick(ctx context.Context) (int, error)
}

var _ pluginBackend = (*service.Service)(nil)

type pluginEventWorker struct {
	river.WorkerDefaults[PluginEventArgs]
	h *Heartbeat
}

func (w *pluginEventWorker) Timeout(*river.Job[PluginEventArgs]) time.Duration { return time.Minute }

func (w *pluginEventWorker) NextRetry(job *river.Job[PluginEventArgs]) time.Time {
	return time.Now().Add(service.PluginEventBackoff(job.Attempt))
}

func (w *pluginEventWorker) Work(ctx context.Context, job *river.Job[PluginEventArgs]) error {
	pb, ok := w.h.svc.(pluginBackend)
	if !ok {
		return errNotAttached
	}
	a := job.Args
	err := pb.DeliverPluginEvent(ctx, a.PluginID, a.CompanyID, a.ActivityID)
	if err == nil {
		return nil
	}
	if job.Attempt >= job.MaxAttempts {
		pb.LogPluginEventGiveUp(ctx, a.PluginID, a.CompanyID, err)
		w.h.log.Warn("plugin_event: desistindo", "plugin_id", a.PluginID, "activity_id", a.ActivityID, "err", err)
		return nil
	}
	return err
}

type pluginJobWorker struct {
	river.WorkerDefaults[PluginJobArgs]
	h *Heartbeat
}

func (w *pluginJobWorker) Timeout(*river.Job[PluginJobArgs]) time.Duration { return 6 * time.Minute }

func (w *pluginJobWorker) Work(ctx context.Context, job *river.Job[PluginJobArgs]) error {
	pb, ok := w.h.svc.(pluginBackend)
	if !ok {
		return errNotAttached
	}
	return pb.RunPluginJob(ctx, job.Args.JobRunID)
}

type pluginTickWorker struct {
	river.WorkerDefaults[PluginTickArgs]
	h *Heartbeat
}

func (w *pluginTickWorker) Work(ctx context.Context, _ *river.Job[PluginTickArgs]) error {
	pb, ok := w.h.svc.(pluginBackend)
	if !ok {
		return nil
	}
	n, err := pb.PluginTick(ctx)
	if err != nil {
		return fmt.Errorf("plugin_tick: %w", err)
	}
	if n > 0 {
		w.h.log.Debug("plugin_tick: jobs agendados", "count", n)
	}
	return nil
}
