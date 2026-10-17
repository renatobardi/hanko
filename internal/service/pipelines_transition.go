package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Transições, sugestões, automação e o pacote de contexto dos cases (ADR-0034).

// TransitionCaseInput move um case para outra etapa do pipeline.
type TransitionCaseInput struct {
	ExpectedVersion int
	ToStage         string
	// Force passa por cima do modo estrito (fora das arestas), com Reason; só member humano.
	Force  bool
	Reason string
	RunID  *domain.ID
	TaskID *domain.ID
}

// SuggestInput é a sugestão de uma persona (ou de um humano) para mover o case.
type SuggestInput struct {
	ExpectedVersion int
	ToStage         string
	Rationale       string
	Confidence      float64
	RunID, TaskID   *domain.ID
}

// ResolveSuggestionInput aceita (move para a etapa sugerida) ou dispensa a sugestão pendente.
type ResolveSuggestionInput struct {
	ExpectedVersion int
	Accept          bool
	Reason          string
}

// reviewMove é a decisão de revisão que provoca a movimentação.
type reviewMove struct {
	decision domain.ReviewDecision
	reason   string
	snapshot map[string]any
}

type moveOpts struct {
	actor         domain.Actor
	role          domain.MemberRole
	runID, taskID *domain.ID
	force         bool
	reason        string
	review        *reviewMove
	accepted      *domain.CaseSuggestion
}

const (
	maxContextSummaryBytes = 24 << 10
	maxContextFieldsBytes  = 12 << 10
	maxLearningBytes       = 4 << 10
	maxLearningItems       = 8
	maxLearningItemRunes   = 400
	maxContextHistory      = 10
)

// approverOK diz se quem age pode tirar o case de uma etapa com aprovação.
func approverOK(c domain.StageConfig, actor domain.Actor, role domain.MemberRole) bool {
	if !c.ApprovalRequired() {
		return true
	}
	ref, err := domain.ParseApprover(c.Approval.Approver)
	if err != nil {
		return false
	}
	switch actor.Type {
	case domain.ActorSystem:
		return true
	case domain.ActorPersona:
		return ref.PersonaID != nil && actor.ID != nil && *ref.PersonaID == *actor.ID
	}
	switch {
	case ref.AnyMember:
		return role.AtLeast(domain.MemberMember)
	case ref.UserID != nil:
		return (actor.ID != nil && *actor.ID == *ref.UserID) || role.AtLeast(domain.MemberAdmin)
	}
	// persona aprovadora: um admin humano ainda pode destravar
	return role.AtLeast(domain.MemberAdmin)
}

// callerRoleOf é o papel do chamador para as regras de aprovação (sem identidade = owner, como em require).
func (s *Service) callerRoleOf(ctx context.Context, companyID domain.ID) (domain.MemberRole, error) {
	role, _, err := s.callerRole(ctx, companyID)
	return role, err
}

