//go:build integration

package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestAPIMemoriaProviderNone(t *testing.T) {
	e := newAPIEnv(t)
	e.svc.MemoryProvider = "none"
	var st api.MemoryStatusOut
	e.want(200, "GET", e.base()+"/memory", "", &st)
	if st.Available || st.Provider != "none" || st.Settings.Enabled {
		t.Fatalf("status: %+v", st)
	}
	cur := e.persona(e.co, "Curadora", nil)
	status, body := e.call("PATCH", e.base()+"/memory", `{"enabled":true,"curator":"`+cur.Slug+`"}`, nil)
	if status != 422 || !strings.Contains(string(body), "HANKO_MEMORY_PROVIDER=none") {
		t.Fatalf("ligar sem provedor: %d %s", status, body)
	}
	e.want(422, "POST", e.base()+"/memory/consolidate", `{}`, nil)
}

func TestAPIMemoriaFluxoCompleto(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	e.svc.MemoryProvider = "ai-memory"
	cur := e.persona(e.co, "Curadora", nil)
	ws, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}

	var st api.MemoryStatusOut
	e.want(200, "PATCH", e.base()+"/memory", `{"enabled":true,"interval_hours":12,"curator":"`+cur.Slug+`"}`, &st)
	if !st.Available || !st.Settings.Enabled || st.Settings.IntervalHours != 12 || st.Settings.CuratorSlug != cur.Slug || st.Settings.NextFireAt == nil || st.Workspaces != 1 {
		t.Fatalf("configurar: %+v", st)
	}
	e.want(422, "PATCH", e.base()+"/memory", `{"interval_hours":0}`, nil)
	e.want(422, "PATCH", e.base()+"/memory", `{"interval_hours":9999}`, nil)

	// persona (run token) não configura nem consolida
	other := e.persona(e.co, "Outra", nil)
	tok := e.personaToken(e.co, other)
	if status, _ := e.call("PATCH", e.base()+"/memory", `{"enabled":false}`, tok); status != 403 {
		t.Fatalf("run token deveria ser 403, veio %d", status)
	}

	var cs api.ListBody[api.MemoryConsolidationOut]
	e.want(200, "POST", e.base()+"/memory/consolidate", `{"workspace":"site"}`, &cs)
	if len(cs.Items) != 1 || cs.Items[0].WorkspaceSlug != "site" || cs.Items[0].Status != "open" || cs.Items[0].Origin != "manual" {
		t.Fatalf("consolidar: %+v", cs.Items)
	}
	e.want(409, "POST", e.base()+"/memory/consolidate", `{"workspace":"site"}`, nil)

	var hist api.ListBody[api.MemoryConsolidationOut]
	e.want(200, "GET", e.base()+"/memory/consolidations", "", &hist)
	if len(hist.Items) != 1 || hist.Items[0].TaskKey == "" {
		t.Fatalf("histórico: %+v", hist.Items)
	}

	// duas propostas da run de curadoria: uma aprovada, outra rejeitada
	task, err := e.svc.GetTask(ctx, e.co.ID, hist.Items[0].TaskKey)
	if err != nil {
		t.Fatal(err)
	}
	run, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: cur.ID, TaskID: task.ID, Trigger: domain.TriggerMemory})
	if err != nil {
		t.Fatal(err)
	}
	p1, _, err := e.svc.RecordMemoryProposal(ctx, e.co.ID, service.RecordProposalInput{RunID: run.ID, Tool: "memory_delete_page",
		Arguments: map[string]any{"path": "wiki/a"}, Current: "um\ndois\n"})
	if err != nil {
		t.Fatal(err)
	}
	p2, _, err := e.svc.RecordMemoryProposal(ctx, e.co.ID, service.RecordProposalInput{RunID: run.ID, Tool: "memory_merge_pages",
		Arguments: map[string]any{"paths": []any{"b", "c"}, "into": "d", "body": "fundido"}})
	if err != nil {
		t.Fatal(err)
	}

	var ps api.ListBody[api.MemoryProposalOut]
	e.want(200, "GET", e.base()+"/memory/proposals?status=pending", "", &ps)
	if len(ps.Items) != 2 {
		t.Fatalf("pendentes: %+v", ps.Items)
	}
	e.want(422, "GET", e.base()+"/memory/proposals?status=lixo", "", nil)
	var one api.MemoryProposalOut
	e.want(200, "GET", e.base()+"/memory/proposals/"+p1.ID.String(), "", &one)
	if one.Kind != "delete" || one.Status != "pending" || !strings.Contains(one.Diff, "-um") || one.GateID == "" || len(one.SHA256) != 64 {
		t.Fatalf("proposta: %+v", one)
	}
	e.want(404, "GET", e.base()+"/memory/proposals/"+domain.NewID().String(), "", nil)

	e.want(200, "POST", e.base()+"/memory/proposals/"+p1.ID.String()+"/approve", `{"note":"pode"}`, &one)
	if one.Status != "approved" || one.DecisionNote != "pode" || one.DecidedBy == nil {
		t.Fatalf("aprovada: %+v", one)
	}
	e.want(200, "POST", e.base()+"/memory/proposals/"+p2.ID.String()+"/reject", `{}`, &one)
	if one.Status != "rejected" {
		t.Fatalf("rejeitada: %+v", one)
	}
	e.want(409, "POST", e.base()+"/memory/proposals/"+p2.ID.String()+"/approve", `{}`, nil) // já decidida
	e.want(409, "POST", e.base()+"/memory/proposals/"+p1.ID.String()+"/retry", `{}`, nil)   // só falhas voltam

	// o gate também aparece no Inbox com o tipo memory
	var gates api.ListBody[api.Gate]
	e.want(200, "GET", e.base()+"/gates?kind=memory", "", &gates)
	if len(gates.Items) != 2 || gates.Items[0].Kind != "memory" {
		t.Fatalf("gates: %+v", gates.Items)
	}

	// permissões por workspace
	var g api.MemoryGrantOut
	e.want(200, "PUT", e.base()+"/memory/grants", `{"persona":"`+other.Slug+`","workspace":"`+ws.Slug+`","access":"read"}`, &g)
	if g.Access != "read" || g.WorkspaceSlug != "site" || g.PersonaName != "Outra" {
		t.Fatalf("conceder: %+v", g)
	}
	e.want(422, "PUT", e.base()+"/memory/grants", `{"persona":"`+other.Slug+`","workspace":"`+ws.Slug+`","access":"root"}`, nil)
	var gl api.ListBody[api.MemoryGrantOut]
	e.want(200, "GET", e.base()+"/memory/grants", "", &gl)
	if len(gl.Items) != 1 {
		t.Fatalf("lista: %+v", gl.Items)
	}
	e.want(204, "DELETE", e.base()+"/memory/grants/"+other.Slug+"/"+ws.Slug, "", nil)
	e.want(404, "DELETE", e.base()+"/memory/grants/"+other.Slug+"/"+ws.Slug, "", nil)

	// outra empresa não enxerga nada
	oc := e.company("Outra Empresa", "outra")
	var empty api.ListBody[api.MemoryProposalOut]
	e.want(200, "GET", "/api/v1/companies/"+oc.Slug+"/memory/proposals", "", &empty)
	if len(empty.Items) != 0 {
		t.Fatalf("vazamento entre empresas: %+v", empty.Items)
	}
	e.want(404, "GET", "/api/v1/companies/"+oc.Slug+"/memory/proposals/"+p1.ID.String(), "", nil)
}
