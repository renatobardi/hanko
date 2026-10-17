package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/store"
)

// Resiliência de run (ADR-0039): continuação limitada, diagnóstico com decisão humana, steering e a fila de
// follow-ups. Toda escrita gera activity_event na mesma transação.

// RunSteerMsg é o tipo da mensagem que o service publica no tópico da run para o executor repassar ao adapter.
const RunSteerMsg = "run.steer"

// SteerEvent é a mensagem de steer publicada no broker.
type SteerEvent struct {
	RunID      domain.ID `json:"run_id"`
	FollowupID domain.ID `json:"followup_id"`
	Text       string    `json:"text"`
}

// maxDecisionNote é o maior comentário que acompanha uma decisão do operador.
const maxDecisionNote = 2000

// ---------- continuação ----------

// continuation descreve a run que uma run nova continua.
type continuation struct {
	of      store.Run
	reason  domain.ContinuationReason
	attempt int
}

// nextAttempt é a contagem de continuações da run nova: a recuperação consome uma tentativa (menos se a run morta
// nem tinha começado), o steer não consome, e uma decisão humana devolve o orçamento inteiro.
func nextAttempt(old store.Run, reason domain.ContinuationReason) int {
	switch reason {
	case domain.ContinueRecovery:
		if old.StartedAt == nil {
			return int(old.ContinuationAttempt)
		}
		return int(old.ContinuationAttempt) + 1
	case domain.ContinueSteer:
		return int(old.ContinuationAttempt)
	}
	return 0
}

// pendingContinuation devolve a continuação que a próxima run da task deve ser (a run interrompida, ou cancelada por
// steer, que deixou a marca resume_pending), ou nil.
func (t *txc) pendingContinuation(taskID domain.ID) (*continuation, error) {
	old, err := t.q.PendingResumeRunForTask(t.ctx, &taskID)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pending resume run: %w", err)
	}
	stale := !domain.RunStatus(old.Status).Terminal()
	if !stale {
		if _, cerr := t.q.ContinuationOfRun(t.ctx, &old.ID); cerr == nil {
			stale = true // já tem continuação: a marca sobrou
		} else if !isNoRows(cerr) {
			return nil, fmt.Errorf("continuation of run: %w", cerr)
		}
	}
	if stale {
		if err := t.q.ClearResumePending(t.ctx, old.ID); err != nil {
			return nil, fmt.Errorf("clear resume pending: %w", err)
		}
		return nil, nil
	}
	reason := domain.ContinuationReason(old.ResumeReason)
	if !reason.Valid() || reason == domain.ContinueNone {
		reason = domain.ContinueRecovery
	}
	return &continuation{of: old, reason: reason, attempt: nextAttempt(old, reason)}, nil
}

// runOpenedAsContinuation fecha a contabilidade depois de criar a run continuação: limpa a marca da antiga e registra.
func (t *txc) runOpenedAsContinuation(run store.Run, c *continuation) error {
	if c == nil {
		return nil
	}
	if err := t.q.ClearResumePending(t.ctx, c.of.ID); err != nil {
		return fmt.Errorf("clear resume pending: %w", err)
	}
	return t.activity(domain.SystemActor, run.CompanyID, "run.continued", "run", run.ID,
		map[string]any{"of_run_id": c.of.ID, "reason": c.reason, "attempt": c.attempt, "max_attempts": domain.MaxContinuations}, "")
}

// afterInterruption diagnostica uma run interrompida (queda do processo ou encerramento do servidor) e decide o que
// vem depois: continuação automática, ou espera por decisão humana quando o desfecho é incerto (tool de efeito
// externo em andamento) ou as continuações acabaram. ok=false para runs que não entram na recuperação (revisão,
// curadoria de memória). A task é acordada aqui quando a continuação é automática; no chat a continuação abre na hora.
func (t *txc) afterInterruption(row store.Run, cause domain.DiagnosisCause) (d domain.RunDiagnosis, ok bool, err error) {
	switch domain.RunTrigger(row.Trigger) {
	case domain.TriggerReview, domain.TriggerMemory, domain.TriggerSkillTest, domain.TriggerStatusCard:
		return d, false, nil
	}
	if row.TaskID != nil {
		if system, serr := t.isSystemTask(*row.TaskID); serr != nil {
			return d, false, serr
		} else if system {
			return d, false, nil
		}
	}
	started := row.StartedAt != nil
	var uncertain *domain.UncertainTool
	if started {
		rows, qerr := t.q.ListToolEventsOfRun(t.ctx, store.ListToolEventsOfRunParams{CompanyID: row.CompanyID, RunID: row.ID, Lim: 500})
		if qerr != nil {
			return d, false, fmt.Errorf("list tool events: %w", qerr)
		}
		evs := make([]domain.RunEvent, len(rows)) // the query is newest-first
		for i, r := range rows {
			evs[len(rows)-1-i] = domain.RunEvent{Seq: r.Seq, Kind: domain.RunEventKind(r.Kind), Text: r.Text, Payload: json.RawMessage(r.Payload)}
		}
		uncertain = domain.UncertainOf(domain.OpenToolCalls(evs))
	}
	d = domain.RunDiagnosis{Cause: cause, Attempt: int(row.ContinuationAttempt), MaxAttempts: domain.MaxContinuations, Uncertain: uncertain}
	switch cause {
	case domain.CauseProcessRestart:
		d.CauseDetail = "o processo do Hanko foi reiniciado com a execução em andamento"
	case domain.CauseServerShutdown:
		d.CauseDetail = "o Hanko foi encerrado com a execução em andamento"
	}
	autoContinue := false
	switch {
	case uncertain != nil:
		d.NextAction, d.Owner = domain.ActionHumanDecision, domain.OwnerHuman
		d.CauseDetail += fmt.Sprintf("; a tool %s estava em andamento e pode ter produzido efeito externo (desfecho incerto)", uncertain.Name)
	case started && int(row.ContinuationAttempt) >= domain.MaxContinuations:
		d.NextAction, d.Owner, d.Exhausted = domain.ActionHumanDecision, domain.OwnerHuman, true
		d.CauseDetail += fmt.Sprintf("; as %d continuações automáticas já foram usadas", domain.MaxContinuations)
	default:
		d.NextAction, d.Owner = domain.ActionAutoContinue, domain.OwnerSystem
		autoContinue = true
	}

	chat := row.ConversationID != nil
	if chat && autoContinue {
		// A chat run continues right away (there is no wake): same conversation, same messages, session resumed.
		c := &continuation{of: row, reason: domain.ContinueRecovery, attempt: nextAttempt(row, domain.ContinueRecovery)}
		_, blocked, cerr := t.continueChatRun(row, c)
		switch {
		case cerr != nil:
			return d, false, cerr
		case blocked != nil:
			d.NextAction, d.Owner = domain.ActionHumanDecision, domain.OwnerHuman
			d.CauseDetail += "; não foi possível retomar agora: " + blocked.Error()
			autoContinue = false
		default:
			d.NextAction = domain.ActionContinued
		}
	}
	raw, merr := json.Marshal(d)
	if merr != nil {
		return d, false, fmt.Errorf("marshal diagnosis: %w", merr)
	}
	resume := autoContinue && !chat
	reason := ""
	if resume {
		reason = string(domain.ContinueRecovery)
	}
	if err := t.q.SetRunInterruption(t.ctx, store.SetRunInterruptionParams{ID: row.ID, Diagnosis: raw,
		ResumePending: resume, ResumeReason: reason, DecisionPending: d.NeedsDecision()}); err != nil {
		return d, false, fmt.Errorf("set run interruption: %w", err)
	}
	if err := t.activity(domain.SystemActor, row.CompanyID, "run.diagnosed", "run", row.ID, d, ""); err != nil {
		return d, false, err
	}
	if resume && row.TaskID != nil {
		task, terr := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: row.CompanyID, ID: *row.TaskID})
		if terr != nil {
			return d, false, mapErr(terr, "get task")
		}
		if err := t.wake(row.CompanyID, task.ID, task.PersonaID, domain.TaskStatus(task.Status), domain.TriggerHeartbeat, true); err != nil {
			return d, false, err
		}
	}
	return d, true, nil
}

