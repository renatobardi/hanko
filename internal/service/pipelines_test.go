package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Pipelines, cases, revisão, aprendizados e exemplos de decisão (ADR-0034).

// salesPipeline cria o pipeline de vendas: novo (working, automação da persona) → qualificacao (working, com
// aprovação) → revisao (review) → ganho (done) | perdido (cancelled).
func (e *env) salesPipeline(t *testing.T, ana domain.Persona, mod func(*service.CreatePipelineInput)) service.PipelineView {
	t.Helper()
	in := service.CreatePipelineInput{Name: "Vendas", Stages: []service.StageInput{
		{Key: "novo", Name: "Novo", Kind: domain.StageWorking, Config: domain.StageConfig{
			Fields: []domain.StageField{
				{Key: "empresa", Label: "Empresa", Type: domain.FieldText, Required: true, ShowInAddForm: true},
				{Key: "porte", Label: "Porte", Type: domain.FieldSelect, Options: []string{"pequeno", "grande"}},
			},
			WhatHappensHere: "Pesquisar o lead.",
			Automation:      &domain.StageAutomation{PersonaID: &ana.ID, Instructions: "Pesquise o lead e preencha o porte."},
		}},
		{Key: "qualificacao", Name: "Qualificação", Kind: domain.StageWorking, Config: domain.StageConfig{
			Approval: &domain.StageApproval{Required: true, Approver: domain.ApproverAnyMember},
		}},
		{Key: "revisao", Name: "Revisão", Kind: domain.StageReview, Config: domain.StageConfig{
			Review: &domain.ReviewRouting{ApproveTo: "ganho", RejectTo: "perdido", RequestChangesTo: "novo"},
		}},
		{Key: "ganho", Name: "Ganho", Kind: domain.StageDone},
		{Key: "perdido", Name: "Perdido", Kind: domain.StageCancelled},
	}}
	if mod != nil {
		mod(&in)
	}
	pv, err := e.svc.CreatePipeline(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	return pv
}

func (e *env) newCase(t *testing.T, pv *service.PipelineView, mod func(*service.CreateCaseInput)) service.CaseView {
	t.Helper()
	in := service.CreateCaseInput{Title: "Acme quer 10 licenças", Type: "lead", Fields: map[string]any{"empresa": "Acme"}}
	if pv != nil {
		in.PipelineID = &pv.ID
	}
	if mod != nil {
		mod(&in)
	}
	cv, _, err := e.svc.CreateCase(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	return cv
}

func (e *env) move(t *testing.T, cv service.CaseView, to string) service.CaseView {
	t.Helper()
	out, err := e.svc.TransitionCase(context.Background(), domain.HumanActor, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: cv.Version, ToStage: to})
	if err != nil {
		t.Fatalf("move %s → %s: %v", cv.StageKey, to, err)
	}
	return out
}

func eventKinds(v service.CaseView) []string {
	var out []string
	for _, ev := range v.Events {
		out = append(out, string(ev.Kind))
	}
	return out
}

func hasKind(v service.CaseView, k domain.CaseEventKind) bool {
	for _, ev := range v.Events {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

func TestPipelineConfigValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	pv := e.salesPipeline(t, ana, nil)
	if pv.Key != "vendas" || len(pv.Stages) != 5 || pv.Stages[2].Kind != domain.StageReview || pv.Stages[0].Position != 0 || pv.Stages[4].Position != 4 {
		t.Fatalf("pipeline: %+v", pv)
	}
	ghost := domain.NewID()
	bad := []struct {
		name string
		st   service.StageInput
	}{
		{"review sem destino", service.StageInput{Key: "r", Name: "R", Kind: domain.StageReview}},
		{"destino inexistente", service.StageInput{Key: "r", Name: "R", Kind: domain.StageReview, Config: domain.StageConfig{Review: &domain.ReviewRouting{ApproveTo: "ganho", RejectTo: "nada"}}}},
		{"select sem opções", service.StageInput{Key: "x", Name: "X", Config: domain.StageConfig{Fields: []domain.StageField{{Key: "a", Label: "A", Type: domain.FieldSelect}}}}},
		{"campo duplicado", service.StageInput{Key: "x", Name: "X", Config: domain.StageConfig{Fields: []domain.StageField{{Key: "a", Label: "A", Type: "text"}, {Key: "a", Label: "B", Type: "text"}}}}},
		{"persona inexistente", service.StageInput{Key: "x", Name: "X", Config: domain.StageConfig{Automation: &domain.StageAutomation{PersonaID: &ghost}}}},
		{"instruções enormes", service.StageInput{Key: "x", Name: "X", Config: domain.StageConfig{Automation: &domain.StageAutomation{PersonaID: &ana.ID, Instructions: strings.Repeat("a", domain.MaxAutomationBytes+1)}}}},
		{"revisão decidida por persona", service.StageInput{Key: "r", Name: "R", Kind: domain.StageReview, Config: domain.StageConfig{
			Approval: &domain.StageApproval{Required: true, Approver: domain.PersonaApprover(ana.ID)}, Review: &domain.ReviewRouting{ApproveTo: "ganho", RejectTo: "perdido"}}}},
		{"aprovador estranho", service.StageInput{Key: "x", Name: "X", Config: domain.StageConfig{Approval: &domain.StageApproval{Required: true, Approver: "fulano"}}}},
		{"key duplicada", service.StageInput{Key: "novo", Name: "Novo 2"}},
	}
	for _, c := range bad {
		if _, err := e.svc.AddStage(ctx, domain.HumanActor, e.co.ID, pv.ID, c.st, nil); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v, want invalid", c.name, err)
		}
	}
	// etapa nova no meio, reordenar e remover
	pv2, err := e.svc.AddStage(ctx, domain.HumanActor, e.co.ID, pv.ID, service.StageInput{Name: "Proposta"}, ptr(1))
	if err != nil || pv2.Stages[1].Key != "proposta" || pv2.Stages[2].Key != "qualificacao" {
		t.Fatalf("add stage: %+v %v", pv2.Stages, err)
	}
	pv2, err = e.svc.ReorderStages(ctx, domain.HumanActor, e.co.ID, pv.ID, []string{"novo", "qualificacao", "proposta", "revisao", "ganho", "perdido"})
	if err != nil || pv2.Stages[2].Key != "proposta" {
		t.Fatalf("reorder: %+v %v", pv2.Stages, err)
	}
	if _, err := e.svc.ReorderStages(ctx, domain.HumanActor, e.co.ID, pv.ID, []string{"novo"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("reorder incompleto: %v", err)
	}
	if _, err := e.svc.DeleteStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "ganho"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("remover destino de revisão: %v", err)
	}
	if pv2, err = e.svc.DeleteStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "proposta"); err != nil || len(pv2.Stages) != 5 {
		t.Fatalf("delete stage: %v", err)
	}
	// etapa com case não sai nem muda de tipo
	e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.StageKey = "qualificacao" })
	if _, err := e.svc.DeleteStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "qualificacao"); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("remover etapa com case: %v", err)
	}
	k := domain.StageReview
	if _, err := e.svc.UpdateStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "qualificacao", service.UpdateStageInput{Kind: &k}); !errors.Is(err, domain.ErrConflict) && !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("mudar tipo com case: %v", err)
	}
	if err := e.svc.DeletePipeline(ctx, domain.HumanActor, e.co.ID, pv.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("excluir pipeline com cases: %v", err)
	}
	// transições e modo estrito
	pv2, err = e.svc.SetTransitions(ctx, domain.HumanActor, e.co.ID, pv.ID, []service.TransitionSpec{{From: "novo", To: "qualificacao"}, {From: "novo", To: "qualificacao"}}, true)
	if err != nil || len(pv2.Transitions) != 1 || !pv2.EnforceTransitions {
		t.Fatalf("transitions: %+v %v", pv2, err)
	}
	if _, err := e.svc.SetTransitions(ctx, domain.HumanActor, e.co.ID, pv.ID, []service.TransitionSpec{{From: "novo", To: "xx"}}, true); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("transição para etapa inexistente: %v", err)
	}
	// persona não desenha pipeline
	if _, err := e.svc.CreatePipeline(ctx, domain.PersonaActor(ana.ID), e.co.ID, service.CreatePipelineInput{Name: "X"}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("persona cria pipeline: %v", err)
	}
	list, err := e.svc.ListPipelines(ctx, e.co.ID, false)
	if err != nil || len(list) != 1 || list[0].OpenCases != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err := e.svc.UpdatePipeline(ctx, domain.HumanActor, e.co.ID, pv.ID, service.UpdatePipelineInput{Archived: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.ListPipelines(ctx, e.co.ID, false); len(list) != 0 {
		t.Errorf("arquivado aparece na lista padrão")
	}
	if _, _, err := e.svc.CreateCase(ctx, domain.HumanActor, e.co.ID, service.CreateCaseInput{Title: "x", PipelineID: &pv.ID, Fields: map[string]any{"empresa": "a"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("case em pipeline arquivado: %v", err)
	}
}

func TestCaseTransitionRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	pv := e.salesPipeline(t, ana, nil)

	// campo obrigatório do formulário de criação
	if _, _, err := e.svc.CreateCase(ctx, domain.HumanActor, e.co.ID, service.CreateCaseInput{Title: "sem empresa", PipelineID: &pv.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem campo obrigatório: %v", err)
	}
	cv := e.newCase(t, &pv, nil)
	if cv.StageKey != "novo" || cv.Status != domain.CaseInProgress || cv.Version != 1 || !strings.HasSuffix(cv.Identifier, "-C1") {
		t.Fatalf("case novo: %+v", cv.Case)
	}

	// versão otimista
	_, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: 7, ToStage: "qualificacao"})
	if cur, ok := domain.CurrentVersion(err); !ok || cur != cv.Version || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("versão errada: %v", err)
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.TransitionCaseInput{ToStage: "qualificacao"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("sem versão: %v", err)
	}
	// sair da etapa de revisão só decidindo; e não se entra numa etapa desligada
	dis := domain.StageConfig{Disabled: true, DisabledReason: "em manutenção"}
	if _, err := e.svc.UpdateStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "qualificacao", service.UpdateStageInput{Config: &dis}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: cv.Version, ToStage: "qualificacao"}); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "manutenção") {
		t.Errorf("etapa desligada: %v", err)
	}
	appr := domain.StageConfig{Approval: &domain.StageApproval{Required: true}}
	if _, err := e.svc.UpdateStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "qualificacao", service.UpdateStageInput{Config: &appr}); err != nil {
		t.Fatal(err)
	}

	// modo estrito: fora das arestas só com force + motivo
	if _, err := e.svc.SetTransitions(ctx, domain.HumanActor, e.co.ID, pv.ID, []service.TransitionSpec{{From: "novo", To: "qualificacao"}, {From: "qualificacao", To: "revisao"}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: cv.Version, ToStage: "revisao"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("estrito sem force: %v", err)
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: cv.Version, ToStage: "revisao", Force: true}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("force sem motivo: %v", err)
	}
	forced, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: cv.Version, ToStage: "revisao", Force: true, Reason: "cliente com pressa"})
	if err != nil || forced.StageKey != "revisao" || forced.Status != domain.CaseInReview || forced.Version != 2 {
		t.Fatalf("forçada: %+v %v", forced.Case, err)
	}
	full, _ := e.svc.GetCaseView(ctx, e.co.ID, forced.Identifier)
	if !hasKind(full, domain.CaseEvTransitionForced) {
		t.Errorf("evento transition_forced: %v", eventKinds(full))
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: 2, ToStage: "novo", Force: true, Reason: "x"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("sair de revisão sem decidir: %v", err)
	}

	// aprovador da etapa: só o membro escolhido (ou admin) tira o case dali
	if _, err := e.svc.SetTransitions(ctx, domain.HumanActor, e.co.ID, pv.ID, nil, false); err != nil {
		t.Fatal(err)
	}
	ivo := mkUser(t, e, "ivo@x.com", domain.RoleMember)
	join(t, e, ivo, domain.MemberMember)
	lia := mkUser(t, e, "lia@x.com", domain.RoleMember)
	join(t, e, lia, domain.MemberMember)
	onlyIvo := domain.StageConfig{Approval: &domain.StageApproval{Required: true, Approver: domain.MemberApprover(ivo.ID)}}
	if _, err := e.svc.UpdateStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "qualificacao", service.UpdateStageInput{Config: &onlyIvo}); err != nil {
		t.Fatal(err)
	}
	c2 := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "Beta"; in.StageKey = "qualificacao" })
	if _, err := e.svc.TransitionCase(as(lia), lia.Actor(), e.co.ID, c2.ID, service.TransitionCaseInput{ExpectedVersion: c2.Version, ToStage: "revisao"}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("não aprovador: %v", err)
	}
	if c2, err = e.svc.TransitionCase(as(ivo), ivo.Actor(), e.co.ID, c2.ID, service.TransitionCaseInput{ExpectedVersion: c2.Version, ToStage: "revisao"}); err != nil {
		t.Fatalf("aprovador: %v", err)
	}

	// bloqueios: um case aberto bloqueia; ao chegar a done libera e chama atenção
	c3 := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "Gama" })
	c4 := e.newCase(t, nil, func(in *service.CreateCaseInput) { in.Title = "Contrato assinado" })
	if c3, err = e.svc.SetCaseBlockers(ctx, domain.HumanActor, e.co.ID, c3.ID, c3.Version, []domain.ID{c4.ID}, nil); err != nil || c3.OpenBlockers != 1 {
		t.Fatalf("set blockers: %+v %v", c3, err)
	}
	if _, err := e.svc.SetCaseBlockers(ctx, domain.HumanActor, e.co.ID, c4.ID, c4.Version, []domain.ID{c3.ID}, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("ciclo de bloqueio: %v", err)
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, c3.ID, service.TransitionCaseInput{ExpectedVersion: c3.Version, ToStage: "qualificacao"}); !errors.Is(err, domain.ErrCaseBlocked) {
		t.Errorf("bloqueado: %v", err)
	}
	done := domain.CaseDone
	if _, err := e.svc.UpdateCase(ctx, domain.HumanActor, e.co.ID, c4.ID, service.UpdateCaseInput{ExpectedVersion: c4.Version, Status: &done}); err != nil {
		t.Fatal(err)
	}
	c3, _ = e.svc.GetCaseView(ctx, e.co.ID, c3.ID.String())
	if c3.OpenBlockers != 0 || c3.Attention != "blockers_resolved" || !hasKind(c3, domain.CaseEvBlockersResolved) {
		t.Fatalf("bloqueio resolvido: %+v %v", c3.Case, eventKinds(c3))
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, c3.ID, service.TransitionCaseInput{ExpectedVersion: c3.Version, ToStage: "qualificacao"}); err != nil {
		t.Errorf("liberado: %v", err)
	}

	// filhos: com require_children_terminal, o pai só sai quando todos terminam
	gates := domain.StageConfig{Gates: &domain.StageGates{RequireChildrenTerminal: true}}
	if _, err := e.svc.UpdateStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "qualificacao", service.UpdateStageInput{Config: &gates}); err != nil {
		t.Fatal(err)
	}
	parent := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "Pai"; in.StageKey = "qualificacao" })
	kid := e.newCase(t, nil, func(in *service.CreateCaseInput) { in.Title = "Filho"; in.ParentID = &parent.ID })
	parent, _ = e.svc.GetCaseView(ctx, e.co.ID, parent.ID.String())
	if parent.ChildCount != 1 || parent.TerminalChildCount != 0 {
		t.Fatalf("contadores: %+v", parent.Case)
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, parent.ID, service.TransitionCaseInput{ExpectedVersion: parent.Version, ToStage: "revisao"}); !errors.Is(err, domain.ErrCaseBlocked) {
		t.Errorf("filhos abertos: %v", err)
	}
	if _, err := e.svc.UpdateCase(ctx, domain.HumanActor, e.co.ID, kid.ID, service.UpdateCaseInput{ExpectedVersion: kid.Version, Status: &done}); err != nil {
		t.Fatal(err)
	}
	parent, _ = e.svc.GetCaseView(ctx, e.co.ID, parent.ID.String())
	if parent.TerminalChildCount != 1 || parent.Attention != "children_terminal" || !hasKind(parent, domain.CaseEvChildrenTerminal) {
		t.Fatalf("filhos terminaram: %+v", parent.Case)
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, parent.ID, service.TransitionCaseInput{ExpectedVersion: parent.Version, ToStage: "revisao"}); err != nil {
		t.Errorf("pai liberado: %v", err)
	}
	// campo obrigatório para sair da etapa
	req := domain.StageConfig{Fields: []domain.StageField{{Key: "valor", Label: "Valor", Type: domain.FieldNumber, Required: true}}}
	if _, err := e.svc.UpdateStage(ctx, domain.HumanActor, e.co.ID, pv.ID, "qualificacao", service.UpdateStageInput{Config: &req}); err != nil {
		t.Fatal(err)
	}
	c5 := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "Delta"; in.StageKey = "qualificacao" })
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, c5.ID, service.TransitionCaseInput{ExpectedVersion: c5.Version, ToStage: "revisao"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("campo obrigatório vazio: %v", err)
	}
	if _, err := e.svc.UpdateCase(ctx, domain.HumanActor, e.co.ID, c5.ID, service.UpdateCaseInput{ExpectedVersion: c5.Version, Fields: map[string]any{"valor": "abc"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("número inválido: %v", err)
	}
	c5, err = e.svc.UpdateCase(ctx, domain.HumanActor, e.co.ID, c5.ID, service.UpdateCaseInput{ExpectedVersion: c5.Version, Fields: map[string]any{"valor": 1200.0}})
	if err != nil || c5.Version != 2 {
		t.Fatalf("update fields: %v", err)
	}
	// cancelar sempre passa (mesmo bloqueado ou incompleto)
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, c5.ID, service.TransitionCaseInput{ExpectedVersion: c5.Version, ToStage: "perdido"}); err != nil {
		t.Errorf("cancelar: %v", err)
	}
	// case solto não troca de etapa; case de pipeline não troca status à mão
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, c4.ID, service.TransitionCaseInput{ExpectedVersion: 2, ToStage: "novo"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("solto: %v", err)
	}
	c6 := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "Épsilon" })
	if _, err := e.svc.UpdateCase(ctx, domain.HumanActor, e.co.ID, c6.ID, service.UpdateCaseInput{ExpectedVersion: c6.Version, Status: &done}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("status à mão em pipeline: %v", err)
	}
}

