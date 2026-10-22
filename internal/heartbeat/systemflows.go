package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// SystemFlowTickArgs é a varredura periódica dos fluxos de sistema (job "system_flow_tick"): tarefas da própria
// instalação, hoje o backup agendado (ADR-0062). Fica fora do flow_tick porque um backup dura minutos e o job
// ganha um teto de tempo maior que o padrão do River.
type SystemFlowTickArgs struct{}

func (SystemFlowTickArgs) Kind() string { return "system_flow_tick" }

func (SystemFlowTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// systemFlowJobTimeout é o teto de um backup agendado.
const systemFlowJobTimeout = 2 * time.Hour

type systemFlowTickWorker struct {
	river.WorkerDefaults[SystemFlowTickArgs]
	h *Heartbeat
}

func (w *systemFlowTickWorker) Timeout(*river.Job[SystemFlowTickArgs]) time.Duration {
	return systemFlowJobTimeout
}

func (w *systemFlowTickWorker) Work(ctx context.Context, _ *river.Job[SystemFlowTickArgs]) error {
	return w.h.systemFlowTick(ctx, time.Now().UTC())
}

// systemFlows é a parte do service que os fluxos de sistema usam (interface opcional: dublês antigos não a têm).
type systemFlows interface {
	DueSystemFlows(ctx context.Context, now time.Time) ([]domain.SystemFlow, error)
	FireSystemFlow(ctx context.Context, key string, scheduledFor time.Time) (service.SystemFlowResult, error)
}

// systemFlowTick dispara os fluxos de sistema vencidos. Idempotente por ocorrência; uma falha não para as outras.
func (h *Heartbeat) systemFlowTick(ctx context.Context, now time.Time) error {
	if h.svc == nil {
		return errNotAttached
	}
	sf, ok := h.svc.(systemFlows)
	if !ok {
		return nil
	}
	due, err := sf.DueSystemFlows(ctx, now)
	if err != nil {
		return fmt.Errorf("system_flow_tick: %w", err)
	}
	var errs []error
	for _, f := range due {
		if f.NextFireAt == nil {
			continue
		}
		res, err := sf.FireSystemFlow(ctx, f.Key, *f.NextFireAt)
		if err != nil {
			h.log.Warn("system_flow_tick: disparo falhou", "key", f.Key, "err", err)
			errs = append(errs, fmt.Errorf("system flow %s: %w", f.Key, err))
			continue
		}
		switch {
		case res.Skipped:
			h.log.Debug("system_flow_tick: nada a fazer", "key", f.Key, "reason", res.SkipReason)
		case res.Backup != nil && res.Backup.Status == domain.BackupFailed:
			h.log.Warn("system_flow_tick: backup agendado falhou", "key", f.Key, "id", res.Backup.ID, "error", res.Backup.Error)
		default:
			h.log.Info("system_flow_tick: fluxo de sistema disparado", "key", f.Key)
		}
	}
	return errors.Join(errs...)
}
