package service_test

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/oauth/oauthtest"
	"github.com/renatobardi/hanko/internal/service"
)

// Assistente Conectar (ADR-0089): do zero a uma persona lendo issues com escrita pedindo aprovação num fluxo só.

// connectEnv é uma empresa com servidor MCP http de teste e as quatro tools-base (ler/escrever/apagar/misterio).
type connectEnv struct {
	*intEnv
	up *upstreamMCP
}

func newConnectEnv(t *testing.T) *connectEnv {
	t.Helper()
	e := newIntEnv(t, service.IntegrationsConfig{AllowPrivate: true})
	up := newUpstreamMCP(t)
	up.add("ler", &sdk.ToolAnnotations{ReadOnlyHint: true})
	up.add("escrever", &sdk.ToolAnnotations{DestructiveHint: bptr(false)})
	up.add("apagar", &sdk.ToolAnnotations{DestructiveHint: bptr(true)})
	up.add("misterio", nil)
	return &connectEnv{intEnv: e, up: up}
}

// begin header mode: cria a integração e descobre o catálogo.
func (e *connectEnv) begin(t *testing.T, name string) service.ConnectView {
	t.Helper()
	v, err := e.svc.ConnectBegin(context.Background(), domain.HumanActor, e.co.ID, service.ConnectBeginInput{
		Name: name, URL: e.up.hs.URL + "/mcp", Headers: map[string]string{"Authorization": upstreamSecret}})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	return v
}

// beginDead aponta para um servidor morto: a descoberta falha, o rascunho fica de pé.
func (e *connectEnv) beginDead(t *testing.T) service.ConnectView {
	t.Helper()
	up := newUpstreamMCP(t)
	url := up.hs.URL + "/mcp"
	up.hs.Close()
	v, err := e.svc.ConnectBegin(context.Background(), domain.HumanActor, e.co.ID, service.ConnectBeginInput{
		Name: "Morto", URL: url, Headers: map[string]string{"Authorization": upstreamSecret}})
	if err != nil {
		t.Fatalf("begin dead: %v", err)
	}
	return v
}

// preview confere o passo 5.
func (e *connectEnv) preview(t *testing.T, ref, tpl string, personas ...domain.ID) service.ConnectPreviewView {
	t.Helper()
	v, err := e.svc.ConnectPreview(context.Background(), domain.HumanActor, e.co.ID, ref, tpl, personas)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	return v
}

// complete conclui o assistente.
func (e *connectEnv) complete(t *testing.T, ref, tpl string, personas ...domain.ID) service.ConnectResultView {
	t.Helper()
	v, err := e.svc.ConnectComplete(context.Background(), domain.HumanActor, e.co.ID, ref,
		service.ConnectCompleteInput{Template: tpl, PersonaIDs: personas})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	return v
}

