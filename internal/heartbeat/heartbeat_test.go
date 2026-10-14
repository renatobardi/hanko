package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// ---- fixtures ----

func sampleContext() service.RunContext {
	company := domain.Company{ID: domain.NewID(), Slug: "acme", Name: "Acme"}
	persona := domain.Persona{ID: domain.NewID(), CompanyID: company.ID, Name: "Ana", Title: "Engenheira", Instructions: "Escreva código limpo.", Agent: "fake", Model: "m1"}
	task := service.TaskView{Task: domain.Task{ID: domain.NewID(), CompanyID: company.ID, Key: "HNK-7", Title: "Corrigir login", Description: "Falha no login.",
		Status: domain.TaskInProgress, Priority: domain.PriorityHigh}, ProjectName: "Core"}
	run := domain.Run{ID: domain.NewID(), CompanyID: company.ID, TaskID: task.ID, PersonaID: persona.ID, Status: domain.RunQueued, Trigger: domain.TriggerAssign, Agent: "fake"}
	return service.RunContext{Run: run, Company: company, Persona: persona, Task: task}
}

type fakeBackend struct {
	mu       sync.Mutex
	rc       service.RunContext
	loadErr  error
	startErr error
	finErr   error
	events   []adapter.Event
	started  []string // traceID
	handoff  string
	finished []service.FinishRunInput
	checkout func(service.Wake) (domain.Run, error)
	orphans  int
	cands    []service.WakeCandidate
	pending  []domain.ID
	item     service.MemoryItem
	itemErr  error
	sent     []domain.ID
	failed   []string
}

func (f *fakeBackend) CheckoutRun(_ context.Context, w service.Wake) (domain.Run, error) {
	return f.checkout(w)
}
func (f *fakeBackend) LoadRunContext(context.Context, domain.ID, domain.ID) (service.RunContext, error) {
	return f.rc, f.loadErr
}
func (f *fakeBackend) MarkRunStarted(_ context.Context, _, _ domain.ID, traceID, handoff string) (domain.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, traceID)
	f.handoff = handoff
	return domain.Run{}, f.startErr
}
func (f *fakeBackend) AppendRunEvent(_ context.Context, _, _ domain.ID, e adapter.Event) (domain.RunEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return domain.RunEvent{}, nil
}
func (f *fakeBackend) FinishRun(_ context.Context, _, _ domain.ID, in service.FinishRunInput) (domain.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, in)
	return domain.Run{}, f.finErr
}
func (f *fakeBackend) InterruptOrphanRuns(context.Context) (int, error) { return f.orphans, nil }
func (f *fakeBackend) WakeCandidates(context.Context, int) ([]service.WakeCandidate, error) {
	return f.cands, nil
}
func (f *fakeBackend) GetMemoryItem(context.Context, domain.ID) (service.MemoryItem, error) {
	return f.item, f.itemErr
}
func (f *fakeBackend) MarkMemorySent(_ context.Context, id domain.ID) error {
	f.sent = append(f.sent, id)
	return nil
}
func (f *fakeBackend) MarkMemoryFailed(_ context.Context, _ domain.ID, msg string) error {
	f.failed = append(f.failed, msg)
	return nil
}
func (f *fakeBackend) PendingMemory(context.Context, int) ([]domain.ID, error) { return f.pending, nil }

func (f *fakeBackend) texts(k domain.RunEventKind) (out []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.Kind == k {
			out = append(out, e.Text)
		}
	}
	return
}

type scriptedAdapter struct {
	run  func(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error)
	spec adapter.Spec
}

func (s *scriptedAdapter) Name() string { return "fake" }
func (s *scriptedAdapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	s.spec = spec
	return s.run(ctx, spec, sink)
}

func okAdapter() *scriptedAdapter {
	return &scriptedAdapter{run: func(ctx context.Context, _ adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
		_ = sink.Emit(ctx, adapter.Event{Kind: domain.EventAssistant, Text: "oi"})
		return adapter.Result{SessionRef: "sess-1", Summary: "feito"}, nil
	}}
}

