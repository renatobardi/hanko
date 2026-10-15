package service_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Metas (ADR-0028).

func (e *env) goal(t *testing.T, mod func(*service.CreateGoalInput)) domain.Goal {
	t.Helper()
	in := service.CreateGoalInput{Title: "Chegar a 100 usuários", Metric: "usuários ativos", Start: 0, Target: 100}
	if mod != nil {
		mod(&in)
	}
	g, err := e.svc.CreateGoal(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	return g
}

func TestGoalProgressMath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	up := e.goal(t, nil)
	if up.Slug == "" || up.Status != domain.GoalActive || up.CurrentValue != 0 {
		t.Fatalf("meta nova: %+v", up)
	}
	for _, c := range []struct{ v, pct float64 }{{25, 25}, {0, 0}, {-30, 0}, {99, 99}} {
		gv, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, up.ID, c.v, "")
		if err != nil || !near(gv.ProgressPercent, c.pct) || gv.Status != domain.GoalActive || gv.CurrentValue != c.v {
			t.Fatalf("valor %v: %+v %v", c.v, gv, err)
		}
	}

	// meta de redução: de 200 para 50
	down := e.goal(t, func(in *service.CreateGoalInput) { in.Title = "Reduzir latência"; in.Start = 200; in.Target = 50 })
	if down.CurrentValue != 200 {
		t.Fatalf("começa no inicial: %+v", down)
	}
	gv, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, down.ID, 125, "")
	if err != nil || !near(gv.ProgressPercent, 50) {
		t.Fatalf("redução a meio caminho: %+v %v", gv, err)
	}
	if gv, _ = e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, down.ID, 400, ""); gv.ProgressPercent != 0 {
		t.Errorf("pior que o inicial deve dar 0: %v", gv.ProgressPercent)
	}
	gv, err = e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, down.ID, 50, "meta batida")
	if err != nil || gv.Status != domain.GoalAchieved || gv.ProgressPercent != 100 || gv.AchievedAt == nil {
		t.Fatalf("redução atingida: %+v %v", gv, err)
	}
}

func TestGoalValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	create := func(mod func(*service.CreateGoalInput)) error {
		in := service.CreateGoalInput{Title: "ok", Start: 0, Target: 10}
		mod(&in)
		_, err := e.svc.CreateGoal(ctx, domain.HumanActor, e.co.ID, in)
		return err
	}
	ghost := domain.NewID()
	for name, mod := range map[string]func(*service.CreateGoalInput){
		"título vazio":     func(in *service.CreateGoalInput) { in.Title = " " },
		"inicial = alvo":   func(in *service.CreateGoalInput) { in.Target = in.Start },
		"NaN":              func(in *service.CreateGoalInput) { in.Target = math.NaN() },
		"infinito":         func(in *service.CreateGoalInput) { in.Target = math.Inf(1) },
		"projeto fantasma": func(in *service.CreateGoalInput) { in.ProjectID = &ghost },
		"dona fantasma":    func(in *service.CreateGoalInput) { in.OwnerPersonaID = &ghost },
		"título enorme":    func(in *service.CreateGoalInput) { in.Title = strings.Repeat("x", 500) },
	} {
		t.Run(name, func(t *testing.T) { wantErr(t, create(mod), domain.ErrInvalid) })
	}

	g := e.goal(t, nil)
	if _, err := e.svc.GetGoal(ctx, e.co.ID, g.Slug); err != nil {
		t.Errorf("por slug: %v", err)
	}
	if _, err := e.svc.GetGoal(ctx, e.co.ID, g.ID.String()); err != nil {
		t.Errorf("por id: %v", err)
	}
	_, err := e.svc.GetGoal(ctx, e.co.ID, "nao-existe")
	wantErr(t, err, domain.ErrNotFound)
	// slugs repetidos recebem sufixo ou falham, mas nunca colidem em silêncio
	g2, err := e.svc.CreateGoal(ctx, domain.HumanActor, e.co.ID, service.CreateGoalInput{Title: g.Title, Start: 0, Target: 5})
	if err == nil && g2.Slug == g.Slug {
		t.Errorf("slug duplicado: %q", g2.Slug)
	}
}

func TestGoalAchievedFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return t0 }
	g := e.goal(t, nil)

	if gv, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, g.ID, 60, "boa"); err != nil || gv.Status != domain.GoalActive {
		t.Fatalf("60: %+v %v", gv, err)
	}
	in, _ := e.svc.Inbox(ctx, e.co.ID)
	if len(in.AchievedGoals) != 0 {
		t.Fatalf("ainda não atingida: %+v", in.AchievedGoals)
	}
	gv, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, g.ID, 120, "passamos")
	if err != nil || gv.Status != domain.GoalAchieved || gv.ProgressPercent != 100 || gv.AchievedAt == nil || !gv.AchievedAt.Equal(t0) {
		t.Fatalf("atingida: %+v %v", gv, err)
	}
	if len(gv.Updates) != 2 || gv.Updates[0].Value != 60 || gv.Updates[1].Value != 120 || gv.Updates[1].Note != "passamos" {
		t.Errorf("histórico (do mais antigo ao mais novo): %+v", gv.Updates)
	}
	acts, err := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "goal", SubjectID: &g.ID})
	verbs := map[string]int{}
	for _, a := range acts {
		verbs[a.Verb]++
	}
	if err != nil || verbs["goal.created"] != 1 || verbs["goal.progress"] != 2 || verbs["goal.achieved"] != 1 {
		t.Errorf("atividade: %v %v", verbs, err)
	}
	// Caixa de entrada: aparece, sem entrar na contagem de pendências
	in, err = e.svc.Inbox(ctx, e.co.ID)
	if err != nil || len(in.AchievedGoals) != 1 || in.AchievedGoals[0].ID != g.ID {
		t.Fatalf("inbox: %+v %v", in.AchievedGoals, err)
	}
	if in.Count() != 0 {
		t.Errorf("meta atingida não é pendência: count=%d", in.Count())
	}
	// depois de uma semana some
	e.svc.Now = func() time.Time { return t0.Add(8 * 24 * time.Hour) }
	if in, _ = e.svc.Inbox(ctx, e.co.ID); len(in.AchievedGoals) != 0 {
		t.Errorf("deveria ter saído da janela: %+v", in.AchievedGoals)
	}
	// atingida não aceita mais progresso
	_, err = e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, g.ID, 130, "")
	wantErr(t, err, domain.ErrInvalid)
}

func TestGoalAbandonReopenAndEdit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.goal(t, nil)
	ab := domain.GoalAbandoned
	up, err := e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{Status: &ab})
	if err != nil || up.Status != domain.GoalAbandoned {
		t.Fatalf("abandonar: %+v %v", up, err)
	}
	_, err = e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, g.ID, 5, "")
	wantErr(t, err, domain.ErrInvalid)
	// achieved não se escolhe à mão
	ach := domain.GoalAchieved
	_, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{Status: &ach})
	wantErr(t, err, domain.ErrInvalid)
	act := domain.GoalActive
	if up, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{Status: &act}); err != nil || up.Status != domain.GoalActive {
		t.Fatalf("reabrir: %+v %v", up, err)
	}
	// mudar o alvo para baixo do atual reavalia: a meta é atingida
	if _, err = e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, g.ID, 40, ""); err != nil {
		t.Fatal(err)
	}
	low := 30.0
	up, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{Target: &low})
	if err != nil || up.Status != domain.GoalAchieved || up.AchievedAt == nil {
		t.Fatalf("alvo reduzido: %+v %v", up, err)
	}
	// reabrir uma atingida e subir o alvo
	hi := 200.0
	up, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{Target: &hi, Status: &act})
	if err != nil || up.Status != domain.GoalActive || up.AchievedAt != nil || up.TargetValue != 200 {
		t.Fatalf("reabrir com alvo maior: %+v %v", up, err)
	}
	same := up.StartValue
	_, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{Target: &same})
	wantErr(t, err, domain.ErrInvalid)

	// prazo e projeto
	pr := e.project(t, "Crescimento", nil)
	due := time.Date(2026, 12, 31, 15, 30, 0, 0, time.UTC)
	up, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{DueDate: &due, ProjectID: &pr.ID})
	if err != nil || up.DueDate == nil || up.DueDate.Format("2006-01-02") != "2026-12-31" || up.ProjectID == nil {
		t.Fatalf("prazo/projeto: %+v %v", up, err)
	}
	if up, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{ClearDue: true, ClearProject: true}); err != nil || up.DueDate != nil || up.ProjectID != nil {
		t.Fatalf("limpar: %+v %v", up, err)
	}
}