func TestCaseReviewAndLearnings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	pv := e.salesPipeline(t, ana, nil)
	cv := e.newCase(t, &pv, nil)
	// a automação da etapa inicial criou a task da Ana
	cv, _ = e.svc.GetCaseView(ctx, e.co.ID, cv.ID.String())
	if len(cv.Links) != 1 || cv.Links[0].Role != domain.LinkAutomation || !hasKind(cv, domain.CaseEvAutomationStarted) {
		t.Fatalf("automação inicial: %+v %v", cv.Links, eventKinds(cv))
	}
	cv = e.move(t, cv, "qualificacao")
	cv = e.move(t, cv, "revisao")

	review := func(c service.CaseView, d domain.ReviewDecision, reason string) (service.CaseView, error) {
		return e.svc.ReviewCase(ctx, domain.HumanActor, e.co.ID, c.ID, service.ReviewCaseInput{ExpectedVersion: c.Version, Decision: d, Reason: reason})
	}
	if _, err := review(cv, domain.ReviewRequestChanges, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("mudanças sem motivo: %v", err)
	}
	if _, err := review(cv, domain.ReviewReject, " "); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("recusa sem motivo: %v", err)
	}
	if _, err := e.svc.ReviewCase(ctx, domain.PersonaActor(ana.ID), e.co.ID, cv.ID, service.ReviewCaseInput{ExpectedVersion: cv.Version, Decision: domain.ReviewApprove}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("persona decide: %v", err)
	}
	viewer := mkUser(t, e, "v@x.com", domain.RoleMember)
	join(t, e, viewer, domain.MemberViewer)
	if _, err := e.svc.ReviewCase(as(viewer), viewer.Actor(), e.co.ID, cv.ID, service.ReviewCaseInput{ExpectedVersion: cv.Version, Decision: domain.ReviewApprove}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer decide: %v", err)
	}
	// pedir mudanças volta para "novo" e roda a automação de novo, com o motivo e o aprendizado no contexto
	cv, err := review(cv, domain.ReviewRequestChanges, "Faltou o CNPJ da empresa")
	if err != nil || cv.StageKey != "novo" {
		t.Fatalf("request_changes: %+v %v", cv.Case, err)
	}
	cv, _ = e.svc.GetCaseView(ctx, e.co.ID, cv.ID.String())
	var autos []service.CaseLink
	for _, l := range cv.Links {
		if l.Role == domain.LinkAutomation {
			autos = append(autos, l)
		}
	}
	if len(autos) != 2 {
		t.Fatalf("automação de novo: %+v", cv.Links)
	}
	task, err := e.svc.GetTask(ctx, e.co.ID, autos[1].TaskKey)
	if err != nil {
		t.Fatal(err)
	}
	if task.PersonaID == nil || *task.PersonaID != ana.ID || !strings.Contains(task.Description, "Faltou o CNPJ") ||
		!strings.Contains(task.Description, "Aprendizados desta etapa") || !strings.Contains(task.Description, "Pesquise o lead") {
		t.Fatalf("pacote de contexto:\n%s", task.Description)
	}
	// a primeira task (aberta) recebeu o comentário de sistema da movimentação
	cs, _ := e.svc.ListComments(ctx, e.co.ID, autos[0].TaskID)
	if len(cs) == 0 || cs[len(cs)-1].Author.Type != domain.ActorSystem {
		t.Errorf("comentário de sistema: %+v", cs)
	}

	cv = e.move(t, cv, "qualificacao")
	cv = e.move(t, cv, "revisao")
	approved, err := e.svc.ReviewCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.ReviewCaseInput{ExpectedVersion: cv.Version, Decision: domain.ReviewApprove,
		Reason: "Lead bom", Title: ptr("Acme — 10 licenças")})
	if err != nil || approved.StageKey != "ganho" || approved.Status != domain.CaseDone || approved.TerminalKind != "done" || approved.Title != "Acme — 10 licenças" ||
		approved.ApprovedVersion == nil || *approved.ApprovedVersion != approved.Version {
		t.Fatalf("approve: %+v %v", approved.Case, err)
	}

	ls, err := e.svc.ListLearnings(ctx, e.co.ID, service.LearningFilter{})
	if err != nil || len(ls) != 2 || ls[0].Decision != "approve" || ls[1].Decision != "request_changes" || ls[1].Reason != "Faltou o CNPJ da empresa" ||
		ls[0].ToName != "Ganho" || ls[0].Snapshot["title"] != "Acme — 10 licenças" {
		t.Fatalf("learnings: %+v %v", ls, err)
	}
	if ls, _ := e.svc.ListLearnings(ctx, e.co.ID, service.LearningFilter{Decision: "request_changes"}); len(ls) != 1 {
		t.Errorf("filtro decisão: %d", len(ls))
	}
	if _, err := e.svc.ListLearnings(ctx, e.co.ID, service.LearningFilter{Decision: "talvez"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("decisão inválida: %v", err)
	}
	cl, err := e.svc.CaseLearnings(ctx, e.co.ID, approved.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = cl
}

func TestReviewOutdatedAndBulk(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	// aprovar devolve para uma etapa de trabalho (entrega), para testar "mudou depois de aprovado"
	pv := e.salesPipeline(t, ana, func(in *service.CreatePipelineInput) {
		in.Stages = append(in.Stages, service.StageInput{Key: "entrega", Name: "Entrega", Kind: domain.StageWorking})
		in.Stages[2].Config.Review.ApproveTo = "entrega"
		in.Stages[0].Config.Automation = nil
	})
	a := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "A"; in.StageKey = "revisao" })
	b := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "B"; in.StageKey = "revisao" })
	c := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "C"; in.StageKey = "revisao" })

	q, err := e.svc.ReviewQueue(ctx, e.co.ID)
	if err != nil || len(q) != 3 || q[0].Kind != "review" || q[0].ApproveToName != "Entrega" || !q[0].RequireRejectReason {
		t.Fatalf("fila: %+v %v", q, err)
	}
	if n, _ := e.svc.ReviewQueueCount(ctx, e.co.ID); n != 3 {
		t.Errorf("contagem: %d", n)
	}
	// B mudou depois de lido: o item dele falha, os outros passam
	if _, err := e.svc.UpdateCase(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdateCaseInput{ExpectedVersion: b.Version, Summary: ptr("novo resumo")}); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.BulkApprove(ctx, domain.HumanActor, e.co.ID, []service.BulkReviewItem{{CaseID: a.ID, ExpectedVersion: a.Version}, {CaseID: b.ID, ExpectedVersion: b.Version},
		{CaseID: c.ID, ExpectedVersion: c.Version}})
	if err != nil || len(res) != 3 || !res[0].OK || res[1].OK || res[1].CurrentVersion != 2 || !res[2].OK {
		t.Fatalf("lote: %+v %v", res, err)
	}
	if n, _ := e.svc.ReviewQueueCount(ctx, e.co.ID); n != 1 {
		t.Errorf("contagem depois do lote: %d", n)
	}
	// A foi aprovado para "entrega"; editar depois exige nova revisão para a próxima saída
	av, _ := e.svc.GetCaseView(ctx, e.co.ID, a.ID.String())
	av, err = e.svc.UpdateCase(ctx, domain.HumanActor, e.co.ID, av.ID, service.UpdateCaseInput{ExpectedVersion: av.Version, Fields: map[string]any{"empresa": "Acme SA"}})
	if err != nil || !av.ReapprovalRequired {
		t.Fatalf("reaprovação: %+v %v", av.Case, err)
	}
	if _, err := e.svc.TransitionCase(ctx, domain.HumanActor, e.co.ID, av.ID, service.TransitionCaseInput{ExpectedVersion: av.Version, ToStage: "ganho"}); !errors.Is(err, domain.ErrReviewOutdated) {
		t.Errorf("review_outdated: %v", err)
	}
	av = e.move(t, av, "revisao")
	av, err = e.svc.ReviewCase(ctx, domain.HumanActor, e.co.ID, av.ID, service.ReviewCaseInput{ExpectedVersion: av.Version, Decision: domain.ReviewApprove})
	if err != nil || av.ReapprovalRequired {
		t.Fatalf("nova aprovação: %+v %v", av.Case, err)
	}
	if _, err := e.svc.BulkApprove(ctx, domain.HumanActor, e.co.ID, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("lote vazio: %v", err)
	}
}

