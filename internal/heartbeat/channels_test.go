package heartbeat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// chanBackend é o fakeBackend que também entrega e processa mensagens de canal.
type chanBackend struct {
	*fakeBackend
	sendErr    error
	sendOut    service.ChannelDelivery
	procErr    error
	sendCalls  []int
	gaveUp     []domain.ID
	procCalls  int
	gaveUpInbd []domain.ID
}

func (b *chanBackend) DeliverChannelMessage(_ context.Context, _ domain.ID, attempt, _ int) (service.ChannelDelivery, error) {
	b.sendCalls = append(b.sendCalls, attempt)
	return b.sendOut, b.sendErr
}

func (b *chanBackend) GiveUpChannelMessage(_ context.Context, id domain.ID, _ error) {
	b.gaveUp = append(b.gaveUp, id)
}

func (b *chanBackend) ProcessChannelInbound(context.Context, domain.ID) error {
	b.procCalls++
	return b.procErr
}

func (b *chanBackend) GiveUpChannelInbound(_ context.Context, id domain.ID, _ error) {
	b.gaveUpInbd = append(b.gaveUpInbd, id)
}

func sendJob(args ChannelSendArgs, attempt, max int) *river.Job[ChannelSendArgs] {
	return &river.Job[ChannelSendArgs]{JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: max}, Args: args}
}

func inboundJob(args ChannelInboundArgs, attempt, max int) *river.Job[ChannelInboundArgs] {
	return &river.Job[ChannelInboundArgs]{JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: max}, Args: args}
}

func TestChannelJobOptsAndBackoff(t *testing.T) {
	if (ChannelSendArgs{}).Kind() != "channel_send" || (ChannelSendArgs{}).InsertOpts().MaxAttempts != service.ChannelMaxAttempts {
		t.Fatalf("send: %+v", (ChannelSendArgs{}).InsertOpts())
	}
	if (ChannelInboundArgs{}).Kind() != "channel_inbound" || (ChannelInboundArgs{}).InsertOpts().MaxAttempts != channelInboundMaxAttempts {
		t.Fatalf("inbound: %+v", (ChannelInboundArgs{}).InsertOpts())
	}
	w := &channelSendWorker{}
	for _, attempt := range []int{1, 3, 5} {
		want := service.ChannelBackoff(attempt)
		if got := time.Until(w.NextRetry(sendJob(ChannelSendArgs{}, attempt, 6))); got < want-2*time.Second || got > want+2*time.Second {
			t.Errorf("tentativa %d: próxima em %v, esperado ~%v", attempt, got, want)
		}
	}
	if w.Timeout(nil) <= 0 || (&channelInboundWorker{}).Timeout(nil) <= 0 {
		t.Error("timeout")
	}
	if got := time.Until((&channelInboundWorker{}).NextRetry(inboundJob(ChannelInboundArgs{}, 2, 5))); got < 38*time.Second || got > 42*time.Second {
		t.Errorf("backoff da entrada: %v", got)
	}
	if service.ChannelBackoff(1) != 30*time.Second || service.ChannelBackoff(2) != 2*time.Minute || service.ChannelBackoff(20) != 2*time.Hour {
		t.Errorf("backoff: %v %v %v", service.ChannelBackoff(1), service.ChannelBackoff(2), service.ChannelBackoff(20))
	}
}

func TestChannelSendWorker(t *testing.T) {
	be := &chanBackend{fakeBackend: &fakeBackend{}}
	h, _ := newTestHB(t, be.fakeBackend, okAdapter(), &memStub{}, Options{})
	h.svc = be
	w := &channelSendWorker{h: h}
	args := ChannelSendArgs{MessageID: domain.NewID()}

	if err := w.Work(context.Background(), sendJob(args, 1, 6)); err != nil {
		t.Fatalf("entregue: %v", err)
	}
	// falha transitória: o erro volta para a fila agendar a próxima tentativa
	be.sendErr = errors.New("HTTP 502")
	if err := w.Work(context.Background(), sendJob(args, 2, 6)); err == nil {
		t.Fatal("falha transitória deveria devolver erro")
	}
	if len(be.gaveUp) != 0 {
		t.Fatalf("desistiu cedo demais: %v", be.gaveUp)
	}
	// na última tentativa o job fecha a mensagem e termina sem erro
	if err := w.Work(context.Background(), sendJob(args, 6, 6)); err != nil {
		t.Fatalf("última tentativa: %v", err)
	}
	if len(be.gaveUp) != 1 || be.gaveUp[0] != args.MessageID {
		t.Fatalf("GiveUp: %v", be.gaveUp)
	}
	// backend sem a interface
	h.svc = &fakeBackend{}
	if err := w.Work(context.Background(), sendJob(args, 1, 6)); !errors.Is(err, errNotAttached) {
		t.Fatalf("sem interface: %v", err)
	}
}

func TestChannelInboundWorker(t *testing.T) {
	be := &chanBackend{fakeBackend: &fakeBackend{}}
	h, _ := newTestHB(t, be.fakeBackend, okAdapter(), &memStub{}, Options{})
	h.svc = be
	w := &channelInboundWorker{h: h}
	args := ChannelInboundArgs{MessageID: domain.NewID()}

	if err := w.Work(context.Background(), inboundJob(args, 1, 5)); err != nil || be.procCalls != 1 {
		t.Fatalf("processada: %v %d", err, be.procCalls)
	}
	be.procErr = errors.New("banco fora")
	if err := w.Work(context.Background(), inboundJob(args, 2, 5)); err == nil {
		t.Fatal("falha deveria voltar para a fila")
	}
	if err := w.Work(context.Background(), inboundJob(args, 5, 5)); err != nil {
		t.Fatalf("última tentativa: %v", err)
	}
	if len(be.gaveUpInbd) != 1 || be.gaveUpInbd[0] != args.MessageID {
		t.Fatalf("GiveUp: %v", be.gaveUpInbd)
	}
	h.svc = &fakeBackend{}
	if err := w.Work(context.Background(), inboundJob(args, 1, 5)); !errors.Is(err, errNotAttached) {
		t.Fatalf("sem interface: %v", err)
	}
}
