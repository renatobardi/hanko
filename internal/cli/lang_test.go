package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
)

func envLang(v string) map[string]string { return map[string]string{"HANKO_LANG": v} }

func TestHelpEmIngles(t *testing.T) {
	code, out, _ := exec(t, Runtime{}, envLang("en"), "", "--help")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"Hanko — control plane for teams of AI agents", "Usage:", "Available Commands:", "Flags:",
		"hanko [command]", "Manages tasks", "Manages companies", "JSON output", "company: id or slug (HANKO_COMPANY)",
		"help for hanko", `Use "hanko [command] --help" for more information about a command.`, "Help about any command"} {
		if !strings.Contains(out, want) {
			t.Errorf("help em inglês sem %q:\n%s", want, out)
		}
	}
	for _, pt := range []string{"Uso:", "Gerencia", "saída em JSON", "Comandos disponíveis"} {
		if strings.Contains(out, pt) {
			t.Errorf("help em inglês com texto em português %q", pt)
		}
	}

	_, out, _ = exec(t, Runtime{}, envLang("en"), "", "task", "create", "--help")
	for _, want := range []string{"Creates a task", "create <title>", "Global Flags:", "assignee persona (id or slug)", "description", "help for create"} {
		if !strings.Contains(out, want) {
			t.Errorf("task create --help sem %q:\n%s", want, out)
		}
	}
	_, out, _ = exec(t, Runtime{}, envLang("en"), "", "help", "task")
	if !strings.Contains(out, "Manages tasks") || !strings.Contains(out, "Available Commands:") {
		t.Errorf("help task:\n%s", out)
	}
	_, out, _ = exec(t, Runtime{}, envLang("en"), "", "gate", "ls", "--help")
	if !strings.Contains(out, "Aliases:") {
		t.Errorf("gate ls --help sem Aliases:\n%s", out)
	}
	_, out, _ = exec(t, Runtime{}, envLang("en"), "", "auth", "create-owner", "--help")
	if !strings.Contains(out, "Creates the installation owner. The password comes from HANKO_OWNER_PASSWORD") {
		t.Errorf("auth create-owner --help:\n%s", out)
	}
}

func TestHelpPtBRPorPadrao(t *testing.T) {
	for _, env := range []map[string]string{nil, envLang("pt-BR"), {"LANG": "fr_FR.UTF-8"}} {
		_, out, _ := exec(t, Runtime{}, env, "", "--help")
		for _, want := range []string{"Hanko — control plane para times de agentes de IA", "Uso:", "Comandos disponíveis:", "Opções:",
			"Gerencia tasks", "saída em JSON", "ajuda de hanko", "Ajuda sobre qualquer comando"} {
			if !strings.Contains(out, want) {
				t.Errorf("env %v: help sem %q:\n%s", env, want, out)
			}
		}
		if strings.Contains(out, "Usage:") || strings.Contains(out, "Flags:") {
			t.Errorf("env %v: help em pt-BR com rótulo em inglês:\n%s", env, out)
		}
	}
}

// Nenhum comando pode deixar texto em português escapar do help em inglês, nem rótulo em inglês no pt-BR.
func TestHelpDeTodosOsComandos(t *testing.T) {
	probe := &env{getenv: func(string) string { return "" }}
	var paths [][]string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		paths = append(paths, path)
		for _, sub := range c.Commands() {
			walk(sub, append(append([]string{}, path...), sub.Name()))
		}
	}
	walk(probe.root(), nil)
	if len(paths) < 60 {
		t.Fatalf("só %d comandos; a varredura está quebrada", len(paths))
	}
	accent := func(s string) string {
		for _, r := range s {
			if r > unicode.MaxASCII && r != '—' && r != '…' && r != '→' && r != '·' && r != '├' && r != '└' && r != '│' && r != '─' {
				return string(r)
			}
		}
		return ""
	}
	for _, p := range paths {
		args := append(append([]string{}, p...), "--help")
		_, out, _ := exec(t, Runtime{}, envLang("en"), "", args...)
		if a := accent(out); a != "" {
			t.Errorf("hanko %s --help em inglês com %q:\n%s", strings.Join(p, " "), a, out)
		}
		_, out, _ = exec(t, Runtime{}, envLang("pt-BR"), "", args...)
		for _, en := range []string{"Usage:", "Flags:", "Available Commands:", "help for "} {
			if strings.Contains(out, en) {
				t.Errorf("hanko %s --help em pt-BR com %q:\n%s", strings.Join(p, " "), en, out)
			}
		}
	}
}