// moveCase move o case (já travado e com a versão conferida) para a etapa to, aplicando as regras de saída e de
// entrada, e roda os efeitos: evento, terminal, contadores do pai, bloqueios, comentário nas tasks e automação.
func (t *txc) moveCase(c store.Case, prefix string, pv PipelineView, to domain.Stage, o moveOpts) (store.Case, error) {
	if pv.ArchivedAt != nil {
		return c, domain.Invalid("pipeline", "the pipeline is archived")
	}
	if c.StageID == nil {
		return c, domain.Invalid("case", "a loose case changes status, not stage")
	}
	from, ok := pv.StageByID(*c.StageID)
	if !ok {
		return c, notFound("stage")
	}
	if to.ID == from.ID {
		return c, domain.Invalid("to_stage", "the case is already in "+to.Name)
	}
	if to.Config.Disabled {
		reason := to.Config.DisabledReason
		if reason == "" {
			reason = "the stage is disabled"
		}
		return c, domain.Invalid("to_stage", to.Name+": "+reason)
	}
	persona := o.actor.Type == domain.ActorPersona
	if persona && (o.force || o.review != nil) {
		return c, fmt.Errorf("a persona never forces a transition nor decides a review: %w", domain.ErrNotAllowed)
	}
	if o.review == nil && from.Kind == domain.StageReview {
		return c, domain.Invalid("to_stage", "the case is in review: approve, request changes or reject it")
	}
	if persona && (from.Kind != domain.StageWorking || to.Kind != domain.StageWorking) {
		return c, fmt.Errorf("a persona moves cases only between working stages; use hanko_case_suggest: %w", domain.ErrNotAllowed)
	}
	if !approverOK(from.Config, o.actor, o.role) {
		return c, fmt.Errorf("only the approver of %s can move the case out of it: %w", from.Name, domain.ErrNotAllowed)
	}
	cancel := to.Kind == domain.StageCancelled
	approve := o.review == nil || o.review.decision == domain.ReviewApprove
	if !cancel && approve {
		if from.Config.RequireChildrenTerminal() && c.ChildCount != c.TerminalChildCount {
			return c, fmt.Errorf("%d of %d child case(s) still open: %w", c.ChildCount-c.TerminalChildCount, c.ChildCount, domain.ErrCaseBlocked)
		}
		n, err := t.q.CountOpenBlockers(t.ctx, store.CountOpenBlockersParams{CompanyID: c.CompanyID, CaseID: c.ID})
		if err != nil {
			return c, fmt.Errorf("count blockers: %w", err)
		}
		if n > 0 {
			return c, fmt.Errorf("%d open blocker(s): %w", n, domain.ErrCaseBlocked)
		}
	}
	forced := false
	if pv.EnforceTransitions && o.review == nil {
		has, err := t.q.HasTransition(t.ctx, store.HasTransitionParams{PipelineID: pv.ID, FromStageID: from.ID, ToStageID: to.ID})
		if err != nil {
			return c, fmt.Errorf("check transition: %w", err)
		}
		if !has {
			if !o.force {
				return c, domain.Invalid("to_stage", fmt.Sprintf("%s → %s is not an allowed transition (strict mode); force it with a reason", from.Name, to.Name))
			}
			if o.actor.Type != domain.ActorHuman || !o.role.AtLeast(domain.MemberMember) {
				return c, fmt.Errorf("only a human member forces a transition: %w", domain.ErrNotAllowed)
			}
			if strings.TrimSpace(o.reason) == "" {
				return c, domain.Invalid("reason", "forcing a transition needs a reason")
			}
			forced = true
		}
	}
	if c.ReapprovalRequired && o.review == nil && to.Kind != domain.StageReview && !cancel {
		return c, domain.ErrReviewOutdated
	}
	if !cancel && approve {
		fields := caseFromRow(c, prefix).Fields
		if err := checkStageFields(from, fields, true, false); err != nil {
			return c, err
		}
	}

	before := c
	st := domain.StatusForStage(to.Kind)
	c.Status, c.StageID = string(st), &to.ID
	dTerminal := applyTerminal(&c, st, t.now)
	c.Attention, c.AttentionAt = nil, nil
	hadSuggestion := len(c.PendingSuggestion) > 0
	c.PendingSuggestion = nil
	if o.review != nil && o.review.decision == domain.ReviewApprove {
		v := c.Version + 1
		c.ApprovedVersion, c.ReapprovalRequired = &v, false
	}
	saved, err := t.saveCase(c)
	if err != nil {
		return c, err
	}
	payload := map[string]any{"from": from.Key, "from_name": from.Name, "to": to.Key, "to_name": to.Name}
	if r := strings.TrimSpace(o.reason); r != "" {
		payload["reason"] = r
	}
	kind := domain.CaseEvTransitioned
	switch {
	case o.review != nil:
		kind = domain.CaseEvReviewDecided
		payload["decision"] = o.review.decision
		payload["approved_version"] = int(saved.Version)
		if o.review.snapshot != nil {
			payload["snapshot"] = o.review.snapshot
		}
	case forced:
		kind = domain.CaseEvTransitionForced
		payload["forced"], payload["decision"] = true, "forced"
	}
	if o.accepted != nil {
		payload["suggestion_id"] = o.accepted.ID.String()
	}
	ev, err := t.caseEvent(c.CompanyID, c.ID, kind, o.actor, o.runID, &from.ID, &to.ID, payload)
	if err != nil {
		return c, err
	}
	if hadSuggestion {
		res := "superseded"
		if o.accepted != nil {
			res = "accepted"
		}
		if _, err := t.caseEvent(c.CompanyID, c.ID, domain.CaseEvSuggestionResolved, o.actor, o.runID, nil, nil, map[string]any{"resolution": res}); err != nil {
			return c, err
		}
	}
	if err := t.afterStatus(before, saved, dTerminal, o.actor, o.runID); err != nil {
		return c, err
	}
	if err := t.commentLinkedTasks(saved, prefix, from, to, o); err != nil {
		return c, err
	}
	if err := t.linkPersonaTask(c.CompanyID, c.ID, o.actor, o.taskID, o.runID, domain.LinkWork); err != nil {
		return c, err
	}
	if to.Config.Automation.Enabled() {
		if err := t.startAutomation(saved, prefix, pv, to, ev.ID); err != nil {
			return c, err
		}
	}
	verb := "case.transitioned"
	if o.review != nil {
		verb = "case.reviewed"
	}
	if err := t.activity(o.actor, c.CompanyID, verb, "case", c.ID, map[string]any{"identifier": domain.CaseIdentifier(prefix, c.Number),
		"from": from.Key, "to": to.Key, "forced": forced}, ""); err != nil {
		return c, err
	}
	return saved, nil
}

