package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// O contrato do cofre (ADR-0068/0069/0070) está no OpenAPI e responde 501 até cada fatia ligar o backend.
func TestVaultContratoResponde501(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	const uuid = "00000000-0000-0000-0000-000000000001"
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/companies/acme/user-secrets/gh/fields/password/history"},
		{"DELETE", "/api/v1/companies/acme/secret-collections/infra/grants/" + uuid},
		{"GET", "/api/v1/companies/acme/sends/" + uuid},
		{"GET", "/api/v1/s/AAAAAAAAAAAAAAAAAAAAAA"},
		{"GET", "/api/v1/companies/acme/me/delegations"},
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
	// A fatia 1 (#56) liga 17 das 54 operações do contrato e a fatia 2 (#57) liga 6 (reautenticação e passkeys);
	// as restantes (envios, delegações, saúde, portabilidade) seguem com a descrição "Contrato do cofre" e respondem 501.
	if n < 31 {
		t.Fatalf("operações do contrato do cofre ainda em 501 = %d, quer pelo menos 31 (54 menos 17 da fatia 1 e 6 da fatia 2)", n)
	}
}
