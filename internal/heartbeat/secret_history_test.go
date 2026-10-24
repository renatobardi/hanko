package heartbeat

import (
	"context"
	"testing"
)

// fakePruneBackend só implementa a retenção; o resto da interface nunca é chamado por este job.
type fakePruneBackend struct {
	backend
	calls int
}

func (f *fakePruneBackend) PruneSecretFieldHistory(context.Context) (int, error) {
	f.calls++
	return 0, nil
}

// O job de retenção chama o service quando ele tem o cofre, e não falha quando não tem.
func TestSecretHistoryTickChamaAPoda(t *testing.T) {
	fb := &fakePruneBackend{}
	w := &secretHistoryTickWorker{h: &Heartbeat{svc: fb}}
	if err := w.Work(context.Background(), nil); err != nil || fb.calls != 1 {
		t.Fatalf("Work: chamadas %d, erro %v; quer 1 chamada e sem erro", fb.calls, err)
	}

	if err := (&secretHistoryTickWorker{h: &Heartbeat{svc: &struct{ backend }{}}}).Work(context.Background(), nil); err != nil {
		t.Fatalf("backend sem o cofre não devia falhar: %v", err)
	}
}