// effTools devolve as tools efetivas da persona para a integração do assistente.
func (e *connectEnv) effTools(t *testing.T, personaID domain.ID, integSlug string) map[string]service.EffectiveTool {
	t.Helper()
	v, err := e.svc.EffectiveToolsFor(context.Background(), e.co.ID, personaID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range v.Integrations {
		if i.Slug == integSlug {
			out := map[string]service.EffectiveTool{}
			for _, tl := range i.Tools {
				out[tl.Tool.Name] = tl
			}
			return out
		}
	}
	return nil
}

// ADR-0089 passo 1-2: o rascunho nasce fechado — catálogo descoberto, escrita em quarentena, nenhuma persona
// alcança a integração, e os modelos de acesso da empresa estão lá (dois semeados, o sugerido é o de aprovação).
func TestConnectBeginLeavesIntegrationClosed(t *testing.T) {
	e := newConnectEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)

	v := e.begin(t, "Docs")

	if !v.CanComplete || len(v.Tools) != 4 {
		t.Fatalf("estado após begin: %+v", v)
	}
	if v.PersonasLinked != 0 || v.Integration.AllPersonas {
		t.Fatalf("rascunho nasce aberto: %+v", v.Integration)
	}
	quarantined := 0
	for _, tl := range v.Tools {
		if tl.Quarantined {
			quarantined++
		}
	}
	if quarantined != 3 { // escrever, apagar e misterio (ADR-0067)
		t.Fatalf("quarentena após descoberta: %d de %d", quarantined, len(v.Tools))
	}
	if len(v.Templates) != 2 {
		t.Fatalf("modelos semeados: %d", len(v.Templates))
	}
	sug := 0
	for _, tp := range v.Templates {
		if tp.Suggested {
			sug++
			if tp.Slug != "leitura-livre-escrita-com-aprovacao" {
				t.Fatalf("sugerido errado: %s", tp.Slug)
			}
		}
	}
	if sug != 1 {
		t.Fatalf("sugeridos: %d", sug)
	}
	// nenhuma persona alcança a integração antes da conclusão
	integs, err := e.svc.ListPersonaIntegrations(ctx, e.co.ID, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range integs {
		if i.Slug == v.Integration.Slug && (i.Linked || i.AllPersonas) {
			t.Fatal("persona alcança integração antes da conclusão")
		}
	}
}

// O critério de pronto: do zero até uma persona lendo issues com escrita pedindo aprovação num fluxo só —
// begin, preview e complete; no fim a persona lê sem perguntar e escreve/apaga pedindo aprovação.
func TestConnectWizardHeaderFlow(t *testing.T) {
	e := newConnectEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)

	v := e.begin(t, "Docs")
	ref := v.Integration.Slug

	// passo 5 antes de concluir: ler liberado, escrever e apagar pedem aprovação, tudo ainda em quarentena
	pv := e.preview(t, ref, "leitura-livre-escrita-com-aprovacao", ana.ID)
	if len(pv.Released) != 3 {
		t.Fatalf("liberadas na conclusão: %v", pv.Released)
	}
	verdicts := map[string]struct {
		mode domain.ToolPolicyMode
		tm   domain.ToolMode
	}{
		"ler":      {domain.PolicyAllow, domain.ToolModeAllow},
		"escrever": {domain.PolicyAsk, domain.ToolModeAsk},
		"apagar":   {domain.PolicyAsk, domain.ToolModeAsk},
		"misterio": {domain.PolicyAsk, domain.ToolModeAsk},
	}
	for _, p := range pv.Personas {
		for name, want := range verdicts {
			tl, ok := toolPreview(p, name)
			if !ok {
				t.Fatalf("tool %s fora do preview", name)
			}
			if tl.Mode != want.mode || !tl.Verdict.Allowed || tl.Verdict.Mode != want.tm {
				t.Fatalf("%s: %+v", name, tl)
			}
		}
	}

	res := e.complete(t, ref, "leitura-livre-escrita-com-aprovacao", ana.ID)

	if len(res.Released) != 3 || res.Profile.Slug == "" || len(res.Personas) != 1 {
		t.Fatalf("resultado: %+v", res)
	}
	// o perfil nasceu com padrão negar e as regras do modelo
	if res.Profile.DefaultAction != domain.ToolDefaultDeny {
		t.Fatalf("padrão do perfil: %s", res.Profile.DefaultAction)
	}

	// a persona lê sem perguntar e escreve pedindo aprovação
	tools := e.effTools(t, ana.ID, ref)
	if tools == nil {
		t.Fatalf("a persona não alcança a integração %s", ref)
	}
	if tl := tools["ler"]; !tl.Visible || tl.Verdict.Mode != domain.ToolModeAllow {
		t.Fatalf("ler: %+v", tl.Verdict)
	}
	for _, name := range []string{"escrever", "apagar", "misterio"} {
		tl, ok := tools[name]
		if !ok || !tl.Visible {
			t.Fatalf("%s invisível: %+v", name, tools[name])
		}
		if tl.Verdict.Mode != domain.ToolModeAsk || !tl.Verdict.Allowed {
			t.Fatalf("%s: %+v", name, tl.Verdict)
		}
	}

	// toda escrita do assistente deixou activity_event
	for verb, want := range map[string]int{
		"integration.created": 1, "tool_profile.created": 1, "tool_profile.bound": 1,
		"persona.integration_enabled": 1, "integration.tools_reviewed": 1, "integration.connected": 1,
	} {
		if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = $2`, e.co.ID, verb); n != want {
			t.Fatalf("activity %s = %d, quer %d", verb, n, want)
		}
	}
	// o vínculo é da persona escolhida, nunca de todas
	if n := e.count(t, `SELECT count(*) FROM integration_personas ip JOIN integrations i ON i.id = ip.integration_id
		WHERE ip.company_id = $1 AND i.slug = $2`, e.co.ID, ref); n != 1 {
		t.Fatalf("ligações de persona: %d", n)
	}
	_ = ctx
}

func toolPreview(p service.ConnectPreviewPersona, name string) (service.ConnectPreviewTool, bool) {
	for _, tl := range p.Tools {
		if tl.Name == name {
			return tl, true
		}
	}
	return service.ConnectPreviewTool{}, false
}

// "Somente leitura": a escrita não é liberada da quarentena e a persona não vê as tools de escrita — nenhum passo
// deixa persona com tool de escrita liberada sem escolha explícita.
func TestConnectWizardReadOnly(t *testing.T) {
	e := newConnectEnv(t)
	ana := e.persona(t, "Ana", nil)

	v := e.begin(t, "Docs")
	ref := v.Integration.Slug

	pv := e.preview(t, ref, "somente-leitura", ana.ID)
	if len(pv.Released) != 0 {
		t.Fatalf("somente leitura liberaria escrita: %v", pv.Released)
	}
	res := e.complete(t, ref, "somente-leitura", ana.ID)
	if len(res.Released) != 0 {
		t.Fatalf("conclusão liberou escrita: %v", res.Released)
	}
	tools := e.effTools(t, ana.ID, ref)
	if tl := tools["ler"]; !tl.Visible {
		t.Fatalf("ler invisível: %+v", tl)
	}
	for _, name := range []string{"escrever", "apagar", "misterio"} {
		if tl, ok := tools[name]; ok && tl.Visible {
			t.Fatalf("%s visível no somente leitura: %+v", name, tl)
		}
	}
	// e as tools continuam em quarentena no catálogo
	cat, err := e.svc.ListIntegrationTools(context.Background(), e.co.ID, v.Integration.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range cat.Tools {
		if tl.Name != "ler" && tl.Status != domain.ToolQuarantined {
			t.Fatalf("%s saiu da quarentena: %s", tl.Name, tl.Status)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'integration.tools_reviewed'`, e.co.ID); n != 0 {
		t.Fatalf("tools_reviewed sem revisão: %d", n)
	}
}

