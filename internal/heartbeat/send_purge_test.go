package heartbeat

import (
	"context"
	"testing"
)

// fakeSendPurgeBackend só implementa a limpeza dos envios; o resto da interface nunca é chamado por este job.
type fakeSendPurgeBackend struct {
	backend
	calls int
}

func (f *fakeSendPurgeBackend) PurgeExpiredSends(context.Context) (int, error) {
	f.calls++
	return 0, nil
}

// O job de limpeza chama o service quando ele tem o cofre, e não falha quando não tem.
func TestSendPurgeTickChamaALimpeza(t *testing.T) {
	fb := &fakeSendPurgeBackend{}
	w := &sendPurgeTickWorker{h: &Heartbeat{svc: fb}}
	if err := w.Work(context.Background(), nil); err != nil || fb.calls != 1 {
		t.Fatalf("Work: chamadas %d, erro %v; quer 1 chamada e sem erro", fb.calls, err)
	}

	if err := (&sendPurgeTickWorker{h: &Heartbeat{svc: &struct{ backend }{}}}).Work(context.Background(), nil); err != nil {
		t.Fatalf("backend sem o cofre não devia falhar: %v", err)
	}
}
