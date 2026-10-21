//go:build integration

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/service"
)

// Gates estendidos pela API (ADR-0041): pergunta, plano, reenvio, contratação, persona principal e de sistema,
// política de plano e de empresa, incidentes de orçamento.

func TestAPIQuestionGate(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	p := e.persona(e.co, "Eng", nil)
	hdr := e.personaToken(e.co, p)

	form := `{"fields":[{"key":"env","label":"Onde publicar?","type":"single","required":true,"options":[{"value":"stg","label":"Staging"},{"value":"prd"}]},{"key":"obs","label":"Obs","type":"text"}]}`
	// um formulário inválido é recusado
	if status, out := e.call("POST", base+"/gates", `{"title":"q","kind":"question","form":{"fields":[{"key":"a","label":"x","type":"single"}]}}`, hdr); status != 422 {
		t.Fatalf("form sem opções: %d %s", status, out)
	}
	var g api.Gate
	status, out := e.call("POST", base+"/gates", `{"title":"Onde publicar?","kind":"question","form":`+form+`}`, hdr)
	if status != 201 {
		t.Fatalf("pergunta: %d %s", status, out)
	}
	e.want(200, "GET", base+"/gates?kind=question&status=pending", "", nil)
	e.want(200, "GET", base+"/gates", "", nil)
	var list struct {
		Items []api.Gate `json:"items"`
	}
	e.want(200, "GET", base+"/gates?kind=question", "", &list)
	if len(list.Items) != 1 {
		t.Fatalf("lista: %+v", list.Items)
	}
	g = list.Items[0]
	if g.Kind != "question" || g.Form == nil || len(g.Form.Fields) != 2 || g.Revision != 1 || g.Status != "pending" {
		t.Fatalf("gate: %+v", g)
	}

	// uma persona não responde, nem decide
	if status, _ := e.call("POST", base+"/gates/"+g.ID+"/answer", `{"answers":{"env":["stg"]}}`, hdr); status != 403 {
		t.Fatalf("persona responde: %d", status)
	}
	// resposta inválida
	if status, _ := e.call("POST", base+"/gates/"+g.ID+"/answer", `{"answers":{"env":["dev"]}}`, nil); status != 422 {
		t.Fatalf("opção desconhecida: %d", status)
	}
	var done api.Gate
	e.want(200, "POST", base+"/gates/"+g.ID+"/answer", `{"answers":{"env":["prd"],"obs":["depois das 22h"]},"note":"ok"}`, &done)
	if done.Status != "approved" || done.Answers["env"][0] != "prd" || done.Answers["obs"][0] != "depois das 22h" || done.DecidedBy == nil {
		t.Fatalf("respondido: %+v", done)
	}
	// responder de novo: conflito
	if status, _ := e.call("POST", base+"/gates/"+g.ID+"/answer", `{"answers":{"env":["stg"]}}`, nil); status != 409 {
		t.Fatalf("segunda resposta: %d", status)
	}
}