func TestTabelaEmIngles(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/tasks": jsonResp(200, list(task1))})
	base := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	en := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme", "HANKO_LANG": "en"}
	code, out, _ := exec(t, Runtime{}, en, "", "task", "list")
	want := "KEY    STATUS  PRIORITY  PERSONA  TITLE\nHNK-1  todo    high      Marina   Implementar login\n"
	if code != 0 || out != want {
		t.Fatalf("%d %q", code, out)
	}
	// dados do usuário não são traduzidos; sem HANKO_LANG o pt-BR continua igual
	code, out, _ = exec(t, Runtime{}, base, "", "task", "list")
	if code != 0 || !strings.HasPrefix(out, "CHAVE  STATUS  PRIORIDADE  PERSONA  TÍTULO\n") {
		t.Fatalf("%d %q", code, out)
	}

	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/tasks": jsonResp(200, list[api.Task]())})
	code, out, _ = exec(t, Runtime{}, map[string]string{"HANKO_URL": ts2.URL, "HANKO_COMPANY": "acme", "LANG": "en_US.UTF-8"}, "", "task", "list")
	if code != 0 || out != "No tasks found.\n" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestShowEmInglesFormataDataEMoeda(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/tasks/HNK-1": jsonResp(200, task1),
		"GET /api/v1/companies/acme/tasks/" + task1.ID + "/comments": jsonResp(200, list(
			api.Comment{Body: "primeiro", Author: api.ActorRef{Type: "human"}, CreatedAt: time.Date(2026, 10, 13, 9, 1, 0, 0, time.UTC)})),
		"GET /api/v1/companies/acme/runs": jsonResp(200, list(api.Run{ID: "run-1", TaskKey: "HNK-1", Status: "done", Trigger: "assign"})),
	})
	en := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme", "HANKO_LANG": "en"}
	code, out, errOut := exec(t, Runtime{}, en, "", "task", "show", "HNK-1")
	if code != 0 {
		t.Fatalf("%d %q", code, errOut)
	}
	for _, want := range []string{"Priority:", "Created at:", "2026-10-13 09:00", "[2026-10-13 09:01] human: primeiro", "Comments (1):", "Runs (1):", "TRIGGER"} {
		if !strings.Contains(out, want) {
			t.Errorf("sem %q:\n%s", want, out)
		}
	}
	_, out, _ = exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}, "", "task", "show", "HNK-1")
	if !strings.Contains(out, "13/10/2026 09:00") || !strings.Contains(out, "Comentários (1):") {
		t.Errorf("pt-BR mudou:\n%s", out)
	}

	e := &env{getenv: func(k string) string { return en[k] }}
	e.root()
	if got := e.usd(1.5); got != "$1.50" {
		t.Errorf("usd en = %q", got)
	}
	p := &env{getenv: func(string) string { return "" }}
	p.root()
	if got := p.usd(1.5); got != "US$ 1.50" {
		t.Errorf("usd pt = %q", got)
	}
}

func TestErrosDeUsoEmIngles(t *testing.T) {
	code, _, errOut := exec(t, Runtime{}, envLang("en"), "", "task", "list")
	if code != 2 || !strings.Contains(errOut, "error: company not provided: use --company or HANKO_COMPANY") ||
		!strings.Contains(errOut, "Use 'hanko --help' to see the options.") {
		t.Fatalf("%d %q", code, errOut)
	}
	code, _, errOut = exec(t, Runtime{}, nil, "", "task", "list")
	if code != 2 || !strings.Contains(errOut, "erro: empresa não informada") || !strings.Contains(errOut, "Use 'hanko --help' para ver as opções.") {
		t.Fatalf("%d %q", code, errOut)
	}
	// erros vindos da API não são traduzidos
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{})
	code, _, errOut = exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme", "HANKO_LANG": "en"}, "", "task", "list")
	if code != 1 || !strings.Contains(errOut, "rota desconhecida") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestPrecedenciaDoIdioma(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		en   bool
	}{
		{"padrão", nil, false},
		{"LANG en", map[string]string{"LANG": "en_US.UTF-8"}, true},
		{"LC_MESSAGES vence LANG", map[string]string{"LC_MESSAGES": "pt_BR.UTF-8", "LANG": "en_US.UTF-8"}, false},
		{"LC_ALL vence LC_MESSAGES", map[string]string{"LC_ALL": "en_GB.UTF-8", "LC_MESSAGES": "pt_BR.UTF-8"}, true},
		{"HANKO_LANG vence LC_ALL", map[string]string{"HANKO_LANG": "pt-BR", "LC_ALL": "en_US.UTF-8"}, false},
		{"HANKO_LANG en vence LANG pt", map[string]string{"HANKO_LANG": "en", "LANG": "pt_BR.UTF-8"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, out, _ := exec(t, Runtime{}, c.env, "", "version", "--help")
			if got := strings.Contains(out, "Usage:"); got != c.en {
				t.Errorf("inglês = %v, quer %v:\n%s", got, c.en, out)
			}
		})
	}
}
