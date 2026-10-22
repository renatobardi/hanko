package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// stageFake acrescenta ao fake a conclusão de etapa do pipeline do projeto (ADR-0048).
type stageFake struct {
	*fake
	view      service.TaskStageView
	err       error
	lastActor domain.Actor
	lastTask  domain.ID
	lastNote  string
}

func (s *stageFake) CompleteTaskStage(_ context.Context, a domain.Actor, _ domain.ID, task domain.ID, note string) (service.TaskStageView, error) {
	s.lastActor, s.lastTask, s.lastNote = a, task, note
	return s.view, s.err
}

func stageSetup(t *testing.T) (*env, *stageFake) {
	t.Helper()
	e := setup(t, false)
	sf := &stageFake{fake: e.f}
	srv := httptest.NewServer(newHandler(sf, Deps{Tokens: e.tokens, Version: "test"}))
	t.Cleanup(srv.Close)
	e.srv = srv
	return e, sf
}

func TestStageCompleteUsesTheRunToken(t *testing.T) {
	e, sf := stageSetup(t)
	taskID := e.f.tasks[0].ID
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// avançou para a próxima etapa
	sf.view = service.TaskStageView{TaskStage: domain.TaskStage{State: domain.TaskStageWorking, Stage: domain.ProjectStage{Key: "dev", Name: "Desenvolvimento"}, NextKey: "qa"}}
	res := call(t, cs, "hanko_stage_complete", map[string]any{"note": "  feito  "})
	var out stageCompleteOut
	structured(t, res, &out)
	if res.IsError || sf.lastTask != taskID || sf.lastActor.Type != domain.ActorPersona || sf.lastNote != "feito" ||
		out.Stage != "dev" || out.State != "working" || out.NextStage != "qa" || !strings.Contains(text(res), "Desenvolvimento") {
		t.Fatalf("complete: %v %s %+v %+v", res.IsError, text(res), sf.lastActor, out)
	}

	// esperando o gate
	sf.view = service.TaskStageView{TaskStage: domain.TaskStage{State: domain.TaskStageCompleting, Stage: domain.ProjectStage{Key: "dev"}}}
	if res = call(t, cs, "hanko_stage_complete", map[string]any{}); res.IsError || !strings.Contains(text(res), "gate decide o avanço") {
		t.Fatalf("gate: %v %s", res.IsError, text(res))
	}
	// última etapa
	sf.view = service.TaskStageView{TaskStage: domain.TaskStage{State: domain.TaskStageFinished, Stage: domain.ProjectStage{Key: "qa"}}}
	if res = call(t, cs, "hanko_stage_complete", map[string]any{}); res.IsError || !strings.Contains(text(res), "task foi encerrada") {
		t.Fatalf("fim: %v %s", res.IsError, text(res))
	}
	// o service recusa (a task não está num pipeline): erro de ferramenta, não de protocolo
	sf.err = domain.Invalid("task", "a task não está em um pipeline")
	if res = call(t, cs, "hanko_stage_complete", map[string]any{}); !res.IsError || !strings.Contains(text(res), "pipeline") {
		t.Fatalf("recusa: %v %s", res.IsError, text(res))
	}
}

// Sem backend de pipelines a tool nem é anunciada.
func TestStageCompleteHiddenWithoutBackend(t *testing.T) {
	e := setup(t, false)
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name == "hanko_stage_complete" {
			t.Fatal("hanko_stage_complete só existe com o backend de pipelines")
		}
	}
}
