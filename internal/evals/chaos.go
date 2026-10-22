package evals

import (
	"context"
	"fmt"
	"sync"
)

// Fault faz as chamadas de tool de número From a To (1-based, To 0 = até o fim) falharem com Message,
// sem chegar ao harness interno: é o que a persona vê quando a run foi morta ou o banco caiu.
type Fault struct {
	From, To int
	Message  string
}

// Faulty envolve um Harness e injeta falhas por ordem de chamada (determinístico, sem relógio).
type Faulty struct {
	Inner  Harness
	Faults []Fault

	mu       sync.Mutex
	n        int
	injected bool
}

// Tools implementa Harness.
func (f *Faulty) Tools() []ToolSpec { return f.Inner.Tools() }

// Call implementa Harness.
func (f *Faulty) Call(ctx context.Context, c ToolCall) (string, bool, error) {
	f.mu.Lock()
	f.n++
	n := f.n
	f.injected = false
	for _, ft := range f.Faults {
		if n >= ft.From && (ft.To == 0 || n <= ft.To) {
			f.injected = true
			f.mu.Unlock()
			return ft.Message, true, nil
		}
	}
	f.mu.Unlock()
	return f.Inner.Call(ctx, c)
}

// WasInjected diz se a última chamada foi falha injetada.
func (f *Faulty) WasInjected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.injected
}

// Injector derruba e restaura infraestrutura de verdade (processo de run, conexões do banco). As
// implementações reais ficam em testes de integração; aqui só o contrato, para o runner aceitar qualquer uma.
type Injector interface {
	// Name identifica a falha no relatório.
	Name() string
	// Inject provoca a falha. Restore desfaz (idempotente).
	Inject(ctx context.Context) error
	Restore(ctx context.Context) error
}

// RunWithInjector provoca a falha, roda check enquanto ela vale e restaura sempre, mesmo se check falhar.
func RunWithInjector(ctx context.Context, inj Injector, check func(ctx context.Context) error) (err error) {
	if err := inj.Inject(ctx); err != nil {
		return fmt.Errorf("injetar %s: %w", inj.Name(), err)
	}
	defer func() {
		if rerr := inj.Restore(context.WithoutCancel(ctx)); rerr != nil && err == nil {
			err = fmt.Errorf("restaurar %s: %w", inj.Name(), rerr)
		}
	}()
	return check(ctx)
}
