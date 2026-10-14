//go:build integration

package heartbeat

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// afterCheckout runs a hook once the real checkout returned and before the worker looks at its
// result, which is exactly when a request can slip in and be coalesced into the running job.
type hookedBackend struct {
	*service.Service
	mu   sync.Mutex
	hook func()
}

func (b *hookedBackend) CheckoutRun(ctx context.Context, w service.Wake) (domain.Run, error) {
	run, err := b.Service.CheckoutRun(ctx, w)
	b.mu.Lock()
	h := b.hook
	b.hook = nil
	b.mu.Unlock()
	if h != nil {
		h()
	}
	return run, err
}

func (b *hookedBackend) setHook(h func()) {
	b.mu.Lock()
	b.hook = h
	b.mu.Unlock()
}

func newWakeStack(t *testing.T, o Options, delay time.Duration) (*service.Service, *Heartbeat, *hookedBackend, domain.Company) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	if o.Workers == 0 {
		o.Workers = 4
	}
	o.RunTimeout = time.Minute
	hb, err := New(pool, o)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	be := &hookedBackend{Service: svc}
	hb.attach(be, adapter.Registry{"fake": fake.New(fake.Options{Delay: delay})}, memory.Noop{}, broker, authForTest())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})
	return svc, hb, be, co
}

func waitFor(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("não aconteceu em %v: %s", within, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func terminalRuns(t *testing.T, svc *service.Service, co, task domain.ID) (done, all int) {
	t.Helper()
	runs, err := svc.ListRuns(context.Background(), co, service.RunFilter{TaskID: &task})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if r.Status.Terminal() {
			done++
		}
	}
	return done, len(runs)
}

func busyWakes(t *testing.T, svc *service.Service) int {
	t.Helper()
	var n int
	if err := svc.Pool.QueryRow(context.Background(),
		"SELECT count(*) FROM river_job WHERE kind = 'wake' AND state IN ('available','running','retryable','scheduled','pending')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A gate decided while another wake of the same persona and task is being processed used to be
// lost: the queue coalesced the decision's wake into the running job, whose checkout had already
// refused the blocked task and ended. The counter in wake_requests makes the worker try again.
func TestGateDecidedDuringAnotherWakeStillProducesRun(t *testing.T) {
	ctx := context.Background()
	svc, _, be, co := newWakeStack(t, Options{PeriodicInterval: time.Hour}, 2*time.Second)
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	gated, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "Publicar", PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "primeira run terminada", func() bool { d, a := terminalRuns(t, svc, co.ID, gated.ID); return a == 1 && d == 1 })
	waitFor(t, 10*time.Second, "wake inicial encerrado", func() bool { return busyWakes(t, svc) == 0 })

	// The persona is busy on another task, so asking for a gate does not put it in awaiting_gate:
	// it can still be woken for the gated task, which is what lets two wakes overlap.
	other, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "Outra", PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "run da outra task em andamento", func() bool {
		runs, _ := svc.ListRuns(ctx, co.ID, service.RunFilter{TaskID: &other.ID, Statuses: []domain.RunStatus{domain.RunRunning}})
		return len(runs) == 1
	})
	g, err := svc.RequestGate(ctx, domain.PersonaActor(p.ID), co.ID, service.RequestGateInput{Title: "Autorizar o deploy", TaskID: &gated.ID})
	if err != nil {
		t.Fatal(err)
	}
	if tv, _ := svc.GetTask(ctx, co.ID, gated.Key); tv.Status != domain.TaskBlocked {
		t.Fatalf("task = %s, want blocked", tv.Status)
	}
	// Right after the next wake's checkout refuses the blocked task, the human decides.
	decided := make(chan error, 1)
	be.setHook(func() {
		_, err := svc.DecideGate(ctx, domain.HumanActor, co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "Pode."})
		decided <- err
	})
	// a human comment enqueues the wake that will be running when the decision arrives
	if _, err := svc.AddComment(ctx, domain.HumanActor, co.ID, gated.ID, service.AddCommentInput{Body: "e então?"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-decided:
		if err != nil {
			t.Fatalf("DecideGate: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("o wake não chegou ao checkout")
	}
	waitFor(t, 30*time.Second, "segunda run da task depois da decisão", func() bool {
		d, a := terminalRuns(t, svc, co.ID, gated.ID)
		return a >= 2 && d == a
	})
}

// The periodic scan wakes a task whose wake never happened (here: it was created while no queue
// was attached) even though its persona has no heartbeat interval.
func TestTickWakesTaskWhoseWakeWasLost(t *testing.T) {
	ctx := context.Background()
	svc, hb, _, co := newWakeStack(t, Options{PeriodicInterval: 300 * time.Millisecond}, 10*time.Millisecond)
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	quiet := service.New(svc.Pool, nil, nil) // no Jobs: creating the task enqueues no wake
	task, err := quiet.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "Esquecida", PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	_ = hb
	// not stale yet: nothing happens for a while
	time.Sleep(time.Second)
	if _, a := terminalRuns(t, svc, co.ID, task.ID); a != 0 {
		t.Fatalf("task recém-criada não deveria ter run ainda: %d", a)
	}
	if _, err := svc.Pool.Exec(ctx, "UPDATE tasks SET updated_at = now() - interval '2 minutes' WHERE id = $1", task.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "run aberta pela rede de segurança", func() bool { d, a := terminalRuns(t, svc, co.ID, task.ID); return a == 1 && d == 1 })
	// and it does not loop: no second run while the task stays untouched
	time.Sleep(2 * time.Second)
	if _, a := terminalRuns(t, svc, co.ID, task.ID); a != 1 {
		t.Fatalf("runs = %d, a rede de segurança não pode repetir a task", a)
	}
}
