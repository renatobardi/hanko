package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func (f *fakeSvc) RequestGate(_ context.Context, a domain.Actor, cid domain.ID, in service.RequestGateInput) (service.GateView, error) {
	f.lastActor, f.lastGateIn = a, in
	if in.Title == "boom" {
		return service.GateView{}, domain.ErrConflict
	}
	g := service.GateView{Gate: domain.Gate{ID: domain.NewID(), CompanyID: cid, Kind: in.Kind, Status: domain.GatePending, Title: in.Title,
		Body: in.Body, TaskID: in.TaskID, RunID: in.RunID, RequestedBy: a, EventID: in.EventID, CreatedAt: time.Now()}}
	if g.Kind == "" {
		g.Kind = domain.GateApproval
	}
	if a.Type == domain.ActorPersona {
		g.PersonaID = a.ID
	}
	f.gates = append(f.gates, g)
	return g, nil
}

func (f *fakeSvc) DecideGate(_ context.Context, a domain.Actor, cid, gid domain.ID, in service.DecideGateInput) (service.GateView, error) {
	f.lastActor, f.lastDecide = a, in
	if a.Type != domain.ActorHuman {
		return service.GateView{}, domain.ErrNotAllowed
	}
	for i, g := range f.gates {
		if g.ID != gid {
			continue
		}
		if g.Status != domain.GatePending {
			return service.GateView{}, domain.ErrConflict
		}
		now := time.Now()
		g.Status, g.DecisionNote, g.DecidedAt, g.DecidedBy = in.Decision, in.Note, &now, &a
		f.gates[i] = g
		return g, nil
	}
	return service.GateView{}, domain.ErrNotFound
}

func (f *fakeSvc) ListGates(_ context.Context, cid domain.ID, fl service.GateFilter) ([]service.GateView, error) {
	f.lastGateF = fl
	return f.gates, nil
}

func (f *fakeSvc) GetGate(_ context.Context, cid, gid domain.ID) (service.GateView, error) {
	for _, g := range f.gates {
		if g.ID == gid {
			return g, nil
		}
	}
	return service.GateView{}, domain.ErrNotFound
}

func (f *fakeSvc) OrgChart(_ context.Context, cid domain.ID) (service.OrgChart, error) {
	root := &service.OrgNode{Persona: domain.Persona{ID: domain.NewID(), CompanyID: cid, Slug: "ceo", Name: "Ceo", Status: domain.PersonaIdle}, OpenTasks: 2}
	kid := &service.OrgNode{Persona: domain.Persona{ID: domain.NewID(), CompanyID: cid, Slug: "eng", Name: "Eng", Status: domain.PersonaIdle, ReportsTo: &root.Persona.ID}, Depth: 1, PendingGates: 1}
	root.Children = []*service.OrgNode{kid}
	return service.OrgChart{Roots: []*service.OrgNode{root}, Total: 2, Depth: 2}, nil
}

func (f *fakeSvc) UpdatePersona(_ context.Context, a domain.Actor, cid, pid domain.ID, in service.UpdatePersonaInput) (domain.Persona, error) {
	f.lastActor, f.lastPersonaUp = a, in
	f.cf().lastPersona = in
	for _, p := range f.personas {
		if p.ID == pid {
			if in.BudgetMonthlyCents != nil {
				p.BudgetMonthlyCents = in.BudgetMonthlyCents
			}
			if in.RequireReview != nil {
				p.RequireReview = *in.RequireReview
			}
			if in.ReportsTo != nil {
				p.ReportsTo = in.ReportsTo
			}
			return p, nil
		}
	}
	return domain.Persona{}, domain.ErrNotFound
}

func gateFixtureAPI(t *testing.T) (*fakeSvc, domain.Company, string) {
	t.Helper()
	f := &fakeSvc{}
	c := seedCompany(f)
	f.tasks = append(f.tasks, service.TaskView{Task: domain.Task{ID: domain.NewID(), CompanyID: c.ID, Key: "HNK-1", Number: 1}})
	return f, c, ""
}

