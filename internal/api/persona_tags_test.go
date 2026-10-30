package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// O contrato das tags de persona (ADR-0087) responde 501 até a fatia 1 ligar o backend (#118).
func TestPersonaTagsContratoResponde501(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	const id = "00000000-0000-0000-0000-000000000001"
	const base = "/api/v1/companies/acme"
	for _, c := range []struct{ method, path, body string }{
		{"GET", base + "/persona-tags", ""},
		{"POST", base + "/persona-tags", `{"name":"Revisão"}`},
		{"GET", base + "/persona-tags/" + id, ""},
		{"PATCH", base + "/persona-tags/" + id, `{"color":"blue"}`},
		{"POST", base + "/persona-tags/" + id + "/archive", ""},
		{"POST", base + "/persona-tags/" + id + "/unarchive", ""},
		{"DELETE", base + "/persona-tags/" + id, ""},
		{"PUT", base + "/personas/cto/tags/" + id, ""},
		{"POST", base + "/personas/cto/tags", `{"name":"Infra"}`},
		{"DELETE", base + "/personas/cto/tags/" + id, ""},
		{"GET", base + "/org-chart/personas?match=all&level=1,2", ""},
	} {
		resp, body := do(t, c.method, ts.URL+c.path, c.body, nil)
		if resp.StatusCode != 501 {
			t.Errorf("%s %s: %d %s, quer 501", c.method, c.path, resp.StatusCode, body)
		}
	}

	resp, body := do(t, "GET", ts.URL+"/openapi.json", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("openapi.json: %d", resp.StatusCode)
	}
	if n := checkTagContractOps(t, body); n != 11 {
		t.Fatalf("operações do contrato das tags = %d, quer 11", n)
	}
}

// A cor fora da paleta é recusada já pelo esquema, antes do 501: a API nunca aceita hex livre (ADR-0087 item 2).
func TestPersonaTagsCorForaDaPaleta(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	resp, body := do(t, "POST", ts.URL+"/api/v1/companies/acme/persona-tags", `{"name":"Infra","color":"#ff0000"}`, nil)
	if resp.StatusCode != 422 {
		t.Fatalf("cor hex: %d %s, quer 422", resp.StatusCode, body)
	}
}

// checkTagContractOps confere cada operação do contrato das tags no OpenAPI: resposta 501 e a descrição com a fatia, e
// devolve quantas são.
func checkTagContractOps(t *testing.T, body string) int {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string                     `json:"operationId"`
			Description string                     `json:"description"`
			Responses   map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	n := 0
	for path, ops := range doc.Paths {
		for method, o := range ops {
			if !strings.HasPrefix(o.Description, "Contrato das tags de persona") {
				continue
			}
			n++
			if _, ok := o.Responses["501"]; !ok {
				t.Errorf("%s %s (%s): sem resposta 501 no OpenAPI", method, path, o.OperationID)
			}
			if !strings.Contains(o.Description, "fatia 1") {
				t.Errorf("%s: a descrição deve dizer qual fatia implementa", o.OperationID)
			}
		}
	}
	return n
}
