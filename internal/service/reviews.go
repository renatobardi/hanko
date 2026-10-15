package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Revisão por outro modelo antes do gate humano.
//
// Quando uma persona com require_review e um revisor configurado (outra persona, ou o mesmo papel em
// outro harness/modelo) tenta fechar uma task, o Hanko não abre o gate de imediato:
//
//  1. requestTaskReview registra uma revisão pending e enfileira o job start_review;
//  2. StartReview espera a run da autora terminar (a task só tem uma run ativa), monta o pacote do
//     artefato (task + entrega), grava o sha256 dele e abre uma run headless com gatilho review;
//  3. quando a run termina (FinishRun, interrupção ou cancelamento) o veredito é lido da resposta
//     final do revisor e o gate task_review nasce já carregando-o (aprovado/reprovado, justificativa,
//     sha256, run). Reprovado não decide nada: o gate continua pendente e o humano decide.
//
// Revisão que não conclui (revisor indisponível, orçamento, falha, sem veredito válido, tempo) abre o
// gate sem veredito, com o motivo: nunca deixa a task parada em in_review sem gate. Task que sai de
// in_review no meio (fechada, devolvida) cancela a revisão e não abre gate.

// reviewPendingGiveUp é quanto uma revisão pode esperar para iniciar antes de o gate abrir sem veredito.
const reviewPendingGiveUp = 2 * time.Hour

const (
	maxReviewThread  = 10
	maxReviewComment = 8 << 10
	maxReviewNote    = 4000
)

// ReviewContext é o que o executor precisa para rodar a run de revisão.
type ReviewContext struct {
	ID              domain.ID
	Artifact        string
	ArtifactSHA256  string
	RequestedStatus domain.TaskStatus
	AuthorName      string
	// Independent: a run é de outra persona (com instruções próprias). Falso = o mesmo papel da autora
	// em outro harness/modelo.
	Independent bool
}

func reviewerLabel(name, agent, model string, independent bool) string {
	h := agent
	if model != "" {
		h += " · " + model
	}
	if independent {
		return fmt.Sprintf("%s (%s)", name, h)
	}
	return fmt.Sprintf("%s em %s", name, h)
}

// requestReview registers the review of a closing request and enqueues its start.
func (t *txc) requestReview(companyID domain.ID, task store.Task, author store.Persona, requested domain.TaskStatus) error {
	if _, err := t.q.OpenReviewForTask(t.ctx, task.ID); err == nil {
		return nil // already under review
	} else if !isNoRows(err) {
		return fmt.Errorf("open review: %w", err)
	}
	agent, model := author.ReviewerAgent, author.ReviewerModel
	if author.ReviewerPersonaID != nil {
		rp, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *author.ReviewerPersonaID})
		if err != nil {
			return mapErr(err, "reviewer persona")
		}
		agent, model = rp.Agent, rp.Model
	}
	var authorRun *domain.ID
	if run, err := t.q.ActiveRunForTask(t.ctx, task.ID); err == nil && run.PersonaID == author.ID {
		authorRun = &run.ID
	}
	rv, err := t.q.CreateReview(t.ctx, store.CreateReviewParams{ID: domain.NewID(), CompanyID: companyID, TaskID: task.ID,
		AuthorPersonaID: author.ID, AuthorRunID: authorRun, ReviewerPersonaID: author.ReviewerPersonaID,
		Agent: agent, Model: model, RequestedStatus: string(requested), Now: t.now})
	if err != nil {
		return mapErr(err, "create review")
	}
	if err := t.activity(domain.PersonaActor(author.ID), companyID, "review.requested", "review", rv.ID, map[string]any{
		"task_id": task.ID, "reviewer_persona_id": rv.ReviewerPersonaID, "agent": agent, "model": model, "requested": requested}, ""); err != nil {
		return err
	}
	if t.s.Jobs != nil {
		if err := t.s.Jobs.StartReviewTx(t.ctx, t.tx, companyID, rv.ID); err != nil {
			return fmt.Errorf("enqueue start_review: %w", err)
		}
	}
	return nil
}