func TestGatesRequestAndList(t *testing.T) {
	f, c, _ := gateFixtureAPI(t)
	ts, _ := newTestServer(t, f, nil)

	resp, body := do(t, "POST", ts.URL+"/api/v1/companies/acme/gates",
		`{"title":"Deploy","body":"irreversível","task":"HNK-1","event_id":"e1"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("request: %d %s", resp.StatusCode, body)
	}
	var g Gate
	if err := json.Unmarshal([]byte(body), &g); err != nil || g.Status != "pending" || g.Kind != "approval" || g.TaskID != f.tasks[0].ID.String() || g.RequestedBy.Type != "human" {
		t.Fatalf("gate: %+v %v", g, err)
	}
	if f.lastGateIn.TaskID == nil || *f.lastGateIn.TaskID != f.tasks[0].ID || f.lastGateIn.EventID != "e1" {
		t.Fatalf("input: %+v", f.lastGateIn)
	}
	if resp, _ = do(t, "POST", ts.URL+"/api/v1/companies/acme/gates", `{"title":"boom"}`, nil); resp.StatusCode != 409 {
		t.Fatalf("conflict: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "POST", ts.URL+"/api/v1/companies/acme/gates", `{"title":""}`, nil); resp.StatusCode != 422 {
		t.Fatalf("empty title: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "POST", ts.URL+"/api/v1/companies/acme/gates", `{"title":"x","task":"HNK-99"}`, nil); resp.StatusCode != 404 {
		t.Fatalf("unknown task: %d", resp.StatusCode)
	}

	resp, body = do(t, "GET", ts.URL+"/api/v1/companies/acme/gates?status=pending,approved&kind=approval&task=HNK-1", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	var l ListBody[Gate]
	if err := json.Unmarshal([]byte(body), &l); err != nil || len(l.Items) != 1 {
		t.Fatalf("list body: %s", body)
	}
	if len(f.lastGateF.Statuses) != 2 || f.lastGateF.Statuses[1] != domain.GateApproved || len(f.lastGateF.Kinds) != 1 ||
		f.lastGateF.TaskID == nil || *f.lastGateF.TaskID != f.tasks[0].ID {
		t.Fatalf("filter: %+v", f.lastGateF)
	}
	for _, q := range []string{"status=nope", "kind=nope"} {
		if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/acme/gates?"+q, "", nil); resp.StatusCode != 422 {
			t.Fatalf("%s: %d", q, resp.StatusCode)
		}
	}
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/acme/gates/"+g.ID, "", nil); resp.StatusCode != 200 {
		t.Fatalf("get: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/acme/gates/"+domain.NewID().String(), "", nil); resp.StatusCode != 404 {
		t.Fatalf("get unknown: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/acme/gates/not-a-uuid", "", nil); resp.StatusCode == 200 {
		t.Fatal("bad id accepted")
	}
	_ = c
}

func TestGateDecide(t *testing.T) {
	f, _, _ := gateFixtureAPI(t)
	tokens := &auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	ts, _ := newTestServer(t, f, func(d *Deps) { d.Tokens = tokens })
	_, body := do(t, "POST", ts.URL+"/api/v1/companies/acme/gates", `{"title":"Deploy","task":"HNK-1"}`, nil)
	var g Gate
	_ = json.Unmarshal([]byte(body), &g)
	url := ts.URL + "/api/v1/companies/acme/gates/" + g.ID + "/decide"

	// a persona (run token) cannot decide, not even its own gate
	pid, rid := domain.NewID(), domain.NewID()
	f.runs = map[domain.ID]service.RunView{rid: {Run: domain.Run{ID: rid, CompanyID: f.companies[0].ID, Status: domain.RunRunning}}}
	tok, _ := tokens.Issue(f.companies[0].ID, pid, rid, f.tasks[0].ID)
	if resp, b := do(t, "POST", url, `{"decision":"approved"}`, map[string]string{"Authorization": "Bearer " + tok}); resp.StatusCode != 403 {
		t.Fatalf("persona decide: %d %s", resp.StatusCode, b)
	}
	if f.lastDecide.Decision != "" {
		t.Fatalf("service must not be reached by a persona: %+v", f.lastDecide)
	}
	// invalid decision
	if resp, _ := do(t, "POST", url, `{"decision":"maybe"}`, nil); resp.StatusCode != 422 {
		t.Fatalf("bad decision: %d", resp.StatusCode)
	}
	resp, b := do(t, "POST", url, `{"decision":"changes_requested","note":"inclua rollback"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("decide: %d %s", resp.StatusCode, b)
	}
	var d Gate
	if err := json.Unmarshal([]byte(b), &d); err != nil || d.Status != "changes_requested" || d.DecisionNote != "inclua rollback" || d.DecidedBy == nil || d.DecidedBy.Type != "human" || d.DecidedAt == nil {
		t.Fatalf("decided: %+v %v", d, err)
	}
	if f.lastActor.Type != domain.ActorHuman {
		t.Fatalf("actor = %+v", f.lastActor)
	}
	// already decided
	if resp, _ := do(t, "POST", url, `{"decision":"approved"}`, nil); resp.StatusCode != 409 {
		t.Fatalf("second decision: %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", ts.URL+"/api/v1/companies/acme/gates/"+domain.NewID().String()+"/decide", `{"decision":"approved"}`, nil); resp.StatusCode != 404 {
		t.Fatalf("unknown gate: %d", resp.StatusCode)
	}
}

func TestGateRequestByPersonaTakesTaskAndRunFromToken(t *testing.T) {
	f, c, _ := gateFixtureAPI(t)
	tokens := &auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	ts, _ := newTestServer(t, f, func(d *Deps) { d.Tokens = tokens })
	pid, rid := domain.NewID(), domain.NewID()
	f.runs = map[domain.ID]service.RunView{rid: {Run: domain.Run{ID: rid, CompanyID: c.ID, Status: domain.RunRunning}}}
	tok, _ := tokens.Issue(c.ID, pid, rid, f.tasks[0].ID)

	resp, b := do(t, "POST", ts.URL+"/api/v1/companies/acme/gates", `{"title":"Merge na main"}`, map[string]string{"Authorization": "Bearer " + tok})
	if resp.StatusCode != 201 {
		t.Fatalf("persona request: %d %s", resp.StatusCode, b)
	}
	if f.lastActor.Type != domain.ActorPersona || f.lastGateIn.TaskID == nil || *f.lastGateIn.TaskID != f.tasks[0].ID ||
		f.lastGateIn.RunID == nil || *f.lastGateIn.RunID != rid {
		t.Fatalf("actor=%+v in=%+v", f.lastActor, f.lastGateIn)
	}
}

func TestOrgChartAndPersonaPatch(t *testing.T) {
	f, c, _ := gateFixtureAPI(t)
	boss := domain.Persona{ID: domain.NewID(), CompanyID: c.ID, Slug: "boss", Name: "Boss"}
	eng := domain.Persona{ID: domain.NewID(), CompanyID: c.ID, Slug: "eng", Name: "Eng"}
	f.personas = []domain.Persona{boss, eng}
	ts, _ := newTestServer(t, f, nil)

	resp, b := do(t, "GET", ts.URL+"/api/v1/companies/acme/org-chart", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("org-chart: %d %s", resp.StatusCode, b)
	}
	var oc OrgChart
	if err := json.Unmarshal([]byte(b), &oc); err != nil || oc.Total != 2 || oc.Depth != 2 || len(oc.Roots) != 1 ||
		oc.Roots[0].Persona.Slug != "ceo" || len(oc.Roots[0].Children) != 1 || oc.Roots[0].Children[0].PendingGates != 1 ||
		oc.Roots[0].Children[0].Persona.ReportsTo != oc.Roots[0].Persona.ID {
		t.Fatalf("org-chart body: %+v %v\n%s", oc, err, b)
	}

	resp, b = do(t, "PATCH", ts.URL+"/api/v1/companies/acme/personas/eng", `{"reports_to":"boss","require_review":true}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	var p Persona
	_ = json.Unmarshal([]byte(b), &p)
	if !p.RequireReview || p.ReportsTo != boss.ID.String() {
		t.Fatalf("persona: %+v", p)
	}
	if f.lastPersonaUp.ReportsTo == nil || *f.lastPersonaUp.ReportsTo != boss.ID || f.lastPersonaUp.RequireReview == nil || !*f.lastPersonaUp.RequireReview {
		t.Fatalf("update input: %+v", f.lastPersonaUp)
	}
	if resp, _ = do(t, "PATCH", ts.URL+"/api/v1/companies/acme/personas/eng", `{"reports_to":"ninguem"}`, nil); resp.StatusCode != 404 {
		t.Fatalf("unknown superior: %d", resp.StatusCode)
	}
}
