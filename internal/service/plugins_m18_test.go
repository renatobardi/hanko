package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O plugin de exemplo do SDK (examples/plugins/showcase) é compilado uma vez por execução do pacote.
var (
	showcaseOnce sync.Once
	showcaseBin  string
	showcaseErr  error
)

func buildShowcase(t *testing.T) string {
	t.Helper()
	showcaseOnce.Do(func() {
		dir, err := os.MkdirTemp("", "hanko-showcase-bin-")
		if err != nil {
			showcaseErr = err
			return
		}
		showcaseBin = filepath.Join(dir, "showcase")
		cmd := exec.Command("go", "build", "-o", showcaseBin, "./examples/plugins/showcase") //nolint:gosec // teste
		cmd.Dir = filepath.Join("..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			showcaseErr = errors.New(string(out))
		}
	})
	if showcaseErr != nil {
		t.Fatalf("compilar o plugin showcase: %v", showcaseErr)
	}
	return showcaseBin
}

func installShowcaseDir(t *testing.T, root string) {
	t.Helper()
	bin := buildShowcase(t)
	dir := filepath.Join(root, "showcase")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "plugins", "showcase", domain.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, domain.ManifestFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(bin) //nolint:gosec // binário compilado acima
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "showcase"), b, 0o755); err != nil { //nolint:gosec // executável do plugin de teste
		t.Fatal(err)
	}
}

func (e *plugEnv) installShowcase(t *testing.T) {
	t.Helper()
	installShowcaseDir(t, e.root)
	v, err := e.svc.InstallPlugin(context.Background(), "showcase")
	if err != nil || v.Status != domain.PluginReady {
		t.Fatalf("instalar showcase: %v %+v", err, v.Status)
	}
	on := true
	if _, err := e.svc.UpdateCompanyPlugin(context.Background(), domain.HumanActor, e.co.ID, "showcase", service.UpdateCompanyPluginInput{Enabled: &on}); err != nil {
		t.Fatalf("ligar showcase: %v", err)
	}
	waitUntil(t, "showcase de pé", func() bool { return e.mgr.Status("showcase").Ready })
}