func TestCaseUpsertAndPersona(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	pv := e.salesPipeline(t, ana, func(in *service.CreatePipelineInput) {
		in.Stages[0].Config.Automation = nil
		in.Stages = append(in.Stages[:1], append([]service.StageInput{{Key: "pesquisa", Name: "Pesquisa", Kind: domain.StageWorking}}, in.Stages[1:]...)...)
	})
	tv := e.task(t, service.CreateTaskInput{Title: "prospectar", PersonaID: &ana.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(ana, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	pa := domain.PersonaActor(ana.ID)
	in := service.CreateCaseInput{Title: "Lead Acme", Type: "Lead", Key: "acme.com", PipelineID: &pv.ID, Fields: map[string]any{"empresa": "Acme"}, TaskID: &tv.ID}
	if _, _, err := e.svc.CreateCase(ctx, pa, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("persona sem run: %v", err)
	}
	in.RunID = &run.ID
	cv, created, err := e.svc.CreateCase(ctx, pa, e.co.ID, in)
	if err != nil || !created || cv.Type != "lead" || cv.OriginRunID == nil {
		t.Fatalf("persona cria: %+v %v", cv.Case, err)
	}
	in.Summary, in.Fields = "Empresa de 200 pessoas", map[string]any{"porte": "grande"}
	cv2, created, err := e.svc.CreateCase(ctx, pa, e.co.ID, in)
	if err != nil || created || cv2.ID != cv.ID || cv2.Summary != "Empresa de 200 pessoas" || cv2.Fields["porte"] != "grande" || cv2.Fields["empresa"] != "Acme" || cv2.Version != 2 {
		t.Fatalf("upsert: %+v created=%v %v", cv2.Case, created, err)
	}
	full, _ := e.svc.GetCaseView(ctx, e.co.ID, cv.ID.String())
	if len(full.Links) != 1 || full.Links[0].Role != domain.LinkOrigin || full.Links[0].TaskID != tv.ID {
		t.Fatalf("task de origem ligada: %+v", full.Links)
	}
	// persona: entre working sem aprovação sim; nunca para revisão, nunca force
	mv, err := e.svc.TransitionCase(ctx, pa, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: cv2.Version, ToStage: "pesquisa", RunID: &run.ID, TaskID: &tv.ID})
	if err != nil || mv.StageKey != "pesquisa" {
		t.Fatalf("persona move working: %v", err)
	}
	if _, err := e.svc.TransitionCase(ctx, pa, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: mv.Version, ToStage: "revisao", RunID: &run.ID}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("persona para revisão: %v", err)
	}
	if _, err := e.svc.TransitionCase(ctx, pa, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: mv.Version, ToStage: "qualificacao", RunID: &run.ID, Force: true, Reason: "x"}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("persona força: %v", err)
	}
	mv = e.move(t, mv, "qualificacao")
	if _, err := e.svc.TransitionCase(ctx, pa, e.co.ID, cv.ID, service.TransitionCaseInput{ExpectedVersion: mv.Version, ToStage: "novo", RunID: &run.ID}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("persona sai de etapa com aprovação: %v", err)
	}
	// sugestão: persona sugere, humano aceita
	sg, err := e.svc.SuggestCaseTransition(ctx, pa, e.co.ID, cv.ID, service.SuggestInput{ExpectedVersion: mv.Version, ToStage: "revisao", Rationale: "tudo pronto", Confidence: 0.9, RunID: &run.ID})
	if err != nil || sg.PendingSuggestion == nil || sg.PendingSuggestion.ToStage != "revisao" || sg.PendingSuggestion.PersonaID == nil {
		t.Fatalf("suggest: %+v %v", sg.Case, err)
	}
	if _, err := e.svc.SuggestCaseTransition(ctx, pa, e.co.ID, cv.ID, service.SuggestInput{ExpectedVersion: sg.Version, ToStage: "revisao", Rationale: "x", Confidence: 2, RunID: &run.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("confiança fora de 0..1: %v", err)
	}
	q, _ := e.svc.ReviewQueue(ctx, e.co.ID)
	if len(q) != 1 || q[0].Kind != "suggestion" || q[0].SuggestedStageName != "Revisão" {
		t.Fatalf("fila com sugestão: %+v", q)
	}
	if _, err := e.svc.ResolveSuggestion(ctx, pa, e.co.ID, cv.ID, service.ResolveSuggestionInput{ExpectedVersion: sg.Version, Accept: true}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("persona resolve: %v", err)
	}
	acc, err := e.svc.ResolveSuggestion(ctx, domain.HumanActor, e.co.ID, cv.ID, service.ResolveSuggestionInput{ExpectedVersion: sg.Version, Accept: true})
	if err != nil || acc.StageKey != "revisao" || acc.PendingSuggestion != nil {
		t.Fatalf("aceitar: %+v %v", acc.Case, err)
	}
	// persona liga só a task da própria run
	other := e.task(t, service.CreateTaskInput{Title: "outra"})
	if _, err := e.svc.LinkCaseTask(ctx, pa, e.co.ID, cv.ID, other.ID, domain.LinkWork, &run.ID); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("persona liga task alheia: %v", err)
	}
	// tipo+key único: o mesmo par em outro tipo é outro case
	_, created, err = e.svc.CreateCase(ctx, domain.HumanActor, e.co.ID, service.CreateCaseInput{Title: "outro", Type: "cliente", Key: "acme.com"})
	if err != nil || !created {
		t.Fatalf("outro tipo: %v", err)
	}
	if list, _ := e.svc.ListCases(ctx, e.co.ID, service.CaseFilter{Type: "lead"}); len(list) != 1 {
		t.Errorf("filtro tipo: %d", len(list))
	}
	if list, _ := e.svc.ListCases(ctx, e.co.ID, service.CaseFilter{Q: "acme"}); len(list) != 2 {
		t.Errorf("busca: %d", len(list))
	}
	// referência por identificador e por número
	if v, err := e.svc.GetCaseView(ctx, e.co.ID, "C1"); err != nil || v.ID != cv.ID {
		t.Errorf("ref C1: %v", err)
	}
	// abrir conversa cria a task ligada
	conv, err := e.svc.OpenCaseConversation(ctx, domain.HumanActor, e.co.ID, cv.ID)
	if err != nil || !strings.HasPrefix(conv.Title, "Discutir: ") {
		t.Fatalf("conversa: %v", err)
	}
	if cs, _ := e.svc.CasesForTask(ctx, e.co.ID, conv.ID); len(cs) != 1 || cs[0].Role != domain.LinkConversation {
		t.Errorf("cases da task: %+v", cs)
	}
}

func TestAutomationIdempotentAndFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	pv := e.salesPipeline(t, ana, nil)
	cv := e.newCase(t, &pv, nil)
	full, _ := e.svc.GetCaseView(ctx, e.co.ID, cv.ID.String())
	var created *domain.CaseEvent
	for i := range full.Events {
		if full.Events[i].Kind == domain.CaseEvCreated {
			created = &full.Events[i]
		}
	}
	if created == nil || len(full.Links) != 1 {
		t.Fatalf("eventos: %v", eventKinds(full))
	}
	// a mesma chave de idempotência (case, etapa, evento) devolve a mesma task
	key := "case-automation:" + cv.ID.String() + ":" + pv.Stages[0].ID.String() + ":" + created.ID.String()
	again, err := e.svc.CreateTask(ctx, domain.SystemActor, e.co.ID, service.CreateTaskInput{Title: "x", EventID: key})
	if err != nil || again.ID != full.Links[0].TaskID {
		t.Fatalf("idempotência: %v %v", again.ID, err)
	}
	// persona removida: a movimentação passa e o histórico registra a falha
	if _, err := e.svc.Pool.Exec(ctx, `DELETE FROM personas WHERE id = $1`, ana.ID); err != nil {
		t.Fatal(err)
	}
	cv2 := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.Title = "sem persona" })
	cv2, _ = e.svc.GetCaseView(ctx, e.co.ID, cv2.ID.String())
	if !hasKind(cv2, domain.CaseEvAutomationFailed) || len(cv2.Links) != 0 {
		t.Fatalf("falha da automação: %v", eventKinds(cv2))
	}
	pkg, err := e.svc.GetCaseContext(ctx, e.co.ID, cv2.ID)
	if err != nil || !strings.Contains(pkg, cv2.Identifier) || !strings.Contains(pkg, "Pesquisar o lead.") {
		t.Fatalf("contexto: %v\n%s", err, pkg)
	}
}

