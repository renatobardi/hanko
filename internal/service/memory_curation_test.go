package service_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// memJobs acrescenta ao fakeJobs a fila de aplicação das propostas (service.ProposalJobs).
type memJobs struct {
	*fakeJobs
	amu     sync.Mutex
	applies []domain.ID
}

func (m *memJobs) ApplyMemoryProposalTx(_ context.Context, _ pgx.Tx, _, proposalID domain.ID) error {
	m.amu.Lock()
	defer m.amu.Unlock()
	m.applies = append(m.applies, proposalID)
	return nil
}

func (m *memJobs) applied() []domain.ID {
	m.amu.Lock()
	defer m.amu.Unlock()
	return append([]domain.ID(nil), m.applies...)
}

type memEnv struct {
	*env
	mj      *memJobs
	pool    *pgxpool.Pool
	curator domain.Persona
	ws      domain.Workspace
	now     time.Time
}

// newMemEnv: empresa com provedor de memória ligado, um workspace e uma persona curadora. O relógio é fixo.
func newMemEnv(t *testing.T, provider string) *memEnv {
	t.Helper()
	pool := testdb.New(t)
	mj := &memJobs{fakeJobs: &fakeJobs{}}
	svc := service.New(pool, mj, events.NewBroker())
	svc.MemoryProvider = provider
	m := &memEnv{env: &env{svc: svc, jobs: mj.fakeJobs, broker: events.NewBroker()}, mj: mj, pool: pool, now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	svc.Now = func() time.Time { return m.now }
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme Corp"})
	if err != nil {
		t.Fatal(err)
	}
	m.co = co
	m.curator = m.persona(t, "Curadora", nil)
	m.ws, err = svc.CreateWorkspace(context.Background(), domain.HumanActor, co.ID, service.CreateWorkspaceInput{Name: "Site", Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *memEnv) enable(t *testing.T) service.MemoryStatus {
	t.Helper()
	on, hours := true, 6
	st, err := m.svc.UpdateMemorySettings(context.Background(), domain.HumanActor, m.co.ID, service.UpdateMemorySettingsInput{
		Enabled: &on, IntervalHours: &hours, CuratorPersonaID: &m.curator.ID})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// startRun pega a task aberta da consolidação e abre a run da curadora (gatilho memory).
func (m *memEnv) startRun(t *testing.T, c domain.MemoryConsolidation) domain.Run {
	t.Helper()
	ctx := context.Background()
	if c.TaskID == nil {
		t.Fatal("consolidação sem task")
	}
	run, err := m.svc.CheckoutRun(ctx, service.Wake{CompanyID: m.co.ID, PersonaID: m.curator.ID, TaskID: *c.TaskID, Trigger: domain.TriggerMemory})
	if err != nil {
		t.Fatalf("CheckoutRun: %v", err)
	}
	if run.Trigger != domain.TriggerMemory {
		t.Fatalf("trigger = %s", run.Trigger)
	}
	if _, err := m.svc.MarkRunStarted(ctx, m.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	return run
}

func (m *memEnv) fireDue(t *testing.T) []domain.MemoryConsolidation {
	t.Helper()
	m.now = m.now.Add(7 * time.Hour) // passa do intervalo de 6h
	due, err := m.svc.DueMemorySettings(context.Background(), m.now)
	if err != nil || len(due) != 1 || due[0].NextFireAt == nil {
		t.Fatalf("DueMemorySettings = %+v %v", due, err)
	}
	cs, err := m.svc.FireMemoryConsolidation(context.Background(), m.co.ID, *due[0].NextFireAt)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func (m *memEnv) propose(t *testing.T, run domain.Run, tool string, args map[string]any) (service.MemoryProposalView, bool) {
	t.Helper()
	v, created, err := m.svc.RecordMemoryProposal(context.Background(), m.co.ID, service.RecordProposalInput{
		RunID: run.ID, Tool: tool, Arguments: args, Current: "linha 1\nlinha 2\n"})
	if err != nil {
		t.Fatalf("RecordMemoryProposal: %v", err)
	}
	return v, created
}

func TestMemoriaProviderNoneDesligaARecurso(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "none")
	st, err := m.svc.GetMemoryStatus(ctx, m.co.ID)
	if err != nil || st.Available || st.Provider != "none" {
		t.Fatalf("status = %+v %v", st, err)
	}
	on := true
	_, err = m.svc.UpdateMemorySettings(ctx, domain.HumanActor, m.co.ID, service.UpdateMemorySettingsInput{Enabled: &on, CuratorPersonaID: &m.curator.ID})
	wantErr(t, err, domain.ErrInvalid)
	if err == nil || !strings.Contains(err.Error(), "HANKO_MEMORY_PROVIDER=none") {
		t.Errorf("a mensagem deve dizer por que está desligada: %v", err)
	}
	_, err = m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "")
	wantErr(t, err, domain.ErrInvalid)
	if cs, err := m.svc.FireMemoryConsolidation(ctx, m.co.ID, m.now); err != nil || len(cs) != 0 {
		t.Errorf("disparo sem provedor deveria ser no-op: %v %v", cs, err)
	}
}

func TestMemoriaConfiguracao(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")

	st, err := m.svc.GetMemoryStatus(ctx, m.co.ID)
	if err != nil || !st.Available || st.Settings.Enabled || st.Settings.IntervalHours != domain.MemoryDefaultIntervalHours {
		t.Fatalf("padrão: %+v %v", st, err)
	}
	// ligar sem curador não vale
	on := true
	_, err = m.svc.UpdateMemorySettings(ctx, domain.HumanActor, m.co.ID, service.UpdateMemorySettingsInput{Enabled: &on})
	wantErr(t, err, domain.ErrInvalid)
	// intervalo fora dos limites
	bad := 0
	_, err = m.svc.UpdateMemorySettings(ctx, domain.HumanActor, m.co.ID, service.UpdateMemorySettingsInput{IntervalHours: &bad})
	wantErr(t, err, domain.ErrInvalid)
	// curador de outra empresa não vale
	other := domain.NewID()
	_, err = m.svc.UpdateMemorySettings(ctx, domain.HumanActor, m.co.ID, service.UpdateMemorySettingsInput{CuratorPersonaID: &other})
	wantErr(t, err, domain.ErrInvalid)

	st = m.enable(t)
	if !st.Settings.Enabled || st.Settings.IntervalHours != 6 || st.CuratorSlug != m.curator.Slug {
		t.Fatalf("configurada: %+v", st)
	}
	if st.Settings.NextFireAt == nil || !st.Settings.NextFireAt.Equal(m.now.Add(6*time.Hour)) {
		t.Fatalf("próxima rodada = %v", st.Settings.NextFireAt)
	}
	// desligar zera a agenda
	off := false
	st, err = m.svc.UpdateMemorySettings(ctx, domain.HumanActor, m.co.ID, service.UpdateMemorySettingsInput{Enabled: &off})
	if err != nil || st.Settings.Enabled || st.Settings.NextFireAt != nil {
		t.Fatalf("desligada: %+v %v", st, err)
	}
}

func TestMemoriaDisparoAgendadoCriaTaskEFechaComARun(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)

	if due, _ := m.svc.DueMemorySettings(ctx, m.now); len(due) != 0 {
		t.Fatalf("ainda não venceu: %+v", due)
	}
	cs := m.fireDue(t)
	if len(cs) != 1 || cs[0].WorkspaceSlug != "site" || cs[0].Status != domain.ConsolidationOpen || cs[0].Origin != domain.MemoryScheduled {
		t.Fatalf("consolidações = %+v", cs)
	}
	// a agenda andou: nada devido agora
	if due, _ := m.svc.DueMemorySettings(ctx, m.now); len(due) != 0 {
		t.Fatalf("a agenda deveria avançar: %+v", due)
	}
	tv, err := m.svc.GetTask(ctx, m.co.ID, cs[0].TaskID.String())
	if err != nil || tv.WorkspaceID == nil || *tv.WorkspaceID != m.ws.ID || tv.PersonaID == nil || *tv.PersonaID != m.curator.ID {
		t.Fatalf("task da consolidação: %+v %v", tv, err)
	}
	// mesma rodada de novo (job repetido): idempotente
	again, err := m.svc.FireMemoryConsolidation(ctx, m.co.ID, m.now.Add(-time.Hour))
	if err != nil || len(again) != 0 {
		t.Fatalf("repetir o disparo não pode criar outra: %+v %v", again, err)
	}

	run := m.startRun(t, cs[0])
	if _, err := m.svc.FinishRun(ctx, m.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone,
		Result: adapter.Result{Summary: "2 páginas fundidas."}}); err != nil {
		t.Fatal(err)
	}
	hist, err := m.svc.ListMemoryConsolidations(ctx, m.co.ID, 10)
	if err != nil || len(hist) != 1 {
		t.Fatalf("histórico: %+v %v", hist, err)
	}
	if h := hist[0]; h.Status != domain.ConsolidationDone || !strings.Contains(h.Summary, "fundidas") || h.RunStatus != string(domain.RunDone) {
		t.Fatalf("consolidação ao fim da run: %+v", h)
	}
	closed, _ := m.svc.GetTask(ctx, m.co.ID, cs[0].TaskID.String())
	if closed.Status == domain.TaskTodo || closed.Status == domain.TaskInProgress {
		t.Errorf("a task de sistema deveria ser encerrada: %s", closed.Status)
	}
}

func TestMemoriaNaoEmpilhaCuradoria(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	first, err := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	if err != nil || len(first) != 1 || first[0].Origin != domain.MemoryManual {
		t.Fatalf("manual: %+v %v", first, err)
	}
	// já há uma aberta nesse workspace
	_, err = m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	wantErr(t, err, domain.ErrConflict)
	// a agenda também pula o workspace ocupado, e mesmo assim anda
	if cs := m.fireDue(t); len(cs) != 0 {
		t.Fatalf("workspace ocupado não ganha outra rodada: %+v", cs)
	}
	_, err = m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "nao-existe")
	wantErr(t, err, domain.ErrInvalid)
}

func TestMemoriaCuradorIndisponivelPulaSemTravar(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	paused := domain.PersonaPaused
	if _, err := m.svc.UpdatePersona(ctx, domain.HumanActor, m.co.ID, m.curator.ID, service.UpdatePersonaInput{Status: &paused}); err != nil {
		t.Fatal(err)
	}
	if cs := m.fireDue(t); len(cs) != 0 {
		t.Fatalf("curador pausado não gera rodada: %+v", cs)
	}
	if due, _ := m.svc.DueMemorySettings(ctx, m.now); len(due) != 0 {
		t.Fatal("a agenda deve avançar mesmo pulando")
	}
}

func TestMemoriaRodadaQueNuncaComecouExpira(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, err := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	if err != nil || len(cs) != 1 {
		t.Fatal(err)
	}
	if n, err := m.svc.ResolveStaleConsolidations(ctx); err != nil || n != 0 {
		t.Fatalf("ainda é cedo: %d %v", n, err)
	}
	m.now = m.now.Add(7 * time.Hour)
	if n, err := m.svc.ResolveStaleConsolidations(ctx); err != nil || n != 1 {
		t.Fatalf("deveria expirar: %d %v", n, err)
	}
	hist, _ := m.svc.ListMemoryConsolidations(ctx, m.co.ID, 5)
	if len(hist) != 1 || hist[0].Status != domain.ConsolidationExpired {
		t.Fatalf("histórico: %+v", hist)
	}
	// liberou o workspace para uma nova rodada
	if _, err := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site"); err != nil {
		t.Fatalf("nova rodada depois de expirar: %v", err)
	}
}

