package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Resiliência de run (ADR-0039): card de diagnóstico, ações do operador, steer e fila de follow-ups.

func diagCauseLabel(ctx context.Context, c domain.DiagnosisCause) string {
	switch c {
	case domain.CauseProcessRestart:
		return i18n.T(ctx, "o processo do Hanko caiu")
	case domain.CauseServerShutdown:
		return i18n.T(ctx, "o Hanko foi encerrado")
	case domain.CauseAgentError:
		return i18n.T(ctx, "erro do agente")
	case domain.CauseTimeout:
		return i18n.T(ctx, "tempo limite")
	case domain.CauseCancelled:
		return i18n.T(ctx, "cancelada")
	}
	return string(c)
}

func diagActionLabel(ctx context.Context, a domain.DiagnosisAction) string {
	switch a {
	case domain.ActionAutoContinue:
		return i18n.T(ctx, "continuação automática")
	case domain.ActionHumanDecision:
		return i18n.T(ctx, "espera a sua decisão")
	case domain.ActionContinued:
		return i18n.T(ctx, "já continuada")
	}
	return i18n.T(ctx, "nenhuma")
}

func decisionLabel(ctx context.Context, d domain.RunDecision) string {
	switch d {
	case domain.DecisionRetry:
		return i18n.T(ctx, "Tentar de novo")
	case domain.DecisionContinue:
		return i18n.T(ctx, "Continuar")
	case domain.DecisionClose:
		return i18n.T(ctx, "Encerrar")
	}
	return ""
}

func continuationReasonLabel(ctx context.Context, r domain.ContinuationReason) string {
	switch r {
	case domain.ContinueRecovery:
		return i18n.T(ctx, "recuperação")
	case domain.ContinueSteer:
		return i18n.T(ctx, "mudança de rumo")
	case domain.ContinueRetry:
		return i18n.T(ctx, "tentar de novo")
	case domain.ContinueManual:
		return i18n.T(ctx, "continuar")
	}
	return ""
}

func steerModeLabel(ctx context.Context, m domain.SteerMode) string {
	switch m {
	case domain.SteerInject:
		return i18n.T(ctx, "injeta ao vivo")
	case domain.SteerRestart:
		return i18n.T(ctx, "interrompe e continua")
	}
	return i18n.T(ctx, "não aceita")
}

func steerHint(ctx context.Context, m domain.SteerMode) string {
	if m == domain.SteerInject {
		return i18n.T(ctx, "A mensagem entra na execução em andamento; a persona muda de rumo sem recomeçar.")
	}
	return i18n.T(ctx, "Este harness não aceita mensagem no meio: a execução é interrompida e continua com a sua mensagem.")
}

func followupDeliveryLabel(ctx context.Context, d domain.FollowupDelivery) string {
	switch d {
	case domain.DeliverySteer:
		return i18n.T(ctx, "injetar ao vivo")
	case domain.DeliveryRestart:
		return i18n.T(ctx, "interromper e continuar")
	}
	return i18n.T(ctx, "na fila")
}

func followupWaitLabel(ctx context.Context, f domain.Followup) string {
	var s string
	switch f.WaitReason {
	case domain.WaitRunActive:
		s = i18n.T(ctx, "espera a execução terminar")
	case domain.WaitSteerPending:
		s = i18n.T(ctx, "será injetado na execução ativa")
	case domain.WaitRestartPending:
		s = i18n.T(ctx, "a execução será interrompida e continuada")
	case domain.WaitDecisionPending:
		s = i18n.T(ctx, "espera a sua decisão sobre uma execução parada")
	case domain.WaitPersonaUnavailable:
		s = i18n.T(ctx, "a persona não pode trabalhar agora")
	case domain.WaitUnassigned:
		s = i18n.T(ctx, "a task não tem persona")
	default:
		s = i18n.T(ctx, "a próxima execução está abrindo")
	}
	if f.WaitDetail != "" {
		s += " (" + f.WaitDetail + ")"
	}
	return s
}

// diagVM builds the diagnosis card of a run; nil when there is nothing to show (a run that ended well and was never
// part of a continuation chain).
func diagVM(ctx context.Context, base string, rv service.RunView, back string) *views.DiagVM {
	d := rv.Diagnosis
	if d == nil && rv.ContinuationOfRunID == nil && rv.ContinuedByRunID == nil {
		return nil
	}
	vm := &views.DiagVM{ActionHref: base + "/execucoes/" + rv.ID.String() + "/acao", Back: back}
	if d != nil {
		vm.Cause, vm.Detail = diagCauseLabel(ctx, d.Cause), d.CauseDetail
		vm.Attempt = i18n.Tf(ctx, "continuação %d de %d", d.Attempt, d.MaxAttempts)
		vm.NextAction, vm.Exhausted = diagActionLabel(ctx, d.NextAction), d.Exhausted
		vm.Owner = i18n.T(ctx, "operador")
		if d.Owner == domain.OwnerSystem {
			vm.Owner = i18n.T(ctx, "sistema")
		}
		if d.Uncertain != nil {
			vm.Uncertain = d.Uncertain.Name
			if d.Uncertain.Input != "" {
				vm.Uncertain += ": " + d.Uncertain.Input
			}
		}
		vm.NeedsDecision = rv.Decision == domain.DecisionNone && (rv.DecisionPending || d.NextAction == domain.ActionHumanDecision) && rv.ContinuedByRunID == nil
	}
	if rv.Decision != domain.DecisionNone {
		vm.Decided, vm.Note = decisionLabel(ctx, rv.Decision), rv.DecisionNote
	}
	if rv.ContinuationOfRunID != nil {
		vm.OfHref, vm.OfName = base+"/execucoes/"+rv.ContinuationOfRunID.String(), shortID(*rv.ContinuationOfRunID)
		vm.Reason = continuationReasonLabel(ctx, rv.ContinuationReason)
	}
	if rv.ContinuedByRunID != nil {
		vm.ByHref, vm.ByName = base+"/execucoes/"+rv.ContinuedByRunID.String(), shortID(*rv.ContinuedByRunID)
	}
	return vm
}

