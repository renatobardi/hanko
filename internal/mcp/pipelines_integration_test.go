//go:build integration

package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

type noCaseJobs struct{}

func (noCaseJobs) WakeTx(context.Context, pgx.Tx, service.Wake) error                { return nil }
func (noCaseJobs) ExecuteRunTx(context.Context, pgx.Tx, domain.ID, domain.ID) error  { return nil }
func (noCaseJobs) StartReviewTx(context.Context, pgx.Tx, domain.ID, domain.ID) error { return nil }
func (noCaseJobs) DeliverMemoryTx(context.Context, pgx.Tx, domain.ID) error          { return nil }

// As tools de case da persona contra o service e o banco reais (ADR-0034).
func TestCaseToolsPersona(t *testing.T) {
	ctx := context.Background()
	svc := service.New(testdb.New(t), noCaseJobs{}, events.NewBroker())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	ana, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreatePipeline(ctx, domain.HumanActor, co.ID, service.CreatePipelineInput{Name: "Vendas", Stages: []service.StageInput{
		{Key: "novo", Name: "Novo", Config: domain.StageConfig{Fields: []domain.StageField{{Key: "empresa", Label: "Empresa", Type: domain.FieldText, Required: true}}}},
		{Key: "pesquisa", Name: "Pesquisa"},
		{Key: "revisao", Name: "Revisão", Kind: domain.StageReview, Config: domain.StageConfig{Review: &domain.ReviewRouting{ApproveTo: "ganho", RejectTo: "ganho"}}},
		{Key: "ganho", Name: "Ganho", Kind: domain.StageDone},
	}}); err != nil {
		t.Fatal(err)
	}
	tk, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "prospectar", PersonaID: &ana.ID})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.CheckoutRun(ctx, service.Wake{CompanyID: co.ID, PersonaID: ana.ID, TaskID: tk.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	tok, err := tokens.Issue(co.ID, ana.ID, run.ID, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newHandler(svc, Deps{Tokens: tokens, Version: "test"}))
	t.Cleanup(srv.Close)
	e := &env{srv: srv}
	cs, err := e.connect(t, tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	var pl pipelineListOut
	structured(t, call(t, cs, "hanko_pipeline_list", map[string]any{}), &pl)
	if len(pl.Pipelines) != 1 || len(pl.Pipelines[0].Stages) != 4 || !pl.Pipelines[0].Stages[0].Fields[0].Required {
		t.Fatalf("pipelines: %+v", pl)
	}
	var cr caseResult
	res := call(t, cs, "hanko_case_create", map[string]any{"pipeline": "vendas", "title": "Lead Acme", "case_type": "lead", "key": "acme.com",
		"fields": map[string]any{"empresa": "Acme"}})
	if res.IsError {
		t.Fatal(text(res))
	}
	structured(t, res, &cr)
	if cr.Created == nil || !*cr.Created || cr.Case.Stage != "novo" || cr.Case.Version != 1 {
		t.Fatalf("create: %+v", cr)
	}
	// upsert pelo mesmo tipo+key
	res = call(t, cs, "hanko_case_create", map[string]any{"title": "Lead Acme", "case_type": "lead", "key": "acme.com", "summary": "200 pessoas"})
	cr = caseResult{}
	structured(t, res, &cr)
	if cr.Created == nil || *cr.Created || cr.Case.Summary != "200 pessoas" {
		t.Fatalf("upsert: %+v %s", cr, text(res))
	}
	id := cr.Case.Identifier
	// update exige a versão; versão velha devolve a atual
	if res = call(t, cs, "hanko_case_update", map[string]any{"case": id, "summary": "x"}); !res.IsError {
		t.Errorf("update sem versão passou")
	}
	if res = call(t, cs, "hanko_case_update", map[string]any{"case": id, "expected_version": 1, "summary": "x"}); !res.IsError || !strings.Contains(text(res), "versão atual 2") {
		t.Errorf("versão velha: %s", text(res))
	}
	res = call(t, cs, "hanko_case_update", map[string]any{"case": id, "expected_version": 2, "fields": map[string]any{"porte": "grande"}})
	cr = caseResult{}
	structured(t, res, &cr)
	if cr.Case.Version != 3 || cr.Case.Fields["porte"] != "grande" {
		t.Fatalf("update: %+v %s", cr.Case, text(res))
	}
	// mover entre working sim; para revisão não (sugere)
	if res = call(t, cs, "hanko_case_transition", map[string]any{"case": id, "to_stage": "pesquisa"}); res.IsError {
		t.Fatal(text(res))
	}
	if res = call(t, cs, "hanko_case_transition", map[string]any{"case": id, "to_stage": "revisao"}); !res.IsError || !strings.Contains(text(res), "hanko_case_suggest") {
		t.Errorf("persona para revisão: %s", text(res))
	}
	if res = call(t, cs, "hanko_case_suggest", map[string]any{"case": id, "to_stage": "revisao", "rationale": "pronto", "confidence": 0.7}); res.IsError {
		t.Fatal(text(res))
	}
	var got caseResult
	structured(t, call(t, cs, "hanko_case_get", map[string]any{"case": id}), &got)
	if got.Case.Suggestion == "" || len(got.Case.History) < 4 || len(got.Case.Tasks) != 1 || !strings.Contains(got.Case.Tasks[0], tk.Key) {
		t.Fatalf("get: %+v", got.Case)
	}
	var list caseListOut
	structured(t, call(t, cs, "hanko_case_list", map[string]any{"pipeline": "vendas"}), &list)
	if len(list.Cases) != 1 {
		t.Fatalf("list: %+v", list)
	}
	// os eventos da persona guardam a run
	cv, err := svc.GetCaseView(ctx, co.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range cv.Events {
		if ev.Actor.Type == domain.ActorPersona && (ev.RunID == nil || *ev.RunID != run.ID) {
			t.Errorf("evento de persona sem a run: %+v", ev)
		}
	}
}
