package heartbeat

import (
	"context"
	"time"

	"github.com/riverqueue/river"
)

// SecretHistoryPruneInterval é a cadência da retenção do histórico de campos do cofre (ADR-0068 §3.2).
const SecretHistoryPruneInterval = time.Hour

// SecretHistoryTickArgs é a retenção periódica do histórico de campos (job "secret_history_tick").
type SecretHistoryTickArgs struct{}

func (SecretHistoryTickArgs) Kind() string { return "secret_history_tick" }

func (SecretHistoryTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

type secretHistoryBackend interface {
	PruneSecretFieldHistory(ctx context.Context) (int, error)
}

type secretHistoryTickWorker struct {
	river.WorkerDefaults[SecretHistoryTickArgs]
	h *Heartbeat
}

// Work apaga as versões que passaram da retenção. Sem o cofre ligado no backend, não há o que podar.
func (w *secretHistoryTickWorker) Work(ctx context.Context, _ *river.Job[SecretHistoryTickArgs]) error {
	if w.h.svc == nil {
		return errNotAttached
	}
	sb, ok := w.h.svc.(secretHistoryBackend)
	if !ok {
		return nil
	}
	_, err := sb.PruneSecretFieldHistory(ctx)
	return err
}
