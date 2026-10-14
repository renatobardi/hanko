package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Métodos de custos e fluxos do fake (o resto vem do fake principal).
type costFlowFake struct {
	lastCostF   service.CostFilter
	budget      *int64
	flows       []service.FlowView
	lastCreate  service.CreateFlowInput
	lastUpdate  service.UpdateFlowInput
	lastPersona service.UpdatePersonaInput
	ran         []domain.ID
}

func (f *fakeSvc) cf() *costFlowFake { return &f.m4 }

func (f *fakeSvc) CostSummary(_ context.Context, cid domain.ID, fl service.CostFilter) (service.CostSummary, error) {
	f.cf().lastCostF = fl
	return service.CostSummary{From: fl.From, To: fl.To, Timezone: "America/Sao_Paulo", GroupBy: fl.GroupBy, TotalUSD: 3.5, EstimatedUSD: 1,
		ReportedUSD: 2.5, Runs: 2, Rows: []service.CostRow{{Key: "2026-03-10", Label: "2026-03-10", CostUSD: 3.5, Runs: 2}}}, nil
}

func (f *fakeSvc) CompanyBudget(context.Context, domain.ID) (service.BudgetStatus, error) {
	return service.BudgetStatus{BudgetCents: f.cf().budget, SpentUSD: 3.5, Percent: 70, State: service.BudgetOK}, nil
}

func (f *fakeSvc) SetCompanyBudget(_ context.Context, a domain.Actor, cid domain.ID, cents *int64) (domain.Company, error) {
	f.lastActor = a
	f.cf().budget = cents
	c := f.companies[0]
	c.BudgetMonthlyCents = cents
	return c, nil
}

func (f *fakeSvc) UpdatePersona(_ context.Context, a domain.Actor, cid, pid domain.ID, in service.UpdatePersonaInput) (domain.Persona, error) {
	f.cf().lastPersona = in
	p := f.personas[0]
	p.BudgetMonthlyCents = in.BudgetMonthlyCents
	return p, nil
}

func (f *fakeSvc) CreateFlow(_ context.Context, a domain.Actor, cid domain.ID, in service.CreateFlowInput) (service.FlowView, error) {
	f.lastActor = a
	f.cf().lastCreate = in
	if in.Cron == "dup" {
		return service.FlowView{}, domain.ErrConflict
	}
	fv := service.FlowView{Flow: domain.Flow{ID: domain.NewID(), CompanyID: cid, Slug: domain.Slugify(in.Name), Name: in.Name,
		Cron: in.Cron, Timezone: "America/Sao_Paulo", Enabled: true, PersonaID: in.PersonaID, Priority: domain.PriorityMedium}, PersonaName: "Ada"}
	f.cf().flows = append(f.cf().flows, fv)
	return fv, nil
}

func (f *fakeSvc) UpdateFlow(_ context.Context, a domain.Actor, cid, fid domain.ID, in service.UpdateFlowInput) (service.FlowView, error) {
	f.cf().lastUpdate = in
	for i, fl := range f.cf().flows {
		if fl.ID == fid {
			if in.Enabled != nil {
				fl.Enabled = *in.Enabled
			}
			f.cf().flows[i] = fl
			return fl, nil
		}
	}
	return service.FlowView{}, domain.ErrNotFound
}

func (f *fakeSvc) ListFlows(context.Context, domain.ID) ([]service.FlowView, error) {
	return f.cf().flows, nil
}

func (f *fakeSvc) GetFlow(_ context.Context, cid domain.ID, ref string) (service.FlowView, error) {
	for _, fl := range f.cf().flows {
		if fl.Slug == ref || fl.ID.String() == ref {
			return fl, nil
		}
	}
	return service.FlowView{}, domain.ErrNotFound
}

func (f *fakeSvc) ListFlowFires(_ context.Context, cid, fid domain.ID, limit int) ([]service.FlowFireView, error) {
	tid := domain.NewID()
	return []service.FlowFireView{{FlowFire: domain.FlowFire{ID: domain.NewID(), FlowID: fid, TaskID: &tid,
		ScheduledFor: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)}, TaskKey: "HNK-1", TaskTitle: "Resumo", TaskStatus: "todo"}}, nil
}

func (f *fakeSvc) RunFlowNow(_ context.Context, a domain.Actor, cid, fid domain.ID) (service.FlowFireResult, error) {
	f.lastActor = a
	f.cf().ran = append(f.cf().ran, fid)
	tid := domain.NewID()
	tv := service.TaskView{Task: domain.Task{ID: tid, CompanyID: cid, Key: "HNK-9", Title: "Resumo", Status: domain.TaskTodo, Priority: domain.PriorityMedium}}
	return service.FlowFireResult{Fire: domain.FlowFire{ID: domain.NewID(), FlowID: fid, TaskID: &tid, ScheduledFor: time.Now()}, Task: &tv,
		Duplicate: len(f.cf().ran) > 1}, nil
}

