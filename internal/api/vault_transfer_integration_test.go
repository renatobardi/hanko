//go:build integration

package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
)

// Import de .env e export cifrado (fatia 7, #62) pela API: prévia sem gravar, import, export em uma instância e import
// em outra; nenhuma resposta leva valor em claro e a senha errada dá 422 sem gravar.
func TestAPIImportEnvEExportCifrado(t *testing.T) {
	const sentinela = "SENTINELA-api-portabilidade-5d3f"
	const senha = "senha-do-arquivo-1234"
	a, b := newAPIEnv(t), newAPIEnv(t)
	for i, e := range []*apiEnv{a, b} {
		key := []string{"0123456789abcdef0123456789abcdef-one", "fedcba9876543210fedcba9876543210-two"}[i]
		box, err := secretbox.New(key, "")
		if err != nil {
			t.Fatal(err)
		}
		e.svc.Integrations.Secrets = box
	}
	env, _ := json.Marshal(map[string]any{"mode": "items", "content": "# c\n\nexport TOKEN=\"" + sentinela + "\" # x\nVAZIA=\n"})

	var plan api.EnvImportPlan
	raw := a.want(200, "POST", a.base()+"/vault/import-env/preview", string(env), &plan)
	if len(plan.Items) != 1 || plan.Items[0].Secret != "TOKEN" || len(plan.Skipped) != 1 || !strings.HasPrefix(plan.Skipped[0], "linha 4:") {
		t.Fatalf("prévia = %s", raw)
	}
	if strings.Contains(string(raw), sentinela) {
		t.Fatalf("a prévia devolveu o valor: %s", raw)
	}
	var list struct {
		Items []api.Secret `json:"items"`
	}
	a.want(200, "GET", a.base()+"/secrets", "", &list)
	if len(list.Items) != 0 {
		t.Fatalf("a prévia gravou: %+v", list.Items)
	}

	var res api.VaultImportResult
	raw = a.want(200, "POST", a.base()+"/vault/import-env", string(env), &res)
	if res.Secrets != 1 || res.Fields != 1 || strings.Contains(string(raw), sentinela) {
		t.Fatalf("import = %s", raw)
	}
	a.want(422, "POST", a.base()+"/vault/import-env", `{"mode":"nada","content":"A=1"}`, nil)
	a.want(422, "POST", a.base()+"/vault/export", `{"password":"curta"}`, nil)

	var exp api.VaultExport
	raw = a.want(200, "POST", a.base()+"/vault/export", `{"password":"`+senha+`"}`, &exp)
	if exp.Secrets != 1 || len(exp.SHA256) != 64 || strings.Contains(string(raw), sentinela) {
		t.Fatalf("export = %s", raw)
	}

	wrong, _ := json.Marshal(map[string]any{"file_base64": exp.FileBase64, "password": "outra-senha-qualquer-9"})
	b.want(422, "POST", b.base()+"/vault/import", string(wrong), nil)
	b.want(422, "POST", b.base()+"/vault/import", `{"file_base64":"@@@","password":"`+senha+`"}`, nil)
	b.want(200, "GET", b.base()+"/secrets", "", &list)
	if len(list.Items) != 0 {
		t.Fatalf("o import recusado gravou: %+v", list.Items)
	}

	good, _ := json.Marshal(map[string]any{"file_base64": exp.FileBase64, "password": senha})
	b.want(200, "POST", b.base()+"/vault/import", string(good), &res)
	if res.Secrets != 1 || res.Fields != 1 {
		t.Fatalf("import do cofre = %+v", res)
	}
	b.want(200, "GET", b.base()+"/secrets", "", &list)
	if len(list.Items) != 1 || list.Items[0].Name != "TOKEN" {
		t.Fatalf("segredos no destino = %+v", list.Items)
	}
}
