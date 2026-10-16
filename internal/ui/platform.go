package ui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// PlatformInfo é o que a instalação sabe de si (vem da config no boot): base das telas Ambientes,
// Adapters e Plugins. Nada aqui é segredo; tokens e chaves nunca entram.
type PlatformInfo struct {
	Mode, AuthProvider       string
	PublicURL, AgentURL      string
	ClaudeBin, CodexBin      string
	ACPAllow                 bool
	ACPCommands              []string
	MemoryProvider           string
	MemoryURL                string
	OTelEndpoint             string
	IntegrationsAllowStdio   bool
	IntegrationsAllowPrivate bool
	SecretKeySet             bool
	Workers                  int
	RunTimeout               time.Duration
	HeartbeatInterval        time.Duration
}

// lookPath é exec.LookPath trocável nos testes.
var lookPath = exec.LookPath

func settingsSections(ctx context.Context, base, cur string) []views.TabLink {
	secs := []struct{ key, label, path string }{
		{"geral", i18n.T(ctx, "Geral"), "/configuracoes"},
		{"ambientes", i18n.T(ctx, "Ambientes"), "/configuracoes/ambientes"},
		{"adapters", i18n.T(ctx, "Adapters"), "/configuracoes/adapters"},
		{"plugins", i18n.T(ctx, "Plugins"), "/configuracoes/plugins"},
	}
	out := make([]views.TabLink, 0, len(secs))
	for _, sct := range secs {
		out = append(out, views.TabLink{Key: sct.key, Label: sct.label, Href: base + sct.path, On: sct.key == cur})
	}
	return out
}

func (s *server) settingsShell(r *http.Request, co domain.Company, title string) views.Shell {
	ctx := r.Context()
	base := "/c/" + co.Slug
	return s.shell(r, co, "configuracoes", title, views.Crumb{Label: i18n.T(ctx, "Configurações"), Href: base + "/configuracoes"}, views.Crumb{Label: title})
}

func check(ok bool, label, detail string) views.EnvCheck {
	return views.EnvCheck{OK: ok, Label: label, Detail: detail}
}

func binCheck(ctx context.Context, name, bin string) views.EnvCheck {
	if bin == "" {
		return check(false, name, i18n.T(ctx, "não configurado"))
	}
	p, err := lookPath(bin)
	if err != nil {
		return check(false, name, i18n.Tf(ctx, "%s não encontrado no PATH do servidor", bin))
	}
	return check(true, name, p)
}

// rootWritable diz se a raiz dos workspaces existe e aceita escrita (cria e apaga um arquivo vazio).
func rootWritable(ctx context.Context, root string) views.EnvCheck {
	label := i18n.T(ctx, "Raiz dos workspaces gravável")
	if root == "" {
		return check(false, label, i18n.T(ctx, "não configurada"))
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return check(false, label, i18n.Tf(ctx, "%s não existe (é criada no primeiro workspace)", root))
	}
	f, err := os.CreateTemp(root, ".hanko-probe-*")
	if err != nil {
		return check(false, label, i18n.Tf(ctx, "%s sem permissão de escrita", root))
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return check(true, label, root)
}

func (s *server) handleEnvironments(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p := s.platform
	root := s.workspacesRoot(co)
	checks := []views.EnvCheck{
		rootWritable(ctx, root),
		binCheck(ctx, "git", "git"),
		binCheck(ctx, "Claude Code", p.ClaudeBin),
		binCheck(ctx, "Codex", p.CodexBin),
	}
	if p.ACPAllow {
		for _, c := range p.ACPCommands {
			checks = append(checks, binCheck(ctx, "ACP · "+filepath.Base(c), c))
		}
	}
	ok := 0
	for _, c := range checks {
		if c.OK {
			ok++
		}
	}
	agent := p.AgentURL
	if agent == "" {
		agent = p.PublicURL
	}
	vm := views.EnvironmentsVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "ambientes"), Checks: checks, OK: ok, Total: len(checks),
		Facts: []views.KV{
			{K: i18n.T(ctx, "Tipo"), V: i18n.T(ctx, "Local · processos filhos neste servidor")},
			{K: i18n.T(ctx, "Raiz dos workspaces"), V: root},
			{K: i18n.T(ctx, "URL pública"), V: dashIfEmpty(p.PublicURL)},
			{K: i18n.T(ctx, "URL dos agentes (/mcp)"), V: dashIfEmpty(agent)},
			{K: i18n.T(ctx, "Execuções simultâneas"), V: fmt.Sprint(p.Workers)},
			{K: i18n.T(ctx, "Tempo máximo por execução"), V: durOrDash(p.RunTimeout)},
			{K: i18n.T(ctx, "Heartbeat do agendador"), V: durOrDash(p.HeartbeatInterval)},
			{K: i18n.T(ctx, "Modo"), V: modeLabel(ctx, p)},
		}}
	s.render(w, r, http.StatusOK, views.EnvironmentsPage(s.settingsShell(r, co, i18n.T(ctx, "Ambientes")), vm))
}

