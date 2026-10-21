package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

// Gates estendidos pela CLI (ADR-0041), contra uma API de mentira.

var qgate = api.Gate{ID: gid, Kind: "question", Status: "pending", Title: "Onde publicar?", Revision: 1,
	Form:      &api.GateForm{Fields: []api.GateFormField{{Key: "env", Label: "Onde?", Type: "single", Required: true, Options: []api.GateFormOption{{Value: "stg"}, {Value: "prd"}}}}},
	CreatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}

func TestGateAnswerDismissReopen(t *testing.T) {
	base := "/api/v1/companies/acme/gates/" + gid
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/gates": jsonResp(200, list(qgate)),
		"GET " + base:                      jsonResp(200, qgate),
		"POST " + base + "/answer":         jsonResp(200, qgate),
		"POST " + base + "/dismiss":        jsonResp(200, qgate),
		"POST " + base + "/reopen":         jsonResp(200, qgate),
	})
	env := gateEnv(ts.URL)

	// show mostra o formulário
	code, out, _ := exec(t, Runtime{}, env, "", "gate", "show", "0198f2a0")
	if code != 0 || !strings.Contains(out, "env (single) * stg prd") {
		t.Fatalf("show: %d %q", code, out)
	}

	code, out, errs := exec(t, Runtime{}, env, "", "gate", "answer", "0198f2a0", "--set", "env=prd", "--set", "extras=a", "--set", "extras=b", "-n", "ok")
	if code != 0 || !strings.Contains(out, "respondido") {
		t.Fatalf("answer: %d %q %q", code, out, errs)
	}
	body := f.last().Body
	for _, w := range []string{`"env":["prd"]`, `"extras":["a","b"]`, `"note":"ok"`} {
		if !strings.Contains(body, w) {
			t.Errorf("corpo sem %s: %s", w, body)
		}
	}
	// --set sem "=" é erro de uso
	if code, _, errs := exec(t, Runtime{}, env, "", "gate", "answer", "0198f2a0", "--set", "env"); code != 2 || !strings.Contains(errs, "chave=valor") {
		t.Fatalf("--set inválido: %d %q", code, errs)
	}

	if code, out, _ = exec(t, Runtime{}, env, "", "gate", "dismiss", "0198f2a0", "-n", "depois"); code != 0 || !strings.Contains(out, "dispensado") || !strings.Contains(f.last().Body, `"note":"depois"`) {
		t.Fatalf("dismiss: %d %q %s", code, out, f.last().Body)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "gate", "reopen", "0198f2a0"); code != 0 || !strings.Contains(out, "reaberto") {
		t.Fatalf("reopen: %d %q", code, out)
	}
}

func TestGateResubmitAndHistory(t *testing.T) {
	base := "/api/v1/companies/acme/gates/" + gid
	g2 := qgate
	g2.Revision = 2
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/gates": jsonResp(200, list(g2)),
		"POST " + base + "/resubmit":       jsonResp(200, g2),
		"GET " + base + "/revisions": jsonResp(200, api.GateHistoryOut{Gate: g2, Added: 1,
			Revisions: []api.GateRevisionOut{{Revision: 1, Title: "t", Body: "a", DecisionNote: "falta o rollback", CreatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}},
			Diff:      "  1. a\n+ 2. rollback\n"}),
	})
	env := gateEnv(ts.URL)
	code, out, errs := exec(t, Runtime{}, env, "", "gate", "resubmit", "0198f2a0", "--body", "novo texto", "--artifact", "plano.md", "-n", "acrescentei")
	if code != 0 || !strings.Contains(out, "revisão 2") {
		t.Fatalf("resubmit: %d %q %q", code, out, errs)
	}
	for _, w := range []string{`"body":"novo texto"`, `"artifact":"plano.md"`, `"note":"acrescentei"`} {
		if !strings.Contains(f.last().Body, w) {
			t.Errorf("corpo sem %s: %s", w, f.last().Body)
		}
	}
	code, out, _ = exec(t, Runtime{}, env, "", "gate", "history", "0198f2a0")
	if code != 0 || !strings.Contains(out, "revisão vigente 2") || !strings.Contains(out, "falta o rollback") || !strings.Contains(out, "+ 2. rollback") {
		t.Fatalf("history: %d %q", code, out)
	}
}

