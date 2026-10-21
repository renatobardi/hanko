package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func demoForm() *domain.QuestionForm {
	return &domain.QuestionForm{Fields: []domain.QuestionField{
		{Key: "ambiente", Label: "Onde publicar?", Type: domain.QuestionSingle, Required: true,
			Options: []domain.QuestionOption{{Value: "stg", Label: "Staging"}, {Value: "prd", Label: "Produção"}}},
		{Key: "obs", Label: "Observações", Type: domain.QuestionText},
	}}
}

func TestQuestionGateAnswerFlow(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, nil)

	if _, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{
		Kind: domain.GateQuestion, Title: "Sem formulário", TaskID: &tv.ID}); err == nil {
		t.Fatal("question sem formulário deveria ser recusada")
	}
	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{
		Kind: domain.GateQuestion, Title: "Preciso de duas respostas", TaskID: &tv.ID, RunID: &run.ID, Form: demoForm()})
	if err != nil {
		t.Fatal(err)
	}
	if g.Kind != domain.GateQuestion || g.Status != domain.GatePending || len(g.Payload) == 0 {
		t.Fatalf("gate: %+v", g)
	}
	if st := personaStatus(t, e, p); st != domain.PersonaAwaitingGate {
		t.Fatalf("persona = %s, a pergunta bloqueia como a aprovação", st)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskBlocked {
		t.Fatalf("task = %s", got.Status)
	}
	// decidir não vale para pergunta
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved})
	wantErr(t, err, domain.ErrInvalid)
	// persona não responde
	_, err = e.svc.AnswerGate(ctx, domain.PersonaActor(p.ID), e.co.ID, g.ID, service.AnswerGateInput{Answers: map[string][]string{"ambiente": {"stg"}}})
	wantErr(t, err, domain.ErrNotAllowed)
	// resposta inválida não grava nada
	_, err = e.svc.AnswerGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.AnswerGateInput{Answers: map[string][]string{"ambiente": {"dev"}}})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.AnswerGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.AnswerGateInput{Answers: map[string][]string{"obs": {"só isso"}}})
	wantErr(t, err, domain.ErrInvalid) // obrigatório vazio
	if cur, _ := e.svc.GetGate(ctx, e.co.ID, g.ID); cur.Status != domain.GatePending {
		t.Fatalf("resposta inválida mudou o gate: %s", cur.Status)
	}

	finishOK(t, e, run) // o run da persona termina; ela segue esperando a resposta
	e.jobs.reset()
	done, err := e.svc.AnswerGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.AnswerGateInput{
		Answers: map[string][]string{"ambiente": {"prd"}, "obs": {"depois das 22h"}}, Note: "valeu"})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.GateApproved || done.DecidedBy == nil {
		t.Fatalf("respondido: %+v", done)
	}
	var ans domain.QuestionAnswer
	if err := json.Unmarshal(done.Answer, &ans); err != nil || ans.Values["ambiente"][0] != "prd" || ans.Values["obs"][0] != "depois das 22h" {
		t.Fatalf("resposta gravada: %s %v", done.Answer, err)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskInProgress {
		t.Fatalf("task depois da resposta = %s", got.Status)
	}
	if st := personaStatus(t, e, p); st != domain.PersonaIdle {
		t.Fatalf("persona depois da resposta = %s", st)
	}
	cs, _ := e.svc.ListComments(ctx, e.co.ID, tv.ID)
	last := cs[len(cs)-1]
	if !strings.Contains(last.Body, "Produção (prd)") || !strings.Contains(last.Body, "depois das 22h") || !strings.Contains(last.Body, "valeu") {
		t.Fatalf("a resposta deveria estar na thread: %q", last.Body)
	}
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].Trigger != domain.TriggerGate {
		t.Fatalf("wakes: %+v", e.jobs.wakes)
	}
	if n := countVerb(t, e, "gate.answered"); n != 1 {
		t.Fatalf("gate.answered = %d", n)
	}
	_, err = e.svc.AnswerGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.AnswerGateInput{Answers: map[string][]string{"ambiente": {"stg"}}})
	wantErr(t, err, domain.ErrConflict)
}

