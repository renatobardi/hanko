package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/api"
)

// Coordenação de tasks pela CLI (ADR-0040).

func TestTaskDepCommands(t *testing.T) {
	blocked := task1
	blocked.Blocked, blocked.OpenDependencies = true, 1
	rel := api.TaskRelations{
		DependsOn:  []api.TaskRef{{Key: "HNK-7", Title: "Modelar", Status: "in_progress", PersonaName: "Marina"}},
		Dependents: []api.TaskRef{{Key: "HNK-9", Title: "Validar", Status: "todo", Closed: false}},
		Ancestors:  []api.TaskRef{{Key: "HNK-3", Title: "Épico", Status: "in_progress"}},
		Children:   []api.TaskRef{{Key: "HNK-11", Title: "Filha", Status: "done", Closed: true}},
	}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/tasks/HNK-1/dependencies":         jsonResp(201, blocked),
		"DELETE /api/v1/companies/acme/tasks/HNK-1/dependencies/HNK-7": jsonResp(200, task1),
		"GET /api/v1/companies/acme/tasks/HNK-1/relations":             jsonResp(200, rel),
		"POST /api/v1/companies/acme/tasks":                            jsonResp(201, task1),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	body := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(f.last().Body), &m)
		return m
	}

	code, out, _ := exec(t, Runtime{}, env, "", "task", "dep", "add", "HNK-1", "HNK-7")
	if code != 0 || !strings.Contains(out, "HNK-1 depende de HNK-7 (bloqueada)") {
		t.Fatalf("dep add: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"depends_on": "HNK-7"}, body()); diff != "" {
		t.Fatal(diff)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "dep", "rm", "HNK-1", "HNK-7")
	if code != 0 || !strings.Contains(out, "HNK-1 não depende mais de HNK-7") {
		t.Fatalf("dep rm: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "dep", "list", "HNK-1")
	if code != 0 {
		t.Fatal(code)
	}
	for _, frag := range []string{"Depende de:", "HNK-7", "Modelar", "Esperam por esta:", "HNK-9"} {
		if !strings.Contains(out, frag) {
			t.Fatalf("dep list sem %q:\n%s", frag, out)
		}
	}
	_, out, _ = exec(t, Runtime{}, env, "", "task", "dep", "list", "HNK-1", "--json")
	var got api.TaskRelations
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.DependsOn) != 1 {
		t.Fatalf("json: %v %s", err, out)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "task", "dep", "add", "HNK-1"); code != 2 {
		t.Fatalf("dep add sem a segunda task: %d", code)
	}

	// criar já com dependências (flag repetível)
	exec(t, Runtime{}, env, "", "task", "create", "Passo 2", "--depends-on", "HNK-7", "--depends-on", "HNK-8", "--parent", "HNK-3")
	if diff := cmp.Diff(map[string]any{"title": "Passo 2", "parent": "HNK-3", "depends_on": []any{"HNK-7", "HNK-8"}}, body()); diff != "" {
		t.Fatal(diff)
	}
}

func TestTaskParentHoldResumeCommands(t *testing.T) {
	held := task1
	held.Hold = &api.TaskHold{Reason: "congelar", OriginKey: "HNK-1", OriginID: task1.ID}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/companies/acme/tasks/HNK-1":       jsonResp(200, task1),
		"POST /api/v1/companies/acme/tasks/HNK-1/hold":   jsonResp(200, held),
		"POST /api/v1/companies/acme/tasks/HNK-1/resume": jsonResp(200, task1),
		"GET /api/v1/companies/acme/held-tasks":          jsonResp(200, list(held)),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	body := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(f.last().Body), &m)
		return m
	}

	exec(t, Runtime{}, env, "", "task", "parent", "HNK-1", "HNK-3")
	if diff := cmp.Diff(map[string]any{"parent": "HNK-3"}, body()); diff != "" {
		t.Fatal(diff)
	}
	code, out, _ := exec(t, Runtime{}, env, "", "task", "parent", "HNK-1", "--none")
	if code != 0 || !strings.Contains(out, "agora é raiz") {
		t.Fatalf("parent --none: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"clear_parent": true}, body()); diff != "" {
		t.Fatal(diff)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "task", "parent", "HNK-1"); code != 2 {
		t.Fatalf("parent sem argumento: %d", code)
	}

	if code, _, errOut := exec(t, Runtime{}, env, "", "task", "hold", "HNK-1"); code != 2 || !strings.Contains(errOut, "--reason") {
		t.Fatalf("hold sem motivo: %d %q", code, errOut)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "hold", "HNK-1", "--reason", "congelar")
	if code != 0 || !strings.Contains(out, "pausada") {
		t.Fatalf("hold: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"reason": "congelar"}, body()); diff != "" {
		t.Fatal(diff)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "resume", "HNK-1")
	if code != 0 || !strings.Contains(out, "retomada") {
		t.Fatalf("resume: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "held")
	if code != 0 || !strings.Contains(out, "HNK-1") || !strings.Contains(out, "congelar") {
		t.Fatalf("held: %d %q", code, out)
	}

	// concluir o pai com subtask aberta: override humano com motivo
	if code, _, errOut := exec(t, Runtime{}, env, "", "task", "status", "HNK-1", "done", "--force"); code != 2 || !strings.Contains(errOut, "--reason") {
		t.Fatalf("force sem motivo: %d %q", code, errOut)
	}
	exec(t, Runtime{}, env, "", "task", "status", "HNK-1", "done", "--force", "--reason", "virou outra frente")
	if diff := cmp.Diff(map[string]any{"status": "done", "force_close": true, "override_reason": "virou outra frente"}, body()); diff != "" {
		t.Fatal(diff)
	}
}

func TestReviewPolicyCommands(t *testing.T) {
	yes := true
	pol := api.TaskReviewPolicy{
		Task:      api.ReviewOverride{RequireReview: &yes, ReviewerMode: "none"},
		Project:   api.ReviewOverride{ReviewerMode: "inherit"},
		Persona:   api.ReviewOverride{ReviewerMode: "inherit"},
		Effective: api.ReviewPolicy{Required: true, RequiredSource: "task", ReviewerSource: "task"},
	}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/tasks/HNK-1/review-policy":  jsonResp(200, pol),
		"PUT /api/v1/companies/acme/tasks/HNK-1/review-policy":  jsonResp(200, pol),
		"GET /api/v1/companies/acme/projects/web/review-policy": jsonResp(200, api.ReviewOverride{ReviewerMode: "inherit"}),
		"PUT /api/v1/companies/acme/projects/web/review-policy": jsonResp(200, api.ReviewOverride{RequireReview: &yes, ReviewerMode: "custom", ReviewerAgent: "codex"}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	body := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(f.last().Body), &m)
		return m
	}
	code, out, _ := exec(t, Runtime{}, env, "", "task", "review-policy", "HNK-1")
	if code != 0 || !strings.Contains(out, "origem: task") || f.last().Method != "GET" {
		t.Fatalf("leitura: %d %q (%s)", code, out, f.last().Method)
	}
	exec(t, Runtime{}, env, "", "task", "review-policy", "HNK-1", "--require", "--no-reviewer")
	if f.last().Method != "PUT" {
		t.Fatalf("alterar usa PUT: %s", f.last().Method)
	}
	if diff := cmp.Diff(map[string]any{"require_review": true, "reviewer_mode": "none"}, body()); diff != "" {
		t.Fatal(diff)
	}
	exec(t, Runtime{}, env, "", "task", "review-policy", "HNK-1", "--no-require", "--reviewer-agent", "codex", "--reviewer-model", "m1")
	if diff := cmp.Diff(map[string]any{"require_review": false, "reviewer_mode": "custom", "reviewer_agent": "codex", "reviewer_model": "m1"}, body()); diff != "" {
		t.Fatal(diff)
	}
	exec(t, Runtime{}, env, "", "task", "review-policy", "HNK-1", "--clear")
	if diff := cmp.Diff(map[string]any{}, body()); diff != "" {
		t.Fatal(diff)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "task", "review-policy", "HNK-1", "--require", "--no-require"); code != 2 {
		t.Fatalf("flags exclusivas: %d", code)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "task", "review-policy", "HNK-1", "--no-reviewer", "--reviewer-agent", "codex"); code != 2 {
		t.Fatalf("flags exclusivas do revisor: %d", code)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "project", "review-policy", "web", "--require", "--reviewer-agent", "codex")
	if code != 0 || !strings.Contains(out, "codex") {
		t.Fatalf("projeto: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"require_review": true, "reviewer_mode": "custom", "reviewer_agent": "codex"}, body()); diff != "" {
		t.Fatal(diff)
	}
}

func TestCoordinationHelpInEnglish(t *testing.T) {
	code, out, _ := exec(t, Runtime{}, envLang("en"), "", "task", "--help")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"Blocking dependencies between tasks", "Pauses the task and its whole subtree", "Resumes the paused task", "Shows or changes the review policy of the task"} {
		if !strings.Contains(out, want) {
			t.Errorf("help em inglês sem %q:\n%s", want, out)
		}
	}
	_, out, _ = exec(t, Runtime{}, envLang("en"), "", "task", "hold", "--help")
	if !strings.Contains(out, "reason for the pause (required)") {
		t.Errorf("hold --help:\n%s", out)
	}
}
