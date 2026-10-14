package service_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// orgFixture: ceo <- cto <- eng, and qa <- cto.
func orgFixture(t *testing.T) (e *env, ceo, cto, eng, qa domain.Persona) {
	t.Helper()
	e = newEnv(t)
	ceo = e.persona(t, "Ceo", nil)
	cto = e.persona(t, "Cto", func(in *service.HirePersonaInput) { in.ReportsTo = &ceo.ID })
	eng = e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.ReportsTo = &cto.ID })
	qa = e.persona(t, "Qa", func(in *service.HirePersonaInput) { in.ReportsTo = &cto.ID })
	return
}

func TestOrgChart(t *testing.T) {
	ctx := context.Background()
	e, ceo, cto, eng, qa := orgFixture(t)
	solo := e.persona(t, "Solo", nil)
	e.task(t, service.CreateTaskInput{PersonaID: &eng.ID})
	e.task(t, service.CreateTaskInput{PersonaID: &eng.ID, Status: domain.TaskDone})

	chart, err := e.svc.OrgChart(ctx, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chart.Total != 5 || chart.Depth != 3 || len(chart.Roots) != 2 {
		t.Fatalf("chart: total=%d depth=%d roots=%d", chart.Total, chart.Depth, len(chart.Roots))
	}
	r := chart.Roots[0]
	if r.Persona.ID != ceo.ID || r.Depth != 0 || len(r.Children) != 1 || r.Children[0].Persona.ID != cto.ID {
		t.Fatalf("root: %+v", r)
	}
	c := r.Children[0]
	if len(c.Children) != 2 || c.Children[0].Persona.ID != eng.ID || c.Children[1].Persona.ID != qa.ID || c.Children[0].Depth != 2 {
		t.Fatalf("cto children: %+v", c.Children)
	}
	if c.Children[0].OpenTasks != 1 {
		t.Fatalf("eng open tasks = %d, want 1", c.Children[0].OpenTasks)
	}
	if chart.Roots[1].Persona.ID != solo.ID {
		t.Fatalf("second root: %+v", chart.Roots[1].Persona)
	}
}

func TestReportsToCycles(t *testing.T) {
	ctx := context.Background()
	e, ceo, cto, eng, _ := orgFixture(t)
	up := func(p domain.Persona, in service.UpdatePersonaInput) error {
		_, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, in)
		return err
	}
	wantErr(t, up(ceo, service.UpdatePersonaInput{ReportsTo: &ceo.ID}), domain.ErrInvalid) // itself
	wantErr(t, up(ceo, service.UpdatePersonaInput{ReportsTo: &cto.ID}), domain.ErrInvalid) // direct loop
	wantErr(t, up(ceo, service.UpdatePersonaInput{ReportsTo: &eng.ID}), domain.ErrInvalid) // grandchild
	wantErr(t, up(cto, service.UpdatePersonaInput{ReportsTo: ptr(domain.NewID())}), domain.ErrInvalid)
	wantErr(t, up(cto, service.UpdatePersonaInput{ReportsTo: &ceo.ID, ClearReportsTo: true}), domain.ErrInvalid)

	// a valid move and clearing both work
	got, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, eng.ID, service.UpdatePersonaInput{ReportsTo: &ceo.ID})
	if err != nil || got.ReportsTo == nil || *got.ReportsTo != ceo.ID {
		t.Fatalf("move: %v %+v", err, got.ReportsTo)
	}
	if got, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, cto.ID, service.UpdatePersonaInput{ClearReportsTo: true}); err != nil || got.ReportsTo != nil {
		t.Fatalf("clear: %v %+v", err, got.ReportsTo)
	}
	// now the former top may report to its former subordinate
	if err := up(ceo, service.UpdatePersonaInput{ReportsTo: &cto.ID}); err != nil {
		t.Fatalf("ceo under cto after the chart changed: %v", err)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "persona", SubjectID: &eng.ID})
	if len(acts) < 2 || acts[0].Verb != "persona.updated" {
		t.Fatalf("activity: %+v", acts)
	}

	// require_review toggles through UpdatePersona
	on := true
	if got, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, eng.ID, service.UpdatePersonaInput{RequireReview: &on}); err != nil || !got.RequireReview || got.ReportsTo == nil || *got.ReportsTo != ceo.ID {
		t.Fatalf("require_review: %v %+v", err, got)
	}
}

func TestDelegationFollowsTheChart(t *testing.T) {
	ctx := context.Background()
	e, ceo, cto, eng, qa := orgFixture(t)
	create := func(actor domain.Actor, who *domain.Persona) error {
		in := service.CreateTaskInput{Title: "delegada"}
		if who != nil {
			in.PersonaID = &who.ID
		}
		_, err := e.svc.CreateTask(ctx, actor, e.co.ID, in)
		return err
	}
	cto0 := domain.PersonaActor(cto.ID)
	if err := create(cto0, &cto); err != nil { // itself
		t.Fatalf("to itself: %v", err)
	}
	if err := create(cto0, &eng); err != nil { // subordinate
		t.Fatalf("to subordinate: %v", err)
	}
	if err := create(cto0, nil); err != nil { // unassigned
		t.Fatalf("unassigned: %v", err)
	}
	wantErr(t, create(cto0, &ceo), domain.ErrNotAllowed)                       // superior
	wantErr(t, create(domain.PersonaActor(eng.ID), &qa), domain.ErrNotAllowed) // peer
	wantErr(t, create(domain.PersonaActor(eng.ID), &cto), domain.ErrNotAllowed)
	if err := create(domain.PersonaActor(ceo.ID), &eng); err != nil { // grandchild
		t.Fatalf("to descendant: %v", err)
	}
	if err := create(domain.HumanActor, &ceo); err != nil { // humans assign to anyone
		t.Fatalf("human: %v", err)
	}

	// reassigning follows the same rule
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &eng.ID})
	_, err := e.svc.UpdateTask(ctx, domain.PersonaActor(eng.ID), e.co.ID, tv.ID, service.UpdateTaskInput{PersonaID: &qa.ID})
	wantErr(t, err, domain.ErrNotAllowed)
	if got, err := e.svc.UpdateTask(ctx, cto0, e.co.ID, tv.ID, service.UpdateTaskInput{PersonaID: &qa.ID}); err != nil || got.PersonaID == nil || *got.PersonaID != qa.ID {
		t.Fatalf("cto reassigns to qa: %v", err)
	}
	if _, err := e.svc.UpdateTask(ctx, domain.PersonaActor(eng.ID), e.co.ID, tv.ID, service.UpdateTaskInput{Unassign: true}); err != nil {
		t.Fatalf("unassign: %v", err)
	}
}
