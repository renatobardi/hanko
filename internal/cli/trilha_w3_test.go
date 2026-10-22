package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// Trilha onda 3 (ADR-0060) na CLI: contextos, sandbox, onboard, perfil, favoritos e novidades.

func TestContextAddUseListRm(t *testing.T) {
	url, _ := loginServer(t, "approved") // serve /whoami
	dir := t.TempDir()
	env := map[string]string{"HANKO_CONFIG_DIR": dir}

	// add com chave pela entrada padrão (nunca por flag); confere o servidor e guarda o e-mail devolvido
	code, out, errb := exec(t, Runtime{}, env, "hk_abc123\n", "context", "add", "prod", "--url", url+"/", "--token-stdin", "--use")
	if code != 0 || !strings.Contains(out, "contexto prod salvo") {
		t.Fatalf("add: %d %s %s", code, out, errb)
	}
	raw, _ := os.ReadFile(dir + "/contexts.json")
	var cs cliContexts
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	if c := cs.Contexts["prod"]; c.URL != url || c.Token != "hk_abc123" || c.User != "ana@x.com" || cs.Current != "prod" {
		t.Fatalf("contexto gravado: %+v (atual %q)", c, cs.Current)
	}
	// segundo contexto, sem chave (servidor trusted) e sem conferir
	if code, out, errb = exec(t, Runtime{}, env, "", "context", "add", "local", "--url", "http://127.0.0.1:9999", "--no-check"); code != 0 {
		t.Fatalf("add local: %d %s %s", code, out, errb)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "context", "list")
	if code != 0 || !strings.Contains(out, "prod") || !strings.Contains(out, "local") || strings.Contains(out, "hk_abc123") {
		t.Fatalf("list nunca mostra a chave: %d %s", code, out)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "--json", "context", "list")
	if code != 0 || strings.Contains(out, "hk_abc123") || !strings.Contains(out, `"has_token": true`) {
		t.Fatalf("list json: %s", out)
	}
	// use troca o ativo; current mostra
	if code, out, _ = exec(t, Runtime{}, env, "", "context", "use", "local"); code != 0 {
		t.Fatalf("use: %s", out)
	}
	if _, out, _ = exec(t, Runtime{}, env, "", "context", "current"); !strings.Contains(out, "local") || !strings.Contains(out, "9999") {
		t.Fatalf("current: %s", out)
	}
	if code, _, errb = exec(t, Runtime{}, env, "", "context", "use", "nao-existe"); code != 2 || !strings.Contains(errb, "não existe") {
		t.Fatalf("use inexistente: %d %s", code, errb)
	}
	// --context vale só para a chamada
	if code, _, errb = exec(t, Runtime{}, env, "", "--context", "fantasma", "context", "list"); code != 2 {
		t.Fatalf("--context inexistente deve ser erro de uso: %d %s", code, errb)
	}
	// rm do ativo limpa o ativo
	if code, _, _ = exec(t, Runtime{}, env, "", "context", "rm", "local"); code != 0 {
		t.Fatal("rm")
	}
	if _, out, _ = exec(t, Runtime{}, env, "", "context", "current"); !strings.Contains(out, "nenhum contexto ativo") {
		t.Fatalf("depois do rm: %s", out)
	}
}

func TestContextAddValidation(t *testing.T) {
	env := map[string]string{"HANKO_CONFIG_DIR": t.TempDir()}
	for name, args := range map[string][]string{
		"sem url":      {"context", "add", "x"},
		"nome ruim":    {"context", "add", "a b", "--url", "http://h"},
		"esquema ruim": {"context", "add", "x", "--url", "ftp://h", "--no-check"},
		"credencial":   {"context", "add", "x", "--url", "http://u:p@h", "--no-check"},
		"token vazio":  {"context", "add", "x", "--url", "http://h", "--token-stdin", "--no-check"},
	} {
		if code, _, _ := exec(t, Runtime{}, env, "", args...); code != 2 {
			t.Errorf("%s: código %d, quer 2", name, code)
		}
	}
	// servidor que não responde: erro e nada gravado
	if code, _, _ := exec(t, Runtime{}, env, "", "context", "add", "x", "--url", "http://127.0.0.1:1"); code == 0 {
		t.Error("servidor fora do ar deve falhar sem --no-check")
	}
	if _, err := os.Stat(env["HANKO_CONFIG_DIR"] + "/contexts.json"); err == nil {
		t.Error("nada deveria ter sido gravado")
	}
}