// commentLinkedTasks deixa um comentário de sistema nas tasks abertas ligadas ao case.
func (t *txc) commentLinkedTasks(c store.Case, prefix string, from, to domain.Stage, o moveOpts) error {
	links, err := t.q.ListCaseLinks(t.ctx, store.ListCaseLinksParams{CompanyID: c.CompanyID, CaseID: c.ID})
	if err != nil {
		return fmt.Errorf("list case links: %w", err)
	}
	body := fmt.Sprintf("Case %s (%s) passou de **%s** para **%s**.", domain.CaseIdentifier(prefix, c.Number), c.Title, from.Name, to.Name)
	if o.review != nil {
		body += " Decisão de revisão: " + string(o.review.decision) + "."
	}
	if r := strings.TrimSpace(o.reason); r != "" {
		body += "\n\nMotivo: " + clipRunes(r, 2000)
	}
	for _, l := range links {
		if !domain.TaskStatus(l.TaskStatus).Open() {
			continue
		}
		if o.taskID != nil && l.TaskID == *o.taskID {
			continue // a persona que moveu já sabe
		}
		if _, err := t.q.CreateComment(t.ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: c.CompanyID, TaskID: l.TaskID,
			AuthorType: string(domain.ActorSystem), Body: body, Now: t.now}); err != nil {
			return fmt.Errorf("comment linked task: %w", err)
		}
	}
	return nil
}

// clip corta s em n runas com reticências.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// clipBytes corta s em até n bytes (sem partir uma runa), com aviso.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n\n… (cortado)"
}

