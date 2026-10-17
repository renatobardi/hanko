//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/mcp"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// apiUpstream sobe um servidor MCP de verdade (SDK) com uma tool de leitura e uma destrutiva.
func apiUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := sdk.NewServer(&sdk.Implementation{Name: "up", Version: "1"}, nil)
	destructive := true
	for name, ann := range map[string]*sdk.ToolAnnotations{"ler": {ReadOnlyHint: true}, "apagar": {DestructiveHint: &destructive}} {
		n := name
		srv.AddTool(&sdk.Tool{Name: n, InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: ann},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "feito " + n}}}, nil
			})
	}
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	hs := httptest.NewServer(h)
	t.Cleanup(hs.Close)
	return hs
}

type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

func TestAPIToolsCatalogProfilesGatewaysAndAudit(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{AllowPrivate: true, Secrets: box}
	up := apiUpstream(t)
	ana := e.persona(e.co, "Ana", nil)
	base := e.base()
	e.want(201, "POST", base+"/integrations", `{"kind":"mcp","name":"Docs","url":"`+up.URL+`/mcp","all_personas":true,"headers":{"Authorization":"Bearer up-123456789"}}`, nil)

	// catálogo: vazio até a descoberta; a descoberta é a linha de base
	var cat api.ToolCatalog
	e.want(200, "GET", base+"/integrations/docs/tools", "", &cat)
	if len(cat.Tools) != 0 || cat.SyncedAt != nil || cat.Stdio {
		t.Fatalf("catálogo vazio: %+v", cat)
	}
	var ref api.ToolCatalogRefresh
	e.want(200, "POST", base+"/integrations/docs/tools/refresh", "", &ref)
	if !ref.OK || !ref.Baseline || ref.Tools != 2 {
		t.Fatalf("refresh: %+v", ref)
	}
	e.want(200, "GET", base+"/integrations/docs/tools", "", &cat)
	if len(cat.Tools) != 2 || cat.Tools[0].Name != "apagar" || cat.Tools[0].Risk != "destructive" || cat.Tools[1].Risk != "read" ||
		cat.Tools[1].InputSchema["type"] != "object" {
		t.Fatalf("catálogo: %+v", cat.Tools)
	}
	var tl api.IntegrationTool
	e.want(200, "PATCH", base+"/integrations/docs/tools/apagar", `{"status":"disabled"}`, &tl)
	if tl.Status != "disabled" || tl.ReviewedAt == nil {
		t.Fatalf("desligar: %+v", tl)
	}
	e.want(422, "PATCH", base+"/integrations/docs/tools/apagar", `{"status":"removed"}`, nil)
	e.want(404, "PATCH", base+"/integrations/docs/tools/nao-existe", `{"status":"active"}`, nil)
	e.want(200, "PATCH", base+"/integrations/docs/tools/apagar", `{"status":"active"}`, nil)
	var rv api.ReviewResult
	e.want(200, "POST", base+"/integrations/docs/tools/review", `{}`, &rv)

	// perfis: CRUD, regras, escolhas por integração e vínculos
	var prof api.ToolProfile
	e.want(201, "POST", base+"/tool-profiles", `{"name":"Seguro"}`, &prof)
	if prof.Slug != "seguro" || prof.DefaultAction != "deny" || prof.RateLimitPerMin != 120 {
		t.Fatalf("perfil: %+v", prof)
	}
	e.want(409, "POST", base+"/tool-profiles", `{"name":"Seguro"}`, nil)
	e.want(200, "PUT", base+"/tool-profiles/seguro/integrations/docs", `{"group":"allow","tools":{"apagar":"ask"}}`, &prof)
	if len(prof.Rules) != 2 {
		t.Fatalf("regras das escolhas: %+v", prof.Rules)
	}
	var rule api.ToolRule
	e.want(201, "POST", base+"/tool-profiles/seguro/rules", `{"target":"risk","risk":"unknown","effect":"exclude"}`, &rule)
	e.want(422, "POST", base+"/tool-profiles/seguro/rules", `{"target":"tool","integration":"docs","effect":"include"}`, nil)
	e.want(204, "DELETE", base+"/tool-profiles/seguro/rules/"+rule.ID, "", nil)
	e.want(200, "PATCH", base+"/tool-profiles/seguro", `{"rate_limit_per_min":30,"description":"o padrão"}`, &prof)
	if prof.RateLimitPerMin != 30 {
		t.Fatalf("patch: %+v", prof)
	}
	var b api.ToolBinding
	e.want(201, "POST", base+"/tool-profiles/seguro/bindings", `{"scope":"persona","persona":"ana"}`, &b)
	if b.PersonaID != ana.ID.String() || b.TargetName != "Ana" {
		t.Fatalf("vínculo: %+v", b)
	}
	e.want(422, "POST", base+"/tool-profiles/seguro/bindings", `{"scope":"project"}`, nil)
	e.want(404, "POST", base+"/tool-profiles/seguro/bindings", `{"scope":"persona","persona":"ninguem"}`, nil)

	var eff api.EffectiveTools
	e.want(200, "GET", base+"/tool-profiles/effective?persona=ana", "", &eff)
	if eff.Profile != "seguro" || eff.Binding == nil || len(eff.Integrations) != 1 || len(eff.Integrations[0].Tools) != 2 {
		t.Fatalf("efetivas: %+v", eff)
	}
	for _, x := range eff.Integrations[0].Tools {
		if x.Name == "apagar" && (x.Verdict.Mode != "ask" || x.Verdict.Source != "include" || x.Verdict.RuleID == "") {
			t.Errorf("apagar: %+v", x)
		}
	}
	e.want(422, "GET", base+"/tool-profiles/effective", "", nil)

	// gateway nomeado: o token só na criação
	var gw api.ToolGateway
	e.want(201, "POST", base+"/tool-gateways", `{"name":"Minha IDE","profile":"seguro"}`, &gw)
	if gw.Endpoint != "/mcp/gw/acme/minha-ide" || gw.Profile != "seguro" {
		t.Fatalf("gateway: %+v", gw)
	}
	var tok api.GatewayToken
	e.want(201, "POST", base+"/tool-gateways/minha-ide/tokens", `{"label":"notebook"}`, &tok)
	if !strings.HasPrefix(tok.Token, "hkgw_") || !strings.HasPrefix(tok.Token, tok.Prefix) || len(tok.Actions) != 2 {
		t.Fatalf("token: %+v", tok)
	}
	raw := e.want(200, "GET", base+"/tool-gateways/minha-ide", "", &gw)
	if strings.Contains(string(raw), tok.Token) || len(gw.Tokens) != 1 || gw.ActiveTokens != 1 {
		t.Fatalf("leitura com o token: %s", raw)
	}
	e.want(422, "DELETE", base+"/tool-profiles/seguro", "", nil) // em uso pelo gateway
	e.want(422, "POST", base+"/tool-gateways/minha-ide/tokens", `{"actions":["tools/delete"]}`, nil)

	// o cliente MCP de fora usa o gateway nomeado: só as tools que o perfil deixa
	mux := http.NewServeMux()
	mux.Handle(mcp.NamedToolGatewayPattern, mcp.NamedToolGateway(mcp.ToolGatewayDeps{Backend: e.svc}))
	gs := httptest.NewServer(mux)
	t.Cleanup(gs.Close)
	c := sdk.NewClient(&sdk.Implementation{Name: "ide", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: gs.URL + gw.Endpoint,
		HTTPClient: &http.Client{Transport: bearer{tok.Token}}, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("conectar ao gateway: %v", err)
	}
	defer cs.Close()
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(lt.Tools) != 2 || lt.Tools[0].Name != "docs__apagar" {
		t.Fatalf("tools do gateway: %+v %v", lt, err)
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "docs__ler", Arguments: map[string]any{"q": "x"}})
	if err != nil || res.IsError || res.Content[0].(*sdk.TextContent).Text != "feito ler" {
		t.Fatalf("chamada pelo gateway: %+v %v", res, err)
	}
	res, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "docs__apagar", Arguments: map[string]any{}})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*sdk.TextContent).Text, "aguardando aprovação") {
		t.Fatalf("ask pelo gateway: %+v %v", res, err)
	}
	// o gate tool aparece na lista de gates
	var gates api.ListBody[api.Gate]
	e.want(200, "GET", base+"/gates?kind=tool", "", &gates)
	if len(gates.Items) != 1 || gates.Items[0].Kind != "tool" {
		t.Fatalf("gates tool: %+v", gates.Items)
	}

	// auditoria: filtros e sem argumentos
	var inv api.ListBody[api.ToolInvocation]
	e.want(200, "GET", base+"/tool-invocations?gateway=minha-ide", "", &inv)
	if len(inv.Items) != 2 || inv.Items[0].Gateway != "minha-ide" || inv.Items[0].ArgsSHA256 == "" {
		t.Fatalf("auditoria: %+v", inv.Items)
	}
	e.want(200, "GET", base+"/tool-invocations?decision=ask", "", &inv)
	if len(inv.Items) != 1 || inv.Items[0].Tool != "apagar" {
		t.Fatalf("filtro ask: %+v", inv.Items)
	}
	e.want(422, "GET", base+"/tool-invocations?since=ontem", "", nil)

	// revogar derruba o cliente
	e.want(200, "DELETE", base+"/tool-gateways/minha-ide/tokens/"+tok.ID, "", nil)
	if _, err := cs.ListTools(context.Background(), nil); err == nil {
		t.Fatal("token revogado ainda lista")
	}

	// run token (persona) não configura nada aqui
	hdr := e.personaToken(e.co, ana)
	for _, c := range []struct{ method, path, body string }{
		{"POST", base + "/integrations/docs/tools/refresh", ""},
		{"PATCH", base + "/integrations/docs/tools/ler", `{"status":"disabled"}`},
		{"POST", base + "/tool-profiles", `{"name":"X"}`},
		{"POST", base + "/tool-profiles/seguro/bindings", `{"scope":"company"}`},
		{"POST", base + "/tool-gateways", `{"name":"Y","profile":"seguro"}`},
		{"POST", base + "/tool-gateways/minha-ide/tokens", `{}`},
	} {
		if status, body := e.call(c.method, c.path, c.body, hdr); status != 403 {
			t.Errorf("run token %s %s: %d %s", c.method, c.path, status, body)
		}
	}

	// apagar: vínculo, gateway, perfil
	e.want(204, "DELETE", base+"/tool-profiles/seguro/bindings/"+b.ID, "", nil)
	e.want(204, "DELETE", base+"/tool-gateways/minha-ide", "", nil)
	e.want(204, "DELETE", base+"/tool-profiles/seguro", "", nil)
	e.want(404, "GET", base+"/tool-profiles/seguro", "", nil)
}
