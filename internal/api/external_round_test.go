package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// O contrato da rodada e da aprovação externas (ADR-0080 e 0081) responde 501 até cada fatia ligar o backend (#124, grupo 3).
func TestExternalRoundContratoResponde501(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	const id = "00000000-0000-0000-0000-000000000001"
	const base = "/api/v1/companies/acme"
	for _, c := range []struct{ method, path, body string }{
		{"GET", base + "/external-rounds", ""},
		{"GET", base + "/external-rounds/" + id, ""},
		{"GET", base + "/external-rounds/" + id + "/steps/" + id + "/content", ""},
		{"POST", base + "/external-rounds/" + id + "/steps/" + id + "/actions/ab1/marks", `{"state":"done"}`},
		{"GET", base + "/external-cycles/" + id, ""},
		{"GET", base + "/external-approvals", ""},
		{"GET", base + "/external-approvals/" + id, ""},
		{"GET", base + "/external-approvals/" + id + "/script", ""},
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
	n := checkRoundContractOps(t, body)
	if n != 8 {
		t.Fatalf("operações do contrato da rodada e da aprovação = %d, quer 8", n)
	}
}

// checkRoundContractOps confere cada operação do grupo 3 no OpenAPI: resposta 501 e a descrição com a fatia, e devolve quantas são.
func checkRoundContractOps(t *testing.T, body string) int {
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
			if !strings.HasPrefix(o.Description, "Contrato do grupo 3 da v0.8") {
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
	return n
}