func TestCaseEventsImmutable(t *testing.T) {
	e := newEnv(t)
	cv := e.newCase(t, nil, nil)
	if _, err := e.svc.Pool.Exec(context.Background(), `UPDATE case_events SET kind = 'updated' WHERE case_id = $1`, cv.ID); err == nil {
		t.Fatal("case_events aceitou UPDATE")
	}
}

func TestCaseRolesAndIsolation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cv := e.newCase(t, nil, nil)
	viewer := mkUser(t, e, "v@x.com", domain.RoleMember)
	join(t, e, viewer, domain.MemberViewer)
	stranger := mkUser(t, e, "s@x.com", domain.RoleMember)
	if _, err := e.svc.GetCaseView(as(viewer), e.co.ID, cv.ID.String()); err != nil {
		t.Errorf("viewer lê: %v", err)
	}
	if _, _, err := e.svc.CreateCase(as(viewer), viewer.Actor(), e.co.ID, service.CreateCaseInput{Title: "x"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer cria: %v", err)
	}
	if _, err := e.svc.GetCaseView(as(stranger), e.co.ID, cv.ID.String()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("não membro: %v", err)
	}
	if _, err := e.svc.CreatePipeline(as(viewer), viewer.Actor(), e.co.ID, service.CreatePipelineInput{Name: "X"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer cria pipeline: %v", err)
	}
	// outra empresa não enxerga o case
	other, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetCaseView(ctx, other.ID, cv.ID.String()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("isolamento: %v", err)
	}
}

func TestDecisionExamples(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "deploy", PersonaID: &ana.ID})
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tk.ID, service.AddCommentInput{Body: "antes da decisão"}); err != nil {
		t.Fatal(err)
	}
	g, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "Posso publicar?", TaskID: &tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveDecisionExample(ctx, domain.HumanActor, e.co.ID, service.SaveDecisionExampleInput{SourceKind: domain.DecisionFromGate, SourceID: g.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("gate pendente: %v", err)
	}
	base := time.Now().UTC()
	e.svc.Now = func() time.Time { return base.Add(time.Minute) }
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "ok, mas avise"}); err != nil {
		t.Fatal(err)
	}
	e.svc.Now = func() time.Time { return base.Add(time.Hour) }
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tk.ID, service.AddCommentInput{Body: "depois da decisão"}); err != nil {
		t.Fatal(err)
	}
	u1 := mkUser(t, e, "u1@x.com", domain.RoleMember)
	join(t, e, u1, domain.MemberMember)
	u2 := mkUser(t, e, "u2@x.com", domain.RoleMember)
	join(t, e, u2, domain.MemberAdmin)
	ex, err := e.svc.SaveDecisionExample(as(u1), u1.Actor(), e.co.ID, service.SaveDecisionExampleInput{SourceKind: domain.DecisionFromGate, SourceID: g.ID, Notes: "bom exemplo"})
	if err != nil || ex.DecisionOutcome != "approved" || ex.TaskID == nil {
		t.Fatalf("salvar: %+v %v", ex, err)
	}
	raw, _ := json.Marshal(ex.Snapshot)
	if !strings.Contains(string(raw), "antes da decisão") || strings.Contains(string(raw), "depois da decisão") || !strings.Contains(string(raw), "ok, mas avise") {
		t.Fatalf("snapshot no corte: %s", raw)
	}
	if _, err := e.svc.SaveDecisionExample(as(u1), u1.Actor(), e.co.ID, service.SaveDecisionExampleInput{SourceKind: domain.DecisionFromGate, SourceID: g.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicado do mesmo autor: %v", err)
	}
	if _, err := e.svc.UpdateDecisionExampleNotes(as(u2), u2.Actor(), e.co.ID, ex.ID, "x"); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("outro edita: %v", err)
	}
	if err := e.svc.DeleteDecisionExample(as(u2), u2.Actor(), e.co.ID, ex.ID); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("outro apaga: %v", err)
	}
	ex, err = e.svc.UpdateDecisionExampleNotes(as(u1), u1.Actor(), e.co.ID, ex.ID, "ótimo exemplo")
	if err != nil || ex.Notes != "ótimo exemplo" || len(ex.NotesHistory) != 1 || ex.NotesHistory[0].Notes != "bom exemplo" {
		t.Fatalf("editar notas: %+v %v", ex, err)
	}
	// revisão de case como fonte
	pv := e.salesPipeline(t, ana, func(in *service.CreatePipelineInput) { in.Stages[0].Config.Automation = nil })
	cv := e.newCase(t, &pv, func(in *service.CreateCaseInput) { in.StageKey = "revisao" })
	if _, err := e.svc.ReviewCase(ctx, domain.HumanActor, e.co.ID, cv.ID, service.ReviewCaseInput{ExpectedVersion: cv.Version, Decision: domain.ReviewReject, Reason: "fora do perfil"}); err != nil {
		t.Fatal(err)
	}
	ls, _ := e.svc.ListLearnings(ctx, e.co.ID, service.LearningFilter{})
	ex2, err := e.svc.SaveDecisionExample(as(u2), u2.Actor(), e.co.ID, service.SaveDecisionExampleInput{SourceKind: domain.DecisionFromCaseReview, SourceID: ls[0].EventID, Notes: "perfil"})
	if err != nil || ex2.DecisionOutcome != "reject" || ex2.CaseID == nil {
		t.Fatalf("exemplo de revisão: %+v %v", ex2, err)
	}
	// exportar JSONL: só admin
	var buf bytes.Buffer
	if _, err := e.svc.ExportDecisionExamples(as(u1), e.co.ID, &buf); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member exporta: %v", err)
	}
	n, err := e.svc.ExportDecisionExamples(as(u2), e.co.ID, &buf)
	if err != nil || n != 2 {
		t.Fatalf("export: %d %v", n, err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("linhas: %q", buf.String())
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m["snapshot"] == nil || m["decision_outcome"] == "" {
			t.Errorf("linha JSONL: %s %v", l, err)
		}
	}
	if err := e.svc.DeleteDecisionExample(as(u1), u1.Actor(), e.co.ID, ex.ID); err != nil {
		t.Errorf("autor apaga: %v", err)
	}
	if list, _ := e.svc.ListDecisionExamples(ctx, e.co.ID, service.DecisionExampleFilter{}); len(list) != 1 {
		t.Errorf("lista: %d", len(list))
	}
}
