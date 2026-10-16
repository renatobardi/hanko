package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Fila de revisão e aprendizados (ADR-0034). Quem decide uma revisão é o aprovador humano da etapa (any_member =
// qualquer member); a persona nunca decide.

const maxBulkReview = 100

// ReviewCaseInput é a decisão sobre um case numa etapa de revisão. As edições (título, resumo, campos) são
// aplicadas antes de mover.
type ReviewCaseInput struct {
	ExpectedVersion int
	Decision        domain.ReviewDecision
	Reason          string
	Title, Summary  *string
	Fields          map[string]any
}

// BulkReviewItem é um item da aprovação em lote.
type BulkReviewItem struct {
	CaseID          domain.ID
	ExpectedVersion int
	Reason          string
}

// BulkReviewResult é o resultado de um item do lote.
type BulkReviewResult struct {
	CaseID  domain.ID `json:"case_id"`
	OK      bool      `json:"ok"`
	Version int       `json:"version,omitempty"`
	// Error é o motivo da recusa; CurrentVersion vem num conflito de versão.
	Error          string `json:"error,omitempty"`
	CurrentVersion int    `json:"current_version,omitempty"`
	Err            error  `json:"-"`
}

// ReviewItem é uma linha da fila de revisão.
type ReviewItem struct {
	// Kind é review (case numa etapa de revisão), suggestion (sugestão pendente) ou attention (bloqueio resolvido,
	// filhos terminaram).
	Kind  string    `json:"kind"`
	Case  CaseView  `json:"case"`
	Since time.Time `json:"since"`
	// Reason é o texto da linha: a justificativa da sugestão, o motivo da atenção ou o que acontece na etapa.
	Reason               string                 `json:"reason,omitempty"`
	Suggestion           *domain.CaseSuggestion `json:"suggestion,omitempty"`
	SuggestedStageName   string                 `json:"suggested_stage_name,omitempty"`
	ApproveTo            string                 `json:"approve_to,omitempty"`
	ApproveToName        string                 `json:"approve_to_name,omitempty"`
	RejectTo             string                 `json:"reject_to,omitempty"`
	RejectToName         string                 `json:"reject_to_name,omitempty"`
	RequestChangesTo     string                 `json:"request_changes_to,omitempty"`
	RequestChangesToName string                 `json:"request_changes_to_name,omitempty"`
	RequireRejectReason  bool                   `json:"require_reject_reason"`
	RequireChangesReason bool                   `json:"require_changes_reason"`
	// Approver é o aprovador da etapa (any_member, member:<id>); vazio = qualquer member.
	Approver string `json:"approver,omitempty"`
}

// LearningFilter filtra os aprendizados.
type LearningFilter struct {
	PipelineID *domain.ID
	StageID    *domain.ID
	// Decision é approve, reject, request_changes ou forced.
	Decision      string
	Limit, Offset int
}

// Learning é uma decisão (revisão ou transição forçada) e o que ela ensinou.
type Learning struct {
	EventID        domain.ID        `json:"event_id"`
	Kind           string           `json:"kind"`
	Decision       string           `json:"decision"`
	Reason         string           `json:"reason,omitempty"`
	Actor          domain.Actor     `json:"actor"`
	ActorName      string           `json:"actor_name,omitempty"`
	CaseID         domain.ID        `json:"case_id"`
	CaseIdentifier string           `json:"case_identifier"`
	CaseTitle      string           `json:"case_title"`
	PipelineKey    string           `json:"pipeline_key,omitempty"`
	PipelineName   string           `json:"pipeline_name,omitempty"`
	FromStage      string           `json:"from_stage,omitempty"`
	FromName       string           `json:"from_name,omitempty"`
	ToStage        string           `json:"to_stage,omitempty"`
	ToName         string           `json:"to_name,omitempty"`
	At             time.Time        `json:"at"`
	Snapshot       map[string]any   `json:"snapshot,omitempty"`
	ApprovedVer    int              `json:"approved_version,omitempty"`
	FromStageID    *domain.ID       `json:"from_stage_id,omitempty"`
	ToStageID      *domain.ID       `json:"to_stage_id,omitempty"`
}

func reviewTarget(r *domain.ReviewRouting, d domain.ReviewDecision) string {
	if r == nil {
		return ""
	}
	switch d {
	case domain.ReviewApprove:
		return r.ApproveTo
	case domain.ReviewReject:
		return r.RejectTo
	}
	return r.RequestChangesTo
}

// ReviewCase decide a revisão de um case (aprovar, pedir mudanças ou recusar) e o move para a etapa configurada.
func (s *Service) ReviewCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in ReviewCaseInput) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, err
	}
	if actor.Type != domain.ActorHuman {
		return CaseView{}, fmt.Errorf("a review is decided by a human: %w", domain.ErrNotAllowed)
	}
	if !in.Decision.Valid() {
		return CaseView{}, domain.Invalid("decision", "use approve, reject or request_changes")
	}
	reason := strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(reason) > maxReasonRunes {
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
		from, ok := pv.StageByID(*c.StageID)
		if !ok || from.Kind != domain.StageReview || c.TerminalKind != nil {
			return domain.Invalid("case", "the case is not in a review stage")
		}
		if !approverOK(from.Config, actor, role) {
			return fmt.Errorf("only the approver of %s decides this review: %w", from.Name, domain.ErrNotAllowed)
		}
		r := from.Config.Review
		switch {
		case in.Decision == domain.ReviewReject && r.RejectReasonRequired() && reason == "":
			return domain.Invalid("reason", "rejecting needs a reason")
		case in.Decision == domain.ReviewRequestChanges && r.ChangesReasonRequired() && reason == "":
			return domain.Invalid("reason", "asking for changes needs a reason")
		}
		destKey := reviewTarget(r, in.Decision)
		if destKey == "" {
			return domain.Invalid("decision", "this stage takes no request for changes")
		}
		to, ok := pv.Stage(destKey)
		if !ok {
			return domain.Invalid("decision", "the destination stage "+destKey+" no longer exists")
		}
		next, changed, fchanged, err := t.applyCaseEdits(c, prefix, in.Title, in.Summary, in.Fields, false)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			changed["in_review"] = true
			if _, err := t.caseEvent(companyID, c.ID, domain.CaseEvUpdated, actor, nil, nil, nil, changed); err != nil {
				return err
			}
		}
		if fchanged {
			if _, err := t.caseEvent(companyID, c.ID, domain.CaseEvFieldsChanged, actor, nil, nil, nil, map[string]any{"keys": fieldKeys(in.Fields), "in_review": true}); err != nil {
				return err
			}
		}
		snap := caseFromRow(next, prefix)
		snapshot := map[string]any{"title": snap.Title, "summary": clipBytes(snap.Summary, 4<<10), "fields": snap.Fields, "version": snap.Version,
			"case_type": snap.Type}
		_, err = t.moveCase(next, prefix, pv, to, moveOpts{actor: actor, role: role, reason: reason,
			review: &reviewMove{decision: in.Decision, reason: reason, snapshot: snapshot}})
		return err
	})
	if err != nil {
		return CaseView{}, err
	}
	return loadCaseView(ctx, s.read(), companyID, caseID)
}

