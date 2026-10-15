package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type reviewEnv struct {
	*env
	author, reviewer domain.Persona
	task             service.TaskView
	authorRun        domain.Run
}

// newReviewEnv: autora com require_review e revisor (persona própria ou harness alternativo), com a
// run da autora ativa e a task pronta para ser dada como concluída.
func newReviewEnv(t *testing.T, mod func(*service.HirePersonaInput, *env) domain.Persona) *reviewEnv {
	t.Helper()
	e := newEnv(t)
	re := &reviewEnv{env: e}
	in := service.HirePersonaInput{Name: "Autora", Agent: "claude", Model: "m-autora", RequireReview: true}
	re.reviewer = mod(&in, e)
	var err error
	re.author, err = e.svc.HirePersona(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("hire autora: %v", err)
	}
	re.task = e.task(t, service.CreateTaskInput{Title: "Entregar relatório", Description: "Escrever o relatório.", PersonaID: &re.author.ID})
	re.authorRun, err = e.svc.CheckoutRun(context.Background(), wakeOf(re.author, re.task, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(context.Background(), e.co.ID, re.authorRun.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	return re
}

func withReviewerPersona(t *testing.T) func(*service.HirePersonaInput, *env) domain.Persona {
	return func(in *service.HirePersonaInput, e *env) domain.Persona {
		rev := e.persona(t, "Revisor", func(r *service.HirePersonaInput) { r.Agent = "codex"; r.Model = "m-revisor" })
		in.ReviewerPersona = &rev.ID
		return rev
	}
}

// close: a autora (persona) dá a task como pronta durante a própria run.
func (r *reviewEnv) closeTask(t *testing.T) {
	t.Helper()
	done := domain.TaskDone
	if _, err := r.svc.UpdateTask(context.Background(), domain.PersonaActor(r.author.ID), r.co.ID, r.task.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
}

func (r *reviewEnv) finishAuthor(t *testing.T) {
	t.Helper()
	if _, err := r.svc.FinishRun(context.Background(), r.co.ID, r.authorRun.ID, service.FinishRunInput{Status: domain.RunDone,
		Result: adapter.Result{Summary: "Relatório pronto."}}); err != nil {
		t.Fatal(err)
	}
}

func (r *reviewEnv) pendingGates(t *testing.T) []service.GateView {
	t.Helper()
	gs, err := r.svc.ListGates(context.Background(), r.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if err != nil {
		t.Fatal(err)
	}
	return gs
}

// startReview roda o job: devolve a run de revisão aberta.
func (r *reviewEnv) startReview(t *testing.T) service.RunView {
	t.Helper()
	if len(r.jobs.reviews) == 0 {
		t.Fatal("nenhum start_review enfileirado")
	}
	if err := r.svc.StartReview(context.Background(), r.co.ID, r.jobs.reviews[len(r.jobs.reviews)-1]); err != nil {
		t.Fatalf("StartReview: %v", err)
	}
	runs, err := r.svc.ListRuns(context.Background(), r.co.ID, service.RunFilter{TaskID: &r.task.ID, Statuses: []domain.RunStatus{domain.RunQueued}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("run de revisão: %+v %v", runs, err)
	}
	return runs[0]
}

func (r *reviewEnv) finishReview(t *testing.T, run service.RunView, status domain.RunStatus, summary string) {
	t.Helper()
	ctx := context.Background()
	if _, err := r.svc.MarkRunStarted(ctx, r.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.FinishRun(ctx, r.co.ID, run.ID, service.FinishRunInput{Status: status, Result: adapter.Result{Summary: summary, Error: "falhou"}}); err != nil {
		t.Fatal(err)
	}
}

// Fluxo completo com persona revisora: a revisão espera a run da autora, o revisor reprova e o gate
// nasce já marcado como reprovado, com justificativa e sha256 do artefato; a decisão segue humana.
func TestReviewReprovadaMarcaOGate(t *testing.T) {
	ctx := context.Background()
	r := newReviewEnv(t, withReviewerPersona(t))
	r.closeTask(t)

	if gs := r.pendingGates(t); len(gs) != 0 {
		t.Fatalf("o gate só nasce depois da revisão: %+v", gs)
	}
	tv, _ := r.svc.GetTask(ctx, r.co.ID, r.task.ID.String())
	if tv.Status != domain.TaskInReview || len(r.jobs.reviews) != 1 {
		t.Fatalf("task=%s jobs=%d", tv.Status, len(r.jobs.reviews))
	}
	// run da autora ainda ativa: a revisão espera
	wantErr(t, r.svc.StartReview(ctx, r.co.ID, r.jobs.reviews[0]), domain.ErrActiveRun)
	// fechar de novo não duplica a revisão
	r.closeTask(t)
	if len(r.jobs.reviews) != 1 {
		t.Fatalf("revisão duplicada: %d", len(r.jobs.reviews))
	}
	r.finishAuthor(t)

	run := r.startReview(t)
	if run.Trigger != domain.TriggerReview || run.PersonaID != r.reviewer.ID || run.Agent != "codex" || run.Model != "m-revisor" || run.Name == "" {
		t.Fatalf("run de revisão: %+v", run.Run)
	}
	if err := r.svc.StartReview(ctx, r.co.ID, r.jobs.reviews[0]); err != nil { // idempotente: já está running
		t.Fatal(err)
	}
	rc, err := r.svc.LoadRunContext(ctx, r.co.ID, run.ID)
	if err != nil || rc.Review == nil || !rc.Review.Independent || !strings.Contains(rc.Review.Artifact, "Relatório pronto.") ||
		!strings.Contains(rc.Review.Artifact, "Escrever o relatório.") {
		t.Fatalf("contexto da revisão: %+v %v", rc.Review, err)
	}
	sum := sha256.Sum256([]byte(rc.Review.Artifact))
	if rc.Review.ArtifactSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 do artefato: %s", rc.Review.ArtifactSHA256)
	}

	r.finishReview(t, run, domain.RunDone, "VEREDITO: REPROVADO\nFaltam os números do trimestre.")
	gs := r.pendingGates(t)
	if len(gs) != 1 {
		t.Fatalf("gates pendentes: %+v", gs)
	}
	g := gs[0]
	if g.Kind != domain.GateTaskReview || g.Status != domain.GatePending || g.Review == nil || g.Review.Verdict != domain.ReviewRejected ||
		!strings.Contains(g.Review.Justification, "Faltam os números") || g.Review.ArtifactSHA256 != rc.Review.ArtifactSHA256 ||
		g.Review.RunID == nil || *g.Review.RunID != run.ID || !strings.Contains(g.Review.Reviewer, "Revisor") ||
		g.RequestedStatus != domain.TaskDone || g.PersonaID == nil || *g.PersonaID != r.author.ID {
		t.Fatalf("gate: %+v review=%+v", g.Gate, g.Review)
	}
	if !strings.Contains(g.Body, "REPROVADO pelo revisor") || !strings.Contains(g.Body, rc.Review.ArtifactSHA256) {
		t.Fatalf("o corpo do gate mostra o veredito:\n%s", g.Body)
	}
	// a decisão continua humana: o humano aprova apesar da reprovação
	if _, err := r.svc.DecideGate(ctx, domain.PersonaActor(r.author.ID), r.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err == nil {
		t.Fatal("persona não decide gate")
	}
	dg, err := r.svc.DecideGate(ctx, domain.HumanActor, r.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved})
	if err != nil || dg.Status != domain.GateApproved {
		t.Fatalf("decisão humana: %+v %v", dg, err)
	}
	if tv, _ := r.svc.GetTask(ctx, r.co.ID, r.task.ID.String()); tv.Status != domain.TaskDone {
		t.Fatalf("task = %s", tv.Status)
	}
	// o fato de atividade registra a revisão
	acts, _ := r.svc.ListActivity(ctx, r.co.ID, service.ActivityFilter{SubjectType: "review", Limit: 50})
	verbs := map[string]bool{}
	for _, a := range acts {
		verbs[a.Verb] = true
	}
	for _, v := range []string{"review.requested", "review.started", "review.finished"} {
		if !verbs[v] {
			t.Errorf("faltou activity %s (%v)", v, verbs)
		}
	}
}

func TestReviewAprovada(t *testing.T) {
	r := newReviewEnv(t, withReviewerPersona(t))
	r.closeTask(t)
	r.finishAuthor(t)
	run := r.startReview(t)
	r.finishReview(t, run, domain.RunDone, "veredito: aprovado\nTudo conferido.")
	gs := r.pendingGates(t)
	if len(gs) != 1 || gs[0].Review == nil || gs[0].Review.Verdict != domain.ReviewApproved {
		t.Fatalf("gate: %+v", gs)
	}
}

// Autora claude, revisor codex: o mesmo papel roda em outro harness/modelo.
func TestReviewHarnessAlternativo(t *testing.T) {
	r := newReviewEnv(t, func(in *service.HirePersonaInput, e *env) domain.Persona {
		in.ReviewerAgent, in.ReviewerModel = "codex", "gpt-rev"
		return domain.Persona{}
	})
	r.closeTask(t)
	r.finishAuthor(t)
	run := r.startReview(t)
	if run.PersonaID != r.author.ID || run.Agent != "codex" || run.Model != "gpt-rev" || run.Trigger != domain.TriggerReview {
		t.Fatalf("run: %+v", run.Run)
	}
	rc, _ := r.svc.LoadRunContext(context.Background(), r.co.ID, run.ID)
	if rc.Review == nil || rc.Review.Independent {
		t.Fatalf("revisão do mesmo papel não é independente: %+v", rc.Review)
	}
	r.finishReview(t, run, domain.RunDone, "VEREDITO: APROVADO\nok")
	gs := r.pendingGates(t)
	if len(gs) != 1 || gs[0].Review == nil || !strings.Contains(gs[0].Review.Reviewer, "codex · gpt-rev") {
		t.Fatalf("gate: %+v", gs)
	}
}

// Revisão que não conclui abre o gate sem veredito, com o motivo: nunca deixa a task sem gate.
func TestReviewSemVeredito(t *testing.T) {
	for name, tc := range map[string]struct {
		status  domain.RunStatus
		summary string
		want    string
	}{
		"falha":        {domain.RunFailed, "", "não concluiu"},
		"expirada":     {domain.RunExpired, "", "expired"},
		"sem veredito": {domain.RunDone, "Parece bom, mas não sei.", "veredito válido"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newReviewEnv(t, withReviewerPersona(t))
			r.closeTask(t)
			r.finishAuthor(t)
			run := r.startReview(t)
			r.finishReview(t, run, tc.status, tc.summary)
			gs := r.pendingGates(t)
			if len(gs) != 1 || gs[0].Review == nil || gs[0].Review.Verdict != "" || !strings.Contains(gs[0].Review.Justification, tc.want) {
				t.Fatalf("gate: %+v review=%+v", gs, gs[0].Review)
			}
		})
	}
}

// Revisor que não pode trabalhar: o gate abre sem veredito na hora de iniciar.
func TestReviewRevisorIndisponivel(t *testing.T) {
	ctx := context.Background()
	r := newReviewEnv(t, withReviewerPersona(t))
	paused := domain.PersonaPaused
	if _, err := r.svc.UpdatePersona(ctx, domain.HumanActor, r.co.ID, r.reviewer.ID, service.UpdatePersonaInput{Status: &paused}); err != nil {
		t.Fatal(err)
	}
	r.closeTask(t)
	r.finishAuthor(t)
	if err := r.svc.StartReview(ctx, r.co.ID, r.jobs.reviews[0]); err != nil {
		t.Fatal(err)
	}
	gs := r.pendingGates(t)
	if len(gs) != 1 || gs[0].Review == nil || gs[0].Review.Verdict != "" || !strings.Contains(gs[0].Review.Justification, "não pode trabalhar") {
		t.Fatalf("gate: %+v", gs)
	}
}

// Task fechada/devolvida durante a revisão: a revisão é cancelada e nenhum gate nasce.
func TestReviewCanceladaQuandoATaskSai(t *testing.T) {
	ctx := context.Background()
	r := newReviewEnv(t, withReviewerPersona(t))
	r.closeTask(t)
	r.finishAuthor(t)
	run := r.startReview(t)
	cancelled := domain.TaskCancelled
	if _, err := r.svc.UpdateTask(ctx, domain.HumanActor, r.co.ID, r.task.ID, service.UpdateTaskInput{Status: &cancelled}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.svc.GetRun(ctx, r.co.ID, run.ID)
	if got.Status != domain.RunCancelled {
		t.Fatalf("run de revisão = %s", got.Status)
	}
	if gs := r.pendingGates(t); len(gs) != 0 {
		t.Fatalf("gate indevido: %+v", gs)
	}
	var st string
	if err := r.svc.Pool.QueryRow(ctx, `SELECT status FROM reviews WHERE task_id = $1`, r.task.ID).Scan(&st); err != nil || st != "cancelled" {
		t.Fatalf("review = %q %v", st, err)
	}
}

// Revisão ainda pending (job não rodou) e task fechada: cancela sem gate.
func TestReviewPendenteCanceladaAoFecharATask(t *testing.T) {
	ctx := context.Background()
	r := newReviewEnv(t, withReviewerPersona(t))
	r.closeTask(t)
	cancelled := domain.TaskCancelled
	if _, err := r.svc.UpdateTask(ctx, domain.HumanActor, r.co.ID, r.task.ID, service.UpdateTaskInput{Status: &cancelled}); err != nil {
		t.Fatal(err)
	}
	r.finishAuthor(t)
	if err := r.svc.StartReview(ctx, r.co.ID, r.jobs.reviews[0]); err != nil {
		t.Fatal(err)
	}
	if gs := r.pendingGates(t); len(gs) != 0 {
		t.Fatalf("gate indevido: %+v", gs)
	}
}

// Rede de segurança: run de revisão interrompida (reinício) e revisão pending esquecida abrem o gate sem veredito.
func TestReviewResolveStale(t *testing.T) {
	ctx := context.Background()
	r := newReviewEnv(t, withReviewerPersona(t))
	r.closeTask(t)
	r.finishAuthor(t)
	r.startReview(t)
	if n, err := r.svc.InterruptOrphanRuns(ctx); err != nil || n != 1 {
		t.Fatalf("órfãs = %d %v", n, err)
	}
	gs := r.pendingGates(t)
	if len(gs) != 1 || gs[0].Review == nil || gs[0].Review.Verdict != "" || !strings.Contains(gs[0].Review.Justification, "interrupted") {
		t.Fatalf("gate: %+v", gs)
	}

	// pending esquecida há mais de 2 h
	r2 := newReviewEnv(t, withReviewerPersona(t))
	r2.closeTask(t)
	r2.finishAuthor(t)
	later := r2.svc.Now().Add(3 * time.Hour)
	r2.svc.Now = func() time.Time { return later }
	n, err := r2.svc.ResolveStaleReviews(ctx)
	if err != nil || n != 1 {
		t.Fatalf("stale = %d %v", n, err)
	}
	gs = r2.pendingGates(t)
	if len(gs) != 1 || gs[0].Review == nil || !strings.Contains(gs[0].Review.Justification, "iniciar a tempo") {
		t.Fatalf("gate: %+v", gs)
	}
}

// Sem revisor configurado o comportamento é o de antes: o gate abre direto e nada é enfileirado.
func TestSemRevisorGateDireto(t *testing.T) {
	r := newReviewEnv(t, func(in *service.HirePersonaInput, e *env) domain.Persona { return domain.Persona{} })
	r.closeTask(t)
	gs := r.pendingGates(t)
	if len(gs) != 1 || gs[0].Review != nil || len(r.jobs.reviews) != 0 {
		t.Fatalf("gate: %+v reviews=%d", gs, len(r.jobs.reviews))
	}
}

func TestReviewerConfigValidacao(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a := e.persona(t, "A", nil)
	b := e.persona(t, "B", nil)
	upd := func(in service.UpdatePersonaInput) (domain.Persona, error) {
		return e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, a.ID, in)
	}
	if _, err := upd(service.UpdatePersonaInput{ReviewerPersona: &a.ID}); err == nil {
		t.Error("revisar a si mesma deveria falhar")
	}
	if _, err := upd(service.UpdatePersonaInput{ReviewerAgent: ptr("gpt")}); err == nil {
		t.Error("agente desconhecido deveria falhar")
	}
	if _, err := upd(service.UpdatePersonaInput{ReviewerModel: ptr("m")}); err == nil {
		t.Error("modelo sem agente deveria falhar")
	}
	if _, err := upd(service.UpdatePersonaInput{ReviewerPersona: &b.ID, ClearReviewer: true}); err == nil {
		t.Error("definir e limpar juntos deveria falhar")
	}
	// persona revisora e harness alternativo juntos: erro, como na contratação (antes o harness era ignorado)
	if _, err := upd(service.UpdatePersonaInput{ReviewerPersona: &b.ID, ReviewerAgent: ptr("codex")}); err == nil {
		t.Error("persona e harness alternativo juntos deveria falhar")
	} else {
		wantErr(t, err, domain.ErrInvalid)
	}
	if got, _ := e.svc.GetPersona(ctx, e.co.ID, a.ID.String()); got.HasReviewer() {
		t.Error("o pedido recusado não pode ter gravado revisor")
	}
	p, err := upd(service.UpdatePersonaInput{ReviewerPersona: &b.ID})
	if err != nil || p.ReviewerPersonaID == nil || *p.ReviewerPersonaID != b.ID || !p.HasReviewer() {
		t.Fatalf("persona revisora: %+v %v", p, err)
	}
	// trocar por harness alternativo substitui a persona revisora
	p, err = upd(service.UpdatePersonaInput{ReviewerAgent: ptr("codex"), ReviewerModel: ptr("x")})
	if err != nil || p.ReviewerPersonaID != nil || p.ReviewerAgent != "codex" || p.ReviewerModel != "x" {
		t.Fatalf("harness alternativo: %+v %v", p, err)
	}
	p, err = upd(service.UpdatePersonaInput{ClearReviewer: true})
	if err != nil || p.HasReviewer() {
		t.Fatalf("limpar: %+v %v", p, err)
	}
	// na contratação
	h, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "C", Agent: "claude", ReviewerPersona: &b.ID})
	if err != nil || h.ReviewerPersonaID == nil {
		t.Fatalf("contratar com revisor: %+v %v", h, err)
	}
	if _, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "D", Agent: "claude", ReviewerPersona: &b.ID, ReviewerAgent: "codex"}); err == nil {
		t.Error("os dois juntos deveria falhar")
	}
}

// O parecer do revisor vive no gate: a run de revisão não posta comentário na task.
func TestReviewNaoPostaComentario(t *testing.T) {
	ctx := context.Background()
	r := newReviewEnv(t, withReviewerPersona(t))
	r.closeTask(t)
	r.finishAuthor(t)
	before, _ := r.svc.ListComments(ctx, r.co.ID, r.task.ID)
	run := r.startReview(t)
	r.finishReview(t, run, domain.RunDone, "VEREDITO: APROVADO\nTudo certo.")
	after, _ := r.svc.ListComments(ctx, r.co.ID, r.task.ID)
	if len(after) != len(before) {
		t.Fatalf("revisão postou comentário: %d -> %d", len(before), len(after))
	}
	for _, c := range after {
		if strings.Contains(c.Body, "VEREDITO") {
			t.Fatalf("parecer vazou para a thread: %q", c.Body)
		}
	}
}

// Ordem de locks task → run → review: StartReview e o fim da run de revisão correm contra o
// fechamento da task sem deadlock (o Postgres aborta um deles com 40P01 se a ordem divergir).
func TestReviewLockOrderSemDeadlock(t *testing.T) {
	ctx := context.Background()
	race := func(t *testing.T, a, b func() error) {
		t.Helper()
		errs := make(chan error, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, f := range []func() error{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- f()
			}()
		}
		close(start)
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("travou: possível deadlock")
		}
		close(errs)
		for err := range errs {
			if err != nil && (strings.Contains(err.Error(), "deadlock") || strings.Contains(err.Error(), "40P01")) {
				t.Fatalf("deadlock: %v", err)
			}
		}
	}
	cancelTask := func(r *reviewEnv) func() error {
		return func() error {
			c := domain.TaskCancelled
			_, err := r.svc.UpdateTask(ctx, domain.HumanActor, r.co.ID, r.task.ID, service.UpdateTaskInput{Status: &c})
			return err
		}
	}
	for i := 0; i < 8; i++ {
		t.Run("start", func(t *testing.T) {
			r := newReviewEnv(t, withReviewerPersona(t))
			r.closeTask(t)
			r.finishAuthor(t)
			race(t, func() error { return r.svc.StartReview(ctx, r.co.ID, r.jobs.reviews[0]) }, cancelTask(r))
		})
		t.Run("finish", func(t *testing.T) {
			r := newReviewEnv(t, withReviewerPersona(t))
			r.closeTask(t)
			r.finishAuthor(t)
			run := r.startReview(t)
			if _, err := r.svc.MarkRunStarted(ctx, r.co.ID, run.ID, "tr", ""); err != nil {
				t.Fatal(err)
			}
			race(t, func() error {
				_, err := r.svc.FinishRun(ctx, r.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "VEREDITO: APROVADO\nok"}})
				return err
			}, cancelTask(r))
		})
	}
}