func TestGoalRoles(t *testing.T) {
	e := newEnv(t)
	roles := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin} {
		u := mkUser(t, e, "g"+string(r)+"@x.com", domain.RoleMember)
		join(t, e, u, r)
		roles[r] = u
	}
	g := e.goal(t, nil)
	create := service.CreateGoalInput{Title: "outra", Start: 0, Target: 1}

	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember} {
		if _, err := e.svc.CreateGoal(as(roles[r]), roles[r].Actor(), e.co.ID, create); err == nil {
			t.Errorf("%s não cria meta", r)
		} else {
			wantErr(t, err, domain.ErrForbidden)
		}
	}
	if _, err := e.svc.CreateGoal(as(roles[domain.MemberAdmin]), roles[domain.MemberAdmin].Actor(), e.co.ID, create); err != nil {
		t.Errorf("admin cria: %v", err)
	}
	ab := domain.GoalAbandoned
	_, err := e.svc.UpdateGoal(as(roles[domain.MemberMember]), roles[domain.MemberMember].Actor(), e.co.ID, g.ID, service.UpdateGoalInput{Status: &ab})
	wantErr(t, err, domain.ErrForbidden)

	_, err = e.svc.RecordGoalProgress(as(roles[domain.MemberViewer]), roles[domain.MemberViewer].Actor(), e.co.ID, g.ID, 5, "")
	wantErr(t, err, domain.ErrForbidden)
	if _, err = e.svc.RecordGoalProgress(as(roles[domain.MemberMember]), roles[domain.MemberMember].Actor(), e.co.ID, g.ID, 5, ""); err != nil {
		t.Errorf("member registra progresso: %v", err)
	}
	if l, err := e.svc.ListGoalViews(as(roles[domain.MemberViewer]), e.co.ID, service.GoalFilter{}); err != nil || len(l) != 2 {
		t.Errorf("viewer lê: %d %v", len(l), err)
	}
	// persona (sem identidade humana) registra progresso
	p := e.persona(t, "Ana", nil)
	if _, err := e.svc.RecordGoalProgress(context.Background(), domain.PersonaActor(p.ID), e.co.ID, g.ID, 7, "pela persona"); err != nil {
		t.Errorf("persona registra: %v", err)
	}
	gv, _ := e.svc.GetGoalView(context.Background(), e.co.ID, g.Slug)
	if last := gv.Updates[len(gv.Updates)-1]; last.Author.Type != domain.ActorPersona || last.Value != 7 {
		t.Errorf("autoria do progresso: %+v", last)
	}
}

