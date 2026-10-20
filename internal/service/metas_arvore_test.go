package service_test

import (
	"context"
	"sync"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Metas em árvore (ADR-0042 §C).

func (e *env) subgoal(t *testing.T, title string, parent *domain.Goal) domain.Goal {
	t.Helper()
	return e.goal(t, func(in *service.CreateGoalInput) {
		in.Title = title
		if parent != nil {
			in.ParentID = &parent.ID
		}
	})
}

func (e *env) goalView(t *testing.T, g domain.Goal) service.GoalView {
	t.Helper()
	v, err := e.svc.GetGoalView(context.Background(), e.co.ID, g.ID.String())
	if err != nil {
		t.Fatalf("get goal view: %v", err)
	}
	return v
}

func TestGoalTreeRollupAndAchievement(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	root := e.subgoal(t, "Visão", nil)
	a := e.subgoal(t, "Receita", &root)
	b := e.subgoal(t, "Produto", &root)

	v := e.goalView(t, root)
	if !v.Rollup || len(v.Children) != 2 || v.Depth != 1 || v.ProgressPercent != 0 {
		t.Fatalf("pai com submetas: %+v", v)
	}
	if va := e.goalView(t, a); va.ParentSlug != root.Slug || va.Depth != 2 || len(va.Path) != 1 || va.Path[0].Slug != root.Slug {
		t.Fatalf("filha: %+v", va)
	}

	// progresso direto no pai é recusado: o dele é o agregado
	_, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, root.ID, 10, "")
	wantErr(t, err, domain.ErrInvalid)

	if _, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, a.ID, 50, ""); err != nil {
		t.Fatal(err)
	}
	if v := e.goalView(t, root); !near(v.ProgressPercent, 25) { // (50% + 0%) / 2
		t.Fatalf("agregado = %v, quer 25", v.ProgressPercent)
	}
	// listagem usa o mesmo agregado
	list, err := e.svc.ListGoalViews(ctx, e.co.ID, service.GoalFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range list {
		if g.ID == root.ID && !near(g.ProgressPercent, 25) {
			t.Fatalf("lista: %v", g.ProgressPercent)
		}
	}

	// a filha atinge o alvo; o pai ainda não (Produto está em 0)
	if _, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, a.ID, 100, ""); err != nil {
		t.Fatal(err)
	}
	if v := e.goalView(t, root); v.Status != domain.GoalActive || !near(v.ProgressPercent, 50) {
		t.Fatalf("pai ainda ativo: %+v", v)
	}
	// uma filha abandonada não conta: o pai passa a depender só de Receita (atingida) e atinge
	ab := domain.GoalAbandoned
	if _, err := e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdateGoalInput{Status: &ab}); err != nil {
		t.Fatal(err)
	}
	v = e.goalView(t, root)
	if v.Status != domain.GoalAchieved || v.AchievedAt == nil || !near(v.ProgressPercent, 100) {
		t.Fatalf("pai deveria ser atingido: %+v", v)
	}
	acts, err := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "goal", SubjectID: &root.ID})
	if err != nil {
		t.Fatal(err)
	}
	var achieved bool
	for _, ev := range acts {
		achieved = achieved || ev.Verb == "goal.achieved"
	}
	if !achieved {
		t.Fatal("a meta-pai atingida deveria gerar goal.achieved")
	}

	// reabrir a filha abandonada derruba o agregado e reabre o pai
	act := domain.GoalActive
	if _, err := e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdateGoalInput{Status: &act}); err != nil {
		t.Fatal(err)
	}
	if v := e.goalView(t, root); v.Status != domain.GoalActive || v.AchievedAt != nil || !near(v.ProgressPercent, 50) {
		t.Fatalf("pai deveria reabrir: %+v", v)
	}

	// uma submeta nova num pai atingido também o reabre
	if _, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, b.ID, 100, ""); err != nil {
		t.Fatal(err)
	}
	if v := e.goalView(t, root); v.Status != domain.GoalAchieved {
		t.Fatalf("as duas atingidas: %+v", v)
	}
	c := e.subgoal(t, "Time", &root)
	if v := e.goalView(t, root); v.Status != domain.GoalActive || !near(v.ProgressPercent, 200.0/3) {
		t.Fatalf("submeta nova reabre o pai: %+v", v)
	}
	_ = c
}

func TestGoalTreeParentRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	l1 := e.subgoal(t, "N1", nil)
	l2 := e.subgoal(t, "N2", &l1)
	l3 := e.subgoal(t, "N3", &l2)
	l4 := e.subgoal(t, "N4", &l3)

	// quinto nível recusado, na criação e ao reparentar
	_, err := e.svc.CreateGoal(ctx, domain.HumanActor, e.co.ID, service.CreateGoalInput{Title: "N5", Start: 0, Target: 1, ParentID: &l4.ID})
	wantErr(t, err, domain.ErrInvalid)
	loose := e.subgoal(t, "Solta", nil)
	_, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, loose.ID, service.UpdateGoalInput{ParentID: &l4.ID})
	wantErr(t, err, domain.ErrInvalid)

	// ciclo: o avô não pode ficar sob o neto
	_, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, l1.ID, service.UpdateGoalInput{ParentID: &l3.ID})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, l1.ID, service.UpdateGoalInput{ParentID: &l1.ID})
	wantErr(t, err, domain.ErrInvalid)

	// a subárvore de 3 níveis não cabe sob o nível 2 (3+2 = 5), mas cabe sob uma raiz (3+1 = 4)
	_, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, l2.ID, service.UpdateGoalInput{ParentID: &loose.ID})
	if err != nil {
		t.Fatalf("subárvore de 3 níveis sob uma raiz cabe: %v", err)
	}
	if v := e.goalView(t, l4); v.Depth != 4 {
		t.Fatalf("profundidade de N4 = %d", v.Depth)
	}

	// pai inexistente, de outra empresa e abandonado
	ghost := domain.NewID()
	_, err = e.svc.CreateGoal(ctx, domain.HumanActor, e.co.ID, service.CreateGoalInput{Title: "x", Start: 0, Target: 1, ParentID: &ghost})
	wantErr(t, err, domain.ErrInvalid)
	ab := domain.GoalAbandoned
	dead := e.subgoal(t, "Morta", nil)
	if _, err := e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, dead.ID, service.UpdateGoalInput{Status: &ab}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateGoal(ctx, domain.HumanActor, e.co.ID, service.CreateGoalInput{Title: "y", Start: 0, Target: 1, ParentID: &dead.ID})
	wantErr(t, err, domain.ErrInvalid)

	// soltar e mudar de pai ao mesmo tempo é incoerente
	_, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, l2.ID, service.UpdateGoalInput{ParentID: &l1.ID, ClearParent: true})
	wantErr(t, err, domain.ErrInvalid)

	// virar raiz de novo: o pai antigo perde a submeta e volta a valer o próprio progresso
	if _, err := e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, l2.ID, service.UpdateGoalInput{ClearParent: true}); err != nil {
		t.Fatal(err)
	}
	if v := e.goalView(t, loose); v.Rollup || len(v.Children) != 0 {
		t.Fatalf("sem filhos de novo: %+v", v)
	}
	if v := e.goalView(t, l2); v.Depth != 1 || v.ParentSlug != "" {
		t.Fatalf("N2 raiz: %+v", v)
	}
}

// Duas mudanças concorrentes que fechariam um ciclo: o trigger com lock por empresa deixa passar só uma.
func TestGoalTreeConcurrentCycleIsRejected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.subgoal(t, "A", nil)
	b := e.subgoal(t, "B", nil)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, pair := range [][2]domain.Goal{{a, b}, {b, a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, pair[0].ID, service.UpdateGoalInput{ParentID: &pair[1].ID})
		}()
	}
	wg.Wait()
	if errs[0] == nil && errs[1] == nil {
		t.Fatal("A sob B e B sob A não podem os dois passar")
	}
	for _, v := range []domain.Goal{a, b} {
		if gv := e.goalView(t, v); gv.Depth > 2 {
			t.Fatalf("profundidade inesperada: %+v", gv)
		}
	}
}

func TestGoalTreeBriefingCarriesChain(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	root := e.subgoal(t, "Visão 2027", nil)
	mid := e.subgoal(t, "Crescer", &root)
	leaf := e.subgoal(t, "Cem usuários", &mid)
	if _, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, leaf.ID, 40, ""); err != nil {
		t.Fatal(err)
	}
	tv := e.task(t, service.CreateTaskInput{Title: "trabalho", PersonaID: &p.ID, GoalID: &leaf.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Goals) != 1 {
		t.Fatalf("metas no briefing: %+v", rc.Goals)
	}
	g := rc.Goals[0]
	want := []string{"Visão 2027", "Crescer", "Cem usuários"}
	if len(g.Chain) != 3 || g.Chain[0] != want[0] || g.Chain[1] != want[1] || g.Chain[2] != want[2] || !near(g.Progress, 40) {
		t.Fatalf("cadeia: %+v", g)
	}
}
