package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/storage"
)

var secret1 = api.Secret{ID: "0198f2a0-dddd-7000-8000-000000000001", Scope: "company", Name: "gateway-token", Reference: "${secret:gateway-token}",
	Provider: "local", Version: 1, CreatedAt: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)}

func TestSecretCreateReadsValueFromStdinAndNeverPrintsIt(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST /api/v1/companies/acme/secrets": jsonResp(201, secret1)})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "valor-super-secreto\n", "secret", "create", "gateway-token", "--value-stdin", "--description", "gw")
	if code != 0 || !strings.Contains(out, "Segredo criado: gateway-token (use ${secret:gateway-token})") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if strings.Contains(out+errs, "valor-super-secreto") {
		t.Fatal("a CLI não pode ecoar o valor")
	}
	h := f.last()
	if h.Method != "POST" || !strings.Contains(h.Body, `"value":"valor-super-secreto"`) || strings.Contains(h.Body, "\\n") {
		t.Fatalf("corpo enviado: %+v", h)
	}
	// --value e --value-stdin juntos é erro de uso
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "x", "secret", "create", "a", "--value", "x", "--value-stdin"); code != 2 {
		t.Fatalf("código = %d", code)
	}
	// cofre externo
	_, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "create", "cofre", "--vault-ref", "apps/hanko#token")
	if b := f.last().Body; !strings.Contains(b, `"provider":"vault"`) || !strings.Contains(b, `"external_ref":"apps/hanko#token"`) {
		t.Fatalf("vault: %s", b)
	}
	// escopo do usuário usa outra rota
	_, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "--user", "create", "pessoal", "--generate")
	if f.last().Path != "/api/v1/companies/acme/user-secrets" || !strings.Contains(f.last().Body, `"generate":true`) {
		t.Fatalf("user: %+v", f.last())
	}
}