// startAutomation cria a task da persona da etapa com o pacote de contexto. A chave de idempotência é
// (case, etapa, evento de entrada): repetir a mesma entrada não duplica a task. Persona ausente ou projeto
// arquivado não derrubam a movimentação: viram o evento automation_failed.
func (t *txc) startAutomation(c store.Case, prefix string, pv PipelineView, st domain.Stage, eventID domain.ID) error {
	a := st.Config.Automation
	fail := func(reason string) error {
		_, err := t.caseEvent(c.CompanyID, c.ID, domain.CaseEvAutomationFailed, domain.SystemActor, nil, nil, &st.ID,
			map[string]any{"stage": st.Key, "reason": reason})
		return err
	}
	p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: c.CompanyID, ID: *a.PersonaID})
	if err != nil {
		if isNoRows(err) {
			return fail("persona not found")
		}
		return fmt.Errorf("load automation persona: %w", err)
	}
	body, err := t.s.buildCaseContext(t.ctx, t.q, c.CompanyID, c, prefix, contextOpts{pipeline: &pv, stage: &st, automation: true})
	if err != nil {
		return err
	}
	project := activeProject(t.ctx, t.q, c.CompanyID, c.ProjectID)
	if project == nil {
		project = activeProject(t.ctx, t.q, c.CompanyID, pv.ProjectID)
	}
	title := clipRunes(st.Name+": "+c.Title, 500)
	tv, err := t.createTask(domain.SystemActor, c.CompanyID, CreateTaskInput{Title: title, Description: body, PersonaID: &p.ID, ProjectID: project,
		Priority: a.Priority, EventID: "case-automation:" + c.ID.String() + ":" + st.ID.String() + ":" + eventID.String()})
	if err != nil {
		if isValidation(err) {
			return fail(err.Error())
		}
		return err
	}
	if _, err := t.q.LinkCaseTask(t.ctx, store.LinkCaseTaskParams{CaseID: c.ID, TaskID: tv.ID, CompanyID: c.CompanyID, Role: string(domain.LinkAutomation), Now: t.now}); err != nil {
		return mapErr(err, "link automation task")
	}
	_, err = t.caseEvent(c.CompanyID, c.ID, domain.CaseEvAutomationStarted, domain.SystemActor, nil, nil, &st.ID,
		map[string]any{"stage": st.Key, "task_id": tv.ID.String(), "task_key": tv.Key, "persona": p.Slug})
	return err
}

func isValidation(err error) bool {
	var ve *domain.ValidationError
	return errors.As(err, &ve)
}

// TransitionCase move o case para outra etapa (member, ou persona entre etapas working sem aprovação).
func (s *Service) TransitionCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in TransitionCaseInput) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, err
	}
	if err := personaWrite(actor, in.RunID); err != nil {
		return CaseView{}, err
	}
	if utf8.RuneCountInString(in.Reason) > maxReasonRunes {
		return CaseView{}, domain.Invalid("reason", fmt.Sprintf("too long (max %d)", maxReasonRunes))
	}
	role, err := s.callerRoleOf(ctx, companyID)
	if err != nil {
		return CaseView{}, err
	}
	err = s.withTx(ctx, func(t *txc) error {
		c, pv, prefix, err := t.lockPipelineCase(companyID, caseID, in.ExpectedVersion)
		if err != nil {
			return err
		}
		to, ok := pv.Stage(strings.TrimSpace(in.ToStage))
		if !ok {
			return domain.Invalid("to_stage", "unknown stage "+in.ToStage)
		}
		_, err = t.moveCase(c, prefix, pv, to, moveOpts{actor: actor, role: role, runID: in.RunID, taskID: in.TaskID, force: in.Force, reason: in.Reason})
		return err
	})
	if err != nil {
		return CaseView{}, err
	}
	return loadCaseView(ctx, s.read(), companyID, caseID)
}

// lockPipelineCase trava o case, confere a versão e carrega o pipeline dele.
func (t *txc) lockPipelineCase(companyID, caseID domain.ID, expected int) (store.Case, PipelineView, string, error) {
	c, err := t.lockCase(companyID, caseID)
	if err != nil {
		return c, PipelineView{}, "", err
	}
	if err := checkVersion(c, expected); err != nil {
		return c, PipelineView{}, "", err
	}
	if c.PipelineID == nil {
		return c, PipelineView{}, "", domain.Invalid("case", "a loose case changes status, not stage")
	}
	p, err := t.q.GetPipelineByID(t.ctx, store.GetPipelineByIDParams{CompanyID: companyID, ID: *c.PipelineID})
	if err != nil {
		return c, PipelineView{}, "", mapErr(err, "get pipeline")
	}
	pv, err := t.s.pipelineView(t.ctx, t.q, p)
	if err != nil {
		return c, PipelineView{}, "", err
	}
	prefix, err := t.companyPrefix(companyID)
	return c, pv, prefix, err
}

