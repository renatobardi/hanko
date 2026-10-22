package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// stagesFixture: um projeto com o pipeline triagem → dev → review (aprovação) → qa (revisão da task), uma persona por
// etapa. Devolve o ambiente, as personas por chave e o projeto.
func stagesFixture(t *testing.T) (*env, map[string]domain.Persona, domain.Project) {
	t.Helper()
	ctx := context.Background()
	e := newEnv(t)
	ps := map[string]domain.Persona{}
	for _, k := range []string{"tri", "dev", "rev", "qa"} {
		ps[k] = e.persona(t, "P "+k, func(in *service.HirePersonaInput) { in.Slug = k })
	}
	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetProjectStages(ctx, domain.HumanActor, e.co.ID, pr.Slug, []domain.StageSpec{
		{Key: "triagem", Name: "Triagem", Status: domain.TaskTodo, Persona: "tri"},
		{Key: "dev", Name: "Desenvolvimento", Status: domain.TaskInProgress, Persona: "dev", Instructions: "Faça pequeno."},
		{Key: "review", Name: "Review", Status: domain.TaskInProgress, Persona: "rev", ExitGate: domain.StageGateApproval},
		{Key: "qa", Name: "QA", Status: domain.TaskInProgress, Persona: "qa", ExitGate: domain.StageGateReview},
	}); err != nil {
		t.Fatal(err)
	}
	return e, ps, pr
}

func lastWake(e *env) service.Wake {
	e.jobs.mu.Lock()
	defer e.jobs.mu.Unlock()
	if len(e.jobs.wakes) == 0 {
		return service.Wake{}
	}
	return e.jobs.wakes[len(e.jobs.wakes)-1]
}

