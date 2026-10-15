//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/secretbox"
)

func TestAPISkillsCRUDAndPersonaLinks(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base() + "/skills"
	ana := e.persona(e.co, "Ana", nil)

	// um SKILL.md inteiro: o frontmatter completa nome e descrição e não fica no corpo
	var sk api.Skill
	raw := e.want(201, "POST", base, `{"body":"---\nname: Revisar PR\ndescription: Como revisar um pull request\n---\n# Passos\n1. leia o diff"}`, &sk)
	if sk.Slug != "revisar-pr" || sk.Name != "Revisar PR" || sk.Description != "Como revisar um pull request" || sk.Version != 1 ||
		sk.Body != "# Passos\n1. leia o diff" || strings.Contains(sk.Body, "---") {
		t.Fatalf("skill criada: %s", raw)
	}
	e.want(409, "POST", base, `{"name":"Revisar PR","description":"d","body":"x"}`, nil)
	for name, body := range map[string]string{
		"sem corpo":     `{"name":"A","description":"d"}`,
		"slug inválido": `{"name":"A","slug":"Inválido!","description":"d","body":"x"}`,
		"corpo gigante": `{"name":"A","description":"d","body":"` + strings.Repeat("x", 33<<10) + `"}`,
	} {
		if status, b := e.call("POST", base, body, nil); status != 422 && status != 400 {
			t.Errorf("%s: status %d: %s", name, status, b)
		}
	}

	// o corpo da requisição tem teto próprio (413), bem acima do limite da skill (422 acima)
	if status, b := e.call("POST", base, `{"name":"A","description":"d","body":"`+strings.Repeat("x", 300<<10)+`"}`, nil); status != 413 {
		t.Errorf("requisição gigante: status %d: %.200s", status, b)
	}
	if status, b := e.call("PATCH", base+"/revisar-pr", `{"body":"`+strings.Repeat("x", 300<<10)+`"}`, nil); status != 413 {
		t.Errorf("patch gigante: status %d: %.200s", status, b)
	}

	// leitura por slug e por id; a listagem não traz o corpo e conta as personas
	var got api.Skill
	e.want(200, "GET", base+"/revisar-pr", "", &got)
	if got.ID != sk.ID || got.Body == "" {
		t.Fatalf("get: %+v", got)
	}
	e.want(200, "GET", base+"/"+sk.ID, "", &got)
	e.want(404, "GET", base+"/nao-existe", "", nil)

	// habilitar para a persona (pelos dois lados) e conferir
	e.want(204, "PUT", base+"/revisar-pr/personas/ana", `{"enabled":true}`, nil)
	var ps api.ListBody[api.PersonaSkill]
	e.want(200, "GET", e.base()+"/personas/ana/skills", "", &ps)
	if len(ps.Items) != 1 || ps.Items[0].Slug != "revisar-pr" || !ps.Items[0].Enabled {
		t.Fatalf("skills da persona: %+v", ps.Items)
	}
	var list api.ListBody[api.Skill]
	e.want(200, "GET", base, "", &list)
	if len(list.Items) != 1 || list.Items[0].Body != "" || list.Items[0].Personas == nil || *list.Items[0].Personas != 1 {
		t.Fatalf("lista: %+v", list.Items)
	}
	e.want(204, "PUT", e.base()+"/personas/ana/skills/revisar-pr", `{"enabled":false}`, nil)
	var sp api.ListBody[api.SkillPersona]
	e.want(200, "GET", base+"/revisar-pr/personas", "", &sp)
	if len(sp.Items) != 1 || sp.Items[0].Slug != "ana" || sp.Items[0].Enabled {
		t.Fatalf("personas da skill: %+v", sp.Items)
	}
	e.want(404, "PUT", base+"/revisar-pr/personas/ninguem", `{"enabled":true}`, nil)

	// editar sobe a versão; sem mudança não sobe
	e.want(200, "PATCH", base+"/revisar-pr", `{"body":"# Novo"}`, &got)
	if got.Version != 2 || got.Body != "# Novo" {
		t.Fatalf("patch: %+v", got)
	}
	e.want(200, "PATCH", base+"/revisar-pr", `{"body":"# Novo"}`, &got)
	if got.Version != 2 {
		t.Fatalf("sem mudança a versão não sobe: %+v", got)
	}

	// token de run (persona) não administra skills
	hdr := e.personaToken(e.co, ana)
	for _, c := range []struct{ method, path, body string }{
		{"POST", base, `{"name":"X","description":"d","body":"x"}`},
		{"PATCH", base + "/revisar-pr", `{"body":"y"}`},
		{"DELETE", base + "/revisar-pr", ""},
		{"PUT", base + "/revisar-pr/personas/ana", `{"enabled":true}`},
	} {
		if status, b := e.call(c.method, c.path, c.body, hdr); status != 403 {
			t.Errorf("%s %s com token de persona: %d %s", c.method, c.path, status, b)
		}
	}

	e.want(204, "DELETE", base+"/revisar-pr", "", nil)
	e.want(404, "GET", base+"/revisar-pr", "", nil)
}