func TestSecretRevealAndGenerateShowTheValueOnce(t *testing.T) {
	revealed := secret1
	revealed.Value, revealed.Revealed = "hk_valor-gerado-123", true
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/secrets/gateway-token/reveal": jsonResp(200, revealed),
		"GET /api/v1/companies/acme/secrets":                       jsonResp(200, list(secret1)),
		"POST /api/v1/companies/acme/secrets/gateway-token/rotate": jsonResp(200, revealed),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "reveal", "gateway-token")
	if code != 0 || !strings.Contains(out, "Valor (mostrado só agora; guarde-o): hk_valor-gerado-123") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "ls")
	if code != 0 || !strings.Contains(out, "gateway-token") || strings.Contains(out, "hk_") {
		t.Fatalf("ls: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "rotate", "gateway-token", "--generate")
	if code != 0 || !strings.Contains(out, "hk_valor-gerado-123") {
		t.Fatalf("rotate: %d %q", code, out)
	}
}

func TestSecretDeleteForceAndMigrateEnv(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"DELETE /api/v1/companies/acme/secrets/gateway-token": jsonResp(204, nil),
		"POST /api/v1/secrets/migrate-persona-env":            jsonResp(200, api.MigrateEnvResult{Personas: 2}),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "rm", "gateway-token", "--force")
	if code != 0 || !strings.Contains(out, "Segredo gateway-token apagado") || !strings.Contains(f.last().Query, "force=true") {
		t.Fatalf("%d %q %+v", code, out, f.last())
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "migrate-env")
	if code != 0 || !strings.Contains(out, "2 persona(s)") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestSettingsCommands(t *testing.T) {
	entry := api.InstanceSetting{Key: "acp.allow", Group: "acp", Kind: "bool", Label: "Liberar o harness ACP", Env: "HANKO_ACP_ALLOW", Value: true, Source: "db"}
	locked := api.InstanceSetting{Key: "alerts.silent_factor", Group: "alerts", Kind: "int", Env: "HANKO_ALERT_SILENT_FACTOR", Value: float64(3), Source: "env", Locked: true}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/instance/settings":                      jsonResp(200, list(entry, locked)),
		"PUT /api/v1/instance/settings/acp.allow":            jsonResp(200, entry),
		"DELETE /api/v1/instance/settings/acp.allow":         jsonResp(200, entry),
		"GET /api/v1/instance/settings-audit":                jsonResp(200, list(api.InstanceSettingAudit{Key: "acp.allow", New: []byte("true"), ActorType: "human", OccurredAt: time.Now()})),
		"PUT /api/v1/instance/settings/alerts.silent_factor": jsonResp(409, map[string]any{"title": "Conflito", "status": 409, "detail": "travada"}),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "settings", "list")
	if code != 0 || !strings.Contains(out, "acp.allow") || !strings.Contains(out, "travada por HANKO_ALERT_SILENT_FACTOR") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "settings", "get", "acp.allow")
	if code != 0 || strings.TrimSpace(out) != "true" {
		t.Fatalf("get: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "settings", "set", "acp.allow", "true")
	if code != 0 || !strings.Contains(out, "acp.allow = true") || !strings.Contains(f.last().Body, `{"value":true}`) {
		t.Fatalf("set: %d %q %+v", code, out, f.last())
	}
	// valor do tipo errado é erro de uso e chave desconhecida também (nenhuma chamada)
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "settings", "set", "acp.allow", "talvez"); code != 2 {
		t.Fatalf("tipo errado: %d", code)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "settings", "set", "database_url", "x"); code != 2 {
		t.Fatalf("chave fora do registro: %d", code)
	}
	if code, _, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "settings", "set", "alerts.silent_factor", "5"); code == 0 || !strings.Contains(errs, "travada") {
		t.Fatalf("chave travada: %d %q", code, errs)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "settings", "unset", "acp.allow")
	if code != 0 || f.last().Method != "DELETE" {
		t.Fatalf("unset: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "settings", "audit")
	if code != 0 || !strings.Contains(out, "acp.allow") {
		t.Fatalf("audit: %d %q", code, out)
	}
}

func TestBlobsMigrateRunsInProcess(t *testing.T) {
	var got BlobsMigrateArgs
	rt := Runtime{BlobsMigrate: func(_ context.Context, a BlobsMigrateArgs) (storage.MigrateReport, error) {
		got = a
		return storage.MigrateReport{Scanned: 3, Moved: 3, Bytes: 1234}, nil
	}}
	code, out, errs := exec(t, rt, gateEnv("http://x"), "", "blobs", "migrate", "--from", "postgres", "--to", "s3", "--keep-source", "--batch", "10")
	if code != 0 || got.From != "postgres" || got.To != "s3" || !got.KeepSource || got.Batch != 10 || !strings.Contains(out, "3 encontrado(s), 3 movidos (1234 bytes)") {
		t.Fatalf("%d %q %q %+v", code, out, errs, got)
	}
	// --from/--to são obrigatórios
	if code, _, _ := exec(t, rt, gateEnv("http://x"), "", "blobs", "migrate"); code != 2 {
		t.Fatalf("sem flags: %d", code)
	}
	// falhas dão código de saída diferente de zero e listam os artefatos
	rt.BlobsMigrate = func(context.Context, BlobsMigrateArgs) (storage.MigrateReport, error) {
		return storage.MigrateReport{Scanned: 2, Moved: 1, Failed: 1, Errors: []string{"id-1: gravar destino"}}, nil
	}
	code, _, errs = exec(t, rt, gateEnv("http://x"), "", "blobs", "migrate", "--from", "s3", "--to", "postgres")
	if code != 1 || !strings.Contains(errs, "id-1: gravar destino") {
		t.Fatalf("falha: %d %q", code, errs)
	}
	// sem runtime conectado
	if code, _, _ := exec(t, Runtime{}, gateEnv("http://x"), "", "blobs", "migrate", "--from", "s3", "--to", "postgres"); code != 1 {
		t.Fatalf("sem runtime: %d", code)
	}
}

func TestBlobsStatus(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/instance/blobs": jsonResp(200, api.BlobStatus{
		Active: "s3", Available: []string{"postgres", "s3"}, Usage: []api.BlobDriverUsage{{Driver: "postgres", Versions: 2, Bytes: 100}, {Driver: "s3", Versions: 5, Bytes: 900}}})})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "blobs", "status")
	if code != 0 || !strings.Contains(out, "Driver ativo: s3") || !strings.Contains(out, "postgres") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestSecretFieldCommands(t *testing.T) {
	visible := api.SecretField{ID: "f1", Name: "login", Reference: "${secret:gw#login}", Kind: "text", Sensitive: false, Version: 1, VisibleText: "bot@acme"}
	hidden := api.SecretField{ID: "f2", Name: "password", Reference: "${secret:gw#password}", Kind: "text", Sensitive: true, Version: 2, Revealed: true}
	generated := hidden
	generated.Value = "hk_gerado-xyz"
	const base = "/api/v1/companies/acme/secrets/gw/fields"
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET " + base:                                       jsonResp(200, list(visible, hidden)),
		"POST " + base:                                      jsonResp(201, generated),
		"POST " + base + "/password/rotate":                 jsonResp(200, generated),
		"PATCH " + base + "/password":                       jsonResp(200, hidden),
		"GET /api/v1/companies/acme/user-secrets/gw/fields": jsonResp(200, list(visible)),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "ls", "gw")
	if code != 0 || !strings.Contains(out, "bot@acme") || !strings.Contains(out, "password") || !strings.Contains(out, "secreto") {
		t.Fatalf("ls: %d %q", code, out)
	}
	if code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "--user", "field", "ls", "gw"); code != 0 || !strings.Contains(out, "login") {
		t.Fatalf("ls --user: %d %q", code, out)
	}
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "senha-do-usuario\n", "secret", "field", "add", "gw", "password", "--value-stdin", "--position", "2")
	if code != 0 || strings.Contains(out+errs, "senha-do-usuario") || !strings.Contains(out, "Campo criado: password") {
		t.Fatalf("add: %d %q %q", code, out, errs)
	}
	if b := f.last().Body; !strings.Contains(b, `"name":"password"`) || !strings.Contains(b, `"value":"senha-do-usuario"`) || !strings.Contains(b, `"position":2`) || strings.Contains(b, "sensitive") {
		t.Fatalf("corpo: %s", b)
	}
	// --generate mostra o valor gerado pelo servidor; --visible envia sensitive=false
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "add", "gw", "password", "--generate", "--visible")
	if code != 0 || !strings.Contains(out, "hk_gerado-xyz") || !strings.Contains(f.last().Body, `"sensitive":false`) {
		t.Fatalf("add --generate: %d %q %s", code, out, f.last().Body)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "rotate", "gw", "password", "--generate")
	if code != 0 || !strings.Contains(out, "rotacionado (versão 2)") || !strings.Contains(out, "hk_gerado-xyz") {
		t.Fatalf("rotate: %d %q", code, out)
	}
	if code, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "x", "secret", "field", "rotate", "gw", "password", "--value", "x", "--value-stdin"); code != 2 {
		t.Fatalf("value+stdin: %d", code)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "set", "gw", "password", "--visible", "--position", "3")
	if code != 0 || f.last().Method != "PATCH" || !strings.Contains(f.last().Body, `"sensitive":false`) || !strings.Contains(f.last().Body, `"position":3`) {
		t.Fatalf("set: %d %q %+v", code, out, f.last())
	}
	if code, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "set", "gw", "password"); code != 2 {
		t.Fatalf("set sem nada: %d", code)
	}
	if code, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "field", "set", "gw", "password", "--visible", "--secret"); code != 2 {
		t.Fatalf("set visible+secret: %d", code)
	}
}

