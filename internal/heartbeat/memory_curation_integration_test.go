//go:build integration

package heartbeat_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/heartbeat"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// curationMemory é o provedor de memória falso da curadoria: grava o que o Hanko pede e pode estar "fora do ar".
type curationMemory struct {
	mu        sync.Mutex
	down      bool
	briefings int
	handoffs  int
	finalized int
	calls     []toolCall
}

type toolCall struct {
	Scope memory.Scope
	Tool  string
	Args  map[string]any
}

func (m *curationMemory) Name() string { return "ai-memory" }
func (m *curationMemory) Search(context.Context, memory.Scope, string, int) ([]memory.Hit, error) {
	return nil, nil
}
func (m *curationMemory) Briefing(context.Context, memory.Scope) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.briefings++
	return "", nil
}
func (m *curationMemory) AcceptHandoff(context.Context, memory.Scope) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handoffs++
	return "", nil
}
func (m *curationMemory) Record(context.Context, memory.Scope, memory.Fact) error { return nil }
func (m *curationMemory) FinalizeSession(context.Context, memory.Scope, string, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalized++
	return nil
}
func (m *curationMemory) AgentMCP(memory.Scope, string, string) map[string]json.RawMessage {
	return nil
}
func (m *curationMemory) Ping(context.Context) error { return nil }
func (m *curationMemory) CallTool(_ context.Context, s memory.Scope, tool string, args map[string]any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, toolCall{Scope: s, Tool: tool, Args: args})
	if m.down {
		return "", errors.New("ai-memory fora do ar")
	}
	return "ok", nil
}

func (m *curationMemory) setDown(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.down = v
}

func (m *curationMemory) toolCalls() []toolCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]toolCall(nil), m.calls...)
}

type memHB struct {
	pool *pgxpool.Pool
	hb   *heartbeat.Heartbeat
	svc  *service.Service
	mem  *curationMemory
	co   domain.Company
	cur  domain.Persona
	ws   domain.Workspace
}

