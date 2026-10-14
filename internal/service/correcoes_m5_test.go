package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// candidatesAt lists the wake candidates as of `at`.
func candidatesAt(t *testing.T, e *env, at time.Time) map[domain.ID]bool {
	t.Helper()
	e.svc.Now = func() time.Time { return at }
	cs, err := e.svc.WakeCandidates(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	m := map[domain.ID]bool{}
	for _, c := range cs {
		m[c.TaskID] = true
	}
	return m
}

// ---------- 1. safety net without run loops ----------

// runCycle opens, starts and finishes a run on the task; mid runs while it is running.
func runCycle(t *testing.T, e *env, p domain.Persona, tv service.TaskView, start time.Time, mid func(at time.Time)) domain.Run {
	t.Helper()
	ctx := context.Background()
	e.svc.Now = func() time.Time { return start }
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Now = func() time.Time { return start.Add(time.Minute) }
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if mid != nil {
		mid(start.Add(2 * time.Minute))
	}
	e.svc.Now = func() time.Time { return start.Add(5 * time.Minute) }
	finishOK(t, e, run)
	return run
}

func TestSafetyNetIgnoresPersonaOwnWritesDuringRun(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return t0 }
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})

	runCycle(t, e, p, tv, t0.Add(10*time.Minute), func(at time.Time) {
		// the persona edits its own task while it runs (title, description, priority, project-less)
		e.svc.Now = func() time.Time { return at }
		title, desc, prio := "novo título", "nova descrição", domain.PriorityHigh
		if _, err := e.svc.UpdateTask(ctx, domain.PersonaActor(p.ID), e.co.ID, tv.ID, service.UpdateTaskInput{Title: &title, Description: &desc, Priority: &prio}); err != nil {
			t.Fatal(err)
		}
	})
	// the run is over (t0+15m); well past the one-minute grace period the task must not be a candidate:
	// the only "change" after the run started was the persona's own edit.
	if candidatesAt(t, e, t0.Add(30*time.Minute))[tv.ID] {
		t.Fatal("edição da própria persona durante a run não pode virar candidata depois (laço de runs)")
	}
	if candidatesAt(t, e, t0.Add(10*time.Hour))[tv.ID] {
		t.Fatal("nem muito tempo depois")
	}
}

func TestSafetyNetHumanEditsThatDoNotWake(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return t0 }
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	runCycle(t, e, p, tv, t0.Add(10*time.Minute), nil) // finishes at t0+15m
	if candidatesAt(t, e, t0.Add(20*time.Minute))[tv.ID] {
		t.Fatal("sem mudança depois da run não é candidata")
	}

	e.svc.Now = func() time.Time { return t0.Add(25 * time.Minute) }
	prio, title, desc := domain.PriorityCritical, "outro título", "outra descrição"
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Priority: &prio, Title: &title, Description: &desc}); err != nil {
		t.Fatal(err)
	}
	if candidatesAt(t, e, t0.Add(40*time.Minute))[tv.ID] {
		t.Fatal("humano mudando prioridade/título/descrição não deve acordar a persona")
	}
}