// continueChatRun opens the continuation of an interrupted chat run: the human messages the dead run was answering move
// to the new run, which resumes the harness session. blocked is the refusal (persona unavailable, budget…).
func (t *txc) continueChatRun(old store.Run, c *continuation) (run store.Run, blocked, err error) {
	if old.ConversationID == nil {
		return run, nil, errors.New("continueChatRun: not a chat run")
	}
	conv, err := t.q.GetConversationForUpdate(t.ctx, store.GetConversationForUpdateParams{CompanyID: old.CompanyID, ID: *old.ConversationID})
	if err != nil {
		return run, nil, mapErr(err, "get conversation")
	}
	if domain.ConversationStatus(conv.Status) != domain.ConversationOpen {
		return run, domain.Invalid("conversation", "a conversa está arquivada"), nil
	}
	run, blocked, err = t.openPersonaRun(conv, old.PersonaID, c)
	if err != nil || blocked != nil {
		return run, blocked, err
	}
	if _, err := t.q.ReassignHumanConversationMessages(t.ctx, store.ReassignHumanConversationMessagesParams{
		ConversationID: conv.ID, NewRunID: &run.ID, OldRunID: &old.ID}); err != nil {
		return run, nil, fmt.Errorf("reassign messages: %w", err)
	}
	if err := t.runOpenedAsContinuation(run, c); err != nil {
		return run, nil, err
	}
	return run, nil, t.launchChatRun(conv, run)
}

// chatInterrupted tells the conversation what happened to an interrupted run and what comes next.
func (t *txc) chatInterrupted(run store.Run, d domain.RunDiagnosis) error {
	if run.ConversationID == nil {
		return nil
	}
	conv, err := t.q.GetConversation(t.ctx, store.GetConversationParams{CompanyID: run.CompanyID, ID: *run.ConversationID})
	if err != nil {
		return mapErr(err, "get conversation")
	}
	var body string
	switch d.NextAction {
	case domain.ActionContinued:
		body = fmt.Sprintf("A resposta foi interrompida (%s). Retomando sozinha, continuação %d de %d.", d.CauseDetail, d.Attempt+1, d.MaxAttempts)
	default:
		body = fmt.Sprintf("A resposta foi interrompida e precisa da sua decisão: %s. Abra a execução para Tentar de novo, Continuar ou Encerrar.", d.CauseDetail)
	}
	return t.addChatMessage(conv, domain.MessageSystem, nil, body, &run.ID)
}

// ContinuationContext é o que a run continuação lê da run que ela continua.
type ContinuationContext struct {
	OfRunID     domain.ID
	OfRunName   string
	Reason      domain.ContinuationReason
	Attempt     int
	MaxAttempts int
	PrevStatus  domain.RunStatus
	PrevError   string
	Diagnosis   *domain.RunDiagnosis
	// Tail são os últimos passos registrados no log da run anterior (falas e tools), do mais antigo ao mais novo.
	Tail         []string
	Decision     domain.RunDecision
	DecisionNote string
	// ResumeSession é a sessão do harness a retomar ("" = recomeça do zero ou o harness mudou).
	ResumeSession string
}

const continuationTail = 14