// lockReview takes the locks of a review in the canonical order of the service (task → run →
// review): the task first, then the review. UpdateTask and the run-cancel paths lock in that order,
// so locking the review first would deadlock against them. A missing review returns ErrNoRows-like
// store errors (isNoRows).
func (t *txc) lockReview(companyID, reviewID domain.ID) (store.Review, store.Task, error) {
	peek, err := t.q.GetReviewByID(t.ctx, store.GetReviewByIDParams{CompanyID: companyID, ID: reviewID})
	if err != nil {
		return store.Review{}, store.Task{}, err
	}
	task, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: peek.TaskID})
	if err != nil {
		return store.Review{}, store.Task{}, mapErr(err, "get task")
	}
	rv, err := t.q.GetReviewByIDForUpdate(t.ctx, store.GetReviewByIDForUpdateParams{CompanyID: companyID, ID: reviewID})
	return rv, task, err
}

// lockTaskOfReviewRun locks the task of a review run before the run itself (canonical order).
// The same goes for the consolidation of memory (trigger memory), which closes its task when the run ends.
// It is a no-op for any other run.
func (t *txc) lockTaskOfReviewRun(companyID, runID domain.ID) error {
	peek, err := t.q.GetRunByID(t.ctx, store.GetRunByIDParams{CompanyID: companyID, ID: runID})
	if isNoRows(err) {
		return nil // the caller's own lookup reports it
	}
	if err != nil {
		return mapErr(err, "get run")
	}
	if tr := domain.RunTrigger(peek.Trigger); tr != domain.TriggerReview && tr != domain.TriggerMemory {
		return nil
	}
	if _, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: peek.TaskID}); err != nil {
		return mapErr(err, "get task")
	}
	return nil
}

// StartReview opens the headless review run of a pending review. While the task still has an
// active run (typically the author's, which asked to close it) it returns domain.ErrActiveRun and
// changes nothing, so the job tries again later.
func (s *Service) StartReview(ctx context.Context, companyID, reviewID domain.ID) error {
	return s.withTx(ctx, func(t *txc) error {
		rv, task, err := t.lockReview(companyID, reviewID)
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return mapErr(err, "get review")
		}
		if rv.Status != "pending" {
			return nil
		}
		if domain.TaskStatus(task.Status) != domain.TaskInReview {
			return t.cancelReview(rv, "task_left_review")
		}
		if _, err := t.q.ActiveRunForTask(ctx, task.ID); err == nil {
			return domain.ErrActiveRun
		} else if !isNoRows(err) {
			return fmt.Errorf("check active run: %w", err)
		}
		author, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: rv.AuthorPersonaID})
		if err != nil {
			return mapErr(err, "author persona")
		}
		runPersona, independent := author, false
		if rv.ReviewerPersonaID != nil {
			rp, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *rv.ReviewerPersonaID})
			if err != nil {
				return mapErr(err, "reviewer persona")
			}
			runPersona, independent = rp, true
			if !domain.PersonaStatus(rp.Status).CanWork() {
				return t.completeReview(rv, task, nil, "failed", nil,
					fmt.Sprintf("O revisor %s não pode trabalhar agora (status %s).", rp.Name, rp.Status),
					reviewerLabel(rp.Name, rv.Agent, rv.Model, true), nil)
			}
		}
		label := reviewerLabel(runPersona.Name, rv.Agent, rv.Model, independent)
		co, err := t.q.GetCompanyByID(ctx, companyID)
		if err != nil {
			return mapErr(err, "get company")
		}
		project, err := t.projectOf(companyID, task.ProjectID)
		if err != nil {
			return err
		}
		if berr := t.checkBudgets(co, runPersona, project, true); berr != nil {
			return t.completeReview(rv, task, nil, "failed", nil, "A revisão não rodou: "+berr.Error(), label, nil)
		}
		artifact, err := t.reviewArtifact(co, task, author, domain.TaskStatus(rv.RequestedStatus))
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(artifact))
		sha := hex.EncodeToString(sum[:])
		// The harness of the review may differ from the persona's: charge the subscription of that harness.
		model, modelSource := domain.ResolveModel("", rv.Model)
		sub, err := t.subscriptionForHarness(runPersona, rv.Agent, model)
		if err != nil {
			return err
		}
		var subID *domain.ID
		if sub != nil {
			subID = &sub.ID
			if gerr := t.checkWeeklyGuard(co, *sub); gerr != nil {
				if !errors.Is(gerr, domain.ErrSubscriptionGuard) {
					return gerr
				}
				return t.completeReview(rv, task, nil, "failed", nil, "A revisão não rodou: "+gerr.Error(), label, nil)
			}
		}
		ws, wsSource := pickWorkspace(task.WorkspaceID, project, runPersona)
		run, err := t.createRun(companyID, task.ID, runPersona.ID, rv.Agent, model, modelSource, subID, ws, wsSource, domain.TriggerReview)
		if err != nil {
			return err
		}
		if _, err := t.q.MarkReviewRunning(ctx, store.MarkReviewRunningParams{ID: rv.ID, RunID: &run.ID, Artifact: artifact,
			ArtifactSha256: sha, Now: &t.now}); err != nil {
			return mapErr(err, "mark review running")
		}
		if err := t.activity(domain.SystemActor, companyID, "run.queued", "run", run.ID,
			map[string]any{"task_id": task.ID, "persona_id": runPersona.ID, "trigger": domain.TriggerReview, "review_id": rv.ID}, ""); err != nil {
			return err
		}
		if err := t.activity(domain.SystemActor, companyID, "review.started", "review", rv.ID, map[string]any{
			"task_id": task.ID, "run_id": run.ID, "reviewer": label, "artifact_sha256": sha}, ""); err != nil {
			return err
		}
		if t.s.Jobs != nil {
			if err := t.s.Jobs.ExecuteRunTx(ctx, t.tx, companyID, run.ID); err != nil {
				return fmt.Errorf("enqueue execute_run: %w", err)
			}
		}
		t.publishRunStatus(runFromRow(run))
		return nil
	})
}