func TestSafetyNetHumanWakeReasons(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	t.Run("comentário humano depois da run", func(t *testing.T) {
		e := newEnv(t)
		e.svc.Now = func() time.Time { return t0 }
		p := e.persona(t, "Eng", nil)
		tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
		runCycle(t, e, p, tv, t0.Add(10*time.Minute), nil)
		e.svc.Now = func() time.Time { return t0.Add(30 * time.Minute) }
		if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "e agora?"}); err != nil {
			t.Fatal(err)
		}
		if candidatesAt(t, e, t0.Add(30*time.Minute+30*time.Second))[tv.ID] {
			t.Fatal("antes do período de graça não é candidata")
		}
		if !candidatesAt(t, e, t0.Add(33*time.Minute))[tv.ID] {
			t.Fatal("comentário humano sem run depois dele deveria tornar a task candidata")
		}
	})

	t.Run("comentário da persona não conta", func(t *testing.T) {
		e := newEnv(t)
		e.svc.Now = func() time.Time { return t0 }
		p := e.persona(t, "Eng", nil)
		tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
		runCycle(t, e, p, tv, t0.Add(10*time.Minute), nil)
		e.svc.Now = func() time.Time { return t0.Add(30 * time.Minute) }
		if _, err := e.svc.AddComment(ctx, domain.PersonaActor(p.ID), e.co.ID, tv.ID, service.AddCommentInput{Body: "nota minha"}); err != nil {
			t.Fatal(err)
		}
		if candidatesAt(t, e, t0.Add(40*time.Minute))[tv.ID] {
			t.Fatal("comentário da própria persona não acorda")
		}
	})

	t.Run("humano muda o status depois da run", func(t *testing.T) {
		e := newEnv(t)
		e.svc.Now = func() time.Time { return t0 }
		p := e.persona(t, "Eng", nil)
		tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
		runCycle(t, e, p, tv, t0.Add(10*time.Minute), nil)
		e.svc.Now = func() time.Time { return t0.Add(30 * time.Minute) }
		todo := domain.TaskTodo
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &todo}); err != nil {
			t.Fatal(err)
		}
		if !candidatesAt(t, e, t0.Add(33*time.Minute))[tv.ID] {
			t.Fatal("mudança de status por humano sem run depois dela deveria tornar a task candidata")
		}
	})

	t.Run("humano atribui a task a outra persona", func(t *testing.T) {
		e := newEnv(t)
		e.svc.Now = func() time.Time { return t0 }
		p := e.persona(t, "Eng", nil)
		q := e.persona(t, "Outra", nil)
		tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
		runCycle(t, e, p, tv, t0.Add(10*time.Minute), nil)
		e.svc.Now = func() time.Time { return t0.Add(30 * time.Minute) }
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{PersonaID: &q.ID}); err != nil {
			t.Fatal(err)
		}
		if !candidatesAt(t, e, t0.Add(33*time.Minute))[tv.ID] {
			t.Fatal("reatribuição por humano deveria tornar a task candidata para a nova persona")
		}
	})

	t.Run("mudança humana durante a run é coberta pela janela da run", func(t *testing.T) {
		e := newEnv(t)
		e.svc.Now = func() time.Time { return t0 }
		p := e.persona(t, "Eng", nil)
		tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
		runCycle(t, e, p, tv, t0.Add(10*time.Minute), func(at time.Time) {
			e.svc.Now = func() time.Time { return at }
			prio := domain.PriorityLow
			if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Priority: &prio}); err != nil {
				t.Fatal(err)
			}
			blocked := domain.TaskBlocked
			if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &blocked}); err != nil {
				t.Fatal(err)
			}
			inprog := domain.TaskInProgress
			if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &inprog}); err != nil {
				t.Fatal(err)
			}
		})
		if candidatesAt(t, e, t0.Add(40*time.Minute))[tv.ID] {
			t.Fatal("a mudança caiu dentro da janela da run: não é um wake perdido")
		}
	})

	t.Run("task atribuída e nunca tentada continua candidata", func(t *testing.T) {
		e := newEnv(t)
		e.svc.Now = func() time.Time { return t0 }
		p := e.persona(t, "Eng", nil)
		tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
		if !candidatesAt(t, e, t0.Add(2*time.Minute))[tv.ID] {
			t.Fatal("wake perdido de uma task nova deveria ser achado")
		}
		// atribuída depois, por humano
		un := e.task(t, service.CreateTaskInput{Title: "sem dono"})
		e.svc.Now = func() time.Time { return t0.Add(5 * time.Minute) }
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, un.ID, service.UpdateTaskInput{PersonaID: &p.ID}); err != nil {
			t.Fatal(err)
		}
		if !candidatesAt(t, e, t0.Add(8*time.Minute))[un.ID] {
			t.Fatal("atribuição humana sem run deveria tornar a task candidata")
		}
	})
}

// ---------- 7. budget reservation: no wake storm ----------