// SuggestCaseTransition registra a sugestão de mover o case (persona ou member). Uma sugestão nova substitui a
// pendente; a decisão é humana (ResolveSuggestion ou a fila de revisão).
func (s *Service) SuggestCaseTransition(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in SuggestInput) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, err
	}
	if err := personaWrite(actor, in.RunID); err != nil {
		return CaseView{}, err
	}
	rat := strings.TrimSpace(in.Rationale)
	if n := utf8.RuneCountInString(rat); n < 1 || n > maxReasonRunes {
		return CaseView{}, domain.Invalid("rationale", fmt.Sprintf("must have 1-%d characters", maxReasonRunes))
	}
	if math.IsNaN(in.Confidence) || in.Confidence < 0 || in.Confidence > 1 {
		return CaseView{}, domain.Invalid("confidence", "must be between 0 and 1")
	}
	err := s.withTx(ctx, func(t *txc) error {
		c, pv, _, err := t.lockPipelineCase(companyID, caseID, in.ExpectedVersion)
		if err != nil {
			return err
		}
		if c.TerminalKind != nil {
			return domain.Invalid("case", "the case is closed")
		}
		to, ok := pv.Stage(strings.TrimSpace(in.ToStage))
		if !ok {
			return domain.Invalid("to_stage", "unknown stage "+in.ToStage)
		}
		if c.StageID != nil && *c.StageID == to.ID {
			return domain.Invalid("to_stage", "the case is already in "+to.Name)
		}
		if to.Config.Disabled {
			return domain.Invalid("to_stage", to.Name+" is disabled")
		}
		sg := domain.CaseSuggestion{ID: domain.NewID(), ToStage: to.Key, Rationale: rat, Confidence: in.Confidence, RunID: in.RunID, At: t.now}
		if actor.Type == domain.ActorPersona {
			sg.PersonaID = actor.ID
		}
		raw, err := json.Marshal(sg)
		if err != nil {
			return fmt.Errorf("marshal suggestion: %w", err)
		}
		c.PendingSuggestion = raw
		if _, err := t.saveCase(c); err != nil {
			return err
		}
		if _, err := t.caseEvent(companyID, c.ID, domain.CaseEvTransitionSuggested, actor, in.RunID, c.StageID, &to.ID,
			map[string]any{"to": to.Key, "to_name": to.Name, "rationale": clipRunes(rat, 1000), "confidence": in.Confidence, "suggestion_id": sg.ID.String()}); err != nil {
			return err
		}
		if err := t.linkPersonaTask(companyID, c.ID, actor, in.TaskID, in.RunID, domain.LinkWork); err != nil {
			return err
		}
		return t.activity(actor, companyID, "case.suggested", "case", c.ID, map[string]any{"to": to.Key, "confidence": in.Confidence}, "")
	})
	if err != nil {
		return CaseView{}, err
	}
	return loadCaseView(ctx, s.read(), companyID, caseID)
}

