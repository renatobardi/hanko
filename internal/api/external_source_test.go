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
