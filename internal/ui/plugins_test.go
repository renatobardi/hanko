package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func newPluginsEnv(t *testing.T, role domain.MemberRole, mod func(*uitest.PluginsFake)) (*env, *uitest.PluginsFake) {
	t.Helper()
	f := uitest.NewPluginsFake()
	f.MyRoleIs = role
	if mod != nil {
		mod(f)
	}
	e := newEnv(t, f.Fake)
	e.h = serveWith(t, f)
	return e, f
}

const pluginsPath = "/c/hanko/configuracoes/plugins"

func TestPluginsPageForTheInstallationOwner(t *testing.T) {
	e, _ := newPluginsEnv(t, domain.MemberOwner, nil)
	rec := e.get(t, pluginsPath)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Plugins e extensões", "Instalados", "Hello", "v1.0.0", "Pronto",
		"Disponíveis", "Relatórios", "Acessar a rede (declarado)", pluginsPath+"/instalar?key=relatorios", "sem hanko-plugin.json",
		pluginsPath+"/hello/instalacao/desinstalar", pluginsPath+"/hello/ativar", "Pontos de extensão embutidos", "Servidor MCP do Hanko")
	wantNotContains(t, rec, "SEGREDO-UI", "Em breve")
}

func TestPluginsPageWithoutPluginsDir(t *testing.T) {
	e, _ := newPluginsEnv(t, domain.MemberOwner, func(f *uitest.PluginsFake) { f.On = false })
	rec := e.get(t, pluginsPath)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Plugins de terceiros desligados", "HANKO_PLUGINS_DIR", "Servidor MCP do Hanko")
	wantNotContains(t, rec, "Disponíveis")
}

func TestPluginsPageUnavailableBackend(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, pluginsPath)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Plugins de terceiros desligados", "Servidor MCP do Hanko")
}