// ResolveSuggestion aceita (move para a etapa sugerida, com as regras de sempre) ou dispensa a sugestão (humano).
func (s *Service) ResolveSuggestion(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in ResolveSuggestionInput) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return CaseView{}, fmt.Errorf("a suggestion is resolved by a human: %w", domain.ErrNotAllowed)
	}
	if utf8.RuneCountInString(in.Reason) > maxReasonRunes {
		return CaseView{}, domain.Invalid("reason", fmt.Sprintf("too long (max %d)", maxReasonRunes))
	}
	role, err := s.callerRoleOf(ctx, companyID)
	if err != nil {
		return CaseView{}, err
	}
	err = s.withTx(ctx, func(t *txc) error {
		c, pv, prefix, err := t.lockPipelineCase(companyID, caseID, in.ExpectedVersion)
		if err != nil {
			return err
		}
		if len(c.PendingSuggestion) == 0 {
			return domain.Invalid("suggestion", "the case has no pending suggestion")
		}
		var sg domain.CaseSuggestion
		if err := json.Unmarshal(c.PendingSuggestion, &sg); err != nil {
			return fmt.Errorf("read suggestion: %w", err)
		}
		if in.Accept {
			to, ok := pv.Stage(sg.ToStage)
			if !ok {
				return domain.Invalid("suggestion", "the suggested stage no longer exists")
			}
			if cur, ok := pv.StageByID(*c.StageID); ok && cur.Kind == domain.StageReview {
				return domain.Invalid("suggestion", "the case is in review: decide the review instead")
			}
			reason := in.Reason
			if reason == "" {
				reason = sg.Rationale
			}
			_, err := t.moveCase(c, prefix, pv, to, moveOpts{actor: actor, role: role, reason: clipRunes(reason, maxReasonRunes), accepted: &sg})
			return err
		}
		c.PendingSuggestion = nil
		if _, err := t.saveCase(c); err != nil {
			return err
		}
		payload := map[string]any{"resolution": "dismissed", "suggestion_id": sg.ID.String(), "to": sg.ToStage}
		if r := strings.TrimSpace(in.Reason); r != "" {
			payload["reason"] = r
		}
		if _, err := t.caseEvent(companyID, c.ID, domain.CaseEvSuggestionResolved, actor, nil, nil, nil, payload); err != nil {
			return err
		}
		return t.activity(actor, companyID, "case.suggestion_dismissed", "case", c.ID, map[string]any{"to": sg.ToStage}, "")
	})
	if err != nil {
		return CaseView{}, err
	}
	return loadCaseView(ctx, s.read(), companyID, caseID)
}

// ---------- pacote de contexto ----------

type contextOpts struct {
	pipeline   *PipelineView
	stage      *domain.Stage
	automation bool
}

// GetCaseContext devolve o pacote de contexto do case (o mesmo que vai na task de automação da etapa atual).
func (s *Service) GetCaseContext(ctx context.Context, companyID, caseID domain.ID) (string, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return "", err
	}
	q := s.read()
	c, err := q.GetCaseByID(ctx, store.GetCaseByIDParams{CompanyID: companyID, ID: caseID})
	if err != nil {
		return "", mapErr(err, "get case")
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return "", mapErr(err, "company")
	}
	o := contextOpts{}
	if c.PipelineID != nil {
		p, err := q.GetPipelineByID(ctx, store.GetPipelineByIDParams{CompanyID: companyID, ID: *c.PipelineID})
		if err != nil {
			return "", mapErr(err, "get pipeline")
		}
		pv, err := s.pipelineView(ctx, q, p)
		if err != nil {
			return "", err
		}
		o.pipeline = &pv
		if st, ok := pv.StageByID(*c.StageID); ok {
			o.stage = &st
		}
	}
	return s.buildCaseContext(ctx, q, companyID, c, co.TaskPrefix, o)
}

// StageLearning é uma decisão passada com motivo, em texto curto (o pacote de contexto e o hanko_case_get).
type StageLearning struct {
	CaseIdentifier string    `json:"case_identifier"`
	CaseTitle      string    `json:"case_title"`
	Kind           string    `json:"kind"`
	Decision       string    `json:"decision,omitempty"`
	ToStage        string    `json:"to_stage,omitempty"`
	Reason         string    `json:"reason"`
	At             string    `json:"at"`
	EventID        domain.ID `json:"event_id"`
}

