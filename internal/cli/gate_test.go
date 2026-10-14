package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

const gid = "0198f2a0-aaaa-7000-8000-000000000001"

var gate1 = api.Gate{ID: gid, Kind: "approval", Status: "pending", Title: "Fazer deploy", Body: "Corpo do pedido", TaskKey: "HNK-1",
	TaskTitle: "Login", PersonaName: "Marina", RequestedBy: api.ActorRef{Type: "persona"}, CreatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}

func gateEnv(ts string) map[string]string {
	return map[string]string{"HANKO_URL": ts, "HANKO_COMPANY": "acme"}
}

func TestGateListEShow(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/gates":        jsonResp(200, list(gate1)),
		"GET /api/v1/companies/acme/gates/" + gid: jsonResp(200, gate1),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "gate", "list", "--status", "pending", "--kind", "approval")
	if code != 0 || !strings.Contains(out, "0198f2a0") || !strings.Contains(out, "Fazer deploy") || !strings.Contains(out, "Marina") {
		t.Fatalf("%d %q", code, out)
	}
	if q := f.last().Query; !strings.Contains(q, "status=pending") || !strings.Contains(q, "kind=approval") {
		t.Fatalf("query %q", q)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "gate", "show", "0198f2a0")
	if code != 0 || !strings.Contains(out, "Corpo do pedido") || !strings.Contains(out, "HNK-1 Login") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestGateDecisoes(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/gates":                     jsonResp(200, list(gate1)),
		"POST /api/v1/companies/acme/gates/" + gid + "/decide": jsonResp(200, gate1),
	})
	for _, tc := range []struct{ cmd, decision string }{{"approve", "approved"}, {"reject", "rejected"}, {"request-changes", "changes_requested"}} {
		code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "gate", tc.cmd, "0198f2a0", "-n", "ok")
		if code != 0 {
			t.Fatalf("%s: %d %q %q", tc.cmd, code, out, errs)
		}
		h := f.last()
		if h.Method != "POST" || !strings.Contains(h.Body, `"decision":"`+tc.decision+`"`) || !strings.Contains(h.Body, `"note":"ok"`) {
			t.Fatalf("%s: %+v", tc.cmd, h)
		}
	}
	// rejeitar sem motivo é erro de uso
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "gate", "reject", gid); code != 2 {
		t.Fatalf("código = %d", code)
	}
	// aprovar não exige nota
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "gate", "approve", gid); code != 0 {
		t.Fatalf("código = %d", code)
	}
}

func TestOrgArvore(t *testing.T) {
	child := api.OrgNode{Persona: api.Persona{Name: "Davi", Slug: "davi", Status: "idle"}, Depth: 1, OpenTasks: 2, PendingGates: 1}
	root := api.OrgNode{Persona: api.Persona{Name: "Marina", Slug: "marina", Status: "running"}, Children: []api.OrgNode{child}}
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/org-chart": jsonResp(200, api.OrgChart{Roots: []api.OrgNode{root}, Total: 2, Depth: 2}),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "org")
	if code != 0 || !strings.Contains(out, "Marina (marina) [running]") || !strings.Contains(out, "└─ Davi (davi) [idle] 2 tasks abertas · 1 gate(s) pendente(s)") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestPersonaUpdate(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/companies/acme/personas/davi": jsonResp(200, api.Persona{Name: "Davi", Slug: "davi", RequireReview: true}),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "persona", "update", "davi", "--reports-to", "marina", "--require-review", "true")
	if code != 0 || !strings.Contains(out, "Persona atualizada") {
		t.Fatalf("%d %q", code, out)
	}
	if b := f.last().Body; !strings.Contains(b, `"reports_to":"marina"`) || !strings.Contains(b, `"require_review":true`) {
		t.Fatalf("body %q", b)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "persona", "update", "davi"); code != 2 {
		t.Fatalf("código = %d", code)
	}
}