func loadContinuation(ctx context.Context, q *store.Queries, run store.Run, persona store.Persona) (*ContinuationContext, error) {
	if run.ContinuationOfRunID == nil {
		return nil, nil
	}
	of, err := q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: run.CompanyID, ID: *run.ContinuationOfRunID})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err, "get continued run")
	}
	cc := &ContinuationContext{OfRunID: of.ID, OfRunName: of.Name, Reason: domain.ContinuationReason(run.ContinuationReason),
		Attempt: int(run.ContinuationAttempt), MaxAttempts: domain.MaxContinuations, PrevStatus: domain.RunStatus(of.Status),
		PrevError: of.Error, Decision: domain.RunDecision(of.Decision), DecisionNote: of.DecisionNote}
	if len(of.Diagnosis) > 0 {
		var d domain.RunDiagnosis
		if json.Unmarshal(of.Diagnosis, &d) == nil {
			cc.Diagnosis = &d
		}
	}
	if cc.Reason.ResumesSession() && of.SessionRef != "" && of.Agent == persona.Agent {
		cc.ResumeSession = of.SessionRef
	}
	rows, err := q.ListRunEventsBefore(ctx, store.ListRunEventsBeforeParams{CompanyID: run.CompanyID, RunID: of.ID, BeforeSeq: math.MaxInt64, Lim: 80})
	if err != nil {
		return nil, fmt.Errorf("list continued run events: %w", err)
	}
	for _, r := range rows { // newest first
		var tag string
		switch domain.RunEventKind(r.Kind) {
		case domain.EventAssistant:
			tag = "agente"
		case domain.EventToolUse:
			tag = "tool"
		case domain.EventToolResult:
			tag = "resultado"
		default:
			continue
		}
		line := tag + ": " + clipText(strings.Join(strings.Fields(r.Text), " "), 300)
		cc.Tail = append([]string{line}, cc.Tail...)
		if len(cc.Tail) >= continuationTail {
			break
		}
	}
	return cc, nil
}

// ---------- diagnóstico na leitura ----------

// fillResilience completa a visão da run com o modo de steer e, para runs que pararam sem diagnóstico gravado
// (falha, expiração, cancelamento), o card calculado na hora.
func (v *RunView) fillResilience() {
	v.SteerMode = domain.SteerModeFor(v.Agent)
	if v.Diagnosis == nil {
		v.Diagnosis = diagnoseTerminal(v.Run)
	}
	if v.Diagnosis != nil && v.Decision != domain.DecisionNone {
		d := *v.Diagnosis
		d.NextAction, d.Owner = domain.ActionNone, domain.OwnerHuman
		v.Diagnosis = &d
	}
}

// diagnoseTerminal calcula o card de uma run que terminou mal; nil para runs que terminaram bem ou ainda correm.
func diagnoseTerminal(r domain.Run) *domain.RunDiagnosis {
	d := domain.RunDiagnosis{Attempt: r.ContinuationAttempt, MaxAttempts: domain.MaxContinuations, NextAction: domain.ActionNone, Owner: domain.OwnerHuman}
	switch r.Status {
	case domain.RunFailed:
		d.Cause, d.CauseDetail = domain.CauseAgentError, clipText(strings.TrimSpace(r.Error), 300)
		d.NextAction = domain.ActionHumanDecision
	case domain.RunExpired:
		d.Cause, d.CauseDetail = domain.CauseTimeout, "a execução passou do tempo limite"
		d.NextAction = domain.ActionHumanDecision
	case domain.RunCancelled:
		d.Cause, d.CauseDetail = domain.CauseCancelled, "cancelada"
	case domain.RunInterrupted:
		d.Cause, d.CauseDetail = domain.CauseProcessRestart, "interrompida"
	default:
		return nil
	}
	return &d
}

// ---------- ações do operador (Tentar de novo / Continuar / Encerrar) ----------

// RunActionInput é a decisão do operador sobre uma run parada.
type RunActionInput struct {
	Action domain.RunDecision
	Note   string
}