// Um contexto sem chave aponta o servidor (trusted) e a CLI o usa.
func TestTokenlessContextSelectsTheServer(t *testing.T) {
	srv, f := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/whoami": jsonResp(200, api.Whoami{})})
	env := map[string]string{"HANKO_CONFIG_DIR": t.TempDir()}
	if code, out, errb := exec(t, Runtime{}, env, "", "context", "add", "t", "--url", srv.URL, "--no-check", "--use"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	if code, out, errb := exec(t, Runtime{}, env, "", "whoami"); code != 0 || !strings.Contains(out, "sem login") {
		t.Fatalf("whoami: %d %s %s", code, out, errb)
	}
	if len(f.hits) == 0 || f.hits[0].Auth != "" {
		t.Fatalf("a chamada deveria ir ao servidor do contexto, sem credencial: %+v", f.hits)
	}
}

func TestOnboardRunsStepsAndStopsWhereInputIsMissing(t *testing.T) {
	steps := func(done ...string) []api.OnboardingStep {
		var out []api.OnboardingStep
		for _, k := range []string{"company", "harness", "persona", "task"} {
			d := k == "company"
			for _, x := range done {
				d = d || x == k
			}
			out = append(out, api.OnboardingStep{Key: k, Done: d})
		}
		return out
	}
	state := api.OnboardingState{Steps: steps(), Current: "harness"}
	calls := []string{}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/companies":                 jsonResp(200, map[string]any{"items": []api.Company{{Slug: "acme", Name: "Acme"}}}),
		"GET /api/v1/companies/acme/onboarding": func(w http.ResponseWriter, r *http.Request) { jsonResp(200, state)(w, r) },
		"POST /api/v1/companies/acme/onboarding/probe": func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, "probe")
			state = api.OnboardingState{Steps: steps("harness"), Current: "persona", HarnessAgent: "claude", HarnessStatus: "ready"}
			jsonResp(200, api.OnboardingProbe{Agent: "claude", Status: "ready", Steps: []api.ReadinessStep{{Name: "binary", Status: "ok", Detail: "/usr/bin/claude"}}})(w, r)
		},
		"POST /api/v1/companies/acme/onboarding/team": func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, "team")
			state = api.OnboardingState{Steps: steps("harness", "persona"), Current: "task"}
			jsonResp(200, api.OnboardingTeamInstalled{Package: "squad", Personas: 3, Lead: "cto", LeadID: "11111111-1111-1111-1111-111111111111"})(w, r)
		},
		"GET /api/v1/companies/acme/personas/cto": jsonResp(200, api.Persona{ID: "11111111-1111-1111-1111-111111111111", Slug: "cto", Name: "Tess"}),
		"POST /api/v1/companies/acme/onboarding/task": func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, "task")
			state = api.OnboardingState{Steps: steps("harness", "persona", "task"), Complete: true}
			jsonResp(201, api.Task{Key: "HNK-1"})(w, r)
		},
	}
	srv, _ := newFakeAPI(t, routes)
	env := map[string]string{"HANKO_CONFIG_DIR": t.TempDir()}

	// sem flags: só mostra o estado e diz o próximo passo
	code, out, errb := exec(t, Runtime{}, env, "", "--url", srv.URL, "onboard")
	if code != 0 || !strings.Contains(out, "[ ] Harness") || !strings.Contains(out, "hanko onboard --harness claude") || len(calls) != 0 {
		t.Fatalf("estado: %d %s %s %v", code, out, errb, calls)
	}
	// com tudo: percorre os três passos de uma vez
	code, out, errb = exec(t, Runtime{}, env, "", "--url", srv.URL, "onboard", "--harness", "claude", "--team", "squad", "--task", "Resumir o README")
	if code != 0 || strings.Join(calls, ",") != "probe,team,task" || !strings.Contains(out, "task HNK-1 criada para Tess") || !strings.Contains(out, "a primeira task está rodando") {
		t.Fatalf("fluxo completo: %d %s %s %v", code, out, errb, calls)
	}
	// de novo: já completo, nada é refeito
	calls = nil
	if code, out, _ = exec(t, Runtime{}, env, "", "--url", srv.URL, "onboard", "--harness", "claude"); code != 0 || len(calls) != 0 || !strings.Contains(out, "Tudo pronto") {
		t.Fatalf("idempotente: %d %s %v", code, out, calls)
	}
}

func TestOnboardFailedProbeExplainsAndExitsNonZero(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies":                        jsonResp(200, map[string]any{"items": []api.Company{{Slug: "acme", Name: "Acme"}}}),
		"GET /api/v1/companies/acme/onboarding":        jsonResp(200, api.OnboardingState{Current: "harness", Steps: []api.OnboardingStep{{Key: "company", Done: true}, {Key: "harness"}}}),
		"POST /api/v1/companies/acme/onboarding/probe": jsonResp(200, api.OnboardingProbe{Agent: "codex", Status: "not_ready", Reason: "auth: sem login", Steps: []api.ReadinessStep{{Name: "auth", Status: "fail", Detail: "sem login"}}}),
	})
	code, out, errb := exec(t, Runtime{}, map[string]string{"HANKO_CONFIG_DIR": t.TempDir()}, "", "--url", srv.URL, "onboard", "--harness", "codex")
	if code != 1 || !strings.Contains(out, "NÃO está pronto: auth: sem login") || !strings.Contains(errb, "pule com --skip-probe") {
		t.Fatalf("%d %s %s", code, out, errb)
	}
}