func TestPlanPolicyCommands(t *testing.T) {
	yes := true
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/tasks/HNK-1/plan-policy": jsonResp(200, api.PlanPolicyOut{Persona: &yes, Required: true, Source: "persona", Phase: "plan"}),
		"PUT /api/v1/companies/acme/plan-policy":             jsonResp(204, nil),
	})
	env := gateEnv(ts.URL)
	code, out, _ := exec(t, Runtime{}, env, "", "plan-policy", "show", "HNK-1")
	if code != 0 || !strings.Contains(out, "exige") || !strings.Contains(out, "herda") || !strings.Contains(out, "plan") {
		t.Fatalf("show: %d %q", code, out)
	}
	if code, _, errs := exec(t, Runtime{}, env, "", "plan-policy", "set", "HNK-1"); code != 2 || !strings.Contains(errs, "--on, --off ou --inherit") {
		t.Fatalf("set sem modo: %d %q", code, errs)
	}
	if code, _, errs := exec(t, Runtime{}, env, "", "plan-policy", "set", "HNK-1", "--on", "--off"); code != 2 {
		t.Fatalf("set com dois modos: %d %q", code, errs)
	}
	if code, out, errs := exec(t, Runtime{}, env, "", "plan-policy", "set", "eng", "--scope", "persona", "--on"); code != 0 ||
		!strings.Contains(f.last().Body, `"scope":"persona"`) || !strings.Contains(f.last().Body, `"require":true`) {
		t.Fatalf("set --on: %d %q %q %s", code, out, errs, f.last().Body)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "plan-policy", "set", "HNK-1", "--inherit"); code != 0 || strings.Contains(f.last().Body, "require") {
		t.Fatalf("set --inherit: %s", f.last().Body)
	}
}

func TestBudgetIncidentCommands(t *testing.T) {
	inc := api.BudgetIncidentOut{ID: "0198f2a0-bbbb-7000-8000-000000000002", Scope: "persona", TargetName: "Eng", Month: "2026-10", Open: true, BudgetCents: 300, SpentUSD: 4}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/budget-incidents":                         jsonResp(200, list(inc)),
		"POST /api/v1/companies/acme/budget-incidents/" + inc.ID + "/resolve": jsonResp(200, inc),
	})
	env := gateEnv(ts.URL)
	code, out, _ := exec(t, Runtime{}, env, "", "budget", "incidents")
	if code != 0 || !strings.Contains(out, "0198f2a0") || !strings.Contains(out, "Eng") || !strings.Contains(out, "US$ 4.00 / 3.00") {
		t.Fatalf("incidents: %d %q", code, out)
	}
	if !strings.Contains(f.last().Query, "open=true") {
		t.Fatalf("query: %q", f.last().Query)
	}
	if code, _, errs := exec(t, Runtime{}, env, "", "budget", "resolve", "0198f2a0"); code != 2 || !strings.Contains(errs, "exatamente um") {
		t.Fatalf("sem ação: %d %q", code, errs)
	}
	if code, out, errs := exec(t, Runtime{}, env, "", "budget", "resolve", "0198f2a0", "--raise", "50", "-n", "mês pesado"); code != 0 ||
		!strings.Contains(f.last().Body, `"resolution":"ceiling_raised"`) || !strings.Contains(f.last().Body, `"budget_cents":5000`) {
		t.Fatalf("raise: %d %q %q %s", code, out, errs, f.last().Body)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "budget", "resolve", "0198f2a0", "--once"); code != 0 || !strings.Contains(f.last().Body, `"released_once"`) {
		t.Fatalf("once: %s", f.last().Body)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "budget", "resolve", "0198f2a0", "--keep"); code != 0 || !strings.Contains(f.last().Body, `"kept_paused"`) {
		t.Fatalf("keep: %s", f.last().Body)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "budget", "resolve", "0198f2a0", "--remove-ceiling"); code != 0 || !strings.Contains(f.last().Body, `"remove_ceiling":true`) {
		t.Fatalf("remove: %s", f.last().Body)
	}
	if code, _, errs := exec(t, Runtime{}, env, "", "budget", "resolve", "zzzz", "--keep"); code == 0 || !strings.Contains(errs, "não encontrado") {
		t.Fatalf("id desconhecido: %d %q", code, errs)
	}
}