// FailReview gives up on a pending review (the job could not start it in time): the gate opens
// without a verdict, with the reason.
func (s *Service) FailReview(ctx context.Context, companyID, reviewID domain.ID, reason string) error {
	return s.withTx(ctx, func(t *txc) error {
		rv, _, err := t.lockReview(companyID, reviewID)
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return mapErr(err, "get review")
		}
		return t.abandonReview(rv, reason)
	})
}

// abandonReview ends an open review that has no usable run: the gate opens without a verdict
// (unless the task already left in_review, then the review is just cancelled).
func (t *txc) abandonReview(rv store.Review, reason string) error {
	if rv.Status != "pending" && rv.Status != "running" {
		return nil
	}
	task, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: rv.CompanyID, ID: rv.TaskID})
	if err != nil {
		return mapErr(err, "get task")
	}
	if domain.TaskStatus(task.Status) != domain.TaskInReview {
		return t.cancelReview(rv, "task_left_review")
	}
	label := "revisor"
	if p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: rv.CompanyID, ID: rv.AuthorPersonaID}); err == nil {
		indep := rv.ReviewerPersonaID != nil
		name := p.Name
		if indep {
			if rp, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: rv.CompanyID, ID: *rv.ReviewerPersonaID}); err == nil {
				name = rp.Name
			}
		}
		label = reviewerLabel(name, rv.Agent, rv.Model, indep)
	}
	return t.completeReview(rv, task, nil, "failed", nil, reason, label, rv.RunID)
}

// cancelReview ends a review without opening a gate (the task left in_review).
func (t *txc) cancelReview(rv store.Review, reason string) error {
	row, err := t.q.FinishReview(t.ctx, store.FinishReviewParams{ID: rv.ID, Status: "cancelled", Justification: reason, Now: &t.now})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return mapErr(err, "cancel review")
	}
	return t.activity(domain.SystemActor, rv.CompanyID, "review.cancelled", "review", row.ID,
		map[string]any{"task_id": row.TaskID, "reason": reason}, "")
}

