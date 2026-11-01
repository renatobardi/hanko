package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// Toda operação da rodada e da aprovação externas (ADR-0080 e 0081) está no OpenAPI e já tem backend: nenhuma responde 501.
func TestExternalRoundOperationsAreWired(t *testing.T) {
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
	for _, id := range []string{"list-external-rounds", "get-external-round", "get-round-step-content", "mark-round-action", "get-external-cycle",
		"list-external-approvals", "get-external-approval", "get-approval-script"} {
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