func TestAPIDismissReopenAndHistory(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	p := e.persona(e.co, "Eng", nil)
	hdr := e.personaToken(e.co, p)

	var q api.Gate
	if status, out := e.call("POST", base+"/gates", `{"title":"q","kind":"question","form":{"fields":[{"key":"a","label":"A?","type":"text"}]}}`, hdr); status != 201 {
		t.Fatalf("%d %s", status, out)
	}
	var list struct {
		Items []api.Gate `json:"items"`
	}
	e.want(200, "GET", base+"/gates?kind=question", "", &list)
	q = list.Items[0]
	e.want(200, "POST", base+"/gates/"+q.ID+"/dismiss", `{"note":"depois"}`, &q)
	if q.Status != "dismissed" {
		t.Fatalf("dismissed: %+v", q)
	}
	e.want(200, "POST", base+"/gates/"+q.ID+"/reopen", `{}`, &q)
	if q.Status != "pending" || q.DecidedAt != nil {
		t.Fatalf("reaberta: %+v", q)
	}

	// plano: mudanças pedidas, reenvio pelo token da persona, histórico com diff
	var pg api.Gate
	if status, out := e.call("POST", base+"/gates", `{"title":"Plano","kind":"plan","body":"1. a\n2. b"}`, hdr); status != 201 {
		t.Fatalf("plano: %d %s", status, out)
	}
	e.want(200, "GET", base+"/gates?kind=plan", "", &list)
	pg = list.Items[0]
	e.want(200, "POST", base+"/gates/"+pg.ID+"/decide", `{"decision":"changes_requested","note":"falta o rollback"}`, nil)
	if status, out := e.call("POST", base+"/gates/"+pg.ID+"/resubmit", `{"body":"1. a\n2. b\n3. rollback","note":"acrescentei"}`, hdr); status != 200 {
		t.Fatalf("reenvio: %d %s", status, out)
	}
	var h api.GateHistoryOut
	e.want(200, "GET", base+"/gates/"+pg.ID+"/revisions", "", &h)
	if h.Gate.Revision != 2 || len(h.Revisions) != 1 || h.Added != 1 || h.Removed != 0 || !strings.Contains(h.Diff, "+ 3. rollback") ||
		h.Revisions[0].DecisionNote != "falta o rollback" {
		t.Fatalf("histórico: %+v", h)
	}
	// reenviar um gate que não está com mudanças pedidas: conflito
	if status, _ := e.call("POST", base+"/gates/"+pg.ID+"/resubmit", `{"body":"x"}`, hdr); status != 409 {
		t.Fatalf("reenvio fora de hora: %d", status)
	}
}

func TestAPIHireByGateAndPolicy(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	boss := e.persona(e.co, "Chefe", nil)
	hdr := e.personaToken(e.co, boss)

	var pol api.CompanyPolicies
	e.want(200, "GET", base+"/policies", "", &pol)
	if pol.RequireHireGate {
		t.Fatalf("padrão desligado: %+v", pol)
	}
	e.want(200, "PUT", base+"/policies", `{"require_hire_gate":true}`, &pol)
	if !pol.RequireHireGate {
		t.Fatal("deveria ligar")
	}
	if status, out := e.call("POST", base+"/personas", `{"name":"Direto","agent":"fake"}`, nil); status != 409 {
		t.Fatalf("contratar direto com a política ligada: %d %s", status, out)
	}

	var g api.Gate
	status, out := e.call("POST", base+"/gates", `{"title":"Contratar Yuki","kind":"hire","hire":{"name":"Yuki","agent":"fake","instructions":"Analise."}}`, hdr)
	if status != 201 {
		t.Fatalf("hire: %d %s", status, out)
	}
	var list struct {
		Items []api.Gate `json:"items"`
	}
	e.want(200, "GET", base+"/gates?kind=hire", "", &list)
	g = list.Items[0]
	if g.Hire == nil || g.Hire.Name != "Yuki" {
		t.Fatalf("ficha: %+v", g)
	}
	e.want(200, "POST", base+"/gates/"+g.ID+"/decide", `{"decision":"approved"}`, nil)
	var yuki api.Persona
	e.want(200, "GET", base+"/personas/yuki", "", &yuki)
	if yuki.ReportsTo != boss.ID.String() || yuki.Instructions != "Analise." {
		t.Fatalf("persona contratada: %+v", yuki)
	}
}

