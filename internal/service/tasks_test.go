package service_test

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

func TestCreateTaskValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tests := []struct {
		name string
		in   service.CreateTaskInput
		want error
	}{
		{"ok defaults", service.CreateTaskInput{Title: "a"}, nil},
		{"empty title", service.CreateTaskInput{Title: "  "}, domain.ErrInvalid},
		{"long title", service.CreateTaskInput{Title: string(make([]byte, 501))}, domain.ErrInvalid},
		{"bad status", service.CreateTaskInput{Title: "a", Status: "nope"}, domain.ErrInvalid},
		{"bad priority", service.CreateTaskInput{Title: "a", Priority: "urgent"}, domain.ErrInvalid},
		{"unknown persona", service.CreateTaskInput{Title: "a", PersonaID: ptr(domain.NewID())}, domain.ErrInvalid},
		{"unknown project", service.CreateTaskInput{Title: "a", ProjectID: ptr(domain.NewID())}, domain.ErrInvalid},
		{"unknown workspace", service.CreateTaskInput{Title: "a", WorkspaceID: ptr(domain.NewID())}, domain.ErrInvalid},
		{"unknown parent", service.CreateTaskInput{Title: "a", ParentID: ptr(domain.NewID())}, domain.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tv, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, tt.in)
			if tt.want != nil {
				wantErr(t, err, tt.want)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tv.Status != domain.TaskTodo || tv.Priority != domain.PriorityMedium || tv.Key != "HNK-1" {
				t.Errorf("defaults: %+v", tv.Task)
			}
		})
	}
	_, err := e.svc.CreateTask(ctx, domain.HumanActor, domain.NewID(), service.CreateTaskInput{Title: "a"})
	wantErr(t, err, domain.ErrNotFound)

	// failed creations must not burn numbers
	tv := e.task(t, service.CreateTaskInput{Title: "b"})
	if tv.Number != 2 {
		t.Fatalf("number = %d, want 2 (failed attempts roll back)", tv.Number)
	}
	child := e.task(t, service.CreateTaskInput{Title: "filha", ParentID: &tv.ID})
	if child.ParentID == nil || *child.ParentID != tv.ID {
		t.Fatalf("parent: %+v", child.Task)
	}
}

func TestTaskNumberingConcurrent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const n = 20
	nums := make([]int64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tv, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "t"})
			nums[i], errs[i] = tv.Number, err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	want := make([]int64, n)
	for i := range want {
		want[i] = int64(i + 1)
	}
	if d := cmp.Diff(want, nums); d != "" {
		t.Fatalf("numbers not unique+sequential (-want +got):\n%s", d)
	}
}

func TestCreateTaskIdempotentEventID(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	in := service.CreateTaskInput{Title: "idem", EventID: "ev-1"}
	a, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, in)
	if err != nil || b.ID != a.ID {
		t.Fatalf("repeat should return existing: %v %v", b.ID, err)
	}
	list, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(list) != 1 {
		t.Fatalf("tasks = %d, want 1", len(list))
	}

	// concurrent same event id → exactly one task
	var wg sync.WaitGroup
	ids := make([]domain.ID, 6)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tv, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "race", EventID: "ev-2"})
			if err != nil {
				t.Errorf("race create: %v", err)
			}
			ids[i] = tv.ID
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("racers got different tasks: %v", ids)
		}
	}
	list, _ = e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if len(list) != 2 {
		t.Fatalf("tasks = %d, want 2", len(list))
	}

	// the same event id used by another kind of write is a conflict
	_, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdateTaskInput{Title: ptr("novo"), EventID: "ev-1"})
	if err != nil {
		t.Fatalf("update with reused event id returns current state: %v", err)
	}
}