func TestSecretCollectionCommands(t *testing.T) {
	def := api.SecretCollection{ID: "c1", Name: "default", Description: "Todas as personas", IsDefault: true, Secrets: 3}
	rot := 90
	infra := api.SecretCollection{ID: "c2", Name: "infra", Description: "servidores", RotateAfterDays: &rot, Secrets: 1}
	const base = "/api/v1/companies/acme/secret-collections"
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET " + base:                                     jsonResp(200, list(def, infra)),
		"POST " + base:                                    jsonResp(201, infra),
		"GET " + base + "/infra":                          jsonResp(200, infra),
		"PATCH " + base + "/infra":                        jsonResp(200, infra),
		"GET " + base + "/infra/secrets":                  jsonResp(200, list(secret1)),
		"PUT " + base + "/infra/secrets/gateway-token":    jsonResp(204, nil),
		"DELETE " + base + "/infra/secrets/gateway-token": jsonResp(204, nil),
		"DELETE " + base + "/default":                     jsonResp(422, map[string]any{"title": "Inválido", "status": 422, "detail": "a coleção padrão não se apaga"}),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "ls")
	if code != 0 || !strings.Contains(out, "infra") || !strings.Contains(out, "default") || !strings.Contains(out, "90") {
		t.Fatalf("ls: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "create", "infra", "--description", "servidores", "--rotate-days", "90")
	if code != 0 || !strings.Contains(out, "Coleção criada: infra") || !strings.Contains(f.last().Body, `"rotate_after_days":90`) {
		t.Fatalf("create: %d %q %s", code, out, f.last().Body)
	}
	if code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "show", "infra"); code != 0 || !strings.Contains(out, "servidores") {
		t.Fatalf("show: %d %q", code, out)
	}
	if code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "secrets", "infra"); code != 0 || !strings.Contains(out, "gateway-token") {
		t.Fatalf("secrets: %d %q", code, out)
	}
	// update parte da coleção atual: só o nome muda, descrição e prazo seguem
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "update", "infra", "--name", "infra2")
	b := f.last().Body
	if code != 0 || f.last().Method != "PATCH" || !strings.Contains(b, `"name":"infra2"`) || !strings.Contains(b, `"description":"servidores"`) || !strings.Contains(b, `"rotate_after_days":90`) {
		t.Fatalf("update: %d %q %s", code, out, b)
	}
	if code, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "update", "infra"); code != 2 {
		t.Fatalf("update sem flags: %d", code)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "add", "infra", "gateway-token")
	if code != 0 || f.last().Method != "PUT" || !strings.Contains(out, "gateway-token") {
		t.Fatalf("add: %d %q %+v", code, out, f.last())
	}
	code, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "remove", "infra", "gateway-token")
	if code != 0 || f.last().Method != "DELETE" {
		t.Fatalf("remove: %d %+v", code, f.last())
	}
	code, _, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "secret", "collection", "rm", "default")
	if code == 0 || !strings.Contains(errs, "a coleção padrão não se apaga") {
		t.Fatalf("rm padrão: %d %q", code, errs)
	}
}
