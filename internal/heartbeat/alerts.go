package heartbeat

import (
	"context"
	"fmt"

	"github.com/riverqueue/river"
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
	return nil
}
