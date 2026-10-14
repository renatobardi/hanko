package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestCronValidation(t *testing.T) {
	tests := []struct {
		spec, tz string
		ok       bool
	}{
		{"0 9 * * *", "America/Sao_Paulo", true},
		{"*/15 * * * 1-5", "UTC", true},
		{"0 9 * * *", "Mars/Olympus", false},
		{"0 9 * *", "UTC", false},
		{"0 9 * * * *", "UTC", false},
		{"@daily", "UTC", false},
		{"61 9 * * *", "UTC", false},
		{"0 25 * * *", "UTC", false},
		{"", "UTC", false},
		{"0 9 * * *", "", false},
	}
	for _, tc := range tests {
		_, err := service.ParseCron(tc.spec, tc.tz)
		if (err == nil) != tc.ok {
			t.Errorf("ParseCron(%q,%q) err=%v, want ok=%v", tc.spec, tc.tz, err, tc.ok)
		}
		if err != nil {
			wantErr(t, err, domain.ErrInvalid)
		}
	}
	// 09:00 in São Paulo is 12:00Z
	n, err := service.NextCron("0 9 * * *", "America/Sao_Paulo", time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC))
	if err != nil || !n.Equal(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %v %v", n, err)
	}
	n, _ = service.NextCron("0 9 * * *", "America/Sao_Paulo", time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))
	if !n.Equal(time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("next after boundary = %v", n)
	}
	// weekday in the zone, not in UTC: Mon 00:30 local is Mon 03:30Z
	n, _ = service.NextCron("30 0 * * 1", "America/Sao_Paulo", time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)) // Mon 09/03 00:00Z = Sun 21:00 local
	if !n.Equal(time.Date(2026, 3, 9, 3, 30, 0, 0, time.UTC)) {
		t.Fatalf("weekday tz next = %v", n)
	}
}

func flowFixture(t *testing.T) (*env, domain.Persona, service.FlowView, time.Time) {
	t.Helper()
	e := newEnv(t)
	p := e.persona(t, "Ada", nil)
	now := time.Date(2026, 3, 10, 13, 0, 0, 0, time.UTC) // 10:00 in São Paulo, after today's 09:00 slot
	e.svc.Now = func() time.Time { return now }
	f, err := e.svc.CreateFlow(context.Background(), domain.HumanActor, e.co.ID, service.CreateFlowInput{
		Name: "Resumo diário", Cron: "0 9 * * *", PersonaID: p.ID,
		TaskTitle: "Resumo de {{data}} (#{{n}})", TaskDescription: "Rodada {{n}} em {{data}}", Priority: domain.PriorityHigh})
	if err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()
	return e, p, f, now
}

