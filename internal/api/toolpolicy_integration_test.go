//go:build integration

package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// API da política de tools (ADR-0053): políticas, teste como a persona X, "sempre permitir" na decisão do gate e a
// consulta de permissão do hook do claude (run token).
func TestAPIToolPoliciesTestAndPermission(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-two", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{AllowPrivate: true, Secrets: box}
	up := apiUpstream(t)
	ana := e.persona(e.co, "Ana", nil)
	base := e.base()
	e.want(201, "POST", base+"/integrations", `{"kind":"mcp","name":"Docs","url":"`+up.URL+`/mcp","all_personas":true}`, nil)
	e.want(200, "POST", base+"/integrations/docs/tools/refresh", "", nil)

	// política: gravar, trocar (mesma chave), listar por persona, validar
	var pol api.ToolPolicy
	e.want(200, "PUT", base+"/tool-policies", `{"persona":"ana","target":"tool","integration":"docs","tool":"apagar","mode":"ask"}`, &pol)
	if pol.Scope != "persona" || pol.Persona != "Ana" || pol.Integration != "docs" || pol.Tool != "apagar" || pol.Mode != "ask" || pol.Origin != "manual" {
		t.Fatalf("política: %+v", pol)
	}
	e.want(200, "PUT", base+"/tool-policies", `{"target":"native","tool":"Bash","mode":"ask"}`, &pol)
	if pol.Scope != "company" || pol.Target != "native" {
		t.Fatalf("política da empresa: %+v", pol)
	}
	e.want(422, "PUT", base+"/tool-policies", `{"target":"tool","integration":"docs","mode":"ask"}`, nil)     // tool sem nome
	e.want(422, "PUT", base+"/tool-policies", `{"target":"native","tool":"com espaço","mode":"ask"}`, nil)    // nome inválido
	e.want(404, "PUT", base+"/tool-policies", `{"persona":"ninguem","target":"all","mode":"off"}`, nil)       // persona de fora
	e.want(404, "PUT", base+"/tool-policies", `{"target":"integration","integration":"x","mode":"off"}`, nil) // integração de fora
	e.want(422, "PUT", base+"/tool-policies", `{"target":"all","mode":"talvez"}`, nil)                        // modo inválido (enum)
	var list api.ListBody[api.ToolPolicy]
	e.want(200, "GET", base+"/tool-policies", "", &list)
	if len(list.Items) != 2 {
		t.Fatalf("lista: %+v", list.Items)
	}
	e.want(200, "GET", base+"/tool-policies?persona=ana", "", &list)
	if len(list.Items) != 1 || list.Items[0].Tool != "apagar" {
		t.Fatalf("por persona: %+v", list.Items)
	}

	// as efetivas da persona mostram a política que venceu
	var eff api.EffectiveTools
	e.want(200, "GET", base+"/tool-profiles/effective?persona=ana", "", &eff)
	if len(eff.Policies) != 2 {
		t.Fatalf("efetivas: %+v", eff.Policies)
	}
	for _, x := range eff.Integrations[0].Tools {
		if x.Name == "apagar" && (x.Verdict.Mode != "ask" || x.Verdict.Source != "policy:persona" || x.Verdict.PolicyID == "") {
			t.Errorf("apagar: %+v", x)
		}
	}

	// testar como a persona X: simulação e chamada de verdade
	var tr api.ToolTestResult
	e.want(200, "POST", base+"/integrations/docs/tools/apagar/test", `{"persona":"ana"}`, &tr)
	if tr.Decision != "ask" || tr.Executed || tr.Verdict.Source != "policy:persona" || tr.Status != "active" || tr.GateID != "" {
		t.Fatalf("simulação: %+v", tr)
	}
	e.want(200, "POST", base+"/integrations/docs/tools/ler/test", `{"persona":"ana","execute":true}`, &tr)
	if tr.Decision != "allow" || !tr.Executed || tr.Output != "feito ler" {
		t.Fatalf("execute allow: %+v", tr)
	}
	e.want(200, "POST", base+"/integrations/docs/tools/apagar/test", `{"persona":"ana","execute":true,"arguments":{"x":1}}`, &tr)
	if tr.Decision != "ask" || tr.GateID == "" {
		t.Fatalf("execute ask: %+v", tr)
	}
	e.want(422, "POST", base+"/integrations/docs/tools/apagar/test", `{}`, nil)
	e.want(404, "POST", base+"/integrations/docs/tools/apagar/test", `{"persona":"ninguem"}`, nil)

	// "sempre permitir" na decisão do gate
	var g api.Gate
	e.want(200, "POST", base+"/gates/"+tr.GateID+"/decide", `{"decision":"approved","always":true}`, &g)
	if g.Status != "approved" {
		t.Fatalf("gate: %+v", g)
	}
	e.want(200, "GET", base+"/tool-policies?persona=ana", "", &list)
	if len(list.Items) != 1 || list.Items[0].Mode != "allow" || list.Items[0].Origin != "always" {
		t.Fatalf("o sempre permitir trocou a política: %+v", list.Items)
	}
	e.want(200, "POST", base+"/integrations/docs/tools/apagar/test", `{"persona":"ana","execute":true,"arguments":{"x":2}}`, &tr)
	if tr.Decision != "allow" || tr.GateID != "" || tr.Verdict.Source != "policy:persona" {
		t.Fatalf("depois do sempre: %+v", tr)
	}

	// auditoria: origem, ator e o resultado redigido; filtro por origem
	var inv api.ListBody[api.ToolInvocation]
	e.want(200, "GET", base+"/tool-invocations?source=test", "", &inv)
	if len(inv.Items) != 3 || inv.Items[0].Source != "test" || inv.Items[0].Actor == nil || inv.Items[0].Actor.Type != "human" ||
		inv.Items[0].ResultPreview != "feito apagar" || inv.Items[0].PolicySource != "policy:persona" {
		t.Fatalf("auditoria dos testes: %+v", inv.Items)
	}
	e.want(422, "GET", base+"/tool-invocations?source=x", "", nil)

	// o hook do claude consulta a política com o run token da própria execução
	hdr := e.personaToken(e.co, ana)
	status, body := e.call("POST", base+"/tool-permission", `{"tool":"Bash","input":{"command":"ls"}}`, hdr)
	if status != 200 || !strings.Contains(string(body), `"decision":"ask"`) {
		t.Fatalf("permissão ask: %d %s", status, body)
	}
	var dec api.ToolPermissionDecision
	if err := json.Unmarshal(body, &dec); err != nil || dec.GateID == "" {
		t.Fatalf("decisão: %s %v", body, err)
	}
	status, body = e.call("GET", base+"/tool-permission/"+dec.GateID, "", hdr)
	if status != 200 || !strings.Contains(string(body), `"state":"pending"`) {
		t.Fatalf("estado pendente: %d %s", status, body)
	}
	e.want(200, "POST", base+"/gates/"+dec.GateID+"/decide", `{"decision":"approved"}`, nil)
	status, body = e.call("GET", base+"/tool-permission/"+dec.GateID, "", hdr)
	if status != 200 || !strings.Contains(string(body), `"state":"allow"`) {
		t.Fatalf("estado aprovado: %d %s", status, body)
	}
	status, body = e.call("POST", base+"/tool-permission", `{"tool":"Read"}`, hdr)
	if status != 200 || !strings.Contains(string(body), `"decision":"none"`) {
		t.Fatalf("sem política: %d %s", status, body)
	}
	// só run token: o humano (trusted) não usa essa rota
	e.want(403, "POST", base+"/tool-permission", `{"tool":"Bash"}`, nil)
	e.want(403, "GET", base+"/tool-permission/"+dec.GateID, "", nil)

	// run token não grava política, não testa tool e não decide gate
	for _, c := range []struct{ method, path, body string }{
		{"PUT", base + "/tool-policies", `{"target":"all","mode":"allow"}`},
		{"DELETE", base + "/tool-policies/" + pol.ID, ""},
		{"POST", base + "/integrations/docs/tools/ler/test", `{"persona":"ana"}`},
		{"POST", base + "/gates/" + dec.GateID + "/decide", `{"decision":"approved","always":true}`},
	} {
		if status, body := e.call(c.method, c.path, c.body, hdr); status != 403 {
			t.Errorf("run token %s %s: %d %s", c.method, c.path, status, body)
		}
	}

	// apagar
	e.want(204, "DELETE", base+"/tool-policies/"+pol.ID, "", nil)
	e.want(404, "DELETE", base+"/tool-policies/"+pol.ID, "", nil)
}