func TestOnboardNoCompanyNeedsAName(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies": jsonResp(200, map[string]any{"items": []api.Company{}})})
	code, _, errb := exec(t, Runtime{}, map[string]string{"HANKO_CONFIG_DIR": t.TempDir()}, "", "--url", srv.URL, "onboard")
	if code != 2 || !strings.Contains(errb, "--company-name") {
		t.Fatalf("%d %s", code, errb)
	}
}

func TestProfileFavoriteAndWhatsNew(t *testing.T) {
	srv, fk := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/me/profile": jsonResp(200, api.UserProfile{Email: "ana@x.com", Name: "Ana", Timezone: "UTC"}),
		"PATCH /api/v1/me/profile": func(w http.ResponseWriter, r *http.Request) {
			jsonResp(200, api.UserProfile{Email: "ana@x.com", Name: "Ana Souza"})(w, r)
		},
		"GET /api/v1/companies/acme/nav":           jsonResp(200, api.NavState{Favorites: []api.NavItem{{Path: "/tasks/HNK-1", Title: "Revisar"}}, Recents: []api.NavItem{}}),
		"PUT /api/v1/companies/acme/nav/favorites": jsonResp(200, api.NavState{}),
		"GET /api/v1/whats-new":                    jsonResp(200, api.WhatsNew{Installed: "v1", Version: "Unreleased", Body: "- novidade", Key: "1"}),
		"POST /api/v1/whats-new/dismiss":           func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) },
	})
	env := map[string]string{"HANKO_CONFIG_DIR": t.TempDir()}
	if code, out, _ := exec(t, Runtime{}, env, "", "--url", srv.URL, "profile", "show"); code != 0 || !strings.Contains(out, "ana@x.com") {
		t.Fatalf("show: %s", out)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "--url", srv.URL, "profile", "set"); code != 2 {
		t.Fatalf("set sem flags: %d", code)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "--url", srv.URL, "profile", "set", "--name", "Ana Souza", "--locale", ""); code != 0 || !strings.Contains(out, "Ana Souza") || !strings.Contains(fk.hits[len(fk.hits)-1].Body, `"locale":""`) {
		t.Fatalf("set: %s patch=%q", out, fk.hits[len(fk.hits)-1].Body)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "--url", srv.URL, "-c", "acme", "favorite", "list"); code != 0 || !strings.Contains(out, "/tasks/HNK-1") {
		t.Fatalf("favorite list: %s", out)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "--url", srv.URL, "-c", "acme", "favorite", "add", "/tasks/HNK-2"); code != 0 || !strings.Contains(out, "favorito: /tasks/HNK-2") {
		t.Fatalf("favorite add: %s", out)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "--url", srv.URL, "whats-new"); code != 0 || !strings.Contains(out, "- novidade") {
		t.Fatalf("whats-new: %s", out)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "--url", srv.URL, "whats-new", "--dismiss"); code != 0 || !strings.Contains(out, "dispensadas") {
		t.Fatalf("dismiss: %s", out)
	}
}

// `sandbox up --no-serve` cria o banco e o contexto; `down` apaga os dois (precisa do Postgres de teste).
func TestSandboxUpDownNoServe(t *testing.T) {
	admin := os.Getenv("HANKO_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definida")
	}
	dir := t.TempDir()
	env := map[string]string{"HANKO_CONFIG_DIR": dir, "HANKO_SANDBOX_DATABASE_URL": admin}
	t.Cleanup(func() { exec(t, Runtime{}, env, "", "sandbox", "down", "--name", "cli-teste-w3") })

	code, out, errb := exec(t, Runtime{}, env, "", "sandbox", "up", "--name", "cli-teste-w3", "--no-serve")
	if code != 0 || !strings.Contains(out, "hanko_sbx_cli_teste_w3") || !strings.Contains(out, "HANKO_DATABASE_URL=") || !strings.Contains(out, "HANKO_MODE=trusted") {
		t.Fatalf("up: %d %s %s", code, out, errb)
	}
	if _, out, _ = exec(t, Runtime{}, env, "", "context", "list"); !strings.Contains(out, "sandbox-cli-teste-w3") {
		t.Fatalf("o contexto deveria existir: %s", out)
	}
	if _, out, _ = exec(t, Runtime{}, env, "", "sandbox", "list"); !strings.Contains(out, "cli-teste-w3") {
		t.Fatalf("list: %s", out)
	}
	if code, out, errb = exec(t, Runtime{}, env, "", "sandbox", "down", "--name", "cli-teste-w3"); code != 0 || !strings.Contains(out, "apagada") {
		t.Fatalf("down: %d %s %s", code, out, errb)
	}
	if _, out, _ = exec(t, Runtime{}, env, "", "context", "list"); strings.Contains(out, "sandbox-cli-teste-w3") {
		t.Fatalf("o contexto deveria ter sumido: %s", out)
	}
	// sem Postgres informado é erro de uso
	if code, _, _ = exec(t, Runtime{}, map[string]string{"HANKO_CONFIG_DIR": dir}, "", "sandbox", "list"); code != 2 {
		t.Fatalf("sem URL: %d", code)
	}
}
