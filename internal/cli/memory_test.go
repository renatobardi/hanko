package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

const pid = "0198f2a0-bbbb-7000-8000-000000000001"

var memProposal = api.MemoryProposalOut{ID: pid, WorkspaceSlug: "site", Kind: "delete", Status: "pending", Tool: "memory_delete_page",
	Targets: []string{"wiki/velha", "wiki/outra"}, Diff: "-linha velha\n", SHA256: strings.Repeat("a", 64),
	CreatedAt: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}

func memEnv(url string) map[string]string {
	return map[string]string{"HANKO_URL": url, "HANKO_COMPANY": "acme"}
}

func TestMemoryStatusEConfig(t *testing.T) {
	st := api.MemoryStatusOut{Provider: "ai-memory", Available: true, Workspaces: 2, PendingProposals: 1,
		Settings: api.MemorySettingsOut{Enabled: true, IntervalHours: 12, CuratorSlug: "curadora", CuratorName: "Curadora"}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/memory":   jsonResp(200, st),
		"PATCH /api/v1/companies/acme/memory": jsonResp(200, st),
	})
	code, out, _ := exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "status")
	if code != 0 || !strings.Contains(out, "ai-memory") || !strings.Contains(out, "Curadora (curadora)") || !strings.Contains(out, "12h") {
		t.Fatalf("%d %q", code, out)
	}
	code, _, _ = exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "config", "--enable", "--interval", "6", "--curator", "curadora")
	if h := f.last(); code != 0 || h.Method != "PATCH" || !strings.Contains(h.Body, `"enabled":true`) ||
		!strings.Contains(h.Body, `"interval_hours":6`) || !strings.Contains(h.Body, `"curator":"curadora"`) {
		t.Fatalf("%d %+v", code, h)
	}
	if code, _, _ = exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "config", "--enable", "--disable"); code == 0 {
		t.Fatal("--enable e --disable juntos deveria falhar")
	}
	// provedor none: avisa por quê
	st.Available, st.Provider = false, "none"
	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/memory": jsonResp(200, st)})
	_, out, _ = exec(t, Runtime{}, memEnv(ts2.URL), "", "memory", "status")
	if !strings.Contains(out, "HANKO_MEMORY_PROVIDER=none") {
		t.Fatalf("faltou o aviso: %q", out)
	}
}

func TestMemoryConsolidateEHistory(t *testing.T) {
	cost := 0.0123
	c := api.MemoryConsolidationOut{ID: "x", WorkspaceSlug: "site", Origin: "manual", Status: "done", TaskKey: "HNK-9", Proposals: 2, CostUSD: &cost,
		CreatedAt: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/memory/consolidate":   jsonResp(200, list(c)),
		"GET /api/v1/companies/acme/memory/consolidations": jsonResp(200, list(c)),
	})
	code, out, _ := exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "consolidate", "site")
	if code != 0 || !strings.Contains(out, "workspace site") || !strings.Contains(f.last().Body, `"workspace":"site"`) {
		t.Fatalf("%d %q %+v", code, out, f.last())
	}
	code, out, _ = exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "history")
	if code != 0 || !strings.Contains(out, "HNK-9") || !strings.Contains(out, "0.0123") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestMemoryProposalsDecisoes(t *testing.T) {
	approved := memProposal
	approved.Status = "approved"
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/memory/proposals":                      jsonResp(200, list(memProposal)),
		"GET /api/v1/companies/acme/memory/proposals/" + pid:               jsonResp(200, memProposal),
		"POST /api/v1/companies/acme/memory/proposals/" + pid + "/approve": jsonResp(200, approved),
		"POST /api/v1/companies/acme/memory/proposals/" + pid + "/reject":  jsonResp(200, approved),
		"POST /api/v1/companies/acme/memory/proposals/" + pid + "/retry":   jsonResp(200, approved),
	})
	code, out, _ := exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "proposals", "--status", "pending")
	if code != 0 || !strings.Contains(out, "0198f2a0") || !strings.Contains(out, "wiki/velha (+1)") || !strings.Contains(f.last().Query, "status=pending") {
		t.Fatalf("%d %q %+v", code, out, f.last())
	}
	// mostrar uma, por prefixo do id, com o diff
	code, out, _ = exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "proposals", "0198f2a0")
	if code != 0 || !strings.Contains(out, "-linha velha") || !strings.Contains(out, strings.Repeat("a", 64)) {
		t.Fatalf("%d %q", code, out)
	}
	for _, cmd := range []string{"approve", "reject", "retry"} {
		args := []string{"memory", cmd, "0198f2a0"}
		if cmd != "retry" {
			args = append(args, "-m", "ok")
		}
		code, out, errs := exec(t, Runtime{}, memEnv(ts.URL), "", args...)
		if code != 0 || !strings.Contains(out, "approved") {
			t.Fatalf("%s: %d %q %q", cmd, code, out, errs)
		}
		h := f.last()
		if h.Method != "POST" || !strings.HasSuffix(h.Path, "/"+cmd) {
			t.Fatalf("%s: %+v", cmd, h)
		}
		if cmd != "retry" && !strings.Contains(h.Body, `"note":"ok"`) {
			t.Fatalf("%s: nota ausente %+v", cmd, h)
		}
	}
	if code, _, _ := exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "approve", "ffff"); code == 0 {
		t.Fatal("prefixo desconhecido deveria falhar")
	}
}

func TestMemoryGrants(t *testing.T) {
	g := api.MemoryGrantOut{PersonaID: "p", PersonaName: "Outra", WorkspaceID: "w", WorkspaceSlug: "site", Access: "read"}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/memory/grants":               jsonResp(200, list(g)),
		"PUT /api/v1/companies/acme/memory/grants":               jsonResp(200, g),
		"DELETE /api/v1/companies/acme/memory/grants/outra/site": jsonResp(204, nil),
	})
	code, out, _ := exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "grants")
	if code != 0 || !strings.Contains(out, "Outra") || !strings.Contains(out, "read") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "grant", "outra", "site", "--access", "write")
	if h := f.last(); code != 0 || h.Method != "PUT" || !strings.Contains(h.Body, `"access":"write"`) || !strings.Contains(out, "Outra") {
		t.Fatalf("%d %q %+v", code, out, h)
	}
	if code, _, _ = exec(t, Runtime{}, memEnv(ts.URL), "", "memory", "revoke", "outra", "site"); code != 0 {
		t.Fatalf("revogar: %d", code)
	}
}