// Guardas: sem persona não conclui, sem modelo não conclui, integração em uso não é rascunho, descoberta pendente
// não conclui e rascunho se descarta inteiro.
func TestConnectWizardGuards(t *testing.T) {
	e := newConnectEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)

	v := e.begin(t, "Docs")
	ref := v.Integration.Slug

	if _, err := e.svc.ConnectComplete(ctx, domain.HumanActor, e.co.ID, ref, service.ConnectCompleteInput{
		Template: "somente-leitura"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem persona: %v", err)
	}
	if _, err := e.svc.ConnectComplete(ctx, domain.HumanActor, e.co.ID, ref, service.ConnectCompleteInput{
		PersonaIDs: []domain.ID{ana.ID}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem modelo: %v", err)
	}
	if _, err := e.svc.ConnectPreview(ctx, domain.HumanActor, e.co.ID, ref, "somente-leitura", nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("preview sem persona: %v", err)
	}

	// persona ligada à mão: não é mais rascunho do assistente
	if err := e.svc.SetPersonaIntegration(ctx, domain.HumanActor, e.co.ID, ana.ID, v.Integration.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ConnectDiscard(ctx, domain.HumanActor, e.co.ID, ref); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("descartar integração em uso: %v", err)
	}
	if _, err := e.svc.ConnectComplete(ctx, domain.HumanActor, e.co.ID, ref, service.ConnectCompleteInput{
		Template: "somente-leitura", PersonaIDs: []domain.ID{ana.ID}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("concluir integração em uso: %v", err)
	}
	if err := e.svc.SetPersonaIntegration(ctx, domain.HumanActor, e.co.ID, ana.ID, v.Integration.ID, false); err != nil {
		t.Fatal(err)
	}

	// descoberta pendente: integração criada sem rodar o passo 2
	integ := e.mcp(t, service.CreateIntegrationInput{Name: "Pendente", URL: e.up.hs.URL + "/mcp"})
	if _, err := e.svc.ConnectComplete(ctx, domain.HumanActor, e.co.ID, integ.Slug, service.ConnectCompleteInput{
		Template: "somente-leitura", PersonaIDs: []domain.ID{ana.ID}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("descoberta pendente: %v", err)
	}

	// descartar: rascunho some inteiro (integração e catálogo)
	if err := e.svc.ConnectDiscard(ctx, domain.HumanActor, e.co.ID, integ.Slug); err != nil {
		t.Fatalf("descartar: %v", err)
	}
	integs, err := e.svc.ListIntegrations(ctx, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range integs {
		if i.Slug == integ.Slug {
			t.Fatal("integração descartada continua na lista")
		}
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'integration.deleted'`, e.co.ID); n != 1 {
		t.Fatalf("integration.deleted = %d", n)
	}
}

// Passo 2 com servidor morto: não é erro — o rascunho fica de pé com o erro registrado, sem persona, sem tools;
// descobrir de novo continua sem pânico e descartar limpa.
func TestConnectWizardDeadUpstream(t *testing.T) {
	e := newConnectEnv(t)

	v := e.beginDead(t)
	if v.CanComplete || v.CatalogErr == "" || len(v.Tools) != 0 {
		t.Fatalf("estado do morto: %+v", v)
	}
	if _, err := e.svc.ConnectDiscover(context.Background(), domain.HumanActor, e.co.ID, v.Integration.Slug); err != nil {
		t.Fatalf("descobrir de novo: %v", err)
	}
	if err := e.svc.ConnectDiscard(context.Background(), domain.HumanActor, e.co.ID, v.Integration.Slug); err != nil {
		t.Fatalf("descartar: %v", err)
	}
}

// A escrita toda é numa transação só: quando qualquer activity_event é recusado (empresa arquivada, ADR-0057),
// o perfil criado no meio da conclusão volta atrás — nada fica escrito pela metade.
func TestConnectCompleteRollsBack(t *testing.T) {
	e := newConnectEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)

	v := e.begin(t, "Docs")
	before := e.count(t, `SELECT count(*) FROM tool_profiles WHERE company_id = $1`, e.co.ID)

	if _, err := e.svc.ArchiveCompany(ctx, domain.HumanActor, e.co.ID); err != nil {
		t.Fatalf("arquivar: %v", err)
	}
	if _, err := e.svc.ConnectComplete(ctx, domain.HumanActor, e.co.ID, v.Integration.Slug, service.ConnectCompleteInput{
		Template: "leitura-livre-escrita-com-aprovacao", PersonaIDs: []domain.ID{ana.ID}}); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Fatalf("concluir em empresa arquivada: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM tool_profiles WHERE company_id = $1`, e.co.ID); n != before {
		t.Fatalf("perfil sobrou depois do rollback: %d (antes %d)", n, before)
	}
	if n := e.count(t, `SELECT count(*) FROM integration_personas WHERE company_id = $1`, e.co.ID); n != 0 {
		t.Fatalf("ligação sobrou: %d", n)
	}
}

// OAuth (ADR-0043) também entra no assistente: nasce o rascunho, o consentimento liga a integração e a descoberta
// roda; daí a conclusão é a mesma.
func TestConnectWizardOAuthFlow(t *testing.T) {
	e := newConnectEnv(t)
	e.svc.PublicURL = "https://hanko.example.test"
	fake := oauthtest.New()
	t.Cleanup(fake.Close)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)

	v, err := e.svc.ConnectBegin(ctx, domain.HumanActor, e.co.ID, service.ConnectBeginInput{
		Name: "Docs Remoto", URL: fake.MCPURL(), OAuth: true})
	if err != nil {
		t.Fatalf("begin oauth: %v", err)
	}
	if v.AuthURL == "" || v.CanComplete || v.Connection == nil {
		t.Fatalf("estado oauth inicial: %+v", v)
	}
	ref := v.Integration.Slug

	// sem consentimento, descobrir devolve a URL de novo
	v2, err := e.svc.ConnectDiscover(ctx, domain.HumanActor, e.co.ID, ref)
	if err != nil {
		t.Fatal(err)
	}
	if v2.AuthURL == "" {
		t.Fatal("descobrir sem consentimento deveria trazer a URL de autorização")
	}

	// consentimento no servidor falso
	st, err := e.svc.AuthorizeConnection(ctx, domain.HumanActor, e.co.ID, v.Connection.Slug)
	if err != nil {
		t.Fatal(err)
	}
	state, challenge, _, err := oauthtest.ParseAuthURL(st.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteConnectionAuth(ctx, domain.HumanActor, service.CompleteConnectionInput{
		State: state, Code: fake.IssueCode(challenge)}); err != nil {
		t.Fatal(err)
	}

	// de volta ao assistente: descobrir de novo roda o tools/list com o token (o servidor falso lista zero tools)
	v3, err := e.svc.ConnectDiscover(ctx, domain.HumanActor, e.co.ID, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !v3.CanComplete || v3.Catalog.SyncedAt == nil {
		t.Fatalf("após consentimento: %+v", v3)
	}
	pv := e.preview(t, ref, "leitura-livre-escrita-com-aprovacao", ana.ID)
	if len(pv.Released) != 0 || len(pv.Personas) != 1 {
		t.Fatalf("preview oauth: %+v", pv)
	}
	res := e.complete(t, ref, "leitura-livre-escrita-com-aprovacao", ana.ID)
	if res.Profile.Slug == "" || len(res.Personas) != 1 {
		t.Fatalf("conclusão oauth: %+v", res)
	}
	// a persona alcança a integração pelo vínculo criado (o veredito por tool está coberto no modo header)
	if tools := e.effTools(t, ana.ID, ref); tools == nil {
		t.Fatalf("persona não alcança a integração oauth")
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'integration.connected'`, e.co.ID); n != 1 {
		t.Fatalf("integration.connected = %d", n)
	}
}

// Descartar no OAuth apaga a conexão inteira (integração e segredos junto).
func TestConnectWizardOAuthDiscard(t *testing.T) {
	e := newConnectEnv(t)
	e.svc.PublicURL = "https://hanko.example.test"
	fake := oauthtest.New()
	t.Cleanup(fake.Close)
	ctx := context.Background()

	v, err := e.svc.ConnectBegin(ctx, domain.HumanActor, e.co.ID, service.ConnectBeginInput{
		Name: "Docs Remoto", URL: fake.MCPURL(), OAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ConnectDiscard(ctx, domain.HumanActor, e.co.ID, v.Integration.Slug); err != nil {
		t.Fatalf("descartar oauth: %v", err)
	}
	conns, err := e.svc.ListConnections(ctx, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 0 {
		t.Fatalf("conexão sobrou: %d", len(conns))
	}
	if n := e.count(t, `SELECT count(*) FROM integrations WHERE company_id = $1`, e.co.ID); n != 0 {
		t.Fatalf("integração sobrou: %d", n)
	}
}
