package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"
)

// FlowTickArgs is the periodic scan for due Fluxos (job "flow_tick").
type FlowTickArgs struct{}

func (FlowTickArgs) Kind() string { return "flow_tick" }

func (FlowTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

type flowTickWorker struct {
	river.WorkerDefaults[FlowTickArgs]
	h *Heartbeat
}

func (w *flowTickWorker) Work(ctx context.Context, _ *river.Job[FlowTickArgs]) error {
	return w.h.flowTick(ctx, time.Now().UTC())
}

// flowTick fires every Fluxo whose next occurrence has come. Firing is idempotent per
// (flow, occurrence), so a repeated or overlapping tick never duplicates a task. One flow
// failing does not stop the others; the failure is logged and retried on the next tick.
func (h *Heartbeat) flowTick(ctx context.Context, now time.Time) error {
	if h.svc == nil {
		return errNotAttached
	}
	due, err := h.svc.DueFlows(ctx, now)
	if err != nil {
		return fmt.Errorf("flow_tick: %w", err)
	}
	var errs []error
	for _, f := range due {
		if f.NextFireAt == nil {
			continue
		}
		res, err := h.svc.FireFlow(ctx, f.ID, *f.NextFireAt)
		if err != nil {
			h.log.Warn("flow_tick: disparo falhou", "flow_id", f.ID, "slug", f.Slug, "err", err)
			errs = append(errs, fmt.Errorf("flow %s: %w", f.Slug, err))
			continue
		}
		switch {
		case res.Duplicate, res.Skipped:
			h.log.Debug("flow_tick: nada a criar", "flow_id", f.ID, "duplicate", res.Duplicate, "skipped", res.Skipped)
		default:
			h.log.Info("flow_tick: fluxo disparado", "flow_id", f.ID, "slug", f.Slug, "task_id", res.Task.ID)
		}
	}
	return errors.Join(errs...)
}
