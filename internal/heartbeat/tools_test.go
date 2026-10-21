package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// As integrações MCP http da run apontam para o gateway de tools do Hanko, com o run token (ADR-0036).
func TestBuildSpecRoutesHTTPIntegrationsThroughToolGateway(t *testing.T) {
	h, _ := newTestHB(t, &fakeBackend{}, okAdapter(), &mcpMem{}, Options{PublicURL: "http://h:7420"})
	rc := sampleContext()
	spec, cleanup, err := h.buildSpec(rc, "", "", memory.Scope{Workspace: "acme"}, service.RunExtras{ToolGateway: []string{"docs"}},
		func(domain.RunEventKind, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	srv := mcpServers(t, spec)
	docs := srv["docs"]
	if docs["url"] != "http://h:7420/mcp/run/docs" || docs["type"] != "http" {
		t.Fatalf("docs: %s", spec.MCPConfig)
	}
	hdr, _ := docs["headers"].(map[string]any)
	auth, _ := hdr["Authorization"].(string)
	hankoHdr, _ := srv["hanko"]["headers"].(map[string]any)
	if !strings.HasPrefix(auth, "Bearer ") || auth != hankoHdr["Authorization"] {
		t.Fatalf("o gateway usa o run token: %q vs %v", auth, hankoHdr)
	}
}

func TestToolGatewayMCPWithoutToken(t *testing.T) {
	if got := toolGatewayMCP("http://h", "", []string{"docs"}, false); got != nil {
		t.Fatalf("sem token não há gateway: %v", got)
	}
	if got := toolGatewayMCP("http://h", "tok", []string{"a b"}, false); !strings.Contains(string(got["a b"]), "/mcp/run/a%20b") {
		t.Fatalf("slug escapado: %s", got["a b"])
	}
}

// Com HANKO_TOOLS_SINGLE_ENDPOINT o harness recebe um só servidor para as integrações (ADR-0053).
func TestToolGatewayMCPSingleEndpoint(t *testing.T) {
	got := toolGatewayMCP("http://h", "tok", []string{"docs", "jira"}, true)
	if len(got) != 1 {
		t.Fatalf("um servidor só: %v", got)
	}
	var e struct {
		URL     string            `json:"url"`
		Type    string            `json:"type"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(got["hanko-tools"], &e); err != nil || e.URL != "http://h/mcp/run" || e.Type != "http" || e.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("hanko-tools: %s %v", got["hanko-tools"], err)
	}
	if toolGatewayMCP("http://h", "tok", nil, true) != nil {
		t.Fatal("sem integração não há servidor")
	}
}

// O executor liga a política de tools ao harness conforme o agente: hook no claude, guarda no ACP.
func TestAttachToolPolicyPerHarness(t *testing.T) {
	emitted := []string{}
	emit := func(_ domain.RunEventKind, s string) { emitted = append(emitted, s) }
	rc := sampleContext()

	h, _ := newTestHB(t, &fakeBackend{}, okAdapter(), &mcpMem{}, Options{ToolHookBin: "/opt/hanko"})
	spec := adapter.Spec{Agent: "claude"}
	h.attachToolPolicy(&spec, rc, emit)
	if spec.ToolHookBin != "/opt/hanko" || spec.ToolGuard != nil {
		t.Fatalf("claude: %+v", spec)
	}
	// sem o executável configurado usa o do próprio processo
	h2, _ := newTestHB(t, &fakeBackend{}, okAdapter(), &mcpMem{}, Options{})
	spec = adapter.Spec{Agent: "claude"}
	h2.attachToolPolicy(&spec, rc, emit)
	if spec.ToolHookBin == "" {
		t.Fatal("deveria usar os.Executable")
	}
	// ACP: o guarda só existe se o backend sabe decidir tools nativas
	spec = adapter.Spec{Agent: "acp"}
	h.attachToolPolicy(&spec, rc, emit)
	if spec.ToolGuard != nil || spec.ToolHookBin != "" {
		t.Fatalf("backend sem política nativa: %+v", spec)
	}
	h.svc = &nativeFake{fakeBackend: &fakeBackend{}}
	h.attachToolPolicy(&spec, rc, emit)
	if spec.ToolGuard == nil {
		t.Fatal("acp deveria receber o guarda")
	}
	// harness sem gancho: avisa no log da run
	spec = adapter.Spec{Agent: "codex"}
	h.attachToolPolicy(&spec, rc, emit)
	if spec.ToolGuard != nil || spec.ToolHookBin != "" || len(emitted) != 1 || !strings.Contains(emitted[0], "codex") {
		t.Fatalf("codex: %+v %v", spec, emitted)
	}
}

type nativeFake struct {
	*fakeBackend
	decision service.NativeToolDecision
	awaited  service.NativeToolDecision
	err      error
	checks   []service.NativeToolRequest
}

func (n *nativeFake) RunToolCaller(context.Context, domain.ID, domain.ID) (service.ToolCaller, error) {
	return service.ToolCaller{}, n.err
}

func (n *nativeFake) NativeToolCheck(_ context.Context, c service.ToolCaller, req service.NativeToolRequest) (service.NativeToolDecision, error) {
	n.checks = append(n.checks, req)
	if c.Source != domain.SourceACP {
		return service.NativeToolDecision{}, errors.New("a origem deveria ser acp")
	}
	return n.decision, nil
}

func (n *nativeFake) AwaitNativeToolGate(context.Context, service.ToolCaller, domain.ID) (service.NativeToolDecision, error) {
	return n.awaited, nil
}

func TestNativeGuardMapsDecisions(t *testing.T) {
	gid := domain.NewID()
	nf := &nativeFake{fakeBackend: &fakeBackend{}}
	g := &nativeGuard{be: nf, company: domain.NewID(), run: domain.NewID()}
	req := adapter.ToolRequest{Name: "execute", Title: "rodar", Input: json.RawMessage(`{"c":1}`)}
	cases := []struct {
		name    string
		decided service.NativeToolDecision
		awaited service.NativeToolDecision
		err     error
		want    adapter.ToolDecision
	}{
		{"sem opinião", service.NativeToolDecision{Decision: service.NativeNone}, service.NativeToolDecision{}, nil, adapter.ToolNoOpinion},
		{"allow", service.NativeToolDecision{Decision: service.NativeAllow}, service.NativeToolDecision{}, nil, adapter.ToolAllow},
		{"deny", service.NativeToolDecision{Decision: service.NativeDeny, Reason: "off"}, service.NativeToolDecision{}, nil, adapter.ToolDeny},
		{"ask aprovado", service.NativeToolDecision{Decision: service.NativeAsk, GateID: &gid}, service.NativeToolDecision{Decision: service.NativeAllow}, nil, adapter.ToolAllow},
		{"ask recusado", service.NativeToolDecision{Decision: service.NativeAsk, GateID: &gid}, service.NativeToolDecision{Decision: service.NativeDeny, Reason: "não"}, nil, adapter.ToolDeny},
		{"falha do service nega", service.NativeToolDecision{}, service.NativeToolDecision{}, errors.New("banco fora"), adapter.ToolDeny},
	}
	for _, c := range cases {
		nf.decision, nf.awaited, nf.err = c.decided, c.awaited, c.err
		got := g.Check(context.Background(), req)
		if got.Decision != c.want {
			t.Errorf("%s: %+v, queria %q", c.name, got, c.want)
		}
	}
	if len(nf.checks) == 0 || nf.checks[0].Name != "execute" || nf.checks[0].Title != "rodar" {
		t.Fatalf("pedido repassado: %+v", nf.checks)
	}
}
