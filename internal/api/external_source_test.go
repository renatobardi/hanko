package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// Toda operação da fonte externa (ADR-0075 a 0079) está no OpenAPI e já tem backend: nenhuma responde 501.
func TestExternalSourceOperationsAreWired(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	_, body := do(t, "GET", ts.URL+"/openapi.json", "", nil)
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Description string `json:"description"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, id := range []string{"list-external-agents", "get-external-agent", "update-external-agent", "merge-external-agent",
		"list-external-hosts", "get-external-host", "update-external-host", "merge-external-host", "list-repo-pairings", "update-repo-pairing",
		"list-external-sessions", "get-external-session", "list-external-conversations", "get-external-conversation",
		"list-conversation-class-inputs", "classify-external-conversation", "get-external-conversation-content", "get-external-usage",
		"list-ingest-keys", "create-ingest-key", "revoke-ingest-key"} {
		want[id] = true
	}
	for _, ops := range doc.Paths {
		for _, o := range ops {
			if want[o.OperationID] {
				delete(want, o.OperationID)
				if strings.Contains(o.Description, "Responde 501") {
					t.Errorf("%s ainda responde 501", o.OperationID)
				}
			}
		}
	}
	for id := range want {
		t.Errorf("a operação %s não está no OpenAPI", id)
	}
}

// O contrato do custo elevado e dos alertas externos (ADR-0082 e 0083) responde 501 até a fatia 2 (#126).
func TestExternalCostContratoResponde501(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	const base = "/api/v1/companies/acme"
	const run = "00000000-0000-0000-0000-000000000001"
	n := 0
	for _, c := range []struct{ method, path, body string }{
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
