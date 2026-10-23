//go:build integration

package heartbeat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/mcp"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// Demo do M9 (ADR-0053), ponta a ponta com um adapter fake que faz o papel do harness: ele lê o mcpServers que o
// executor montou e chama as tools pelo gateway do Hanko por HTTP, com o run token. A persona tenta uma tool em ask,
// um admin aprova "sempre", a segunda chamada passa direto e tudo fica no log da conexão.

// toolAgent é o harness de mentira: executa as chamadas listadas em adapter_config.calls pelo mcpServers da run.
type toolAgent struct {
	mu      sync.Mutex
	specs   []adapter.Spec
	results map[string][]string // por run: o texto de cada resposta
}

type agentCall struct {
	Server string          `json:"server"`
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
}

func (a *toolAgent) Name() string { return "fake" }

func (a *toolAgent) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	a.mu.Lock()
	a.specs = append(a.specs, spec)
	if a.results == nil {
		a.results = map[string][]string{}
	}
	a.mu.Unlock()
	var cfg struct {
		Calls []agentCall `json:"calls"`
	}
	if err := json.Unmarshal(spec.Config, &cfg); err != nil {
		return adapter.Result{IsError: true, Error: err.Error()}, err
	}
	var mcpCfg struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(spec.MCPConfig, &mcpCfg); err != nil {
		return adapter.Result{IsError: true, Error: err.Error()}, err
	}
	for i, c := range cfg.Calls {
		srv, ok := mcpCfg.Servers[c.Server]
		if !ok {
			err := fmt.Errorf("servidor %q fora do mcpServers", c.Server)
			return adapter.Result{IsError: true, Error: err.Error()}, err
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call",
			"params": map[string]any{"name": c.Tool, "arguments": json.RawMessage(c.Args)}})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range srv.Headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return adapter.Result{IsError: true, Error: err.Error()}, err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var rpc struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &rpc)
		text := ""
		switch {
		case rpc.Error != nil:
			text = "erro: " + rpc.Error.Message
		case len(rpc.Result.Content) > 0:
			text = rpc.Result.Content[0].Text
		}
		a.mu.Lock()
		a.results[spec.RunID.String()] = append(a.results[spec.RunID.String()], text)
		a.mu.Unlock()
		_ = sink.Emit(ctx, adapter.Event{Kind: domain.EventToolResult, Text: text, OccurredAt: time.Now().UTC()})
	}
	return adapter.Result{Summary: "feito", Model: "fake", SessionRef: "fake-" + spec.RunID.String()}, nil
}

func (a *toolAgent) runResults(run domain.ID) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.results[run.String()]...)
}

func (a *toolAgent) lastSpec() adapter.Spec {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.specs[len(a.specs)-1]
}

type upstream struct {
	hs    *httptest.Server
	mu    sync.Mutex
	calls []string
}

func (u *upstream) log() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.calls...)
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{}
	srv := sdk.NewServer(&sdk.Implementation{Name: "upstream", Version: "1"}, nil)
	for _, tl := range []struct {
		name string
		ann  *sdk.ToolAnnotations
	}{{"apagar", &sdk.ToolAnnotations{DestructiveHint: boolPtr(true)}}, {"ler", &sdk.ToolAnnotations{ReadOnlyHint: true}}} {
		name := tl.name
		srv.AddTool(&sdk.Tool{Name: name, Description: "tool " + name, InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: tl.ann},
			func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				u.mu.Lock()
				u.calls = append(u.calls, name+" "+string(req.Params.Arguments))
				u.mu.Unlock()
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok " + name}}}, nil
			})
	}
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	u.hs = httptest.NewServer(h)
	t.Cleanup(u.hs.Close)
	return u
}

func boolPtr(b bool) *bool { return &b }

type toolStack struct {
	svc   *service.Service
	co    domain.Company
	agent *toolAgent
	up    *upstream
	integ service.IntegrationView
}

func newToolStack(t *testing.T, single bool) *toolStack {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// o servidor do Hanko (gateway de tools) precisa existir antes do heartbeat, que guarda a URL pública
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	srv.Listener.Close()
	srv.Listener = ln
	broker := events.NewBroker()
	hb, err := New(pool, Options{Workers: 2, PeriodicInterval: time.Hour, RunTimeout: time.Minute,
		PublicURL: "http://" + ln.Addr().String(), ToolsSingleEndpoint: single})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-m9", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations = service.IntegrationsConfig{Secrets: box, AllowPrivate: true}
	agent := &toolAgent{}
	hb.Attach(svc, adapter.Registry{"fake": agent}, memory.Noop{}, broker, authForTest())

	deps := mcp.ToolGatewayDeps{Backend: svc, Tokens: authForTest(), Version: "test"}
	mux := http.NewServeMux()
	mux.Handle(mcp.RunToolGatewayPattern, mcp.RunToolGateway(deps))
	mux.Handle(mcp.RunAggregateGatewayPattern, mcp.RunAggregateGateway(deps))
	srv.Config.Handler = mux
	srv.Start()
	t.Cleanup(srv.Close)

	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	up := newUpstream(t)
	integ, err := svc.CreateIntegration(ctx, domain.HumanActor, co.ID, service.CreateIntegrationInput{Kind: domain.IntegrationMCP, Name: "Docs",
		URL: up.hs.URL + "/mcp", AllPersonas: true, Headers: map[string]string{"Authorization": "Bearer up-secret-token-123456"}})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := svc.RefreshIntegrationTools(ctx, domain.HumanActor, co.ID, integ.ID); err != nil || !res.OK {
		t.Fatalf("refresh: %+v %v", res, err)
	}
	// integração nova: a primeira descoberta põe a escrita em quarentena (ADR-0067); o admin revisa e libera
	if _, err := svc.ReviewQuarantinedTools(ctx, domain.HumanActor, co.ID, integ.ID, nil); err != nil {
		t.Fatalf("revisar: %v", err)
	}
	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})
	return &toolStack{svc: svc, co: co, agent: agent, up: up, integ: integ}
}