func TestGoalTasksAndCounts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.goal(t, nil)
	other := e.goal(t, func(in *service.CreateGoalInput) { in.Title = "Outra meta" })
	gid := g.ID

	a := e.task(t, service.CreateTaskInput{Title: "a", GoalID: &gid})
	b := e.task(t, service.CreateTaskInput{Title: "b", GoalID: &gid})
	c := e.task(t, service.CreateTaskInput{Title: "c"})
	if a.GoalID == nil || *a.GoalID != g.ID || c.GoalID != nil {
		t.Fatalf("goal_id: %+v %+v", a.GoalID, c.GoalID)
	}
	// ligar depois, trocar e desligar
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, c.ID, service.UpdateTaskInput{GoalID: &gid}); err != nil {
		t.Fatal(err)
	}
	done := domain.TaskDone
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	gv, err := e.svc.GetGoalView(ctx, e.co.ID, g.Slug)
	if err != nil || gv.TasksTotal != 3 || gv.Tasks[domain.TaskDone] != 1 || gv.Tasks[domain.TaskTodo] != 2 {
		t.Fatalf("contagens: %+v %v", gv.Tasks, err)
	}
	list, _ := e.svc.ListGoalViews(ctx, e.co.ID, service.GoalFilter{})
	for _, v := range list {
		if v.ID == other.ID && v.TasksTotal != 0 {
			t.Errorf("outra meta: %+v", v.Tasks)
		}
		if v.ID == g.ID && v.TasksTotal != 3 {
			t.Errorf("lista: %+v", v.Tasks)
		}
	}
	tasks, err := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{GoalID: &gid})
	if err != nil || len(tasks) != 3 {
		t.Fatalf("filtro por meta: %d %v", len(tasks), err)
	}
	oid := other.ID
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, c.ID, service.UpdateTaskInput{GoalID: &oid}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdateTaskInput{ClearGoal: true}); err != nil {
		t.Fatal(err)
	}
	if tasks, _ = e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{GoalID: &gid}); len(tasks) != 1 {
		t.Errorf("depois de trocar/desligar: %d", len(tasks))
	}
	// não se liga a meta fantasma nem a uma não ativa
	ghost := domain.NewID()
	_, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdateTaskInput{GoalID: &ghost})
	wantErr(t, err, domain.ErrInvalid)
	ab := domain.GoalAbandoned
	if _, err = e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, other.ID, service.UpdateGoalInput{Status: &ab}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "x", GoalID: &oid})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdateTaskInput{GoalID: &oid, ClearGoal: true})
	if err == nil {
		t.Error("GoalID e ClearGoal juntos deveriam falhar")
	}
}

func TestGoalBriefing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	pr := e.project(t, "Crescimento", nil)
	due := time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)
	inProj := e.goal(t, func(in *service.CreateGoalInput) {
		in.Title = "Meta do projeto"
		in.ProjectID = &pr.ID
		in.DueDate = &due
		in.Unit = "usuários"
	})
	loose := e.goal(t, func(in *service.CreateGoalInput) { in.Title = "Meta solta" })
	e.goal(t, func(in *service.CreateGoalInput) { in.Title = "Meta de outro lugar" })
	gone := e.goal(t, func(in *service.CreateGoalInput) { in.Title = "Abandonada"; in.ProjectID = &pr.ID })
	ab := domain.GoalAbandoned
	if _, err := e.svc.UpdateGoal(ctx, domain.HumanActor, e.co.ID, gone.ID, service.UpdateGoalInput{Status: &ab}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RecordGoalProgress(ctx, domain.HumanActor, e.co.ID, inProj.ID, 40, ""); err != nil {
		t.Fatal(err)
	}

	tv := e.task(t, service.CreateTaskInput{Title: "trabalho", PersonaID: &p.ID, ProjectID: &pr.ID, GoalID: &loose.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]service.GoalBrief{}
	for _, b := range rc.Goals {
		got[b.Slug] = b
	}
	if len(got) != 2 || got[inProj.Slug].Title != "Meta do projeto" || !near(got[inProj.Slug].Progress, 40) || got[inProj.Slug].Linked ||
		got[inProj.Slug].DueDate == nil || !got[loose.Slug].Linked {
		t.Fatalf("briefing: %+v", rc.Goals)
	}

	// task sem projeto nem meta: sem metas no briefing
	plain := e.task(t, service.CreateTaskInput{Title: "solta", PersonaID: &p.ID})
	run2, err := e.svc.CheckoutRun(ctx, wakeOf(p, plain, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if rc, err = e.svc.LoadRunContext(ctx, e.co.ID, run2.ID); err != nil || len(rc.Goals) != 0 {
		t.Errorf("sem metas: %+v %v", rc.Goals, err)
	}
}
