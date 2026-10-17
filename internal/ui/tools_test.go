package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// As abas de Integrações (Tools, Perfis, Gateways, Auditoria) renderizam com os dados de exemplo (ADR-0036).
func TestToolsPagesRender(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for path, want := range map[string][]string{
		"/c/hanko/ferramentas":                       {`href="/c/hanko/integracoes"`, "create_issue", "delete_repo", "quarentena", "destrutiva", "Atualizar catálogo", "Liberar 1 em quarentena"},
		"/c/hanko/ferramentas?integracao=local":      {"stdio não passa pelo gateway"},
		"/c/hanko/ferramentas/perfis":                {"Seguro", "Negar", "Novo perfil"},
		"/c/hanko/ferramentas/perfis/novo":           {"Passo 1 de 3", "Criar e escolher as tools"},
		"/c/hanko/ferramentas/perfis/seguro":         {"1. Nome e padrão", "Apagar perfil"},
		"/c/hanko/ferramentas/perfis/seguro?passo=2": {"GitHub", "Salvar escolhas", "Regras por risco", `value="ask" selected`},
		"/c/hanko/ferramentas/perfis/seguro?passo=3": {"Vincular", "Persona"},
		"/c/hanko/ferramentas/gateways":              {"Minha IDE", "Novo gateway"},
		"/c/hanko/ferramentas/gateways/minha-ide":    {"hkgw_Ab12Cd", "/mcp/gw/hanko/minha-ide", "Gerar token", "Authorization: Bearer hkgw_"},
		"/c/hanko/ferramentas/auditoria":             {"list_issues", "perguntar antes", "gateway minha-ide", "9f86d081884c"},
	} {
		rec := e.get(t, path)
		wantStatus(t, rec, 200)
		wantContains(t, rec, want...)
	}
	// o painel da persona carrega as tools efetivas sob demanda
	p := uitest.Sample().Personas[0]
	rec := e.get(t, "/c/hanko/personas/"+p.Slug)
	wantContains(t, rec, `/ferramentas/efetivas?persona=`+p.Slug)
	rec = e.get(t, "/c/hanko/ferramentas/efetivas?persona="+p.Slug)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "Tools efetivas", "Seguro", "github/create_issue", "Perguntar antes", "regra da tool create_issue", "regra de risco destrutiva")
}

func TestToolsActions(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	b := "/c/hanko/ferramentas"
	rec := e.post(t, b+"/catalogo/github/tools/delete_repo/liberar", url.Values{}, true)
	wantStatus(t, rec, 204)
	if !strings.Contains(rec.Header().Get("HX-Redirect"), "ok=liberada") || !f.Called("SetToolStatus") {
		t.Fatalf("liberar: %v", rec.Header())
	}
	e.post(t, b+"/catalogo/github/atualizar", url.Values{}, true)
	if !f.Called("RefreshIntegrationTools") {
		t.Fatal("refresh")
	}
	// falha na descoberta volta como aviso
	f.SetErr("RefreshIntegrationTools", errString("conexão recusada"))
	rec = e.post(t, b+"/catalogo/github/atualizar", url.Values{}, true)
	if !strings.Contains(rec.Header().Get("HX-Redirect"), "erro=") {
		t.Fatalf("erro da descoberta: %v", rec.Header())
	}
	// passo 1 → passo 2
	rec = e.post(t, b+"/perfis/novo", url.Values{"name": {"Leitura"}, "rate": {"60"}, "default_action": {"deny"}}, false)
	wantStatus(t, rec, 303)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/perfis/leitura?passo=2") {
		t.Fatalf("redirect: %s", loc)
	}
	rec = e.post(t, b+"/perfis/novo", url.Values{"name": {""}, "rate": {"60"}}, false)
	wantStatus(t, rec, 422)
	wantContains(t, rec, "Dê ao perfil um nome")
	// passo 2: escolhas por integração e por tool
	rec = e.post(t, b+"/perfis/seguro/integracoes/github", url.Values{"group": {"allow"}, "tool:delete_repo": {"off"}, "tool:create_issue": {"ask"}}, true)
	wantStatus(t, rec, 204)
	if c := f.Tools.LastChoices; c.Group != "allow" || c.Tools["delete_repo"] != "off" || c.Tools["create_issue"] != "ask" {
		t.Fatalf("escolhas: %+v", c)
	}
	// passo 3: vínculo por persona
	p := f.Personas[1]
	rec = e.post(t, b+"/perfis/leitura/vinculos", url.Values{"scope": {"persona"}, "persona": {p.ID.String()}, "priority": {"5"}}, true)
	wantStatus(t, rec, 204)
	// gateway: gerar token mostra o valor uma vez, sem redirecionar
	rec = e.post(t, b+"/gateways/minha-ide/tokens", url.Values{"label": {"ci"}, "days": {"30"}, "actions": {"tools/list"}}, false)
	wantStatus(t, rec, 201)
	wantContains(t, rec, uitest.FakeGatewayToken, "não será mostrado de novo")
	rec = e.get(t, b+"/gateways/minha-ide")
	wantNotContains(t, rec, uitest.FakeGatewayToken)
	rec = e.post(t, b+"/gateways/minha-ide/ativar", url.Values{"enabled": {"0"}}, true)
	wantStatus(t, rec, 204)
	if g, _ := f.GetToolGateway(t.Context(), domain.ID{}, "minha-ide"); g.Enabled {
		t.Fatal("gateway deveria estar desligado")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// Controles de admin somem para quem não é admin; a auditoria é só de admin.
func TestToolsRoles(t *testing.T) {
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberMember
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/ferramentas")
	wantStatus(t, rec, 200)
	wantNotContains(t, rec, "Atualizar catálogo", "/liberar")
	rec = e.get(t, "/c/hanko/ferramentas/auditoria")
	wantContains(t, rec, "Só admin lê a auditoria")
	wantNotContains(t, rec, "list_issues")
	rec = e.get(t, "/c/hanko/ferramentas/gateways/minha-ide")
	wantNotContains(t, rec, "Gerar token")
	rec = e.post(t, "/c/hanko/ferramentas/perfis/novo", url.Values{"name": {"X"}, "rate": {"10"}}, false)
	wantStatus(t, rec, 403)
	rec = e.post(t, "/c/hanko/ferramentas/catalogo/github/tools/delete_repo/liberar", url.Values{}, true)
	wantStatus(t, rec, 403)
}

// A Caixa de entrada tem a aba Tools com os gates tool.
func TestInboxToolsTab(t *testing.T) {
	f := uitest.Sample()
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/caixa?aba=tools")
	wantStatus(t, rec, 200)
	wantContains(t, rec, "?aba=tools")
	rec = e.get(t, "/c/hanko/gates?tipo=tools")
	wantStatus(t, rec, 200)
}