// completeReview opens the task_review gate carrying the review outcome and closes the review.
// status is done (verdict set) or failed (no verdict; justification says why).
func (t *txc) completeReview(rv store.Review, task store.Task, _ *store.Run, status string, verdict *domain.ReviewVerdict,
	justification, label string, runID *domain.ID) error {
	co, err := t.q.GetCompanyByID(t.ctx, rv.CompanyID)
	if err != nil {
		return mapErr(err, "company")
	}
	key := domain.TaskKey(co.TaskPrefix, task.Number)
	in := taskReviewInput(key, task, domain.TaskStatus(rv.RequestedStatus), rv.AuthorRunID)
	justification = clipText(strings.TrimSpace(justification), maxReviewNote)
	in.Body += reviewSection(label, verdict, justification, rv.ArtifactSha256)
	in.EventID = "review:" + rv.ID.String()
	var v *string
	if verdict != nil {
		v = ptr(string(*verdict))
	}
	in.review = &gateReviewData{reviewer: label, verdict: v, justification: justification, sha: rv.ArtifactSha256, runID: runID}
	g, err := t.createGate(domain.PersonaActor(rv.AuthorPersonaID), rv.CompanyID, in, &rv.AuthorPersonaID)
	if err != nil {
		return err
	}
	row, err := t.q.FinishReview(t.ctx, store.FinishReviewParams{ID: rv.ID, Status: status, Verdict: v,
		Justification: justification, GateID: &g.ID, Now: &t.now})
	if err != nil {
		return mapErr(err, "finish review")
	}
	verb := "review.finished"
	if status == "failed" {
		verb = "review.failed"
	}
	return t.activity(domain.SystemActor, rv.CompanyID, verb, "review", row.ID, map[string]any{
		"task_id": row.TaskID, "gate_id": g.ID, "run_id": runID, "verdict": v, "reviewer": label, "artifact_sha256": rv.ArtifactSha256,
		"note": justification}, "")
}

// reviewRunEnded advances the review of a run with trigger review that just ended. Reviews that
// are not running (already cancelled, or never linked) are left alone.
func (t *txc) reviewRunEnded(run store.Run, status domain.RunStatus, summary, errMsg string) error {
	// Canonical lock order: task before review (callers already hold the run).
	if _, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: run.CompanyID, ID: run.TaskID}); err != nil {
		return mapErr(err, "get task")
	}
	rv, err := t.q.GetReviewByRunIDForUpdate(t.ctx, &run.ID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get review by run: %w", err)
	}
	if rv.Status != "running" {
		return nil
	}
	task, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: rv.CompanyID, ID: rv.TaskID})
	if err != nil {
		return mapErr(err, "get task")
	}
	if domain.TaskStatus(task.Status) != domain.TaskInReview {
		return t.cancelReview(rv, "task_left_review")
	}
	name := ""
	if p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: rv.CompanyID, ID: run.PersonaID}); err == nil {
		name = p.Name
	}
	label := reviewerLabel(name, rv.Agent, rv.Model, rv.ReviewerPersonaID != nil)
	runID := run.ID
	if status != domain.RunDone {
		note := fmt.Sprintf("A revisão automática não concluiu (execução %s).", status)
		if e := strings.TrimSpace(errMsg); e != "" {
			note += " " + e
		}
		return t.completeReview(rv, task, nil, "failed", nil, note, label, &runID)
	}
	v, just, ok := domain.ParseVerdict(summary)
	if !ok {
		return t.completeReview(rv, task, nil, "failed", nil,
			"O revisor terminou sem emitir um veredito válido (a resposta deve começar com `VEREDITO: APROVADO` ou `VEREDITO: REPROVADO`). Resposta do revisor:\n\n"+just,
			label, &runID)
	}
	if just == "" {
		just = "(o revisor não deu justificativa)"
	}
	return t.completeReview(rv, task, nil, "done", &v, just, label, &runID)
}