// Teste determinístico da ordem: com a task travada por outra transação, StartReview e FinishRun
// da revisão esperam NA task sem segurar a linha da revisão/run (ordem task → run → review).
func TestReviewLockOrderTaskPrimeiro(t *testing.T) {
	ctx := context.Background()
	hold := func(t *testing.T, r *reviewEnv, f func() error, free string, id domain.ID) {
		t.Helper()
		tx, err := r.svc.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT 1 FROM tasks WHERE id = $1 FOR UPDATE`, r.task.ID); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- f() }()
		time.Sleep(400 * time.Millisecond) // a chamada já bloqueou na task
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sp.Exec(ctx, `SELECT 1 FROM `+free+` WHERE id = $1 FOR UPDATE NOWAIT`, id); err != nil {
			t.Errorf("a chamada segurou %s antes de travar a task: %v", free, err)
		}
		_ = sp.Rollback(ctx)
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("chamada: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("chamada não terminou depois de soltar a task")
		}
	}
	t.Run("StartReview", func(t *testing.T) {
		r := newReviewEnv(t, withReviewerPersona(t))
		r.closeTask(t)
		r.finishAuthor(t)
		hold(t, r, func() error { return r.svc.StartReview(ctx, r.co.ID, r.jobs.reviews[0]) }, "reviews", r.jobs.reviews[0])
	})
	t.Run("FinishRun", func(t *testing.T) {
		r := newReviewEnv(t, withReviewerPersona(t))
		r.closeTask(t)
		r.finishAuthor(t)
		run := r.startReview(t)
		if _, err := r.svc.MarkRunStarted(ctx, r.co.ID, run.ID, "tr", ""); err != nil {
			t.Fatal(err)
		}
		hold(t, r, func() error {
			_, err := r.svc.FinishRun(ctx, r.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "VEREDITO: APROVADO\nok"}})
			return err
		}, "runs", run.ID)
	})
}

// Fechar a task cancela a run de revisão aberta mesmo sem persona atribuída (cancelTaskRun não roda).
func TestReviewFecharCancelaARunDeRevisao(t *testing.T) {
	ctx := context.Background()
	r := newReviewEnv(t, withReviewerPersona(t))
	r.closeTask(t)
	r.finishAuthor(t)
	run := r.startReview(t)
	if _, err := r.svc.Pool.Exec(ctx, `UPDATE tasks SET persona_id = NULL WHERE id = $1`, r.task.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := domain.TaskCancelled
	if _, err := r.svc.UpdateTask(ctx, domain.HumanActor, r.co.ID, r.task.ID, service.UpdateTaskInput{Status: &cancelled}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.svc.GetRun(ctx, r.co.ID, run.ID)
	if got.Status != domain.RunCancelled {
		t.Fatalf("run de revisão = %s", got.Status)
	}
	var n int
	if err := r.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE subject_id = $1 AND verb = 'run.cancelled'`, run.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("run.cancelled = %d %v", n, err)
	}
}

