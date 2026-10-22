package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/api"
)

// Etapas do projeto e cards de status do M20 pela CLI (ADR-0048).

func TestStageCommands(t *testing.T) {
	stages := api.ProjectStages{ProjectSlug: "site", Stages: []api.ProjectStage{
		{Key: "triagem", Name: "Triagem", Position: 1, Status: "todo", PersonaSlug: "tri", ExitGate: "none", Tasks: 2},
		{Key: "dev", Name: "Desenvolvimento", Position: 2, Status: "in_progress", PersonaSlug: "dev", ExitGate: "approval"},
	}}
	ts := api.TaskStage{TaskID: "t1", Stage: stages.Stages[1], State: "working", Position: 2, Total: 2, NextStage: "",
		Learnings: []api.StageLearning{{Stage: "dev", Decision: "changes_requested", Note: "cite o arquivo", CreatedAt: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}}}
	srv, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/projects/site/stages":          jsonResp(200, stages),
		"PUT /api/v1/companies/acme/projects/site/stages":          jsonResp(200, stages),
		"DELETE /api/v1/companies/acme/projects/site/stages":       jsonResp(204, nil),
		"GET /api/v1/companies/acme/projects/site/stage-learnings": jsonResp(200, api.ListBody[api.StageLearning]{Items: ts.Learnings}),
		"GET /api/v1/companies/acme/tasks/HNK-1/stage":             jsonResp(200, ts),
		"POST /api/v1/companies/acme/tasks/HNK-1/stage/start":      jsonResp(200, ts),
		"POST /api/v1/companies/acme/tasks/HNK-1/stage/complete":   jsonResp(200, ts),
		"POST /api/v1/companies/acme/tasks/HNK-1/stage/advance":    jsonResp(200, ts),
		"POST /api/v1/companies/acme/tasks/HNK-1/stage/leave":      jsonResp(204, nil),
	})
	env := map[string]string{"HANKO_URL": srv.URL, "HANKO_COMPANY": "acme"}
	body := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(f.last().Body), &m)
		return m
	}

	code, out, _ := exec(t, Runtime{}, env, "", "stage", "list", "site")
	if code != 0 || !strings.Contains(out, "triagem") || !strings.Contains(out, "Desenvolvimento") || !strings.Contains(out, "approval") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "stage", "set", "site", "--stage", "triagem:Triagem:todo:tri", "--stage", "dev:Desenvolvimento:in_progress:dev:approval", "--stage", "qa:QA:in_review")
	if code != 0 {
		t.Fatalf("set: %d %q", code, out)
	}
	want := map[string]any{"stages": []any{
		map[string]any{"key": "triagem", "name": "Triagem", "status": "todo", "persona": "tri"},
		map[string]any{"key": "dev", "name": "Desenvolvimento", "status": "in_progress", "persona": "dev", "exit_gate": "approval"},
		map[string]any{"key": "qa", "name": "QA", "status": "in_review"},
	}}
	if diff := cmp.Diff(want, body()); diff != "" {
		t.Fatalf("corpo do set: %s", diff)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "stage", "set", "site", "--stage", "so-chave"); code != 2 {
		t.Fatalf("etapa mal formada: %d", code)
	}
	// por arquivo JSON
	file := filepath.Join(t.TempDir(), "etapas.json")
	if err := os.WriteFile(file, []byte(`[{"key":"a","name":"A","status":"todo","instructions":"faça"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "stage", "set", "site", "--file", file); code != 0 {
		t.Fatalf("set --file: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"stages": []any{map[string]any{"key": "a", "name": "A", "status": "todo", "instructions": "faça"}}}, body()); diff != "" {
		t.Fatalf("corpo do set --file: %s", diff)
	}

	if code, out, _ = exec(t, Runtime{}, env, "", "stage", "learnings", "site", "--stage", "dev"); code != 0 || !strings.Contains(out, "cite o arquivo") || !strings.Contains(out, "changes_requested") {
		t.Fatalf("learnings: %d %q", code, out)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "stage", "show", "HNK-1"); code != 0 || !strings.Contains(out, "Desenvolvimento (2/2)") || !strings.Contains(out, "Aprendizados desta etapa") {
		t.Fatalf("show: %d %q", code, out)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "stage", "start", "HNK-1", "--stage", "dev"); code != 0 || !strings.Contains(out, "Task no pipeline.") {
		t.Fatalf("start: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"stage": "dev"}, body()); diff != "" {
		t.Fatalf("corpo do start: %s", diff)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "stage", "complete", "HNK-1", "--note", "pronto"); code != 0 || !strings.Contains(out, "Etapa concluída.") {
		t.Fatalf("complete: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"note": "pronto"}, body()); diff != "" {
		t.Fatalf("corpo do complete: %s", diff)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "stage", "advance", "HNK-1", "--to", "triagem", "--note", "volta"); code != 0 {
		t.Fatalf("advance: %d", code)
	}
	if diff := cmp.Diff(map[string]any{"to": "triagem", "note": "volta"}, body()); diff != "" {
		t.Fatalf("corpo do advance: %s", diff)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "stage", "leave", "HNK-1"); code != 0 || !strings.Contains(out, "Task fora do pipeline.") {
		t.Fatalf("leave: %d %q", code, out)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "stage", "clear", "site"); code != 0 || !strings.Contains(out, "Pipeline desligado.") {
		t.Fatalf("clear: %d %q", code, out)
	}
	if _, out, _ = exec(t, Runtime{}, env, "", "stage", "show", "HNK-1", "--json"); !strings.Contains(out, `"state": "working"`) {
		t.Fatalf("--json: %s", out)
	}
}

func TestStatusCardCreateWithDecisionEngineAndCron(t *testing.T) {
	card := api.StatusCard{ID: "c1c1c1c1-0000-0000-0000-000000000000", Title: "Bloqueios", Engine: "decision", ViewState: "fresh", OnDashboard: true,
		ArtifactName: "card-bloqueios.md", Policy: api.StatusCardPolicy{Mode: "cron", Cron: "0 7 * * 1-5", CronTZ: "America/Sao_Paulo"}}
	srv, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/status-cards":                   jsonResp(201, card),
		"GET /api/v1/companies/acme/status-cards/" + card.ID:         jsonResp(200, card),
		"PATCH /api/v1/companies/acme/status-cards/" + card.ID:       jsonResp(200, card),
		"GET /api/v1/companies/acme/dashboard/status-cards":          jsonResp(200, api.ListBody[api.DashboardStatusCard]{}),
		"GET /api/v1/companies/acme/status-cards?archived=all&limit": jsonResp(200, api.ListBody[api.StatusCard]{}),
	})
	env := map[string]string{"HANKO_URL": srv.URL, "HANKO_COMPANY": "acme"}
	body := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(f.last().Body), &m)
		return m
	}
	code, out, _ := exec(t, Runtime{}, env, "", "status-card", "create", "o que está bloqueado?", "--engine", "decision", "--cron", "0 7 * * 1-5", "--tz", "America/Sao_Paulo",
		"--dashboard", "--project", "site", "--status", "blocked,in_review", "--open")
	if code != 0 {
		t.Fatalf("create: %d %q", code, out)
	}
	want := map[string]any{"interest": "o que está bloqueado?", "engine": "decision", "on_dashboard": true,
		"refresh_policy": map[string]any{"mode": "cron", "cron": "0 7 * * 1-5", "cron_tz": "America/Sao_Paulo", "triggers": map[string]any{}},
		"queries":        []any{map[string]any{"status": []any{"blocked", "in_review"}, "project": "site", "open": true}}}
	if diff := cmp.Diff(want, body()); diff != "" {
		t.Fatalf("corpo do create: %s", diff)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "status-card", "show", card.ID)
	if code != 0 || !strings.Contains(out, "decision") || !strings.Contains(out, "cron 0 7 * * 1-5 (America/Sao_Paulo)") || !strings.Contains(out, "card-bloqueios.md") {
		t.Fatalf("show: %d %q", code, out)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "status-card", "update", card.ID, "--no-dashboard", "--cron", "0 8 * * *"); code != 0 {
		t.Fatalf("update: %d", code)
	}
	if diff := cmp.Diff(map[string]any{"on_dashboard": false, "refresh_policy": map[string]any{"mode": "cron", "cron": "0 8 * * *", "triggers": map[string]any{}}}, body()); diff != "" {
		t.Fatalf("corpo do update: %s", diff)
	}
}
