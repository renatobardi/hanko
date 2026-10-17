//go:build integration

package heartbeat

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func runsOf(t *testing.T, svc *service.Service, co domain.ID, task service.TaskView) int {
	t.Helper()
	_, all := terminalRuns(t, svc, co, task.ID)
	return all
}

func finished(t *testing.T, svc *service.Service, co domain.ID, task service.TaskView, n int) func() bool {
	return func() bool { d, a := terminalRuns(t, svc, co, task.ID); return a == n && d == n }
}

// Demo do M14 (ADR-0040), de ponta a ponta com a fila de verdade e o harness fake: a persona quebra a task em
// três com dependência (A → B → C); elas destravam em cascata, uma depois da outra, e o pai acorda quando a última
// fecha. Em seguida, pausar o pai congela tudo: a subtask criada durante a pausa não roda até retomar.
func TestCoordinationDemoBreakIntoThreeCascadeAndFreeze(t *testing.T) {
	ctx := context.Background()
	svc, _, _, co := newWakeStack(t, Options{PeriodicInterval: time.Hour}, 30*time.Millisecond)
	ana, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "Entregar a feature", PersonaID: &ana.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "primeira run do pai", finished(t, svc, co.ID, parent, 1))
	waitFor(t, 10*time.Second, "wake do pai encerrado", func() bool { return busyWakes(t, svc) == 0 })

	// a persona divide o trabalho: A, B depende de A, C depende de B (as tools MCP chamam estes mesmos casos de uso)
	me := domain.PersonaActor(ana.ID)
	a, err := svc.CreateTask(ctx, me, co.ID, service.CreateTaskInput{Title: "A: modelar", PersonaID: &ana.ID, ParentID: &parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateTask(ctx, me, co.ID, service.CreateTaskInput{Title: "B: implementar", PersonaID: &ana.ID, ParentID: &parent.ID, DependsOn: []domain.ID{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateTask(ctx, me, co.ID, service.CreateTaskInput{Title: "C: validar", PersonaID: &ana.ID, ParentID: &parent.ID, DependsOn: []domain.ID{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !svcBlocked(t, svc, co.ID, b) || !svcBlocked(t, svc, co.ID, c) {
		t.Fatal("B e C nascem bloqueadas")
	}

	// só A roda; B e C esperam
	waitFor(t, 30*time.Second, "run de A", finished(t, svc, co.ID, a, 1))
	time.Sleep(700 * time.Millisecond)
	if runsOf(t, svc, co.ID, b) != 0 || runsOf(t, svc, co.ID, c) != 0 {
		t.Fatalf("B/C não podem rodar antes das dependências: B=%d C=%d", runsOf(t, svc, co.ID, b), runsOf(t, svc, co.ID, c))
	}

	// cascata: fechar A libera B; fechar B libera C
	done := domain.TaskDone
	if _, err := svc.UpdateTask(ctx, domain.HumanActor, co.ID, a.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "run de B depois de A", finished(t, svc, co.ID, b, 1))
	time.Sleep(500 * time.Millisecond)
	if runsOf(t, svc, co.ID, c) != 0 {
		t.Fatal("C ainda espera B")
	}
	if _, err := svc.UpdateTask(ctx, domain.HumanActor, co.ID, b.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "run de C depois de B", finished(t, svc, co.ID, c, 1))

	// o pai só acorda quando o último filho fecha
	time.Sleep(500 * time.Millisecond)
	if runsOf(t, svc, co.ID, parent) != 1 {
		t.Fatalf("o pai não acorda com filho aberto: %d runs", runsOf(t, svc, co.ID, parent))
	}
	if _, err := svc.UpdateTask(ctx, domain.HumanActor, co.ID, parent.ID, service.UpdateTaskInput{Status: &done}); err == nil {
		t.Fatal("o pai não conclui com a subtask C aberta")
	}
	if _, err := svc.UpdateTask(ctx, domain.HumanActor, co.ID, c.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "o pai acordou quando a última subtask fechou", finished(t, svc, co.ID, parent, 2))

	// pausar o pai congela tudo: nada roda na subárvore enquanto durar
	if _, err := svc.HoldTask(ctx, domain.HumanActor, co.ID, parent.ID, "revisão de arquitetura"); err != nil {
		t.Fatal(err)
	}
	d, err := svc.CreateTask(ctx, me, co.ID, service.CreateTaskInput{Title: "D: ajuste", PersonaID: &ana.ID, ParentID: &parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := svc.CreateTask(ctx, me, co.ID, service.CreateTaskInput{Title: "E: depende de D", PersonaID: &ana.ID, ParentID: &parent.ID, DependsOn: []domain.ID{d.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if hv, _ := svc.GetTask(ctx, co.ID, d.Key); hv.Hold == nil || !hv.Hold.Inherited {
		t.Fatalf("D herda a pausa do pai: %+v", hv.Hold)
	}
	time.Sleep(900 * time.Millisecond)
	if runsOf(t, svc, co.ID, d) != 0 || runsOf(t, svc, co.ID, e2) != 0 {
		t.Fatalf("pausada a subárvore, nenhuma run abre: D=%d E=%d", runsOf(t, svc, co.ID, d), runsOf(t, svc, co.ID, e2))
	}
	// fechar uma dependência durante a pausa também não acorda ninguém
	if _, err := svc.UpdateTask(ctx, domain.HumanActor, co.ID, d.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if runsOf(t, svc, co.ID, e2) != 0 {
		t.Fatal("E liberada durante a pausa não roda")
	}

	// retomar acorda os wakes pendentes
	if _, err := svc.ResumeTask(ctx, domain.HumanActor, co.ID, parent.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "E roda depois de retomar", finished(t, svc, co.ID, e2, 1))
}

func svcBlocked(t *testing.T, svc *service.Service, co domain.ID, tv service.TaskView) bool {
	t.Helper()
	got, err := svc.GetTask(context.Background(), co, tv.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	return got.Blocked
}