func TestQuestionGateDismissAndReopen(t *testing.T) {
	ctx := context.Background()
	e, p, tv, _ := gateFixture(t, nil)
	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{
		Kind: domain.GateQuestion, Title: "Pergunta", TaskID: &tv.ID, Form: demoForm()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ReopenGate(ctx, domain.HumanActor, e.co.ID, g.ID)
	wantErr(t, err, domain.ErrConflict) // só dispensada reabre
	e.jobs.reset()
	d, err := e.svc.DismissGate(ctx, domain.HumanActor, e.co.ID, g.ID, "decida você")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != domain.GateDismissed || d.DecidedAt == nil {
		t.Fatalf("dispensada: %+v", d)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskInProgress {
		t.Fatalf("task depois de dispensar = %s", got.Status)
	}
	if len(e.jobs.wakes) != 1 {
		t.Fatalf("a persona deveria ser acordada: %+v", e.jobs.wakes)
	}
	cs, _ := e.svc.ListComments(ctx, e.co.ID, tv.ID)
	if last := cs[len(cs)-1]; !strings.Contains(last.Body, "Dispensado") || !strings.Contains(last.Body, "decida você") {
		t.Fatalf("comentário: %q", last.Body)
	}
	_, err = e.svc.AnswerGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.AnswerGateInput{Answers: map[string][]string{"ambiente": {"stg"}}})
	wantErr(t, err, domain.ErrConflict)

	r, err := e.svc.ReopenGate(ctx, domain.HumanActor, e.co.ID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != domain.GatePending || r.DecidedAt != nil || r.DecidedBy != nil || r.DecisionNote != "" {
		t.Fatalf("reaberta: %+v", r)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskBlocked {
		t.Fatalf("task depois de reabrir = %s", got.Status)
	}
	if _, err := e.svc.AnswerGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.AnswerGateInput{Answers: map[string][]string{"ambiente": {"stg"}}}); err != nil {
		t.Fatalf("responder depois de reabrir: %v", err)
	}
	for _, v := range []string{"gate.dismissed", "gate.reopened", "gate.answered"} {
		if countVerb(t, e, v) != 1 {
			t.Fatalf("%s não registrado", v)
		}
	}
}

func TestPlanReviewFlow(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, nil)
	pv, err := e.svc.TaskPlanPolicy(ctx, e.co.ID, tv.ID)
	if err != nil || pv.Effective.Required || pv.Phase != domain.PlanPhaseNone {
		t.Fatalf("sem política: %+v %v", pv, err)
	}
	// persona não liga a política; humano liga na persona
	wantErr(t, e.svc.SetPlanPolicy(ctx, domain.PersonaActor(p.ID), e.co.ID, domain.PlanScopePersona, p.ID, ptr(true)), domain.ErrNotAllowed)
	if err := e.svc.SetPlanPolicy(ctx, domain.HumanActor, e.co.ID, domain.PlanScopePersona, p.ID, ptr(true)); err != nil {
		t.Fatal(err)
	}
	if pv, _ = e.svc.TaskPlanPolicy(ctx, e.co.ID, tv.ID); !pv.Effective.Required || pv.Effective.Source != domain.PlanScopePersona || pv.Phase != domain.PlanPhasePlan {
		t.Fatalf("política da persona: %+v", pv)
	}
	// a task desliga (task > persona)
	if err := e.svc.SetPlanPolicy(ctx, domain.HumanActor, e.co.ID, domain.PlanScopeTask, tv.ID, ptr(false)); err != nil {
		t.Fatal(err)
	}
	if pv, _ = e.svc.TaskPlanPolicy(ctx, e.co.ID, tv.ID); pv.Effective.Required || pv.Effective.Source != domain.PlanScopeTask {
		t.Fatalf("task vence a persona: %+v", pv)
	}
	if err := e.svc.SetPlanPolicy(ctx, domain.HumanActor, e.co.ID, domain.PlanScopeTask, tv.ID, nil); err != nil { // volta a herdar
		t.Fatal(err)
	}

	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil || rc.Plan == nil || rc.Plan.Phase != domain.PlanPhasePlan {
		t.Fatalf("o run está na fase plan: %+v %v", rc.Plan, err)
	}
	if ph, err := e.svc.PlanPhaseFor(ctx, e.co.ID, tv.ID, p.ID); err != nil || ph != domain.PlanPhasePlan {
		t.Fatalf("PlanPhaseFor = %q %v", ph, err)
	}

	// plano sem texto nem artefato é recusado; sem task também
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Kind: domain.GatePlan, Title: "Plano", TaskID: &tv.ID})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Kind: domain.GatePlan, Title: "Plano", Body: "x"})
	wantErr(t, err, domain.ErrInvalid)

	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{
		Kind: domain.GatePlan, Title: "Plano da release", Body: "1. migrar\n2. publicar\n3. smoke", TaskID: &tv.ID, RunID: &run.ID})
	if err != nil {
		t.Fatal(err)
	}
	if g.Revision != 1 || g.Kind != domain.GatePlan {
		t.Fatalf("plano: %+v", g)
	}
	if pv, _ = e.svc.TaskPlanPolicy(ctx, e.co.ID, tv.ID); !pv.PendingPlan {
		t.Fatal("deveria haver um plano pendente")
	}
	// reenviar antes de pedir mudanças não vale
	_, err = e.svc.ResubmitGate(ctx, domain.PersonaActor(p.ID), e.co.ID, g.ID, service.ResubmitGateInput{Body: "novo"})
	wantErr(t, err, domain.ErrConflict)

	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateChangesRequested, Note: "falta rollback"}); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskInProgress {
		t.Fatalf("task = %s", got.Status)
	}
	if pv, _ = e.svc.TaskPlanPolicy(ctx, e.co.ID, tv.ID); pv.Phase != domain.PlanPhasePlan {
		t.Fatalf("mudanças pedidas: segue replanejando, fase = %q", pv.Phase)
	}

	// só a persona que pediu reenvia
	other := e.persona(t, "Outra", nil)
	_, err = e.svc.ResubmitGate(ctx, domain.PersonaActor(other.ID), e.co.ID, g.ID, service.ResubmitGateInput{Body: "x"})
	wantErr(t, err, domain.ErrNotAllowed)
	g2, err := e.svc.ResubmitGate(ctx, domain.PersonaActor(p.ID), e.co.ID, g.ID, service.ResubmitGateInput{
		Body: "1. migrar\n2. publicar\n3. smoke\n4. rollback se o smoke falhar", Note: "acrescentei o rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if g2.ID != g.ID || g2.Revision != 2 || g2.Status != domain.GatePending || g2.DecidedAt != nil {
		t.Fatalf("reenviado: %+v", g2)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskBlocked {
		t.Fatalf("task depois do reenvio = %s", got.Status)
	}
	h, err := e.svc.GateRevisions(ctx, e.co.ID, g.ID)
	if err != nil || len(h.Revisions) != 1 || h.Revisions[0].Revision != 1 || h.Revisions[0].DecisionNote != "falta rollback" {
		t.Fatalf("histórico: %+v %v", h, err)
	}
	if !h.Diff.Changed() || h.Diff.Added != 1 || h.Diff.Removed != 0 {
		t.Fatalf("diff: %+v", h.Diff)
	}
	if !strings.Contains(h.Revisions[0].Body, "3. smoke") || strings.Contains(h.Revisions[0].Body, "rollback") {
		t.Fatalf("a revisão 1 guarda o texto antigo: %q", h.Revisions[0].Body)
	}
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE gate_revisions SET body = 'x'`); err == nil {
		t.Fatal("gate_revisions é append-only")
	}

	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "ok"}); err != nil {
		t.Fatal(err)
	}
	if pv, _ = e.svc.TaskPlanPolicy(ctx, e.co.ID, tv.ID); pv.Phase != domain.PlanPhaseExecute {
		t.Fatalf("plano aprovado: fase = %q", pv.Phase)
	}
	finishOK(t, e, run)
	run2, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerGate))
	if err != nil {
		t.Fatal(err)
	}
	rc, err = e.svc.LoadRunContext(ctx, e.co.ID, run2.ID)
	if err != nil || rc.Plan == nil || rc.Plan.Phase != domain.PlanPhaseExecute || !strings.Contains(rc.Plan.ApprovedPlan, "4. rollback") || rc.Plan.Revision != 2 {
		t.Fatalf("a execução recebe o plano aprovado: %+v %v", rc.Plan, err)
	}
	if n := countVerb(t, e, "gate.resubmitted"); n != 1 {
		t.Fatalf("gate.resubmitted = %d", n)
	}
	// reenviar um gate de outro tipo é recusado
	ap, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Kind: domain.GateTaskReview, Title: "rev", TaskID: &tv.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ResubmitGate(ctx, domain.HumanActor, e.co.ID, ap.ID, service.ResubmitGateInput{Body: "x"})
	wantErr(t, err, domain.ErrInvalid)
}

func TestPlanGateFromArtifact(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, nil)
	art, err := e.svc.PutArtifact(ctx, domain.PersonaActor(p.ID), e.co.ID, service.PutArtifactInput{
		Name: "plano.md", Content: []byte("# Plano\n\n- passo um\n- passo dois\n"), TaskID: &tv.ID, RunID: &run.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{
		Kind: domain.GatePlan, Title: "Plano", TaskID: &tv.ID, Artifact: "nao-existe.md"})
	wantErr(t, err, domain.ErrInvalid)
	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{
		Kind: domain.GatePlan, Title: "Plano", TaskID: &tv.ID, Artifact: "plano.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Body, "passo dois") || !strings.Contains(string(g.Payload), art.ID.String()) {
		t.Fatalf("o plano vem do artefato: %q %s", g.Body, g.Payload)
	}
	// artefato de outra task não vale
	other := e.task(t, service.CreateTaskInput{Title: "outra", PersonaID: &p.ID})
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{
		Kind: domain.GatePlan, Title: "Plano", TaskID: &other.ID, Artifact: art.ID.String()})
	wantErr(t, err, domain.ErrInvalid)
}

func TestGateAudience(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.ExternalIdentity = true
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	owner := mkUser(t, e, "dono@x.com", domain.RoleMember)
	join(t, e, owner, domain.MemberOwner)
	adm := mkUser(t, e, "adm@x.com", domain.RoleMember)
	join(t, e, adm, domain.MemberAdmin)
	mem := mkUser(t, e, "mem@x.com", domain.RoleMember)
	join(t, e, mem, domain.MemberMember)
	stranger := mkUser(t, e, "fora@x.com", domain.RoleMember)

	_, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "x", TaskID: &tv.ID,
		Audience: domain.GateAudience{Users: []domain.ID{stranger.ID}}})
	wantErr(t, err, domain.ErrInvalid) // audiência só com membros
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "x", TaskID: &tv.ID,
		Audience: domain.GateAudience{Roles: []domain.MemberRole{"chefe"}}})
	wantErr(t, err, domain.ErrInvalid)

	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "Só o admin", TaskID: &tv.ID,
		Audience: domain.GateAudience{Roles: []domain.MemberRole{domain.MemberAdmin}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Audience.Roles) != 1 {
		t.Fatalf("audiência não gravada: %+v", g.Audience)
	}
	// a Caixa de quem não está na audiência não mostra; a do admin e a do owner mostram
	for who, want := range map[string]int{"mem": 0, "adm": 1, "dono": 1} {
		u := map[string]domain.User{"mem": mem, "adm": adm, "dono": owner}[who]
		in, err := e.svc.Inbox(as(u), e.co.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(in.Gates) != want {
			t.Fatalf("caixa de %s: %d gates, quer %d", who, len(in.Gates), want)
		}
	}
	// a lista segue legível para todo membro (ADR-0021: ler tudo)
	if gs, err := e.svc.ListGates(as(mem), e.co.ID, service.GateFilter{}); err != nil || len(gs) != 1 {
		t.Fatalf("lista: %d %v", len(gs), err)
	}
	_, err = e.svc.DecideGate(as(mem), domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved})
	wantErr(t, err, domain.ErrForbidden)
	if _, err := e.svc.DecideGate(as(adm), domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatalf("admin na audiência: %v", err)
	}

	// usuário nomeado: o member nomeado decide; o admin que não está na lista, não (o owner sempre pode)
	g2, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "Só a Maria", TaskID: &tv.ID,
		Audience: domain.GateAudience{Users: []domain.ID{mem.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.DecideGate(as(adm), domain.HumanActor, e.co.ID, g2.ID, service.DecideGateInput{Decision: domain.GateRejected})
	wantErr(t, err, domain.ErrForbidden)
	if _, err := e.svc.DecideGate(as(mem), domain.HumanActor, e.co.ID, g2.ID, service.DecideGateInput{Decision: domain.GateRejected}); err != nil {
		t.Fatalf("usuário nomeado: %v", err)
	}
	g3, _ := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "Só a Maria 2", TaskID: &tv.ID,
		Audience: domain.GateAudience{Users: []domain.ID{mem.ID}}})
	if _, err := e.svc.DecideGate(as(owner), domain.HumanActor, e.co.ID, g3.ID, service.DecideGateInput{Decision: domain.GateRejected}); err != nil {
		t.Fatalf("o owner sempre pode: %v", err)
	}
}

func TestHireGateCreatesPersona(t *testing.T) {
	ctx := context.Background()
	e, boss, tv, _ := gateFixture(t, nil)
	spec := domain.HireSpec{Name: "Yuki", Title: "Dados", Agent: "fake", Model: "m1", Instructions: "Você analisa dados."}

	// env não viaja na ficha
	bad := spec
	bad.AdapterConfig = json.RawMessage(`{"env":{"K":"segredo"}}`)
	_, err := e.svc.RequestGate(ctx, domain.PersonaActor(boss.ID), e.co.ID, service.RequestGateInput{Kind: domain.GateHire, Title: "Contratar Yuki", TaskID: &tv.ID, Hire: &bad})
	wantErr(t, err, domain.ErrInvalid)

	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(boss.ID), e.co.ID, service.RequestGateInput{Kind: domain.GateHire, Title: "Contratar Yuki", TaskID: &tv.ID, Hire: &spec})
	if err != nil {
		t.Fatal(err)
	}
	if g.Kind != domain.GateHire || !strings.Contains(string(g.Payload), "Yuki") {
		t.Fatalf("gate: %+v", g)
	}
	if ps, _ := e.svc.ListPersonas(ctx, e.co.ID); len(ps) != 1 {
		t.Fatalf("nada é contratado antes da decisão: %d personas", len(ps))
	}
	// rejeitar não cria
	// (o gate segue pendente: aprova)
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "bem-vinda"}); err != nil {
		t.Fatal(err)
	}
	y, err := e.svc.GetPersona(ctx, e.co.ID, "yuki")
	if err != nil {
		t.Fatalf("a persona deveria existir: %v", err)
	}
	if y.Name != "Yuki" || y.Agent != "fake" || y.ReportsTo == nil || *y.ReportsTo != boss.ID || y.Instructions != "Você analisa dados." || y.InstructionRevision != 1 {
		t.Fatalf("persona contratada: %+v", y)
	}
	if n := countVerb(t, e, "persona.hired"); n != 2 { // a do fixture e a do gate
		t.Fatalf("persona.hired = %d", n)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "persona"})
	via := false
	for _, a := range acts {
		if a.Verb == "persona.hired" && strings.Contains(string(a.Data), g.ID.String()) {
			via = true
		}
	}
	if !via {
		t.Fatal("persona.hired deveria apontar o gate (via_gate)")
	}

	// rejeitada: nada é criado
	spec2 := domain.HireSpec{Name: "Zoe", Agent: "fake"}
	g2, err := e.svc.RequestGate(ctx, domain.PersonaActor(boss.ID), e.co.ID, service.RequestGateInput{Kind: domain.GateHire, Title: "Contratar Zoe", Hire: &spec2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g2.ID, service.DecideGateInput{Decision: domain.GateRejected}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetPersona(ctx, e.co.ID, "zoe"); err == nil {
		t.Fatal("rejeitar não contrata")
	}
	// slug já usado é recusado no pedido
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(boss.ID), e.co.ID, service.RequestGateInput{Kind: domain.GateHire, Title: "outra Yuki", Hire: &spec})
	wantErr(t, err, domain.ErrConflict)
}

func TestHirePolicyRequiresGateForHumans(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if err := e.svc.SetRequireHireGate(ctx, domain.HumanActor, e.co.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "Direto", Agent: "fake"})
	wantErr(t, err, domain.ErrConflict)
	spec := domain.HireSpec{Name: "Via Gate", Agent: "fake"}
	g, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Kind: domain.GateHire, Title: "Contratar", Hire: &spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetPersona(ctx, e.co.ID, "via-gate"); err != nil {
		t.Fatalf("contratada pelo gate: %v", err)
	}
	if err := e.svc.SetRequireHireGate(ctx, domain.HumanActor, e.co.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "Direto", Agent: "fake"}); err != nil {
		t.Fatalf("política desligada: %v", err)
	}
	if on, err := e.svc.RequireHireGate(ctx, e.co.ID); err != nil || on {
		t.Fatalf("RequireHireGate = %v %v", on, err)
	}
}

func TestPrimaryPersonaDefaultsTasks(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.ExternalIdentity = true
	ana := e.persona(t, "Ana", nil)
	bia := e.persona(t, "Bia", nil)
	u := mkUser(t, e, "pessoa@x.com", domain.RoleMember)
	join(t, e, u, domain.MemberMember)
	uc := as(u)

	if p, err := e.svc.PrimaryPersona(uc, e.co.ID); err != nil || p != nil {
		t.Fatalf("sem principal: %+v %v", p, err)
	}
	if _, err := e.svc.SetPrimaryPersona(ctx, domain.HumanActor, e.co.ID, &ana.ID); err == nil {
		t.Fatal("sem usuário autenticado não há quem escolher")
	}
	if _, err := e.svc.SetPrimaryPersona(uc, domain.HumanActor, e.co.ID, ptr(domain.NewID())); err == nil {
		t.Fatal("persona que não existe")
	}
	if got, err := e.svc.SetPrimaryPersona(uc, domain.HumanActor, e.co.ID, &ana.ID); err != nil || got == nil || got.ID != ana.ID {
		t.Fatalf("set: %+v %v", got, err)
	}
	tv, err := e.svc.CreateTask(uc, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "sem dono"})
	if err != nil || tv.PersonaID == nil || *tv.PersonaID != ana.ID {
		t.Fatalf("a task nasce com a persona principal: %+v %v", tv, err)
	}
	tv2, err := e.svc.CreateTask(uc, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "com dono", PersonaID: &bia.ID})
	if err != nil || *tv2.PersonaID != bia.ID {
		t.Fatalf("o responsável explícito vence: %+v %v", tv2, err)
	}
	tv3, err := e.svc.CreateTask(uc, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "sem ninguém", Unassigned: true})
	if err != nil || tv3.PersonaID != nil {
		t.Fatalf("Unassigned recusa o padrão: %+v %v", tv3, err)
	}
	// outra pessoa não herda a principal desta
	other := mkUser(t, e, "outra@x.com", domain.RoleMember)
	join(t, e, other, domain.MemberMember)
	if tv4, _ := e.svc.CreateTask(as(other), domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "outra pessoa"}); tv4.PersonaID != nil {
		t.Fatalf("a principal é por pessoa: %+v", tv4)
	}
	// persona desligada deixa de valer
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, ana.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaOff)}); err != nil {
		t.Fatal(err)
	}
	if tv5, _ := e.svc.CreateTask(uc, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "principal desligada"}); tv5.PersonaID != nil {
		t.Fatalf("principal desligada não é padrão: %+v", tv5)
	}
	if _, err := e.svc.SetPrimaryPersona(uc, domain.HumanActor, e.co.ID, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSystemPersonaProtected(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.ExternalIdentity = true
	cur := e.persona(t, "Curadoria", nil)
	owner := mkUser(t, e, "dono@x.com", domain.RoleMember)
	join(t, e, owner, domain.MemberOwner)
	adm := mkUser(t, e, "adm@x.com", domain.RoleMember)
	join(t, e, adm, domain.MemberAdmin)

	_, err := e.svc.SetPersonaSystem(as(adm), domain.HumanActor, e.co.ID, cur.ID, true)
	wantErr(t, err, domain.ErrForbidden) // só owner marca
	got, err := e.svc.SetPersonaSystem(as(owner), domain.HumanActor, e.co.ID, cur.ID, true)
	if err != nil || !got.System {
		t.Fatalf("marcar: %+v %v", got, err)
	}
	_, err = e.svc.UpdatePersona(as(adm), domain.HumanActor, e.co.ID, cur.ID, service.UpdatePersonaInput{Title: ptr("novo")})
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.UpdatePersona(as(owner), domain.HumanActor, e.co.ID, cur.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaOff)})
	wantErr(t, err, domain.ErrConflict) // nem o owner a desliga
	if _, err := e.svc.UpdatePersona(as(owner), domain.HumanActor, e.co.ID, cur.ID, service.UpdatePersonaInput{Title: ptr("novo")}); err != nil {
		t.Fatalf("owner edita: %v", err)
	}
	if _, err := e.svc.UpdatePersona(as(owner), domain.HumanActor, e.co.ID, cur.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaPaused)}); err != nil {
		t.Fatalf("pausar não é desligar: %v", err)
	}
	// desmarcar libera
	if _, err := e.svc.SetPersonaSystem(as(owner), domain.HumanActor, e.co.ID, cur.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePersona(as(adm), domain.HumanActor, e.co.ID, cur.ID, service.UpdatePersonaInput{Title: ptr("livre")}); err != nil {
		t.Fatalf("depois de desmarcar o admin edita: %v", err)
	}
	_ = ctx
}

func openIncidents(t *testing.T, e *env) []domain.BudgetIncident {
	t.Helper()
	in, err := e.svc.ListBudgetIncidents(context.Background(), e.co.ID, true, 50)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestBudgetIncidentRaiseCeiling(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a := e.persona(t, "A", nil)
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return at }
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(500))); err != nil {
		t.Fatal(err)
	}
	e.spend(t, a, at, nil, usd(6.0))
	inc := openIncidents(t, e)
	if len(inc) != 1 || inc[0].Scope != domain.BudgetScopeCompany || inc[0].Month != "2026-03" || !inc[0].Open {
		t.Fatalf("incidentes: %+v", inc)
	}
	in, _ := e.svc.Inbox(ctx, e.co.ID)
	if len(in.BudgetIncidents) != 1 {
		t.Fatalf("o incidente aparece na Caixa: %d", len(in.BudgetIncidents))
	}
	if n := countVerb(t, e, "budget.incident_opened"); n != 1 {
		t.Fatalf("budget.incident_opened = %d", n)
	}
	// uma segunda estourada no mês não abre outro
	tv := e.task(t, service.CreateTaskInput{PersonaID: &a.ID})
	_, err := e.svc.CheckoutRun(ctx, wakeOf(a, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	if len(openIncidents(t, e)) != 1 {
		t.Fatal("um incidente por alvo e mês")
	}

	// validações da resolução
	id := inc[0].ID
	_, err = e.svc.ResolveBudgetIncident(ctx, domain.PersonaActor(a.ID), e.co.ID, id, service.ResolveIncidentInput{Resolution: domain.ResolveKeepPaused})
	wantErr(t, err, domain.ErrNotAllowed)
	_, err = e.svc.ResolveBudgetIncident(ctx, domain.HumanActor, e.co.ID, id, service.ResolveIncidentInput{Resolution: domain.ResolveAuto})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.ResolveBudgetIncident(ctx, domain.HumanActor, e.co.ID, id, service.ResolveIncidentInput{Resolution: domain.ResolveRaiseCeiling})
	wantErr(t, err, domain.ErrInvalid) // sem teto novo
	_, err = e.svc.ResolveBudgetIncident(ctx, domain.HumanActor, e.co.ID, id, service.ResolveIncidentInput{Resolution: domain.ResolveRaiseCeiling, BudgetCents: ptr(int64(550))})
	wantErr(t, err, domain.ErrInvalid) // 5,50 < 6,00 gasto
	if len(openIncidents(t, e)) != 1 {
		t.Fatal("resolução inválida não fecha o incidente")
	}

	res, err := e.svc.ResolveBudgetIncident(ctx, domain.HumanActor, e.co.ID, id, service.ResolveIncidentInput{
		Resolution: domain.ResolveRaiseCeiling, BudgetCents: ptr(int64(5000)), Note: "mês pesado"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Open || res.Resolution != domain.ResolveRaiseCeiling || res.ResolvedBy == nil || res.ResolvedBy.Type != domain.ActorHuman || res.Note != "mês pesado" {
		t.Fatalf("resolvido: %+v", res)
	}
	if st, _ := e.svc.CompanyBudget(ctx, e.co.ID); st.BudgetCents == nil || *st.BudgetCents != 5000 {
		t.Fatalf("o teto subiu: %+v", st)
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(a, tv, domain.TriggerAssign)); err != nil {
		t.Fatalf("depois de subir o teto o run abre: %v", err)
	}
	if len(openIncidents(t, e)) != 0 {
		t.Fatal("nenhum incidente aberto")
	}
	_, err = e.svc.ResolveBudgetIncident(ctx, domain.HumanActor, e.co.ID, id, service.ResolveIncidentInput{Resolution: domain.ResolveKeepPaused})
	wantErr(t, err, domain.ErrConflict)
	if n := countVerb(t, e, "budget.incident_resolved"); n != 1 {
		t.Fatalf("budget.incident_resolved = %d (a resolução manual não duplica o auto)", n)
	}
}

func TestBudgetIncidentKeepAndAuto(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a := e.persona(t, "A", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(300)) })
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return at }
	e.spend(t, a, at, nil, usd(4.0))
	inc := openIncidents(t, e)
	if len(inc) != 1 || inc[0].Scope != domain.BudgetScopePersona || inc[0].TargetID != a.ID {
		t.Fatalf("incidente da persona: %+v", inc)
	}
	// manter pausado: sai da Caixa, a persona continua pausada
	tv := e.task(t, service.CreateTaskInput{PersonaID: &a.ID})
	_, err := e.svc.CheckoutRun(ctx, wakeOf(a, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	if _, err := e.svc.ResolveBudgetIncident(ctx, domain.HumanActor, e.co.ID, inc[0].ID, service.ResolveIncidentInput{Resolution: domain.ResolveKeepPaused, Note: "segura"}); err != nil {
		t.Fatal(err)
	}
	if len(openIncidents(t, e)) != 0 {
		t.Fatal("manter pausado fecha o incidente")
	}
	if g, _ := e.svc.GetPersona(ctx, e.co.ID, a.ID.String()); g.Status != domain.PersonaPaused {
		t.Fatalf("segue pausada: %s", g.Status)
	}
	// o teto sobe por outro caminho (edição da persona): nada aberto, nada a resolver
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdatePersonaInput{BudgetMonthlyCents: ptr(int64(5000))}); err != nil {
		t.Fatal(err)
	}

	// novo estouro no mesmo mês depois de baixar o teto reabre o incidente
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaActive), BudgetMonthlyCents: ptr(int64(300))}); err != nil {
		t.Fatal(err)
	}
	tv2 := e.task(t, service.CreateTaskInput{PersonaID: &a.ID})
	_, err = e.svc.CheckoutRun(ctx, wakeOf(a, tv2, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	if len(openIncidents(t, e)) != 1 {
		t.Fatal("o estouro de novo reabre o incidente")
	}
	// o teto sobe por outro caminho: resolve sozinho (auto)
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdatePersonaInput{BudgetMonthlyCents: ptr(int64(9000))}); err != nil {
		t.Fatal(err)
	}
	all, _ := e.svc.ListBudgetIncidents(ctx, e.co.ID, false, 50)
	if len(all) != 1 || all[0].Open || all[0].Resolution != domain.ResolveAuto {
		t.Fatalf("auto: %+v", all)
	}
}

func TestBudgetIncidentReleaseOnce(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a := e.persona(t, "A", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(300)) })
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return at }
	e.spend(t, a, at, nil, usd(4.0))
	tv := e.task(t, service.CreateTaskInput{PersonaID: &a.ID})
	_, err := e.svc.CheckoutRun(ctx, wakeOf(a, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	inc := openIncidents(t, e)
	if len(inc) != 1 {
		t.Fatalf("incidentes: %+v", inc)
	}
	if g, _ := e.svc.GetPersona(ctx, e.co.ID, a.ID.String()); g.Status != domain.PersonaPaused {
		t.Fatalf("pausada: %s", g.Status)
	}

	e.jobs.reset()
	res, err := e.svc.ResolveBudgetIncident(ctx, domain.HumanActor, e.co.ID, inc[0].ID, service.ResolveIncidentInput{Resolution: domain.ResolveReleaseOnce})
	if err != nil {
		t.Fatal(err)
	}
	if res.Open || res.ReleaseRuns != 1 || res.Resolution != domain.ResolveReleaseOnce {
		t.Fatalf("liberado: %+v", res)
	}
	if g, _ := e.svc.GetPersona(ctx, e.co.ID, a.ID.String()); g.Status == domain.PersonaPaused {
		t.Fatalf("a persona pausada por orçamento volta: %s", g.Status)
	}
	if st, _ := e.svc.PersonaBudget(ctx, e.co.ID, a.ID); st.State != service.BudgetExceeded {
		t.Fatalf("o teto não mudou: %+v", st)
	}
	// um run passa...
	run, err := e.svc.CheckoutRun(ctx, wakeOf(a, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatalf("o run liberado abre: %v", err)
	}
	// ...e o seguinte volta a ser barrado
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	finishOK(t, e, run)
	tv2 := e.task(t, service.CreateTaskInput{PersonaID: &a.ID})
	_, err = e.svc.CheckoutRun(ctx, wakeOf(a, tv2, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
}

func TestBudgetIncidentMonthRolls(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a := e.persona(t, "A", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(300)) })
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return march }
	e.spend(t, a, march, nil, usd(4.0))
	if len(openIncidents(t, e)) != 1 {
		t.Fatal("incidente aberto")
	}
	e.svc.Now = func() time.Time { return time.Date(2026, 4, 1, 3, 0, 0, 0, time.UTC) }
	if _, err := e.svc.ResumeBudgetPaused(ctx); err != nil { // o tick do heartbeat
		t.Fatal(err)
	}
	if len(openIncidents(t, e)) != 0 {
		t.Fatal("o mês virou: o incidente se resolve sozinho")
	}
}

func TestMigration00110Reversible(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// tabelas novas existem e a constraint de kind aceita question e plan
	for _, q := range []string{"gate_revisions", "plan_policies", "company_policies", "member_primary_persona", "budget_incidents"} {
		var reg *string
		if err := e.svc.Pool.QueryRow(ctx, `SELECT to_regclass('public.`+q+`')::text`).Scan(&reg); err != nil || reg == nil {
			t.Fatalf("falta a tabela %s (%v)", q, err)
		}
	}
	p := e.persona(t, "P", nil)
	for _, k := range []string{"question", "plan", "approval", "task_review", "hire", "memory", "tool", "instruction"} {
		if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO gates (id, company_id, kind, status, title, persona_id, requested_by_type)
			VALUES (gen_random_uuid(), $1, $2, 'pending', 't', $3, 'persona')`, e.co.ID, k, p.ID); err != nil {
			t.Fatalf("kind %s: %v", k, err)
		}
	}
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO gates (id, company_id, kind, status, title, persona_id, requested_by_type, decided_at)
		VALUES (gen_random_uuid(), $1, 'question', 'dismissed', 't', $2, 'persona', now())`, e.co.ID, p.ID); err != nil {
		t.Fatalf("dismissed: %v", err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO gates (id, company_id, kind, status, title, requested_by_type) VALUES (gen_random_uuid(), $1, 'bogus', 'pending', 't', 'persona')`, e.co.ID); err == nil {
		t.Fatal("kind desconhecido deveria ser recusado")
	}
}