func TestMemoriaPropostaViraGateEAprovacaoEnfileiraAplicacao(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, _ := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	run := m.startRun(t, cs[0])

	args := map[string]any{"path": "wiki/a", "workspace": "acme-corp", "project": "site"}
	p, created := m.propose(t, run, "memory_delete_page", args)
	if !created || p.Status != domain.ProposalPending || p.Kind != domain.ProposalDelete || p.GateID == nil || len(p.SHA256) != 64 {
		t.Fatalf("proposta: %+v", p)
	}
	if p.WorkspaceSlug != "site" || len(p.Targets) != 1 || p.Targets[0] != "wiki/a" || !strings.Contains(p.Diff, "-linha 1") {
		t.Fatalf("alvo/diff: %+v", p)
	}
	// repetir a mesma chamada na mesma run não duplica
	p2, created2 := m.propose(t, run, "memory_delete_page", args)
	if created2 || p2.ID != p.ID {
		t.Fatalf("deveria devolver a existente: %+v created=%v", p2, created2)
	}

	g, err := m.svc.GetGate(ctx, m.co.ID, *p.GateID)
	if err != nil || g.Kind != domain.GateMemory || g.Status != domain.GatePending || g.TaskID != nil {
		t.Fatalf("gate: %+v %v", g, err)
	}
	if !strings.Contains(g.Body, "wiki/a") || !strings.Contains(g.Body, p.SHA256) {
		t.Errorf("o gate deve mostrar o alvo e o sha256:\n%s", g.Body)
	}
	if st, _ := m.svc.GetMemoryStatus(ctx, m.co.ID); st.PendingProposals != 1 {
		t.Errorf("pendentes = %d", st.PendingProposals)
	}
	if len(m.mj.applied()) != 0 {
		t.Fatal("nada é aplicado antes da decisão")
	}

	// aprovar pelo gate (a mesma decisão do Inbox) enfileira a aplicação
	if _, err := m.svc.DecideGate(ctx, domain.HumanActor, m.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "ok"}); err != nil {
		t.Fatal(err)
	}
	got, _ := m.svc.GetMemoryProposal(ctx, m.co.ID, p.ID)
	if got.Status != domain.ProposalApproved || got.DecidedBy == nil || got.DecisionNote != "ok" {
		t.Fatalf("depois de aprovar: %+v", got)
	}
	if ap := m.mj.applied(); len(ap) != 1 || ap[0] != p.ID {
		t.Fatalf("aplicação enfileirada: %v", ap)
	}

	// o job: carrega, falha (ai-memory fora), tenta de novo e aplica
	item, err := m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	if err != nil || item.Tool != "memory_delete_page" || item.CompanySlug != m.co.Slug || item.Arguments["path"] != "wiki/a" {
		t.Fatalf("carregar para aplicar: %+v %v", item, err)
	}
	wait, err := m.svc.MarkMemoryProposalFailed(ctx, m.co.ID, p.ID, "ai-memory fora do ar")
	if err != nil || wait <= 0 {
		t.Fatalf("falha: %v %v", wait, err)
	}
	got, _ = m.svc.GetMemoryProposal(ctx, m.co.ID, p.ID)
	if got.Status != domain.ProposalFailed || got.Attempts != 1 || !strings.Contains(got.LastError, "fora do ar") || got.NextAttemptAt == nil {
		t.Fatalf("proposta falha: %+v", got)
	}
	// nada se perde: a falha segue aplicável, mas só quando a próxima tentativa chega (o job não espera dentro dele)
	_, err = m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	wantErr(t, err, service.ErrProposalNotDue)
	m.now = m.now.Add(wait + time.Second)
	if item, err := m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID); err != nil || item.PriorAttempt {
		t.Fatalf("falha ainda é aplicável (falha registrada não é tentativa pendente): %+v %v", item, err)
	}
	if due, err := m.svc.DueMemoryProposals(ctx, 10); err != nil || len(due) != 1 || due[0].ID != p.ID {
		t.Fatalf("a varredura deve achar a devida: %+v %v", due, err)
	}
	if _, err := m.svc.RetryMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.svc.MarkMemoryProposalApplied(ctx, m.co.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = m.svc.GetMemoryProposal(ctx, m.co.ID, p.ID)
	if got.Status != domain.ProposalApplied || got.AppliedAt == nil || got.Attempts != 2 {
		t.Fatalf("aplicada: %+v", got)
	}
	// aplicada não volta para a fila
	_, err = m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	wantErr(t, err, domain.ErrConflict)
	_, err = m.svc.RetryMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID)
	wantErr(t, err, domain.ErrConflict)
}