// RunAction aplica a decisão do operador sobre uma run interrompida, falha ou expirada que ainda não foi decidida:
// retry (run nova do zero), continue (continuação com a sessão e o briefing) ou close (encerra o card; a task em
// andamento vai para blocked). Só humano; papel member.
func (s *Service) RunAction(ctx context.Context, actor domain.Actor, companyID, runID domain.ID, in RunActionInput) (RunView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return RunView{}, err
	}
	if err := requireHuman(actor); err != nil {
		return RunView{}, err
	}
	if !in.Action.Valid() {
		return RunView{}, domain.Invalid("action", fmt.Sprintf("ação desconhecida %q (retry, continue ou close)", in.Action))
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > maxDecisionNote {
		return RunView{}, domain.Invalid("note", fmt.Sprintf("a nota passa de %d caracteres", maxDecisionNote))
	}
	var (
		out     RunView
		blocked error
	)
	err := s.withTx(ctx, func(t *txc) error {
		peek, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		// same lock order as the rest of the code: the conversation or the task first, then the run
		var task store.Task
		switch {
		case peek.ConversationID != nil:
			if _, err := t.q.GetConversationForUpdate(ctx, store.GetConversationForUpdateParams{CompanyID: companyID, ID: *peek.ConversationID}); err != nil {
				return mapErr(err, "get conversation")
			}
		case peek.TaskID != nil:
			if task, err = t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: *peek.TaskID}); err != nil {
				return mapErr(err, "get task")
			}
		}
		cur, err := t.q.GetRunByIDForUpdate(ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		switch domain.RunStatus(cur.Status) {
		case domain.RunInterrupted, domain.RunFailed, domain.RunExpired:
		default:
			return domain.Invalid("run", fmt.Sprintf("a execução está %s: só uma execução interrompida, falha ou expirada aceita Tentar de novo, Continuar ou Encerrar", cur.Status))
		}
		switch domain.RunTrigger(cur.Trigger) {
		case domain.TriggerReview, domain.TriggerMemory, domain.TriggerSkillTest, domain.TriggerStatusCard:
			return domain.Invalid("run", "execuções de revisão e de curadoria de memória não têm ação do operador")
		}
		if cur.Decision != "" {
			return domain.Invalid("run", "esta execução já foi decidida: "+cur.Decision)
		}
		if _, cerr := t.q.ContinuationOfRun(ctx, &cur.ID); cerr == nil {
			return domain.Invalid("run", "esta execução já tem continuação")
		} else if !isNoRows(cerr) {
			return fmt.Errorf("continuation of run: %w", cerr)
		}
		reason := domain.ContinueRetry
		if in.Action == domain.DecisionContinue {
			reason = domain.ContinueManual
		}

		if cur.ConversationID != nil {
			conv, err := t.q.GetConversation(ctx, store.GetConversationParams{CompanyID: companyID, ID: *cur.ConversationID})
			if err != nil {
				return mapErr(err, "get conversation")
			}
			if in.Action == domain.DecisionClose {
				if err := t.addChatMessage(conv, domain.MessageSystem, nil, "O operador encerrou a execução interrompida. Envie uma nova mensagem para continuar a conversa.", &cur.ID); err != nil {
					return err
				}
			} else {
				c := &continuation{of: cur, reason: reason, attempt: nextAttempt(cur, reason)}
				_, berr, err := t.continueChatRun(cur, c)
				if err != nil {
					return err
				}
				if berr != nil {
					blocked = berr
					return nil // nothing is decided; the refusal's own side effects still commit
				}
			}
		} else {
			if task.ID == (domain.ID{}) {
				return domain.Invalid("run", "a execução não pertence a uma task")
			}
			if in.Action == domain.DecisionClose {
				if st := domain.TaskStatus(task.Status); st == domain.TaskTodo || st == domain.TaskInProgress {
					if err := t.q.SetTaskStatus(ctx, store.SetTaskStatusParams{ID: task.ID, Status: string(domain.TaskBlocked), Now: t.now}); err != nil {
						return fmt.Errorf("set task blocked: %w", err)
					}
					if err := t.activity(actor, companyID, "task.status_changed", "task", task.ID,
						map[string]any{"from": st, "to": domain.TaskBlocked, "run_id": cur.ID, "reason": "run encerrada pelo operador"}, ""); err != nil {
						return err
					}
				}
			} else {
				switch st := domain.TaskStatus(task.Status); {
				case st == domain.TaskDone || st == domain.TaskCancelled:
					return domain.Invalid("task", "a task já está encerrada")
				case !st.Workable():
					if err := t.q.SetTaskStatus(ctx, store.SetTaskStatusParams{ID: task.ID, Status: string(domain.TaskInProgress), Now: t.now}); err != nil {
						return fmt.Errorf("set task in_progress: %w", err)
					}
					if err := t.activity(actor, companyID, "task.status_changed", "task", task.ID,
						map[string]any{"from": st, "to": domain.TaskInProgress, "run_id": cur.ID}, ""); err != nil {
						return err
					}
				}
				if task.PersonaID == nil {
					return domain.Invalid("task", "a task não tem persona atribuída")
				}
			}
		}
		decided, err := t.q.SetRunDecision(ctx, store.SetRunDecisionParams{CompanyID: companyID, ID: cur.ID, Decision: string(in.Action),
			DecisionNote: note, DecidedByType: ptr(string(actor.Type)), DecidedByID: actor.ID, Now: &t.now})
		if err != nil {
			return mapErr(err, "set run decision")
		}
		if cur.ConversationID == nil && in.Action != domain.DecisionClose {
			// the next checkout of the task opens the continuation (or the fresh retry); wake the assignee now
			if err := t.q.MarkResumePending(ctx, store.MarkResumePendingParams{CompanyID: companyID, ID: cur.ID, ResumeReason: string(reason)}); err != nil {
				return fmt.Errorf("mark resume pending: %w", err)
			}
			if err := t.wake(companyID, task.ID, task.PersonaID, domain.TaskInProgress, domain.TriggerManual, true); err != nil {
				return err
			}
		}
		data := map[string]any{"decision": in.Action, "note": note, "status": cur.Status}
		if len(cur.Diagnosis) > 0 {
			data["diagnosis"] = json.RawMessage(cur.Diagnosis)
		}
		if err := t.activity(actor, companyID, "run.decision", "run", cur.ID, data, ""); err != nil {
			return err
		}
		t.publishRunStatus(runFromRow(decided))
		out, err = loadRunView(ctx, t.q, companyID, runID)
		return err
	})
	if err != nil {
		return RunView{}, err
	}
	if blocked != nil {
		return RunView{}, blocked
	}
	return out, nil
}

// ---------- steering (mudar o rumo de uma run ativa) ----------

// SteerResult diz como a mensagem do operador foi tratada.
type SteerResult struct {
	Mode     domain.SteerMode `json:"mode"`
	Followup domain.Followup  `json:"followup"`
}

