package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Canais externos (ADR-0046): entrega das mensagens de saída e processamento das de entrada.

// ChannelSendArgs entrega uma mensagem de saída de canal (job "channel_send"). O retry é da fila:
// service.ChannelMaxAttempts tentativas com o backoff do service; esgotadas, a mensagem fica failed.
type ChannelSendArgs struct {
	MessageID domain.ID `json:"message_id"`
}

func (ChannelSendArgs) Kind() string { return "channel_send" }

func (ChannelSendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: service.ChannelMaxAttempts}
}

// channelInboundMaxAttempts é o total de tentativas de processar uma entrada.
const channelInboundMaxAttempts = 5

// ChannelInboundArgs processa uma mensagem que entrou por um canal (job "channel_inbound").
type ChannelInboundArgs struct {
	MessageID domain.ID `json:"message_id"`
}

func (ChannelInboundArgs) Kind() string { return "channel_inbound" }

func (ChannelInboundArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: channelInboundMaxAttempts}
}

var _ service.ChannelJobs = (*Heartbeat)(nil)

// SendChannelMessageTx enfileira a entrega, na transação do fato que gerou a mensagem.
func (h *Heartbeat) SendChannelMessageTx(ctx context.Context, tx pgx.Tx, messageID domain.ID) error {
	if _, err := h.client.InsertTx(ctx, tx, ChannelSendArgs{MessageID: messageID}, nil); err != nil {
		return fmt.Errorf("enqueue channel_send: %w", err)
	}
	return nil
}

// ProcessChannelInboundTx enfileira o processamento de uma entrada gravada.
func (h *Heartbeat) ProcessChannelInboundTx(ctx context.Context, tx pgx.Tx, messageID domain.ID) error {
	if _, err := h.client.InsertTx(ctx, tx, ChannelInboundArgs{MessageID: messageID}, nil); err != nil {
		return fmt.Errorf("enqueue channel_inbound: %w", err)
	}
	return nil
}

// channelBackend é o que o service oferece aos jobs de canal; opcional como os demais.
type channelBackend interface {
	DeliverChannelMessage(ctx context.Context, id domain.ID, attempt, maxAttempts int) (service.ChannelDelivery, error)
	GiveUpChannelMessage(ctx context.Context, id domain.ID, cause error)
	ProcessChannelInbound(ctx context.Context, id domain.ID) error
	GiveUpChannelInbound(ctx context.Context, id domain.ID, cause error)
}

var _ channelBackend = (*service.Service)(nil)

// addChannelWorkers registra os workers de canal.
func addChannelWorkers(workers *river.Workers, h *Heartbeat) error {
	return errors.Join(
		river.AddWorkerSafely(workers, &channelSendWorker{h: h}),
		river.AddWorkerSafely(workers, &channelInboundWorker{h: h}),
	)
}

type channelSendWorker struct {
	river.WorkerDefaults[ChannelSendArgs]
	h *Heartbeat
}

func (w *channelSendWorker) Timeout(*river.Job[ChannelSendArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *channelSendWorker) NextRetry(job *river.Job[ChannelSendArgs]) time.Time {
	return time.Now().Add(service.ChannelBackoff(job.Attempt))
}

func (w *channelSendWorker) Work(ctx context.Context, job *river.Job[ChannelSendArgs]) error {
	cb, ok := w.h.svc.(channelBackend)
	if !ok {
		return errNotAttached
	}
	_, err := cb.DeliverChannelMessage(ctx, job.Args.MessageID, job.Attempt, job.MaxAttempts)
	if err == nil {
		return nil
	}
	if job.Attempt >= job.MaxAttempts { // a última tentativa já fechou a mensagem; este caminho cobre o erro de gravação
		cb.GiveUpChannelMessage(ctx, job.Args.MessageID, err)
		w.h.log.Warn("channel_send: desistindo", "message_id", job.Args.MessageID, "err", err)
		return nil
	}
	return err
}

type channelInboundWorker struct {
	river.WorkerDefaults[ChannelInboundArgs]
	h *Heartbeat
}

func (w *channelInboundWorker) Timeout(*river.Job[ChannelInboundArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *channelInboundWorker) NextRetry(job *river.Job[ChannelInboundArgs]) time.Time {
	return time.Now().Add(time.Duration(job.Attempt*job.Attempt) * 10 * time.Second)
}

func (w *channelInboundWorker) Work(ctx context.Context, job *river.Job[ChannelInboundArgs]) error {
	cb, ok := w.h.svc.(channelBackend)
	if !ok {
		return errNotAttached
	}
	err := cb.ProcessChannelInbound(ctx, job.Args.MessageID)
	if err == nil {
		return nil
	}
	if job.Attempt >= job.MaxAttempts {
		cb.GiveUpChannelInbound(ctx, job.Args.MessageID, err)
		w.h.log.Warn("channel_inbound: desistindo", "message_id", job.Args.MessageID, "err", err)
		return nil
	}
	return err
}