func TestUpdateTaskIdempotentAndTransitions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tv := e.task(t, service.CreateTaskInput{Title: "x"})

	up, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{
		Title: ptr("novo"), Description: ptr("desc"), Priority: ptr(domain.PriorityHigh), EventID: "u1"})
	if err != nil || up.Title != "novo" || up.Priority != domain.PriorityHigh {
		t.Fatalf("update: %+v %v", up, err)
	}
	again, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Title: ptr("outro"), EventID: "u1"})
	if err != nil || again.Title != "novo" {
		t.Fatalf("event replay must be a no-op: %+v %v", again, err)
	}

	tests := []struct {
		name string
		in   service.UpdateTaskInput
		want error
	}{
		{"bad title", service.UpdateTaskInput{Title: ptr("")}, domain.ErrInvalid},
		{"bad status value", service.UpdateTaskInput{Status: ptr(domain.TaskStatus("x"))}, domain.ErrInvalid},
		{"bad priority", service.UpdateTaskInput{Priority: ptr(domain.Priority("x"))}, domain.ErrInvalid},
		{"assign+unassign", service.UpdateTaskInput{PersonaID: ptr(domain.NewID()), Unassign: true}, domain.ErrInvalid},
		{"unknown persona", service.UpdateTaskInput{PersonaID: ptr(domain.NewID())}, domain.ErrInvalid},
		{"unknown project", service.UpdateTaskInput{ProjectID: ptr(domain.NewID())}, domain.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, tt.in)
			wantErr(t, err, tt.want)
		})
	}
	_, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, domain.NewID(), service.UpdateTaskInput{Title: ptr("x")})
	wantErr(t, err, domain.ErrNotFound)

	// lifecycle: close, invalid reopen target, reopen
	done, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)})
	if err != nil || done.ClosedAt == nil {
		t.Fatalf("close: %+v %v", done, err)
	}
	_, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskInProgress)})
	wantErr(t, err, domain.ErrInvalid)
	re, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskTodo)})
	if err != nil || re.ClosedAt != nil || re.Status != domain.TaskTodo {
		t.Fatalf("reopen: %+v %v", re, err)
	}
	// same status is a no-op, not an error
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskTodo)}); err != nil {
		t.Fatalf("same status: %v", err)
	}

	// verbs recorded
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "task", SubjectID: &tv.ID, Limit: 100})
	verbs := map[string]int{}
	for _, a := range acts {
		verbs[a.Verb]++
	}
	for _, v := range []string{"task.created", "task.updated", "task.status_changed", "task.closed"} {
		if verbs[v] == 0 {
			t.Errorf("missing activity verb %s in %v", v, verbs)
		}
	}
	if verbs["task.status_changed"] != 2 {
		t.Errorf("status_changed = %d, want 2", verbs["task.status_changed"])
	}
}

func TestAssignAndProjectUpdate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Eng", nil)
	pr, _ := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "P"})
	tv := e.task(t, service.CreateTaskInput{})

	up, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{PersonaID: &p.ID, ProjectID: &pr.ID})
	if err != nil || up.PersonaName != "Eng" || up.ProjectName != "P" {
		t.Fatalf("assign: %+v %v", up, err)
	}
	un, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Unassign: true})
	if err != nil || un.PersonaID != nil {
		t.Fatalf("unassign: %+v %v", un, err)
	}
}