// steerVM is the "change course" box of an active run; nil when the run takes no operator message.
func steerVM(ctx context.Context, base string, rv service.RunView) *views.SteerVM {
	if !rv.Status.Active() || rv.SteerMode == domain.SteerNone || rv.SteerMode == "" {
		return nil
	}
	switch rv.Trigger {
	case domain.TriggerReview, domain.TriggerMemory, domain.TriggerSkillTest, domain.TriggerStatusCard:
		return nil
	}
	return &views.SteerVM{Href: base + "/execucoes/" + rv.ID.String() + "/steer", Mode: string(rv.SteerMode),
		ModeLabel: steerModeLabel(ctx, rv.SteerMode), Hint: steerHint(ctx, rv.SteerMode), Back: base + "/execucoes/" + rv.ID.String()}
}

// followupsVM turns the queue into the panel; steer is the box shown under it when a run is active.
func followupsVM(ctx context.Context, base string, fs []domain.Followup, back string, steer *views.SteerVM) *views.FollowupsVM {
	vm := &views.FollowupsVM{Back: back, Steer: steer, Active: steer != nil}
	for _, f := range fs {
		vm.Items = append(vm.Items, views.FollowupItem{ID: f.ID.String(), Pos: f.Position, Body: f.Body,
			Delivery: followupDeliveryLabel(ctx, f.Delivery), Wait: followupWaitLabel(ctx, f), WaitKey: string(f.WaitReason), Edited: f.EditedAt != nil,
			EditHref:    base + "/followups/" + f.ID.String() + "/editar",
			DiscardHref: base + "/followups/" + f.ID.String() + "/descartar"})
	}
	return vm
}

// safeBack accepts only a path inside the company (the form says where to go back to) and falls back otherwise.
func safeBack(co domain.Company, raw, fallback string) string {
	prefix := "/c/" + co.Slug + "/"
	if raw == "" || !strings.HasPrefix(raw, prefix) || strings.ContainsAny(raw, "\\\r\n") || strings.Contains(raw, "//") {
		return fallback
	}
	if u, err := url.Parse(raw); err != nil || u.Host != "" || u.Scheme != "" {
		return fallback
	}
	return raw
}

func (s *server) handleRunAction(w http.ResponseWriter, r *http.Request, co domain.Company) {
	runID, err := domain.ParseID(r.PathValue("run"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	action := domain.RunDecision(strings.TrimSpace(r.PostFormValue("action")))
	if _, err := s.be.RunAction(r.Context(), actorFrom(r.Context()), co.ID, runID,
		service.RunActionInput{Action: action, Note: strings.TrimSpace(r.PostFormValue("note"))}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, safeBack(co, r.PostFormValue("voltar"), "/c/"+co.Slug+"/execucoes/"+runID.String()))
}

func (s *server) handleRunSteer(w http.ResponseWriter, r *http.Request, co domain.Company) {
	runID, err := domain.ParseID(r.PathValue("run"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.be.SteerRun(r.Context(), actorFrom(r.Context()), co.ID, runID, r.PostFormValue("message")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, safeBack(co, r.PostFormValue("voltar"), "/c/"+co.Slug+"/execucoes/"+runID.String()))
}

func (s *server) handleQueueFollowup(w http.ResponseWriter, r *http.Request, co domain.Company) {
	t, err := s.be.GetTask(r.Context(), co.ID, r.PathValue("task"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := s.be.QueueTaskFollowup(r.Context(), actorFrom(r.Context()), co.ID, t.ID, r.PostFormValue("message")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, safeBack(co, r.PostFormValue("voltar"), "/c/"+co.Slug+"/tasks/"+t.Key+"#followups"))
}

func (s *server) handleEditFollowup(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("followup"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.be.EditFollowup(r.Context(), actorFrom(r.Context()), co.ID, id, r.PostFormValue("message")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, safeBack(co, r.PostFormValue("voltar"), "/c/"+co.Slug+"/caixa"))
}

func (s *server) handleDiscardFollowup(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, err := domain.ParseID(r.PathValue("followup"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.be.DiscardFollowup(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, safeBack(co, r.PostFormValue("voltar"), "/c/"+co.Slug+"/caixa"))
}

// handleChatQueue is the htmx fragment that keeps the conversation's queue fresh while the persona answers.
func (s *server) handleChatQueue(w http.ResponseWriter, r *http.Request, co domain.Company) {
	cv, ok := s.chatConversation(w, r, co)
	if !ok {
		return
	}
	vm, err := s.buildChatVM(r, co, cv)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.ChatQueue(vm))
}

// inboxRunDiag is the diagnosis card of an inbox row; back returns to the inbox.
func inboxRunDiag(ctx context.Context, co domain.Company, rv service.RunView) *views.DiagVM {
	return diagVM(ctx, "/c/"+co.Slug, rv, fmt.Sprintf("/c/%s/caixa", co.Slug))
}