func TestCustosAgregados(t *testing.T) {
	f := &fakeSvc{}
	seedCompany(f)
	f.companies[0].Timezone = "America/Sao_Paulo"
	ts, _ := newTestServer(t, f, nil)
	base := ts.URL + "/api/v1/companies/acme/costs"

	resp, body := do(t, "GET", base+"?from=2026-03-01&to=2026-03-31&group_by=persona", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("costs: %d %s", resp.StatusCode, body)
	}
	var out CostSummary
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.GroupBy != "persona" || out.TotalUSD != 3.5 || out.EstimatedUSD != 1 || len(out.Rows) != 1 || out.Budget.State != "ok" {
		t.Fatalf("body: %s", body)
	}
	// datas valem no fuso da empresa (UTC-3); "to" é inclusivo
	got := f.cf().lastCostF
	if !got.From.Equal(time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC)) || !got.To.Equal(time.Date(2026, 4, 1, 3, 0, 0, 0, time.UTC)) || got.GroupBy != "persona" {
		t.Fatalf("filtro: %+v", got)
	}
	// RFC 3339 passa exato
	do(t, "GET", base+"?from=2026-03-01T00:00:00Z&to=2026-03-02T00:00:00Z", "", nil)
	got = f.cf().lastCostF
	if !got.From.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) || !got.To.Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("rfc3339: %+v", got)
	}
	// padrão: sem datas, group_by day
	do(t, "GET", base, "", nil)
	if got = f.cf().lastCostF; !got.From.IsZero() || !got.To.IsZero() || got.GroupBy != "day" {
		t.Fatalf("padrão: %+v", got)
	}
	for _, q := range []string{"?from=ontem", "?group_by=clima", "?to=31/03/2026"} {
		if resp, _ := do(t, "GET", base+q, "", nil); resp.StatusCode != 422 {
			t.Errorf("%s: %d, quer 422", q, resp.StatusCode)
		}
	}
	if resp, _ := do(t, "GET", ts.URL+"/api/v1/companies/nada/costs", "", nil); resp.StatusCode != 404 {
		t.Errorf("empresa inexistente: %d", resp.StatusCode)
	}
}

