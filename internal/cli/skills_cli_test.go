package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

var skill1 = api.Skill{ID: "0198f2a0-bbbb-7000-8000-000000000001", Slug: "revisar-pr", Name: "Revisar PR", Description: "Use ao revisar PR.",
	Body: "# Checklist\n\n- testes\n", Version: 2, CreatedAt: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}

func TestSkillCreateFromFileAndStdin(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST /api/v1/companies/acme/skills": jsonResp(201, skill1)})
	md := "---\nname: revisar-pr\ndescription: Use ao revisar PR.\n---\n# Checklist\n"
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), md, "skill", "create", "Revisar PR", "--file", "-")
	if code != 0 || !strings.Contains(out, "Skill criada: Revisar PR (revisar-pr), versão 2") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	h := f.last()
	if h.Method != "POST" || !strings.Contains(h.Body, `"name":"Revisar PR"`) || !strings.Contains(h.Body, "# Checklist") {
		t.Fatalf("%+v", h)
	}
	// sem --file é erro de uso
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "create", "X"); code != 2 {
		t.Fatalf("código = %d", code)
	}
}

func TestSkillListShowMD(t *testing.T) {
	n := 1
	listed := skill1
	listed.Body, listed.Personas = "", &n
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/skills":            jsonResp(200, list(listed)),
		"GET /api/v1/companies/acme/skills/revisar-pr": jsonResp(200, skill1),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "list")
	if code != 0 || !strings.Contains(out, "revisar-pr") || !strings.Contains(out, "Revisar PR") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "show", "revisar-pr", "--md")
	if code != 0 || !strings.HasPrefix(out, "---\nname: revisar-pr") || !strings.Contains(out, "# Checklist") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestSkillEditDeleteAndToggle(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/companies/acme/skills/revisar-pr":            jsonResp(200, skill1),
		"DELETE /api/v1/companies/acme/skills/revisar-pr":           jsonResp(204, nil),
		"PUT /api/v1/companies/acme/personas/ada/skills/revisar-pr": jsonResp(204, nil),
		"GET /api/v1/companies/acme/skills/revisar-pr/personas":     jsonResp(200, list(api.SkillPersona{Slug: "ada", Name: "Ada", Enabled: true})),
	})
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "edit", "revisar-pr"); code != 2 {
		t.Fatalf("edit sem nada: %d", code)
	}
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "edit", "revisar-pr", "--description", "nova")
	if code != 0 || !strings.Contains(f.last().Body, `"description":"nova"`) {
		t.Fatalf("%d %q %q %+v", code, out, errs, f.last())
	}
	code, out, errs = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "enable", "revisar-pr", "ada")
	if code != 0 || !strings.Contains(out, "Skill revisar-pr habilitada para ada") || !strings.Contains(f.last().Body, `"enabled":true`) {
		t.Fatalf("%d %q %q %+v", code, out, errs, f.last())
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "personas", "revisar-pr")
	if code != 0 || !strings.Contains(out, "ada") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "delete", "revisar-pr")
	if code != 0 || !strings.Contains(out, "Skill revisar-pr apagada") || f.last().Method != "DELETE" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestIntegrationCreateMCPSendsSecretsOnlyToServer(t *testing.T) {
	created := api.Integration{ID: "0198f2a0-cccc-7000-8000-000000000001", Kind: "mcp", Slug: "docs", Name: "Docs", Enabled: true, SecretSet: true,
		Config: api.IntegrationConfig{Transport: "http", URL: "https://mcp.example.com/mcp", HeaderNames: []string{"Authorization"}}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST /api/v1/companies/acme/integrations": jsonResp(201, created)})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "create", "Docs", "--kind", "mcp", "--url", "https://mcp.example.com/mcp",
		"--header", "Authorization=Bearer cli_tok_123456", "--all-personas")
	if code != 0 || !strings.Contains(out, "Integração criada: Docs (mcp, docs)") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if strings.Contains(out+errs, "cli_tok_123456") {
		t.Fatal("o CLI imprimiu o segredo")
	}
	if b := f.last().Body; !strings.Contains(b, `"headers":{"Authorization":"Bearer cli_tok_123456"}`) || !strings.Contains(b, `"all_personas":true`) {
		t.Fatalf("corpo: %s", b)
	}
	// header mal formado é erro de uso
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "create", "X", "--kind", "mcp", "--url", "https://a.example.com", "--header", "semigual"); code != 2 {
		t.Fatalf("código = %d", code)
	}
	// --kind é obrigatório
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "create", "X"); code != 2 {
		t.Fatalf("sem --kind: %d", code)
	}
}

func TestIntegrationWebhookSecretShownOnce(t *testing.T) {
	created := api.Integration{ID: "0198f2a0-cccc-7000-8000-000000000002", Kind: "webhook", Slug: "deploy", Name: "Deploy", Enabled: true,
		Events: []string{"task.created"}, SecretSet: true, SecretHint: "whsec_ab", Secret: "whsec_abcdefghijklmnop"}
	shown := created
	shown.Secret = ""
	d200 := 200
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/integrations":                  jsonResp(201, created),
		"GET /api/v1/companies/acme/integrations/deploy":            jsonResp(200, shown),
		"GET /api/v1/companies/acme/integrations/deploy/deliveries": jsonResp(200, list(api.WebhookDelivery{Event: "task.created", Status: "success", Attempts: 1, HTTPStatus: &d200})),
		"POST /api/v1/companies/acme/integrations/deploy/test":      jsonResp(200, api.IntegrationTestResult{OK: true, Status: 200, LatencyMs: 9}),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "create", "Deploy", "--kind", "webhook", "--url", "https://hooks.example.com/x", "--event", "task.created")
	if code != 0 || !strings.Contains(out, "whsec_abcdefghijklmnop") {
		t.Fatalf("o segredo gerado deveria aparecer na criação: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "show", "deploy")
	if code != 0 || strings.Contains(out, "whsec_abcdefghijklmnop") || !strings.Contains(out, "task.created") {
		t.Fatalf("show não pode trazer o segredo: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "deliveries", "deploy")
	if code != 0 || !strings.Contains(out, "task.created") || !strings.Contains(out, "200") {
		t.Fatalf("%d %q", code, out)
	}
	if code, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "test", "deploy"); code != 0 {
		t.Fatalf("test: %d", code)
	}
}

func TestIntegrationTestFailureExitsNonZero(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/integrations/deploy/test": jsonResp(200, api.IntegrationTestResult{OK: false, Error: "connection refused"}),
	})
	code, _, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "test", "deploy")
	if code == 0 {
		t.Fatalf("teste que falha deveria sair com código != 0 (%q)", errs)
	}
}

func TestIntegrationRekeyAndAttach(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/integrations/rekey-secrets":                   jsonResp(200, api.RekeyResult{Rewritten: 3}),
		"PUT /api/v1/companies/acme/personas/ada/integrations/docs": jsonResp(204, nil),
	})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "rekey")
	if code != 0 || !strings.Contains(out, "3 segredo(s) reescrito(s)") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	code, out, errs = exec(t, Runtime{}, gateEnv(ts.URL), "", "integration", "attach", "docs", "ada")
	if code != 0 || !strings.Contains(out, "ligado para ada") || !strings.Contains(f.last().Body, `"enabled":true`) {
		t.Fatalf("%d %q %q %+v", code, out, errs, f.last())
	}
}