// stageLearnings são as últimas decisões com motivo da etapa (ou do pipeline, sem etapa), até 4 KiB no total.
func stageLearnings(ctx context.Context, q *store.Queries, companyID domain.ID, prefix string, pipelineID domain.ID, stageID *domain.ID) ([]StageLearning, error) {
	type row struct {
		id                 domain.ID
		kind, toName       string
		payload            []byte
		number             int64
		title, createdDate string
	}
	var rows []row
	if stageID != nil {
		rs, err := q.StageLearnings(ctx, store.StageLearningsParams{CompanyID: companyID, PipelineID: &pipelineID, StageID: stageID, Lim: maxLearningItems})
		if err != nil {
			return nil, fmt.Errorf("stage learnings: %w", err)
		}
		for _, r := range rs {
			rows = append(rows, row{r.ID, r.Kind, r.ToName, r.Payload, r.CaseNumber, r.CaseTitle, r.CreatedAt.Format("2006-01-02")})
		}
	} else {
		rs, err := q.PipelineLearnings(ctx, store.PipelineLearningsParams{CompanyID: companyID, PipelineID: &pipelineID, Lim: maxLearningItems})
		if err != nil {
			return nil, fmt.Errorf("pipeline learnings: %w", err)
		}
		for _, r := range rs {
			rows = append(rows, row{r.ID, r.Kind, r.ToName, r.Payload, r.CaseNumber, r.CaseTitle, r.CreatedAt.Format("2006-01-02")})
		}
	}
	var out []StageLearning
	total := 0
	for _, r := range rows {
		var p map[string]any
		_ = json.Unmarshal(r.payload, &p)
		reason, _ := p["reason"].(string)
		decision, _ := p["decision"].(string)
		l := StageLearning{CaseIdentifier: domain.CaseIdentifier(prefix, r.number), CaseTitle: clipRunes(r.title, 120), Kind: r.kind, Decision: decision,
			ToStage: r.toName, Reason: clipRunes(strings.TrimSpace(reason), maxLearningItemRunes), At: r.createdDate, EventID: r.id}
		size := len(l.Reason) + len(l.CaseTitle) + 64
		if total+size > maxLearningBytes {
			break
		}
		total += size
		out = append(out, l)
	}
	return out, nil
}

func decisionVerbPT(kind, decision string) string {
	switch {
	case kind == string(domain.CaseEvTransitionForced):
		return "forçou a passagem de"
	case decision == string(domain.ReviewApprove):
		return "aprovou"
	case decision == string(domain.ReviewReject):
		return "recusou"
	case decision == string(domain.ReviewRequestChanges):
		return "pediu mudanças em"
	}
	return "decidiu"
}

