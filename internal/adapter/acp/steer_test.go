package acp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

// Uma mensagem do operador cancela o turno em curso e vai como o próximo session/prompt da MESMA sessão.
func TestSteerInjectsNextPromptInSameSession(t *testing.T) {
	spec := fakeSpec(t, "steer", nil, nil)
	ch := make(chan adapter.Steer, 1)
	spec.Steer = ch
	col := &collector{}
	acked := make(chan error, 1)
	go func() {
		for i := 0; i < 500 && !col.has(domain.EventAssistant, "trabalhando") && !col.has(domain.EventSystem, "agente ACP conectado"); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond) // o turno está em andamento
		ch <- adapter.Steer{ID: "f1", Text: "foque nos testes", Ack: func(err error) { acked <- err }}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := testAdapter().Run(ctx, spec, col)
	t.Logf("events:\n%s", col.dump())
	if err != nil || res.IsError {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	select {
	case e := <-acked:
		if e != nil {
			t.Fatalf("ack = %v", e)
		}
	default:
		t.Fatal("a mensagem não foi confirmada")
	}
	if !strings.Contains(res.Summary, "rumo novo: Mensagem do operador durante a execução") || !strings.Contains(res.Summary, "foque nos testes") ||
		!strings.Contains(res.Summary, "sessão=sess-1") {
		t.Fatalf("summary = %q", res.Summary)
	}
	if !col.has(domain.EventSystem, "mensagem do operador recebida") {
		t.Error("o log não registra a intervenção")
	}
	if res.SessionRef != "sess-1" {
		t.Fatalf("session = %q", res.SessionRef)
	}
	announced := false
	for _, e := range col.all() {
		if e.SessionRef == "sess-1" {
			announced = true
		}
	}
	if !announced {
		t.Error("a sessão não foi anunciada com SessionRef")
	}
}

func TestSteerBlocksEncoding(t *testing.T) {
	b := steerBlocks([]adapter.Steer{{Text: "um"}, {Text: "dois"}})
	if len(b) != 1 || b[0]["type"] != "text" || !strings.HasSuffix(b[0]["text"], "um\n\ndois") {
		t.Fatalf("blocks = %+v", b)
	}
}

func TestDeclaresInject(t *testing.T) {
	if adapter.SteerModeOf(testAdapter()) != domain.SteerInject || domain.SteerModeFor(testAdapter().Name()) != domain.SteerInject {
		t.Fatal("acp deve declarar inject")
	}
}