// SteerRun entrega uma mensagem do operador a uma run ativa. Harness com injeção (claude, acp, fake): a mensagem
// segue como follow-up "steer" e o executor a injeta na run viva (o consumo é confirmado depois, em
// MarkSteerDelivered). Harness sem injeção (codex): a run é interrompida e a mensagem inicia a continuação.
func (s *Service) SteerRun(ctx context.Context, actor domain.Actor, companyID, runID domain.ID, body string) (SteerResult, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return SteerResult{}, err
	}
	if err := requireHuman(actor); err != nil {
		return SteerResult{}, err
	}
	body, err := cleanFollowup(body)
	if err != nil {
		return SteerResult{}, err
	}
	var res SteerResult
	err = s.withTx(ctx, func(t *txc) error {
		peek, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		var (
			task store.Task
			conv store.Conversation
		)
		switch {
		case peek.ConversationID != nil:
			if conv, err = t.q.GetConversationForUpdate(ctx, store.GetConversationForUpdateParams{CompanyID: companyID, ID: *peek.ConversationID}); err != nil {
				return mapErr(err, "get conversation")
			}
		case peek.TaskID != nil:
			if task, err = t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: *peek.TaskID}); err != nil {
				return mapErr(err, "get task")
			}
		}
		cur, err := t.q.GetRunByIDForUpdate(ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		if !domain.RunStatus(cur.Status).Active() {
			return domain.Invalid("run", fmt.Sprintf("a execução já está %s: não há o que redirecionar", cur.Status))
		}
		switch domain.RunTrigger(cur.Trigger) {
		case domain.TriggerReview, domain.TriggerMemory, domain.TriggerSkillTest, domain.TriggerStatusCard:
			return domain.Invalid("run", "execuções de revisão e de curadoria de memória não aceitam steer")
		}
		mode := domain.SteerModeFor(cur.Agent)
		if domain.RunStatus(cur.Status) == domain.RunQueued {
			mode = domain.SteerRestart // nothing running yet to inject into: the queued run is replaced by one that starts with the message
		}
		if mode == domain.SteerNone {
			return domain.Invalid("run", "o harness desta execução não aceita mensagens durante a execução")
		}
		delivery := domain.DeliverySteer
		if mode == domain.SteerRestart {
			delivery = domain.DeliveryRestart
		}
		res.Mode = mode
		actorAuthor := actor
		var fu domain.Followup
		if cur.ConversationID != nil {
			pending, err := t.q.ListPendingConversationMessages(ctx, conv.ID)
			if err != nil {
				return fmt.Errorf("list pending messages: %w", err)
			}
			if len(pending) >= maxPendingMessages {
				return domain.Invalid("message", fmt.Sprintf("já há %d mensagens esperando a resposta; aguarde a persona", len(pending)))
			}
			m, err := t.storeChatMessage(conv, actorAuthor, body, nil)
			if err != nil {
				return err
			}
			if err := t.q.SetConversationMessageDelivery(ctx, store.SetConversationMessageDeliveryParams{ID: m.ID, Delivery: string(delivery)}); err != nil {
				return fmt.Errorf("set message delivery: %w", err)
			}
			m.Delivery = string(delivery)
			fu = chatFollowupFromRow(m)
		} else {
			if task.ID == (domain.ID{}) {
				return domain.Invalid("run", "a execução não pertence a uma task")
			}
			pending, err := t.q.ListPendingTaskFollowups(ctx, task.ID)
			if err != nil {
				return fmt.Errorf("list pending followups: %w", err)
			}
			if len(pending) >= domain.MaxPendingFollowups {
				return domain.Invalid("message", fmt.Sprintf("já há %d follow-ups esperando; aguarde a persona", len(pending)))
			}
			row, err := t.q.InsertTaskFollowup(ctx, store.InsertTaskFollowupParams{ID: domain.NewID(), CompanyID: companyID, TaskID: task.ID,
				AuthorType: string(actor.Type), AuthorID: actor.ID, Body: body, Delivery: string(delivery), WaitingRunID: &cur.ID, Now: t.now})
			if err != nil {
				return fmt.Errorf("insert followup: %w", err)
			}
			if err := t.activity(actor, companyID, "followup.queued", "task", task.ID,
				map[string]any{"followup_id": row.ID, "delivery": delivery, "run_id": cur.ID}, ""); err != nil {
				return err
			}
			fu = taskFollowupFromRow(row)
		}
		fu.WaitRunID = &cur.ID
		res.Followup = fu
		if err := t.activity(actor, companyID, "run.steered", "run", cur.ID,
			map[string]any{"mode": mode, "followup_id": fu.ID, "agent": cur.Agent}, ""); err != nil {
			return err
		}
		if mode == domain.SteerInject {
			fu.WaitReason = domain.WaitSteerPending
			res.Followup = fu
			t.publish(events.RunTopic(cur.ID.String()), RunSteerMsg, SteerEvent{RunID: cur.ID, FollowupID: fu.ID, Text: body})
			return nil
		}
		// restart: the run is cancelled and the next one continues it with the message (session kept when the harness resumes)
		if err := t.q.MarkResumePending(ctx, store.MarkResumePendingParams{CompanyID: companyID, ID: cur.ID, ResumeReason: string(domain.ContinueSteer)}); err != nil {
			return fmt.Errorf("mark resume pending: %w", err)
		}
		fu.WaitReason = domain.WaitRestartPending
		res.Followup = fu
		return t.cancelRun(actor, companyID, cur, map[string]any{"reason": "steer"})
	})
	if err != nil {
		return SteerResult{}, err
	}
	return res, nil
}

// MarkSteerDelivered confirma que o adapter entregou o follow-up ao harness: ele deixa de ser pendente, passa a
// pertencer à run (comentário humano na task, mensagem humana da run no chat) e o log da run registra a entrega.
func (s *Service) MarkSteerDelivered(ctx context.Context, companyID, runID, followupID domain.ID) error {
	var text string
	err := s.withTx(ctx, func(t *txc) error {
		run, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		if run.TaskID != nil {
			task, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: *run.TaskID})
			if err != nil {
				return mapErr(err, "get task")
			}
			f, err := t.q.GetTaskFollowup(ctx, store.GetTaskFollowupParams{CompanyID: companyID, ID: followupID})
			if err != nil {
				return mapErr(err, "get followup")
			}
			if f.TaskID != task.ID || f.Status != string(domain.FollowupPending) {
				return domain.Invalid("followup", "o follow-up não está mais pendente")
			}
			text = f.Body
			return t.consumeTaskFollowup(f, run, "followup.steered")
		}
		if run.ConversationID == nil {
			return domain.Invalid("run", "run sem task nem conversa")
		}
		if _, err := t.q.GetConversationForUpdate(ctx, store.GetConversationForUpdateParams{CompanyID: companyID, ID: *run.ConversationID}); err != nil {
			return mapErr(err, "get conversation")
		}
		m, err := t.q.SteerConversationMessage(ctx, store.SteerConversationMessageParams{CompanyID: companyID, ID: followupID, RunID: &runID})
		if err != nil {
			if isNoRows(err) {
				return domain.Invalid("followup", "o follow-up não está mais pendente")
			}
			return mapErr(err, "steer message")
		}
		text = m.Body
		return t.activity(domain.SystemActor, companyID, "followup.steered", "conversation", m.ConversationID,
			map[string]any{"followup_id": m.ID, "run_id": runID}, "")
	})
	if err != nil {
		return err
	}
	// the log shows the moment the course changed (best effort: the delivery itself already happened)
	_, _ = s.AppendRunEvent(ctx, companyID, runID, adapterSystemEvent("mensagem do operador injetada na execução: "+clipText(strings.Join(strings.Fields(text), " "), 240)))
	return nil
}

