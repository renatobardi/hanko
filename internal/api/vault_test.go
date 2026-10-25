package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// O contrato do cofre (ADR-0068/0069/0070) está no OpenAPI e responde 501 até cada fatia ligar o backend.
func TestVaultContratoResponde501(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/s/AAAAAAAAAAAAAAAAAAAAAA"},
		{"GET", "/api/v1/companies/acme/secrets/gh/usage"},
		{"GET", "/api/v1/companies/acme/vault/health"},
	} {
		resp, body := do(t, c.method, ts.URL+c.path, "", nil)
		if resp.StatusCode != 501 {
			t.Errorf("%s %s: %d %s, quer 501", c.method, c.path, resp.StatusCode, body)
		}
	}

	resp, body := do(t, "GET", ts.URL+"/openapi.json", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("openapi.json: %d", resp.StatusCode)
	}
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
			if !strings.HasPrefix(o.Description, "Contrato do cofre") {
				continue
			}
			n++
			if _, ok := o.Responses["501"]; !ok {
				t.Errorf("%s %s (%s): sem resposta 501 no OpenAPI", method, path, o.OperationID)
			}
			if !strings.Contains(o.Description, "fatia ") {
				t.Errorf("%s: a descrição deve dizer qual fatia implementa", o.OperationID)
			}
		}
	}
	// Com todas as fatias do contrato ligadas (1, 2, 4, 5, 6 e 7), nenhuma operação do cofre responde 501: o openapi
	// não tem mais descrição "Contrato do cofre" pendente. Se uma operação nova entrar em contrato, este teste acusa.
	if n != 0 {
		t.Fatalf("operações do contrato do cofre ainda em 501 = %d, quer 0 (todas as fatias ligadas)", n)
	}
}