// Critério de demo do M18: o plugin de exemplo instalado adiciona uma tool e uma aba; revogar a capacidade tira
// cada uma na hora (o host confere a concessão a cada uso) e conceder de novo traz de volta.
func TestPluginShowcaseToolTabAndRevocation(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.installShowcase(t)

	tabs, err := e.svc.PluginTabs(ctx, e.co.ID)
	if err != nil || len(tabs) != 1 || tabs[0].Key != "painel" || tabs[0].PluginKey != "showcase" {
		t.Fatalf("abas = %+v, %v", tabs, err)
	}
	tools, err := e.svc.PluginTools(ctx, e.co.ID)
	if err != nil || len(tools) != 1 || tools[0].Name != "plugin_showcase_ping" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	p := e.persona(t, "Ana", nil)
	res, err := e.svc.CallPluginTool(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PluginToolCall{Name: tools[0].Name,
		Arguments: json.RawMessage(`{"text":"oi"}`), PersonaID: &p.ID})
	if err != nil || res.Text != "pong: oi" {
		t.Fatalf("tool = %+v, %v", res, err)
	}
	title, html, err := e.svc.PluginPageHTML(ctx, e.co.ID, "showcase", "painel")
	if err != nil || title != "Showcase" || !strings.Contains(html, "window.hanko.call") {
		t.Fatalf("página = %q %q, %v", title, html, err)
	}
	if _, _, err := e.svc.PluginPageHTML(ctx, e.co.ID, "showcase", "inexistente"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("página inexistente = %v", err)
	}

	// ponte: a página chama o plugin; sem fluxo ligado o gatilho dispara 0 fluxos
	raw, err := e.svc.PluginPageCall(ctx, e.co.ID, "showcase", "painel", "registrar", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(string(raw), `"total":1`) {
		t.Fatalf("ponte = %s, %v", raw, err)
	}
	if _, err := e.svc.PluginPageCall(ctx, e.co.ID, "showcase", "painel", "Inválido!", nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("método inválido = %v", err)
	}
	if _, err := e.svc.PluginPageCall(ctx, e.co.ID, "showcase", "painel", "nao_existe", nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("método que o plugin recusa = %v", err)
	}

	// revogar pages tira a aba, a página e a ponte; a tool continua
	v, err := e.svc.SetPluginCapability(ctx, "showcase", domain.CapPages, false)
	if err != nil || v.Has(domain.CapPages) {
		t.Fatalf("revogar pages = %+v, %v", v.GrantedCapabilities, err)
	}
	if tabs, _ := e.svc.PluginTabs(ctx, e.co.ID); len(tabs) != 0 {
		t.Fatalf("abas depois de revogar = %+v", tabs)
	}
	if _, _, err := e.svc.PluginPageHTML(ctx, e.co.ID, "showcase", "painel"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("página depois de revogar = %v", err)
	}
	if _, err := e.svc.PluginPageCall(ctx, e.co.ID, "showcase", "painel", "registrar", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ponte depois de revogar = %v", err)
	}
	if tools, _ := e.svc.PluginTools(ctx, e.co.ID); len(tools) != 1 {
		t.Fatalf("a tool não depende de pages: %+v", tools)
	}
	// revogar agent.tools tira a tool; chamar uma tool revogada é "não encontrada"
	if _, err := e.svc.SetPluginCapability(ctx, "showcase", domain.CapAgentTools, false); err != nil {
		t.Fatal(err)
	}
	if tools, _ := e.svc.PluginTools(ctx, e.co.ID); len(tools) != 0 {
		t.Fatalf("tools depois de revogar = %+v", tools)
	}
	if _, err := e.svc.CallPluginTool(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PluginToolCall{Name: "plugin_showcase_ping"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tool revogada = %v", err)
	}
	// conceder de novo traz de volta; conceder o que o manifesto não declara é recusado
	for _, c := range []string{domain.CapPages, domain.CapAgentTools} {
		if _, err := e.svc.SetPluginCapability(ctx, "showcase", c, true); err != nil {
			t.Fatalf("conceder %s: %v", c, err)
		}
	}
	if tabs, _ := e.svc.PluginTabs(ctx, e.co.ID); len(tabs) != 1 {
		t.Fatalf("abas depois de conceder = %+v", tabs)
	}
	if _, err := e.svc.SetPluginCapability(ctx, "showcase", domain.CapTasksWrite, true); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("conceder capacidade fora do manifesto = %v", err)
	}
	if _, err := e.svc.SetPluginCapability(ctx, "showcase", "nao.existe", true); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("capacidade desconhecida = %v", err)
	}
	// repetir é no-op (sem atividade nova)
	if _, err := e.svc.SetPluginCapability(ctx, "showcase", domain.CapPages, true); err != nil {
		t.Fatal(err)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "plugin"})
	if pluginVerbCount(acts, "plugin.capability_revoked") != 2 || pluginVerbCount(acts, "plugin.capability_granted") != 2 {
		t.Fatalf("atividade = %v", pluginVerbs(acts))
	}
	var audits int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE action LIKE 'plugin.capability_%'`).Scan(&audits); err != nil || audits != 4 {
		t.Fatalf("admin_audit = %d, %v", audits, err)
	}
}

func TestPluginCapabilityRoles(t *testing.T) {
	e := newPlugEnv(t, true)
	installShowcaseDir(t, e.root)
	installOwner := mkUser(t, e.env, "dono@x.com", domain.RoleOwner)
	owner := mkUser(t, e.env, "owner@x.com", domain.RoleMember)
	join(t, e.env, owner, domain.MemberOwner)
	if _, err := e.svc.InstallPlugin(as(installOwner), "showcase"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetPluginCapability(as(owner), "showcase", domain.CapPages, false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("revogar como owner da empresa = %v", err)
	}
	if _, err := e.svc.SetPluginCapability(as(installOwner), "showcase", domain.CapPages, false); err != nil {
		t.Fatalf("revogar como dono da instalação: %v", err)
	}
}

func TestPluginFlowTriggers(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.installShowcase(t)
	ana := e.persona(t, "Ana", nil)
	mk := func(name string, enabled bool) service.FlowView {
		f, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: name, Cron: "0 3 * * *", Timezone: "UTC",
			PersonaID: ana.ID, Enabled: &enabled, TaskTitle: "Pedido novo"})
		if err != nil {
			t.Fatalf("criar fluxo: %v", err)
		}
		return f
	}
	on, off := mk("Triagem de pedidos", true), mk("Fluxo parado", false)

	trig, links, err := e.svc.PluginFlowTriggers(ctx, e.co.ID, "showcase")
	if err != nil || len(trig) != 1 || trig[0].Key != "novo-pedido" || len(links) != 0 {
		t.Fatalf("gatilhos = %+v %+v, %v", trig, links, err)
	}
	if err := e.svc.LinkPluginFlowTrigger(ctx, domain.HumanActor, e.co.ID, "showcase", "inexistente", on.Slug, true); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("gatilho fora do manifesto = %v", err)
	}
	if err := e.svc.LinkPluginFlowTrigger(ctx, domain.HumanActor, e.co.ID, "showcase", "novo-pedido", "nao-existe", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("fluxo inexistente = %v", err)
	}
	for _, f := range []service.FlowView{on, off} {
		if err := e.svc.LinkPluginFlowTrigger(ctx, domain.HumanActor, e.co.ID, "showcase", "novo-pedido", f.Slug, true); err != nil {
			t.Fatal(err)
		}
	}
	// religar é idempotente
	if err := e.svc.LinkPluginFlowTrigger(ctx, domain.HumanActor, e.co.ID, "showcase", "novo-pedido", on.Slug, true); err != nil {
		t.Fatal(err)
	}
	if _, links, _ = e.svc.PluginFlowTriggers(ctx, e.co.ID, "showcase"); len(links) != 2 {
		t.Fatalf("ligações = %+v", links)
	}

	// a página dispara o gatilho pela ponte: o fluxo ligado e ativo cria a task; o desligado fica de fora
	raw, err := e.svc.PluginPageCall(ctx, e.co.ID, "showcase", "painel", "registrar", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(string(raw), `"fluxos":1`) {
		t.Fatalf("ponte = %s, %v", raw, err)
	}
	tasks, err := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{Limit: 50})
	if err != nil || len(tasks) != 1 || tasks[0].Title != "Pedido novo" {
		t.Fatalf("tasks = %+v, %v", tasks, err)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "plugin"})
	if pluginVerbCount(acts, "plugin.flow_triggered") != 1 || pluginVerbCount(acts, "plugin.flow_trigger_linked") != 3 {
		t.Fatalf("atividade = %v", pluginVerbs(acts))
	}

	// só admin liga; membro comum não
	member := mkUser(t, e.env, "membro@x.com", domain.RoleMember)
	join(t, e.env, member, domain.MemberMember)
	if err := e.svc.LinkPluginFlowTrigger(as(member), member.Actor(), e.co.ID, "showcase", "novo-pedido", on.Slug, false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("desligar como membro = %v", err)
	}
	if err := e.svc.LinkPluginFlowTrigger(ctx, domain.HumanActor, e.co.ID, "showcase", "novo-pedido", on.Slug, false); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.LinkPluginFlowTrigger(ctx, domain.HumanActor, e.co.ID, "showcase", "novo-pedido", on.Slug, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("desligar de novo = %v", err)
	}

	// revogar flows.trigger: o plugin não consegue mais disparar (a ponte devolve o erro do host)
	if _, err := e.svc.SetPluginCapability(ctx, "showcase", domain.CapFlowTriggers, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PluginPageCall(ctx, e.co.ID, "showcase", "painel", "registrar", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "flows.trigger") {
		t.Fatalf("ponte sem a capacidade = %v", err)
	}
}

func TestPluginSkillSource(t *testing.T) {
	e := newPlugEnv(t, true)
	ctx := context.Background()
	e.installShowcase(t)
	// a fonte de plugin não clona nada: independe de HANKO_SKILL_SOURCES (desligado aqui)
	srcs, err := e.svc.PluginSkillSources(ctx, e.co.ID)
	if err != nil || len(srcs) != 1 || srcs[0].URL != "plugin://showcase/catalogo" || srcs[0].SourceID != nil {
		t.Fatalf("fontes oferecidas = %+v, %v", srcs, err)
	}
	if _, err := e.svc.DiscoverSkillSource(ctx, e.co.ID, service.SkillSourceInput{URL: srcs[0].URL}); err != nil {
		t.Fatalf("descobrir: %v", err)
	}
	if _, _, err := e.svc.CreateSkillSource(ctx, domain.HumanActor, e.co.ID, service.CreateSkillSourceInput{
		SkillSourceInput: service.SkillSourceInput{URL: "plugin://showcase/nao-existe"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("fonte não declarada = %v", err)
	}
	src, res, err := e.svc.CreateSkillSource(ctx, domain.HumanActor, e.co.ID, service.CreateSkillSourceInput{
		SkillSourceInput: service.SkillSourceInput{URL: srcs[0].URL}})
	if err != nil || res.Error != "" || res.Imported != 1 {
		t.Fatalf("criar fonte = %+v %+v, %v", src, res, err)
	}
	if src.Label != "plugin showcase / catalogo" || src.Status != domain.SourceOK {
		t.Fatalf("fonte = %+v", src.SkillSource)
	}
	sk, err := e.svc.GetSkill(ctx, e.co.ID, "checklist-de-pedido")
	if err != nil || !strings.Contains(sk.Body, "Confirme o cliente") {
		t.Fatalf("skill importada = %+v, %v", sk, err)
	}
	// sincronizar de novo: nada mudou
	res, err = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, src.ID, false)
	if err != nil || res.Error != "" || res.Unchanged != 1 || res.Updated != 0 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	if srcs, _ = e.svc.PluginSkillSources(ctx, e.co.ID); srcs[0].SourceID == nil {
		t.Fatal("a fonte conectada deveria aparecer")
	}

	// revogar skills.source: a próxima sincronização falha com erro claro, sem apagar a skill
	if _, err := e.svc.SetPluginCapability(ctx, "showcase", domain.CapSkillSource, false); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, src.ID, false)
	if err != nil || !strings.Contains(res.Error, domain.CapSkillSource) {
		t.Fatalf("sync sem a capacidade = %+v, %v", res, err)
	}
	if _, err := e.svc.GetSkill(ctx, e.co.ID, "checklist-de-pedido"); err != nil {
		t.Fatalf("a skill deveria continuar: %v", err)
	}
	if _, _, err := e.svc.CreateSkillSource(ctx, domain.HumanActor, e.co.ID, service.CreateSkillSourceInput{
		SkillSourceInput: service.SkillSourceInput{URL: "plugin://showcase/catalogo", Ref: "main"}}); err == nil {
		t.Fatal("criar fonte sem a capacidade deveria falhar")
	}
}