func (s *server) handleAdapters(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p := s.platform
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	use := map[string][]string{}
	for _, ps := range personas {
		use[ps.Agent] = append(use[ps.Agent], ps.Name)
	}
	row := func(key, name, kind, desc string, c views.EnvCheck, extra string) views.AdapterRow {
		names := use[key]
		sort.Strings(names)
		return views.AdapterRow{Key: key, Name: name, Kind: kind, Desc: desc, Check: c, Personas: names, Extra: extra}
	}
	acp := check(p.ACPAllow, i18n.T(ctx, "Liberado pelo operador"), i18n.T(ctx, "HANKO_ACP_ALLOW desligado: personas acp não rodam"))
	acpExtra := ""
	if p.ACPAllow {
		acp.Detail = i18n.T(ctx, "qualquer comando")
		if len(p.ACPCommands) > 0 {
			acp.Detail = strings.Join(p.ACPCommands, ", ")
		}
		acpExtra = i18n.T(ctx, "O comando e os argumentos ficam em cada persona (adapter_config).")
	}
	vm := views.AdaptersVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "adapters"), Rows: []views.AdapterRow{
		row("claude", "Claude Code", "CLI", i18n.T(ctx, "stream-json, sessão preservada entre execuções, skills nativas e MCP do Hanko."), binCheck(ctx, i18n.T(ctx, "Binário"), p.ClaudeBin), ""),
		row("codex", "Codex", "CLI", i18n.T(ctx, "exec em JSONL; o MCP do Hanko entra por configuração da execução."), binCheck(ctx, i18n.T(ctx, "Binário"), p.CodexBin), ""),
		row("acp", "ACP", i18n.T(ctx, "Protocolo"), i18n.T(ctx, "Qualquer harness que fale ACP por stdio; você informa o comando na persona."), acp, acpExtra),
		row("fake", "Fake", i18n.T(ctx, "Teste"), i18n.T(ctx, "Harness simulado para demonstrações e testes; não chama modelo nenhum."), check(true, i18n.T(ctx, "Embutido"), i18n.T(ctx, "sempre disponível")), ""),
	}}
	prices, err := s.be.ListModelPrices(ctx)
	if err == nil {
		seen := map[string]bool{}
		for _, mp := range prices { // mais recente primeiro por modelo
			if seen[mp.Model] {
				continue
			}
			seen[mp.Model] = true
			vm.Prices = append(vm.Prices, views.PriceRow{Model: mp.Model, In: fmtUSDCtx(ctx, mp.InputPerMtok), Out: fmtUSDCtx(ctx, mp.OutputPerMtok),
				Since: shortDate(ctx, mp.EffectiveAt.In(loadLoc(co.Timezone)))})
		}
	}
	s.render(w, r, http.StatusOK, views.AdaptersPage(s.settingsShell(r, co, i18n.T(ctx, "Adapters")), vm))
}