// ResolveStaleReviews finishes reviews that lost their way: pending for too long (the job never
// started them) or running with a run that already ended. Safety net so a gate is never lost.
func (s *Service) ResolveStaleReviews(ctx context.Context) (int, error) {
	rows, err := s.read().ListStaleReviews(ctx, store.ListStaleReviewsParams{PendingBefore: s.Now().UTC().Add(-reviewPendingGiveUp), Lim: 50})
	if err != nil {
		return 0, fmt.Errorf("list stale reviews: %w", err)
	}
	n := 0
	var errs []error
	for _, rv := range rows {
		err := s.withTx(ctx, func(t *txc) error {
			cur, _, err := t.lockReview(rv.CompanyID, rv.ID)
			if err != nil {
				return mapErr(err, "get review")
			}
			switch {
			case cur.Status == "running" && cur.RunID != nil:
				run, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: rv.CompanyID, ID: *cur.RunID})
				if err != nil {
					return mapErr(err, "get run")
				}
				if !domain.RunStatus(run.Status).Terminal() {
					return nil
				}
				return t.reviewRunEnded(run, domain.RunStatus(run.Status), run.Summary, run.Error)
			case cur.Status == "running":
				return t.abandonReview(cur, "A revisão perdeu a execução.")
			case cur.Status == "pending":
				return t.abandonReview(cur, "A revisão não conseguiu iniciar a tempo (a task manteve uma execução ativa).")
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// reviewArtifact renders the package the reviewer judges: the task, who asked to close it and the
// latest thread. It is stored on the review together with its sha256.
func (t *txc) reviewArtifact(co store.Company, task store.Task, author store.Persona, requested domain.TaskStatus) (string, error) {
	cs, err := t.q.ListComments(t.ctx, store.ListCommentsParams{CompanyID: co.ID, TaskID: task.ID})
	if err != nil {
		return "", fmt.Errorf("list comments: %w", err)
	}
	var b strings.Builder
	key := domain.TaskKey(co.TaskPrefix, task.Number)
	fmt.Fprintf(&b, "# Entrega para revisão — %s: %s\n\n", key, task.Title)
	fmt.Fprintf(&b, "- Persona autora: %s\n- Status pedido: %s\n\n", author.Name, requested)
	b.WriteString("## Descrição da task\n\n")
	if d := strings.TrimSpace(task.Description); d != "" {
		b.WriteString(d)
	} else {
		b.WriteString("(sem descrição)")
	}
	b.WriteString("\n\n## Thread\n\n")
	if len(cs) == 0 {
		b.WriteString("(sem comentários)\n")
	}
	if len(cs) > maxReviewThread {
		fmt.Fprintf(&b, "(%d comentários anteriores omitidos)\n\n", len(cs)-maxReviewThread)
		cs = cs[len(cs)-maxReviewThread:]
	}
	for _, c := range cs {
		who := "Humano"
		switch domain.ActorType(c.AuthorType) {
		case domain.ActorSystem:
			who = "Sistema"
		case domain.ActorPersona:
			who = "Persona"
			if c.AuthorID != nil {
				if *c.AuthorID == author.ID {
					who = author.Name + " (autora)"
				} else {
					who = "Persona " + c.AuthorID.String()[:8]
				}
			}
		}
		fmt.Fprintf(&b, "**%s** · %s\n\n%s\n\n", who, c.CreatedAt.UTC().Format(time.RFC3339), clipText(strings.TrimSpace(c.Body), maxReviewComment))
	}
	return b.String(), nil
}

func clipText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "") + "…[truncado]"
}

// taskReviewInput is the task_review gate request shared by the direct path and the reviewed one.
func taskReviewInput(key string, task store.Task, requested domain.TaskStatus, runID *domain.ID) RequestGateInput {
	body := "A persona deu a task como pronta e esta persona exige revisão antes de concluir. Aprove para concluir, peça mudanças ou rejeite."
	if requested == domain.TaskCancelled {
		body = "A persona quer cancelar a task e esta persona exige revisão antes de encerrar. Aprove para cancelar, peça mudanças ou rejeite."
	}
	return RequestGateInput{Kind: domain.GateTaskReview, TaskID: &task.ID, RunID: runID, requestedStatus: requested,
		Title: fmt.Sprintf("Revisar %s — %s", key, task.Title), Body: body}
}

// reviewSection is the markdown block that tells the human what the automatic review found.
func reviewSection(label string, verdict *domain.ReviewVerdict, justification, sha string) string {
	var b strings.Builder
	b.WriteString("\n\n## Revisão automática\n\n")
	fmt.Fprintf(&b, "- Revisor: %s\n", label)
	switch {
	case verdict == nil:
		b.WriteString("- Veredito: **sem veredito** (a revisão não concluiu)\n")
	case *verdict == domain.ReviewRejected:
		b.WriteString("- Veredito: **REPROVADO pelo revisor** (a decisão continua sendo sua)\n")
	default:
		b.WriteString("- Veredito: **APROVADO pelo revisor** (a decisão continua sendo sua)\n")
	}
	if sha != "" {
		fmt.Fprintf(&b, "- Artefato revisado (sha256): `%s`\n", sha)
	}
	if justification != "" {
		fmt.Fprintf(&b, "\n%s\n", justification)
	}
	return b.String()
}

// gateReviewData is the review outcome stored on the gate it opened.
type gateReviewData struct {
	reviewer      string
	verdict       *string
	justification string
	sha           string
	runID         *domain.ID
}
