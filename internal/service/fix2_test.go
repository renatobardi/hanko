package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Correções da revisão (ADR-0030 / ADR-0027): a política de curadoria não depende do gatilho que acordou a run.

// A task de sistema da consolidação sempre roda como curadoria: um wake de heartbeat (rede de segurança) não
// grava outro gatilho, e o service enxerga a run como curadoria mesmo que o gatilho gravado seja outro.
func TestFix2TaskDeSistemaSempreRodaComoCuradoria(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, err := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	if err != nil || len(cs) != 1 {
		t.Fatal(err)
	}
	run, err := m.svc.CheckoutRun(ctx, service.Wake{CompanyID: m.co.ID, PersonaID: m.curator.ID, TaskID: *cs[0].TaskID, Trigger: domain.TriggerHeartbeat})
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger != domain.TriggerMemory {
		t.Fatalf("o gatilho gravado deveria ser memory, é %s", run.Trigger)
	}
	var stored string
	if err := m.pool.QueryRow(ctx, "SELECT trigger FROM runs WHERE id = $1", run.ID).Scan(&stored); err != nil || stored != "memory" {
		t.Fatalf("runs.trigger = %q (%v)", stored, err)
	}
	// e a remoção vira proposta (só run de curadoria propõe)
	p, created, err := m.svc.RecordMemoryProposal(ctx, m.co.ID, service.RecordProposalInput{RunID: run.ID, Tool: "memory_delete_page",
		Arguments: map[string]any{"path": "wiki/a"}})
	if err != nil || !created || p.Kind != domain.ProposalDelete {
		t.Fatalf("proposta: %+v %v", p, err)
	}

	// run antiga, gravada com outro gatilho: o service decide pela task
	if _, err := m.pool.Exec(ctx, "UPDATE runs SET trigger = 'heartbeat' WHERE id = $1", run.ID); err != nil {
		t.Fatal(err)
	}
	rv, err := m.svc.GetRun(ctx, m.co.ID, run.ID)
	if err != nil || rv.Trigger != domain.TriggerMemory {
		t.Fatalf("GetRun deve tratar a run da task de sistema como curadoria: %v %v", rv.Trigger, err)
	}
	rc, err := m.svc.LoadRunContext(ctx, m.co.ID, run.ID)
	if err != nil || rc.Run.Trigger != domain.TriggerMemory || !rc.Task.System {
		t.Fatalf("LoadRunContext: trigger=%v system=%v %v", rc.Run.Trigger, rc.Task.System, err)
	}
}

// Ninguém acorda a task de sistema à mão nem por comentário; a rede de segurança (heartbeat) continua valendo.
func TestFix2TaskDeSistemaNaoAcordaPorComentarioNemManual(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, err := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	if err != nil || len(cs) != 1 {
		t.Fatal(err)
	}
	tv, err := m.svc.GetTask(ctx, m.co.ID, cs[0].TaskID.String())
	if err != nil || !tv.System {
		t.Fatalf("a task deve vir marcada como de sistema: %+v %v", tv, err)
	}
	if other := m.task(t, service.CreateTaskInput{Title: "comum"}); other.System {
		t.Error("task comum não é de sistema")
	}
	m.jobs.reset()
	err = m.svc.WakeTask(ctx, domain.HumanActor, m.co.ID, tv.ID)
	wantErr(t, err, domain.ErrInvalid)
	if _, err := m.svc.AddComment(ctx, domain.HumanActor, m.co.ID, tv.ID, service.AddCommentInput{Body: "ei, acorde"}); err != nil {
		t.Fatal(err)
	}
	if n := m.jobs.wakeCount(); n != 0 {
		t.Fatalf("comentário/manual não podem acordar a task de sistema (wakes=%d)", n)
	}
}

