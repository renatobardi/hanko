//go:build integration

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// Persona com harness ACP: o adapter_config (comando do agente) é validado na contratação.
func TestAPIPersonaACP(t *testing.T) {
	e := newAPIEnv(t)
	ps := e.base() + "/personas"

	var p api.Persona
	e.want(201, "POST", ps, `{"name":"Externo","agent":"acp","adapter_config":{"command":"meu-agente","args":["--stdio"],"permission":"read_only"}}`, &p)
	if p.Agent != "acp" || p.AdapterConfig["command"] != "meu-agente" || p.AdapterConfig["permission"] != "read_only" {
		t.Fatalf("persona acp: %+v", p)
	}
	var got api.Persona
	e.want(200, "GET", ps+"/"+p.Slug, "", &got)
	if got.Agent != "acp" || got.AdapterConfig["command"] != "meu-agente" {
		t.Fatalf("get: %+v", got)
	}

	// erros amigáveis: 422 apontando adapter_config e dizendo o que falta
	for name, c := range map[string]struct{ body, msg string }{
		"sem config":         {`{"name":"A1","agent":"acp"}`, `"command" is required`},
		"sem command":        {`{"name":"A2","agent":"acp","adapter_config":{"args":["x"]}}`, `"command" is required`},
		"permissão inválida": {`{"name":"A3","agent":"acp","adapter_config":{"command":"a","permission":"tudo"}}`, `"permission" must be allow, reject or read_only`},
		"chave desconhecida": {`{"name":"A4","agent":"acp","adapter_config":{"command":"a","permision":"allow"}}`, `unknown field "permision"`},
	} {
		status, b := e.call("POST", ps, c.body, nil)
		if status != http.StatusUnprocessableEntity || !strings.Contains(string(b), "adapter_config") || !strings.Contains(string(b), c.msg) {
			t.Errorf("%s: status %d: %s", name, status, b)
		}
	}

	// acp não é harness alternativo de revisão (não há adapter_config para ele)
	if status, b := e.call("POST", ps, `{"name":"A5","agent":"claude","reviewer_agent":"acp"}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("reviewer_agent acp na contratação: status %d: %s", status, b)
	}
	if status, b := e.call("PATCH", ps+"/externo", `{"reviewer_agent":"acp"}`, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("reviewer_agent acp na edição: status %d: %s", status, b)
	}
	// mas uma persona ACP pode ser a persona revisora
	var ada api.Persona
	e.want(201, "POST", ps, `{"name":"Ada","agent":"claude","reviewer":"externo"}`, &ada)
	if ada.ReviewerPersonaID != p.ID {
		t.Fatalf("revisor acp: %+v", ada)
	}
}