func TestAPIIntegrationsNeverReturnSecrets(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	base := e.base() + "/integrations"
	ana := e.persona(e.co, "Ana", nil)

	// MCP: os valores dos headers entram, só os nomes saem
	var mcp api.Integration
	raw := e.want(201, "POST", base, `{"kind":"mcp","name":"Docs","url":"https://mcp.example.com/mcp",
		"headers":{"Authorization":"Bearer tok_super_secreto_123"},"personas":["ana"]}`, &mcp)
	if mcp.Slug != "docs" || mcp.Kind != "mcp" || !mcp.SecretSet || mcp.Config.Transport != "http" || len(mcp.Config.HeaderNames) != 1 ||
		mcp.Config.HeaderNames[0] != "Authorization" || mcp.Secret != "" || strings.Contains(string(raw), "tok_super") {
		t.Fatalf("mcp: %s", raw)
	}
	var one api.Integration
	raw = e.want(200, "GET", base+"/docs", "", &one)
	if strings.Contains(string(raw), "tok_super") {
		t.Fatalf("GET vaza segredo: %s", raw)
	}

	// webhook: o segredo gerado aparece uma única vez
	var wh api.Integration
	raw = e.want(201, "POST", base, `{"kind":"webhook","name":"Notifica","url":"https://hooks.example.com/in","events":["task.created","gate.opened"]}`, &wh)
	if !strings.HasPrefix(wh.Secret, "whsec_") || !wh.SecretSet || wh.SecretHint != wh.Secret[:10] || len(wh.Events) != 2 {
		t.Fatalf("webhook criado: %s", raw)
	}
	raw = e.want(200, "GET", base+"/notifica", "", &one)
	if strings.Contains(string(raw), wh.Secret) || one.Secret != "" || one.SecretHint != wh.SecretHint {
		t.Fatalf("GET do webhook: %s", raw)
	}
	var list api.ListBody[api.Integration]
	raw = e.want(200, "GET", base, "", &list)
	if len(list.Items) != 2 || strings.Contains(string(raw), wh.Secret) || strings.Contains(string(raw), "tok_super") {
		t.Fatalf("lista: %s", raw)
	}
	var rot api.Integration
	e.want(200, "PATCH", base+"/notifica", `{"rotate_secret":true}`, &rot)
	if rot.Secret == "" || rot.Secret == wh.Secret {
		t.Fatalf("rotação: %+v", rot)
	}

	// validações: destino interno, http, evento desconhecido
	for name, body := range map[string]string{
		"destino interno": `{"kind":"webhook","name":"A","url":"https://127.0.0.1/in","events":["task.created"]}`,
		"http":            `{"kind":"webhook","name":"B","url":"http://hooks.example.com/in","events":["task.created"]}`,
		"evento inválido": `{"kind":"webhook","name":"C","url":"https://hooks.example.com/in","events":["task.exploded"]}`,
		"sem eventos":     `{"kind":"webhook","name":"D","url":"https://hooks.example.com/in"}`,
		"stdio desligado": `{"kind":"mcp","name":"E","transport":"stdio","command":"npx"}`,
		"tipo inválido":   `{"kind":"ftp","name":"F"}`,
	} {
		if status, b := e.call("POST", base, body, nil); status != 422 {
			t.Errorf("%s: status %d: %s", name, status, b)
		}
	}
	e.want(409, "POST", base, `{"kind":"mcp","name":"Docs","url":"https://other.example.com/mcp"}`, nil)

	// teste: o destino é um domínio sem DNS no sandbox ou interno; de qualquer forma volta 200 com ok=false
	// e a entrega de teste fica registrada no histórico
	var tr api.IntegrationTestResult
	e.want(200, "POST", base+"/notifica/test", "", &tr)
	if tr.OK || tr.Delivery == nil || !tr.Delivery.Test || tr.Delivery.Event != "ping" {
		t.Fatalf("teste: %+v", tr)
	}
	var dl api.ListBody[api.WebhookDelivery]
	e.want(422, "GET", base+"/notifica/deliveries?limit=101", "", nil) // o teto é 100, o mesmo do service
	e.want(200, "GET", base+"/notifica/deliveries?limit=100", "", &dl)
	e.want(200, "GET", base+"/notifica/deliveries?limit=5", "", &dl)
	if len(dl.Items) != 1 || dl.Items[0].ID != tr.Delivery.ID {
		t.Fatalf("entregas: %+v", dl.Items)
	}

	// personas do MCP: ligadas pelos dois lados
	var ip api.ListBody[api.IntegrationPersona]
	e.want(200, "GET", base+"/docs/personas", "", &ip)
	if len(ip.Items) != 1 || ip.Items[0].Slug != "ana" || !ip.Items[0].Enabled {
		t.Fatalf("personas do MCP: %+v", ip.Items)
	}
	e.want(204, "PUT", e.base()+"/personas/ana/integrations/docs", `{"enabled":false}`, nil)
	var pi api.ListBody[api.PersonaIntegration]
	e.want(200, "GET", e.base()+"/personas/ana/integrations", "", &pi)
	if len(pi.Items) != 1 || pi.Items[0].Slug != "docs" || pi.Items[0].Linked {
		t.Fatalf("integrações da persona: %+v", pi.Items)
	}
	e.want(204, "PUT", base+"/docs/personas/ana", `{"enabled":true}`, nil)
	e.want(422, "PUT", base+"/notifica/personas/ana", `{"enabled":true}`, nil) // webhook não é por persona

	// rotação de chave: sem nada na chave anterior, nada a reescrever
	var rk api.ItemOut[api.RekeyResult]
	e.want(200, "POST", "/api/v1/integrations/rekey-secrets", "", &rk.Body)
	if rk.Body.Rewritten != 0 {
		t.Fatalf("rekey: %+v", rk)
	}

	// token de persona não administra integrações
	hdr := e.personaToken(e.co, ana)
	for _, c := range []struct{ method, path, body string }{
		{"POST", base, `{"kind":"mcp","name":"X","url":"https://x.example.com"}`},
		{"PATCH", base + "/docs", `{"enabled":false}`},
		{"DELETE", base + "/docs", ""},
		{"POST", base + "/notifica/test", ""},
		{"POST", "/api/v1/integrations/rekey-secrets", ""},
	} {
		if status, b := e.call(c.method, c.path, c.body, hdr); status != 403 {
			t.Errorf("%s %s com token de persona: %d %s", c.method, c.path, status, b)
		}
	}

	e.want(204, "DELETE", base+"/docs", "", nil)
	e.want(404, "GET", base+"/docs", "", nil)
}