func TestAPIPrimaryPersonaSystemAndPlanPolicy(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	p := e.persona(e.co, "Eng", nil)
	tk := e.task(e.co, "t", &p, nil)

	// modo trusted: não há pessoa autenticada para ter uma principal
	if status, _ := e.call("PUT", base+"/primary-persona", `{"persona":"eng"}`, nil); status != 422 {
		t.Fatalf("principal em trusted: %d", status)
	}
	if status, _ := e.call("GET", base+"/primary-persona", "", nil); status != 404 {
		t.Fatalf("sem principal: %d", status)
	}

	var sys api.Persona
	e.want(200, "PUT", base+"/personas/eng/system", `{"system":true}`, &sys)
	if !sys.System {
		t.Fatalf("system: %+v", sys)
	}
	if status, _ := e.call("PATCH", base+"/personas/eng", `{"status":"off"}`, nil); status != 409 {
		t.Fatalf("desligar persona de sistema: %d", status)
	}

	var pp api.PlanPolicyOut
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/plan-policy", "", &pp)
	if pp.Required || pp.Phase != "" {
		t.Fatalf("sem política: %+v", pp)
	}
	e.want(204, "PUT", base+"/plan-policy", `{"scope":"persona","target":"eng","require":true}`, nil)
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/plan-policy", "", &pp)
	if !pp.Required || pp.Source != "persona" || pp.Phase != "plan" || pp.Persona == nil || !*pp.Persona {
		t.Fatalf("persona exige: %+v", pp)
	}
	e.want(204, "PUT", base+"/plan-policy", fmt.Sprintf(`{"scope":"task","target":%q,"require":false}`, tk.Key), nil)
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/plan-policy", "", &pp)
	if pp.Required || pp.Source != "task" {
		t.Fatalf("task vence: %+v", pp)
	}
	e.want(204, "PUT", base+"/plan-policy", fmt.Sprintf(`{"scope":"task","target":%q}`, tk.Key), nil) // limpa
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/plan-policy", "", &pp)
	if !pp.Required || pp.Source != "persona" {
		t.Fatalf("herda de novo: %+v", pp)
	}
	if status, _ := e.call("PUT", base+"/plan-policy", `{"scope":"galaxia","target":"x"}`, nil); status < 400 || status >= 500 {
		t.Fatalf("escopo inválido: %d", status)
	}
}

func TestAPIBudgetIncidents(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	p := e.persona(e.co, "Eng", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(300)) })
	e.spend(p, e.now(), usdRes(4.0))

	var list struct {
		Items []api.BudgetIncidentOut `json:"items"`
	}
	e.want(200, "GET", base+"/budget-incidents?open=true", "", &list)
	if len(list.Items) != 1 || list.Items[0].Scope != "persona" || !list.Items[0].Open || list.Items[0].BudgetCents != 300 {
		t.Fatalf("incidentes: %+v", list.Items)
	}
	inc := list.Items[0]
	if status, _ := e.call("POST", base+"/budget-incidents/"+inc.ID+"/resolve", `{"resolution":"ceiling_raised"}`, nil); status != 422 {
		t.Fatalf("sem teto novo: %d", status)
	}
	if status, _ := e.call("POST", base+"/budget-incidents/"+inc.ID+"/resolve", `{"resolution":"auto"}`, nil); status != 422 {
		t.Fatalf("auto não é escolha: %d", status)
	}
	var res api.BudgetIncidentOut
	e.want(200, "POST", base+"/budget-incidents/"+inc.ID+"/resolve", `{"resolution":"ceiling_raised","budget_cents":5000,"note":"mês pesado"}`, &res)
	if res.Open || res.Resolution != "ceiling_raised" || res.Note != "mês pesado" {
		t.Fatalf("resolvido: %+v", res)
	}
	var pr api.Persona
	e.want(200, "GET", base+"/personas/eng", "", &pr)
	if pr.BudgetMonthlyCents == nil || *pr.BudgetMonthlyCents != 5000 {
		t.Fatalf("teto: %+v", pr.BudgetMonthlyCents)
	}
	e.want(200, "GET", base+"/budget-incidents?open=true", "", &list)
	if len(list.Items) != 0 {
		t.Fatalf("nenhum aberto: %+v", list.Items)
	}
	if status, _ := e.call("POST", base+"/budget-incidents/"+inc.ID+"/resolve", `{"resolution":"kept_paused"}`, nil); status != 409 {
		t.Fatalf("já resolvido: %d", status)
	}
	// persona com run token não resolve
	hdr := e.personaToken(e.co, p)
	if status, _ := e.call("POST", base+"/budget-incidents/"+inc.ID+"/resolve", `{"resolution":"kept_paused"}`, hdr); status != 403 {
		t.Fatalf("persona resolve: %d", status)
	}
}