func TestPersonaPrimarySystemAndHirePolicy(t *testing.T) {
	p := api.Persona{ID: "1", Name: "Eng", Slug: "eng", System: true}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/primary-persona":     jsonResp(200, p),
		"PUT /api/v1/companies/acme/primary-persona":     jsonResp(200, p),
		"PUT /api/v1/companies/acme/personas/eng/system": jsonResp(200, p),
		"GET /api/v1/companies/acme/policies":            jsonResp(200, api.CompanyPolicies{}),
		"PUT /api/v1/companies/acme/policies":            jsonResp(200, api.CompanyPolicies{RequireHireGate: true}),
		"POST /api/v1/companies/acme/gates":              jsonResp(201, qgate),
	})
	env := gateEnv(ts.URL)
	if code, out, _ := exec(t, Runtime{}, env, "", "persona", "primary"); code != 0 || !strings.Contains(out, "Eng (eng)") {
		t.Fatalf("primary show: %d %q", code, out)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "persona", "primary", "eng"); code != 0 || !strings.Contains(f.last().Body, `"persona":"eng"`) || !strings.Contains(out, "Persona principal") {
		t.Fatalf("primary set: %d %q %s", code, out, f.last().Body)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "persona", "primary", "--clear"); code != 0 || !strings.Contains(out, "limpa") || !strings.Contains(f.last().Body, `"persona":""`) {
		t.Fatalf("primary clear: %d %q %s", code, out, f.last().Body)
	}
	if code, _, errs := exec(t, Runtime{}, env, "", "persona", "primary", "eng", "--clear"); code != 2 {
		t.Fatalf("primary conflito: %d %q", code, errs)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "persona", "system", "eng"); code != 0 || !strings.Contains(f.last().Body, `"system":true`) || !strings.Contains(out, "persona de sistema") {
		t.Fatalf("system on: %d %q", code, out)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "persona", "system", "eng", "--off"); code != 0 || !strings.Contains(f.last().Body, `"system":false`) {
		t.Fatalf("system off: %s", f.last().Body)
	}

	if code, out, _ := exec(t, Runtime{}, env, "", "company", "policy"); code != 0 || !strings.Contains(out, "desligada") {
		t.Fatalf("policy show: %d %q", code, out)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "company", "policy", "hire-gate=on"); code != 0 || !strings.Contains(out, "ligada") || !strings.Contains(f.last().Body, `"require_hire_gate":true`) {
		t.Fatalf("policy on: %d %q %s", code, out, f.last().Body)
	}
	if code, _, errs := exec(t, Runtime{}, env, "", "company", "policy", "talvez"); code != 2 {
		t.Fatalf("policy inválida: %d %q", code, errs)
	}

	// persona hire --gate abre um gate hire com a ficha
	code, out, errs := exec(t, Runtime{}, env, "", "persona", "hire", "Yuki", "--agent", "fake", "--gate", "--instructions", "Analise.")
	if code != 0 || !strings.Contains(out, "Pedido de contratação aberto") {
		t.Fatalf("hire --gate: %d %q %q", code, out, errs)
	}
	for _, w := range []string{`"kind":"hire"`, `"name":"Yuki"`, `"agent":"fake"`, `"instructions":"Analise."`} {
		if !strings.Contains(f.last().Body, w) {
			t.Errorf("corpo sem %s: %s", w, f.last().Body)
		}
	}
}