// BulkApprove aprova vários cases, cada um com a versão que o humano viu; o resultado é por item (um conflito ou
// uma regra num item não desfaz os outros).
func (s *Service) BulkApprove(ctx context.Context, actor domain.Actor, companyID domain.ID, items []BulkReviewItem) ([]BulkReviewResult, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > maxBulkReview {
		return nil, domain.Invalid("items", fmt.Sprintf("send 1-%d items", maxBulkReview))
	}
	out := make([]BulkReviewResult, 0, len(items))
	for _, it := range items {
		res := BulkReviewResult{CaseID: it.CaseID}
		v, err := s.ReviewCase(ctx, actor, companyID, it.CaseID, ReviewCaseInput{ExpectedVersion: it.ExpectedVersion, Decision: domain.ReviewApprove, Reason: it.Reason})
		if err != nil {
			res.Error, res.Err = err.Error(), err
			if cur, ok := domain.CurrentVersion(err); ok {
				res.CurrentVersion = cur
			}
			if errors.Is(err, domain.ErrForbidden) && len(out) == 0 {
				// sem papel para nada: o lote inteiro falha igual
				return nil, err
			}
		} else {
			res.OK, res.Version = true, v.Version
		}
		out = append(out, res)
	}
	return out, nil
}

// ReviewQueue lista o que espera decisão humana: cases em etapa de revisão, sugestões pendentes e atenção
// (bloqueio resolvido, filhos terminados). Mais antigos primeiro.
func (s *Service) ReviewQueue(ctx context.Context, companyID domain.ID) ([]ReviewItem, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListReviewQueue(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("review queue: %w", err)
	}
	stages, err := q.ListCompanyStages(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list stages: %w", err)
	}
	nameOf := func(pipelineID *domain.ID, key string) string {
		if pipelineID == nil || key == "" {
			return ""
		}
		for _, st := range stages {
			if st.PipelineID == *pipelineID && st.Key == key {
				return st.Name
			}
		}
		return key
	}
	var out []ReviewItem
	for _, r := range rows {
		cv := caseViewOf(r.Case, caseJoin{prefix: r.TaskPrefix, pipelineKey: r.PipelineKey, pipelineName: r.PipelineName, stageKey: r.StageKey,
			stageName: r.StageName, stageKind: r.StageKind})
		since := r.EnteredAt
		if since.IsZero() {
			since = r.Case.UpdatedAt
		}
		if domain.StageKind(r.StageKind) == domain.StageReview {
			cfg := parseStageConfig(r.StageConfig)
			it := ReviewItem{Kind: "review", Case: cv, Since: since, Reason: clipRunes(cfg.WhatHappensHere, 300),
				RequireRejectReason: cfg.Review.RejectReasonRequired(), RequireChangesReason: cfg.Review.ChangesReasonRequired()}
			if cfg.Approval != nil {
				it.Approver = cfg.Approval.Approver
			}
			if rv := cfg.Review; rv != nil {
				it.ApproveTo, it.RejectTo, it.RequestChangesTo = rv.ApproveTo, rv.RejectTo, rv.RequestChangesTo
				it.ApproveToName, it.RejectToName, it.RequestChangesToName = nameOf(r.Case.PipelineID, rv.ApproveTo), nameOf(r.Case.PipelineID, rv.RejectTo),
					nameOf(r.Case.PipelineID, rv.RequestChangesTo)
			}
			if it.Reason == "" {
				it.Reason = clipRunes(cv.Summary, 300)
			}
			out = append(out, it)
		}
		if sg := cv.PendingSuggestion; sg != nil {
			out = append(out, ReviewItem{Kind: "suggestion", Case: cv, Since: sg.At, Reason: clipRunes(sg.Rationale, 300), Suggestion: sg,
				SuggestedStageName: nameOf(r.Case.PipelineID, sg.ToStage)})
		}
		if cv.Attention != "" {
			at := since
			if cv.AttentionAt != nil {
				at = *cv.AttentionAt
			}
			out = append(out, ReviewItem{Kind: "attention", Case: cv, Since: at, Reason: cv.Attention})
		}
	}
	return out, nil
}

