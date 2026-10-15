package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// Ponta a ponta da governança da memória no mux de produção: a run de curadoria chama uma tool que remove
// uma página pelo /memory-mcp; o ai-memory (falso) não recebe nada, a proposta e o gate nascem no banco e a
// run recebe uma resposta sintética. Leitura segue direto ao ai-memory.
func TestWiringMemoriaCuradoriaRetemEscritaDestrutiva(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := service.New(pool, noopJobs{}, events.NewBroker())
	svc.MemoryProvider = config.MemoryAIMemory
	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}

	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(upstream.Close)

	cfg := config.Defaults()
	cfg.Mode = config.ModeTrusted
	cfg.Memory = config.Memory{Provider: config.MemoryAIMemory, URL: upstream.URL, Token: "mem"}
	h, err := buildHandler(handlerDeps{Cfg: cfg, Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool, Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Curadora", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateWorkspace(ctx, domain.HumanActor, co.ID, service.CreateWorkspaceInput{Name: "Site", Slug: "site"}); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := svc.UpdateMemorySettings(ctx, domain.HumanActor, co.ID, service.UpdateMemorySettingsInput{Enabled: &on, CuratorPersonaID: &cur.ID}); err != nil {
		t.Fatal(err)
	}
	cs, err := svc.ConsolidateMemoryNow(ctx, domain.HumanActor, co.ID, "site")
	if err != nil || len(cs) != 1 {
		t.Fatalf("consolidar: %v %v", cs, err)
	}
	run, err := svc.CheckoutRun(ctx, service.Wake{CompanyID: co.ID, PersonaID: cur.ID, TaskID: *cs[0].TaskID, Trigger: domain.TriggerMemory})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := tokens.Issue(co.ID, cur.ID, run.ID, *cs[0].TaskID)
	if err != nil {
		t.Fatal(err)
	}

	// leitura: segue ao ai-memory
	code, _ := get(t, ts.URL, "POST", "/memory-mcp", "", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_search","arguments":{"q":"x"}}}`)
	if code != 200 || upstreamHits.Load() != 1 {
		t.Fatalf("leitura: %d, upstream=%d", code, upstreamHits.Load())
	}

	// remoção: retida
	code, body := get(t, ts.URL, "POST", "/memory-mcp", "", tok,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"memory_delete_page","arguments":{"path":"wiki/velha","project":"outro"}}}`)
	if code != 200 || !strings.Contains(body, `"result"`) || !strings.Contains(body, "aguarda a decisão") || !strings.Contains(body, `"id":7`) {
		t.Fatalf("resposta sintética: %d %s", code, body)
	}
	if upstreamHits.Load() != 1 {
		t.Fatal("a remoção não pode chegar ao ai-memory")
	}
	ps, err := svc.ListMemoryProposals(ctx, co.ID, []domain.MemoryProposalStatus{domain.ProposalPending}, 10)
	if err != nil || len(ps) != 1 {
		t.Fatalf("propostas: %+v %v", ps, err)
	}
	p := ps[0]
	if p.Tool != "memory_delete_page" || p.WorkspaceSlug != "site" || p.GateID == nil || len(p.Targets) != 1 || p.Targets[0] != "wiki/velha" {
		t.Fatalf("proposta: %+v", p)
	}
	g, err := svc.GetGate(ctx, co.ID, *p.GateID)
	if err != nil || g.Kind != domain.GateMemory || g.Status != domain.GatePending {
		t.Fatalf("gate: %+v %v", g, err)
	}

	// o handoff não cabe numa curadoria
	code, body = get(t, ts.URL, "POST", "/memory-mcp", "", tok,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"memory_handoff_accept","arguments":{}}}`)
	if code != 200 || !strings.Contains(body, `"error"`) || upstreamHits.Load() != 1 {
		t.Fatalf("handoff: %d %s (upstream=%d)", code, body, upstreamHits.Load())
	}
}