// ---------- fila de follow-ups ----------

func cleanFollowup(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", domain.Invalid("body", "escreva algo antes de enviar")
	}
	if len(body) > domain.MaxFollowupBytes {
		return "", domain.Invalid("body", fmt.Sprintf("o follow-up passa de %d bytes", domain.MaxFollowupBytes))
	}
	return strings.ToValidUTF8(body, ""), nil
}

func taskFollowupFromRow(r store.TaskFollowup) domain.Followup {
	tid := r.TaskID
	return domain.Followup{ID: r.ID, CompanyID: r.CompanyID, Kind: domain.FollowupTask, TaskID: &tid, Body: r.Body,
		Author: actorOf(r.AuthorType, r.AuthorID), Delivery: domain.FollowupDelivery(r.Delivery), CreatedAt: r.CreatedAt, EditedAt: r.EditedAt}
}

func chatFollowupFromRow(m store.ConversationMessage) domain.Followup {
	cid := m.ConversationID
	return domain.Followup{ID: m.ID, CompanyID: m.CompanyID, Kind: domain.FollowupChat, ConversationID: &cid, Body: m.Body,
		Author: actorOf(m.AuthorType, m.AuthorID), Delivery: domain.FollowupDelivery(m.Delivery), CreatedAt: m.CreatedAt, EditedAt: m.EditedAt}
}

// FollowupTarget identifica a fila: uma task ou uma conversa (exatamente uma).
type FollowupTarget struct {
	TaskID         *domain.ID
	ConversationID *domain.ID
}

// personaWhy explica por que a persona não pode trabalhar agora ("" = pode).
func personaWhy(p store.Persona) string {
	st := domain.PersonaStatus(p.Status)
	if st.CanWork() {
		return ""
	}
	label := string(st)
	switch st {
	case domain.PersonaPaused:
		label = "pausada"
	case domain.PersonaError:
		label = "em erro"
	case domain.PersonaOff:
		label = "desligada"
	case domain.PersonaAwaitingGate:
		label = "esperando uma decisão (gate)"
	}
	why := fmt.Sprintf("%s está %s", p.Name, label)
	if st == domain.PersonaPaused && p.PauseReason != nil && strings.TrimSpace(*p.PauseReason) != "" {
		why += " (" + strings.TrimSpace(*p.PauseReason) + ")"
	}
	return why
}

// waitState é o que impede a fila de andar agora.
type waitState struct {
	active    *store.Run
	undecided *store.Run
	assigned  bool
	why       string
}

func (w waitState) reasonFor(delivery domain.FollowupDelivery) (domain.FollowupWait, string, *domain.ID) {
	if w.undecided != nil {
		return domain.WaitDecisionPending, w.undecided.Name, &w.undecided.ID
	}
	if w.active != nil {
		id := w.active.ID
		switch delivery {
		case domain.DeliverySteer:
			return domain.WaitSteerPending, w.active.Name, &id
		case domain.DeliveryRestart:
			return domain.WaitRestartPending, w.active.Name, &id
		}
		return domain.WaitRunActive, w.active.Name, &id
	}
	if !w.assigned {
		return domain.WaitUnassigned, "", nil
	}
	if w.why != "" {
		return domain.WaitPersonaUnavailable, w.why, nil
	}
	return domain.WaitStarting, "", nil
}

func taskWait(ctx context.Context, q *store.Queries, task store.Task) (waitState, error) {
	w := waitState{assigned: task.PersonaID != nil}
	if r, err := q.ActiveRunForTask(ctx, task.ID); err == nil {
		w.active = &r
	} else if !isNoRows(err) {
		return w, fmt.Errorf("active run for task: %w", err)
	}
	if r, err := q.UndecidedRunForTask(ctx, &task.ID); err == nil {
		w.undecided = &r
	} else if !isNoRows(err) {
		return w, fmt.Errorf("undecided run for task: %w", err)
	}
	if task.PersonaID != nil {
		p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: task.CompanyID, ID: *task.PersonaID})
		if err != nil {
			return w, mapErr(err, "get persona")
		}
		w.why = personaWhy(p)
	}
	return w, nil
}

func conversationWait(ctx context.Context, q *store.Queries, conv store.Conversation) (waitState, error) {
	w := waitState{assigned: true}
	if r, err := q.ActiveRunForConversation(ctx, &conv.ID); err == nil {
		w.active = &r
	} else if !isNoRows(err) {
		return w, fmt.Errorf("active run for conversation: %w", err)
	}
	if r, err := q.UndecidedRunForConversation(ctx, &conv.ID); err == nil {
		w.undecided = &r
	} else if !isNoRows(err) {
		return w, fmt.Errorf("undecided run for conversation: %w", err)
	}
	if conv.PersonaID == nil { // a room (ADR-0033) has no single persona: only the runs explain the wait
		return w, nil
	}
	p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: conv.CompanyID, ID: *conv.PersonaID})
	if err != nil {
		return w, mapErr(err, "get persona")
	}
	w.why = personaWhy(p)
	return w, nil
}

// ListFollowups lists the pending follow-ups of a task or a conversation in consumption order, each with the reason it
// still waits.
func (s *Service) ListFollowups(ctx context.Context, companyID domain.ID, target FollowupTarget) ([]domain.Followup, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	return listFollowups(ctx, s.read(), companyID, target)
}