// ReviewQueueCount é quantos cases esperam decisão (para a Caixa de entrada).
func (s *Service) ReviewQueueCount(ctx context.Context, companyID domain.ID) (int, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return 0, err
	}
	n, err := s.read().CountReviewQueue(ctx, companyID)
	if err != nil {
		return 0, fmt.Errorf("count review queue: %w", err)
	}
	return int(n), nil
}

// ListLearnings lista as decisões de revisão e as transições forçadas, mais recentes primeiro.
func (s *Service) ListLearnings(ctx context.Context, companyID domain.ID, f LearningFilter) ([]Learning, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListLearningsParams{CompanyID: companyID, PipelineID: f.PipelineID, StageID: f.StageID, Lim: i32(clamp(f.Limit, 200, 500)), Off: i32(max(f.Offset, 0))}
	if d := strings.TrimSpace(f.Decision); d != "" {
		if d != "forced" && !domain.ReviewDecision(d).Valid() {
			return nil, domain.Invalid("decision", "use approve, reject, request_changes or forced")
		}
		p.Decision = &d
	}
	rows, err := s.read().ListLearnings(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list learnings: %w", err)
	}
	out := make([]Learning, len(rows))
	for i, r := range rows {
		var pl map[string]any
		_ = json.Unmarshal(r.Payload, &pl)
		l := Learning{EventID: r.ID, Kind: r.Kind, Actor: actorOf(r.ActorType, r.ActorID), CaseID: r.CaseID,
			CaseIdentifier: domain.CaseIdentifier(r.TaskPrefix, r.CaseNumber), CaseTitle: r.CaseTitle, PipelineKey: r.PipelineKey,
			PipelineName: r.PipelineName, FromStage: r.FromKey, FromName: r.FromName, ToStage: r.ToKey, ToName: r.ToName, At: r.CreatedAt,
			FromStageID: r.FromStageID, ToStageID: r.ToStageID}
		l.Decision, _ = pl["decision"].(string)
		l.Reason, _ = pl["reason"].(string)
		if sn, ok := pl["snapshot"].(map[string]any); ok {
			l.Snapshot = sn
		}
		if v, ok := pl["approved_version"].(float64); ok {
			l.ApprovedVer = int(v)
		}
		if l.FromName == "" {
			l.FromName, _ = pl["from_name"].(string)
		}
		if l.ToName == "" {
			l.ToName, _ = pl["to_name"].(string)
		}
		switch r.ActorType {
		case string(domain.ActorHuman):
			l.ActorName = r.UserName
		case string(domain.ActorPersona):
			l.ActorName = r.PersonaName
		}
		out[i] = l
	}
	return out, nil
}