// buildCaseContext monta o pacote de contexto em markdown (pt-BR, como os demais textos que o Hanko grava).
func (s *Service) buildCaseContext(ctx context.Context, q *store.Queries, companyID domain.ID, c store.Case, prefix string, o contextOpts) (string, error) {
	cs := caseFromRow(c, prefix)
	var b strings.Builder
	fmt.Fprintf(&b, "# Case %s — %s\n\n", cs.Identifier, cs.Title)
	if cs.Type != "" {
		fmt.Fprintf(&b, "- Tipo: %s\n", cs.Type)
	}
	if cs.Key != "" {
		fmt.Fprintf(&b, "- Key: %s\n", cs.Key)
	}
	fmt.Fprintf(&b, "- Status: %s · versão %d\n", cs.Status, cs.Version)
	if o.pipeline != nil {
		fmt.Fprintf(&b, "- Pipeline: %s (%s)\n", o.pipeline.Name, o.pipeline.Key)
	}
	if o.stage != nil {
		fmt.Fprintf(&b, "- Etapa: %s (%s, %s)\n", o.stage.Name, o.stage.Key, o.stage.Kind)
	}
	if o.stage != nil && o.stage.Config.WhatHappensHere != "" {
		fmt.Fprintf(&b, "\n## O que acontece nesta etapa\n\n%s\n", o.stage.Config.WhatHappensHere)
	}
	if o.stage != nil && o.automation && o.stage.Config.Automation != nil && o.stage.Config.Automation.Instructions != "" {
		fmt.Fprintf(&b, "\n## Instruções\n\n%s\n", o.stage.Config.Automation.Instructions)
	}
	if cs.Summary != "" {
		fmt.Fprintf(&b, "\n## Resumo\n\n%s\n", clipBytes(cs.Summary, maxContextSummaryBytes))
	}
	if len(cs.Fields) > 0 {
		var fb strings.Builder
		labels := map[string]string{}
		if o.stage != nil {
			for _, f := range o.stage.Config.Fields {
				labels[f.Key] = f.Label
			}
		}
		for _, k := range fieldKeys(cs.Fields) {
			label := k
			if l := labels[k]; l != "" {
				label = l + " (" + k + ")"
			}
			v := strings.ReplaceAll(strings.ReplaceAll(fmt.Sprint(cs.Fields[k]), "\n", " "), "|", "\\|")
			fmt.Fprintf(&fb, "| %s | %s |\n", label, clipRunes(v, 500))
		}
		fmt.Fprintf(&b, "\n## Campos\n\n| Campo | Valor |\n|---|---|\n%s", clipBytes(fb.String(), maxContextFieldsBytes))
	}
	if o.stage != nil {
		var missing []string
		for _, f := range o.stage.Config.Fields {
			if f.Required {
				if v, ok := cs.Fields[f.Key]; !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
					missing = append(missing, f.Label+" ("+f.Key+")")
				}
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(&b, "\n**Campos obrigatórios para sair desta etapa ainda vazios:** %s\n", strings.Join(missing, ", "))
		}
	}
	// a última decisão de revisão deste case (o motivo de um pedido de mudanças volta aqui)
	evs, err := q.ListCaseEvents(ctx, store.ListCaseEventsParams{CompanyID: companyID, CaseID: c.ID, Lim: 50})
	if err != nil {
		return "", fmt.Errorf("list case events: %w", err)
	}
	for _, e := range evs {
		if e.Kind != string(domain.CaseEvReviewDecided) {
			continue
		}
		var p map[string]any
		_ = json.Unmarshal(e.Payload, &p)
		dec, _ := p["decision"].(string)
		reason, _ := p["reason"].(string)
		to, _ := p["to_name"].(string)
		fmt.Fprintf(&b, "\n## Última decisão de revisão\n\n- %s: %s → %s", e.CreatedAt.Format("2006-01-02"), dec, to)
		if reason != "" {
			fmt.Fprintf(&b, "\n- Motivo: %s", clipRunes(reason, 2000))
		}
		b.WriteString("\n")
		break
	}
	if o.pipeline != nil {
		var stID *domain.ID
		if o.stage != nil {
			stID = &o.stage.ID
		}
		ls, err := stageLearnings(ctx, q, companyID, prefix, o.pipeline.ID, stID)
		if err != nil {
			return "", err
		}
		if len(ls) > 0 {
			b.WriteString("\n## Aprendizados desta etapa\n\nO que as revisões humanas decidiram aqui (mais recentes primeiro). Leve em conta antes de agir.\n\n")
			for _, l := range ls {
				fmt.Fprintf(&b, "- %s · %s %s \"%s\"", l.At, decisionVerbPT(l.Kind, l.Decision), l.CaseIdentifier, l.CaseTitle)
				if l.ToStage != "" {
					fmt.Fprintf(&b, " → %s", l.ToStage)
				}
				fmt.Fprintf(&b, ": %s\n", l.Reason)
			}
		}
	}
	if len(evs) > 0 {
		b.WriteString("\n## Histórico recente\n\n")
		n := min(len(evs), maxContextHistory)
		hist := make([]store.CaseEvent, n)
		copy(hist, evs[:n])
		sort.SliceStable(hist, func(i, j int) bool { return hist[i].CreatedAt.Before(hist[j].CreatedAt) })
		for _, e := range hist {
			var p map[string]any
			_ = json.Unmarshal(e.Payload, &p)
			line := fmt.Sprintf("- %s · %s", e.CreatedAt.Format("2006-01-02 15:04"), e.Kind)
			if to, _ := p["to_name"].(string); to != "" {
				line += " → " + to
			}
			if r, _ := p["reason"].(string); r != "" {
				line += ": " + clipRunes(r, 300)
			} else if r, _ := p["rationale"].(string); r != "" {
				line += ": " + clipRunes(r, 300)
			}
			b.WriteString(line + "\n")
		}
	}
	fmt.Fprintf(&b, "\n## Como agir\n\n"+
		"- `hanko_case_get` com `case: %s` traz o case completo, com o histórico e os aprendizados.\n"+
		"- `hanko_case_update` grava resumo e campos (informe `expected_version`, a versão que você leu).\n"+
		"- `hanko_case_transition` move entre etapas de trabalho sem aprovação; para revisão ou outra decisão humana, use `hanko_case_suggest` com a justificativa.\n", cs.Identifier)
	return b.String(), nil
}
