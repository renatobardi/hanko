package cli

import (
	"encoding/base64"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// Política de tools na CLI (ADR-0053): tool-policy, integration tools --test, gate approve --always e o hook do claude.

func TestToolPolicyListSetDelete(t *testing.T) {
	pol := api.ToolPolicy{ID: "0198f2a0-cccc-7000-8000-000000000001", Scope: "persona", Persona: "Ana", Target: "tool", Integration: "docs", Tool: "apagar", Mode: "ask", Origin: "manual"}
	always := api.ToolPolicy{ID: "0198f2a0-dddd-7000-8000-000000000002", Scope: "persona", Persona: "Ana", Target: "native", Tool: "Bash", Mode: "allow", Origin: "always"}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/tool-policies":                 jsonResp(200, list(pol, always)),
		"PUT /api/v1/companies/acme/tool-policies":                 jsonResp(200, pol),
		"DELETE /api/v1/companies/acme/tool-policies/" + pol.ID:    jsonResp(204, nil),
		"DELETE /api/v1/companies/acme/tool-policies/" + always.ID: jsonResp(204, nil),
	})
	env := gateEnv(ts.URL)
	code, out, _ := exec(t, Runtime{}, env, "", "tool-policy", "list", "--persona", "ana")
	if code != 0 || !strings.Contains(out, "docs/apagar") || !strings.Contains(out, "perguntar antes") || !strings.Contains(out, "nativa Bash") ||
		!strings.Contains(out, "sempre permitir") || !strings.Contains(out, "Ana") {
		t.Fatalf("list: %d %q", code, out)
	}
	if q := f.last().Query; q != "limit=500&persona=ana" {
		t.Fatalf("query %q", q)
	}
	for _, tc := range []struct {
		args []string
		body []string
	}{
		{[]string{"set", "ask", "--persona", "ana", "--integration", "docs", "--tool", "apagar"}, []string{`"target":"tool"`, `"integration":"docs"`, `"tool":"apagar"`, `"mode":"ask"`, `"persona":"ana"`}},
		{[]string{"set", "off", "--integration", "docs"}, []string{`"target":"integration"`, `"mode":"off"`}},
		{[]string{"set", "allow", "--native", "Bash"}, []string{`"target":"native"`, `"tool":"Bash"`}},
		{[]string{"set", "ask"}, []string{`"target":"all"`}},
	} {
		code, out, errs := exec(t, Runtime{}, env, "", append([]string{"tool-policy"}, tc.args...)...)
		if code != 0 || !strings.Contains(out, "Política gravada") {
			t.Fatalf("%v: %d %q %q", tc.args, code, out, errs)
		}
		for _, w := range tc.body {
			if !strings.Contains(f.last().Body, w) {
				t.Errorf("%v: corpo %q sem %s", tc.args, f.last().Body, w)
			}
		}
	}
	// combinações inválidas são erro de uso
	for _, args := range [][]string{{"set", "ask", "--tool", "x"}, {"set", "ask", "--native", "Bash", "--tool", "x"}, {"set"}} {
		if code, _, _ := exec(t, Runtime{}, env, "", append([]string{"tool-policy"}, args...)...); code != 2 {
			t.Errorf("%v: código %d", args, code)
		}
	}
	// apagar por prefixo do id
	code, out, _ = exec(t, Runtime{}, env, "", "tool-policy", "delete", "0198f2a0-dddd")
	if code != 0 || !strings.Contains(out, "apagada") || f.last().Path != "/api/v1/companies/acme/tool-policies/"+always.ID {
		t.Fatalf("delete: %d %q %+v", code, out, f.last())
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "tool-policy", "delete", "ffff"); code != 1 {
		t.Fatalf("prefixo desconhecido: %d", code)
	}
}

func TestIntegrationToolsTest(t *testing.T) {
	res := api.ToolTestResult{Persona: "Ana", Integration: "docs", Tool: "apagar", Status: "active", Risk: "destructive",
		Verdict: api.ToolVerdict{Allowed: true, Mode: "ask", Source: "policy:persona"}, Decision: "ask", Message: "simulação: a chamada pediria aprovação"}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/integrations/docs/tools/apagar/test": jsonResp(200, res),
	})
	env := gateEnv(ts.URL)
	code, out, errs := exec(t, Runtime{}, env, "", "integration", "tools", "docs", "--test", "apagar", "--as", "ana", "--args", `{"x":1}`, "--execute")
	if code != 0 || !strings.Contains(out, "Ana") || !strings.Contains(out, "policy:persona") || !strings.Contains(out, "simulação") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if b := f.last().Body; !strings.Contains(b, `"persona":"ana"`) || !strings.Contains(b, `"execute":true`) || !strings.Contains(b, `"x":1`) {
		t.Fatalf("corpo %q", b)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "integration", "tools", "docs", "--test", "apagar"); code != 2 {
		t.Fatalf("sem --as: %d", code)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "integration", "tools", "docs", "--test", "apagar", "--as", "ana", "--args", "[1]"); code != 2 {
		t.Fatalf("args inválidos: %d", code)
	}
}

func TestGateApproveAlways(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/gates/" + gid + "/decide": jsonResp(200, gate1),
	})
	if code, _, errs := exec(t, Runtime{}, gateEnv(ts.URL), "", "gate", "approve", gid, "--always"); code != 0 || !strings.Contains(f.last().Body, `"always":true`) {
		t.Fatalf("%d %q %+v", code, errs, f.last())
	}
	// só a aprovação tem --always
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "gate", "reject", gid, "-n", "x", "--always"); code != 2 {
		t.Fatalf("reject --always: %d", code)
	}
}