func TestWakeEnqueuedOnlyWhenExpected(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	worker := e.persona(t, "Worker", nil)
	paused := e.persona(t, "Paused", nil)
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, paused.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaPaused)}); err != nil {
		t.Fatal(err)
	}

	type step struct {
		name  string
		do    func(t *testing.T) error
		wakes int
		trig  domain.RunTrigger
	}
	var task service.TaskView
	steps := []step{
		{"create assigned todo", func(t *testing.T) error {
			var err error
			task, err = e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "a", PersonaID: &worker.ID})
			return err
		}, 1, domain.TriggerAssign},
		{"create assigned backlog", func(t *testing.T) error {
			_, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "b", PersonaID: &worker.ID, Status: domain.TaskBacklog})
			return err
		}, 0, ""},
		{"create unassigned", func(t *testing.T) error {
			_, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "c"})
			return err
		}, 0, ""},
		{"create assigned to paused persona", func(t *testing.T) error {
			_, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "d", PersonaID: &paused.ID})
			return err
		}, 0, ""},
		{"human comment on assigned open task", func(t *testing.T) error {
			_, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, task.ID, service.AddCommentInput{Body: "oi"})
			return err
		}, 1, domain.TriggerComment},
		{"persona comment does not wake", func(t *testing.T) error {
			_, err := e.svc.AddComment(ctx, domain.PersonaActor(worker.ID), e.co.ID, task.ID, service.AddCommentInput{Body: "feito"})
			return err
		}, 0, ""},
		{"manual wake", func(t *testing.T) error { return e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, task.ID) }, 1, domain.TriggerManual},
		{"reassign to other persona", func(t *testing.T) error {
			other := e.persona(t, "Other", nil)
			_, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, task.ID, service.UpdateTaskInput{PersonaID: &other.ID})
			return err
		}, 1, domain.TriggerAssign},
		{"title-only update does not wake", func(t *testing.T) error {
			_, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, task.ID, service.UpdateTaskInput{Title: ptr("z")})
			return err
		}, 0, ""},
		{"move to in_review does not wake", func(t *testing.T) error {
			_, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, task.ID, service.UpdateTaskInput{Status: ptr(domain.TaskInReview)})
			return err
		}, 0, ""},
		{"human comment on in_review assigned task still wakes", func(t *testing.T) error {
			_, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, task.ID, service.AddCommentInput{Body: "revisa"})
			return err
		}, 1, domain.TriggerComment},
		{"close then comment does not wake", func(t *testing.T) error {
			if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, task.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
				return err
			}
			e.jobs.reset()
			_, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, task.ID, service.AddCommentInput{Body: "tarde"})
			return err
		}, 0, ""},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			e.jobs.reset()
			if err := st.do(t); err != nil {
				t.Fatal(err)
			}
			if got := e.jobs.wakeCount(); got != st.wakes {
				t.Fatalf("wakes = %d, want %d", got, st.wakes)
			}
			if st.wakes > 0 {
				w := e.jobs.wakes[0]
				if w.Trigger != st.trig || w.TaskID != task.ID || w.CompanyID != e.co.ID {
					t.Fatalf("wake = %+v", w)
				}
			}
		})
	}

	t.Run("wake without assignee is invalid", func(t *testing.T) {
		tv := e.task(t, service.CreateTaskInput{})
		wantErr(t, e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, tv.ID), domain.ErrInvalid)
	})
	t.Run("wake of missing task", func(t *testing.T) {
		wantErr(t, e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, domain.NewID()), domain.ErrNotFound)
	})
	t.Run("wake for paused persona is invalid", func(t *testing.T) {
		tv := e.task(t, service.CreateTaskInput{PersonaID: &paused.ID})
		wantErr(t, e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, tv.ID), domain.ErrInvalid)
	})
	t.Run("wake of non-workable task is invalid", func(t *testing.T) {
		tv := e.task(t, service.CreateTaskInput{PersonaID: &worker.ID, Status: domain.TaskBacklog})
		wantErr(t, e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, tv.ID), domain.ErrInvalid)
	})
	t.Run("jobs failure rolls back the write", func(t *testing.T) {
		e.jobs.reset()
		e.jobs.wakeErr = errBoom
		before, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
		_, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "rb", PersonaID: &worker.ID})
		if err == nil {
			t.Fatal("expected error")
		}
		after, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
		if len(after) != len(before) {
			t.Fatalf("task persisted despite failed tx: %d → %d", len(before), len(after))
		}
		e.jobs.reset()
	})
	t.Run("nil jobs is tolerated", func(t *testing.T) {
		s2 := *e.svc
		s2.Jobs = nil
		if _, err := s2.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "nj", PersonaID: &worker.ID}); err != nil {
			t.Fatal(err)
		}
		tv := e.task(t, service.CreateTaskInput{PersonaID: &worker.ID})
		if err := s2.WakeTask(ctx, domain.HumanActor, e.co.ID, tv.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestGetTaskByKeyAndList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Eng", nil)
	a := e.task(t, service.CreateTaskInput{Title: "Corrigir login 100%", PersonaID: &p.ID, Priority: domain.PriorityHigh})
	b := e.task(t, service.CreateTaskInput{Title: "Escrever docs", Status: domain.TaskBacklog})

	for _, ref := range []string{a.ID.String(), "HNK-1", "hnk-1"} {
		got, err := e.svc.GetTask(ctx, e.co.ID, ref)
		if err != nil || got.ID != a.ID || got.Key != "HNK-1" || got.PersonaName != "Eng" {
			t.Fatalf("GetTask(%q) = %+v %v", ref, got.Task, err)
		}
	}
	for _, ref := range []string{"HNK-99", "OTH-1", "lixo", ""} {
		_, err := e.svc.GetTask(ctx, e.co.ID, ref)
		wantErr(t, err, domain.ErrNotFound)
	}
	// isolation between companies
	other, _ := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Other"})
	_, err := e.svc.GetTask(ctx, other.ID, "HNK-1")
	wantErr(t, err, domain.ErrNotFound)

	tests := []struct {
		name string
		f    service.TaskFilter
		want []string
	}{
		{"all", service.TaskFilter{}, []string{b.Key, a.Key}},
		{"status", service.TaskFilter{Statuses: []domain.TaskStatus{domain.TaskBacklog}}, []string{b.Key}},
		{"persona", service.TaskFilter{PersonaID: &p.ID}, []string{a.Key}},
		{"query title", service.TaskFilter{Query: "docs"}, []string{b.Key}},
		{"query escapes wildcard", service.TaskFilter{Query: "100%"}, []string{a.Key}},
		{"query no match", service.TaskFilter{Query: "%"}, []string{a.Key}},
		{"limit", service.TaskFilter{Limit: 1}, []string{b.Key}},
		{"offset", service.TaskFilter{Limit: 1, Offset: 1}, []string{a.Key}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.svc.ListTasks(ctx, e.co.ID, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			for _, g := range got {
				keys = append(keys, g.Key)
			}
			if d := cmp.Diff(tt.want, keys); d != "" {
				t.Errorf("(-want +got):\n%s", d)
			}
		})
	}
	_, err = e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{Statuses: []domain.TaskStatus{"bogus"}})
	wantErr(t, err, domain.ErrInvalid)
}