func (s *toolStack) hire(t *testing.T, name string, calls ...agentCall) domain.Persona {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"calls": calls})
	p, err := s.svc.HirePersona(context.Background(), domain.HumanActor, s.co.ID, service.HirePersonaInput{Name: name, Agent: "fake", AdapterConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// task cria uma task para a persona e espera a primeira run dela terminar (ou a n-ésima, se já houver).
func (s *toolStack) task(t *testing.T, p domain.Persona, title string) service.TaskView {
	t.Helper()
	task, err := s.svc.CreateTask(context.Background(), domain.HumanActor, s.co.ID, service.CreateTaskInput{Title: title, PersonaID: &p.ID, Status: domain.TaskTodo})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (s *toolStack) doneRuns(t *testing.T, task domain.ID, want int) []service.RunView {
	t.Helper()
	var runs []service.RunView
	waitFor(t, 30*time.Second, fmt.Sprintf("%d run(s) terminarem", want), func() bool {
		rs, err := s.svc.ListRuns(context.Background(), s.co.ID, service.RunFilter{TaskID: &task})
		if err != nil || len(rs) < want {
			return false
		}
		for _, r := range rs {
			if !r.Status.Terminal() {
				return false
			}
		}
		runs = rs
		return true
	})
	return runs
}

func TestToolPolicyAskAlwaysAllowEndToEnd(t *testing.T) {
	s := newToolStack(t, false)
	ctx := context.Background()
	ana := s.hire(t, "Ana", agentCall{Server: "docs", Tool: "apagar", Args: json.RawMessage(`{"x":1}`)})
	iid := s.integ.ID
	if _, err := s.svc.SetToolPolicy(ctx, domain.HumanActor, s.co.ID, service.ToolPolicyInput{Scope: domain.PolicyScopePersona, PersonaID: &ana.ID,
		Target: domain.PolicyTargetTool, IntegrationID: &iid, ToolName: "apagar", Mode: domain.PolicyAsk}); err != nil {
		t.Fatal(err)
	}

	// 1ª run: a tool pede aprovação; o servidor não é chamado
	t1 := s.task(t, ana, "apagar o rascunho")
	runs := s.doneRuns(t, t1.ID, 1)
	res := s.agent.runResults(runs[0].ID)
	if len(res) != 1 || !strings.Contains(res[0], "aguardando aprovação") {
		t.Fatalf("1ª run: %q", res)
	}
	if got := s.up.log(); len(got) != 0 {
		t.Fatalf("ask chamou o servidor: %v", got)
	}
	// o harness recebeu o servidor do gateway (sem a URL nem o segredo do servidor de verdade)
	mc := string(s.agent.lastSpec().MCPConfig)
	if !strings.Contains(mc, "/mcp/run/docs") || strings.Contains(mc, s.up.hs.URL) || strings.Contains(mc, "up-secret-token") {
		t.Fatalf("mcpServers: %s", mc)
	}
	gs, err := s.svc.ListGates(ctx, s.co.ID, service.GateFilter{Kinds: []domain.GateKind{domain.GateTool}, Statuses: []domain.GateStatus{domain.GatePending}})
	if err != nil || len(gs) != 1 {
		t.Fatalf("gates: %v %v", gs, err)
	}

	// o humano aprova "sempre": a persona é acordada na task e repete a chamada, que passa
	if _, err := s.svc.DecideGate(ctx, domain.HumanActor, s.co.ID, gs[0].ID, service.DecideGateInput{Decision: domain.GateApproved, Always: true}); err != nil {
		t.Fatal(err)
	}
	runs = s.doneRuns(t, t1.ID, 2)
	passed := 0
	for _, r := range runs {
		for _, x := range s.agent.runResults(r.ID) {
			if x == "ok apagar" {
				passed++
			}
		}
	}
	if passed != 1 || len(s.up.log()) != 1 {
		t.Fatalf("depois de aprovar: passou %d, servidor %v", passed, s.up.log())
	}

	// 2ª task: a chamada passa direto, sem gate novo
	t2 := s.task(t, ana, "apagar outro rascunho")
	runs2 := s.doneRuns(t, t2.ID, 1)
	if res := s.agent.runResults(runs2[0].ID); len(res) != 1 || res[0] != "ok apagar" {
		t.Fatalf("2ª task: %q", res)
	}
	if n := len(s.up.log()); n != 2 {
		t.Fatalf("servidor recebeu %d chamadas", n)
	}
	all, _ := s.svc.ListGates(ctx, s.co.ID, service.GateFilter{Kinds: []domain.GateKind{domain.GateTool}})
	if len(all) != 1 {
		t.Fatalf("um gate só em todo o fluxo, vieram %d", len(all))
	}

	// o log da conexão conta a história: pedido (ask) e, daí em diante, direto pela política que o "sempre" gravou
	inv, err := s.svc.ListToolInvocations(ctx, s.co.ID, service.ToolInvocationFilter{IntegrationID: &iid})
	if err != nil || len(inv) != 3 {
		t.Fatalf("auditoria: %d %v", len(inv), err)
	}
	newest, mid, oldest := inv[0], inv[1], inv[2]
	if oldest.Decision != domain.ToolDecisionAsk || oldest.Status != domain.ToolCallPending ||
		mid.Decision != domain.ToolDecisionAllow || mid.GateID != nil || mid.PolicySource != "policy:persona" ||
		newest.Decision != domain.ToolDecisionAllow || newest.GateID != nil || newest.PolicySource != "policy:persona" {
		t.Fatalf("sequência: %+v", inv)
	}
	for _, i := range inv {
		if i.Source != domain.SourceRun || i.Actor == nil || i.Actor.Type != domain.ActorPersona || i.PersonaID == nil || *i.PersonaID != ana.ID || i.RunID == nil {
			t.Fatalf("ator/origem: %+v", i)
		}
		if i.Decision == domain.ToolDecisionAllow && (i.ResultPreview != "ok apagar" || i.Status != domain.ToolCallOK) {
			t.Fatalf("resultado: %+v", i)
		}
	}
}

// Com HANKO_TOOLS_SINGLE_ENDPOINT o harness recebe um só servidor MCP para as integrações; a política vale igual.
func TestSingleEndpointRoutesThroughPolicy(t *testing.T) {
	s := newToolStack(t, true)
	ctx := context.Background()
	ana := s.hire(t, "Ana",
		agentCall{Server: "hanko-tools", Tool: "docs__ler", Args: json.RawMessage(`{}`)},
		agentCall{Server: "hanko-tools", Tool: "docs__apagar", Args: json.RawMessage(`{"x":2}`)},
		agentCall{Server: "hanko-tools", Tool: "ler", Args: json.RawMessage(`{}`)})
	iid := s.integ.ID
	if _, err := s.svc.SetToolPolicy(ctx, domain.HumanActor, s.co.ID, service.ToolPolicyInput{Scope: domain.PolicyScopeCompany,
		Target: domain.PolicyTargetTool, IntegrationID: &iid, ToolName: "apagar", Mode: domain.PolicyOff}); err != nil {
		t.Fatal(err)
	}
	task := s.task(t, ana, "usar o endpoint único")
	runs := s.doneRuns(t, task.ID, 1)
	res := s.agent.runResults(runs[0].ID)
	if len(res) != 3 || res[0] != "ok ler" || !strings.Contains(res[1], "não está disponível") || !strings.Contains(res[2], "não está disponível") {
		t.Fatalf("resultados: %q", res)
	}
	mc := string(s.agent.lastSpec().MCPConfig)
	if !strings.Contains(mc, `"hanko-tools"`) || !strings.Contains(mc, "/mcp/run\"") || strings.Contains(mc, "/mcp/run/docs") {
		t.Fatalf("mcpServers do endpoint único: %s", mc)
	}
	if got := s.up.log(); len(got) != 1 || !strings.HasPrefix(got[0], "ler") {
		t.Fatalf("servidor: %v", got)
	}
}

// A varredura periódica vence os gates tool sem decisão no prazo (ADR-0053).
func TestMemorySweepExpiresToolGates(t *testing.T) {
	s := newToolStack(t, false)
	ctx := context.Background()
	ana := s.hire(t, "Ana", agentCall{Server: "docs", Tool: "apagar", Args: json.RawMessage(`{}`)})
	iid := s.integ.ID
	if _, err := s.svc.SetToolPolicy(ctx, domain.HumanActor, s.co.ID, service.ToolPolicyInput{Scope: domain.PolicyScopeCompany,
		Target: domain.PolicyTargetIntegration, IntegrationID: &iid, Mode: domain.PolicyAsk}); err != nil {
		t.Fatal(err)
	}
	task := s.task(t, ana, "pedir aprovação")
	s.doneRuns(t, task.ID, 1)
	if _, err := s.svc.Pool.Exec(ctx, `UPDATE gates SET expires_at = now() - interval '1 minute' WHERE kind = 'tool'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.svc.ExpireToolGates(ctx); err != nil || n != 1 {
		t.Fatalf("expirar: %d %v", n, err)
	}
	gs, _ := s.svc.ListGates(ctx, s.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GateExpired}})
	if len(gs) != 1 || gs[0].Kind != domain.GateTool {
		t.Fatalf("expirados: %+v", gs)
	}
}
