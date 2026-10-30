package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// O contrato da fonte externa (ADR-0075 a 0079) está no OpenAPI e responde 501 até cada fatia ligar o backend.
func TestExternalSourceContratoResponde501(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	const id = "00000000-0000-0000-0000-000000000001"
	const base = "/api/v1/companies/acme"
	for _, c := range []struct{ method, path, body string }{
		{"GET", base + "/external-agents", ""},
		{"GET", base + "/external-agents/" + id, ""},
		{"PATCH", base + "/external-agents/" + id, `{"name":"x"}`},
		{"POST", base + "/external-agents/" + id + "/merge", `{"into":"` + id + `"}`},
		{"GET", base + "/external-hosts", ""},
		{"GET", base + "/external-hosts/" + id, ""},
		{"PATCH", base + "/external-hosts/" + id, `{"always_on":true}`},
		{"POST", base + "/external-hosts/" + id + "/merge", `{"into":"` + id + `"}`},
		{"GET", base + "/repo-pairings", ""},
		{"PATCH", base + "/repo-pairings/" + id, `{"workspace_id":"` + id + `"}`},
		{"GET", base + "/external-sessions", ""},
		{"GET", base + "/external-sessions/" + id, ""},
		{"GET", base + "/external-conversations", ""},
		{"GET", base + "/external-conversations/" + id, ""},
		{"GET", base + "/external-conversations/" + id + "/class-inputs", ""},
		{"POST", base + "/external-conversations/" + id + "/class", `{"class":"qa"}`},
		{"GET", base + "/external-conversations/" + id + "/content", ""},
		{"GET", base + "/external-usage", ""},
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
			if !strings.HasPrefix(o.Description, "Contrato da fonte externa") {
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
	if n != 18 {
		t.Fatalf("operações do contrato da fonte externa = %d, quer 18 (as 3 de chave de ingestão já estão ligadas)", n)
	}
}

// O contrato do custo elevado e dos alertas externos (ADR-0082 e 0083) responde 501 até a fatia 2 (#126).
func TestExternalCostContratoResponde501(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	const base = "/api/v1/companies/acme"
	const run = "00000000-0000-0000-0000-000000000001"
	n := 0
	for _, c := range []struct{ method, path, body string }{
		{"GET", base + "/subscriptions/claude-max/monthly-history", ""},
		{"POST", base + "/subscriptions/claude-max/monthly-history", `{"monthly_usd":250,"starts_on":"2026-06-01"}`},
		{"GET", base + "/price-checks", ""},
		{"GET", base + "/price-checks/" + run, ""},
		{"GET", base + "/alert-settings", ""},
		{"PUT", base + "/alert-settings/due.queue", `{"value":0}`},
		{"DELETE", base + "/alert-settings/due.queue", ""},
	} {
		n++
		resp, body := do(t, c.method, ts.URL+c.path, c.body, nil)
		if resp.StatusCode != 501 {
			t.Errorf("%s %s: %d %s, quer 501", c.method, c.path, resp.StatusCode, body)
		}
	}
	_, spec := do(t, "GET", ts.URL+"/openapi.json", "", nil)
	if got := strings.Count(spec, "Contrato do custo elevado e dos alertas externos"); got != n {
		t.Fatalf("operações do contrato do custo = %d, quer %d", got, n)
	}
}
