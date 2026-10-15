package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/api"
)

func TestPersonaHireConfigJSON(t *testing.T) {
	p := api.Persona{ID: "p1", Slug: "externo", Name: "Externo", Agent: "acp", Status: "active"}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST /api/v1/companies/acme/personas": jsonResp(201, p)})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, out, errOut := exec(t, Runtime{}, env, "", "persona", "hire", "Externo", "--agent", "acp",
		"--config-json", `{"command":"meu-agente","args":["--stdio"],"permission":"allow"}`, "--config", "permission=read_only")
	if code != 0 || !strings.Contains(out, "Persona contratada: Externo (externo)") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(f.last().Body), &body)
	want := map[string]any{"command": "meu-agente", "args": []any{"--stdio"}, "permission": "read_only"} // --config sobrepõe o JSON
	if diff := cmp.Diff(want, body["adapter_config"]); diff != "" {
		t.Fatal(diff)
	}
	n := len(f.hits)
	if code, _, errOut := exec(t, Runtime{}, env, "", "persona", "hire", "Ruim", "--agent", "acp", "--config-json", `[1]`); code == 0 || !strings.Contains(errOut, "--config-json") || len(f.hits) != n {
		t.Fatalf("config-json inválido: %d %q (requisições=%d→%d)", code, errOut, n, len(f.hits))
	}
}