func TestCreateUpdateFlow(t *testing.T) {
	ctx := context.Background()
	e, p, f, now := flowFixture(t)
	if f.Slug != "resumo-diario" || !f.Enabled || f.Timezone != "America/Sao_Paulo" || f.PersonaName != "Ada" ||
		f.NextFireAt == nil || !f.NextFireAt.Equal(time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("flow: %+v", f)
	}
	// duplicate slug, bad cron, unknown persona/project
	_, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Resumo diário", Cron: "0 9 * * *", PersonaID: p.ID})
	wantErr(t, err, domain.ErrConflict)
	_, err = e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "x", Cron: "nope", PersonaID: p.ID})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "x", Cron: "0 9 * * *", PersonaID: domain.NewID()})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "x", Cron: "0 9 * * *", PersonaID: p.ID, ProjectID: ptr(domain.NewID())})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "x", Cron: "0 9 * * *", PersonaID: p.ID, Timezone: "Nowhere/Land"})
	wantErr(t, err, domain.ErrInvalid)

	got, err := e.svc.GetFlow(ctx, e.co.ID, "resumo-diario")
	if err != nil || got.ID != f.ID {
		t.Fatalf("get by slug: %v", err)
	}
	if _, err := e.svc.GetFlow(ctx, e.co.ID, f.ID.String()); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.GetFlow(ctx, e.co.ID, "nao-existe")
	wantErr(t, err, domain.ErrNotFound)

	// change the schedule: recomputed from now
	up, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Cron: ptr("30 13 * * *")})
	if err != nil || up.Cron != "30 13 * * *" || !up.NextFireAt.Equal(time.Date(2026, 3, 10, 16, 30, 0, 0, time.UTC)) {
		t.Fatalf("update cron: %+v %v", up, err)
	}
	_, err = e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Cron: ptr("bad")})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Timezone: ptr("Nowhere/Land")})
	wantErr(t, err, domain.ErrInvalid)

	// disable then enable much later: no catch-up, next occurrence from the new "now"
	off, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Enabled: ptr(false)})
	if err != nil || off.Enabled {
		t.Fatalf("disable: %+v %v", off, err)
	}
	later := now.Add(10 * 24 * time.Hour)
	e.svc.Now = func() time.Time { return later }
	on, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Enabled: ptr(true)})
	if err != nil || !on.NextFireAt.After(later) {
		t.Fatalf("enable: %+v %v", on, err)
	}
	// no-op update writes nothing
	if _, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Name: ptr("Resumo diário")}); err != nil {
		t.Fatal(err)
	}
	if n := countVerb(t, e, "flow.created"); n != 1 {
		t.Fatalf("flow.created = %d", n)
	}
	if n := countVerb(t, e, "flow.updated"); n != 3 {
		t.Fatalf("flow.updated = %d", n)
	}
	// another company cannot touch it
	other, _ := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	_, err = e.svc.UpdateFlow(ctx, domain.HumanActor, other.ID, f.ID, service.UpdateFlowInput{Enabled: ptr(false)})
	wantErr(t, err, domain.ErrNotFound)
	_, err = e.svc.RunFlowNow(ctx, domain.HumanActor, other.ID, f.ID)
	wantErr(t, err, domain.ErrNotFound)
	if l, _ := e.svc.ListFlows(ctx, other.ID); len(l) != 0 {
		t.Fatal("flow leaked across companies")
	}
}

