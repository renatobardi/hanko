package auth

import (
	"context"
	"errors"
	"runtime"
	"time"
)

// ErrBusy: o servidor está com todos os slots de hashing de senha ocupados (argon2id gasta 64 MiB e
// CPU por operação); o cliente deve tentar de novo (HTTP 503 com Retry-After).
var ErrBusy = errors.New("muitos logins ao mesmo tempo; tente de novo")

// DefaultGateWait é quanto uma operação espera por um slot antes de desistir.
const DefaultGateWait = 2 * time.Second

// PasswordGate é o semáforo de hashing e verificação de senha: limita quantos argon2id rodam em
// paralelo no processo, para que uma enxurrada de logins não esgote memória e CPU.
type PasswordGate struct {
	slots chan struct{}
	wait  time.Duration
}

// NewPasswordGate cria o semáforo. capacity <= 0 usa max(2, NumCPU); wait <= 0 usa DefaultGateWait.
func NewPasswordGate(capacity int, wait time.Duration) *PasswordGate {
	if capacity <= 0 {
		capacity = max(2, runtime.NumCPU())
	}
	if wait <= 0 {
		wait = DefaultGateWait
	}
	return &PasswordGate{slots: make(chan struct{}, capacity), wait: wait}
}

// Acquire espera até wait por um slot. Devolve a função que o libera, ErrBusy se o prazo estourou ou
// o erro do contexto se ele terminou antes. Um *PasswordGate nil não limita nada.
func (g *PasswordGate) Acquire(ctx context.Context) (release func(), err error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g.slots <- struct{}{}:
		return g.release, nil
	default:
	}
	t := time.NewTimer(g.wait)
	defer t.Stop()
	select {
	case g.slots <- struct{}{}:
		return g.release, nil
	case <-t.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *PasswordGate) release() { <-g.slots }