func stageOf(t *testing.T, e *env, tv service.TaskView) service.TaskStageView {
	t.Helper()
	v, err := e.svc.TaskStage(context.Background(), e.co.ID, tv.Key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStagesValidation(t *testing.T) {
	ctx := context.Background()
	e, _, pr := stagesFixture(t)
	bad := [][]domain.StageSpec{
		nil,
		{{Key: "A b", Name: "x", Status: domain.TaskTodo}},
		{{Key: "a", Name: "x", Status: domain.TaskDone}},
		{{Key: "a", Name: "x", Status: domain.TaskInReview, Persona: "dev"}}, // in_review só em etapa humana
		{{Key: "a", Name: "x", Status: domain.TaskTodo}, {Key: "a", Name: "y", Status: domain.TaskTodo}},
		{{Key: "a", Name: "x", Status: domain.TaskTodo, ExitGate: "nope"}},
		{{Key: "a", Name: "x", Status: domain.TaskTodo, Persona: "nao-existe"}},
	}
	for i, specs := range bad {
		if _, err := e.svc.SetProjectStages(ctx, domain.HumanActor, e.co.ID, pr.Slug, specs); err == nil {
			t.Errorf("caso %d: esperava erro", i)
		}
	}
	v, err := e.svc.ProjectStages(ctx, e.co.ID, pr.Slug)
	if err != nil || len(v.Stages) != 4 {
		t.Fatalf("o pipeline original devia estar intacto: %v %+v", err, v)
	}
	if v.Stages[1].PersonaSlug != "dev" || v.Stages[1].Position != 2 {
		t.Fatalf("etapa dev: %+v", v.Stages[1])
	}
}

func TestStagesRemoveStageWithTasksConflicts(t *testing.T) {
	ctx := context.Background()
	e, _, pr := stagesFixture(t)
	tv := e.task(t, service.CreateTaskInput{Title: "Home nova", ProjectID: &pr.ID})
	if s := stageOf(t, e, tv); s.Stage.Key != "triagem" {
		t.Fatalf("a task devia nascer na triagem: %+v", s)
	}
	// tirar a etapa onde há task é conflito
	_, err := e.svc.SetProjectStages(ctx, domain.HumanActor, e.co.ID, pr.Slug, []domain.StageSpec{
		{Key: "dev", Name: "Dev", Status: domain.TaskInProgress, Persona: "dev"}})
	wantErr(t, err, domain.ErrConflict)
	wantErr(t, e.svc.ClearProjectStages(ctx, domain.HumanActor, e.co.ID, pr.Slug), domain.ErrConflict)
	if _, err := e.svc.AdvanceTaskStage(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AdvanceStageInput{To: "dev"}); err != nil {
		t.Fatal(err)
	}
	// sair do pipeline libera as etapas
	if err := e.svc.LeaveTaskStage(ctx, domain.HumanActor, e.co.ID, tv.ID, "teste"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ClearProjectStages(ctx, domain.HumanActor, e.co.ID, pr.Slug); err != nil {
		t.Fatal(err)
	}
}

// A demo do marco: a task atravessa triagem → dev → review → QA sozinha, parando só nos gates.
func TestStagesPipelineCrossesAllStages(t *testing.T) {
	ctx := context.Background()
	e, ps, pr := stagesFixture(t)

	// 1. nasce na triagem, com a persona e o status da etapa; a persona é acordada
	e.jobs.reset()
	tv := e.task(t, service.CreateTaskInput{Title: "Home nova", ProjectID: &pr.ID})
	if tv.PersonaID == nil || *tv.PersonaID != ps["tri"].ID || tv.Status != domain.TaskTodo {
		t.Fatalf("task nova: persona=%v status=%s", tv.PersonaID, tv.Status)
	}
	if w := lastWake(e); w.PersonaID != ps["tri"].ID || w.TaskID != tv.ID {
		t.Fatalf("wake = %+v, queria a triagem", w)
	}
	if tv.Stage == nil || tv.Stage.Key != "triagem" {
		t.Fatalf("TaskView.Stage = %+v", tv.Stage)
	}

	// 2. a triagem dá a task como concluída: concluir a etapa, não fechar a task; a persona do dev acorda
	e.jobs.reset()
	done := domain.TaskDone
	got, err := e.svc.UpdateTask(ctx, domain.PersonaActor(ps["tri"].ID), e.co.ID, tv.ID, service.UpdateTaskInput{Status: &done})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskInProgress || got.PersonaID == nil || *got.PersonaID != ps["dev"].ID {
		t.Fatalf("depois da triagem: status=%s persona=%v", got.Status, got.PersonaID)
	}
	if w := lastWake(e); w.PersonaID != ps["dev"].ID {
		t.Fatalf("wake = %+v, queria o dev", w)
	}

	// 3. o dev conclui pela rota explícita; o review começa
	e.jobs.reset()
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["dev"].ID), e.co.ID, tv.ID, "implementado"); err != nil {
		t.Fatal(err)
	}
	s := stageOf(t, e, tv)
	if s.Stage.Key != "review" || s.State != domain.TaskStageWorking {
		t.Fatalf("depois do dev: %+v", s.TaskStage)
	}
	if w := lastWake(e); w.PersonaID != ps["rev"].ID {
		t.Fatalf("wake = %+v, queria o review", w)
	}

	// 4. o review conclui: a etapa pede aprovação, a task para em in_review e ninguém é acordado
	e.jobs.reset()
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["rev"].ID), e.co.ID, tv.ID, "tudo certo no review"); err != nil {
		t.Fatal(err)
	}
	s = stageOf(t, e, tv)
	if s.State != domain.TaskStageCompleting || taskStatus(t, e, tv).Status != domain.TaskInReview {
		t.Fatalf("esperava aguardar o gate: %+v / %s", s.TaskStage, taskStatus(t, e, tv).Status)
	}
	if e.jobs.wakeCount() != 0 {
		t.Fatalf("o pipeline parado no gate não acorda ninguém, wakes=%d", e.jobs.wakeCount())
	}
	// concluir de novo enquanto o gate está pendente não duplica
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["rev"].ID), e.co.ID, tv.ID, ""); err != nil {
		t.Fatal(err)
	}
	pending, err := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if err != nil || len(pending) != 1 || pending[0].Kind != domain.GateApproval {
		t.Fatalf("gates pendentes: %v %+v", err, pending)
	}

	// 5. o humano pede mudanças: a etapa volta à persona do review, com a nota, e vira aprendizado
	e.jobs.reset()
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, pending[0].ID, service.DecideGateInput{
		Decision: domain.GateChangesRequested, Note: "falta cobrir o caso de lista vazia"}); err != nil {
		t.Fatal(err)
	}
	s = stageOf(t, e, tv)
	if s.Stage.Key != "review" || s.State != domain.TaskStageWorking || taskStatus(t, e, tv).Status != domain.TaskInProgress {
		t.Fatalf("depois de mudanças pedidas: %+v / %s", s.TaskStage, taskStatus(t, e, tv).Status)
	}
	if w := lastWake(e); w.PersonaID != ps["rev"].ID || w.Trigger != domain.TriggerGate {
		t.Fatalf("wake = %+v, queria o review com gatilho gate", w)
	}
	ls, err := e.svc.StageLearnings(ctx, e.co.ID, pr.Slug, "review", 10)
	if err != nil || len(ls) != 1 || ls[0].Decision != string(domain.GateChangesRequested) || !strings.Contains(ls[0].Note, "lista vazia") {
		t.Fatalf("aprendizados: %v %+v", err, ls)
	}
	// o fato entra na memória
	found := false
	for _, id := range e.jobs.outbox {
		it, err := e.svc.GetMemoryItem(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if it.Kind != "pipeline.learning" {
			continue
		}
		var f memory.Fact
		if err := json.Unmarshal(it.Payload, &f); err != nil {
			t.Fatal(err)
		}
		found = strings.HasPrefix(f.Path, "hanko/pipelines/"+pr.Slug+"/review/") && strings.Contains(f.Body, "lista vazia")
	}
	if !found {
		t.Fatalf("fato pipeline.learning não está no outbox: %v", e.jobs.outbox)
	}

	// 6. o review conclui de novo e o humano aprova: a task vai para o QA
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["rev"].ID), e.co.ID, tv.ID, "coberto"); err != nil {
		t.Fatal(err)
	}
	pending, _ = e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if len(pending) != 1 {
		t.Fatalf("gates pendentes = %d", len(pending))
	}
	e.jobs.reset()
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, pending[0].ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "ok"}); err != nil {
		t.Fatal(err)
	}
	s = stageOf(t, e, tv)
	if s.Stage.Key != "qa" || s.State != domain.TaskStageWorking {
		t.Fatalf("depois da aprovação: %+v", s.TaskStage)
	}
	if w := lastWake(e); w.PersonaID != ps["qa"].ID {
		t.Fatalf("wake = %+v, queria o QA", w)
	}

	// 7. o QA conclui: a última etapa pede a revisão da task (task_review); aprovar encerra a task e o pipeline
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["qa"].ID), e.co.ID, tv.ID, "passou"); err != nil {
		t.Fatal(err)
	}
	pending, _ = e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if len(pending) != 1 || pending[0].Kind != domain.GateTaskReview {
		t.Fatalf("o QA devia abrir task_review: %+v", pending)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, pending[0].ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	final := taskStatus(t, e, tv)
	if final.Status != domain.TaskDone || final.Stage != nil {
		t.Fatalf("task final: status=%s stage=%+v", final.Status, final.Stage)
	}
	s = stageOf(t, e, tv)
	if s.State != domain.TaskStageFinished {
		t.Fatalf("pipeline = %s", s.State)
	}
	var kinds []string
	for _, ev := range s.Events {
		kinds = append(kinds, string(ev.Kind))
	}
	want := "enrolled completed advanced completed advanced completed gate_requested gate_decided returned completed gate_requested gate_decided advanced completed gate_requested gate_decided finished"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("histórico:\n got %s\nwant %s", got, want)
	}
}

