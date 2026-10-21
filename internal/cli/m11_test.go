package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/api"
)

func TestPersonaReadinessCommands(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	notReady := api.PersonaReadiness{PersonaID: "p1", Status: "not_ready", Agent: "claude", Model: "sonnet", EnvironmentSource: "default", Reason: "auth: sem login",
		Blocking: true, CheckedAt: &now, Steps: []api.ReadinessStep{{Name: "environment", Status: "ok", Detail: "local"}, {Name: "binary", Status: "ok", Detail: "/usr/bin/claude"},
			{Name: "auth", Status: "fail", Detail: "sem login"}, {Name: "model", Status: "ok", Detail: "sonnet"}}}
	ready := notReady
	ready.Status, ready.Reason, ready.Blocking = "ready", "", false
	ready.Steps = append([]api.ReadinessStep(nil), notReady.Steps...)
	ready.Steps[2] = api.ReadinessStep{Name: "auth", Status: "ok", Detail: "login do harness"}
	untested := api.PersonaReadiness{PersonaID: "p2", Status: "untested", Steps: []api.ReadinessStep{}}
	cur := notReady
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/personas/ana/readiness": func(w http.ResponseWriter, r *http.Request) { jsonResp(200, cur)(w, r) },
		"GET /api/v1/companies/acme/personas/ana/readiness":  func(w http.ResponseWriter, r *http.Request) { jsonResp(200, cur)(w, r) },
		"GET /api/v1/companies/acme/personas/bia/readiness":  jsonResp(200, untested),
		"GET /api/v1/companies/acme/readiness":               jsonResp(200, api.ListBody[api.PersonaReadiness]{Items: []api.PersonaReadiness{notReady, untested}}),
		"GET /api/v1/companies/acme/personas":                jsonResp(200, api.ListBody[api.Persona]{Items: []api.Persona{{ID: "p1", Slug: "ana"}, {ID: "p2", Slug: "bia"}}}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}

	// auth quebrada: o comando mostra os passos e sai com erro
	code, out, errOut := exec(t, Runtime{}, env, "", "persona", "test", "ana")
	if code == 0 || !strings.Contains(errOut, "não está pronta") {
		t.Fatalf("persona não pronta deve falhar: %d %q", code, errOut)
	}
	for _, want := range []string{"NÃO PRONTA", "auth", "fail — sem login", "Motivo do bloqueio", "auth: sem login", "bloqueado até um teste passar", "claude sonnet"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q em:\n%s", want, out)
		}
	}
	if f.last().Method != "POST" {
		t.Fatalf("test usa POST: %s", f.last().Method)
	}

	// consertada: sai com sucesso
	cur = ready
	code, out, _ = exec(t, Runtime{}, env, "", "persona", "test", "ana")
	if code != 0 || !strings.Contains(out, "pronta") || strings.Contains(out, "Motivo do bloqueio") {
		t.Fatalf("persona pronta: %d %q", code, out)
	}
	// JSON
	code, out, _ = exec(t, Runtime{}, env, "", "--json", "persona", "test", "ana")
	var got api.PersonaReadiness
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.Status != "ready" {
		t.Fatalf("json: %d %q", code, out)
	}

	// leitura de uma persona nunca testada
	code, out, _ = exec(t, Runtime{}, env, "", "persona", "readiness", "bia")
	if code != 0 || !strings.Contains(out, "nunca testada") {
		t.Fatalf("nunca testada: %d %q", code, out)
	}
	// quadro da empresa
	code, out, _ = exec(t, Runtime{}, env, "", "persona", "readiness")
	if code != 0 || !strings.Contains(out, "ana") || !strings.Contains(out, "NÃO PRONTA") || !strings.Contains(out, "bia") {
		t.Fatalf("quadro: %d %q", code, out)
	}
}

func TestModelAndProjectEnvCommands(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/models": jsonResp(200, api.ListBody[api.CatalogModel]{Items: []api.CatalogModel{
			{Agent: "claude", Model: "sonnet", Origins: []string{"builtin", "price"}, Priced: true, PriceModel: "claude-sonnet"},
			{Agent: "claude", Model: "modelo-novo", Origins: []string{"run"}}}}),
		"PUT /api/v1/companies/acme/projects/web/environment": jsonResp(200, api.Project{ID: "pr1", Slug: "web", EnvironmentID: "e1"}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, out, _ := exec(t, Runtime{}, env, "", "model", "list", "--agent", "claude")
	if code != 0 || !strings.Contains(out, "claude-sonnet") || !strings.Contains(out, "sem preço (usa o de reserva)") || !strings.Contains(out, "modelo-novo") {
		t.Fatalf("model list: %d %q", code, out)
	}
	if !strings.Contains(f.last().Query, "agent=claude") {
		t.Fatalf("filtro por harness: %q", f.last().Query)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "project", "env", "web", "build")
	if code != 0 || !strings.Contains(out, "web") {
		t.Fatalf("project env: %d %q", code, out)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(f.last().Body), &m)
	if diff := cmp.Diff(map[string]any{"environment": "build"}, m); diff != "" {
		t.Fatal(diff)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "project", "env", "web"); code != 2 {
		t.Fatalf("faltou o ambiente: %d", code)
	}
}

func TestEnvCreateCloudAndLocal(t *testing.T) {
	created := api.Environment{ID: "e1", Slug: "nuvem", Name: "Nuvem", Driver: "cloud", Available: true, KeySet: true,
		Cloud: &api.CloudConfig{Provider: "daytona", CPUs: 2, MemoryGiB: 4, DiskGiB: 10, Network: "full"}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/environments": jsonResp(201, created),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	keyFile := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(keyFile, []byte("dtn_segredo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := exec(t, Runtime{}, env, "", "env", "create", "Nuvem", "--driver", "cloud", "--api-key-file", keyFile, "--cloud-cpus", "4", "--cloud-network", "none", "--env", "X=1")
	if code != 0 || !strings.Contains(out, "Ambiente cadastrado: Nuvem") {
		t.Fatalf("create cloud: %d %q %q", code, out, errOut)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(f.last().Body), &m)
	cl, _ := m["cloud"].(map[string]any)
	if m["driver"] != "cloud" || m["api_key"] != "dtn_segredo" || cl["cpus"] != float64(4) || cl["network"] != "none" || cl["provider"] != "daytona" {
		t.Fatalf("corpo: %v", m)
	}
	// a chave nunca vai por flag, só por arquivo
	if code, _, _ = exec(t, Runtime{}, env, "", "env", "create", "Nuvem", "--driver", "cloud", "--api-key", "x"); code != 2 {
		t.Fatalf("--api-key não existe: %d", code)
	}
	code, _, _ = exec(t, Runtime{}, env, "", "env", "create", "Servidor", "--driver", "local", "--env", "DA_LOCAL=sim")
	if code != 0 {
		t.Fatalf("create local: %d", code)
	}
	m = map[string]any{}
	_ = json.Unmarshal([]byte(f.last().Body), &m)
	if m["driver"] != "local" || m["cloud"] != nil || m["ssh"] != nil {
		t.Fatalf("corpo local: %v", m)
	}
	if code, _, errOut = exec(t, Runtime{}, env, "", "env", "create", "X", "--driver", "docker"); code != 2 || !strings.Contains(errOut, "--driver aceita local, ssh, sandbox ou cloud") {
		t.Fatalf("driver desconhecido: %d %q", code, errOut)
	}
}