// CaseLearnings são os aprendizados da etapa atual do case (ou do pipeline): o hanko_case_get devolve junto.
func (s *Service) CaseLearnings(ctx context.Context, companyID, caseID domain.ID) ([]StageLearning, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	c, err := q.GetCaseByID(ctx, store.GetCaseByIDParams{CompanyID: companyID, ID: caseID})
	if err != nil {
		return nil, mapErr(err, "get case")
	}
	if c.PipelineID == nil {
		return nil, nil
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil, mapErr(err, "company")
	}
	return stageLearnings(ctx, q, companyID, co.TaskPrefix, *c.PipelineID, c.StageID)
}

// PipelineEvent é um evento de case no feed de atividade do pipeline.
type PipelineEvent struct {
	domain.CaseEvent
	CaseIdentifier string `json:"case_identifier"`
	CaseTitle      string `json:"case_title"`
}

// ListPipelineEvents é a aba Atividade do pipeline (eventos dos cases, mais recentes primeiro).
func (s *Service) ListPipelineEvents(ctx context.Context, companyID, pipelineID domain.ID, limit int) ([]PipelineEvent, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil, mapErr(err, "company")
	}
	rows, err := q.ListPipelineEvents(ctx, store.ListPipelineEventsParams{CompanyID: companyID, PipelineID: &pipelineID, Lim: i32(clamp(limit, 100, 500))})
	if err != nil {
		return nil, fmt.Errorf("pipeline events: %w", err)
	}
	out := make([]PipelineEvent, len(rows))
	for i, r := range rows {
		ev := caseEventFromRow(store.CaseEvent{ID: r.ID, CompanyID: r.CompanyID, CaseID: r.CaseID, Kind: r.Kind, FromStageID: r.FromStageID,
			ToStageID: r.ToStageID, ActorType: r.ActorType, ActorID: r.ActorID, RunID: r.RunID, Payload: r.Payload, CreatedAt: r.CreatedAt})
		out[i] = PipelineEvent{CaseEvent: ev, CaseIdentifier: domain.CaseIdentifier(co.TaskPrefix, r.CaseNumber), CaseTitle: r.CaseTitle}
	}
	return out, nil
}