func TestReservationRefusalSilencesSafetyNet(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e := newEnv(t)
	e.svc.RunReserveUSD = 4
	spender := e.persona(t, "Gasta", nil)
	worker := e.persona(t, "Trabalha", nil)
	e.spend(t, spender, march, nil, usd(1.0))
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(1000))); err != nil {
		t.Fatal(err)
	}
	e.svc.Now = func() time.Time { return march }
	tasks := fiveTasks(t, e, worker)
	if got := openRuns(t, e, worker, tasks); got != 2 { // 1 + 4 = 5, 1 + 8 = 9 fit; 1 + 12 does not
		t.Fatalf("runs abertas = %d, want 2", got)
	}
	var refused []domain.ID
	for _, tv := range tasks {
		if _, err := e.svc.CheckoutRun(ctx, wakeOf(worker, tv, domain.TriggerHeartbeat)); errors.Is(err, domain.ErrActiveRun) {
			continue
		}
		refused = append(refused, tv.ID)
	}
	if len(refused) != 3 {
		t.Fatalf("tasks recusadas = %d, want 3", len(refused))
	}

	cands := candidatesAt(t, e, march.Add(2*time.Minute))
	for _, id := range refused {
		if cands[id] {
			t.Fatal("persona recusada por reserva não pode ser candidata da rede de segurança por 10 min")
		}
	}
	// an explicit human wake still tries (and is refused again, which re-arms the quiet period)
	_, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: worker.ID, TaskID: refused[0], Trigger: domain.TriggerManual})
	wantErr(t, err, domain.ErrBudgetExceeded)

	// the manual refusal at +2 min re-armed the quiet period: still silent at +11 min, back in the net at +13 min
	for _, id := range refused {
		if candidatesAt(t, e, march.Add(11*time.Minute))[id] {
			t.Fatal("a recusa manual reabre o silêncio de 10 min")
		}
	}
	later := candidatesAt(t, e, march.Add(13*time.Minute))
	for _, id := range refused {
		if !later[id] {
			t.Fatal("passados 10 min a persona volta à rede de segurança")
		}
	}

	// a new refusal by the heartbeat silences it again
	e.svc.Now = func() time.Time { return march.Add(14 * time.Minute) }
	_, err = e.svc.CheckoutRun(ctx, wakeOf(worker, tasks[4], domain.TriggerHeartbeat))
	wantErr(t, err, domain.ErrBudgetExceeded)
	if candidatesAt(t, e, march.Add(15*time.Minute))[tasks[4].ID] {
		t.Fatal("nova recusa reabre o silêncio")
	}
	// raising the ceiling lifts the quiet period at once
	e.svc.Now = func() time.Time { return march.Add(16 * time.Minute) }
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(100000))); err != nil {
		t.Fatal(err)
	}
	if !candidatesAt(t, e, march.Add(17*time.Minute))[tasks[4].ID] {
		t.Fatal("subir o teto devolve a persona à rede de segurança")
	}
}

// ---------- 8. manual pause survives the budget tick ----------

func TestManualPauseNotUndoneByBudgetTick(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	april := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	e := newEnv(t)
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(500)) })
	e.spend(t, p, march, nil, usd(5.5))
	tv := e.task(t, service.CreateTaskInput{Title: "aberta", PersonaID: &p.ID})
	_, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	got, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if got.Status != domain.PersonaPaused || got.PauseReason == "" {
		t.Fatalf("pausa por orçamento esperada: %s %q", got.Status, got.PauseReason)
	}

	// an unrelated edit keeps the budget pause (and its reason)
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Title: ptr("Engenheira")}); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); got.PauseReason == "" {
		t.Fatal("editar o título não pode apagar o motivo da pausa por orçamento")
	}

	// the human sets the status explicitly (pause on purpose): the pause is now theirs
	paused := domain.PersonaPaused
	got, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: &paused})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.PersonaPaused || got.PauseReason != "" {
		t.Fatalf("pausa manual não deve manter o motivo de orçamento: %s %q", got.Status, got.PauseReason)
	}

	// the month rolls over; the budget tick must leave the manual pause alone
	e.svc.Now = func() time.Time { return april }
	n, err := e.svc.ResumeBudgetPaused(ctx)
	if err != nil || n != 0 {
		t.Fatalf("tick retomou %d persona(s) (%v); pausa manual precisa ficar", n, err)
	}
	if got, _ = e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); got.Status != domain.PersonaPaused {
		t.Fatalf("status = %s, quer paused", got.Status)
	}
}

// ---------- 10. run event seq is race-proof ----------