func TestMemoriaPropostaRejeitadaNaoAplica(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, _ := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	run := m.startRun(t, cs[0])
	p, _ := m.propose(t, run, "memory_merge_pages", map[string]any{"paths": []any{"a", "b"}, "into": "c", "body": "texto fundido"})
	if p.Kind != domain.ProposalMerge {
		t.Fatalf("tipo = %s", p.Kind)
	}
	got, err := m.svc.DecideMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID, service.DecideGateInput{Decision: domain.GateRejected, Note: "não"})
	if err != nil || got.Status != domain.ProposalRejected {
		t.Fatalf("rejeitar: %+v %v", got, err)
	}
	if len(m.mj.applied()) != 0 {
		t.Fatal("rejeitada não é aplicada")
	}
	_, err = m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	wantErr(t, err, domain.ErrConflict)
	// decidir de novo não vale
	_, err = m.svc.DecideMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID, service.DecideGateInput{Decision: domain.GateApproved})
	if err == nil {
		t.Fatal("decidir duas vezes deveria falhar")
	}
}

func TestMemoriaSoRunDeCuradoriaPropoe(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	tv := m.task(t, service.CreateTaskInput{Title: "trabalho comum", PersonaID: &m.curator.ID})
	run, err := m.svc.CheckoutRun(ctx, service.Wake{CompanyID: m.co.ID, PersonaID: m.curator.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = m.svc.RecordMemoryProposal(ctx, m.co.ID, service.RecordProposalInput{RunID: run.ID, Tool: "memory_delete_page",
		Arguments: map[string]any{"path": "x"}})
	wantErr(t, err, domain.ErrNotAllowed)
}

func TestMemoriaPropostaDeOutraEmpresaNaoApareceNemDecide(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, _ := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	run := m.startRun(t, cs[0])
	p, _ := m.propose(t, run, "memory_delete_page", map[string]any{"path": "wiki/a"})

	other, err := m.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.svc.GetMemoryProposal(ctx, other.ID, p.ID); err == nil {
		t.Fatal("proposta não pode vazar para outra empresa")
	}
	if ps, err := m.svc.ListMemoryProposals(ctx, other.ID, nil, 10); err != nil || len(ps) != 0 {
		t.Fatalf("lista da outra empresa: %+v %v", ps, err)
	}
	if _, err := m.svc.DecideMemoryProposal(ctx, domain.HumanActor, other.ID, p.ID, service.DecideGateInput{Decision: domain.GateApproved}); err == nil {
		t.Fatal("decisão cruzada deveria falhar")
	}
	if _, err := m.svc.BeginMemoryProposalApply(ctx, other.ID, p.ID); err == nil {
		t.Fatal("aplicação cruzada deveria falhar")
	}
}

func TestMemoriaPermissoesPorWorkspace(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	other := m.persona(t, "Outra", nil)

	if a, err := m.svc.MemoryAccessTo(ctx, m.co.ID, other.ID, "site"); err != nil || a != "" {
		t.Fatalf("sem concessão: %q %v", a, err)
	}
	g, err := m.svc.GrantMemoryAccess(ctx, domain.HumanActor, m.co.ID, other.ID, m.ws.ID, domain.MemoryRead)
	if err != nil || g.WorkspaceSlug != "site" || g.PersonaName != "Outra" {
		t.Fatalf("conceder: %+v %v", g, err)
	}
	if a, _ := m.svc.MemoryAccessTo(ctx, m.co.ID, other.ID, "site"); a != domain.MemoryRead {
		t.Fatalf("acesso = %q", a)
	}
	// outra persona e outro workspace continuam sem acesso
	if a, _ := m.svc.MemoryAccessTo(ctx, m.co.ID, m.curator.ID, "site"); a != "" {
		t.Fatalf("a concessão é por persona: %q", a)
	}
	if a, _ := m.svc.MemoryAccessTo(ctx, m.co.ID, other.ID, "inexistente"); a != "" {
		t.Fatalf("workspace inexistente: %q", a)
	}
	// promover para escrita e listar
	if _, err := m.svc.GrantMemoryAccess(ctx, domain.HumanActor, m.co.ID, other.ID, m.ws.ID, domain.MemoryWrite); err != nil {
		t.Fatal(err)
	}
	gs, err := m.svc.ListMemoryGrants(ctx, m.co.ID)
	if err != nil || len(gs) != 1 || gs[0].Access != domain.MemoryWrite {
		t.Fatalf("lista: %+v %v", gs, err)
	}
	// valor inválido e persona de outra empresa
	if _, err := m.svc.GrantMemoryAccess(ctx, domain.HumanActor, m.co.ID, other.ID, m.ws.ID, "admin"); err == nil {
		t.Fatal("acesso inválido deveria falhar")
	}
	oc, _ := m.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra Co"})
	if _, err := m.svc.GrantMemoryAccess(ctx, domain.HumanActor, oc.ID, other.ID, m.ws.ID, domain.MemoryRead); err == nil {
		t.Fatal("conceder entre empresas deveria falhar")
	}
	if a, _ := m.svc.MemoryAccessTo(ctx, oc.ID, other.ID, "site"); a != "" {
		t.Fatalf("a concessão não atravessa empresas: %q", a)
	}
	if err := m.svc.RevokeMemoryAccess(ctx, domain.HumanActor, m.co.ID, other.ID, m.ws.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, m.svc.RevokeMemoryAccess(ctx, domain.HumanActor, m.co.ID, other.ID, m.ws.ID), domain.ErrNotFound)
	if a, _ := m.svc.MemoryAccessTo(ctx, m.co.ID, other.ID, "site"); a != "" {
		t.Fatalf("depois de revogar: %q", a)
	}
}