func listFollowups(ctx context.Context, q *store.Queries, companyID domain.ID, target FollowupTarget) ([]domain.Followup, error) {
	var out []domain.Followup
	switch {
	case target.TaskID != nil:
		task, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: *target.TaskID})
		if err != nil {
			return nil, mapErr(err, "get task")
		}
		rows, err := q.ListPendingTaskFollowups(ctx, task.ID)
		if err != nil {
			return nil, fmt.Errorf("list followups: %w", err)
		}
		w, err := taskWait(ctx, q, task)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, taskFollowupFromRow(r))
		}
		decorate(out, w)
	case target.ConversationID != nil:
		conv, err := q.GetConversation(ctx, store.GetConversationParams{CompanyID: companyID, ID: *target.ConversationID})
		if err != nil {
			return nil, mapErr(err, "get conversation")
		}
		rows, err := q.ListPendingConversationMessages(ctx, conv.ID)
		if err != nil {
			return nil, fmt.Errorf("list pending messages: %w", err)
		}
		w, err := conversationWait(ctx, q, conv)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, chatFollowupFromRow(r))
		}
		decorate(out, w)
	default:
		return nil, domain.Invalid("target", "informe a task ou a conversa")
	}
	return out, nil
}

func decorate(fs []domain.Followup, w waitState) {
	for i := range fs {
		fs[i].Position = i + 1
		fs[i].WaitReason, fs[i].WaitDetail, fs[i].WaitRunID = w.reasonFor(fs[i].Delivery)
	}
}

// QueueTaskFollowup enfileira uma instrução para a task. Com uma run ativa ela espera (ou é injetada, ver SteerRun);
// sem run ativa a persona é acordada e a run que abrir já a recolhe. Só humano; papel member.
func (s *Service) QueueTaskFollowup(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, body string) (domain.Followup, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.Followup{}, err
	}
	if err := requireHuman(actor); err != nil {
		return domain.Followup{}, err
	}
	body, err := cleanFollowup(body)
	if err != nil {
		return domain.Followup{}, err
	}
	var out domain.Followup
	err = s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if !domain.TaskStatus(task.Status).Open() {
			return domain.Invalid("task", "a task está encerrada")
		}
		if system, err := t.isSystemTask(task.ID); err != nil {
			return err
		} else if system {
			return domain.Invalid("task", "a task de sistema da curadoria não aceita follow-ups")
		}
		pending, err := t.q.ListPendingTaskFollowups(ctx, task.ID)
		if err != nil {
			return fmt.Errorf("list pending followups: %w", err)
		}
		if len(pending) >= domain.MaxPendingFollowups {
			return domain.Invalid("body", fmt.Sprintf("já há %d follow-ups esperando; aguarde a persona", len(pending)))
		}
		var waiting *domain.ID
		if r, err := t.q.ActiveRunForTask(ctx, task.ID); err == nil {
			waiting = &r.ID
		} else if !isNoRows(err) {
			return fmt.Errorf("active run for task: %w", err)
		}
		row, err := t.q.InsertTaskFollowup(ctx, store.InsertTaskFollowupParams{ID: domain.NewID(), CompanyID: companyID, TaskID: task.ID,
			AuthorType: string(actor.Type), AuthorID: actor.ID, Body: body, Delivery: string(domain.DeliveryQueue), WaitingRunID: waiting, Now: t.now})
		if err != nil {
			return fmt.Errorf("insert followup: %w", err)
		}
		if err := t.activity(actor, companyID, "followup.queued", "task", task.ID,
			map[string]any{"followup_id": row.ID, "delivery": domain.DeliveryQueue, "waiting_run_id": waiting}, ""); err != nil {
			return err
		}
		if waiting == nil {
			// nobody is running: wake the assignee, whose run collects the follow-up in order
			if err := t.wake(companyID, task.ID, task.PersonaID, domain.TaskStatus(task.Status), domain.TriggerComment, false); err != nil {
				return err
			}
		}
		fs, err := listFollowups(ctx, t.q, companyID, FollowupTarget{TaskID: &task.ID})
		if err != nil {
			return err
		}
		for _, f := range fs {
			if f.ID == row.ID {
				out = f
			}
		}
		return nil
	})
	return out, err
}

// followupOwner locks the owner of a follow-up (task or conversation) before touching it, so the edit never races
// with the run that is collecting the queue.
func (t *txc) lockFollowupOwner(companyID, id domain.ID) (kind domain.FollowupKind, target FollowupTarget, err error) {
	if f, ferr := t.q.GetTaskFollowup(t.ctx, store.GetTaskFollowupParams{CompanyID: companyID, ID: id}); ferr == nil {
		if _, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: f.TaskID}); err != nil {
			return "", target, mapErr(err, "get task")
		}
		tid := f.TaskID
		return domain.FollowupTask, FollowupTarget{TaskID: &tid}, nil
	} else if !isNoRows(ferr) {
		return "", target, fmt.Errorf("get followup: %w", ferr)
	}
	m, merr := t.q.GetConversationMessage(t.ctx, store.GetConversationMessageParams{CompanyID: companyID, ID: id})
	if merr != nil {
		return "", target, mapErr(merr, "get followup")
	}
	if m.AuthorType != string(domain.MessageHuman) {
		return "", target, fmt.Errorf("followup: %w", domain.ErrNotFound)
	}
	if _, err := t.q.GetConversationForUpdate(t.ctx, store.GetConversationForUpdateParams{CompanyID: companyID, ID: m.ConversationID}); err != nil {
		return "", target, mapErr(err, "get conversation")
	}
	cid := m.ConversationID
	return domain.FollowupChat, FollowupTarget{ConversationID: &cid}, nil
}

