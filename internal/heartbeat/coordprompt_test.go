package heartbeat

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestCoordinationSection(t *testing.T) {
	rc := service.RunContext{Persona: domain.Persona{Name: "Ana"}, Company: domain.Company{Name: "Acme"},
		Task: service.TaskView{Task: domain.Task{Key: "HNK-5", Title: "Implementar", Status: domain.TaskInProgress, Priority: domain.PriorityMedium}}}
	if got := coordinationSection(rc); got != "" {
		t.Fatalf("sem relações e sem política de task/projeto não há seção: %q", got)
	}
	_, prompt := BuildPrompt(rc, "", "")
	if strings.Contains(prompt, "## Coordenação") {
		t.Fatal("prompt sem seção vazia")
	}

	rc.Relations = &service.TaskRelations{
		Ancestors: []domain.TaskRef{{Key: "HNK-1", Title: "Épico"}, {Key: "HNK-2", Title: "Entrega"}},
		DependsOn: []service.DependencyView{
			{TaskRef: domain.TaskRef{Key: "HNK-3", Title: "Modelar", Status: domain.TaskDone}, Closed: true},
			{TaskRef: domain.TaskRef{Key: "HNK-4", Title: "Cancelada", Status: domain.TaskCancelled}, Closed: true},
			{TaskRef: domain.TaskRef{Key: "HNK-6", Title: "Aberta", Status: domain.TaskTodo}},
		},
		Dependents: []service.DependencyView{{TaskRef: domain.TaskRef{Key: "HNK-7", Title: "Validar", Status: domain.TaskTodo}}},
		Children: []service.DependencyView{
			{TaskRef: domain.TaskRef{Key: "HNK-8", Title: "Filha 1", Status: domain.TaskDone}, Closed: true},
			{TaskRef: domain.TaskRef{Key: "HNK-9", Title: "Filha 2", Status: domain.TaskTodo}, PersonaName: "Bia"},
		},
	}
	pol := domain.ReviewPolicy{Required: true, RequiredSource: domain.ReviewLayerTask}
	rc.ReviewPolicy = &pol
	sec := coordinationSection(rc)
	for _, want := range []string{"## Coordenação", "HNK-1 (Épico) › HNK-2 (Entrega)", "HNK-3 Modelar [fechada]", "HNK-4 Cancelada [cancelada, sem entrega]",
		"HNK-6 Aberta [aberta]", "esperam por esta", "HNK-7", "Subtasks (1 abertas de 2)", "HNK-9 Filha 2 [todo] · Bia", "só pode ser concluída quando as subtasks fecharem",
		"passa por revisão (definido na própria task)"} {
		if !strings.Contains(sec, want) {
			t.Errorf("seção sem %q:\n%s", want, sec)
		}
	}
	// a política que vem da própria persona não precisa de aviso
	pol.RequiredSource = domain.ReviewLayerPersona
	if strings.Contains(coordinationSection(rc), "passa por revisão") {
		t.Error("origem persona não é avisada")
	}
	system, prompt := BuildPrompt(rc, "", "")
	if !strings.Contains(prompt, "## Coordenação") || !strings.Contains(system, "depends_on") || !strings.Contains(system, "hanko_task_dependency") {
		t.Errorf("o protocolo e o briefing precisam falar da coordenação:\n%s\n%s", system, prompt)
	}
}