type memStub struct {
	memory.Noop
	handoffErr, briefErr, finalizeErr error
	handoff, briefing                 string
	finalized                         []string
	recorded                          []memory.Fact
	scopes                            []memory.Scope
	recordErr                         error
}

func (m *memStub) AcceptHandoff(context.Context, memory.Scope) (string, error) {
	return m.handoff, m.handoffErr
}
func (m *memStub) Briefing(context.Context, memory.Scope) (string, error) {
	return m.briefing, m.briefErr
}
func (m *memStub) FinalizeSession(_ context.Context, s memory.Scope, agent, sid string) error {
	m.finalized = append(m.finalized, agent+":"+sid+":"+s.Workspace+"/"+s.Project)
	return m.finalizeErr
}
func (m *memStub) Record(_ context.Context, s memory.Scope, f memory.Fact) error {
	m.scopes = append(m.scopes, s)
	m.recorded = append(m.recorded, f)
	return m.recordErr
}
func (m *memStub) AgentMCP(memory.Scope, string) map[string]json.RawMessage {
	return map[string]json.RawMessage{"ai-memory": json.RawMessage(`{"type":"http","url":"http://mem/mcp"}`)}
}

func newTestHB(t *testing.T, be *fakeBackend, ad adapter.Adapter, mem memory.Provider, o Options) (*Heartbeat, *events.Broker) {
	t.Helper()
	if o.RunTimeout == 0 {
		o.RunTimeout = 5 * time.Second
	}
	h, err := New(lazyPool(t), o)
	if err != nil {
		t.Fatal(err)
	}
	b := events.NewBroker()
	h.attach(be, adapter.Registry{"fake": ad}, mem, b, auth.RunTokens{Secret: []byte("0123456789abcdef0123")})
	return h, b
}

// ---- BuildPrompt ----

func TestBuildPrompt(t *testing.T) {
	rc := sampleContext()
	other := domain.NewID()
	for i := 0; i < 35; i++ {
		a := domain.HumanActor
		if i%2 == 1 {
			a = domain.PersonaActor(rc.Persona.ID)
		}
		rc.Comments = append(rc.Comments, domain.Comment{Author: a, Body: fmt.Sprintf("comentário %d", i), CreatedAt: time.Date(2026, 10, 14, 12, i, 0, 0, time.UTC)})
	}
	rc.Comments = append(rc.Comments, domain.Comment{Author: domain.PersonaActor(other), Body: "de outra persona", CreatedAt: time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)})

	system, prompt := BuildPrompt(rc, "briefing X", "handoff Y")
	for _, w := range []string{"Escreva código limpo.", "## Protocolo Hanko", "persona Ana (Engenheira)", "empresa Acme", "task HNK-7", "hanko_*", "resumo objetivo"} {
		if !strings.Contains(system, w) {
			t.Errorf("system lacks %q", w)
		}
	}
	for _, w := range []string{"# Task HNK-7: Corrigir login", "Status: in_progress", "Prioridade: high", "Falha no login.", "6 comentários anteriores omitidos",
		"Ana (você)", "Humano", "Persona " + other.String()[:8], "2026-10-14 13:00 UTC", "## Memória do projeto", "### Handoff\n\nhandoff Y", "### Briefing\n\nbriefing X"} {
		if !strings.Contains(prompt, w) {
			t.Errorf("prompt lacks %q\n%s", w, prompt)
		}
	}
	if strings.Contains(prompt, "comentário 5\n") || !strings.Contains(prompt, "comentário 6\n") {
		t.Error("thread window wrong")
	}

	rc.Comments = nil
	rc.Persona.Instructions = ""
	system, prompt = BuildPrompt(rc, "", "")
	if !strings.HasPrefix(system, "## Protocolo Hanko") || !strings.Contains(prompt, "(sem comentários)") || strings.Contains(prompt, "Memória do projeto") {
		t.Errorf("empty case:\n%s\n%s", system, prompt)
	}
}

