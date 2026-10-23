package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// upstreamMCP é um servidor MCP de verdade (SDK) num httptest: registra os headers e as chamadas recebidas.
type upstreamMCP struct {
	srv   *sdk.Server
	hs    *httptest.Server
	mu    sync.Mutex
	auths []string
	calls []string
	hits  int
}

func newUpstreamMCP(t *testing.T) *upstreamMCP {
	t.Helper()
	u := &upstreamMCP{srv: sdk.NewServer(&sdk.Implementation{Name: "upstream", Version: "1"}, nil)}
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return u.srv },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	u.hs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.hits++
		u.auths = append(u.auths, r.Header.Get("Authorization"))
		u.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(u.hs.Close)
	return u
}

func bptr(b bool) *bool { return &b }

func (u *upstreamMCP) add(name string, ann *sdk.ToolAnnotations) {
	u.addSchema(name, ann, `{"type":"object","properties":{"x":{"type":"number"}}}`)
}

func (u *upstreamMCP) addSchema(name string, ann *sdk.ToolAnnotations, schema string) {
	u.srv.AddTool(&sdk.Tool{Name: name, Description: "tool " + name, InputSchema: json.RawMessage(schema), Annotations: ann},
		func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			u.mu.Lock()
			u.calls = append(u.calls, name+" "+string(req.Params.Arguments))
			u.mu.Unlock()
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok " + name}}}, nil
		})
}

func (u *upstreamMCP) callLog() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.calls...)
}

func (u *upstreamMCP) requests() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits
}

// toolsEnv é uma empresa com um servidor MCP http (Authorization guardado cifrado) e as quatro tools-base:
// ler (só leitura), escrever (escreve sem destruir), apagar (destrutiva) e misterio (sem anotações).
type toolsEnv struct {
	*intEnv
	up    *upstreamMCP
	integ service.IntegrationView
}

const upstreamSecret = "Bearer up-secret-token-123456"

func newToolsEnv(t *testing.T) *toolsEnv {
	t.Helper()
	e := newIntEnv(t, service.IntegrationsConfig{AllowPrivate: true})
	up := newUpstreamMCP(t)
	up.add("ler", &sdk.ToolAnnotations{ReadOnlyHint: true})
	up.add("escrever", &sdk.ToolAnnotations{DestructiveHint: bptr(false)})
	up.add("apagar", &sdk.ToolAnnotations{DestructiveHint: bptr(true)})
	up.add("misterio", nil)
	integ := e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: up.hs.URL + "/mcp", AllPersonas: true,
		Headers: map[string]string{"Authorization": upstreamSecret}})
	return &toolsEnv{intEnv: e, up: up, integ: integ}
}

func (e *toolsEnv) refresh(t *testing.T) service.ToolCatalogResult {
	t.Helper()
	res, err := e.svc.RefreshIntegrationTools(context.Background(), domain.HumanActor, e.co.ID, e.integ.ID)
	if err != nil || !res.OK {
		t.Fatalf("refresh: %+v %v", res, err)
	}
	return res
}

// reviewed faz a primeira descoberta e libera o que ela pôs em quarentena, como o admin faz ao cadastrar o servidor
// (ADR-0065): os testes que não tratam da quarentena partem de um catálogo todo ativo.
func (e *toolsEnv) reviewed(t *testing.T) service.ToolCatalogResult {
	t.Helper()
	res := e.refresh(t)
	if _, err := e.svc.ReviewQuarantinedTools(context.Background(), domain.HumanActor, e.co.ID, e.integ.ID, nil); err != nil {
		t.Fatalf("revisar: %v", err)
	}
	return res
}

func (e *toolsEnv) catalog(t *testing.T) map[string]domain.IntegrationTool {
	t.Helper()
	v, err := e.svc.ListIntegrationTools(context.Background(), e.co.ID, e.integ.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]domain.IntegrationTool{}
	for _, tl := range v.Tools {
		out[tl.Name] = tl
	}
	return out
}

