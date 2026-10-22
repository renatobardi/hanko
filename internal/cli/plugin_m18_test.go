package cli

import (
	"go/parser"
	"go/token"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
)

func TestPluginNewScaffold(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "meu-plugin")
	code, out, errOut := exec(t, Runtime{}, nil, "", "plugin", "new", "meu-plugin", "--dir", dir, "--author", "Ana")
	if code != 0 {
		t.Fatalf("plugin new: %d %q %q", code, out, errOut)
	}
	for _, f := range []string{domain.ManifestFile, "main.go", "go.mod", ".gitignore", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("faltou %s: %v", f, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, domain.ManifestFile))
	m, err := domain.ParsePluginManifest(raw)
	if err != nil {
		t.Fatalf("manifesto do esqueleto inválido: %v", err)
	}
	if m.ID != "meu-plugin" || m.Author != "Ana" || len(m.Pages) != 1 || len(m.FlowTriggers) != 1 || len(m.Tools) != 1 {
		t.Errorf("manifesto = %+v", m)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", mustRead(t, filepath.Join(dir, "main.go")), parser.AllErrors); err != nil {
		t.Errorf("main.go não é Go válido: %v", err)
	}
	if !strings.Contains(out, "go mod tidy") {
		t.Errorf("saída = %q", out)
	}
	// diretório com coisas dentro: recusa e não toca em nada
	code, _, errOut = exec(t, Runtime{}, nil, "", "plugin", "new", "outro", "--dir", dir)
	if code == 0 || !strings.Contains(errOut, "não está vazio") {
		t.Errorf("diretório ocupado: %d %q", code, errOut)
	}
	// id inválido
	if code, _, errOut = exec(t, Runtime{}, nil, "", "plugin", "new", "Inválido", "--dir", filepath.Join(t.TempDir(), "x")); code == 0 || !strings.Contains(errOut, "id inválido") {
		t.Errorf("id inválido: %d %q", code, errOut)
	}
	if _, err := PluginScaffold("ok", "a\"b"); err == nil {
		t.Error("autor com aspas deveria ser recusado")
	}
}

// O main.go do esqueleto compila contra o SDK do módulo (sem o go.mod gerado, que apontaria para uma versão publicada).
func TestPluginScaffoldCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("compila um programa Go")
	}
	files, err := PluginScaffold("compila-bem", "")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(filepath.Join("..", "..", "examples", "plugins"), ".scaffold-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	for _, f := range files {
		if f.Name == "main.go" {
			if err := os.WriteFile(filepath.Join(dir, f.Name), []byte(f.Content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	cmd := osexec.Command("go", "vet", "./"+filepath.Base(dir)) //nolint:gosec // teste
	cmd.Dir = filepath.Join("..", "..", "examples", "plugins")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("o esqueleto não compila:\n%s", out)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPluginM18Commands(t *testing.T) {
	plugin := api.Plugin{Key: "showcase", Name: "Showcase", Version: "1.0.0", Status: "ready", Capabilities: []string{"pages"}, PendingCapabilities: []string{},
		Contributions: api.Contributions{Pages: []api.PluginPageDef{{Key: "painel", Title: "Painel"}}, FlowTriggers: []api.PluginFlowTriggerDef{{Key: "novo", Title: "Novo"}},
			EnvProviders: []api.PluginEnvProviderDef{{Key: "p", Title: "P", Provider: "plugin:showcase/p"}}, SkillSources: []api.PluginSkillSourceDef{{Key: "c", Title: "C", URL: "plugin://showcase/c"}}}}
	srcID := "0190aaaa-0000-7000-8000-000000000001"
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/plugins/showcase/capabilities/pages/revoke": jsonResp(200, plugin),
		"POST /api/v1/plugins/showcase/capabilities/pages/grant":  jsonResp(200, plugin),
		"GET /api/v1/companies/acme/plugin-tabs": jsonResp(200, api.ListBody[api.PluginTab]{Items: []api.PluginTab{{Plugin: "showcase", Key: "painel", Title: "Painel",
			URL: "/c/acme/plugins/showcase/painel"}}}),
		"GET /api/v1/companies/acme/plugins/showcase/flow-triggers": jsonResp(200, api.PluginFlowTriggers{
			Triggers: []api.PluginFlowTriggerDef{{Key: "novo", Title: "Novo"}}, Links: []api.PluginFlowLink{{Trigger: "novo", FlowSlug: "triagem", FlowName: "Triagem"}}}),
		"PUT /api/v1/companies/acme/plugins/showcase/flow-triggers/novo/flows/triagem":    func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		"DELETE /api/v1/companies/acme/plugins/showcase/flow-triggers/novo/flows/triagem": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		"GET /api/v1/companies/acme/plugin-skill-sources": jsonResp(200, api.ListBody[api.PluginSkillSourceItem]{Items: []api.PluginSkillSourceItem{
			{Plugin: "showcase", Key: "c", Title: "C", URL: "plugin://showcase/c", SourceID: &srcID}}}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	run := func(args ...string) (int, string, string) {
		t.Helper()
		return exec(t, Runtime{}, env, "", args...)
	}

	code, out, errOut := run("plugin", "cap", "revoke", "showcase", "pages")
	if code != 0 || !strings.Contains(out, "plugin:showcase/p") || !strings.Contains(out, "plugin://showcase/c") || !strings.Contains(out, "painel") {
		t.Fatalf("cap revoke: %d %q %q", code, out, errOut)
	}
	if f.last().Method != "POST" || !strings.HasSuffix(f.last().Path, "/pages/revoke") {
		t.Fatalf("revoke usa POST .../revoke: %+v", f.last())
	}
	if code, _, errOut = run("plugin", "cap", "grant", "showcase", "pages"); code != 0 || !strings.HasSuffix(f.last().Path, "/pages/grant") {
		t.Fatalf("cap grant: %d %q %+v", code, errOut, f.last())
	}
	if code, out, _ = run("plugin", "tabs"); code != 0 || !strings.Contains(out, "/c/acme/plugins/showcase/painel") {
		t.Fatalf("tabs: %d %q", code, out)
	}
	if code, out, _ = run("plugin", "trigger", "list", "showcase"); code != 0 || !strings.Contains(out, "triagem") || !strings.Contains(out, "novo") {
		t.Fatalf("trigger list: %d %q", code, out)
	}
	if code, out, _ = run("plugin", "trigger", "link", "showcase", "novo", "triagem"); code != 0 || f.last().Method != "PUT" || !strings.Contains(out, "ligado") {
		t.Fatalf("trigger link: %d %q %+v", code, out, f.last())
	}
	if code, out, _ = run("plugin", "trigger", "unlink", "showcase", "novo", "triagem"); code != 0 || f.last().Method != "DELETE" || !strings.Contains(out, "desligado") {
		t.Fatalf("trigger unlink: %d %q %+v", code, out, f.last())
	}
	if code, out, _ = run("plugin", "skill-sources"); code != 0 || !strings.Contains(out, "plugin://showcase/c") || !strings.Contains(out, "conectada") {
		t.Fatalf("skill-sources: %d %q", code, out)
	}
	if code, out, _ = run("--json", "plugin", "tabs"); code != 0 || !strings.Contains(out, `"plugin": "showcase"`) {
		t.Fatalf("tabs --json: %d %q", code, out)
	}
}
