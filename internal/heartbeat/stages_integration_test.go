//go:build integration

package heartbeat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/mcp"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// Demo do M20 (ADR-0048), ponta a ponta com um harness de mentira: uma task atravessa triagem → dev → review → QA sozinha,
// cada persona concluindo a própria etapa pela tool MCP hanko_stage_complete. O pipeline só para nos gates (a aprovação do
// review) e a decisão humana volta como aprendizado no briefing da run seguinte.

// stageAgent é o harness de mentira da demo: chama hanko_stage_complete no MCP do Hanko, com o token da run, como faria um
// agente de verdade ao terminar o trabalho da etapa, e guarda o texto da resposta de cada run.
type stageAgent struct {
	mu      sync.Mutex
	specs   []adapter.Spec
	replies map[string]string
}

func (a *stageAgent) Name() string { return "fake" }

func (a *stageAgent) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	a.mu.Lock()
	a.specs = append(a.specs, spec)
	if a.replies == nil {
		a.replies = map[string]string{}
	}
	a.mu.Unlock()
	var mcpCfg struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(spec.MCPConfig, &mcpCfg); err != nil {
		return adapter.Result{IsError: true, Error: err.Error()}, err
	}
	srv := mcpCfg.Servers["hanko"]
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "hanko_stage_complete", "arguments": map[string]any{"note": "entregue por " + spec.TaskKey}}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range srv.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return adapter.Result{IsError: true, Error: err.Error()}, err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	a.mu.Lock()
	a.replies[spec.RunID.String()] = string(raw)
	a.mu.Unlock()
	_ = sink.Emit(ctx, adapter.Event{Kind: domain.EventToolResult, Text: string(raw), OccurredAt: time.Now().UTC()})
	return adapter.Result{Summary: "etapa concluída", Model: "fake", SessionRef: "fake-" + spec.RunID.String()}, nil
}

func TestStagesPipelineEndToEndThroughRealRuns(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	srv.Listener.Close()
	srv.Listener = ln
	broker := events.NewBroker()
	hb, err := New(pool, Options{Workers: 4, PeriodicInterval: time.Hour, RunTimeout: time.Minute, WakeSnooze: 100 * time.Millisecond,
		PublicURL: "http://" + ln.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	agent := &stageAgent{}
	hb.Attach(svc, adapter.Registry{"fake": agent}, memory.Noop{}, broker, authForTest())
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.Handler(mcp.Deps{Svc: svc, Tokens: authForTest(), Version: "test"}))
	srv.Config.Handler = mux
	srv.Start()
	t.Cleanup(srv.Close)
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

	hire := func(slug, name string) domain.Persona {
		p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: name, Slug: slug, Agent: "fake"})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	ana, bruno, carla, davi := hire("ana", "Ana"), hire("bruno", "Bruno"), hire("carla", "Carla"), hire("davi", "Davi")
	pr, err := svc.CreateProject(ctx, domain.HumanActor, co.ID, service.CreateProjectInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetProjectStages(ctx, domain.HumanActor, co.ID, pr.Slug, []domain.StageSpec{
		{Key: "triagem", Name: "Triagem", Status: domain.TaskTodo, Persona: ana.Slug},
		{Key: "dev", Name: "Desenvolvimento", Status: domain.TaskInProgress, Persona: bruno.Slug},
		{Key: "review", Name: "Review", Status: domain.TaskInProgress, Persona: carla.Slug, ExitGate: domain.StageGateApproval},
		{Key: "qa", Name: "QA", Status: domain.TaskInProgress, Persona: davi.Slug},
	}); err != nil {
		t.Fatal(err)
	}

	task, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "Página de preços", ProjectID: &pr.ID})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if !t.Failed() {
			return
		}
		if rs, err := svc.ListRuns(ctx, co.ID, service.RunFilter{TaskID: &task.ID}); err == nil {
			for _, r := range rs {
				t.Logf("run %s %s %s err=%q", r.PersonaName, r.Status, r.Trigger, r.Error)
			}
		}
		if tv, err := svc.GetTask(ctx, co.ID, task.Key); err == nil {
			t.Logf("task %s %s stage=%+v", tv.Status, tv.PersonaName, tv.Stage)
		}
		agent.mu.Lock()
		for id, r := range agent.replies {
			t.Logf("resposta %s: %.300s", id, r)
		}
		agent.mu.Unlock()
	}()

	// a task atravessa triagem → dev → review sozinha e para no gate de aprovação do review
	var gate service.GateView
	waitFor(t, 90*time.Second, "o gate de aprovação do review abrir", func() bool {
		gs, err := svc.ListGates(ctx, co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
		if err != nil || len(gs) != 1 {
			return false
		}
		gate = gs[0]
		return true
	})
	if gate.Kind != domain.GateApproval || !strings.Contains(gate.Title, "Review") {
		t.Fatalf("gate: %+v", gate)
	}
	st, err := svc.TaskStage(ctx, co.ID, task.Key)
	if err != nil || st.Stage.Key != "review" || st.State != domain.TaskStageCompleting {
		t.Fatalf("o pipeline devia estar parado no review: %v %+v", err, st.TaskStage)
	}
	// o humano pede mudanças: a Carla é acordada de novo (gatilho gate) com o aprendizado no briefing
	if _, err := svc.DecideGate(ctx, domain.HumanActor, co.ID, gate.ID, service.DecideGateInput{Decision: domain.GateChangesRequested, Note: "cite o arquivo alterado"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 90*time.Second, "o segundo gate do review abrir", func() bool {
		gs, err := svc.ListGates(ctx, co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
		if err != nil || len(gs) != 1 || gs[0].ID == gate.ID {
			return false
		}
		gate = gs[0]
		return true
	})
	if _, err := svc.DecideGate(ctx, domain.HumanActor, co.ID, gate.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "ok"}); err != nil {
		t.Fatal(err)
	}

	// o QA conclui a última etapa e a task termina
	waitFor(t, 90*time.Second, "a task concluir", func() bool {
		tv, err := svc.GetTask(ctx, co.ID, task.Key)
		return err == nil && tv.Status == domain.TaskDone
	})
	final, err := svc.TaskStage(ctx, co.ID, task.Key)
	if err != nil || final.State != domain.TaskStageFinished {
		t.Fatalf("pipeline final: %v %+v", err, final.TaskStage)
	}
	var kinds []string
	for _, e := range final.Events {
		kinds = append(kinds, string(e.Kind))
	}
	want := "enrolled completed advanced completed advanced completed gate_requested gate_decided returned completed gate_requested gate_decided advanced completed finished"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("histórico:\n got %s\nwant %s", got, want)
	}

	// cada persona trabalhou na própria etapa: Ana 1 run, Bruno 1, Carla 2 (a segunda com o aprendizado), Davi 1
	runs, err := svc.ListRuns(ctx, co.ID, service.RunFilter{TaskID: &task.ID})
	if err != nil {
		t.Fatal(err)
	}
	per := map[string]int{}
	for _, r := range runs {
		per[r.PersonaName]++
	}
	if per["Ana"] != 1 || per["Bruno"] != 1 || per["Carla"] != 2 || per["Davi"] != 1 {
		t.Fatalf("runs por persona: %v", per)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	learned := 0
	for _, sp := range agent.specs {
		if strings.Contains(sp.Prompt, "### Aprendizados desta etapa") && strings.Contains(sp.Prompt, "cite o arquivo alterado") {
			learned++
		}
	}
	if learned != 1 {
		t.Fatalf("só a 2ª run da Carla recebe o aprendizado no briefing: %d", learned)
	}
}