func TestToolCatalogDiscoveryQuarantineAndRemoval(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()

	// primeira descoberta de uma integração criada aqui (ADR-0065): só a leitura nasce ativa; o que escreve, o destrutivo
	// e o que não declara o risco entram em quarentena. Risco derivado das anotações.
	res := e.refresh(t)
	if !res.Baseline || res.Tools != 4 || res.Quarantined != 3 {
		t.Fatalf("primeira descoberta: %+v", res)
	}
	cat := e.catalog(t)
	want := map[string]domain.ToolRisk{"ler": domain.ToolRiskRead, "escrever": domain.ToolRiskWrite, "apagar": domain.ToolRiskDestructive, "misterio": domain.ToolRiskUnknown}
	for n, r := range want {
		st := domain.ToolQuarantined
		if n == "ler" {
			st = domain.ToolActive
		}
		if cat[n].Risk != r || cat[n].Status != st || cat[n].SchemaSHA256 == "" {
			t.Errorf("%s: %+v", n, cat[n])
		}
	}
	// o admin revisa a primeira descoberta e libera tudo
	if n, err := e.svc.ReviewQuarantinedTools(ctx, domain.HumanActor, e.co.ID, e.integ.ID, nil); err != nil || n != 3 {
		t.Fatalf("revisar a primeira descoberta: %d %v", n, err)
	}
	// a descoberta passou o segredo guardado ao servidor
	if a := e.up.auths; len(a) == 0 || a[0] != upstreamSecret {
		t.Fatalf("Authorization repassado: %v", a)
	}

	// tools novas depois da linha de base: escrita e destrutiva em quarentena, leitura ativa
	e.up.add("nova_escrita", &sdk.ToolAnnotations{DestructiveHint: bptr(true)})
	e.up.add("nova_leitura", &sdk.ToolAnnotations{ReadOnlyHint: true})
	e.up.add("nova_sem_dica", nil)
	e.up.srv.RemoveTools("misterio")
	res = e.refresh(t)
	if res.Baseline || res.New != 3 || res.Quarantined != 2 || res.Removed != 1 {
		t.Fatalf("segunda descoberta: %+v", res)
	}
	cat = e.catalog(t)
	if cat["nova_escrita"].Status != domain.ToolQuarantined || cat["nova_sem_dica"].Status != domain.ToolQuarantined ||
		cat["nova_leitura"].Status != domain.ToolActive || cat["misterio"].Status != domain.ToolRemoved {
		t.Fatalf("estados: %+v", cat)
	}

	// mudou a definição de uma tool que escreve: volta para a quarentena
	e.up.srv.RemoveTools("escrever")
	e.up.addSchema("escrever", &sdk.ToolAnnotations{DestructiveHint: bptr(false)}, `{"type":"object","properties":{"y":{"type":"string"}}}`)
	res = e.refresh(t)
	if res.Changed != 1 || e.catalog(t)["escrever"].Status != domain.ToolQuarantined {
		t.Fatalf("mudança de contrato: %+v %+v", res, e.catalog(t)["escrever"])
	}

	// um admin revisa: libera uma e depois as demais de uma vez
	tl, err := e.svc.SetToolStatus(ctx, domain.HumanActor, e.co.ID, e.integ.ID, "escrever", domain.ToolActive)
	if err != nil || tl.Status != domain.ToolActive || tl.ReviewedAt == nil {
		t.Fatalf("revisar: %+v %v", tl, err)
	}
	n, err := e.svc.ReviewQuarantinedTools(ctx, domain.HumanActor, e.co.ID, e.integ.ID, nil)
	if err != nil || n != 2 {
		t.Fatalf("revisar todas: %d %v", n, err)
	}
	// desligar e não aceitar status inválido nem tool removida
	if _, err := e.svc.SetToolStatus(ctx, domain.HumanActor, e.co.ID, e.integ.ID, "ler", domain.ToolDisabled); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.SetToolStatus(ctx, domain.HumanActor, e.co.ID, e.integ.ID, "ler", domain.ToolQuarantined)
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.SetToolStatus(ctx, domain.HumanActor, e.co.ID, e.integ.ID, "misterio", domain.ToolActive)
	wantErr(t, err, domain.ErrInvalid)
	// desligada continua desligada na próxima descoberta
	e.refresh(t)
	if e.catalog(t)["ler"].Status != domain.ToolDisabled {
		t.Fatal("tool desligada voltou sozinha")
	}
	// a atividade registra a quarentena
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "integration"})
	q := 0
	for _, a := range acts {
		if a.Verb == "integration.tool_quarantined" {
			q++
		}
	}
	if q < 3 {
		t.Fatalf("atividades de quarentena: %d", q)
	}
}

func TestToolCatalogSSRFBlocksInternalDestination(t *testing.T) {
	e := newToolsEnv(t)
	e.svc.Integrations.AllowPrivate = false
	before := e.up.requests()
	res, err := e.svc.RefreshIntegrationTools(context.Background(), domain.HumanActor, e.co.ID, e.integ.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Error, "destino") || e.up.requests() != before {
		t.Fatalf("SSRF: %+v, requisições %d→%d", res, before, e.up.requests())
	}
	v, _ := e.svc.ListIntegrationTools(context.Background(), e.co.ID, e.integ.ID)
	if v.Catalog.LastError == "" || v.Catalog.SyncedAt != nil {
		t.Fatalf("estado do catálogo: %+v", v.Catalog)
	}
}