func TestAppendRunEventSeqUnderContention(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	const writers, each = 24, 10
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if _, err := e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: domain.EventStdout, Text: "x"}); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	evs, err := e.svc.ListRunEvents(ctx, e.co.ID, run.ID, 0, 1000)
	if err != nil || len(evs) != writers*each {
		t.Fatalf("eventos = %d (%v), quer %d", len(evs), err, writers*each)
	}
	for i, x := range evs {
		if x.Seq != int64(i+1) {
			t.Fatalf("lacuna ou repetição de seq na posição %d: %d", i, x.Seq)
		}
	}
	// a finished run still accepts late events with the next seq
	if _, err := e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: domain.EventSystem, Text: "fim"}); err != nil {
		t.Fatal(err)
	}
}

// ---------- gates ----------

func TestDecideGateOnExpiredGateConflicts(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	g, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "Aprovar?", TaskID: &tv.ID})
	if err != nil {
		t.Fatal(err)
	}
	cancelled := domain.TaskCancelled
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &cancelled}); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.GetGate(ctx, e.co.ID, g.ID)
	if err != nil || got.Status != domain.GateExpired {
		t.Fatalf("gate deveria ter expirado com a task fechada: %+v %v", got.Status, err)
	}
	for _, d := range []domain.GateStatus{domain.GateApproved, domain.GateRejected} {
		_, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: d})
		wantErr(t, err, domain.ErrConflict)
	}
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateChangesRequested, Note: "ajuste"})
	wantErr(t, err, domain.ErrConflict)
	if got, _ := e.svc.GetGate(ctx, e.co.ID, g.ID); got.Status != domain.GateExpired || got.DecisionNote != "Task encerrada antes da decisão." {
		t.Fatalf("o gate expirado não pode mudar: %+v", got)
	}
}

// ---------- 2. admission and owner bootstrap (firebase) ----------

func fbLogin(uid, email string, mod func(*service.FirebaseLogin)) service.FirebaseLogin {
	in := service.FirebaseLogin{UID: uid, Email: email, Name: "Alguém"}
	if mod != nil {
		mod(&in)
	}
	return in
}

func TestLoginFirebaseOwnerEmail(t *testing.T) {
	ctx := context.Background()

	t.Run("sem listas: só o dono nomeado entra", func(t *testing.T) {
		e := newEnv(t)
		own := func(in *service.FirebaseLogin) { in.OwnerEmail = "Dona@Exemplo.com" }
		// o primeiro visitante NÃO vira dono
		_, err := e.svc.LoginFirebase(ctx, fbLogin("u-intruso", "intruso@x.com", own))
		wantErr(t, err, domain.ErrNotAllowed)
		if n, _ := e.svc.CountUsers(ctx); n != 0 {
			t.Fatalf("o intruso não pode ter virado usuário: %d", n)
		}
		u, err := e.svc.LoginFirebase(ctx, fbLogin("u-dona", "dona@exemplo.com", own))
		if err != nil || u.Role != domain.RoleOwner {
			t.Fatalf("dono nomeado: %+v %v", u, err)
		}
		_, err = e.svc.LoginFirebase(ctx, fbLogin("u-outro", "outro@x.com", own))
		wantErr(t, err, domain.ErrNotAllowed)
		// e-mail do dono com outro uid (conta nova do mesmo e-mail) esbarra no conflito de e-mail, não vira segundo dono
		_, err = e.svc.LoginFirebase(ctx, fbLogin("u-dona-2", "dona@exemplo.com", own))
		if err == nil {
			t.Fatal("segundo uid para o e-mail do dono deveria ser recusado")
		}
	})

	t.Run("com listas: membro admitido não vira dono; dono entra mesmo fora da lista", func(t *testing.T) {
		e := newEnv(t)
		opts := func(admitted bool) func(*service.FirebaseLogin) {
			return func(in *service.FirebaseLogin) {
				in.OwnerEmail, in.ListsConfigured, in.Admitted = "dona@exemplo.com", true, admitted
			}
		}
		m, err := e.svc.LoginFirebase(ctx, fbLogin("u-m", "membro@exemplo.com", opts(true)))
		if err != nil || m.Role != domain.RoleMember {
			t.Fatalf("o primeiro admitido não é dono quando há dono nomeado: %+v %v", m, err)
		}
		_, err = e.svc.LoginFirebase(ctx, fbLogin("u-f", "fora@x.com", opts(false)))
		wantErr(t, err, domain.ErrNotAllowed)
		o, err := e.svc.LoginFirebase(ctx, fbLogin("u-o", "dona@exemplo.com", opts(false)))
		if err != nil || o.Role != domain.RoleOwner {
			t.Fatalf("dono nomeado fora das listas: %+v %v", o, err)
		}
		// relogin
		if o2, err := e.svc.LoginFirebase(ctx, fbLogin("u-o", "dona@exemplo.com", opts(false))); err != nil || o2.ID != o.ID {
			t.Fatalf("relogin do dono: %+v %v", o2, err)
		}
	})

	t.Run("sem dono nomeado o comportamento antigo vale", func(t *testing.T) {
		e := newEnv(t)
		a, err := e.svc.LoginFirebase(ctx, fbLogin("u-a", "a@x.com", nil))
		if err != nil || a.Role != domain.RoleOwner {
			t.Fatalf("primeiro vira dono: %+v %v", a, err)
		}
		_, err = e.svc.LoginFirebase(ctx, fbLogin("u-b", "b@x.com", nil))
		wantErr(t, err, domain.ErrNotAllowed)
	})
}