func (s *server) handlePlugins(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p := s.platform
	base := "/c/" + co.Slug
	agent := p.AgentURL
	if agent == "" {
		agent = p.PublicURL
	}
	mem := check(p.MemoryProvider != "" && p.MemoryProvider != "none", i18n.T(ctx, "Provedor"), dashIfEmpty(p.MemoryProvider))
	if mem.OK && p.MemoryURL != "" {
		mem.Detail = p.MemoryProvider + " · " + p.MemoryURL
	}
	otel := check(p.OTelEndpoint != "", i18n.T(ctx, "Exportador OTLP"), dashIfEmpty(p.OTelEndpoint))
	if !otel.OK {
		otel.Detail = i18n.T(ctx, "sem OTEL_EXPORTER_OTLP_ENDPOINT: telemetria só local")
	}
	stdio := check(p.IntegrationsAllowStdio, i18n.T(ctx, "Integrações MCP stdio"), i18n.T(ctx, "desligadas (HANKO_INTEGRATIONS_ALLOW_STDIO)"))
	if stdio.OK {
		stdio.Detail = i18n.T(ctx, "liberadas pelo operador")
	}
	secret := check(p.SecretKeySet, i18n.T(ctx, "Cofre de segredos"), i18n.T(ctx, "sem HANKO_SECRET_KEY: integrações com segredo não podem ser salvas"))
	if secret.OK {
		secret.Detail = i18n.T(ctx, "AES-256-GCM em repouso")
	}
	vm := views.PluginsVM{Sections: settingsSections(ctx, base, "plugins"), Items: []views.PluginItem{
		{Icon: "link", Name: i18n.T(ctx, "Servidor MCP do Hanko"), Desc: i18n.T(ctx, "Tools de tasks, comentários, artefatos, metas e busca para as personas e para clientes MCP."),
			Checks: []views.EnvCheck{check(true, i18n.T(ctx, "Endpoint"), strings.TrimRight(agent, "/")+"/mcp")}, Href: base + "/integracoes"},
		{Icon: "database", Name: i18n.T(ctx, "Memória entre sessões"), Desc: i18n.T(ctx, "Provedor de memória por MCP, com curadoria governada por gate."),
			Checks: []views.EnvCheck{mem}, Href: base + "/configuracoes#memoria"},
		{Icon: "zap", Name: i18n.T(ctx, "Webhooks e integrações"), Desc: i18n.T(ctx, "Eventos assinados com HMAC e servidores MCP externos ligados às personas."),
			Checks: []views.EnvCheck{stdio, secret}, Href: base + "/integracoes"},
		{Icon: "sparkles", Name: i18n.T(ctx, "Skills"), Desc: i18n.T(ctx, "Pacotes de instruções versionados, distribuídos às personas em cada execução."),
			Checks: []views.EnvCheck{check(true, i18n.T(ctx, "Catálogo"), i18n.T(ctx, "por empresa"))}, Href: base + "/skills"},
		{Icon: "activity", Name: i18n.T(ctx, "Observabilidade"), Desc: i18n.T(ctx, "Traces, métricas e logs OpenTelemetry de cada request e execução."),
			Checks: []views.EnvCheck{otel}, Href: ""},
	}}
	s.render(w, r, http.StatusOK, views.PluginsPage(s.settingsShell(r, co, i18n.T(ctx, "Plugins")), vm))
}

func dashIfEmpty(v string) string {
	if strings.TrimSpace(v) == "" {
		return "—"
	}
	return v
}

func durOrDash(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return fmtDuration(d)
}

func modeLabel(ctx context.Context, p PlatformInfo) string {
	if p.Mode == "trusted" {
		return i18n.T(ctx, "trusted (sem login)")
	}
	if p.AuthProvider != "" {
		return i18n.Tf(ctx, "authenticated · login %s", p.AuthProvider)
	}
	return "authenticated"
}
