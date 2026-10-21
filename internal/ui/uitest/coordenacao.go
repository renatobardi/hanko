package uitest

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// CoordState é o estado em memória da coordenação de tasks (ADR-0040) do Fake.
type CoordState struct {
	// Deps[a] lista as tasks de que a depende.
	Deps map[domain.ID][]domain.ID
	// Holds[t] é a pausa gravada na task t.
	Holds map[domain.ID]domain.TaskHold
	// TaskPolicy e ProjectPolicy são as sobreposições de revisão.
	TaskPolicy    map[domain.ID]domain.ReviewOverride
	ProjectPolicy map[domain.ID]domain.ReviewOverride
}

func (f *Fake) taskByID(id domain.ID) (int, bool) {
	for i, t := range f.Tasks {
		if t.ID == id {
			return i, true
		}
	}
	return 0, false
}

// refresh recalcula os campos derivados de todas as tasks a partir do estado.
func (f *Fake) refreshCoord() {
	for i := range f.Tasks {
		t := &f.Tasks[i]
		t.OpenDependencies, t.DependenciesTotal, t.ChildrenOpen, t.ChildrenTotal, t.Hold = 0, 0, 0, 0, nil
		for _, d := range f.Coord.Deps[t.ID] {
			t.DependenciesTotal++
			if j, ok := f.taskByID(d); ok && f.Tasks[j].Status.Open() {
				t.OpenDependencies++
			}
		}
		t.Blocked = t.OpenDependencies > 0
		for _, c := range f.Tasks {
			if c.ParentID != nil && *c.ParentID == t.ID {
				t.ChildrenTotal++
				if c.Status.Open() {
					t.ChildrenOpen++
				}
			}
		}
		// pausa efetiva: a própria ou a do ancestral mais próximo
		cur := t.ID
		for depth := 0; depth < 16; depth++ {
			if h, ok := f.Coord.Holds[cur]; ok {
				hh := h
				hh.Inherited = cur != t.ID
				t.Hold = &hh
				break
			}
			j, ok := f.taskByID(cur)
			if !ok || f.Tasks[j].ParentID == nil {
				break
			}
			cur = *f.Tasks[j].ParentID
		}
	}
}

func (f *Fake) viewOf(id domain.ID) service.TaskView {
	f.refreshCoord()
	i, _ := f.taskByID(id)
	return f.Tasks[i]
}

func (f *Fake) depView(id domain.ID) service.DependencyView {
	i, _ := f.taskByID(id)
	t := f.Tasks[i]
	return service.DependencyView{TaskRef: domain.TaskRef{ID: t.ID, Key: t.Key, Title: t.Title, Status: t.Status, PersonaID: t.PersonaID},
		PersonaName: t.PersonaName, Closed: !t.Status.Open()}
}

func (f *Fake) TaskRelations(_ context.Context, _, taskID domain.ID) (service.TaskRelations, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("TaskRelations"); err != nil {
		return service.TaskRelations{}, err
	}
	i, ok := f.taskByID(taskID)
	if !ok {
		return service.TaskRelations{}, domain.ErrNotFound
	}
	rel := service.TaskRelations{Ancestors: []domain.TaskRef{}, Children: []service.DependencyView{}, DependsOn: []service.DependencyView{}, Dependents: []service.DependencyView{}}
	for p := f.Tasks[i].ParentID; p != nil; {
		j, ok := f.taskByID(*p)
		if !ok {
			break
		}
		rel.Ancestors = append([]domain.TaskRef{f.depView(f.Tasks[j].ID).TaskRef}, rel.Ancestors...)
		p = f.Tasks[j].ParentID
	}
	for _, c := range f.Tasks {
		if c.ParentID != nil && *c.ParentID == taskID {
			rel.Children = append(rel.Children, f.depView(c.ID))
		}
	}
	for _, d := range f.Coord.Deps[taskID] {
		rel.DependsOn = append(rel.DependsOn, f.depView(d))
	}
	for a, ds := range f.Coord.Deps {
		for _, d := range ds {
			if d == taskID {
				rel.Dependents = append(rel.Dependents, f.depView(a))
			}
		}
	}
	return rel, nil
}

func (f *Fake) AddTaskDependency(_ context.Context, _ domain.Actor, _, taskID, dependsOnID domain.ID) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("AddTaskDependency")
	if err := f.fail("AddTaskDependency"); err != nil {
		return service.TaskView{}, err
	}
	if _, ok := f.taskByID(taskID); !ok {
		return service.TaskView{}, domain.ErrNotFound
	}
	edges := domain.DepEdges{}
	for a, ds := range f.Coord.Deps {
		for _, d := range ds {
			edges.Add(a, d)
		}
	}
	if domain.WouldCycle(edges, taskID, dependsOnID) {
		return service.TaskView{}, domain.ErrDependencyCycle
	}
	if f.Coord.Deps == nil {
		f.Coord.Deps = map[domain.ID][]domain.ID{}
	}
	f.Coord.Deps[taskID] = append(f.Coord.Deps[taskID], dependsOnID)
	return f.viewOf(taskID), nil
}

