package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// fakeJobs records the calls the service makes to the heartbeat.
type fakeJobs struct {
	mu      sync.Mutex
	wakes   []service.Wake
	execs   []domain.ID
	outbox  []domain.ID
	wakeErr error
}

func (f *fakeJobs) WakeTx(_ context.Context, _ pgx.Tx, w service.Wake) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeErr != nil {
		return f.wakeErr
	}
	f.wakes = append(f.wakes, w)
	return nil
}

func (f *fakeJobs) ExecuteRunTx(_ context.Context, _ pgx.Tx, _, runID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, runID)
	return nil
}

func (f *fakeJobs) DeliverMemoryTx(_ context.Context, _ pgx.Tx, id domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outbox = append(f.outbox, id)
	return nil
}

func (f *fakeJobs) wakeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.wakes)
}

func (f *fakeJobs) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wakes, f.execs, f.outbox, f.wakeErr = nil, nil, nil, nil
}

type env struct {
	svc    *service.Service
	jobs   *fakeJobs
	broker *events.Broker
	co     domain.Company
	now    time.Time
}

// newEnv builds a service over a clean database with one company.
func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testdb.New(t)
	jobs := &fakeJobs{}
	broker := events.NewBroker()
	svc := service.New(pool, jobs, broker)
	e := &env{svc: svc, jobs: jobs, broker: broker}
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme Corp"})
	if err != nil {
		t.Fatalf("create company: %v", err)
	}
	e.co = co
	return e
}

func (e *env) persona(t *testing.T, name string, mod func(*service.HirePersonaInput)) domain.Persona {
	t.Helper()
	in := service.HirePersonaInput{Name: name, Agent: "fake", Model: "m1"}
	if mod != nil {
		mod(&in)
	}
	p, err := e.svc.HirePersona(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("hire %s: %v", name, err)
	}
	return p
}

func (e *env) task(t *testing.T, in service.CreateTaskInput) service.TaskView {
	t.Helper()
	if in.Title == "" {
		in.Title = "tarefa"
	}
	tv, err := e.svc.CreateTask(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return tv
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want errors.Is %v", err, target)
	}
}

func ptr[T any](v T) *T { return &v }

var errBoom = errors.New("boom")
