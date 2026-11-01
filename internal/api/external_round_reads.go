package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Leitura da rodada e do ciclo, texto da etapa e marca da ação (ADR-0080; fatia 3 da v0.8, #127). Leitura é de qualquer
// membro; o texto e a marca são de owner ou admin (o serviço confere), a marca com reautenticação recente.

func (s *server) registerRoundReads(api huma.API) {
	const tag = "external-rounds"
	b := "/companies/{company}"
	huma.Register(api, op("list-external-rounds", http.MethodGet, b+"/external-rounds", tag,
		"Lista as rodadas externas, com filtro por ciclo"), s.listExternalRounds)
	huma.Register(api, op("get-external-round", http.MethodGet, b+"/external-rounds/{round}", tag,
		"Mostra uma rodada externa, com participantes, etapas vigentes e ações com a marca mais nova"), s.getExternalRound)
	huma.Register(api, op("get-round-step-content", http.MethodGet, b+"/external-rounds/{round}/steps/{step}/content", tag,
		"Mostra o texto da etapa, lido do fato da ingestão (owner ou admin; cada leitura gera activity_event)"), s.getRoundStepContent)
	huma.Register(api, created(op("mark-round-action", http.MethodPost, b+"/external-rounds/{round}/steps/{step}/actions/{action}/marks", tag,
		"Marca uma ação da etapa como feita ou pendente (owner ou admin, com reautenticação recente; gera activity_event)")), s.markRoundAction)
	huma.Register(api, op("get-external-cycle", http.MethodGet, b+"/external-cycles/{cycle}", tag,
		"Mostra o ciclo de uma issue, com o resumo no topo e as rodadas"), s.getExternalCycle)
}

func roundOut(v service.RoundView) ExternalRound {
	return ExternalRound{ID: v.ID.String(), RoundKey: v.RoundKey, Name: v.Name, SourceKind: "external", CycleID: idPtrStr(v.CycleID), Repo: v.Repo,
		WorkspaceID: idPtrStr(v.WorkspaceID), MaxSessions: v.MaxSessions, OpenedAt: v.OpenedAt, ClosedAt: v.ClosedAt, LastEventAt: v.LastEventAt,
		LastAskedAt: v.LastAskedAt, LastAnsweredAt: v.LastAnsweredAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func markOut(m service.ActionMarkView) ActionMark {
	return ActionMark{State: m.State, MarkedByType: m.MarkedByType, MarkedByID: idPtrStr(m.MarkedByID), MarkedAt: m.MarkedAt, Migrated: m.Migrated}
}

func stepOut(v service.StepView) RoundStep {
	actions := make([]StepAction, len(v.Actions))
	for i, a := range v.Actions {
		actions[i] = StepAction{ActionID: a.ActionID, ApprovalKey: a.ApprovalKey}
		if a.Mark != nil {
			m := markOut(*a.Mark)
			actions[i].Mark = &m
		}
	}
	return RoundStep{ID: v.ID.String(), Kind: v.Kind, StepKey: v.StepKey, Revision: v.Revision, SHA256: v.SHA256, Review: v.Review,
		ReferenceCheck: v.ReferenceCheck, PublishedAt: v.PublishedAt, Actions: actions}
}

func (s *server) listExternalRounds(ctx context.Context, in *listExternalRoundsIn) (*ListOut[ExternalRound], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	cycle, err := optionalID("cycle", in.Cycle)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListExternalRounds(ctx, sc.Company.ID, cycle, in.Limit, in.Offset)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ExternalRound, len(rows))
	for i, r := range rows {
		items[i] = roundOut(r)
	}
	return &ListOut[ExternalRound]{Body: ListBody[ExternalRound]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
}

func (s *server) getExternalRound(ctx context.Context, in *ExternalRoundPath) (*ItemOut[ExternalRoundDetail], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("round", in.Round)
	if err != nil {
		return nil, err
	}
	d, err := s.svc.GetExternalRound(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	out := ExternalRoundDetail{ExternalRound: roundOut(d.RoundView), Participants: make([]RoundParticipant, len(d.Participants)), Steps: make([]RoundStep, len(d.Steps))}
	for i, p := range d.Participants {
		out.Participants[i] = RoundParticipant{ID: p.ID.String(), Slug: p.Slug, IssueRef: p.IssueRef, ExternalSessionID: idPtrStr(p.ExternalSessionID),
			SpawnedAt: p.SpawnedAt, ClosedAt: p.ClosedAt}
	}
	for i, st := range d.Steps {
		out.Steps[i] = stepOut(st)
	}
	return &ItemOut[ExternalRoundDetail]{Body: out}, nil
}

func (s *server) getRoundStepContent(ctx context.Context, in *RoundStepPath) (*ItemOut[RoundStepContent], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	round, step, err := parseRoundStep(in)
	if err != nil {
		return nil, err
	}
	text, err := s.svc.RoundStepContent(ctx, sc.Actor, sc.Company.ID, round, step)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[RoundStepContent]{Body: RoundStepContent{StepID: step.String(), Text: text}}, nil
}

func parseRoundStep(in *RoundStepPath) (round, step domain.ID, err error) {
	if round, err = parseIDParam("round", in.Round); err != nil {
		return round, step, err
	}
	step, err = parseIDParam("step", in.Step)
	return round, step, err
}

func (s *server) markRoundAction(ctx context.Context, in *markRoundActionIn) (*ItemOut[ActionMark], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	round, step, err := parseRoundStep(&in.RoundStepPath)
	if err != nil {
		return nil, err
	}
	m, err := s.svc.MarkRoundAction(ctx, sc.Actor, sc.Company.ID, round, step, in.Action, in.Body.State)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ActionMark]{Body: markOut(m)}, nil
}

func (s *server) getExternalCycle(ctx context.Context, in *ExternalCyclePath) (*ItemOut[ExternalCycle], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("cycle", in.Cycle)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.GetExternalCycle(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	out := ExternalCycle{ID: v.ID.String(), IssueRef: v.IssueRef, CreatedAt: v.CreatedAt, Rounds: make([]ExternalRound, len(v.Rounds))}
	if v.Summary != nil {
		st := stepOut(*v.Summary)
		out.Summary = &st
	}
	for i, r := range v.Rounds {
		out.Rounds[i] = roundOut(r)
	}
	return &ItemOut[ExternalCycle]{Body: out}, nil
}
