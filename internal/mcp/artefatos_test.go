package mcp

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func (f *fake) PutArtifact(_ context.Context, a domain.Actor, _ domain.ID, in service.PutArtifactInput) (service.ArtifactView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor = a
	f.puts = append(f.puts, in)
	v := service.ArtifactView{Artifact: domain.Artifact{ID: domain.NewID(), CompanyID: f.company.ID, TaskID: in.TaskID, RunID: in.RunID,
		Name: in.Name, Mime: "text/markdown", SizeBytes: int64(len(in.Content)), SHA256: strings.Repeat("a", 64), Version: len(f.puts)}, Versions: len(f.puts)}
	if in.TaskID != nil {
		v.TaskKey = "HNK-1"
	}
	return v, nil
}

func (f *fake) GetGoal(_ context.Context, _ domain.ID, ref string) (domain.Goal, error) {
	for _, g := range f.goals {
		if g.Slug == ref || g.ID.String() == ref {
			return g.Goal, nil
		}
	}
	return domain.Goal{}, domain.ErrNotFound
}

func (f *fake) ListGoalViews(_ context.Context, _ domain.ID, fl service.GoalFilter) ([]service.GoalView, error) {
	var out []service.GoalView
	for _, g := range f.goals {
		if fl.Status == "" || g.Status == fl.Status {
			out = append(out, g)
		}
	}
	return out, nil
}

func (f *fake) RecordGoalProgress(_ context.Context, a domain.Actor, _, id domain.ID, value float64, note string) (service.GoalView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor, f.lastGoal, f.lastValue, f.lastNote = a, id, value, note
	for i, g := range f.goals {
		if g.ID == id {
			g.CurrentValue = value
			g.ProgressPercent = domain.GoalProgress(g.StartValue, g.TargetValue, value) * 100
			if g.Reaches(value) {
				g.Status = domain.GoalAchieved
			}
			f.goals[i] = g
			return g, nil
		}
	}
	return service.GoalView{}, domain.ErrNotFound
}

func TestArtifactPut(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	var out artifactPutOut
	res := call(t, cs, "hanko_artifact_put", map[string]any{"name": "plano.md", "content": "# oi\n"})
	if res.IsError {
		t.Fatal(text(res))
	}
	structured(t, res, &out)
	if out.Artifact.Name != "plano.md" || out.Artifact.Version != 1 || out.Artifact.TaskKey != "HNK-1" || out.Artifact.SizeBytes != 5 {
		t.Fatalf("saída: %+v", out)
	}
	in := e.f.puts[0]
	// o token amarra o artefato à task e à execução, e a autoria é da persona
	if in.TaskID == nil || *in.TaskID != e.f.tasks[0].ID || in.RunID == nil || *in.RunID != e.runID || string(in.Content) != "# oi\n" {
		t.Errorf("escopo do token: %+v", in)
	}
	if e.f.lastActor.Type != domain.ActorPersona {
		t.Errorf("autor: %+v", e.f.lastActor)
	}

	// binário em base64
	png := []byte("\x89PNG\r\n\x1a\n\x00")
	if r := call(t, cs, "hanko_artifact_put", map[string]any{"name": "a.png", "content_base64": base64.StdEncoding.EncodeToString(png)}); r.IsError {
		t.Fatal(text(r))
	}
	if string(e.f.puts[1].Content) != string(png) {
		t.Errorf("base64 não decodificado: %q", e.f.puts[1].Content)
	}

	// erros de uso
	for name, args := range map[string]map[string]any{
		"sem nome":          {"content": "x"},
		"sem conteúdo":      {"name": "a.md"},
		"os dois conteúdos": {"name": "a.md", "content": "x", "content_base64": "eA=="},
		"base64 inválido":   {"name": "a.md", "content_base64": "!!!"},
		"projeto fantasma":  {"name": "a.md", "content": "x", "project": "nada"},
		"task fantasma":     {"name": "a.md", "content": "x", "task": "HNK-99"},
		"task de outra":     {"name": "a.md", "content": "x", "task": "HNK-2"},
	} {
		if r := call(t, cs, "hanko_artifact_put", args); !r.IsError {
			t.Errorf("%s deveria falhar", name)
		}
	}
	if n := len(e.f.puts); n != 2 {
		t.Errorf("chamadas inválidas chegaram ao serviço: %d", n)
	}
	// a task do próprio token é aceita
	if r := call(t, cs, "hanko_artifact_put", map[string]any{"name": "b.md", "content": "x", "task": "HNK-1"}); r.IsError {
		t.Errorf("task do token: %s", text(r))
	}
}