func TestOrcamentoEmpresaEPersona(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	f.personas = []domain.Persona{{ID: domain.NewID(), CompanyID: c.ID, Slug: "ada", Name: "Ada"}}
	ts, _ := newTestServer(t, f, nil)
	base := ts.URL + "/api/v1/companies/acme"

	resp, body := do(t, "PATCH", base, `{"budget_monthly_cents":5000}`, nil)
	var co Company
	_ = json.Unmarshal([]byte(body), &co)
	if resp.StatusCode != 200 || co.BudgetMonthlyCents == nil || *co.BudgetMonthlyCents != 5000 {
		t.Fatalf("set: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "PATCH", base, `{"clear_budget":true}`, nil)
	co = Company{}
	_ = json.Unmarshal([]byte(body), &co)
	if resp.StatusCode != 200 || co.BudgetMonthlyCents != nil || f.cf().budget != nil {
		t.Fatalf("clear: %d %s", resp.StatusCode, body)
	}
	if resp, _ = do(t, "PATCH", base, `{"budget_monthly_cents":-1}`, nil); resp.StatusCode != 422 {
		t.Fatalf("negativo: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "PATCH", base, `{"budget_monthly_cents":100,"clear_budget":true}`, nil); resp.StatusCode != 422 {
		t.Fatalf("set+clear: %d", resp.StatusCode)
	}

	resp, body = do(t, "PATCH", base+"/personas/ada", `{"budget_monthly_cents":1200}`, nil)
	var p Persona
	_ = json.Unmarshal([]byte(body), &p)
	if resp.StatusCode != 200 || p.BudgetMonthlyCents == nil || *p.BudgetMonthlyCents != 1200 ||
		f.cf().lastPersona.BudgetMonthlyCents == nil || *f.cf().lastPersona.BudgetMonthlyCents != 1200 {
		t.Fatalf("persona: %d %s", resp.StatusCode, body)
	}
	do(t, "PATCH", base+"/personas/ada", `{"clear_budget":true}`, nil)
	if !f.cf().lastPersona.ClearBudget {
		t.Fatal("clear_budget não chegou ao service")
	}
}

func TestFluxosCRUDERodarAgora(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	ada := domain.Persona{ID: domain.NewID(), CompanyID: c.ID, Slug: "ada", Name: "Ada"}
	f.personas = []domain.Persona{ada}
	ts, _ := newTestServer(t, f, nil)
	base := ts.URL + "/api/v1/companies/acme/flows"

	resp, body := do(t, "POST", base, `{"name":"Resumo diário","cron":"0 9 * * 1-5","persona":"ada","task_title":"Resumo de {{data}}","priority":"high"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var fl Flow
	_ = json.Unmarshal([]byte(body), &fl)
	if fl.Slug != "resumo-diario" || !fl.Enabled || fl.PersonaID != ada.ID.String() ||
		f.cf().lastCreate.PersonaID != ada.ID || f.cf().lastCreate.Priority != domain.PriorityHigh || f.cf().lastCreate.TaskTitle != "Resumo de {{data}}" {
		t.Fatalf("flow: %s / %+v", body, f.cf().lastCreate)
	}
	if resp, _ = do(t, "POST", base, `{"name":"x","cron":"dup","persona":"ada"}`, nil); resp.StatusCode != 409 {
		t.Fatalf("duplicado: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "POST", base, `{"name":"x","cron":"0 9 * * *","persona":"fantasma"}`, nil); resp.StatusCode != 404 {
		t.Fatalf("persona inexistente: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "POST", base, `{"name":"x","cron":"0 9 * * *","persona":"ada","priority":"urgente"}`, nil); resp.StatusCode != 422 {
		t.Fatalf("prioridade: %d", resp.StatusCode)
	}

	_, body = do(t, "GET", base, "", nil)
	var l ListBody[Flow]
	_ = json.Unmarshal([]byte(body), &l)
	if len(l.Items) != 1 {
		t.Fatalf("list: %s", body)
	}
	if resp, _ = do(t, "GET", base+"/resumo-diario", "", nil); resp.StatusCode != 200 {
		t.Fatalf("get slug: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "GET", base+"/nao-existe", "", nil); resp.StatusCode != 404 {
		t.Fatalf("get 404: %d", resp.StatusCode)
	}

	resp, body = do(t, "PATCH", base+"/resumo-diario", `{"enabled":false,"cron":"30 8 * * *"}`, nil)
	_ = json.Unmarshal([]byte(body), &fl)
	if resp.StatusCode != 200 || fl.Enabled || f.cf().lastUpdate.Cron == nil || *f.cf().lastUpdate.Cron != "30 8 * * *" {
		t.Fatalf("patch: %d %s", resp.StatusCode, body)
	}

	resp, body = do(t, "POST", base+"/resumo-diario/run", "", nil)
	var run FlowRun
	_ = json.Unmarshal([]byte(body), &run)
	if resp.StatusCode != 200 || run.Task == nil || run.Task.Key != "HNK-9" || run.Duplicate || run.Fire.TaskKey != "HNK-9" {
		t.Fatalf("run: %d %s", resp.StatusCode, body)
	}
	if f.lastActor.Type != domain.ActorHuman {
		t.Fatalf("ator = %+v", f.lastActor)
	}
	_, body = do(t, "POST", base+"/resumo-diario/run", "", nil)
	if !strings.Contains(body, `"duplicate":true`) {
		t.Fatalf("segundo clique: %s", body)
	}

	resp, body = do(t, "GET", base+"/resumo-diario/fires", "", nil)
	var fires ListBody[FlowFire]
	_ = json.Unmarshal([]byte(body), &fires)
	if resp.StatusCode != 200 || len(fires.Items) != 1 || fires.Items[0].TaskKey != "HNK-1" {
		t.Fatalf("fires: %d %s", resp.StatusCode, body)
	}
}

func TestOrcamentoEFluxosVetadosParaPersona(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	f.personas = []domain.Persona{{ID: domain.NewID(), CompanyID: c.ID, Slug: "ada", Name: "Ada"}}
	fv := service.FlowView{Flow: domain.Flow{ID: domain.NewID(), CompanyID: c.ID, Slug: "f", Name: "F"}}
	f.cf().flows = []service.FlowView{fv}
	tokens := &auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	ts, _ := newTestServer(t, f, func(d *Deps) { d.Tokens = tokens })
	rid := domain.NewID()
	f.runs = map[domain.ID]service.RunView{rid: {Run: domain.Run{ID: rid, CompanyID: c.ID, Status: domain.RunRunning}}}
	tok, _ := tokens.Issue(c.ID, f.personas[0].ID, rid, domain.NewID())
	h := map[string]string{"Authorization": "Bearer " + tok}
	base := ts.URL + "/api/v1/companies/acme"

	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", "", `{"budget_monthly_cents":1}`},
		{"PATCH", "/personas/ada", `{"budget_monthly_cents":1}`},
		{"POST", "/flows", `{"name":"x","cron":"0 9 * * *","persona":"ada"}`},
		{"PATCH", "/flows/f", `{"enabled":false}`},
		{"POST", "/flows/f/run", ``},
	} {
		if resp, body := do(t, tc.method, base+tc.path, tc.body, h); resp.StatusCode != 403 {
			t.Errorf("%s %s: %d (quer 403) %s", tc.method, tc.path, resp.StatusCode, body)
		}
	}
	if len(f.cf().ran) != 0 || f.cf().budget != nil {
		t.Fatal("escrita vazou apesar do 403")
	}
	for _, p := range []string{"/flows", "/flows/f", "/costs"} {
		if resp, body := do(t, "GET", base+p, "", h); resp.StatusCode != 200 {
			t.Errorf("GET %s: %d %s", p, resp.StatusCode, body)
		}
	}
}