func newMemHB(t *testing.T) *memHB {
	t.Helper()
	url := os.Getenv("HANKO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("HANKO_TEST_DATABASE_URL vazio")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDBFromPool(pool)
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("goose: %v", err)
	}
	if err := heartbeat.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	hb, err := heartbeat.New(pool, heartbeat.Options{Workers: 2, PeriodicInterval: 400 * time.Millisecond, RunTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	svc.MemoryProvider = "ai-memory"
	mem := &curationMemory{}
	hb.Attach(svc, adapter.Registry{"fake": fake.New(fake.Options{Delay: 10 * time.Millisecond})}, mem, broker,
		auth.RunTokens{Secret: []byte("integration-secret-0123456789")})

	m := &memHB{pool: pool, hb: hb, svc: svc, mem: mem}
	m.co, err = svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	m.cur, err = svc.HirePersona(ctx, domain.HumanActor, m.co.ID, service.HirePersonaInput{Name: "Curadora", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	m.ws, err = svc.CreateWorkspace(ctx, domain.HumanActor, m.co.ID, service.CreateWorkspaceInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *memHB) start(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := m.hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = m.hb.Stop(sctx)
	})
}

func (m *memHB) enable(t *testing.T) {
	t.Helper()
	on, hours := true, 6
	if _, err := m.svc.UpdateMemorySettings(context.Background(), domain.HumanActor, m.co.ID, service.UpdateMemorySettingsInput{
		Enabled: &on, IntervalHours: &hours, CuratorPersonaID: &m.cur.ID}); err != nil {
		t.Fatal(err)
	}
}

// O tick periódico dispara a curadoria devida: task de sistema, run do curador com gatilho memory pela
// máquina normal (custo no ledger), sem handoff nem fechamento de sessão, e a consolidação fecha sozinha.
func TestMemoryTickFiresConsolidationThatRuns(t *testing.T) {
	ctx := context.Background()
	m := newMemHB(t)
	m.enable(t)
	if _, err := m.pool.Exec(ctx, "UPDATE memory_settings SET next_fire_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	m.start(t)

	deadline := time.Now().Add(40 * time.Second)
	var h service.MemoryConsolidationView
	for {
		hist, err := m.svc.ListMemoryConsolidations(ctx, m.co.ID, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) > 0 && hist[0].Status != domain.ConsolidationOpen {
			h = hist[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a consolidação não terminou: %+v", hist)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if h.Status != domain.ConsolidationDone || h.Origin != domain.MemoryScheduled || h.WorkspaceSlug != "site" || h.RunID == nil {
		t.Fatalf("consolidação: %+v", h)
	}
	run, err := m.svc.GetRun(ctx, m.co.ID, *h.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger != domain.TriggerMemory || run.Status != domain.RunDone || run.PersonaID != m.cur.ID {
		t.Fatalf("run: trigger=%s status=%s", run.Trigger, run.Status)
	}
	if run.CostUSD == nil || run.CostSource != domain.CostReported {
		t.Fatalf("a curadoria passa pelo ledger: %v %s", run.CostUSD, run.CostSource)
	}
	m.mem.mu.Lock()
	defer m.mem.mu.Unlock()
	if m.mem.briefings == 0 {
		t.Error("a curadoria recebe o briefing da memória")
	}
	if m.mem.handoffs != 0 || m.mem.finalized != 0 {
		t.Errorf("curadoria não aceita handoff nem fecha sessão: handoffs=%d finalized=%d", m.mem.handoffs, m.mem.finalized)
	}
	// a agenda andou: não dispara de novo no mesmo intervalo
	st, _ := m.svc.GetMemoryStatus(ctx, m.co.ID)
	if st.Settings.NextFireAt == nil || !st.Settings.NextFireAt.After(time.Now().Add(5*time.Hour)) {
		t.Errorf("próxima rodada = %v", st.Settings.NextFireAt)
	}
}

// Proposta aprovada com o ai-memory fora do ar fica falha (com o erro e a próxima tentativa), nada se perde;
// quando o ai-memory volta, o job aplica a mesma chamada aprovada, com o escopo do workspace da proposta.
func TestApplyMemoryProposalRetriesUntilProviderIsBack(t *testing.T) {
	ctx := context.Background()
	m := newMemHB(t)
	m.enable(t)
	m.mem.setDown(true)

	cs, err := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	if err != nil || len(cs) != 1 {
		t.Fatalf("consolidar: %v %v", cs, err)
	}
	// run de curadoria aberta à mão (o heartbeat ainda não subiu): registra a proposta e aprova
	run, err := m.svc.CheckoutRun(ctx, service.Wake{CompanyID: m.co.ID, PersonaID: m.cur.ID, TaskID: *cs[0].TaskID, Trigger: domain.TriggerMemory})
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := m.svc.RecordMemoryProposal(ctx, m.co.ID, service.RecordProposalInput{RunID: run.ID, Tool: "memory_delete_page",
		Arguments: map[string]any{"path": "wiki/velha", "workspace": "acme", "project": "site"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.svc.DecideMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "pode apagar"})
	if err != nil || got.Status != domain.ProposalApproved {
		t.Fatalf("aprovar: %+v %v", got, err)
	}
	m.start(t)

	waitProposal := func(want domain.MemoryProposalStatus) service.MemoryProposalView {
		t.Helper()
		deadline := time.Now().Add(40 * time.Second)
		for {
			v, err := m.svc.GetMemoryProposal(ctx, m.co.ID, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if v.Status == want {
				return v
			}
			if time.Now().After(deadline) {
				t.Fatalf("proposta ficou %s; esperava %s (%+v)", v.Status, want, v)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	failed := waitProposal(domain.ProposalFailed)
	if failed.Attempts < 1 || failed.LastError == "" || failed.NextAttemptAt == nil {
		t.Fatalf("falha sem rastro: %+v", failed)
	}

	// ai-memory volta; puxa o job adiado para agora (a espera de produção é de minutos)
	m.mem.setDown(false)
	if _, err := m.pool.Exec(ctx, "UPDATE memory_proposals SET next_attempt_at = now() WHERE id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	// o job só vira "scheduled" depois do snooze, que pode chegar um instante após a proposta marcada como falha:
	// repete o puxão até a proposta ser aplicada
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			_, _ = m.pool.Exec(ctx, "UPDATE river_job SET scheduled_at = now() WHERE kind = 'apply_memory_proposal' AND state IN ('scheduled','retryable')")
			select {
			case <-stop:
				return
			case <-time.After(300 * time.Millisecond):
			}
		}
	}()
	applied := waitProposal(domain.ProposalApplied)
	if applied.AppliedAt == nil || applied.LastError != "" {
		t.Fatalf("aplicada: %+v", applied)
	}
	calls := m.mem.toolCalls()
	last := calls[len(calls)-1]
	if last.Tool != "memory_delete_page" || last.Args["path"] != "wiki/velha" || last.Scope.Workspace != "acme" || last.Scope.Project != "site" {
		t.Fatalf("a chamada aplicada não é a aprovada: %+v", last)
	}
}