func (f *Fake) RemoveTaskDependency(_ context.Context, _ domain.Actor, _, taskID, dependsOnID domain.ID) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RemoveTaskDependency")
	if err := f.fail("RemoveTaskDependency"); err != nil {
		return service.TaskView{}, err
	}
	var keep []domain.ID
	for _, d := range f.Coord.Deps[taskID] {
		if d != dependsOnID {
			keep = append(keep, d)
		}
	}
	f.Coord.Deps[taskID] = keep
	return f.viewOf(taskID), nil
}

func (f *Fake) HoldTask(_ context.Context, actor domain.Actor, _, taskID domain.ID, reason string) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("HoldTask")
	if err := f.fail("HoldTask"); err != nil {
		return service.TaskView{}, err
	}
	i, ok := f.taskByID(taskID)
	if !ok {
		return service.TaskView{}, domain.ErrNotFound
	}
	if reason == "" {
		return service.TaskView{}, domain.Invalid("reason", "obrigatório")
	}
	if f.Coord.Holds == nil {
		f.Coord.Holds = map[domain.ID]domain.TaskHold{}
	}
	f.Coord.Holds[taskID] = domain.TaskHold{Reason: reason, By: actor, At: time.Now().UTC(), OriginID: taskID, OriginKey: f.Tasks[i].Key}
	return f.viewOf(taskID), nil
}

func (f *Fake) ResumeTask(_ context.Context, _ domain.Actor, _, taskID domain.ID) (service.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ResumeTask")
	if err := f.fail("ResumeTask"); err != nil {
		return service.TaskView{}, err
	}
	if _, ok := f.Coord.Holds[taskID]; !ok {
		return service.TaskView{}, domain.Invalid("task", "a task não está pausada")
	}
	delete(f.Coord.Holds, taskID)
	return f.viewOf(taskID), nil
}

func (f *Fake) heldTasksLocked() []service.HeldTask {
	f.refreshCoord()
	var out []service.HeldTask
	for id, h := range f.Coord.Holds {
		if i, ok := f.taskByID(id); ok {
			out = append(out, service.HeldTask{Task: f.Tasks[i], Hold: h})
		}
	}
	return out
}

func (f *Fake) ListHeldTasks(_ context.Context, _ domain.ID) ([]service.HeldTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListHeldTasks"); err != nil {
		return nil, err
	}
	return f.heldTasksLocked(), nil
}

func (f *Fake) TaskReviewPolicy(_ context.Context, _, taskID domain.ID) (service.ReviewPolicyView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("TaskReviewPolicy"); err != nil {
		return service.ReviewPolicyView{}, err
	}
	i, ok := f.taskByID(taskID)
	if !ok {
		return service.ReviewPolicyView{}, domain.ErrNotFound
	}
	v := service.ReviewPolicyView{Task: f.Coord.TaskPolicy[taskID]}
	if f.Tasks[i].ProjectID != nil {
		v.Project = f.Coord.ProjectPolicy[*f.Tasks[i].ProjectID]
	}
	for _, p := range f.Personas {
		if f.Tasks[i].PersonaID != nil && p.ID == *f.Tasks[i].PersonaID {
			v.Persona = domain.PersonaReview{RequireReview: p.RequireReview, ReviewerPersonaID: p.ReviewerPersonaID, ReviewerAgent: p.ReviewerAgent, ReviewerModel: p.ReviewerModel}
		}
	}
	v.Effective = domain.ResolveReviewPolicy(v.Task, v.Project, v.Persona)
	return v, nil
}

func (f *Fake) SetTaskReviewPolicy(ctx context.Context, _ domain.Actor, companyID, taskID domain.ID, in domain.ReviewOverride) (service.ReviewPolicyView, error) {
	f.mu.Lock()
	f.note("SetTaskReviewPolicy")
	if err := f.fail("SetTaskReviewPolicy"); err != nil {
		f.mu.Unlock()
		return service.ReviewPolicyView{}, err
	}
	if err := in.Validate(); err != nil {
		f.mu.Unlock()
		return service.ReviewPolicyView{}, err
	}
	if f.Coord.TaskPolicy == nil {
		f.Coord.TaskPolicy = map[domain.ID]domain.ReviewOverride{}
	}
	f.Coord.TaskPolicy[taskID] = in
	f.mu.Unlock()
	return f.TaskReviewPolicy(ctx, companyID, taskID)
}

func (f *Fake) ProjectReviewPolicy(_ context.Context, _, projectID domain.ID) (domain.ReviewOverride, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Coord.ProjectPolicy[projectID], nil
}

func (f *Fake) SetProjectReviewPolicy(_ context.Context, _ domain.Actor, _, projectID domain.ID, in domain.ReviewOverride) (domain.ReviewOverride, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetProjectReviewPolicy")
	if f.Coord.ProjectPolicy == nil {
		f.Coord.ProjectPolicy = map[domain.ID]domain.ReviewOverride{}
	}
	f.Coord.ProjectPolicy[projectID] = in
	return in, nil
}

var _ = fmt.Sprint