// A aplicação marca o início da tentativa antes de chamar; a repetição sem resolução sabe que houve tentativa.
func TestFix2AplicacaoMarcaTentativaAntesDeChamar(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, _ := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	run := m.startRun(t, cs[0])
	p, _ := m.propose(t, run, "memory_delete_page", map[string]any{"path": "wiki/a"})
	if _, err := m.svc.DecideMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	first, err := m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	if err != nil || first.PriorAttempt {
		t.Fatalf("primeira tentativa: %+v %v", first, err)
	}
	// a chamada "deu certo" mas o registro falhou: o job refaz e precisa saber
	second, err := m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	if err != nil || !second.PriorAttempt {
		t.Fatalf("segunda tentativa deveria saber da anterior: %+v %v", second, err)
	}
	// aplicada limpa a marca e sai do ciclo
	if err := m.svc.MarkMemoryProposalApplied(ctx, m.co.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	wantErr(t, err, domain.ErrConflict)
}

// Uma chamada guardada que não bate com o sha256 aprovado nunca é aplicada: falha de vez, sem chamar.
func TestFix2AplicacaoRecusaChamadaQueNaoBateComOSha(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, _ := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	run := m.startRun(t, cs[0])
	p, _ := m.propose(t, run, "memory_delete_page", map[string]any{"path": "wiki/a"})
	if _, err := m.svc.DecideMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pool.Exec(ctx, `UPDATE memory_proposals SET arguments = '{"path":"wiki/outra"}'::jsonb WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	item, err := m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	wantErr(t, err, domain.ErrConflict)
	if item.Tool != "" {
		t.Fatalf("nada pode ser devolvido para aplicar: %+v", item)
	}
	got, _ := m.svc.GetMemoryProposal(ctx, m.co.ID, p.ID)
	if got.Status != domain.ProposalFailed || got.NextAttemptAt != nil || !strings.Contains(got.LastError, "sha256") {
		t.Fatalf("proposta adulterada: %+v", got)
	}
	// só o retry manual a recoloca, e ela continua barrada pelo sha
	if _, err := m.svc.RetryMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	wantErr(t, err, domain.ErrConflict)
}

// Marcar aplicada/falha de outra empresa não tem efeito (filtro por company_id nas queries).
func TestFix2MarcarPropostaFiltraPorEmpresa(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, _ := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	run := m.startRun(t, cs[0])
	p, _ := m.propose(t, run, "memory_delete_page", map[string]any{"path": "wiki/a"})
	if _, err := m.svc.DecideMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	other, err := m.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	_ = m.svc.MarkMemoryProposalApplied(ctx, other.ID, p.ID)
	if _, err := m.svc.MarkMemoryProposalFailed(ctx, other.ID, p.ID, "x"); err != nil && !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	got, _ := m.svc.GetMemoryProposal(ctx, m.co.ID, p.ID)
	if got.Status != domain.ProposalApproved || got.Attempts != 0 {
		t.Fatalf("outra empresa não pode mexer na proposta: %+v", got)
	}
}

// O retry manual vale na hora: a espera da próxima tentativa não é mais um job adormecido.
func TestFix2RetryManualRecolocaNaFilaNaHora(t *testing.T) {
	ctx := context.Background()
	m := newMemEnv(t, "ai-memory")
	m.enable(t)
	cs, _ := m.svc.ConsolidateMemoryNow(ctx, domain.HumanActor, m.co.ID, "site")
	run := m.startRun(t, cs[0])
	p, _ := m.propose(t, run, "memory_delete_page", map[string]any{"path": "wiki/a"})
	if _, err := m.svc.DecideMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	wait, err := m.svc.MarkMemoryProposalFailed(ctx, m.co.ID, p.ID, "fora do ar")
	if err != nil || wait < time.Minute {
		t.Fatal(wait, err)
	}
	_, err = m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID)
	wantErr(t, err, service.ErrProposalNotDue)
	before := len(m.mj.applied())
	if _, err := m.svc.RetryMemoryProposal(ctx, domain.HumanActor, m.co.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.mj.applied()) != before+1 {
		t.Fatal("o retry deve enfileirar a aplicação")
	}
	if _, err := m.svc.BeginMemoryProposalApply(ctx, m.co.ID, p.ID); err != nil {
		t.Fatalf("depois do retry a tentativa é devida já: %v", err)
	}
}

// Uma run de chat que falhou aparece na Caixa de entrada, com o título da conversa; a próxima mensagem a supera.
func TestFix2RunDeChatQueFalhouApareceNaCaixaDeEntrada(t *testing.T) {
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)
	e.answer(t, run.ID, domain.RunFailed, adapter.Result{IsError: true, Error: "harness caiu", ExitCode: 1})

	in, err := e.svc.Inbox(ctx, e.co.ID)
	if err != nil || len(in.FailedRuns) != 1 {
		t.Fatalf("inbox: %+v %v", in.FailedRuns, err)
	}
	f := in.FailedRuns[0]
	if f.ID != run.ID || f.ConversationID == nil || *f.ConversationID != cv.ID || f.ConversationTitle != cv.Title || f.TaskKey != "" {
		t.Fatalf("falha de chat: %+v", f)
	}
	// responder de novo (nova run da conversa) supera a falha
	if _, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "tenta de novo"); err != nil {
		t.Fatal(err)
	}
	if in, err = e.svc.Inbox(ctx, e.co.ID); err != nil || len(in.FailedRuns) != 0 {
		t.Fatalf("a falha superada some: %+v %v", in.FailedRuns, err)
	}
}
