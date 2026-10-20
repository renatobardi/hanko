package heartbeat

import (
	"context"
	"errors"
	"testing"
)

// readyBackend acrescenta a checagem periódica de readiness (opcional, ADR-0055) ao fakeBackend.
type readyBackend struct {
	*fakeBackend
	n     int
	err   error
	calls int
}

func (r *readyBackend) ReadinessTick(context.Context) (int, error) {
	r.calls++
	return r.n, r.err
}

func TestReadinessTick(t *testing.T) {
	be := &readyBackend{fakeBackend: &fakeBackend{}, n: 3}
	h, _ := newTestHB(t, be.fakeBackend, okAdapter(), nil, Options{})
	h.svc = be // o backend com a checagem periódica
	if err := h.readinessTick(context.Background()); err != nil || be.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, be.calls)
	}
	// erro numa persona não derruba o job: o bloqueio e o alerta dependem do resultado gravado, não do job
	be.err = errors.New("ssh fora")
	if err := h.readinessTick(context.Background()); err != nil || be.calls != 2 {
		t.Fatalf("a falha deve ser só registrada: err=%v calls=%d", err, be.calls)
	}
	// backend sem a checagem periódica: nada a fazer
	h2, _ := newTestHB(t, &fakeBackend{}, okAdapter(), nil, Options{})
	if err := h2.readinessTick(context.Background()); err != nil {
		t.Fatalf("esperava nil, veio %v", err)
	}
	h3, _ := New(lazyPool(t), Options{})
	if err := h3.readinessTick(context.Background()); !errors.Is(err, errNotAttached) {
		t.Fatalf("esperava errNotAttached, veio %v", err)
	}
	if k := (ReadinessTickArgs{}).Kind(); k != "readiness_tick" {
		t.Fatalf("kind = %q", k)
	}
	if o := (ReadinessTickArgs{}).InsertOpts(); o.MaxAttempts != 1 {
		t.Fatalf("opts: %+v", o)
	}
}