// A revisão em harness alternativo é cobrada na assinatura ativa da empresa desse harness
// (a persona autora pode ter assinatura de outro) e respeita a trava semanal dela.
func TestReviewCobradaNaAssinaturaDoHarness(t *testing.T) {
	ctx := context.Background()
	altHarness := func(in *service.HirePersonaInput, e *env) domain.Persona {
		in.ReviewerAgent, in.ReviewerModel = "codex", "gpt-rev"
		return domain.Persona{}
	}
	t.Run("cobre", func(t *testing.T) {
		r := newReviewEnv(t, altHarness)
		claudeSub := r.sub(t, func(in *service.CreateSubscriptionInput) { in.Name = "Claude"; in.Agent = "claude" })
		codexSub := r.sub(t, func(in *service.CreateSubscriptionInput) { in.Name = "Codex"; in.Agent = "codex" })
		if _, err := r.svc.UpdatePersona(ctx, domain.HumanActor, r.co.ID, r.author.ID, service.UpdatePersonaInput{SubscriptionID: &claudeSub.ID}); err != nil {
			t.Fatal(err)
		}
		r.closeTask(t)
		r.finishAuthor(t)
		run := r.startReview(t)
		if run.SubscriptionID == nil || *run.SubscriptionID != codexSub.ID {
			t.Fatalf("assinatura da run de revisão = %v, want %s", run.SubscriptionID, codexSub.ID)
		}
	})
	t.Run("trava semanal", func(t *testing.T) {
		r := newReviewEnv(t, altHarness)
		codexSub := r.sub(t, func(in *service.CreateSubscriptionInput) {
			in.Name, in.Agent, in.WeeklyLimitUSD, in.WeeklyGuardPct = "Codex", "codex", ptr(10.0), 80
		})
		other := r.persona(t, "Outra", func(in *service.HirePersonaInput) { in.Agent = "codex"; in.SubscriptionID = &codexSub.ID })
		r.spend(t, other, time.Now(), nil, usd(9))
		r.svc.Now = time.Now
		r.closeTask(t)
		r.finishAuthor(t)
		if err := r.svc.StartReview(ctx, r.co.ID, r.jobs.reviews[0]); err != nil {
			t.Fatal(err)
		}
		gs := r.pendingGates(t)
		if len(gs) != 1 || gs[0].Review == nil || gs[0].Review.Verdict != "" || !strings.Contains(gs[0].Review.Justification, "trava semanal") {
			t.Fatalf("gate: %+v", gs)
		}
	})
}

