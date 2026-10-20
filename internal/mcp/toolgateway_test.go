package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// gwFake é o backend do gateway de tools: registra o que recebeu e responde conforme o nome da tool.
type gwFake struct {
	runStatus  domain.RunStatus
	runErr     error
	callerErr  error
	gotSlug    string
	gotCaller  service.ToolCaller
	gotArgs    string
	listErr    error
	namedToken string
}

func (f *gwFake) GetRun(_ context.Context, _, rid domain.ID) (service.RunView, error) {
	var v service.RunView
	v.ID, v.Status = rid, f.runStatus
	return v, nil
}

func (f *gwFake) RunToolCaller(_ context.Context, cid, rid domain.ID) (service.ToolCaller, error) {
	if f.runErr != nil {
		return service.ToolCaller{}, f.runErr
	}
	pid := domain.NewID()
	return service.ToolCaller{CompanyID: cid, RunID: &rid, PersonaID: &pid}, nil
}

func (f *gwFake) GatewayCaller(_ context.Context, company, gateway, token string) (service.ToolCaller, error) {
	if f.callerErr != nil {
		return service.ToolCaller{}, f.callerErr
	}
	if token != f.namedToken || company != "acme" || gateway != "ide" {
		return service.ToolCaller{}, domain.ErrUnauthenticated
	}
	gid := domain.NewID()
	return service.ToolCaller{GatewayID: &gid, GatewaySlug: gateway, Actions: []string{domain.GatewayActionList}}, nil
}

func (f *gwFake) GatewayListTools(_ context.Context, c service.ToolCaller, slug string) ([]service.GatewayTool, error) {
	f.gotSlug, f.gotCaller = slug, c
	if f.listErr != nil {
		return nil, f.listErr
	}
	ro := true
	return []service.GatewayTool{
		{Name: "ler", Description: "lê", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			Annotations: domain.ToolAnnotations{ReadOnlyHint: &ro}},
		{Name: "apagar", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}, nil
}

func (f *gwFake) GatewayCallTool(_ context.Context, c service.ToolCaller, slug, name string, args json.RawMessage) (service.GatewayCallResult, error) {
	f.gotSlug, f.gotCaller, f.gotArgs = slug, c, string(args)
	switch name {
	case "ler":
		return service.GatewayCallResult{Decision: domain.ToolDecisionAllow,
			Result: json.RawMessage(`{"content":[{"type":"text","text":"resultado remoto"}]}`)}, nil
	case "apagar":
		gid := domain.NewID()
		return service.GatewayCallResult{Decision: domain.ToolDecisionAsk, GateID: &gid, Message: "aguardando aprovação: gate " + gid.String()}, nil
	case "proibida":
		return service.GatewayCallResult{Decision: domain.ToolDecisionDeny, Unknown: true, Message: `tool "proibida" não está disponível`}, nil
	}
	return service.GatewayCallResult{}, domain.ErrForbidden
}

func gwSetup(t *testing.T, f *gwFake) (*httptest.Server, auth.RunTokens) {
	t.Helper()
	toks := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef")}
	mux := http.NewServeMux()
	d := ToolGatewayDeps{Backend: f, Tokens: toks, Version: "test"}
	mux.Handle(RunToolGatewayPattern, RunToolGateway(d))
	mux.Handle(RunAggregateGatewayPattern, RunAggregateGateway(d))
	mux.Handle(NamedToolGatewayPattern, NamedToolGateway(d))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, toks
}

type bearerRT struct {
	tok  string
	next http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return b.next.RoundTrip(r)
}

func sdkSession(t *testing.T, endpoint, tok string) *sdk.ClientSession {
	t.Helper()
	c := sdk.NewClient(&sdk.Implementation{Name: "agente", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: endpoint,
		HTTPClient: &http.Client{Transport: bearerRT{tok: tok, next: http.DefaultTransport}}, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// O cliente MCP do SDK fala com o gateway da run: initialize, tools/list e tools/call (allow, ask e excluída).
func TestRunToolGatewayWithSDKClient(t *testing.T) {
	f := &gwFake{runStatus: domain.RunRunning}
	srv, toks := gwSetup(t, f)
	tok, err := toks.Issue(domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	cs := sdkSession(t, srv.URL+"/mcp/run/docs", tok)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 2 || list.Tools[0].Name != "ler" || list.Tools[0].Annotations == nil || !list.Tools[0].Annotations.ReadOnlyHint {
		t.Fatalf("list: %+v %v", list, err)
	}
	if f.gotSlug != "docs" || f.gotCaller.RunID == nil {
		t.Fatalf("slug/caller: %q %+v", f.gotSlug, f.gotCaller)
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "ler", Arguments: map[string]any{"q": "x"}})
	if err != nil || res.IsError || res.Content[0].(*sdk.TextContent).Text != "resultado remoto" || f.gotArgs != `{"q":"x"}` {
		t.Fatalf("call ler: %+v %v (args %s)", res, err, f.gotArgs)
	}
	res, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "apagar", Arguments: map[string]any{}})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*sdk.TextContent).Text, "aguardando aprovação") {
		t.Fatalf("call ask: %+v %v", res, err)
	}
	if _, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "proibida"}); err == nil || !strings.Contains(err.Error(), "não está disponível") {
		t.Fatalf("call excluída: %v", err)
	}
}

