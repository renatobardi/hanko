package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Pedido de conexão OAuth pelo MCP (ADR-0043).

type connState struct {
	reqs  []service.RequestConnectionInput
	by    domain.Actor
	ready bool // a conexão já está autorizada
}

var connStates = map[*fake]*connState{}

func (f *fake) conn() *connState {
	f.mu.Lock()
	defer f.mu.Unlock()
	if connStates[f] == nil {
		connStates[f] = &connState{}
	}
	return connStates[f]
}

func (f *fake) RequestConnection(_ context.Context, a domain.Actor, _ domain.ID, in service.RequestConnectionInput) (service.RequestConnectionResult, error) {
	st := f.conn()
	f.mu.Lock()
	defer f.mu.Unlock()
	st.reqs = append(st.reqs, in)
	st.by = a
	c := service.ConnectionView{Connection: domain.Connection{Slug: "docs", Status: domain.ConnDraft}}
	if st.ready {
		c.Status = domain.ConnConnected
		return service.RequestConnectionResult{Connection: c, Connected: true}, nil
	}
	g := service.GateView{Gate: domain.Gate{ID: domain.NewID(), Kind: domain.GateConnection, Status: domain.GatePending}}
	return service.RequestConnectionResult{Connection: c, Gate: &g}, nil
}

func TestConnectionRequestTool(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	res := call(t, cs, "hanko_connection_request", map[string]any{"slug": "docs", "reason": "ler a documentação"})
	if res.IsError {
		t.Fatal(text(res))
	}
	var out connectionRequestOut
	structured(t, res, &out)
	st := e.f.conn()
	if out.Slug != "docs" || out.Connected || out.GateID == "" || len(st.reqs) != 1 {
		t.Fatalf("saída: %+v pedidos=%d", out, len(st.reqs))
	}
	in := st.reqs[0]
	// a execução e a task vêm do token; quem pede é a persona do token
	if in.Slug != "docs" || in.Reason != "ler a documentação" || in.RunID == nil || *in.RunID != e.runID || in.TaskID == nil || e.f.lastActor.Type != domain.ActorPersona && st.by.Type != domain.ActorPersona {
		t.Errorf("entrada do service: %+v por %+v", in, st.by)
	}
	if !strings.Contains(text(res), "gate") {
		t.Errorf("texto: %q", text(res))
	}

	// já autorizada: sem gate
	st.ready = true
	res = call(t, cs, "hanko_connection_request", map[string]any{"slug": "docs"})
	var again connectionRequestOut
	structured(t, res, &again)
	if res.IsError || !again.Connected || again.GateID != "" {
		t.Fatalf("já conectada: %+v %s", again, text(res))
	}
}

func TestConnectionRequestNeedsPersonaToken(t *testing.T) {
	e := setup(t, true) // trusted, sem token: ator humano
	cs, _ := e.connect(t, "")
	defer cs.Close()
	res := call(t, cs, "hanko_connection_request", map[string]any{"company": "acme", "slug": "docs"})
	if !res.IsError || !strings.Contains(text(res), "persona") {
		t.Fatalf("humano pedindo: %q", text(res))
	}
}
