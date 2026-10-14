package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPasswordGate(t *testing.T) {
	g := NewPasswordGate(2, 50*time.Millisecond)
	r1, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r2, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := g.Acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("com os slots cheios deveria dar ErrBusy, veio %v", err)
	}
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Fatalf("deveria esperar o prazo antes de desistir: %v", d)
	}
	r1()
	r3, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("slot liberado deveria ser reaproveitado: %v", err)
	}
	r2()
	r3()

	// quem espera é atendido quando um slot libera dentro do prazo
	slow := NewPasswordGate(1, time.Second)
	rel, _ := slow.Acquire(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); rel() }()
	if r, err := slow.Acquire(context.Background()); err != nil {
		t.Fatalf("deveria obter o slot liberado: %v", err)
	} else {
		r()
	}

	// contexto cancelado
	full := NewPasswordGate(1, time.Second)
	_, _ = full.Acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := full.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("contexto cancelado: %v", err)
	}

	var nilGate *PasswordGate
	if r, err := nilGate.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		r()
	}
	if c := cap(NewPasswordGate(0, 0).slots); c < 2 {
		t.Fatalf("capacidade padrão = %d, mínimo 2", c)
	}
}
