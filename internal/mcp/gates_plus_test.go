package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// plusFake é o fake com os gates estendidos (ADR-0041): reenvio e a fase de plano da task do token.
type plusFake struct {
	*fake
	phase     domain.PlanPhase
	resubmit  service.ResubmitGateInput
	resubmits int
}

func (f *plusFake) ResubmitGate(_ context.Context, _ domain.Actor, _, id domain.ID, in service.ResubmitGateInput) (service.GateView, error) {
	f.resubmit = in
	f.resubmits++
	return service.GateView{Gate: domain.Gate{ID: id, Kind: domain.GatePlan, Status: domain.GatePending, Title: "Plano", Revision: 2}, TaskKey: "HNK-1"}, nil
}

func (f *plusFake) PlanPhaseFor(context.Context, domain.ID, domain.ID, domain.ID) (domain.PlanPhase, error) {
	return f.phase, nil
}

func plusSetup(t *testing.T) (*env, *plusFake) {
	t.Helper()
	e := setup(t, true)
	pf := &plusFake{fake: e.f}
	e.srv.Close()
	e.srv = httptest.NewServer(newHandler(pf, Deps{Tokens: e.tokens, Trusted: true, Version: "test"}))
	t.Cleanup(e.srv.Close)
	return e, pf
}

func TestGateRequestQuestionAndPlan(t *testing.T) {
	e, pf := plusSetup(t)
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	res := call(t, cs, "hanko_gate_request", map[string]any{"title": "Qual ambiente?", "kind": "question",
		"questions": []map[string]any{{"key": "env", "label": "Onde?", "type": "single", "required": true,
			"options": []map[string]any{{"value": "stg", "label": "Staging"}, {"value": "prd"}}}}})
	if res.IsError {
		t.Fatalf("question: %s", text(res))
	}
	in := pf.lastGate
	if in.Kind != domain.GateQuestion || in.Form == nil || len(in.Form.Fields) != 1 || in.Form.Fields[0].Key != "env" ||
		in.Form.Fields[0].Type != domain.QuestionSingle || !in.Form.Fields[0].Required || len(in.Form.Fields[0].Options) != 2 {
		t.Fatalf("form: %+v", in)
	}

	res = call(t, cs, "hanko_gate_request", map[string]any{"title": "Plano", "kind": "plan", "artifact": "plano.md", "audience_roles": []string{"admin"}})
	if res.IsError {
		t.Fatalf("plan: %s", text(res))
	}
	in = pf.lastGate
	if in.Kind != domain.GatePlan || in.Artifact != "plano.md" || len(in.Audience.Roles) != 1 || in.Audience.Roles[0] != domain.MemberAdmin {
		t.Fatalf("plan input: %+v", in)
	}

	// kinds que o Hanko abre continuam recusados
	if res = call(t, cs, "hanko_gate_request", map[string]any{"title": "x", "kind": "task_review"}); !res.IsError {
		t.Fatalf("task_review deveria ser recusado: %s", text(res))
	}
	// padrão segue approval
	if res = call(t, cs, "hanko_gate_request", map[string]any{"title": "ok?"}); res.IsError || pf.lastGate.Kind != domain.GateApproval {
		t.Fatalf("approval: %s %q", text(res), pf.lastGate.Kind)
	}
}

func TestHireRequestTool(t *testing.T) {
	e, pf := plusSetup(t)
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, "hanko_hire_request", map[string]any{"name": "Yuki", "agent": "claude", "persona_title": "Dados", "reports_to": e.f.personas[0].Slug,
		"instructions": "Analise dados.", "budget_monthly_cents": 2000})
	if res.IsError {
		t.Fatalf("hire: %s", text(res))
	}
	in := pf.lastGate
	if in.Kind != domain.GateHire || in.Title != "Contratar Yuki" || in.Hire == nil || in.Hire.Name != "Yuki" || in.Hire.Agent != "claude" ||
		in.Hire.Title != "Dados" || in.Hire.ReportsTo == nil || *in.Hire.ReportsTo != e.f.personas[0].ID ||
		in.Hire.BudgetMonthlyCents == nil || *in.Hire.BudgetMonthlyCents != 2000 {
		t.Fatalf("hire input: %+v %+v", in, in.Hire)
	}
	if res = call(t, cs, "hanko_hire_request", map[string]any{"name": "Z", "agent": "claude", "reports_to": "nao-existe"}); !res.IsError {
		t.Fatalf("chefe inexistente: %s", text(res))
	}
}

func TestGateResubmitTool(t *testing.T) {
	e, pf := plusSetup(t)
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	id := domain.NewID()
	res := call(t, cs, "hanko_gate_resubmit", map[string]any{"id": id.String(), "body": "novo", "artifact": "plano.md", "note": "ajustei"})
	if res.IsError || pf.resubmits != 1 || pf.resubmit.Body != "novo" || pf.resubmit.Artifact != "plano.md" || pf.resubmit.Note != "ajustei" {
		t.Fatalf("resubmit: %v %s %+v", res.IsError, text(res), pf.resubmit)
	}
	if !strings.Contains(text(res), "revisão 2") {
		t.Fatalf("texto: %s", text(res))
	}
	if res = call(t, cs, "hanko_gate_resubmit", map[string]any{"id": "x"}); !res.IsError {
		t.Fatal("id inválido deveria ser recusado")
	}
}

func TestPlanPhaseRefusesWriteTools(t *testing.T) {
	e, pf := plusSetup(t)
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// sem política de plano tudo segue igual
	pf.phase = domain.PlanPhaseNone
	if res := call(t, cs, "hanko_task_update", map[string]any{"key": "HNK-1", "priority": "high"}); res.IsError {
		t.Fatalf("sem plano: %s", text(res))
	}

	// na fase de planejamento: leitura, comentário, plano e gate passam; escrita é recusada
	pf.phase = domain.PlanPhasePlan
	for _, tool := range []string{"hanko_whoami", "hanko_task_list"} {
		if res := call(t, cs, tool, map[string]any{}); res.IsError {
			t.Fatalf("%s deveria passar na fase de plano: %s", tool, text(res))
		}
	}
	if res := call(t, cs, "hanko_task_comment", map[string]any{"key": "HNK-1", "body": "plano em andamento"}); res.IsError {
		t.Fatalf("comentar passa: %s", text(res))
	}
	if res := call(t, cs, "hanko_gate_request", map[string]any{"title": "Plano", "kind": "plan", "body": "1. x"}); res.IsError {
		t.Fatalf("pedir o gate de plano passa: %s", text(res))
	}
	for tool, args := range map[string]map[string]any{
		"hanko_task_update": {"key": "HNK-1", "priority": "high"},
		"hanko_task_create": {"title": "outra"},
		"hanko_goal_update": {"goal": "x", "value": 1},
	} {
		res := call(t, cs, tool, args)
		if !res.IsError || !strings.Contains(text(res), "só planeja") {
			t.Fatalf("%s deveria ser recusada na fase de plano: %v %s", tool, res.IsError, text(res))
		}
	}

	// plano aprovado: a execução escreve normalmente
	pf.phase = domain.PlanPhaseExecute
	if res := call(t, cs, "hanko_task_update", map[string]any{"key": "HNK-1", "priority": "high"}); res.IsError {
		t.Fatalf("fase de execução: %s", text(res))
	}
}
