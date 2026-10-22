//go:build integration

package ui_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Gates estendidos na UI contra o service e o banco reais (ADR-0041).

func m15Persona(t *testing.T, u *realUI, name string, mod func(*service.HirePersonaInput)) domain.Persona {
	t.Helper()
	in := service.HirePersonaInput{Name: name, Agent: "fake"}
	if mod != nil {
		mod(&in)
	}
	p, err := u.svc.HirePersona(context.Background(), domain.HumanActor, u.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func m15Form() *domain.QuestionForm {
	return &domain.QuestionForm{Fields: []domain.QuestionField{
		{Key: "env", Label: "Onde publicar?", Type: domain.QuestionSingle, Required: true,
			Options: []domain.QuestionOption{{Value: "stg", Label: "Staging"}, {Value: "prd", Label: "Produção"}}},
		{Key: "extras", Label: "Extras", Type: domain.QuestionMulti, Options: []domain.QuestionOption{{Value: "docs"}, {Value: "tests"}}},
		{Key: "obs", Label: "Observações", Type: domain.QuestionText},
	}}
}

func TestRealQuestionGatePage(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	p := m15Persona(t, u, "Ada", nil)
	tk, err := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "Publicar", PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	g, err := u.svc.RequestGate(ctx, domain.PersonaActor(p.ID), u.co.ID, service.RequestGateInput{Kind: domain.GateQuestion, Title: "Onde publicar?", TaskID: &tk.ID, Form: m15Form()})
	if err != nil {
		t.Fatal(err)
	}
	page := "/c/acme/gates/" + g.ID.String()
	// o formulário: um campo por pergunta, rádio, caixas e texto, com Responder e Dispensar
	mustContain(t, u.get(page), 200, "Pergunta", "Onde publicar?", `type="radio"`, `name="a_env"`, "Staging", `type="checkbox"`, `name="a_extras"`,
		`name="a_obs"`, "Responder", "Dispensar", page+"/responder", page+"/dispensar")
	// a lista de gates tem as abas novas
	mustContain(t, u.get("/c/acme/gates?tipo=perguntas"), 200, "Onde publicar?", "Perguntas")
	// a Caixa oferece Responder (sem decidir inline)
	mustContain(t, u.get("/c/acme/caixa"), 200, "Onde publicar?", "Responder")

	// resposta inválida (obrigatório vazio): 422 e o gate segue pendente
	rec := u.form(page+"/responder", url.Values{"a_obs": {"só isso"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("obrigatório vazio: %d %.300s", rec.Code, rec.Body.String())
	}
	rec = u.form(page+"/responder", url.Values{"a_env": {"prd"}, "a_extras": {"docs", "tests"}, "a_obs": {"depois das 22h"}, "note": {"valeu"}, "next": {"caixa"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/caixa?ok=respondido") {
		t.Fatalf("responder: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, u.get(page), 200, "Respostas", "Produção", "docs, tests", "depois das 22h")
	cs, _ := u.svc.ListComments(ctx, u.co.ID, tk.ID)
	if last := cs[len(cs)-1]; !strings.Contains(last.Body, "Produção (prd)") {
		t.Fatalf("a resposta vai para a thread: %q", last.Body)
	}
	// decidir pelo caminho antigo não vale para pergunta
	if rec := u.form(page+"/decidir", url.Values{"decision": {"approved"}}); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusConflict {
		t.Fatalf("decidir pergunta: %d", rec.Code)
	}
}

func TestRealQuestionDismissReopenUI(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	p := m15Persona(t, u, "Ada", nil)
	tk, _ := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	g, err := u.svc.RequestGate(ctx, domain.PersonaActor(p.ID), u.co.ID, service.RequestGateInput{Kind: domain.GateQuestion, Title: "Pergunta?", TaskID: &tk.ID, Form: m15Form()})
	if err != nil {
		t.Fatal(err)
	}
	page := "/c/acme/gates/" + g.ID.String()
	rec := u.form(page+"/dispensar", url.Values{"note": {"depois"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=dispensado") {
		t.Fatalf("dispensar: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, u.get(page), 200, "Pergunta dispensada", "Reabrir", page+"/reabrir")
	rec = u.form(page+"/reabrir", nil)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=reaberto") {
		t.Fatalf("reabrir: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	mustContain(t, u.get(page+"?ok=reaberto"), 200, "Pergunta reaberta", "Responder")
	// o botão Dispensar do formulário de resposta também dispensa
	rec = u.form(page+"/responder", url.Values{"dismiss": {"1"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=dispensado") {
		t.Fatalf("dispensar pelo formulário: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRealPlanHireAndHistoryPages(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	p := m15Persona(t, u, "Ada", nil)
	tk, _ := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})

	// plano reenviado: a página mostra o histórico com o diff
	g, err := u.svc.RequestGate(ctx, domain.PersonaActor(p.ID), u.co.ID, service.RequestGateInput{Kind: domain.GatePlan, Title: "Plano", Body: "1. a\n2. b", TaskID: &tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.DecideGate(ctx, domain.HumanActor, u.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateChangesRequested, Note: "falta o rollback"}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.ResubmitGate(ctx, domain.PersonaActor(p.ID), u.co.ID, g.ID, service.ResubmitGateInput{Body: "1. a\n2. b\n3. rollback"}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, u.get("/c/acme/gates/"+g.ID.String()), 200, "Plano", "Reenvios (revisão 2)", "+1", "3. rollback", "falta o rollback")

	// contratação: a ficha aparece e aprovar cria a persona
	h, err := u.svc.RequestGate(ctx, domain.PersonaActor(p.ID), u.co.ID, service.RequestGateInput{Kind: domain.GateHire, Title: "Contratar Yuki",
		Hire: &domain.HireSpec{Name: "Yuki", Title: "Dados", Agent: "fake", Instructions: "Analise dados."}})
	if err != nil {
		t.Fatal(err)
	}
	hp := "/c/acme/gates/" + h.ID.String()
	mustContain(t, u.get(hp), 200, "Ficha da contratação", "Yuki", "Analise dados.", "Aprovar cria a persona")
	if rec := u.form(hp+"/decidir", url.Values{"decision": {"approved"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("aprovar: %d", rec.Code)
	}
	mustContain(t, u.get("/c/acme/personas/yuki"), 200, "Yuki", "Dados")
}

func TestRealBudgetIncidentInbox(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	p := m15Persona(t, u, "Ada", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(300)) })
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	u.svc.Now = func() time.Time { return at }
	tk, _ := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "gasto", PersonaID: &p.ID})
	run, err := u.svc.CheckoutRun(ctx, service.Wake{CompanyID: u.co.ID, PersonaID: p.ID, TaskID: tk.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.MarkRunStarted(ctx, u.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	cost := 4.0
	if _, err := u.svc.FinishRun(ctx, u.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{CostUSD: &cost}}); err != nil {
		t.Fatal(err)
	}
	inc, err := u.svc.ListBudgetIncidents(ctx, u.co.ID, true, 10)
	if err != nil || len(inc) != 1 {
		t.Fatalf("incidentes: %+v %v", inc, err)
	}
	mustContain(t, u.get("/c/acme/caixa"), 200, "Orçamento estourado", "Ada", "US$ 4.00", "US$ 3.00", "Subir o teto", "Manter pausado", "/incidentes/"+inc[0].ID.String()+"/resolver")
	mustContain(t, u.get("/c/acme/caixa?aba=orcamento"), 200, "Orçamento estourado")

	// subir o teto sem valor: 422; com valor, resolve
	rec := u.form("/c/acme/incidentes/"+inc[0].ID.String()+"/resolver", url.Values{"resolution": {"ceiling_raised"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("sem valor: %d", rec.Code)
	}
	rec = u.form("/c/acme/incidentes/"+inc[0].ID.String()+"/resolver", url.Values{"resolution": {"ceiling_raised"}, "budget": {"50,5"}, "note": {"mês pesado"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=incidente") {
		t.Fatalf("resolver: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if st, _ := u.svc.PersonaBudget(ctx, u.co.ID, p.ID); st.BudgetCents == nil || *st.BudgetCents != 5050 {
		t.Fatalf("teto: %+v", st)
	}
	if body := u.get("/c/acme/caixa").Body.String(); strings.Contains(body, "Orçamento estourado") {
		t.Fatal("o incidente resolvido sai da Caixa")
	}
}

func TestRealPersonaPlusControls(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	p := m15Persona(t, u, "Ada", nil)
	tk, _ := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})

	mustContain(t, u.get("/c/acme/personas/ada"), 200, "Padrões e proteção", "Persona principal", "Persona de sistema", "Plano antes de executar", "/personas/ada/plano")
	// sistema: marcar e desmarcar
	if rec := u.form("/c/acme/personas/ada/sistema", url.Values{"system": {"on"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("sistema: %d", rec.Code)
	}
	mustContain(t, u.get("/c/acme/personas/ada?ok=sistema"), 200, "Persona de sistema atualizada", "não se desliga")
	// persona principal em modo trusted: não há pessoa autenticada
	if rec := u.form("/c/acme/personas/ada/principal", url.Values{}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("principal em trusted: %d", rec.Code)
	}

	// política de plano pela persona e pela task
	if rec := u.form("/c/acme/personas/ada/plano", url.Values{"mode": {"yes"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("plano da persona: %d", rec.Code)
	}
	mustContain(t, u.get("/c/acme/personas/ada?ok=plano"), 200, "Exige plano aprovado")
	mustContain(t, u.get("/c/acme/tasks/"+tk.Key), 200, "Plano antes de executar", "origem: persona", "politica-plano")
	if rec := u.form("/c/acme/tasks/"+tk.Key+"/politica-plano", url.Values{"mode": {"no"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("plano da task: %d", rec.Code)
	}
	mustContain(t, u.get("/c/acme/tasks/"+tk.Key), 200, "A execução começa direto, sem plano.")

	// política de contratação por gate na tela de configurações
	mustContain(t, u.get("/c/acme/configuracoes"), 200, "Contratação por gate", "Exigir gate para contratar")
	if rec := u.form("/c/acme/politicas", url.Values{"require_hire_gate": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("política: %d", rec.Code)
	}
	if on, _ := u.svc.RequireHireGate(ctx, u.co.ID); !on {
		t.Fatal("política deveria estar ligada")
	}
}

func ptr[T any](v T) *T { return &v }