func TestDueAndFireFlowIdempotent(t *testing.T) {
	ctx := context.Background()
	e, p, f, now := flowFixture(t)

	due, err := e.svc.DueFlows(ctx, now)
	if err != nil || len(due) != 0 {
		t.Fatalf("not due yet: %v %v", due, err)
	}
	fireAt := *f.NextFireAt // 2026-03-11 12:00Z
	due, err = e.svc.DueFlows(ctx, fireAt.Add(30*time.Second))
	if err != nil || len(due) != 1 || due[0].ID != f.ID {
		t.Fatalf("due: %v %v", due, err)
	}

	e.svc.Now = func() time.Time { return fireAt.Add(30 * time.Second) }
	r, err := e.svc.FireFlow(ctx, f.ID, fireAt)
	if err != nil || r.Duplicate || r.Task == nil {
		t.Fatalf("fire: %+v %v", r, err)
	}
	if r.Task.Title != "Resumo de 2026-03-11 (#1)" || r.Task.Description != "Rodada 1 em 2026-03-11" ||
		r.Task.PersonaID == nil || *r.Task.PersonaID != p.ID || r.Task.Priority != domain.PriorityHigh ||
		r.Task.CreatedBy.Type != domain.ActorSystem || r.Task.Status != domain.TaskTodo {
		t.Fatalf("task: %+v", r.Task)
	}
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].Trigger != domain.TriggerFlow || e.jobs.wakes[0].TaskID != r.Task.ID {
		t.Fatalf("wakes: %+v", e.jobs.wakes)
	}
	after, _ := e.svc.GetFlow(ctx, e.co.ID, f.ID.String())
	if after.LastFiredAt == nil || !after.NextFireAt.Equal(time.Date(2026, 3, 12, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("after fire: last=%v next=%v", after.LastFiredAt, after.NextFireAt)
	}

	// same occurrence again: no second task, same fire
	again, err := e.svc.FireFlow(ctx, f.ID, fireAt)
	if err != nil || !again.Duplicate || again.Fire.ID != r.Fire.ID || again.Task == nil || again.Task.ID != r.Task.ID {
		t.Fatalf("duplicate: %+v %v", again, err)
	}
	tasks, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(tasks) != 1 || len(e.jobs.wakes) != 1 {
		t.Fatalf("tasks=%d wakes=%d", len(tasks), len(e.jobs.wakes))
	}
	if due, _ := e.svc.DueFlows(ctx, fireAt.Add(time.Minute)); len(due) != 0 {
		t.Fatalf("still due after firing: %v", due)
	}

	// concurrent firings of the next occurrence create one task (the first one is done by then:
	// an open task of an earlier firing would make the scheduler skip)
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, r.Task.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
		t.Fatal(err)
	}
	next := *after.NextFireAt
	e.svc.Now = func() time.Time { return next.Add(time.Second) }
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.FireFlow(ctx, f.ID, next); err != nil {
				t.Errorf("concurrent fire: %v", err)
			}
		}()
	}
	wg.Wait()
	tasks, _ = e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(tasks) != 2 {
		t.Fatalf("tasks after concurrent fire = %d, want 2", len(tasks))
	}
	fires, err := e.svc.ListFlowFires(ctx, e.co.ID, f.ID, 10)
	if err != nil || len(fires) != 2 || fires[0].TaskKey == "" || fires[0].TaskTitle != "Resumo de 2026-03-12 (#2)" {
		t.Fatalf("fires: %+v %v", fires, err)
	}

	// a disabled flow is skipped by the scheduler
	_, _ = e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Enabled: ptr(false)})
	sk, err := e.svc.FireFlow(ctx, f.ID, next.Add(24*time.Hour))
	if err != nil || !sk.Skipped || sk.Task != nil {
		t.Fatalf("skipped: %+v %v", sk, err)
	}
	_, err = e.svc.FireFlow(ctx, domain.NewID(), next)
	wantErr(t, err, domain.ErrNotFound)
}

func TestRunFlowNow(t *testing.T) {
	ctx := context.Background()
	e, _, f, _ := flowFixture(t)
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 10, 14, 7, 42, 500, time.UTC) }
	r, err := e.svc.RunFlowNow(ctx, domain.HumanActor, e.co.ID, f.ID)
	if err != nil || r.Task == nil || r.Duplicate {
		t.Fatalf("run now: %+v %v", r, err)
	}
	if !r.Fire.ScheduledFor.Equal(time.Date(2026, 3, 10, 14, 7, 0, 0, time.UTC)) {
		t.Fatalf("scheduled_for = %v", r.Fire.ScheduledFor)
	}
	if r.Task.CreatedBy.Type != domain.ActorHuman {
		t.Fatalf("created by %v", r.Task.CreatedBy)
	}
	again, _ := e.svc.RunFlowNow(ctx, domain.HumanActor, e.co.ID, f.ID)
	if !again.Duplicate {
		t.Fatal("second click in the same minute created another task")
	}
	// the schedule is untouched
	got, _ := e.svc.GetFlow(ctx, e.co.ID, f.ID.String())
	if !got.NextFireAt.Equal(*f.NextFireAt) {
		t.Fatalf("next moved: %v -> %v", f.NextFireAt, got.NextFireAt)
	}
	// manual run works on a disabled flow
	_, _ = e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, f.ID, service.UpdateFlowInput{Enabled: ptr(false)})
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC) }
	if r, err := e.svc.RunFlowNow(ctx, domain.HumanActor, e.co.ID, f.ID); err != nil || r.Task == nil {
		t.Fatalf("manual on disabled: %+v %v", r, err)
	}
}