func TestBuildEnvAndMCP(t *testing.T) {
	rc := sampleContext()
	env := buildEnv("service.name=x,", "http://h:7420", "tok", rc)
	want := fmt.Sprintf("service.name=x,hanko.company_id=%s,hanko.persona_id=%s,hanko.run_id=%s,hanko.task_id=%s", rc.Company.ID, rc.Persona.ID, rc.Run.ID, rc.Task.ID)
	if env["OTEL_RESOURCE_ATTRIBUTES"] != want {
		t.Errorf("otel attrs = %q", env["OTEL_RESOURCE_ATTRIBUTES"])
	}
	if env["CLAUDE_CODE_ENABLE_TELEMETRY"] != "1" || env["HANKO_URL"] != "http://h:7420" || env["HANKO_RUN_TOKEN"] != "tok" ||
		env["HANKO_RUN_ID"] != rc.Run.ID.String() || env["HANKO_TASK_KEY"] != "HNK-7" {
		t.Errorf("env=%v", env)
	}
	if !strings.HasPrefix(buildEnv("", "u", "t", rc)["OTEL_RESOURCE_ATTRIBUTES"], "hanko.company_id=") {
		t.Error("no base")
	}

	cfg, err := buildMCPConfig("http://h:7420", "tok", map[string]json.RawMessage{
		"ai-memory": json.RawMessage(`{"type":"http","url":"http://mem/mcp"}`),
		"hanko":     json.RawMessage(`{"evil":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfg, &got); err != nil {
		t.Fatal(err)
	}
	h := got.Servers["hanko"]
	if h.Type != "http" || h.URL != "http://h:7420/mcp" || h.Headers["Authorization"] != "Bearer tok" || got.Servers["ai-memory"].URL != "http://mem/mcp" {
		t.Errorf("cfg=%s", cfg)
	}
}

func TestFinalStatus(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name   string
		cause  error
		parent error
		runErr error
		res    adapter.Result
		want   domain.RunStatus
	}{
		{"done", nil, nil, nil, adapter.Result{}, domain.RunDone},
		{"is_error", nil, nil, nil, adapter.Result{IsError: true, Error: "x"}, domain.RunFailed},
		{"run error", nil, nil, boom, adapter.Result{}, domain.RunFailed},
		{"cancel", errRunCancelled, nil, nil, adapter.Result{IsError: true}, domain.RunCancelled},
		{"timeout", errRunTimeout, nil, nil, adapter.Result{IsError: true}, domain.RunExpired},
		{"shutdown", context.Canceled, context.Canceled, nil, adapter.Result{IsError: true}, domain.RunInterrupted},
		{"cancel beats error", errRunCancelled, nil, boom, adapter.Result{}, domain.RunCancelled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := finalStatus(tc.cause, tc.parent, tc.runErr, tc.res)
			if got != tc.want || (got != domain.RunDone && msg == "") {
				t.Fatalf("got %s %q", got, msg)
			}
		})
	}
}

// ---- executeRun ----

func TestExecuteRunDone(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(old); _ = tp.Shutdown(context.Background()) })

	be := &fakeBackend{rc: sampleContext()}
	ad := okAdapter()
	mem := &memStub{handoff: "H", briefing: "B"}
	h, _ := newTestHB(t, be, ad, mem, Options{PublicURL: "http://pub:1/"})
	if err := h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID}); err != nil {
		t.Fatal(err)
	}
	if len(be.finished) != 1 || be.finished[0].Status != domain.RunDone || be.finished[0].Result.Summary != "feito" {
		t.Fatalf("finished=%+v", be.finished)
	}
	if len(be.started) != 1 || len(be.started[0]) != 32 || be.handoff != "H" {
		t.Fatalf("started=%v handoff=%q", be.started, be.handoff)
	}
	if got := be.texts(domain.EventAssistant); len(got) != 1 || got[0] != "oi" {
		t.Fatalf("events=%v", be.events)
	}
	if diff := cmp.Diff([]string{"fake:sess-1:acme/default"}, mem.finalized); diff != "" {
		t.Fatal(diff)
	}
	s := ad.spec
	if s.TaskKey != "HNK-7" || s.Model != "m1" || s.Timeout != 5*time.Second || s.Dir == "" || !strings.Contains(s.Prompt, "### Briefing\n\nB") ||
		!strings.Contains(s.System, "Protocolo Hanko") || s.Env["HANKO_URL"] != "http://pub:1" || s.Env["HANKO_RUN_TOKEN"] == "" {
		t.Fatalf("spec=%+v", s)
	}
	if !strings.Contains(string(s.MCPConfig), "http://pub:1/mcp") || !strings.Contains(string(s.MCPConfig), "ai-memory") {
		t.Fatalf("mcp=%s", s.MCPConfig)
	}
}

func TestExecuteRunTempDirRemoved(t *testing.T) {
	be := &fakeBackend{rc: sampleContext()}
	be.rc.Workspace = &domain.Workspace{Slug: "api", Path: "/nonexistent/ws-xyz"}
	var dir string
	ad := &scriptedAdapter{run: func(_ context.Context, spec adapter.Spec, _ adapter.Sink) (adapter.Result, error) {
		dir = spec.Dir
		return adapter.Result{}, nil
	}}
	mem := &memStub{}
	h, _ := newTestHB(t, be, ad, mem, Options{})
	if err := h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID}); err != nil {
		t.Fatal(err)
	}
	if dir == "" || dir == "/nonexistent/ws-xyz" {
		t.Fatalf("dir=%q", dir)
	}
	if _, err := statDir(dir); err == nil {
		t.Errorf("temp dir %s not removed", dir)
	}
	if got := be.texts(domain.EventSystem); len(got) != 1 || !strings.Contains(got[0], "não encontrado") {
		t.Errorf("events=%v", got)
	}
}

func TestExecuteRunExistingWorkspace(t *testing.T) {
	be := &fakeBackend{rc: sampleContext()}
	ws := t.TempDir()
	be.rc.Workspace = &domain.Workspace{Slug: "api", Path: ws}
	var dir string
	ad := &scriptedAdapter{run: func(_ context.Context, spec adapter.Spec, _ adapter.Sink) (adapter.Result, error) {
		dir = spec.Dir
		return adapter.Result{SessionRef: "s"}, nil
	}}
	mem := &memStub{}
	h, _ := newTestHB(t, be, ad, mem, Options{})
	_ = h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID})
	if dir != ws {
		t.Fatalf("dir=%q", dir)
	}
	if _, err := statDir(ws); err != nil {
		t.Error("workspace must not be removed")
	}
	if mem.finalized[0] != "fake:s:acme/api" {
		t.Errorf("scope: %v", mem.finalized)
	}
}

func TestExecuteRunFailures(t *testing.T) {
	tests := []struct {
		name string
		ad   func(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error)
		want domain.RunStatus
	}{
		{"is error", func(context.Context, adapter.Spec, adapter.Sink) (adapter.Result, error) {
			return adapter.Result{IsError: true, Error: "ruim", ExitCode: 1}, nil
		}, domain.RunFailed},
		{"adapter error", func(context.Context, adapter.Spec, adapter.Sink) (adapter.Result, error) {
			return adapter.Result{}, errors.New("não iniciou")
		}, domain.RunFailed},
		{"panic", func(context.Context, adapter.Spec, adapter.Sink) (adapter.Result, error) { panic("oops") }, domain.RunFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			be := &fakeBackend{rc: sampleContext()}
			h, _ := newTestHB(t, be, &scriptedAdapter{run: tc.ad}, &memStub{}, Options{})
			if err := h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID}); err != nil {
				t.Fatal(err)
			}
			if len(be.finished) != 1 || be.finished[0].Status != tc.want || be.finished[0].Result.Error == "" || !be.finished[0].Result.IsError {
				t.Fatalf("finished=%+v", be.finished)
			}
			if got := be.texts(domain.EventStatus); len(got) != 1 {
				t.Errorf("status events=%v", got)
			}
		})
	}
}

func blockingAdapter() *scriptedAdapter {
	return &scriptedAdapter{run: func(ctx context.Context, _ adapter.Spec, _ adapter.Sink) (adapter.Result, error) {
		<-ctx.Done()
		return adapter.Result{IsError: true, Error: "interrompido"}, nil
	}}
}

func TestExecuteRunCancelViaBroker(t *testing.T) {
	be := &fakeBackend{rc: sampleContext()}
	ad := blockingAdapter()
	started := make(chan struct{})
	inner := ad.run
	ad.run = func(ctx context.Context, s adapter.Spec, k adapter.Sink) (adapter.Result, error) {
		close(started)
		return inner(ctx, s, k)
	}
	h, b := newTestHB(t, be, ad, &memStub{}, Options{})
	done := make(chan error, 1)
	go func() {
		done <- h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID})
	}()
	<-started
	b.Publish(events.Message{Topic: events.RunTopic(be.rc.Run.ID.String()), Type: "run.event"}) // ignored
	b.Publish(events.Message{Topic: events.RunTopic(be.rc.Run.ID.String()), Type: "run.cancel"})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel not honored")
	}
	if len(be.finished) != 1 || be.finished[0].Status != domain.RunCancelled {
		t.Fatalf("finished=%+v", be.finished)
	}
}

func TestExecuteRunTimeout(t *testing.T) {
	be := &fakeBackend{rc: sampleContext()}
	h, _ := newTestHB(t, be, blockingAdapter(), &memStub{}, Options{RunTimeout: 50 * time.Millisecond})
	if err := h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID}); err != nil {
		t.Fatal(err)
	}
	if len(be.finished) != 1 || be.finished[0].Status != domain.RunExpired {
		t.Fatalf("finished=%+v", be.finished)
	}
}

func TestExecuteRunShutdown(t *testing.T) {
	be := &fakeBackend{rc: sampleContext()}
	ctx, cancel := context.WithCancel(context.Background())
	ad := blockingAdapter()
	inner := ad.run
	ad.run = func(c context.Context, s adapter.Spec, k adapter.Sink) (adapter.Result, error) {
		cancel()
		return inner(c, s, k)
	}
	h, _ := newTestHB(t, be, ad, &memStub{}, Options{})
	if err := h.executeRun(ctx, ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID}); err != nil {
		t.Fatal(err)
	}
	if len(be.finished) != 1 || be.finished[0].Status != domain.RunInterrupted {
		t.Fatalf("finished=%+v", be.finished)
	}
}

func TestExecuteRunMemoryUnavailable(t *testing.T) {
	be := &fakeBackend{rc: sampleContext()}
	mem := &memStub{handoffErr: errors.New("conexão recusada"), finalizeErr: errors.New("fora do ar")}
	h, _ := newTestHB(t, be, okAdapter(), mem, Options{})
	if err := h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID}); err != nil {
		t.Fatal(err)
	}
	got := be.texts(domain.EventSystem)
	if len(got) != 1 || got[0] != "memória indisponível: conexão recusada" {
		t.Fatalf("events=%v", got)
	}
	if be.finished[0].Status != domain.RunDone {
		t.Fatalf("status=%s", be.finished[0].Status)
	}
	// briefing-only failure
	be2 := &fakeBackend{rc: sampleContext()}
	h2, _ := newTestHB(t, be2, okAdapter(), &memStub{briefErr: errors.New("x")}, Options{})
	_ = h2.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be2.rc.Company.ID, RunID: be2.rc.Run.ID})
	if got := be2.texts(domain.EventSystem); len(got) != 1 || !strings.Contains(got[0], "memória indisponível: x") {
		t.Fatalf("events=%v", got)
	}
}

func TestExecuteRunEarlyExits(t *testing.T) {
	t.Run("not queued", func(t *testing.T) {
		be := &fakeBackend{rc: sampleContext()}
		be.rc.Run.Status = domain.RunCancelled
		h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
		if err := h.executeRun(context.Background(), ExecuteRunArgs{}); err != nil || len(be.finished) != 0 || len(be.started) != 0 {
			t.Fatalf("err=%v %+v", err, be)
		}
	})
	t.Run("not found", func(t *testing.T) {
		be := &fakeBackend{loadErr: domain.ErrNotFound}
		h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
		if err := h.executeRun(context.Background(), ExecuteRunArgs{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("load error finishes failed", func(t *testing.T) {
		be := &fakeBackend{loadErr: errors.New("db")}
		h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
		if err := h.executeRun(context.Background(), ExecuteRunArgs{}); err == nil || len(be.finished) != 1 || be.finished[0].Status != domain.RunFailed {
			t.Fatalf("err=%v %+v", err, be.finished)
		}
	})
	t.Run("unknown adapter", func(t *testing.T) {
		be := &fakeBackend{rc: sampleContext()}
		be.rc.Persona.Agent = "nope"
		h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
		if err := h.executeRun(context.Background(), ExecuteRunArgs{}); err != nil || len(be.finished) != 1 || !strings.Contains(be.finished[0].Result.Error, "nope") {
			t.Fatalf("err=%v %+v", err, be.finished)
		}
	})
	t.Run("mark started fails", func(t *testing.T) {
		be := &fakeBackend{rc: sampleContext(), startErr: errors.New("db")}
		h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
		if err := h.executeRun(context.Background(), ExecuteRunArgs{}); err == nil || len(be.finished) != 1 {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("token secret too short", func(t *testing.T) {
		be := &fakeBackend{rc: sampleContext()}
		h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
		h.tokens = auth.RunTokens{}
		if err := h.executeRun(context.Background(), ExecuteRunArgs{}); err != nil || be.finished[0].Status != domain.RunFailed {
			t.Fatalf("err=%v %+v", err, be.finished)
		}
	})
	t.Run("finish error propagates", func(t *testing.T) {
		be := &fakeBackend{rc: sampleContext(), finErr: errors.New("db")}
		h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
		if err := h.executeRun(context.Background(), ExecuteRunArgs{}); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("not attached", func(t *testing.T) {
		h, _ := New(lazyPool(t), Options{})
		if err := h.executeRun(context.Background(), ExecuteRunArgs{}); !errors.Is(err, errNotAttached) {
			t.Fatal(err)
		}
		if err := h.Start(context.Background()); err == nil {
			t.Fatal("want error")
		}
	})
}

// ---- wake / deliver / scans ----

func TestWake(t *testing.T) {
	for _, tc := range []struct {
		err     error
		wantErr bool
	}{{nil, false}, {domain.ErrActiveRun, false}, {domain.ErrInvalid, false}, {domain.ErrNotFound, false},
		{fmt.Errorf("x: %w", domain.ErrActiveRun), false}, {errors.New("db"), true}} {
		be := &fakeBackend{checkout: func(service.Wake) (domain.Run, error) { return domain.Run{}, tc.err }}
		h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
		if err := h.wake(context.Background(), WakeArgs{}); (err != nil) != tc.wantErr {
			t.Errorf("err %v → %v", tc.err, err)
		}
	}
}

func TestDeliverMemory(t *testing.T) {
	ws := &domain.Workspace{Slug: "api", Path: "/w"}
	fact, _ := json.Marshal(memory.Fact{Path: "hanko/tasks/HNK-1.md", Title: "t", Body: "b"})
	id := domain.NewID()
	item := service.MemoryItem{ID: id, CompanySlug: "acme", Workspace: ws, Kind: "task.created", Payload: fact}

	be := &fakeBackend{item: item}
	mem := &memStub{}
	h, _ := newTestHB(t, be, okAdapter(), mem, Options{})
	if err := h.deliverMemory(context.Background(), DeliverMemoryArgs{OutboxID: id}); err != nil {
		t.Fatal(err)
	}
	if len(be.sent) != 1 || mem.scopes[0] != (memory.Scope{Workspace: "acme", Project: "api", Cwd: "/w"}) || mem.recorded[0].Kind != "task.created" {
		t.Fatalf("sent=%v scopes=%v rec=%v", be.sent, mem.scopes, mem.recorded)
	}

	// provider failure → marked failed + error (river retries)
	be = &fakeBackend{item: service.MemoryItem{ID: id, CompanySlug: "acme", Payload: fact}}
	mem = &memStub{recordErr: errors.New("fora")}
	h, _ = newTestHB(t, be, okAdapter(), mem, Options{})
	if err := h.deliverMemory(context.Background(), DeliverMemoryArgs{OutboxID: id}); err == nil || len(be.failed) != 1 || len(be.sent) != 0 {
		t.Fatalf("err=%v failed=%v", err, be.failed)
	}
	if mem.scopes[0].Project != "default" {
		t.Errorf("scope=%+v", mem.scopes[0])
	}

	// bad payload → cancelled job, marked failed
	be = &fakeBackend{item: service.MemoryItem{ID: id, Payload: json.RawMessage(`{`)}}
	h, _ = newTestHB(t, be, okAdapter(), mem, Options{})
	if err := h.deliverMemory(context.Background(), DeliverMemoryArgs{OutboxID: id}); err == nil || len(be.failed) != 1 {
		t.Fatalf("err=%v", err)
	}

	// missing item → silent
	be = &fakeBackend{itemErr: domain.ErrNotFound}
	h, _ = newTestHB(t, be, okAdapter(), mem, Options{})
	if err := h.deliverMemory(context.Background(), DeliverMemoryArgs{OutboxID: id}); err != nil {
		t.Fatal(err)
	}
}

func TestTickAndSweep(t *testing.T) {
	c := service.WakeCandidate{CompanyID: domain.NewID(), PersonaID: domain.NewID(), TaskID: domain.NewID()}
	id := domain.NewID()
	be := &fakeBackend{cands: []service.WakeCandidate{c}, pending: []domain.ID{id}}
	h, _ := newTestHB(t, be, okAdapter(), nil, Options{})
	var wakes []WakeArgs
	var delivs []DeliverMemoryArgs
	h.enqueueWake = func(_ context.Context, a WakeArgs) error { wakes = append(wakes, a); return nil }
	h.enqueueDeliver = func(_ context.Context, a DeliverMemoryArgs) error { delivs = append(delivs, a); return nil }

	if err := (&tickWorker{h: h}).Work(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := (&sweepWorker{h: h}).Work(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(wakes) != 1 || wakes[0].Trigger != domain.TriggerHeartbeat || wakes[0].TaskID != c.TaskID || len(delivs) != 1 || delivs[0].OutboxID != id {
		t.Fatalf("wakes=%+v delivs=%+v", wakes, delivs)
	}
	h.enqueueWake = func(context.Context, WakeArgs) error { return errors.New("db") }
	if err := (&tickWorker{h: h}).Work(context.Background(), nil); err == nil {
		t.Fatal("want error")
	}
}

func TestOptionsDefaultsAndStart(t *testing.T) {
	var o Options
	o.defaults()
	if o.Workers != 4 || o.RunTimeout != 30*time.Minute || o.PeriodicInterval != time.Minute || o.MemorySweepInterval != 5*time.Minute ||
		o.PublicURL == "" || o.Logger == nil {
		t.Fatalf("%+v", o)
	}
	w := &executeRunWorker{h: &Heartbeat{opts: o}}
	if w.Timeout(nil) != 32*time.Minute {
		t.Errorf("timeout=%v", w.Timeout(nil))
	}
}

func TestArgsKinds(t *testing.T) {
	if (WakeArgs{}).Kind() != "wake" || (ExecuteRunArgs{}).Kind() != "execute_run" || (DeliverMemoryArgs{}).Kind() != "deliver_memory" ||
		(HeartbeatTickArgs{}).Kind() != "heartbeat_tick" || (MemorySweepArgs{}).Kind() != "memory_sweep" {
		t.Fatal("kinds")
	}
	if (ExecuteRunArgs{}).InsertOpts().Queue != QueueRuns || !(WakeArgs{}).InsertOpts().UniqueOpts.ByArgs {
		t.Fatal("insert opts")
	}
	_ = (HeartbeatTickArgs{}).InsertOpts()
	_ = (MemorySweepArgs{}).InsertOpts()
	_ = (DeliverMemoryArgs{}).InsertOpts()
}
