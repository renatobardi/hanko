//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

func TestAPISkillVersionsRestoreAndPin(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base() + "/skills"
	e.persona(e.co, "Ana", nil)
	e.want(201, "POST", base, `{"name":"Deploy","description":"d","body":"# v1"}`, nil)
	e.want(200, "PATCH", base+"/deploy", `{"body":"# v2"}`, nil)

	var vs api.ListBody[api.SkillVersion]
	e.want(200, "GET", base+"/deploy/versions", "", &vs)
	if len(vs.Items) != 2 || vs.Items[0].Revision != 2 || vs.Items[0].Body != "" || len(vs.Items[0].SHA256) != 64 {
		t.Fatalf("versões: %+v", vs.Items)
	}
	var v api.SkillVersion
	e.want(200, "GET", base+"/deploy/versions/1", "", &v)
	if v.Body != "# v1" || v.Diff != nil {
		t.Fatalf("v1: %+v", v)
	}
	e.want(200, "GET", base+"/deploy/versions/2?against=1", "", &v)
	if v.Diff == nil || v.Diff.Added != 1 || v.Diff.Removed != 1 {
		t.Fatalf("diff: %+v", v.Diff)
	}
	e.want(404, "GET", base+"/deploy/versions/9", "", nil)

	var k api.Skill
	e.want(200, "POST", base+"/deploy/restore", `{"revision":1}`, &k)
	if k.Version != 3 || k.Body != "# v1" {
		t.Fatalf("restaurar: %+v", k)
	}
	e.want(404, "POST", base+"/deploy/restore", `{"revision":7}`, nil)

	// fixar a versão 2 para a persona, junto com o liga/desliga
	e.want(204, "PUT", e.base()+"/personas/ana/skills/deploy", `{"enabled":true,"pinned_revision":2}`, nil)
	var ps api.ListBody[api.PersonaSkill]
	e.want(200, "GET", e.base()+"/personas/ana/skills", "", &ps)
	if len(ps.Items) != 1 || ps.Items[0].PinnedRevision == nil || *ps.Items[0].PinnedRevision != 2 || ps.Items[0].Version != 3 {
		t.Fatalf("fixada: %+v", ps.Items)
	}
	e.want(422, "PUT", e.base()+"/personas/ana/skills/deploy", `{"enabled":true,"pinned_revision":9}`, nil)
	e.want(204, "PUT", base+"/deploy/personas/ana", `{"enabled":true,"pinned_revision":0}`, nil)
	var sp api.ListBody[api.SkillPersona]
	e.want(200, "GET", base+"/deploy/personas", "", &sp)
	if len(sp.Items) != 1 || sp.Items[0].PinnedRevision != nil {
		t.Fatalf("solta: %+v", sp.Items)
	}
}

func TestAPISkillStudio(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base() + "/skills"
	ana := e.persona(e.co, "Ana", nil)
	e.want(201, "POST", base, `{"name":"Deploy","description":"d","body":"# v1"}`, nil)

	// entradas salvas
	var in api.SkillTestInput
	e.want(201, "POST", base+"/deploy/test-inputs", `{"name":"simples","content":"Faça o deploy"}`, &in)
	e.want(409, "POST", base+"/deploy/test-inputs", `{"name":"simples","content":"outra"}`, nil)
	e.want(422, "POST", base+"/deploy/test-inputs", `{"name":"grande","content":"`+strings.Repeat("x", 33<<10)+`"}`, nil)
	e.want(200, "PATCH", base+"/deploy/test-inputs/"+in.ID, `{"content":"Faça o deploy agora"}`, &in)
	if in.Content != "Faça o deploy agora" {
		t.Fatalf("entrada: %+v", in)
	}
	var ins api.ListBody[api.SkillTestInput]
	e.want(200, "GET", base+"/deploy/test-inputs", "", &ins)
	if len(ins.Items) != 1 {
		t.Fatalf("entradas: %+v", ins.Items)
	}

	// rodar: cria a task de sistema; a execução aparece na lista
	var tr api.SkillTestRun
	e.want(201, "POST", base+"/deploy/test-runs", `{"persona":"ana","input_id":"`+in.ID+`"}`, &tr)
	if tr.Status != "queued" || tr.Revision != 1 || tr.Input != "Faça o deploy agora" || tr.Persona != "ana" || tr.TaskID == "" {
		t.Fatalf("teste: %+v", tr)
	}
	e.want(422, "POST", base+"/deploy/test-runs", `{"persona":"ana"}`, nil)
	e.want(404, "POST", base+"/deploy/test-runs", `{"persona":"ninguem","input":"x"}`, nil)
	var got api.SkillTestRun
	e.want(200, "GET", e.base()+"/skill-test-runs/"+tr.ID, "", &got)
	if got.ID != tr.ID {
		t.Fatalf("get: %+v", got)
	}
	var again api.SkillTestRun
	e.want(201, "POST", e.base()+"/skill-test-runs/"+tr.ID+"/rerun", "", &again)
	if again.ID == tr.ID || again.Revision != 1 {
		t.Fatalf("reexecutar: %+v", again)
	}
	e.want(200, "POST", e.base()+"/skill-test-runs/"+tr.ID+"/cancel", "", &got)
	if got.Status != "cancelled" {
		t.Fatalf("cancelar: %+v", got)
	}
	var runs api.ListBody[api.SkillTestRun]
	e.want(200, "GET", base+"/deploy/test-runs", "", &runs)
	if len(runs.Items) != 2 || runs.Items[0].ID != again.ID {
		t.Fatalf("lista: %+v", runs.Items)
	}
	e.want(204, "DELETE", base+"/deploy/test-inputs/"+in.ID, "", nil)
	e.want(404, "GET", base+"/deploy/test-inputs/"+in.ID, "", nil)

	// run token só lê
	tok := e.personaToken(e.co, ana)
	if status, b := e.call("POST", base+"/deploy/test-runs", `{"persona":"ana","input":"x"}`, tok); status != 403 {
		t.Fatalf("persona rodando teste: %d %s", status, b)
	}
	if status, _ := e.call("GET", base+"/deploy/test-runs", "", tok); status != 200 {
		t.Fatalf("persona lendo testes: %d", status)
	}
}

func TestAPISkillSourcesSwitchOff(t *testing.T) {
	e := newAPIEnv(t)
	status, b := e.call("POST", e.base()+"/skill-sources/discover", `{"url":"https://example.com/org/skills.git"}`, nil)
	if status != 422 || !strings.Contains(string(b), "HANKO_SKILL_SOURCES") {
		t.Fatalf("interruptor desligado: %d %s", status, b)
	}
	if status, b := e.call("POST", e.base()+"/skill-sources", `{"url":"https://example.com/org/skills.git"}`, nil); status != 422 {
		t.Fatalf("criar desligado: %d %s", status, b)
	}
	var list api.ListBody[api.SkillSource]
	e.want(200, "GET", e.base()+"/skill-sources", "", &list)
	if len(list.Items) != 0 {
		t.Fatalf("fontes: %+v", list.Items)
	}
	e.want(404, "GET", e.base()+"/skill-sources/00000000-0000-4000-8000-000000000000", "", nil)

	// ligado, a URL continua validada (https público, sem credenciais)
	e.svc.SkillSources.Enabled = true
	for _, bad := range []string{"http://example.com/r", "https://u:p@example.com/r", "https://127.0.0.1/r", "file:///etc"} {
		if status, b := e.call("POST", e.base()+"/skill-sources/discover", `{"url":"`+bad+`"}`, nil); status != 422 {
			t.Errorf("%s: %d %s", bad, status, b)
		}
	}
}