func TestArtifactPutTrusted(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "")
	defer cs.Close()
	if r := call(t, cs, "hanko_artifact_put", map[string]any{"name": "a.md", "content": "x"}); !r.IsError {
		t.Error("sem company deveria falhar no modo trusted")
	}
	res := call(t, cs, "hanko_artifact_put", map[string]any{"company": "acme", "name": "a.md", "content": "x", "task": "HNK-2"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if in := e.f.puts[0]; in.TaskID == nil || *in.TaskID != e.f.tasks[1].ID || in.RunID != nil {
		t.Errorf("trusted: %+v", in)
	}
	if e.f.lastActor.Type != domain.ActorHuman {
		t.Errorf("autor: %+v", e.f.lastActor)
	}
}

func goalFixture(f *fake) {
	due := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	mk := func(slug string, st domain.GoalStatus, start, target, cur float64) service.GoalView {
		g := domain.Goal{ID: domain.NewID(), CompanyID: f.company.ID, Slug: slug, Title: "Meta " + slug, Metric: "usuários", Unit: "usuários",
			StartValue: start, TargetValue: target, CurrentValue: cur, Status: st, DueDate: &due}
		return service.GoalView{Goal: g, ProgressPercent: g.Progress() * 100, TasksTotal: 3, ProjectSlug: "crescimento"}
	}
	f.goals = []service.GoalView{mk("cem", domain.GoalActive, 0, 100, 25), mk("latencia", domain.GoalActive, 200, 50, 125), mk("velha", domain.GoalAbandoned, 0, 10, 1)}
}

func TestGoalList(t *testing.T) {
	e := setup(t, false)
	goalFixture(e.f)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	var out goalListOut
	structured(t, call(t, cs, "hanko_goal_list", map[string]any{}), &out)
	if len(out.Goals) != 2 || out.Goals[0].Slug != "cem" || out.Goals[0].Progress != 25 || out.Goals[0].DueDate != "2026-12-31" || out.Goals[1].Progress != 50 {
		t.Fatalf("ativas: %+v", out.Goals)
	}
	structured(t, call(t, cs, "hanko_goal_list", map[string]any{"status": "all"}), &out)
	if len(out.Goals) != 3 {
		t.Errorf("all: %+v", out.Goals)
	}
	structured(t, call(t, cs, "hanko_goal_list", map[string]any{"status": "abandoned"}), &out)
	if len(out.Goals) != 1 || out.Goals[0].Slug != "velha" {
		t.Errorf("abandoned: %+v", out.Goals)
	}
	if r := call(t, cs, "hanko_goal_list", map[string]any{"status": "nada"}); !r.IsError {
		t.Error("status inválido deveria falhar")
	}
	if r := call(t, cs, "hanko_goal_list", map[string]any{"project": "fantasma"}); !r.IsError {
		t.Error("projeto inexistente deveria falhar")
	}
}

func TestGoalUpdate(t *testing.T) {
	e := setup(t, false)
	goalFixture(e.f)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	var out goalUpdateOut
	res := call(t, cs, "hanko_goal_update", map[string]any{"goal": "cem", "value": 60, "note": "medi no painel"})
	if res.IsError {
		t.Fatal(text(res))
	}
	structured(t, res, &out)
	if out.Goal.Current != 60 || out.Goal.Progress != 60 || out.Goal.Status != "active" {
		t.Fatalf("saída: %+v", out.Goal)
	}
	if e.f.lastValue != 60 || e.f.lastNote != "medi no painel" || e.f.lastActor.Type != domain.ActorPersona {
		t.Errorf("chamada ao serviço: %v %q %+v", e.f.lastValue, e.f.lastNote, e.f.lastActor)
	}
	// cruzar o alvo (meta de redução)
	res = call(t, cs, "hanko_goal_update", map[string]any{"goal": "latencia", "value": 40})
	structured(t, res, &out)
	if out.Goal.Status != "achieved" || out.Goal.Progress != 100 || !strings.Contains(text(res), "atingida") {
		t.Errorf("atingida: %+v %s", out.Goal, text(res))
	}
	for name, args := range map[string]map[string]any{
		"sem meta":      {"value": 1},
		"meta fantasma": {"goal": "nada", "value": 1},
	} {
		if r := call(t, cs, "hanko_goal_update", args); !r.IsError {
			t.Errorf("%s deveria falhar", name)
		}
	}
}
