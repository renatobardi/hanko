package service_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// A revisão automática da política da persona continua valendo dentro do pipeline: a etapa sem gate próprio vira
// revisão, o job de revisão é enfileirado e o gate task_review que nasce dela decide o avanço.
func TestStagesAutomaticReviewDecidesTheAdvance(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	dev := e.persona(t, "Dev", func(in *service.HirePersonaInput) {
		in.Slug, in.RequireReview, in.ReviewerAgent = "dev", true, "fake"
	})
	qa := e.persona(t, "QA", func(in *service.HirePersonaInput) { in.Slug = "qa" })
	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetProjectStages(ctx, domain.HumanActor, e.co.ID, pr.Slug, []domain.StageSpec{
		{Key: "dev", Name: "Dev", Status: domain.TaskInProgress, Persona: "dev"}, // sem gate próprio
		{Key: "qa", Name: "QA", Status: domain.TaskInProgress, Persona: "qa"},
	}); err != nil {
		t.Fatal(err)
	}
	tv := e.task(t, service.CreateTaskInput{Title: "Home nova", ProjectID: &pr.ID})
	if tv.PersonaID == nil || *tv.PersonaID != dev.ID {
		t.Fatalf("persona da 1ª etapa: %v", tv.PersonaID)
	}
	e.jobs.reset()
	if _, err := e.svc.CompleteTaskStage(ctx, domain.PersonaActor(dev.ID), e.co.ID, tv.ID, "pronto"); err != nil {
		t.Fatal(err)
	}
	if n := len(e.jobs.reviews); n != 1 {
		t.Fatalf("a revisão automática devia ser enfileirada: %d", n)
	}
	if s := stageOf(t, e, tv); s.State != domain.TaskStageCompleting || taskStatus(t, e, tv).Status != domain.TaskInReview {
		t.Fatalf("esperando a revisão: %+v / %s", s.TaskStage, taskStatus(t, e, tv).Status)
	}
	if pend, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}}); len(pend) != 0 {
		t.Fatalf("o gate só nasce quando a revisão termina: %+v", pend)
	}
	// a revisão não conclui (revisor indisponível): o gate abre sem veredito, nunca deixa a task parada
	if err := e.svc.FailReview(ctx, e.co.ID, e.jobs.reviews[0], "revisor indisponível"); err != nil {
		t.Fatal(err)
	}
	pend, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if len(pend) != 1 || pend[0].Kind != domain.GateTaskReview {
		t.Fatalf("gate da revisão: %+v", pend)
	}
	e.jobs.reset()
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, pend[0].ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	s := stageOf(t, e, tv)
	if s.Stage.Key != "qa" || s.State != domain.TaskStageWorking {
		t.Fatalf("depois da aprovação: %+v", s.TaskStage)
	}
	if w := lastWake(e); w.PersonaID != qa.ID {
		t.Fatalf("wake = %+v, queria o QA", w)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskInProgress || got.PersonaID == nil || *got.PersonaID != qa.ID {
		t.Fatalf("task: %s %v", got.Status, got.PersonaID)
	}
}