func TestPluginsInstallUpgradeApproveUninstall(t *testing.T) {
	e, f := newPluginsEnv(t, domain.MemberOwner, nil)
	rec := e.post(t, pluginsPath+"/instalar?key=relatorios", url.Values{}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != pluginsPath+"?ok=instalado" {
		t.Fatalf("redirect = %s", loc)
	}
	// diretório sem manifesto: volta para a lista com o erro
	rec = e.post(t, pluginsPath+"/instalar?key=quebrado", url.Values{}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "erro=") {
		t.Fatalf("redirect com erro = %s", loc)
	}
	wantContains(t, e.get(t, pluginsPath+"?erro=sem+hanko-plugin.json"), "sem hanko-plugin.json")

	rec = e.post(t, pluginsPath+"/hello/instalacao/atualizar", url.Values{}, true)
	wantStatus(t, rec, http.StatusNoContent)
	if got := rec.Header().Get("HX-Redirect"); got != pluginsPath+"?ok=pendente" {
		t.Fatalf("hx-redirect = %s", got)
	}
	rec = e.get(t, pluginsPath)
	wantContains(t, rec, "A versão 2.0.0 pede capacidades novas", "Ler personas", "Aprovar capacidades", pluginsPath+"/hello/instalacao/aprovar")
	wantStatus(t, e.post(t, pluginsPath+"/hello/instalacao/aprovar", url.Values{}, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, pluginsPath+"/hello/instalacao/desligar", url.Values{}, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, pluginsPath+"/hello/instalacao/ligar", url.Values{}, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, pluginsPath+"/hello/instalacao/desinstalar", url.Values{}, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, pluginsPath+"/hello/instalacao/explodir", url.Values{}, false), http.StatusNotFound)
	if got := strings.Join(f.Actions, ","); got != "install relatorios,upgrade hello,approve hello,disable hello,enable hello,uninstall hello" {
		t.Fatalf("ações = %s", got)
	}
}

func TestPluginToggleAndConfigForm(t *testing.T) {
	e, f := newPluginsEnv(t, domain.MemberAdmin, func(f *uitest.PluginsFake) { f.Owner = false })
	rec := e.post(t, pluginsPath+"/hello/ativar", url.Values{"enabled": {"0"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if f.Settings["hello"].Enabled {
		t.Fatal("o plugin continua ligado")
	}
	rec = e.get(t, pluginsPath+"/hello")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `name="cfg_greeting"`, `value="Oi"`, `name="cfg_api_token"`, `type="password"`, "definido", `name="clear_api_token"`,
		`name="cfg_mode"`, `name="has_loud"`, "plugin_hello_hello_echo", "Salvar configuração")
	// segredo nunca volta; quem não é o dono não vê saúde nem log
	wantNotContains(t, rec, "SEGREDO-UI", "Saúde do processo", "Log do plugin")

	rec = e.post(t, pluginsPath+"/hello", url.Values{"cfg_greeting": {"Bom dia"}, "cfg_mode": {"longo"}, "has_loud": {"1"}, "cfg_loud": {"true"},
		"cfg_api_token": {""}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	st := f.Settings["hello"]
	if st.Config["greeting"] != "Bom dia" || st.Config["mode"] != "longo" || st.Config["loud"] != true || st.Secrets["api_token"] != "SEGREDO-UI" {
		t.Fatalf("config = %+v / %+v", st.Config, st.Secrets)
	}
	rec = e.post(t, pluginsPath+"/hello", url.Values{"cfg_api_token": {"NOVO"}, "has_loud": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if st.Secrets["api_token"] != "NOVO" || st.Config["loud"] != false {
		t.Fatalf("segredo/checkbox = %+v / %+v", st.Secrets, st.Config)
	}
	rec = e.post(t, pluginsPath+"/hello", url.Values{"clear_api_token": {"1"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if _, ok := st.Secrets["api_token"]; ok {
		t.Fatal("segredo não foi apagado")
	}
	// valor fora do enum volta com erro no formulário, sem gravar
	rec = e.post(t, pluginsPath+"/hello", url.Values{"cfg_mode": {"enorme"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "use uma das opções")
}

func TestPluginsControlsByRole(t *testing.T) {
	e, _ := newPluginsEnv(t, domain.MemberViewer, func(f *uitest.PluginsFake) { f.Owner = false })
	rec := e.get(t, pluginsPath)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Hello", `disabled aria-checked`)
	wantNotContains(t, rec, "Disponíveis", "/instalacao/", "/instalar")
	rec = e.get(t, pluginsPath+"/hello")
	wantStatus(t, rec, http.StatusOK)
	wantNotContains(t, rec, `name="cfg_greeting"`, "SEGREDO-UI")
	wantContains(t, rec, "Oi")
	wantStatus(t, e.post(t, pluginsPath+"/hello/ativar", url.Values{"enabled": {"0"}}, false), http.StatusForbidden)
	wantStatus(t, e.post(t, pluginsPath+"/instalar?key=relatorios", url.Values{}, false), http.StatusForbidden)
}

func TestPluginDetailShowsHealthToTheOwner(t *testing.T) {
	e, _ := newPluginsEnv(t, domain.MemberOwner, nil)
	rec := e.get(t, pluginsPath+"/hello")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Saúde do processo", "4242", "No ar", "Log do plugin", "processo pronto", "Chaves de estado")
	wantStatus(t, e.get(t, pluginsPath+"/nao-existe"), http.StatusNotFound)
}

func TestPluginWidgetsFragmentIsSanitized(t *testing.T) {
	e, _ := newPluginsEnv(t, domain.MemberViewer, nil)
	rec := e.get(t, "/c/hanko/plugins/widgets?lugar=dashboard", "HX-Request", "true")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "De plugins", "Resumo", "<strong>3</strong>", "<th>Job</th>", "<td>tick</td>")
	wantNotContains(t, rec, "<script>alert(1)</script>")
	rec = e.get(t, "/c/hanko/plugins/widgets?lugar=task&ref=HNK-1", "HX-Request", "true")
	wantStatus(t, rec, http.StatusOK)
	if strings.TrimSpace(rec.Body.String()) != "" {
		t.Fatalf("sem widget o bloco devia sumir: %q", rec.Body.String())
	}
	// o Painel, a task e a persona carregam o bloco depois da página
	wantContains(t, e.get(t, "/c/hanko/"), `hx-get="/c/hanko/plugins/widgets?lugar=dashboard"`)
	wantContains(t, e.get(t, "/c/hanko/personas/ada"), `/c/hanko/plugins/widgets?lugar=persona&amp;ref=ada`)
}
