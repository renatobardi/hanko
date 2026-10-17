package heartbeat

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
)

// O prompt da run de card de status (ADR-0032) traz o protocolo das duas etapas e o pacote de contexto.
func TestBuildStatusCardPrompt(t *testing.T) {
	rc := sampleContext()
	rc.Run.Trigger = domain.TriggerStatusCard
	rc.Task.Title = "Atualizar card de status: Bloqueios"
	rc.Task.Description = "<!-- hanko:status_card id=x etapa=atualizacao -->\n# Atualização do card de status: Bloqueios\n- HNK-3 · blocked"
	system, prompt := BuildStatusCardPrompt(rc, "memória X")
	for _, w := range []string{"Escreva código limpo.", "Protocolo de card de status", "hanko_status_card_set_query", "hanko_status_card_set_summary",
		"Etapa montagem", "Etapa atualização", "Não altere tasks"} {
		if !strings.Contains(system, w) {
			t.Errorf("system sem %q:\n%s", w, system)
		}
	}
	for _, w := range []string{"# Atualizar card de status: Bloqueios", "- HNK-3 · blocked", "gatilho: status_card", "memória X", "Execute a etapa agora."} {
		if !strings.Contains(prompt, w) {
			t.Errorf("prompt sem %q:\n%s", w, prompt)
		}
	}
	// o protocolo normal de task não entra (não é para trabalhar na task nem publicar o resumo como comentário)
	if strings.Contains(system, "hanko_gate_request") || strings.Contains(system, "Trabalhe na task") {
		t.Errorf("protocolo de task comum no card:\n%s", system)
	}
}

type cardTickBackend struct {
	*fakeBackend
	calls int
	err   error
}

func (c *cardTickBackend) EvaluateDueStatusCards(context.Context) (int, error) {
	c.calls++
	return 2, c.err
}

func TestStatusCardTick(t *testing.T) {
	be := &cardTickBackend{fakeBackend: &fakeBackend{}}
	h := &Heartbeat{log: slog.New(slog.DiscardHandler), mem: memory.Noop{}}
	h.attach(be, nil, nil, nil, auth.RunTokens{})
	if err := h.statusCardTick(context.Background()); err != nil || be.calls != 1 {
		t.Fatalf("tick: %v %d", err, be.calls)
	}
	be.err = errors.New("boom")
	if err := h.statusCardTick(context.Background()); err == nil || !strings.Contains(err.Error(), "status_card_tick") {
		t.Fatalf("erro: %v", err)
	}
	// backend sem cards de status: no-op
	h2 := &Heartbeat{log: slog.New(slog.DiscardHandler), mem: memory.Noop{}}
	h2.attach(&fakeBackend{}, nil, nil, nil, auth.RunTokens{})
	if err := h2.statusCardTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := (&Heartbeat{}).statusCardTick(context.Background()); !errors.Is(err, errNotAttached) {
		t.Fatalf("sem attach: %v", err)
	}
}