func TestStagesRejectedReturnsAndPersonaCannotAdvance(t *testing.T) {
	ctx := context.Background()
	e, ps, pr := stagesFixture(t)
	tv := e.task(t, service.CreateTaskInput{Title: "Home nova", ProjectID: &pr.ID})
	// uma persona não pula etapa à mão
	_, err := e.svc.AdvanceTaskStage(ctx, domain.PersonaActor(ps["tri"].ID), e.co.ID, tv.ID, service.AdvanceStageInput{})
	wantErr(t, err, domain.ErrNotAllowed)
	// concluir uma task fora de pipeline é recusado
	other := e.task(t, service.CreateTaskInput{Title: "solta", PersonaID: ptr(ps["dev"].ID)})
	_, err = e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["dev"].ID), e.co.ID, other.ID, "")
	wantErr(t, err, domain.ErrInvalid)

	// o humano leva a task ao review e rejeita o gate: volta à mesma etapa
	if _, err := e.svc.AdvanceTaskStage(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AdvanceStageInput{To: "review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["rev"].ID), e.co.ID, tv.ID, "pronto"); err != nil {
		t.Fatal(err)
	}
	pending, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if len(pending) != 1 {
		t.Fatalf("gates = %d", len(pending))
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, pending[0].ID, service.DecideGateInput{Decision: domain.GateRejected, Note: "não é isso"}); err != nil {
		t.Fatal(err)
	}
	if s := stageOf(t, e, tv); s.Stage.Key != "review" || s.State != domain.TaskStageWorking {
		t.Fatalf("rejeitado devia voltar ao review: %+v", s.TaskStage)
	}
	// avançar à mão com gate pendente expira o gate
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["rev"].ID), e.co.ID, tv.ID, "de novo"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AdvanceTaskStage(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AdvanceStageInput{Note: "passo por cima"}); err != nil {
		t.Fatal(err)
	}
	if s := stageOf(t, e, tv); s.Stage.Key != "qa" {
		t.Fatalf("avanço manual: %+v", s.TaskStage)
	}
	if pend, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}}); len(pend) != 0 {
		t.Fatalf("o gate do pipeline devia ter expirado: %+v", pend)
	}
}