func runTokenFor(company string) string {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return enc(`{"alg":"HS256"}`) + "." + enc(`{"cid":"`+company+`"}`) + ".sig"
}

func hookEnv(url string) map[string]string {
	return map[string]string{"HANKO_URL": url, "HANKO_RUN_TOKEN": runTokenFor("acme")}
}

const (
	hookBash = `{"tool_name":"Bash","tool_input":{"command":"ls"},"hook_event_name":"PreToolUse"}`
	permPath = "/api/v1/companies/acme/tool-permission"
)

func TestHookPreToolUseDecisions(t *testing.T) {
	var decision atomic.Value
	decision.Store(`{"decision":"none"}`)
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST " + permPath: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(decision.Load().(string)))
		},
	})
	run := func(stdin string) (int, string) {
		code, out, errs := exec(t, Runtime{}, hookEnv(ts.URL), stdin, "hook", "pretooluse")
		if code != 0 {
			t.Fatalf("hook saiu com %d: %q %q", code, out, errs)
		}
		return code, out
	}
	// sem opinião: nenhuma saída
	if _, out := run(hookBash); out != "" {
		t.Fatalf("none: %q", out)
	}
	if h := f.last(); h.Auth != "Bearer "+runTokenFor("acme") || !strings.Contains(h.Body, `"tool":"Bash"`) || !strings.Contains(h.Body, `"command":"ls"`) {
		t.Fatalf("pedido: %+v", h)
	}
	decision.Store(`{"decision":"allow"}`)
	if _, out := run(hookBash); !strings.Contains(out, `"permissionDecision":"allow"`) || !strings.Contains(out, `"hookEventName":"PreToolUse"`) {
		t.Fatalf("allow: %q", out)
	}
	decision.Store(`{"decision":"deny","reason":"desligada pela política"}`)
	if _, out := run(hookBash); !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "desligada pela política") {
		t.Fatalf("deny: %q", out)
	}
	// tools de servidores MCP já passam pelo gateway: o hook não pergunta
	before := len(f.hits)
	if _, out := run(`{"tool_name":"mcp__docs__apagar","tool_input":{}}`); out != "" || len(f.hits) != before {
		t.Fatalf("mcp__: %q (%d chamadas)", out, len(f.hits)-before)
	}
	// entrada que não é do hook: sem opinião
	for _, in := range []string{"", "isto não é json", `{"tool_input":{}}`} {
		if _, out := run(in); out != "" {
			t.Fatalf("%q: %q", in, out)
		}
	}
	// fora de uma run do Hanko: sem opinião
	code, out, _ := exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL}, hookBash, "hook", "pretooluse")
	if code != 0 || out != "" {
		t.Fatalf("sem token: %d %q", code, out)
	}
}

func TestHookPreToolUseWaitsForGate(t *testing.T) {
	const gate = "0198f2a0-eeee-7000-8000-000000000003"
	var polls atomic.Int32
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST " + permPath: jsonResp(200, api.ToolPermissionDecision{Decision: "ask", GateID: gate}),
		"GET " + permPath + "/" + gate: func(w http.ResponseWriter, r *http.Request) {
			state := api.ToolPermissionState{State: "pending"}
			if polls.Add(1) >= 2 {
				state = api.ToolPermissionState{State: "allow"}
			}
			jsonResp(200, state)(w, r)
		},
	})
	code, out, errs := exec(t, Runtime{}, hookEnv(ts.URL), hookBash, "hook", "pretooluse")
	if code != 0 || !strings.Contains(out, `"permissionDecision":"allow"`) || !strings.Contains(out, gate) || polls.Load() != 2 {
		t.Fatalf("%d %q %q (consultas %d)", code, out, errs, polls.Load())
	}

	// rejeitado ou vencido: nega com o motivo
	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST " + permPath:             jsonResp(200, api.ToolPermissionDecision{Decision: "ask", GateID: gate}),
		"GET " + permPath + "/" + gate: jsonResp(200, api.ToolPermissionState{State: "deny", Reason: "o prazo do gate esgotou sem decisão"}),
	})
	code, out, _ = exec(t, Runtime{}, hookEnv(ts2.URL), hookBash, "hook", "pretooluse")
	if code != 0 || !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "prazo") {
		t.Fatalf("deny: %d %q", code, out)
	}
}

func TestHookPreToolUseFailsClosed(t *testing.T) {
	// a política é uma trava: se o Hanko não responde, a tool nativa não passa
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST " + permPath: jsonResp(500, map[string]any{"title": "erro", "status": 500}),
	})
	code, out, _ := exec(t, Runtime{}, hookEnv(ts.URL), hookBash, "hook", "pretooluse")
	if code != 0 || !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "indisponível") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestCompanyFromRunToken(t *testing.T) {
	for token, want := range map[string]string{runTokenFor("acme"): "acme", "a.b": "", "a.%%%.c": "", "a." + base64.RawURLEncoding.EncodeToString([]byte("nao json")) + ".c": "", "": ""} {
		if got := companyFromRunToken(token); got != want {
			t.Errorf("%q: %q, queria %q", token, got, want)
		}
	}
}