// EditFollowup changes the text of a follow-up that was not consumed yet.
func (s *Service) EditFollowup(ctx context.Context, actor domain.Actor, companyID, id domain.ID, body string) (domain.Followup, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.Followup{}, err
	}
	if err := requireHuman(actor); err != nil {
		return domain.Followup{}, err
	}
	body, err := cleanFollowup(body)
	if err != nil {
		return domain.Followup{}, err
	}
	var out domain.Followup
	err = s.withTx(ctx, func(t *txc) error {
		kind, target, err := t.lockFollowupOwner(companyID, id)
		if err != nil {
			return err
		}
		subject, subjectID := "task", idOrZeroPtr(target.TaskID)
		if kind == domain.FollowupChat {
			subject, subjectID = "conversation", idOrZeroPtr(target.ConversationID)
			if _, err := t.q.UpdatePendingConversationMessage(ctx, store.UpdatePendingConversationMessageParams{CompanyID: companyID, ID: id, Body: body, Now: &t.now}); err != nil {
				if isNoRows(err) {
					return domain.Invalid("followup", "a mensagem já foi entregue à persona e não pode mais ser editada")
				}
				return mapErr(err, "update message")
			}
		} else if _, err := t.q.UpdateTaskFollowupBody(ctx, store.UpdateTaskFollowupBodyParams{CompanyID: companyID, ID: id, Body: body, Now: &t.now}); err != nil {
			if isNoRows(err) {
				return domain.Invalid("followup", "o follow-up já foi entregue à persona e não pode mais ser editado")
			}
			return mapErr(err, "update followup")
		}
		if err := t.activity(actor, companyID, "followup.edited", subject, subjectID, map[string]any{"followup_id": id}, ""); err != nil {
			return err
		}
		fs, err := listFollowups(ctx, t.q, companyID, target)
		if err != nil {
			return err
		}
		for _, f := range fs {
			if f.ID == id {
				out = f
			}
		}
		return nil
	})
	return out, err
}

// DiscardFollowup drops a follow-up that was not consumed yet.
func (s *Service) DiscardFollowup(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return err
	}
	if err := requireHuman(actor); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		kind, target, err := t.lockFollowupOwner(companyID, id)
		if err != nil {
			return err
		}
		subject, subjectID := "task", idOrZeroPtr(target.TaskID)
		if kind == domain.FollowupChat {
			subject, subjectID = "conversation", idOrZeroPtr(target.ConversationID)
			n, err := t.q.DeletePendingConversationMessage(ctx, store.DeletePendingConversationMessageParams{CompanyID: companyID, ID: id})
			if err != nil {
				return fmt.Errorf("delete message: %w", err)
			}
			if n == 0 {
				return domain.Invalid("followup", "a mensagem já foi entregue à persona e não pode mais ser descartada")
			}
		} else if _, err := t.q.DiscardTaskFollowup(ctx, store.DiscardTaskFollowupParams{CompanyID: companyID, ID: id, Now: &t.now}); err != nil {
			if isNoRows(err) {
				return domain.Invalid("followup", "o follow-up já foi entregue à persona e não pode mais ser descartado")
			}
			return mapErr(err, "discard followup")
		}
		return t.activity(actor, companyID, "followup.discarded", subject, subjectID, map[string]any{"followup_id": id}, "")
	})
}

func idOrZeroPtr(p *domain.ID) domain.ID {
	if p == nil {
		return domain.ID{}
	}
	return *p
}

// consumeTaskFollowup turns a pending follow-up into a human comment of the task (in the thread the agent reads) and
// marks it consumed by run.
func (t *txc) consumeTaskFollowup(f store.TaskFollowup, run store.Run, verb string) error {
	c, err := t.q.CreateComment(t.ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: f.CompanyID, TaskID: f.TaskID,
		AuthorType: f.AuthorType, AuthorID: f.AuthorID, Body: f.Body, EventID: ptr("followup:" + f.ID.String()), Now: t.now})
	if err != nil {
		return mapErr(err, "post followup comment")
	}
	if _, err := t.q.ConsumeTaskFollowup(t.ctx, store.ConsumeTaskFollowupParams{CompanyID: f.CompanyID, ID: f.ID, RunID: &run.ID,
		Now: &t.now, CommentID: &c.ID}); err != nil {
		return mapErr(err, "consume followup")
	}
	return t.activity(domain.SystemActor, f.CompanyID, verb, "task", f.TaskID,
		map[string]any{"followup_id": f.ID, "run_id": run.ID, "comment_id": c.ID}, "")
}

// claimTaskFollowups hands every pending follow-up of the task to the run that was just opened, in order.
func (t *txc) claimTaskFollowups(task store.Task, run store.Run) error {
	rows, err := t.q.ListPendingTaskFollowups(t.ctx, task.ID)
	if err != nil {
		return fmt.Errorf("list pending followups: %w", err)
	}
	for _, f := range rows {
		if err := t.consumeTaskFollowup(f, run, "followup.consumed"); err != nil {
			return err
		}
	}
	return nil
}

// wakeForFollowups wakes the assignee when follow-ups are still pending after a run ended (whatever its end), so the
// next run collects them. The task is read without a lock: the run lock is already held here and everywhere else the
// task is locked first, so locking it now could deadlock. A follow-up queued at the very same instant that this read
// misses is picked up by the heartbeat's safety net (a pending follow-up older than a minute).
func (t *txc) wakeForFollowups(companyID domain.ID, task store.Task) error {
	cur, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: task.ID})
	if err != nil {
		return mapErr(err, "get task")
	}
	pending, err := t.q.ListPendingTaskFollowups(t.ctx, cur.ID)
	if err != nil {
		return fmt.Errorf("list pending followups: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	return t.wake(companyID, cur.ID, cur.PersonaID, domain.TaskStatus(cur.Status), domain.TriggerComment, false)
}

// wakeForFollowupsOfRun is wakeForFollowups for a run row (a queued run that was cancelled before it started).
func (t *txc) wakeForFollowupsOfRun(run store.Run) error {
	if run.TaskID == nil {
		return nil
	}
	task, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: run.CompanyID, ID: *run.TaskID})
	if err != nil {
		return mapErr(err, "get task")
	}
	return t.wakeForFollowups(run.CompanyID, task)
}

func adapterSystemEvent(text string) adapter.Event {
	return adapter.Event{Kind: domain.EventSystem, Text: text}
}
