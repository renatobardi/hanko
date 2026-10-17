package cli

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/service"
)

func TestSkillVersionsRestorePin(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	sha := strings.Repeat("a", 64)
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/skills/deploy/versions": jsonResp(200, list(api.SkillVersion{Revision: 2, Name: "Deploy", SHA256: sha, BodyBytes: 10, CreatedAt: now},
			api.SkillVersion{Revision: 1, Name: "Deploy", SHA256: sha, CreatedAt: now})),
		"GET /api/v1/companies/acme/skills/deploy/versions/2": jsonResp(200, api.SkillVersion{Revision: 2, Name: "Deploy", SHA256: sha, Body: "# v2", CreatedAt: now,
			Diff: &api.ArtifactDiff{From: 1, To: 2, Added: 1, Removed: 1, Lines: []api.ArtifactDiffLine{{Op: "delete", Text: "# v1"}, {Op: "insert", Text: "# v2"}}}}),
		"POST /api/v1/companies/acme/skills/deploy/restore":     jsonResp(200, api.Skill{Slug: "deploy", Version: 3}),
		"GET /api/v1/companies/acme/personas/ada/skills":        jsonResp(200, list(api.PersonaSkill{Slug: "deploy", Enabled: true})),
		"PUT /api/v1/companies/acme/personas/ada/skills/deploy": jsonResp(204, nil),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "versions", "deploy")
	if code != 0 || !strings.Contains(out, "aaaaaaaaaaaa") || strings.Count(out, "Deploy") != 2 {
		t.Fatalf("versions: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "versions", "deploy", "--show", "2", "--against", "1")
	if code != 0 || !strings.Contains(out, "- # v1") || !strings.Contains(out, "+ # v2") || !strings.Contains(f.last().Query, "against=1") {
		t.Fatalf("diff: %d %q %+v", code, out, f.last())
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "restore", "deploy", "1")
	if code != 0 || !strings.Contains(out, "agora na versão 3") || !strings.Contains(f.last().Body, `"revision":1`) {
		t.Fatalf("restore: %d %q", code, out)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "restore", "deploy", "zero"); code != 2 {
		t.Fatalf("versão inválida: %d", code)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "pin", "deploy", "ada", "1")
	if code != 0 || !strings.Contains(f.last().Body, `"pinned_revision":1`) || !strings.Contains(f.last().Body, `"enabled":true`) {
		t.Fatalf("pin: %d %q %+v", code, out, f.last())
	}
	code, _, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "pin", "deploy", "ada", "--unpin")
	if code != 0 || !strings.Contains(f.last().Body, `"pinned_revision":0`) {
		t.Fatalf("unpin: %d %+v", code, f.last())
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "pin", "deploy", "ada"); code != 2 {
		t.Fatalf("pin sem versão: %d", code)
	}
}

func TestSkillSourceCommands(t *testing.T) {
	src := api.SkillSource{ID: "0198f2a0-dddd-7000-8000-000000000001", Label: "example.com/org/skills", Ref: "main", Status: "ok", AutoUpdate: true,
		LastCommit: strings.Repeat("b", 40), Found: 3, Imported: 2, Sync: &service.SkillSyncResult{Imported: 2}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/skill-sources":  jsonResp(200, list(src)),
		"POST /api/v1/companies/acme/skill-sources": jsonResp(201, src),
		"POST /api/v1/companies/acme/skill-sources/discover": jsonResp(200, service.SkillSourceDiscovery{Commit: strings.Repeat("c", 40),
			Skills: []service.DiscoveredSkill{{Path: "skills/deploy", Slug: "deploy", HasScripts: true}}}),
		"POST /api/v1/companies/acme/skill-sources/" + src.ID + "/sync": jsonResp(200, service.SkillSyncResult{Commit: strings.Repeat("d", 40), Updated: 1}),
		"DELETE /api/v1/companies/acme/skill-sources/" + src.ID:         jsonResp(204, nil),
	})
	code, out, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "source", "list")
	if code != 0 || !strings.Contains(out, "example.com/org/skills") || !strings.Contains(out, "Sincronizada") || !strings.Contains(out, "bbbbbbb") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "source", "add", "https://example.com/org/skills.git", "--dry-run")
	if code != 0 || !strings.Contains(out, "tem scripts") || !strings.Contains(f.last().Path, "discover") {
		t.Fatalf("dry-run: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "source", "add", "https://example.com/org/skills.git", "--ref", "main",
		"--select", "skills/deploy", "--no-auto-update")
	if b := f.last().Body; code != 0 || !strings.Contains(out, "2 skill(s) importada(s)") || !strings.Contains(b, `"select":["skills/deploy"]`) ||
		!strings.Contains(b, `"auto_update":false`) || !strings.Contains(b, `"ref":"main"`) {
		t.Fatalf("add: %d %q %s", code, out, b)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "source", "sync", src.ID, "--force")
	if code != 0 || !strings.Contains(out, "1 atualizada(s)") || !strings.Contains(f.last().Body, `"force":true`) {
		t.Fatalf("sync: %d %q", code, out)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "source", "rm", src.ID); code != 2 {
		t.Fatalf("rm sem escolha: %d", code)
	}
	code, out, _ = exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "source", "rm", src.ID, "--keep")
	if code != 0 || !strings.Contains(f.last().Query, "keep_skills=true") || !strings.Contains(out, "skills locais") {
		t.Fatalf("rm: %d %q %+v", code, out, f.last())
	}
}

func TestSkillTestWaitsAndPrintsOutput(t *testing.T) {
	old := skillTestPoll
	skillTestPoll = 10 * time.Millisecond
	t.Cleanup(func() { skillTestPoll = old })
	id := "0198f2a0-eeee-7000-8000-000000000001"
	var polls atomic.Int32
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/skills/deploy/test-runs": jsonResp(201, api.SkillTestRun{ID: id, Skill: "deploy", Revision: 2, Persona: "ada", Status: "queued"}),
		"GET /api/v1/companies/acme/skill-test-runs/" + id: func(w http.ResponseWriter, r *http.Request) {
			st := "running"
			if polls.Add(1) > 2 {
				st = "done"
			}
			jsonResp(200, api.SkillTestRun{ID: id, Status: st, Output: "## Saída\n\nok"})(w, r)
		},
	})
	code, out, errs := exec(t, Runtime{}, gateEnv(ts.URL), "Faça o deploy", "skill", "test", "deploy", "--persona", "ada", "--input", "-", "--revision", "2")
	if code != 0 || !strings.Contains(out, "## Saída") || polls.Load() < 3 {
		t.Fatalf("%d %q %q polls=%d", code, out, errs, polls.Load())
	}
	if b := f.hits[0].Body; !strings.Contains(b, `"input":"Faça o deploy"`) || !strings.Contains(b, `"revision":2`) || !strings.Contains(b, `"persona":"ada"`) {
		t.Fatalf("pedido: %s", b)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "test", "deploy", "--input", "-"); code != 2 {
		t.Fatalf("sem persona: %d", code)
	}
	if code, _, _ := exec(t, Runtime{}, gateEnv(ts.URL), "", "skill", "test", "deploy", "--persona", "ada"); code != 2 {
		t.Fatalf("sem entrada: %d", code)
	}
}