// Revisor alternativo igual à autora (mesmo agent e mesmo modelo) não é segunda opinião.
func TestReviewerAlternativoIgualAAutora(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "A", Agent: "claude", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	upd := func(in service.UpdatePersonaInput) error {
		_, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, a.ID, in)
		return err
	}
	if err := upd(service.UpdatePersonaInput{ReviewerAgent: ptr("claude"), ReviewerModel: ptr("m1")}); err == nil {
		t.Error("mesmo agent e modelo deveria falhar")
	}
	if err := upd(service.UpdatePersonaInput{ReviewerAgent: ptr("claude"), ReviewerModel: ptr("m2")}); err != nil {
		t.Errorf("modelo diferente: %v", err)
	}
	if err := upd(service.UpdatePersonaInput{ReviewerAgent: ptr("codex"), ReviewerModel: ptr("m1")}); err != nil {
		t.Errorf("agent diferente: %v", err)
	}
	if err := upd(service.UpdatePersonaInput{ReviewerAgent: ptr("claude"), ReviewerModel: ptr("m2")}); err != nil {
		t.Fatal(err)
	}
	// a autora passando a usar o modelo do revisor também é refutada
	if err := upd(service.UpdatePersonaInput{Model: ptr("m2")}); err == nil {
		t.Error("autora igualando o revisor deveria falhar")
	}
	if _, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "B", Agent: "claude", Model: "m1", ReviewerAgent: "claude", ReviewerModel: "m1"}); err == nil {
		t.Error("contratar com revisor igual deveria falhar")
	}
}

