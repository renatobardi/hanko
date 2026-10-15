package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
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