func TestToolProfileResolutionScopes(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	pr := e.project(t, "Site", nil)
	mk := func(name string) service.ToolProfileView {
		p, err := e.svc.CreateToolProfile(ctx, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	bind := func(p service.ToolProfileView, in service.ToolBindingInput) domain.ToolBinding {
		b, err := e.svc.BindToolProfile(ctx, domain.HumanActor, e.co.ID, p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	eff := func(project *domain.ID) string {
		v, err := e.svc.EffectiveToolsFor(ctx, e.co.ID, ana.ID, project)
		if err != nil {
			t.Fatal(err)
		}
		if v.Profile == nil {
			return ""
		}
		return v.Profile.Slug
	}
	if eff(nil) != "" {
		t.Fatal("sem vínculo não há perfil")
	}
	empresa, empresa2, projeto, persona := mk("Empresa"), mk("Empresa dois"), mk("Projeto"), mk("Persona")
	bind(empresa, service.ToolBindingInput{Scope: domain.ToolScopeCompany, Priority: 5})
	bind(empresa2, service.ToolBindingInput{Scope: domain.ToolScopeCompany, Priority: 1})
	if got := eff(nil); got != "empresa" {
		t.Fatalf("prioridade no mesmo escopo: %s", got)
	}
	// empatando a prioridade, vale o vínculo mais novo
	bind(empresa2, service.ToolBindingInput{Scope: domain.ToolScopeCompany, Priority: 5})
	if got := eff(nil); got != "empresa-dois" {
		t.Fatalf("mais novo: %s", got)
	}
	bind(projeto, service.ToolBindingInput{Scope: domain.ToolScopeProject, ProjectID: &pr.ID, Priority: -100})
	if eff(nil) != "empresa-dois" || eff(&pr.ID) != "projeto" {
		t.Fatalf("projeto vence empresa: %s %s", eff(nil), eff(&pr.ID))
	}
	pb := bind(persona, service.ToolBindingInput{Scope: domain.ToolScopePersona, PersonaID: &ana.ID})
	if eff(&pr.ID) != "persona" || eff(nil) != "persona" {
		t.Fatal("persona vence projeto")
	}
	if err := e.svc.UnbindToolProfile(ctx, domain.HumanActor, e.co.ID, persona.ID, pb.ID); err != nil {
		t.Fatal(err)
	}
	if eff(&pr.ID) != "projeto" {
		t.Fatal("desvincular")
	}
	// formas inválidas
	_, err := e.svc.BindToolProfile(ctx, domain.HumanActor, e.co.ID, empresa.ID, service.ToolBindingInput{Scope: domain.ToolScopeProject})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.BindToolProfile(ctx, domain.HumanActor, e.co.ID, empresa.ID, service.ToolBindingInput{Scope: domain.ToolScopeCompany, PersonaID: &ana.ID})
	wantErr(t, err, domain.ErrInvalid)

	// exclude vence include e a decisão aparece por tool
	e.refresh(t)
	iid := e.integ.ID
	for _, r := range []service.ToolRuleInput{
		{Target: domain.ToolTargetIntegration, IntegrationID: &iid, Effect: domain.ToolInclude},
		{Target: domain.ToolTargetTool, IntegrationID: &iid, ToolName: "apagar", Effect: domain.ToolExclude},
		{Target: domain.ToolTargetRisk, Risk: domain.ToolRiskWrite, Effect: domain.ToolInclude, Mode: domain.ToolModeAsk},
	} {
		if _, err := e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, projeto.ID, r); err != nil {
			t.Fatal(err)
		}
	}
	v, err := e.svc.EffectiveToolsFor(ctx, e.co.ID, ana.ID, &pr.ID)
	if err != nil || len(v.Integrations) != 1 {
		t.Fatalf("efetivas: %+v %v", v, err)
	}
	got := map[string]service.EffectiveTool{}
	for _, tl := range v.Integrations[0].Tools {
		got[tl.Tool.Name] = tl
	}
	if !got["ler"].Visible || got["ler"].Verdict.Mode != domain.ToolModeAllow || got["ler"].Verdict.Source != "include" {
		t.Errorf("ler: %+v", got["ler"])
	}
	if got["apagar"].Visible || got["apagar"].Verdict.Source != "exclude" {
		t.Errorf("apagar: %+v", got["apagar"])
	}
	// a regra de risco da integração inteira é menos específica que a de integração? não: risco global < integração
	if got["escrever"].Verdict.Mode != domain.ToolModeAllow {
		t.Errorf("escrever (integração vence risco global): %+v", got["escrever"])
	}
	// regras inválidas
	_, err = e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, projeto.ID, service.ToolRuleInput{Target: domain.ToolTargetTool, IntegrationID: &iid, Effect: domain.ToolInclude})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, projeto.ID, service.ToolRuleInput{Target: domain.ToolTargetRisk, Risk: "explosivo", Effect: domain.ToolInclude})
	wantErr(t, err, domain.ErrInvalid)
}

// toolRun abre uma run ativa da persona numa task (para o gateway da run).
func toolRun(t *testing.T, e *toolsEnv, p domain.Persona) (service.ToolCaller, service.TaskView) {
	t.Helper()
	ctx := context.Background()
	tk := e.task(t, service.CreateTaskInput{Title: "usar tools", PersonaID: &p.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tk, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.svc.RunToolCaller(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c, tk
}

func names(ts []service.GatewayTool) string {
	var n []string
	for _, t := range ts {
		n = append(n, t.Name)
	}
	return strings.Join(n, ",")
}

func TestRunGatewayListCallAskAndApproval(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.reviewed(t)
	ana := e.persona(t, "Ana", nil)
	prof, err := e.svc.CreateToolProfile(ctx, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: "Seguro"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetIntegrationToolChoices(ctx, domain.HumanActor, e.co.ID, prof.ID, service.IntegrationToolChoices{
		IntegrationID: e.integ.ID, Group: service.ToolChoiceAllow,
		Tools: map[string]string{"apagar": service.ToolChoiceAsk, "misterio": service.ToolChoiceOff}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.BindToolProfile(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolBindingInput{Scope: domain.ToolScopePersona, PersonaID: &ana.ID}); err != nil {
		t.Fatal(err)
	}
	c, tk := toolRun(t, e, ana)

	// tools/list filtrado pelo perfil
	list, err := e.svc.GatewayListTools(ctx, c, "docs")
	if err != nil || names(list) != "apagar,escrever,ler" {
		t.Fatalf("list: %s %v", names(list), err)
	}
	// integração que a persona não tem
	_, err = e.svc.GatewayListTools(ctx, c, "outra")
	wantErr(t, err, domain.ErrNotFound)

	// allow: repassa com o segredo guardado
	res, err := e.svc.GatewayCallTool(ctx, c, "docs", "ler", json.RawMessage(`{"x":1}`))
	if err != nil || res.Decision != domain.ToolDecisionAllow || !strings.Contains(string(res.Result), "ok ler") {
		t.Fatalf("ler: %+v %v", res, err)
	}
	if a := e.up.auths[len(e.up.auths)-1]; a != upstreamSecret {
		t.Fatalf("Authorization: %q", a)
	}
	// excluída: erro de tool desconhecida, sem chegar ao servidor
	calls := len(e.up.callLog())
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "misterio", nil)
	if !res.Unknown || res.Decision != domain.ToolDecisionDeny {
		t.Fatalf("misterio: %+v", res)
	}

	// ask: abre um gate tool e não chama
	args := json.RawMessage(`{"x":7,"segredo":"valor-ultra-secreto-42"}`)
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "apagar", args)
	if res.Decision != domain.ToolDecisionAsk || res.GateID == nil || !strings.Contains(res.Message, "aguardando aprovação") {
		t.Fatalf("ask: %+v", res)
	}
	gate := *res.GateID
	g, err := e.svc.GetGate(ctx, e.co.ID, gate)
	if err != nil || g.Kind != domain.GateTool || g.Status != domain.GatePending || g.PersonaID == nil || *g.PersonaID != ana.ID {
		t.Fatalf("gate: %+v %v", g, err)
	}
	// a mesma chamada de novo não abre outro gate (as chaves do JSON em outra ordem contam como a mesma chamada)
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "apagar", json.RawMessage(`{"segredo":"valor-ultra-secreto-42","x":7}`))
	if res.Decision != domain.ToolDecisionAsk || res.GateID == nil || *res.GateID != gate {
		t.Fatalf("repetição pendente: %+v", res)
	}
	if len(e.up.callLog()) != calls {
		t.Fatal("ask chamou o servidor")
	}
	// a persona não fica presa (gate tool não bloqueia) e a task não muda
	if p, _ := e.svc.GetPersona(ctx, e.co.ID, ana.ID.String()); p.Status == domain.PersonaAwaitingGate {
		t.Fatal("gate tool não deve prender a persona")
	}

	// aprovar libera UMA repetição com os mesmos argumentos, e acorda a persona na task
	wakes := e.jobs.wakeCount()
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gate, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	if e.jobs.wakeCount() != wakes+1 {
		t.Fatalf("acordar a persona: %d → %d", wakes, e.jobs.wakeCount())
	}
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "apagar", args)
	if res.Decision != domain.ToolDecisionAllow || res.GateID == nil || *res.GateID != gate || !strings.Contains(string(res.Result), "ok apagar") {
		t.Fatalf("depois da aprovação: %+v", res)
	}
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "apagar", args)
	if res.Decision != domain.ToolDecisionAsk || res.GateID == nil || *res.GateID == gate {
		t.Fatalf("a aprovação passa uma vez só: %+v", res)
	}
	// argumentos diferentes pedem nova aprovação; rejeitada, a repetição é recusada
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "apagar", json.RawMessage(`{"x":8}`))
	if res.Decision != domain.ToolDecisionAsk || res.GateID == nil {
		t.Fatalf("args diferentes: %+v", res)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, *res.GateID, service.DecideGateInput{Decision: domain.GateRejected}); err != nil {
		t.Fatal(err)
	}
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "apagar", json.RawMessage(`{"x":8}`))
	if res.Decision != domain.ToolDecisionDeny || !strings.Contains(res.Message, "recusada") {
		t.Fatalf("depois de rejeitar: %+v", res)
	}
	_ = tk

	// quarentena: tool nova que escreve
	e.up.add("nova", &sdk.ToolAnnotations{DestructiveHint: bptr(true)})
	e.refresh(t)
	res, _ = e.svc.GatewayCallTool(ctx, c, "docs", "nova", nil)
	if res.Decision != domain.ToolDecisionQuarantined || res.Unknown {
		t.Fatalf("quarentena: %+v", res)
	}
	if list, _ := e.svc.GatewayListTools(ctx, c, "docs"); strings.Contains(names(list), "nova") {
		t.Fatal("tool em quarentena no tools/list")
	}

	// auditoria: tudo registrado, nunca os argumentos em claro
	inv, err := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{})
	if err != nil || len(inv) < 8 {
		t.Fatalf("auditoria: %d %v", len(inv), err)
	}
	var dump string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(t::text, ' '), '') FROM tool_invocations t`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, "valor-ultra-secreto") || !strings.Contains(dump, "apagar") {
		t.Fatalf("auditoria com argumentos: %s", dump)
	}
	byDecision := map[domain.ToolDecision]int{}
	for _, i := range inv {
		byDecision[i.Decision]++
		if i.ArgsSHA256 == "" && i.Tool == "apagar" {
			t.Errorf("sem hash: %+v", i)
		}
	}
	if byDecision[domain.ToolDecisionAllow] < 2 || byDecision[domain.ToolDecisionAsk] < 3 || byDecision[domain.ToolDecisionDeny] < 2 ||
		byDecision[domain.ToolDecisionQuarantined] != 1 {
		t.Fatalf("decisões: %v", byDecision)
	}
	// filtro por decisão
	asks, _ := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{Decision: domain.ToolDecisionAsk})
	if len(asks) != byDecision[domain.ToolDecisionAsk] {
		t.Fatalf("filtro: %d", len(asks))
	}
}

func TestRunGatewayWithoutProfileKeepsCurrentBehaviour(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	// integração anterior à regra do ADR-0065 (sem linha de catálogo), sem perfil e nunca descoberta: o gateway descobre
	// na hora, a primeira descoberta é a linha de base e tudo aparece
	if _, err := e.svc.Pool.Exec(ctx, "DELETE FROM integration_tool_catalogs WHERE integration_id = $1", e.integ.ID); err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.GatewayListTools(ctx, c, "docs")
	if err != nil || names(list) != "apagar,escrever,ler,misterio" {
		t.Fatalf("sem perfil: %s %v", names(list), err)
	}
	res, err := e.svc.GatewayCallTool(ctx, c, "docs", "apagar", json.RawMessage(`{}`))
	if err != nil || res.Decision != domain.ToolDecisionAllow {
		t.Fatalf("sem perfil, allow: %+v %v", res, err)
	}
}

// Integração criada depois do ADR-0065 e descoberta pela primeira vez numa execução (sem admin olhando): só a leitura
// aparece; o que escreve fica em quarentena até a revisão.
func TestRunGatewayFirstDiscoveryQuarantinesWrites(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	list, err := e.svc.GatewayListTools(ctx, c, "docs")
	if err != nil || names(list) != "ler" {
		t.Fatalf("primeira descoberta pelo gateway: %s %v", names(list), err)
	}
	res, err := e.svc.GatewayCallTool(ctx, c, "docs", "apagar", json.RawMessage(`{}`))
	if err != nil || res.Decision != domain.ToolDecisionQuarantined {
		t.Fatalf("apagar em quarentena: %+v %v", res, err)
	}
}

func TestRunGatewayRateLimit(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.refresh(t)
	ana := e.persona(t, "Ana", nil)
	prof, _ := e.svc.CreateToolProfile(ctx, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: "Lento",
		DefaultAction: domain.ToolDefaultAllow, RateLimitPerMin: 2})
	if _, err := e.svc.BindToolProfile(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolBindingInput{Scope: domain.ToolScopeCompany}); err != nil {
		t.Fatal(err)
	}
	c, _ := toolRun(t, e, ana)
	for i := 0; i < 2; i++ {
		if res, _ := e.svc.GatewayCallTool(ctx, c, "docs", "ler", nil); res.Decision != domain.ToolDecisionAllow {
			t.Fatalf("chamada %d: %+v", i, res)
		}
	}
	res, _ := e.svc.GatewayCallTool(ctx, c, "docs", "ler", nil)
	if res.Decision != domain.ToolDecisionDeny || !strings.Contains(res.Message, "limite") {
		t.Fatalf("teto: %+v", res)
	}
	// passado um minuto, volta a passar
	e.svc.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if res, _ := e.svc.GatewayCallTool(ctx, c, "docs", "ler", nil); res.Decision != domain.ToolDecisionAllow {
		t.Fatalf("depois da janela: %+v", res)
	}
}

func TestNamedGatewayTokens(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.reviewed(t)
	prof, _ := e.svc.CreateToolProfile(ctx, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: "IDE", DefaultAction: domain.ToolDefaultAllow})
	iid := e.integ.ID
	if _, err := e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolRuleInput{Target: domain.ToolTargetRisk,
		Risk: domain.ToolRiskDestructive, Effect: domain.ToolExclude}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolRuleInput{Target: domain.ToolTargetTool,
		IntegrationID: &iid, ToolName: "escrever", Effect: domain.ToolInclude, Mode: domain.ToolModeAsk}); err != nil {
		t.Fatal(err)
	}
	gw, err := e.svc.CreateToolGateway(ctx, domain.HumanActor, e.co.ID, service.CreateToolGatewayInput{Name: "Minha IDE", ProfileID: prof.ID})
	if err != nil || gw.Slug != "minha-ide" {
		t.Fatalf("gateway: %+v %v", gw, err)
	}
	// o perfil usado pelo gateway não pode ser apagado
	wantErr(t, e.svc.DeleteToolProfile(ctx, domain.HumanActor, e.co.ID, prof.ID), domain.ErrInvalid)

	listOnly, tokList, err := e.svc.CreateGatewayToken(ctx, domain.HumanActor, e.co.ID, gw.ID, service.CreateGatewayTokenInput{Label: "só ler",
		Actions: []string{domain.GatewayActionList}})
	if err != nil || !strings.HasPrefix(tokList, "hkgw_") || !strings.HasPrefix(tokList, listOnly.Prefix) ||
		time.Until(listOnly.ExpiresAt) < 89*24*time.Hour {
		t.Fatalf("token: %+v %q %v", listOnly, tokList, err)
	}
	full, tokFull, err := e.svc.CreateGatewayToken(ctx, domain.HumanActor, e.co.ID, gw.ID, service.CreateGatewayTokenInput{ExpiresIn: 2 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	// o token não volta em nenhuma leitura
	v, _ := e.svc.GetToolGateway(ctx, e.co.ID, "minha-ide")
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), tokList) || strings.Contains(string(b), tokFull) || len(v.Tokens) != 2 || v.ActiveTokens != 2 {
		t.Fatalf("leitura: %s", b)
	}
	var stored string
	_ = e.svc.Pool.QueryRow(ctx, `SELECT string_agg(token_hash, ',') FROM tool_gateway_tokens`).Scan(&stored)
	if strings.Contains(stored, tokFull) {
		t.Fatal("token em claro no banco")
	}

	c, err := e.svc.GatewayCaller(ctx, e.co.Slug, "minha-ide", tokList)
	if err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.GatewayListTools(ctx, c, "")
	if err != nil || names(list) != "docs__escrever,docs__ler,docs__misterio" {
		t.Fatalf("list agregado: %s %v", names(list), err)
	}
	_, err = e.svc.GatewayCallTool(ctx, c, "", "docs__ler", nil)
	wantErr(t, err, domain.ErrForbidden) // ação não permitida pelo token

	c, err = e.svc.GatewayCaller(ctx, e.co.Slug, "minha-ide", tokFull)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.GatewayCallTool(ctx, c, "", "docs__ler", json.RawMessage(`{"x":1}`))
	if err != nil || res.Decision != domain.ToolDecisionAllow {
		t.Fatalf("call: %+v %v", res, err)
	}
	res, _ = e.svc.GatewayCallTool(ctx, c, "", "docs__apagar", nil)
	if !res.Unknown {
		t.Fatalf("destrutiva excluída: %+v", res)
	}
	// ask em nome do gateway: o gate não tem persona
	res, _ = e.svc.GatewayCallTool(ctx, c, "", "docs__escrever", json.RawMessage(`{"x":3}`))
	if res.Decision != domain.ToolDecisionAsk || res.GateID == nil {
		t.Fatalf("ask gateway: %+v", res)
	}
	g, _ := e.svc.GetGate(ctx, e.co.ID, *res.GateID)
	if g.PersonaID != nil || g.Kind != domain.GateTool || !strings.Contains(g.Body, "Minha IDE") {
		t.Fatalf("gate do gateway: %+v", g)
	}
	inv, _ := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{GatewayID: &gw.ID})
	if len(inv) < 3 || inv[0].GatewaySlug != "minha-ide" {
		t.Fatalf("auditoria do gateway: %+v", inv)
	}

	// recusas: outro gateway, outra empresa, token inventado, revogado, expirado, gateway desligado
	for name, try := range map[string][3]string{
		"gateway errado": {e.co.Slug, "outro", tokFull},
		"empresa errada": {"outra", "minha-ide", tokFull},
		"inventado":      {e.co.Slug, "minha-ide", "hkgw_inventado"},
		"sem prefixo":    {e.co.Slug, "minha-ide", "abc"},
	} {
		if _, err := e.svc.GatewayCaller(ctx, try[0], try[1], try[2]); err != domain.ErrUnauthenticated {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.RevokeGatewayToken(ctx, domain.HumanActor, e.co.ID, gw.ID, listOnly.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GatewayCaller(ctx, e.co.Slug, "minha-ide", tokList); err != domain.ErrUnauthenticated {
		t.Fatalf("revogado: %v", err)
	}
	e.svc.Now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	if _, err := e.svc.GatewayCaller(ctx, e.co.Slug, "minha-ide", tokFull); err != domain.ErrUnauthenticated {
		t.Fatalf("expirado: %v", err)
	}
	e.svc.Now = time.Now
	if _, err := e.svc.UpdateToolGateway(ctx, domain.HumanActor, e.co.ID, gw.ID, service.UpdateToolGatewayInput{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	full2, tok2, err := e.svc.CreateGatewayToken(ctx, domain.HumanActor, e.co.ID, gw.ID, service.CreateGatewayTokenInput{})
	if err != nil {
		t.Fatal(err)
	}
	_ = full2
	_ = full
	if _, err := e.svc.GatewayCaller(ctx, e.co.Slug, "minha-ide", tok2); err != domain.ErrUnauthenticated {
		t.Fatalf("gateway desligado: %v", err)
	}
	// validação do token
	for _, in := range []service.CreateGatewayTokenInput{{Actions: []string{"tools/delete"}}, {ExpiresIn: time.Minute}, {ExpiresIn: 400 * 24 * time.Hour}} {
		_, _, err := e.svc.CreateGatewayToken(ctx, domain.HumanActor, e.co.ID, gw.ID, in)
		wantErr(t, err, domain.ErrInvalid)
	}
}

func TestRunExtrasRoutesHTTPThroughGatewayAndProfileExcludesStdio(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	e.svc.Integrations.AllowStdio = true
	local := e.mcp(t, service.CreateIntegrationInput{Name: "Local", Transport: "stdio", Command: "npx", AllPersonas: true})
	ana := e.persona(t, "Ana", nil)

	ex, err := e.svc.RunExtras(ctx, e.co.ID, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, direct := ex.MCP["docs"]; direct || len(ex.ToolGateway) != 1 || ex.ToolGateway[0] != "docs" {
		t.Fatalf("http pelo gateway: %v %v", ex.MCP, ex.ToolGateway)
	}
	b, _ := json.Marshal(ex)
	if strings.Contains(string(b), "up-secret-token") {
		t.Fatalf("segredo do http chegou ao agente: %s", b)
	}
	if _, ok := ex.MCP["local"]; !ok {
		t.Fatalf("stdio sem perfil continua: %v", ex.MCP)
	}

	// perfil deny sem incluir o stdio: ele sai, com aviso; incluir com ask é recusado
	prof, _ := e.svc.CreateToolProfile(ctx, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: "Fechado"})
	if _, err := e.svc.BindToolProfile(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolBindingInput{Scope: domain.ToolScopeCompany}); err != nil {
		t.Fatal(err)
	}
	ex, _ = e.svc.RunExtras(ctx, e.co.ID, ana.ID)
	if _, ok := ex.MCP["local"]; ok || len(ex.Warnings) != 1 || !strings.Contains(ex.Warnings[0], "local") {
		t.Fatalf("stdio fora: %v %v", ex.MCP, ex.Warnings)
	}
	lid := local.ID
	_, err = e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolRuleInput{Target: domain.ToolTargetIntegration,
		IntegrationID: &lid, Effect: domain.ToolInclude, Mode: domain.ToolModeAsk})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolRuleInput{Target: domain.ToolTargetTool,
		IntegrationID: &lid, ToolName: "x", Effect: domain.ToolInclude})
	wantErr(t, err, domain.ErrInvalid)
	if _, err := e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolRuleInput{Target: domain.ToolTargetIntegration,
		IntegrationID: &lid, Effect: domain.ToolInclude}); err != nil {
		t.Fatal(err)
	}
	ex, _ = e.svc.RunExtras(ctx, e.co.ID, ana.ID)
	if _, ok := ex.MCP["local"]; !ok {
		t.Fatalf("stdio incluído: %v", ex.Warnings)
	}
	// http excluído inteiro também sai
	iid := e.integ.ID
	if _, err := e.svc.AddToolProfileRule(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolRuleInput{Target: domain.ToolTargetIntegration,
		IntegrationID: &iid, Effect: domain.ToolExclude}); err != nil {
		t.Fatal(err)
	}
	ex, _ = e.svc.RunExtras(ctx, e.co.ID, ana.ID)
	if len(ex.ToolGateway) != 0 {
		t.Fatalf("http excluído: %v", ex.ToolGateway)
	}
	// destino interno sem a liberação do operador: aviso por run
	e.svc.Integrations.AllowPrivate = false
	if err := e.svc.UnbindToolProfile(ctx, domain.HumanActor, e.co.ID, prof.ID, mustBinding(t, e, prof.ID)); err != nil {
		t.Fatal(err)
	}
	c, _ := toolRun(t, e, ana)
	ex, err = e.svc.RunExtrasForRun(ctx, e.co.ID, ana.ID, *c.RunID)
	if err != nil || len(ex.ToolGateway) != 1 || !strings.Contains(strings.Join(ex.Warnings, " "), "HANKO_INTEGRATIONS_ALLOW_PRIVATE") {
		t.Fatalf("aviso de destino interno: %+v %v", ex, err)
	}
}

func mustBinding(t *testing.T, e *toolsEnv, profileID domain.ID) domain.ID {
	t.Helper()
	p, err := e.svc.GetToolProfile(context.Background(), e.co.ID, profileID.String())
	if err != nil || len(p.Bindings) == 0 {
		t.Fatalf("vínculos: %+v %v", p, err)
	}
	return p.Bindings[0].ID
}

func TestToolsRoles(t *testing.T) {
	e := newToolsEnv(t)
	bg := context.Background()
	e.refresh(t)
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin} {
		u := mkUser(t, e.env, string(r)+"@x.com", domain.RoleMember)
		join(t, e.env, u, r)
		users[r] = u
	}
	outsider := mkUser(t, e.env, "fora@x.com", domain.RoleMember)
	prof, _ := e.svc.CreateToolProfile(bg, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: "Base"})

	type op struct {
		name string
		min  domain.MemberRole
		run  func(ctx context.Context) error
	}
	ops := []op{
		{"ListToolProfiles", domain.MemberViewer, func(ctx context.Context) error { _, err := e.svc.ListToolProfiles(ctx, e.co.ID); return err }},
		{"ListIntegrationTools", domain.MemberViewer, func(ctx context.Context) error {
			_, err := e.svc.ListIntegrationTools(ctx, e.co.ID, e.integ.ID)
			return err
		}},
		{"ListToolGateways", domain.MemberViewer, func(ctx context.Context) error { _, err := e.svc.ListToolGateways(ctx, e.co.ID); return err }},
		{"ListToolInvocations", domain.MemberAdmin, func(ctx context.Context) error {
			_, err := e.svc.ListToolInvocations(ctx, e.co.ID, service.ToolInvocationFilter{})
			return err
		}},
		{"CreateToolProfile", domain.MemberAdmin, func(ctx context.Context) error {
			_, err := e.svc.CreateToolProfile(ctx, domain.HumanActor, e.co.ID, service.CreateToolProfileInput{Name: "P " + time.Now().Format("150405.000000")})
			return err
		}},
		{"RefreshIntegrationTools", domain.MemberAdmin, func(ctx context.Context) error {
			_, err := e.svc.RefreshIntegrationTools(ctx, domain.HumanActor, e.co.ID, e.integ.ID)
			return err
		}},
		{"SetToolStatus", domain.MemberAdmin, func(ctx context.Context) error {
			_, err := e.svc.SetToolStatus(ctx, domain.HumanActor, e.co.ID, e.integ.ID, "ler", domain.ToolActive)
			return err
		}},
		{"BindToolProfile", domain.MemberAdmin, func(ctx context.Context) error {
			_, err := e.svc.BindToolProfile(ctx, domain.HumanActor, e.co.ID, prof.ID, service.ToolBindingInput{Scope: domain.ToolScopeCompany})
			return err
		}},
		{"CreateToolGateway", domain.MemberAdmin, func(ctx context.Context) error {
			_, err := e.svc.CreateToolGateway(ctx, domain.HumanActor, e.co.ID, service.CreateToolGatewayInput{Name: "G " + time.Now().Format("150405.000000"), ProfileID: prof.ID})
			return err
		}},
	}
	for _, o := range ops {
		for role, u := range users {
			err := o.run(as(u))
			if role.AtLeast(o.min) && err != nil {
				t.Errorf("%s como %s: %v", o.name, role, err)
			}
			if !role.AtLeast(o.min) && err != domain.ErrForbidden {
				t.Errorf("%s como %s: want ErrForbidden, got %v", o.name, role, err)
			}
		}
		wantErr(t, o.run(as(outsider)), domain.ErrNotFound)
	}
}

// Integração anterior à regra (sem linha de catálogo, ADR-0065): a primeira descoberta continua sendo a linha de base
// do ADR-0036, com tudo ativo, para nada mudar para quem já a usa.
func TestToolCatalogLegacyIntegrationKeepsBaseline(t *testing.T) {
	e := newToolsEnv(t)
	if _, err := e.svc.Pool.Exec(context.Background(), "DELETE FROM integration_tool_catalogs WHERE integration_id = $1", e.integ.ID); err != nil {
		t.Fatal(err)
	}
	res := e.refresh(t)
	if !res.Baseline || res.Tools != 4 || res.Quarantined != 0 {
		t.Fatalf("linha de base: %+v", res)
	}
	for n, tl := range e.catalog(t) {
		if tl.Status != domain.ToolActive {
			t.Errorf("%s: %+v", n, tl)
		}
	}
}