func post(t *testing.T, url, tok, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRunToolGatewayAuthAndProtocol(t *testing.T) {
	f := &gwFake{runStatus: domain.RunRunning}
	srv, toks := gwSetup(t, f)
	url := srv.URL + "/mcp/run/docs"
	tok, _ := toks.Issue(domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID())
	const list = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	if code, _ := post(t, url, "", list); code != http.StatusUnauthorized {
		t.Errorf("sem token: %d", code)
	}
	if code, _ := post(t, url, "hk_apikey", list); code != http.StatusUnauthorized {
		t.Errorf("API key: %d", code)
	}
	if code, _ := post(t, url, "hkgw_x", list); code != http.StatusUnauthorized {
		t.Errorf("token de gateway na rota da run: %d", code)
	}
	if code, _ := post(t, url, "lixo", list); code != http.StatusUnauthorized {
		t.Errorf("token inválido: %d", code)
	}
	f.runStatus = domain.RunDone
	if code, _ := post(t, url, tok, list); code != http.StatusUnauthorized {
		t.Errorf("run encerrada: %d", code)
	}
	f.runStatus = domain.RunRunning
	f.runErr = domain.ErrNotAllowed
	if code, _ := post(t, url, tok, list); code != http.StatusForbidden {
		t.Errorf("run de sistema: %d", code)
	}
	f.runErr = nil

	// GET não abre stream
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %v %v", resp, err)
	}
	resp.Body.Close()

	// notificação: 202 sem corpo; lote com notificação e pedido
	if code, body := post(t, url, tok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != http.StatusAccepted || body != "" {
		t.Errorf("notificação: %d %q", code, body)
	}
	code, body := post(t, url, tok, `[{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":7,"method":"ping"}]`)
	if code != http.StatusOK || !strings.Contains(body, `"id":7`) || strings.Count(body, `"jsonrpc"`) != 1 {
		t.Errorf("lote: %d %s", code, body)
	}
	// initialize ecoa a versão conhecida; método desconhecido
	_, body = post(t, url, tok, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	if !strings.Contains(body, `"protocolVersion":"2025-03-26"`) || !strings.Contains(body, `"tools"`) {
		t.Errorf("initialize: %s", body)
	}
	_, body = post(t, url, tok, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	if !strings.Contains(body, "-32601") {
		t.Errorf("método desconhecido: %s", body)
	}
	// integração que a persona não tem
	f.listErr = domain.ErrNotFound
	_, body = post(t, url, tok, list)
	if !strings.Contains(body, "-32602") || !strings.Contains(body, "integração não disponível") {
		t.Errorf("integração ausente: %s", body)
	}
	f.listErr = nil
	// tools/call sem name
	_, body = post(t, url, tok, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`)
	if !strings.Contains(body, "-32602") {
		t.Errorf("call sem name: %s", body)
	}
}

func TestNamedToolGatewayTokenAndActions(t *testing.T) {
	f := &gwFake{namedToken: "hkgw_valido"}
	srv, _ := gwSetup(t, f)
	url := srv.URL + "/mcp/gw/acme/ide"
	if code, _ := post(t, url, "hkgw_outro", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != http.StatusUnauthorized {
		t.Errorf("token errado: %d", code)
	}
	if code, _ := post(t, srv.URL+"/mcp/gw/acme/outro", "hkgw_valido", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != http.StatusUnauthorized {
		t.Errorf("gateway errado: %d", code)
	}
	code, body := post(t, url, "hkgw_valido", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if code != http.StatusOK || !strings.Contains(body, `"ler"`) || f.gotSlug != "" || f.gotCaller.GatewayID == nil {
		t.Fatalf("list: %d %s (%q)", code, body, f.gotSlug)
	}
	// ação não permitida pelo token: o service devolve ErrForbidden
	_, body = post(t, url, "hkgw_valido", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"x__y"}}`)
	if !strings.Contains(body, "não permite") {
		t.Fatalf("ação proibida: %s", body)
	}
	f.callerErr = errBoom{}
	if code, _ := post(t, url, "hkgw_valido", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != http.StatusServiceUnavailable {
		t.Errorf("backend fora: %d", code)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

// O endpoint único da run (ADR-0053) autentica pelo run token como o de uma integração, mas pede ao service o chamador
// agregado: sem integração na rota e com os nomes prefixados.
func TestAggregateRunGateway(t *testing.T) {
	f := &gwFake{runStatus: domain.RunRunning}
	srv, toks := gwSetup(t, f)
	tok, err := toks.Issue(domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	cs := sdkSession(t, srv.URL+"/mcp/run", tok)
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.gotSlug != "" || !f.gotCaller.Aggregate || f.gotCaller.RunID == nil || f.gotCaller.Named() {
		t.Fatalf("chamador agregado: slug=%q %+v", f.gotSlug, f.gotCaller)
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "ler", Arguments: map[string]any{}})
	if err != nil || res.IsError || f.gotSlug != "" || !f.gotCaller.Aggregate {
		t.Fatalf("call: %+v %v (slug %q)", res, err, f.gotSlug)
	}
	// o endpoint por integração segue sem o modo agregado
	cs2 := sdkSession(t, srv.URL+"/mcp/run/docs", tok)
	if _, err := cs2.ListTools(context.Background(), nil); err != nil || f.gotCaller.Aggregate || f.gotSlug != "docs" {
		t.Fatalf("por integração: %v %+v", err, f.gotCaller)
	}
	// sem token e com token de gateway nomeado: 401, como no endpoint por integração
	if code, _ := post(t, srv.URL+"/mcp/run", "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 401 {
		t.Fatalf("sem token: %d", code)
	}
	if code, _ := post(t, srv.URL+"/mcp/run", "hkgw_abcdef123456", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 401 {
		t.Fatalf("token de gateway: %d", code)
	}
	if c, a := f.gotCaller.Prefixed(), f.gotCaller.Aggregate; c || a {
		t.Fatal("o chamador por integração não é prefixado")
	}
}