func TestStagesHumanClosingLeavesPipelineAndBriefing(t *testing.T) {
	ctx := context.Background()
	e, ps, pr := stagesFixture(t)
	tv := e.task(t, service.CreateTaskInput{Title: "Home nova", ProjectID: &pr.ID})
	// uma persona escolhida a dedo não entra no pipeline sozinha
	solo := e.task(t, service.CreateTaskInput{Title: "avulsa", ProjectID: &pr.ID, PersonaID: ptr(ps["dev"].ID)})
	if solo.Stage != nil {
		t.Fatalf("task com persona explícita não devia entrar: %+v", solo.Stage)
	}
	// ... mas pode ser posta no pipeline
	v, err := e.svc.StartTaskStage(ctx, domain.HumanActor, e.co.ID, solo.ID, "dev")
	if err != nil || v.Stage.Key != "dev" {
		t.Fatalf("start: %v %+v", err, v.TaskStage)
	}
	_, err = e.svc.StartTaskStage(ctx, domain.HumanActor, e.co.ID, solo.ID, "")
	wantErr(t, err, domain.ErrConflict)

	// o humano encerra a task fora do pipeline: ela sai dele
	done := domain.TaskDone
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	if s := stageOf(t, e, tv); s.State != domain.TaskStageFinished {
		t.Fatalf("estado = %s", s.State)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskDone {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestStagesBriefingContext(t *testing.T) {
	ctx := context.Background()
	e, ps, pr := stagesFixture(t)
	tv := e.task(t, service.CreateTaskInput{Title: "Home nova", ProjectID: &pr.ID})
	if _, err := e.svc.AdvanceTaskStage(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AdvanceStageInput{To: "review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(ps["rev"].ID), e.co.ID, tv.ID, "pronto"); err != nil {
		t.Fatal(err)
	}
	pending, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, pending[0].ID, service.DecideGateInput{
		Decision: domain.GateChangesRequested, Note: "sempre cite o arquivo alterado"}); err != nil {
		t.Fatal(err)
	}
	run, err := e.svc.CheckoutRun(ctx, wakeOf(ps["rev"], tv, domain.TriggerGate))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Stage == nil || rc.Stage.Key != "review" || rc.Stage.Position != 3 || rc.Stage.Total != 4 || rc.Stage.NextName != "QA" ||
		!strings.Contains(rc.Stage.Learnings, "sempre cite o arquivo alterado") || rc.Stage.ExitGate != domain.StageGateApproval {
		t.Fatalf("contexto da etapa: %+v", rc.Stage)
	}
}
