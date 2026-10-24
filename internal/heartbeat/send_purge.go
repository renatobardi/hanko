package heartbeat

import (
	"context"
	"time"

	"github.com/riverqueue/river"
)

// SendPurgeInterval é a cadência da limpeza dos envios vencidos (ADR-0069 §7). A checagem de prazo e de limite
// também roda na abertura, então o job só cuida do que ninguém abriu.
const SendPurgeInterval = 5 * time.Minute

// SendPurgeTickArgs é a limpeza periódica dos envios (job "send_purge_tick").
type SendPurgeTickArgs struct{}

func (SendPurgeTickArgs) Kind() string { return "send_purge_tick" }

func (SendPurgeTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

type sendPurger interface {
	PurgeExpiredSends(ctx context.Context) (int, error)
}

type sendPurgeTickWorker struct {
	river.WorkerDefaults[SendPurgeTickArgs]
	h *Heartbeat
}

// Work apaga o conteúdo dos envios vencidos. Sem o backend do cofre ligado, não há o que apagar.
func (w *sendPurgeTickWorker) Work(ctx context.Context, _ *river.Job[SendPurgeTickArgs]) error {
	if w.h.svc == nil {
		return errNotAttached
	}
	sb, ok := w.h.svc.(sendPurger)
	if !ok {
		return nil
	}
	_, err := sb.PurgeExpiredSends(ctx)
	return err
}
