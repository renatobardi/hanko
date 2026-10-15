package heartbeat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// applyBackend controla o que o service devolve ao job de aplicação de propostas.
type applyBackend struct {
	*fakeBackend
	mu         sync.Mutex
	begin      service.MemoryApplyItem
	beginErr   error
	appliedErr []error // um erro por chamada a MarkMemoryProposalApplied (esgotado = sucesso)
	applied    int
	failed     []string
}

func (b *applyBackend) BeginMemoryProposalApply(context.Context, domain.ID, domain.ID) (service.MemoryApplyItem, error) {
	return b.begin, b.beginErr
}

func (b *applyBackend) MarkMemoryProposalApplied(context.Context, domain.ID, domain.ID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.applied++
	if len(b.appliedErr) > 0 {
		err := b.appliedErr[0]
		b.appliedErr = b.appliedErr[1:]
		return err
	}
	return nil
}

func (b *applyBackend) MarkMemoryProposalFailed(_ context.Context, _, _ domain.ID, msg string) (time.Duration, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failed = append(b.failed, msg)
	return time.Minute, nil
}

type callerMem struct {
	memStub
	calls int
	err   error
}

func (c *callerMem) CallTool(context.Context, memory.Scope, string, map[string]any) (string, error) {
	c.calls++
	return "ok", c.err
}

func newApplyHB(t *testing.T, be *applyBackend, mem memory.Provider) *Heartbeat {
	t.Helper()
	h, _ := newTestHB(t, be.fakeBackend, fake.New(fake.Options{}), mem, Options{})
	h.svc = be
	h.markAppliedBackoff = time.Millisecond
	return h
}

func applyArgs() ApplyMemoryProposalArgs {
	return ApplyMemoryProposalArgs{CompanyID: domain.NewID(), ProposalID: domain.NewID()}
}

func applyItem(prior bool) service.MemoryApplyItem {
	return service.MemoryApplyItem{Proposal: domain.MemoryProposal{Kind: domain.ProposalDelete, WorkspaceSlug: "site"},
		CompanySlug: "acme", Tool: "memory_delete_page", Arguments: map[string]any{"path": "wiki/a"}, PriorAttempt: prior}
}

// Próxima tentativa no futuro: o job é no-op, sem snooze (que prenderia o retry manual) e sem chamar o provedor.
func TestApplyProposalNotDueIsNoopWithoutSnooze(t *testing.T) {
	be := &applyBackend{fakeBackend: &fakeBackend{}, beginErr: service.ErrProposalNotDue}
	mem := &callerMem{}
	h := newApplyHB(t, be, mem)
	if err := h.applyMemoryProposal(context.Background(), applyArgs()); err != nil {
		t.Fatalf("no-op esperado, veio %v (snooze incluso)", err)
	}
	if mem.calls != 0 || be.applied != 0 || len(be.failed) != 0 {
		t.Fatalf("nada deveria acontecer: calls=%d applied=%d failed=%v", mem.calls, be.applied, be.failed)
	}
}

// A falha do provedor termina o job (a varredura recoloca): nada de snooze de horas.
func TestApplyProposalFailureEndsJobWithoutSnooze(t *testing.T) {
	be := &applyBackend{fakeBackend: &fakeBackend{}, begin: applyItem(false)}
	mem := &callerMem{err: errors.New("ai-memory fora do ar")}
	h := newApplyHB(t, be, mem)
	if err := h.applyMemoryProposal(context.Background(), applyArgs()); err != nil {
		t.Fatalf("a falha é registrada na proposta, o job termina: %v", err)
	}
	if len(be.failed) != 1 || be.applied != 0 {
		t.Fatalf("failed=%v applied=%d", be.failed, be.applied)
	}
}

// A chamada deu certo e o registro falhou uma vez: o UPDATE é refeito localmente, sem repetir a chamada.
func TestApplyProposalRetriesMarkAppliedLocally(t *testing.T) {
	be := &applyBackend{fakeBackend: &fakeBackend{}, begin: applyItem(false), appliedErr: []error{errors.New("conexão caiu")}}
	mem := &callerMem{}
	h := newApplyHB(t, be, mem)
	if err := h.applyMemoryProposal(context.Background(), applyArgs()); err != nil {
		t.Fatal(err)
	}
	if mem.calls != 1 || be.applied != 2 {
		t.Fatalf("a chamada não pode se repetir: calls=%d applied=%d", mem.calls, be.applied)
	}
}

// Se o registro nunca entra, o job devolve erro (river refaz) e a repetição sabe da tentativa anterior.
func TestApplyProposalMarkAppliedExhaustedReturnsError(t *testing.T) {
	boom := errors.New("banco fora")
	be := &applyBackend{fakeBackend: &fakeBackend{}, begin: applyItem(false), appliedErr: []error{boom, boom, boom}}
	mem := &callerMem{}
	h := newApplyHB(t, be, mem)
	if err := h.applyMemoryProposal(context.Background(), applyArgs()); err == nil {
		t.Fatal("esperava erro para o river refazer o job")
	}
	if len(be.failed) != 0 {
		t.Errorf("a chamada deu certo: não é falha de aplicação: %v", be.failed)
	}
}

// Repetição depois de uma tentativa anterior sem resolução: alvo inexistente conta como aplicado; sem tentativa
// anterior, o mesmo erro é falha comum.
func TestApplyProposalTargetGoneAfterPriorAttemptCountsAsApplied(t *testing.T) {
	gone := errors.New("ai-memory memory_delete_page: erro da tool: page not found")

	be := &applyBackend{fakeBackend: &fakeBackend{}, begin: applyItem(true)}
	h := newApplyHB(t, be, &callerMem{err: gone})
	if err := h.applyMemoryProposal(context.Background(), applyArgs()); err != nil {
		t.Fatal(err)
	}
	if be.applied != 1 || len(be.failed) != 0 {
		t.Fatalf("deveria contar como aplicada: applied=%d failed=%v", be.applied, be.failed)
	}

	be2 := &applyBackend{fakeBackend: &fakeBackend{}, begin: applyItem(false)}
	h2 := newApplyHB(t, be2, &callerMem{err: gone})
	if err := h2.applyMemoryProposal(context.Background(), applyArgs()); err != nil {
		t.Fatal(err)
	}
	if be2.applied != 0 || len(be2.failed) != 1 {
		t.Fatalf("sem tentativa anterior é falha: applied=%d failed=%v", be2.applied, be2.failed)
	}
}