func TestComments(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tv := e.task(t, service.CreateTaskInput{})

	c1, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: " primeiro ", EventID: "c-1"})
	if err != nil || c1.Body != "primeiro" || c1.Author.Type != domain.ActorHuman {
		t.Fatalf("comment: %+v %v", c1, err)
	}
	dup, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "outro texto", EventID: "c-1"})
	if err != nil || dup.ID != c1.ID {
		t.Fatalf("idempotent: %+v %v", dup, err)
	}
	// concurrent duplicates collapse
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "r", EventID: "c-2"}); err != nil {
				t.Errorf("racer: %v", err)
			}
		}()
	}
	wg.Wait()
	list, err := e.svc.ListComments(ctx, e.co.ID, tv.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d %v", len(list), err)
	}
	if list[0].EventID != "c-1" {
		t.Errorf("event id lost: %+v", list[0])
	}

	_, err = e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "  "})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, domain.NewID(), service.AddCommentInput{Body: "x"})
	wantErr(t, err, domain.ErrNotFound)
	_, err = e.svc.ListComments(ctx, e.co.ID, domain.NewID())
	wantErr(t, err, domain.ErrNotFound)
}

func TestBrokerPublishesAfterCommit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ch, cancel := e.broker.Subscribe(events.CompanyTopic(e.co.ID.String()), 32)
	defer cancel()

	e.task(t, service.CreateTaskInput{Title: "ping"})
	types := map[string]bool{}
	for len(ch) > 0 {
		types[(<-ch).Type] = true
	}
	if !types["activity"] || !types["task.updated"] {
		t.Fatalf("published types = %v", types)
	}

	// a rolled-back tx publishes nothing
	e.jobs.wakeErr = errBoom
	p := e.persona(t, "W", nil)
	for len(ch) > 0 {
		<-ch
	}
	_, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "x", PersonaID: &p.ID})
	if err == nil {
		t.Fatal("expected failure")
	}
	if len(ch) != 0 {
		t.Fatalf("published %d messages for a rolled-back tx", len(ch))
	}
}