// ---------- 6. sessions: absolute lifetime and admission ----------

func TestSessionAbsoluteLifetime(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := mustOwner(t, e)
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	start := clock
	e.svc.Now = func() time.Time { return clock }
	tok, sess, err := e.svc.CreateSession(ctx, u.ID, service.SessionMeta{}, "")
	if err != nil {
		t.Fatal(err)
	}
	var abs time.Time
	if err := e.svc.Pool.QueryRow(ctx, `SELECT absolute_expires_at FROM sessions WHERE id=$1`, sess.ID).Scan(&abs); err != nil {
		t.Fatal(err)
	}
	if !abs.Equal(start.Add(auth.SessionAbsoluteTTL)) || auth.SessionAbsoluteTTL != 90*24*time.Hour {
		t.Fatalf("teto absoluto = %v", abs)
	}
	// uso a cada 20 dias mantém a deslizante viva...
	for _, day := range []int{20, 40, 60, 80} {
		clock = start.Add(time.Duration(day) * 24 * time.Hour)
		if _, err := e.svc.ResolveSession(ctx, tok); err != nil {
			t.Fatalf("dia %d: %v", day, err)
		}
	}
	// ...mas a validade nunca passa do teto absoluto
	var exp time.Time
	_ = e.svc.Pool.QueryRow(ctx, `SELECT expires_at FROM sessions WHERE id=$1`, sess.ID).Scan(&exp)
	if exp.After(abs) {
		t.Fatalf("expires_at %v passou do teto %v", exp, abs)
	}
	clock = start.Add(89 * 24 * time.Hour)
	if _, err := e.svc.ResolveSession(ctx, tok); err != nil {
		t.Fatalf("dia 89 ainda vale: %v", err)
	}
	// dia 91: a deslizante (último uso no dia 89) ainda não venceu, o teto absoluto sim
	clock = start.Add(91 * 24 * time.Hour)
	if _, err := e.svc.ResolveSession(ctx, tok); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("depois de 90 dias a sessão tem de expirar mesmo em uso: %v", err)
	}
}

