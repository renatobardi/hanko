package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O fake grava as dependências em memória e recusa ciclo com a mesma regra do domínio.
var coordDeps = map[*fake]domain.DepEdges{}

func (f *fake) edges() domain.DepEdges {
	if coordDeps[f] == nil {
		coordDeps[f] = domain.DepEdges{}
	}
	return coordDeps[f]
}

func (f *fake) AddTaskDependency(_ context.Context, a domain.Actor, _, taskID, dependsOnID domain.ID) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor = a
	if domain.WouldCycle(f.edges(), taskID, dependsOnID) {
		return service.TaskView{}, domain.ErrDependencyCycle
	}
	f.edges().Add(taskID, dependsOnID)
	for _, t := range f.tasks {
		if t.ID == taskID {
			t.Blocked, t.OpenDependencies = true, 1
			return t, nil
		}
	}
	return service.TaskView{}, domain.ErrNotFound
}

func (f *fake) RemoveTaskDependency(_ context.Context, a domain.Actor, _, taskID, dependsOnID domain.ID) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastActor = a
	e := f.edges()
	var keep []domain.ID
	for _, d := range e[taskID] {
		if d != dependsOnID {
			keep = append(keep, d)
		}
	}
	e[taskID] = keep
	for _, t := range f.tasks {
		if t.ID == taskID {
			return t, nil
		}
	}
	return service.TaskView{}, domain.ErrNotFound
}

func (f *fake) TaskRelations(_ context.Context, _, taskID domain.ID) (service.TaskRelations, error) {
	rel := service.TaskRelations{Ancestors: []domain.TaskRef{}, Children: []service.DependencyView{}, DependsOn: []service.DependencyView{}, Dependents: []service.DependencyView{}}
	view := func(id domain.ID) service.DependencyView {
		for _, t := range f.tasks {
			if t.ID == id {
				return service.DependencyView{TaskRef: domain.TaskRef{ID: t.ID, Key: t.Key, Title: t.Title, Status: t.Status}, Closed: !t.Status.Open()}
			}
		}
		return service.DependencyView{}
	}
	for _, d := range f.edges()[taskID] {
		rel.DependsOn = append(rel.DependsOn, view(d))
	}
	for a, ds := range f.edges() {
		for _, d := range ds {
			if d == taskID {
				rel.Dependents = append(rel.Dependents, view(a))
			}
		}
	}
	return rel, nil
}

func TestTaskDependencyTool(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	// HNK-1 é a task do token; HNK-2 está done
	res := call(t, cs, "hanko_task_dependency", map[string]any{"action": "add", "depends_on": "HNK-2"})
	if res.IsError {
		t.Fatal(text(res))
	}
	var out taskDependencyOut
	structured(t, res, &out)
	if out.Task.Key != "HNK-1" || len(out.DependsOn) != 1 || out.DependsOn[0].Key != "HNK-2" || !out.DependsOn[0].Closed {
		t.Errorf("saída = %+v", out)
	}
	if e.f.lastActor.Type != domain.ActorPersona {
		t.Errorf("ator = %+v", e.f.lastActor)
	}
	// o ciclo é recusado com a mensagem de conflito
	r := call(t, cs, "hanko_task_dependency", map[string]any{"action": "add", "key": "HNK-2", "depends_on": "HNK-1"})
	if !r.IsError || !strings.Contains(text(r), "conflito") {
		t.Errorf("esperava conflito de ciclo: %q", text(r))
	}
	res = call(t, cs, "hanko_task_dependency", map[string]any{"action": "list"})
	structured(t, res, &out)
	if len(out.DependsOn) != 1 {
		t.Errorf("list = %+v", out)
	}
	if r := call(t, cs, "hanko_task_dependency", map[string]any{"action": "remove", "depends_on": "HNK-2"}); r.IsError {
		t.Fatal(text(r))
	}
	res = call(t, cs, "hanko_task_dependency", map[string]any{"action": "list"})
	structured(t, res, &out)
	if len(out.DependsOn) != 0 {
		t.Errorf("remove não removeu: %+v", out)
	}
	for _, bad := range []map[string]any{{"action": "add"}, {"action": "voar"}, {"action": "add", "depends_on": "HNK-404"}, {"action": "list", "key": "HNK-404"}} {
		if r := call(t, cs, "hanko_task_dependency", bad); !r.IsError {
			t.Errorf("%v deveria falhar", bad)
		}
	}
}

func TestTaskCreateDependsOn(t *testing.T) {
	e := setup(t, false)
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()
	if r := call(t, cs, "hanko_task_create", map[string]any{"title": "Passo 2", "depends_on": []string{"HNK-1", "HNK-2"}}); r.IsError {
		t.Fatal(text(r))
	}
	got := e.f.lastCreate.DependsOn
	if len(got) != 2 || got[0] != e.f.tasks[0].ID || got[1] != e.f.tasks[1].ID {
		t.Errorf("depends_on = %v", got)
	}
	if r := call(t, cs, "hanko_task_create", map[string]any{"title": "x", "depends_on": []string{"HNK-404"}}); !r.IsError {
		t.Error("dependência inexistente deveria falhar")
	}
	// reparentar pelo update
	call(t, cs, "hanko_task_update", map[string]any{"key": "HNK-1", "parent": "HNK-2"})
	if e.f.lastUpdate.ParentID == nil || *e.f.lastUpdate.ParentID != e.f.tasks[1].ID {
		t.Errorf("parent = %v", e.f.lastUpdate.ParentID)
	}
	call(t, cs, "hanko_task_update", map[string]any{"key": "HNK-1", "parent": "none"})
	if !e.f.lastUpdate.ClearParent {
		t.Errorf("clear parent = %+v", e.f.lastUpdate)
	}
}