// A Caixa de entrada mostra a revisão em andamento (informativa: não conta como pendência) até o gate nascer.
func TestInboxMostraRevisaoEmAndamento(t *testing.T) {
	ctx := context.Background()
	r := newReviewEnv(t, withReviewerPersona(t))
	r.closeTask(t)
	in, err := r.svc.Inbox(ctx, r.co.ID)
	if err != nil || len(in.UnderReview) != 1 || in.UnderReview[0].Status != "pending" || in.UnderReview[0].Task.ID != r.task.ID || in.Count() != 0 {
		t.Fatalf("pending: %+v count=%d %v", in.UnderReview, in.Count(), err)
	}
	r.finishAuthor(t)
	run := r.startReview(t)
	if in, _ = r.svc.Inbox(ctx, r.co.ID); len(in.UnderReview) != 1 || in.UnderReview[0].Status != "running" {
		t.Fatalf("running: %+v", in.UnderReview)
	}
	r.finishReview(t, run, domain.RunDone, "VEREDITO: APROVADO\nok")
	if in, _ = r.svc.Inbox(ctx, r.co.ID); len(in.UnderReview) != 0 || len(in.Gates) != 1 {
		t.Fatalf("depois do gate: review=%+v gates=%d", in.UnderReview, len(in.Gates))
	}
}