func TestSessionAndAPIKeyDroppedWhenEmailLeavesAdmissionList(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.Admission = auth.Admission{Domains: []string{"exemplo.com"}}
	login := func(uid, email string) domain.User {
		u, err := e.svc.LoginFirebase(ctx, fbLogin(uid, email, func(in *service.FirebaseLogin) {
			in.ListsConfigured, in.Admitted = true, e.svc.Admission.Allows(email)
		}))
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	dona := login("u1", "dona@exemplo.com")
	ana := login("u2", "ana@exemplo.com")
	tokDona, _, _ := e.svc.CreateSession(ctx, dona.ID, service.SessionMeta{}, "")
	tokAna, _, _ := e.svc.CreateSession(ctx, ana.ID, service.SessionMeta{}, "")
	keyAna, _, err := e.svc.CreateAPIKey(ctx, ana.ID, "ci")
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func() error{
		"sessão da dona": func() error { _, err := e.svc.ResolveSession(ctx, tokDona); return err },
		"sessão da ana":  func() error { _, err := e.svc.ResolveSession(ctx, tokAna); return err },
		"api key da ana": func() error { _, err := e.svc.ResolveAPIKey(ctx, keyAna); return err },
	} {
		if err := f(); err != nil {
			t.Fatalf("%s deveria valer: %v", name, err)
		}
	}

	// a lista passa a admitir só a dona (ana sai do domínio permitido)
	e.svc.Admission = auth.Admission{Emails: []string{"dona@exemplo.com"}}
	if _, err := e.svc.ResolveSession(ctx, tokDona); err != nil {
		t.Fatalf("a dona continua admitida: %v", err)
	}
	if _, err := e.svc.ResolveSession(ctx, tokAna); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("sessão de quem saiu da lista deveria cair: %v", err)
	}
	if _, err := e.svc.ResolveAPIKey(ctx, keyAna); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("API key de quem saiu da lista deveria cair: %v", err)
	}
	// o dono nomeado passa mesmo fora das listas
	e.svc.OwnerEmail = "ana@exemplo.com"
	e.svc.Admission = auth.Admission{Emails: []string{"outro@exemplo.com"}}
	if _, err := e.svc.ResolveAPIKey(ctx, keyAna); err != nil {
		t.Fatalf("dono nomeado sempre passa: %v", err)
	}
	// sem listas (ou provider local) nada é cortado
	e.svc.OwnerEmail, e.svc.Admission = "", auth.Admission{}
	if _, err := e.svc.ResolveSession(ctx, tokDona); err != nil {
		t.Fatalf("sem listas ninguém é cortado: %v", err)
	}

	// usuário local não depende das listas do firebase
	le := newEnv(t)
	owner := mustOwner(t, le)
	tokLocal, _, _ := le.svc.CreateSession(ctx, owner.ID, service.SessionMeta{}, "")
	le.svc.Admission = auth.Admission{Domains: []string{"nada.com"}}
	if _, err := le.svc.ResolveSession(ctx, tokLocal); err != nil {
		t.Fatalf("usuário local não depende das listas do firebase: %v", err)
	}
}

// ---------- 3. argon2 gate ----------

func TestPasswordGateBoundsHashing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := mustOwner(t, e)
	e.svc.PasswordGate = auth.NewPasswordGate(1, 40*time.Millisecond)
	release, err := e.svc.PasswordGate.Acquire(ctx) // the only slot is taken
	if err != nil {
		t.Fatal(err)
	}

	for name, f := range map[string]func() error{
		"login de usuário existente": func() error { _, err := e.svc.AuthenticateLocal(ctx, u.Email, goodPass); return err },
		"login de e-mail inexistente": func() error {
			_, err := e.svc.AuthenticateLocal(ctx, "ninguem@x.com", goodPass)
			return err
		},
		"criar dono": func() error { _, err := e.svc.CreateOwner(ctx, "x@x.com", "X", goodPass); return err },
	} {
		start := time.Now()
		if err := f(); !errors.Is(err, auth.ErrBusy) {
			t.Errorf("%s: com o semáforo cheio deveria dar auth.ErrBusy, veio %v", name, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s: esperou %v; o teto é 2 s por padrão", name, d)
		}
	}
	if n, _ := e.svc.CountUsers(ctx); n != 1 {
		t.Fatalf("usuários = %d", n)
	}

	release()
	if _, err := e.svc.AuthenticateLocal(ctx, u.Email, goodPass); err != nil {
		t.Fatalf("com o slot livre o login passa: %v", err)
	}
	if _, err := e.svc.AuthenticateLocal(ctx, u.Email, "senha-errada-123"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("senha errada segue sendo ErrUnauthenticated: %v", err)
	}
	// o slot volta depois de cada operação
	if r, err := e.svc.PasswordGate.Acquire(ctx); err != nil {
		t.Fatalf("slot vazou: %v", err)
	} else {
		r()
	}
}
