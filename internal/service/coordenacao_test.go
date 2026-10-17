package service_test

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Coordenação de tasks (ADR-0040): dependências, árvore, pausa herdada e política de revisão por camada.

func (e *env) closeAs(t *testing.T, actor domain.Actor, tv service.TaskView, st domain.TaskStatus) service.TaskView {
	t.Helper()
	out, err := e.svc.UpdateTask(context.Background(), actor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &st})
	if err != nil {
		t.Fatalf("update %s → %s: %v", tv.Key, st, err)
	}
	return out
}

func (e *env) get(t *testing.T, tv service.TaskView) service.TaskView {
	t.Helper()
	out, err := e.svc.GetTask(context.Background(), e.co.ID, tv.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *env) verbs(t *testing.T, tv service.TaskView) []string {
	t.Helper()
	evs, err := e.svc.ListActivity(context.Background(), e.co.ID, service.ActivityFilter{SubjectType: "task", SubjectID: &tv.ID, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Verb
	}
	return out
}

func has(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func (e *env) wokeTasks() map[domain.ID]int {
	e.jobs.mu.Lock()
	defer e.jobs.mu.Unlock()
	out := map[domain.ID]int{}
	for _, w := range e.jobs.wakes {
		out[w.TaskID]++
	}
	return out
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---------- dependências ----------

func TestDependencyBlocksUntilClosedThenWakes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	a := e.task(t, service.CreateTaskInput{Title: "A", PersonaID: &p.ID})
	b := e.task(t, service.CreateTaskInput{Title: "B", PersonaID: &p.ID, DependsOn: []domain.ID{a.ID}})
	if woke := e.wokeTasks(); woke[a.ID] != 1 || woke[b.ID] != 0 {
		t.Fatalf("a nasce com wake, b (bloqueada) não: %v", woke)
	}
	bv := e.get(t, b)
	if !bv.Blocked || bv.OpenDependencies != 1 || bv.DependenciesTotal != 1 {
		t.Fatalf("b deveria estar bloqueada: %+v", bv.TaskCoord)
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, b, domain.TriggerAssign)); !errors.Is(err, domain.ErrTaskBlocked) || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("despacho de task bloqueada: %v", err)
	}
	if err := e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, b.ID); !errors.Is(err, domain.ErrTaskBlocked) {
		t.Fatalf("wake manual de task bloqueada: %v", err)
	}
	// o estado `blocked` é derivado: o status gravado não mudou
	if bv.Status != domain.TaskTodo {
		t.Fatalf("status = %s", bv.Status)
	}
	e.jobs.reset()
	e.closeAs(t, domain.HumanActor, a, domain.TaskDone)
	if woke := e.wokeTasks(); woke[b.ID] != 1 {
		t.Fatalf("fechar a única dependência acorda b: %v", woke)
	}
	if bv = e.get(t, b); bv.Blocked || bv.OpenDependencies != 0 {
		t.Fatalf("b deveria estar livre: %+v", bv.TaskCoord)
	}
	if !has(e.verbs(t, b), "task.unblocked") || !has(e.verbs(t, b), "task.dependency_added") {
		t.Fatalf("fatos de b: %v", e.verbs(t, b))
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, b, domain.TriggerAssign)); err != nil {
		t.Fatalf("depois de liberada, b despacha: %v", err)
	}
}

func TestDependencyOnlyWakesWhenEveryOneIsClosed(t *testing.T) {
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	a1 := e.task(t, service.CreateTaskInput{Title: "A1"})
	a2 := e.task(t, service.CreateTaskInput{Title: "A2"})
	b := e.task(t, service.CreateTaskInput{Title: "B", PersonaID: &p.ID, DependsOn: []domain.ID{a1.ID, a2.ID}})
	e.jobs.reset()
	e.closeAs(t, domain.HumanActor, a1, domain.TaskDone)
	if woke := e.wokeTasks(); woke[b.ID] != 0 {
		t.Fatalf("ainda falta a2: %v", woke)
	}
	if got := e.get(t, b); got.OpenDependencies != 1 {
		t.Fatalf("open = %d", got.OpenDependencies)
	}
	// cancelar também libera, e o fato diz quantas dependências foram canceladas
	e.closeAs(t, domain.HumanActor, a2, domain.TaskCancelled)
	if woke := e.wokeTasks(); woke[b.ID] != 1 {
		t.Fatalf("a última dependência (cancelada) libera b: %v", woke)
	}
	evs, _ := e.svc.ListActivity(context.Background(), e.co.ID, service.ActivityFilter{SubjectType: "task", SubjectID: &b.ID})
	var seen bool
	for _, ev := range evs {
		if ev.Verb == "task.unblocked" && strings.Contains(strings.ReplaceAll(string(ev.Data), " ", ""), `"cancelled_dependencies":1`) {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("task.unblocked sem a contagem de canceladas: %+v", evs)
	}
}

func TestDependencyCascade(t *testing.T) {
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	a := e.task(t, service.CreateTaskInput{Title: "A", PersonaID: &p.ID})
	b := e.task(t, service.CreateTaskInput{Title: "B", PersonaID: &p.ID, DependsOn: []domain.ID{a.ID}})
	c := e.task(t, service.CreateTaskInput{Title: "C", PersonaID: &p.ID, DependsOn: []domain.ID{b.ID}})
	e.jobs.reset()
	e.closeAs(t, domain.HumanActor, a, domain.TaskDone)
	if woke := e.wokeTasks(); woke[b.ID] != 1 || woke[c.ID] != 0 {
		t.Fatalf("só b acorda: %v", woke)
	}
	e.jobs.reset()
	e.closeAs(t, domain.HumanActor, b, domain.TaskDone)
	if woke := e.wokeTasks(); woke[c.ID] != 1 {
		t.Fatalf("c acorda ao fechar b: %v", woke)
	}
	// reabrir a dependência bloqueia de novo (derivado)
	e.closeAs(t, domain.HumanActor, b, domain.TaskTodo)
	if got := e.get(t, c); !got.Blocked {
		t.Fatalf("c deveria voltar a ficar bloqueada: %+v", got.TaskCoord)
	}
}

func TestDependencyRejectsCyclesAndForeignTasks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.task(t, service.CreateTaskInput{Title: "A"})
	b := e.task(t, service.CreateTaskInput{Title: "B"})
	c := e.task(t, service.CreateTaskInput{Title: "C"})
	add := func(x, y service.TaskView) error {
		_, err := e.svc.AddTaskDependency(ctx, domain.HumanActor, e.co.ID, x.ID, y.ID)
		return err
	}
	if err := add(a, b); err != nil {
		t.Fatal(err)
	}
	if err := add(b, c); err != nil {
		t.Fatal(err)
	}
	if err := add(a, b); err != nil { // repetir não faz nada
		t.Fatalf("idempotente: %v", err)
	}
	if got := e.count(t, "SELECT count(*) FROM task_dependencies"); got != 2 {
		t.Fatalf("arestas = %d", got)
	}
	err := add(c, a)
	if !errors.Is(err, domain.ErrDependencyCycle) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ciclo transitivo: %v", err)
	}
	for _, k := range []string{a.Key, b.Key, c.Key} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("a mensagem deveria trazer a cadeia, sem %s: %v", k, err)
		}
	}
	wantErr(t, add(a, a), domain.ErrInvalid)
	wantErr(t, add(b, a), domain.ErrDependencyCycle)

	// outra empresa
	other, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Beta"})
	if err != nil {
		t.Fatal(err)
	}
	ot, err := e.svc.CreateTask(ctx, domain.HumanActor, other.ID, service.CreateTaskInput{Title: "alheia"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddTaskDependency(ctx, domain.HumanActor, e.co.ID, a.ID, ot.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("dependência entre empresas: %v", err)
	}
	if _, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "x", DependsOn: []domain.ID{ot.ID}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("criar com dependência alheia: %v", err)
	}
	if e.count(t, "SELECT count(*) FROM tasks WHERE title = 'x'") != 0 {
		t.Fatal("a criação inválida não pode deixar a task")
	}
}

func TestDependencyLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	hub := e.task(t, service.CreateTaskInput{Title: "hub"})
	for i := 0; i < domain.MaxTaskDependencies; i++ {
		d := e.task(t, service.CreateTaskInput{Title: "dep"})
		if _, err := e.svc.AddTaskDependency(ctx, domain.HumanActor, e.co.ID, hub.ID, d.ID); err != nil {
			t.Fatalf("dependência %d: %v", i, err)
		}
	}
	extra := e.task(t, service.CreateTaskInput{Title: "extra"})
	_, err := e.svc.AddTaskDependency(ctx, domain.HumanActor, e.co.ID, hub.ID, extra.ID)
	wantErr(t, err, domain.ErrInvalid)
}

// O trigger do banco é a última barreira: SQL direto também não cria ciclo nem cruza empresas.
func TestDependencyTriggersGuardTheDatabase(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.task(t, service.CreateTaskInput{Title: "A"})
	b := e.task(t, service.CreateTaskInput{Title: "B"})
	ins := `INSERT INTO task_dependencies (company_id, task_id, depends_on_id, created_by_type, created_at) VALUES ($1,$2,$3,'system',now())`
	if _, err := e.svc.Pool.Exec(ctx, ins, e.co.ID, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Pool.Exec(ctx, ins, e.co.ID, b.ID, a.ID)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.ConstraintName != "task_dependencies_no_cycle" {
		t.Fatalf("ciclo por SQL direto: %v", err)
	}
	other, _ := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Beta"})
	ot, _ := e.svc.CreateTask(ctx, domain.HumanActor, other.ID, service.CreateTaskInput{Title: "alheia"})
	if _, err := e.svc.Pool.Exec(ctx, ins, e.co.ID, a.ID, ot.ID); !errors.As(err, &pe) || pe.Code != "23503" {
		t.Fatalf("empresa diferente deveria violar a FK composta: %v", err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE tasks SET parent_id = $1 WHERE id = $2`, ot.ID, a.ID); !errors.As(err, &pe) || pe.ConstraintName != "tasks_parent_company" {
		t.Fatalf("pai de outra empresa: %v", err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE tasks SET parent_id = $1 WHERE id = $1`, a.ID); !errors.As(err, &pe) || pe.ConstraintName != "tasks_parent_no_cycle" {
		t.Fatalf("pai de si mesma: %v", err)
	}
}

// Duas escritas concorrentes A→B e B→A: no máximo uma entra (o lock por empresa serializa; sem ele as duas passariam).
func TestDependencyConcurrentOppositeEdges(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for round := 0; round < 12; round++ {
		a := e.task(t, service.CreateTaskInput{Title: "A"})
		b := e.task(t, service.CreateTaskInput{Title: "B"})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, pair := range [][2]service.TaskView{{a, b}, {b, a}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = e.svc.AddTaskDependency(ctx, domain.HumanActor, e.co.ID, pair[0].ID, pair[1].ID)
			}()
		}
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !errors.Is(err, domain.ErrDependencyCycle) {
				t.Fatalf("round %d: erro inesperado: %v", round, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d das 2 arestas opostas entraram, esperado exatamente 1 (%v)", round, ok, errs)
		}
	}
}

// Dois fechamentos simultâneos das duas únicas dependências de C acordam C exatamente uma vez: sem a trava por
// dependente, cada transação via a outra ainda aberta e ninguém acordava C (wake perdido).
func TestConcurrentClosesWakeTheDependentExactlyOnce(t *testing.T) {
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	for round := 0; round < 15; round++ {
		a1 := e.task(t, service.CreateTaskInput{Title: "A1"})
		a2 := e.task(t, service.CreateTaskInput{Title: "A2"})
		c := e.task(t, service.CreateTaskInput{Title: "C", PersonaID: &p.ID, DependsOn: []domain.ID{a1.ID, a2.ID}})
		e.jobs.reset()
		var wg sync.WaitGroup
		for _, tv := range []service.TaskView{a1, a2} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				done := domain.TaskDone
				if _, err := e.svc.UpdateTask(context.Background(), domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &done}); err != nil {
					t.Errorf("round %d: %v", round, err)
				}
			}()
		}
		wg.Wait()
		if got := e.wokeTasks()[c.ID]; got != 1 {
			t.Fatalf("round %d: C acordada %d vezes, esperado exatamente 1", round, got)
		}
	}
}

// O mesmo para o pai: os dois últimos filhos fechando juntos acordam o pai uma vez.
func TestConcurrentChildrenCloseWakeTheParentExactlyOnce(t *testing.T) {
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	for round := 0; round < 15; round++ {
		parent := e.task(t, service.CreateTaskInput{Title: "pai", PersonaID: &p.ID, Status: domain.TaskInProgress})
		c1 := e.task(t, service.CreateTaskInput{Title: "f1", ParentID: &parent.ID})
		c2 := e.task(t, service.CreateTaskInput{Title: "f2", ParentID: &parent.ID})
		e.jobs.reset()
		var wg sync.WaitGroup
		for _, tv := range []service.TaskView{c1, c2} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				done := domain.TaskDone
				if _, err := e.svc.UpdateTask(context.Background(), domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &done}); err != nil {
					var pe *pgconn.PgError
					if errors.As(err, &pe) {
						t.Errorf("round %d: %v\nDETAIL: %s\nWHERE: %s", round, err, pe.Detail, pe.Where)
					} else {
						t.Errorf("round %d: %v", round, err)
					}
				}
			}()
		}
		wg.Wait()
		if got := e.wokeTasks()[parent.ID]; got != 1 {
			t.Fatalf("round %d: pai acordado %d vezes, esperado exatamente 1", round, got)
		}
	}
}

func TestRemoveDependencyFreesTheTask(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	a := e.task(t, service.CreateTaskInput{Title: "A"})
	b := e.task(t, service.CreateTaskInput{Title: "B", PersonaID: &p.ID, DependsOn: []domain.ID{a.ID}})
	e.jobs.reset()
	bv, err := e.svc.RemoveTaskDependency(ctx, domain.HumanActor, e.co.ID, b.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bv.Blocked || e.wokeTasks()[b.ID] != 1 {
		t.Fatalf("remover a última dependência aberta libera e acorda: %+v woke=%v", bv.TaskCoord, e.wokeTasks())
	}
	_, err = e.svc.RemoveTaskDependency(ctx, domain.HumanActor, e.co.ID, b.ID, a.ID)
	wantErr(t, err, domain.ErrNotFound)

	// remover uma dependência já fechada não gera `unblocked`
	c := e.task(t, service.CreateTaskInput{Title: "C"})
	d := e.task(t, service.CreateTaskInput{Title: "D", PersonaID: &p.ID, DependsOn: []domain.ID{c.ID}})
	e.closeAs(t, domain.HumanActor, c, domain.TaskDone)
	e.jobs.reset()
	if _, err := e.svc.RemoveTaskDependency(ctx, domain.HumanActor, e.co.ID, d.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if e.wokeTasks()[d.ID] != 0 {
		t.Fatalf("d já tinha sido liberada: %v", e.wokeTasks())
	}
}

func TestPersonaOnlyEditsDependenciesOfItsOwnTasks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p1 := e.persona(t, "Ana", nil)
	p2 := e.persona(t, "Bia", nil)
	own := e.task(t, service.CreateTaskInput{Title: "minha", PersonaID: &p1.ID})
	foreign := e.task(t, service.CreateTaskInput{Title: "alheia", PersonaID: &p2.ID})
	x := e.task(t, service.CreateTaskInput{Title: "x"})
	if _, err := e.svc.AddTaskDependency(ctx, domain.PersonaActor(p1.ID), e.co.ID, own.ID, x.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.AddTaskDependency(ctx, domain.PersonaActor(p1.ID), e.co.ID, foreign.ID, x.ID)
	wantErr(t, err, domain.ErrNotAllowed)
}

// Propriedade (banco): um grafo aleatório montado pelo service nunca tem ciclo; toda aresta recusada é de fato
// alcançável no sentido oposto; e fechar tasks livres em ordem aleatória acorda exatamente as que o modelo puro
// (domain.ReleasedBy) diz, até o grafo inteiro fechar.
func TestPropertyRandomGraphThroughTheService(t *testing.T) {
	for seed := int64(1); seed <= 3; seed++ {
		e := newEnv(t)
		ctx := context.Background()
		rng := rand.New(rand.NewSource(seed))
		p := e.persona(t, "Ana", nil)
		n := 14
		tasks := make([]service.TaskView, n)
		for i := range tasks {
			tasks[i] = e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
		}
		edges := domain.DepEdges{}
		for i := 0; i < 160; i++ {
			a, b := tasks[rng.Intn(n)], tasks[rng.Intn(n)]
			_, err := e.svc.AddTaskDependency(ctx, domain.HumanActor, e.co.ID, a.ID, b.ID)
			switch {
			case err == nil:
				edges.Add(a.ID, b.ID)
			case a.ID == b.ID:
				wantErr(t, err, domain.ErrInvalid)
			default:
				wantErr(t, err, domain.ErrDependencyCycle)
				if !edges.Reaches(b.ID, a.ID) {
					t.Fatalf("seed %d: aresta recusada sem caminho oposto", seed)
				}
			}
		}
		rows, err := e.svc.Pool.Query(ctx, "SELECT task_id, depends_on_id FROM task_dependencies")
		if err != nil {
			t.Fatal(err)
		}
		stored := domain.DepEdges{}
		for rows.Next() {
			var x, y domain.ID
			if err := rows.Scan(&x, &y); err != nil {
				t.Fatal(err)
			}
			stored.Add(x, y)
		}
		rows.Close()
		if domain.HasCycle(stored) {
			t.Fatalf("seed %d: o banco guardou um ciclo", seed)
		}

		status := map[domain.ID]domain.TaskStatus{}
		byID := map[domain.ID]service.TaskView{}
		for _, tv := range tasks {
			status[tv.ID] = domain.TaskTodo
			byID[tv.ID] = tv
		}
		for closed := 0; closed < n; closed++ {
			var free []domain.ID
			for _, tv := range tasks {
				if status[tv.ID].Open() && domain.OpenDependencies(stored, status, tv.ID) == 0 {
					free = append(free, tv.ID)
				}
			}
			if len(free) == 0 {
				t.Fatalf("seed %d: travou com %d abertas", seed, n-closed)
			}
			sort.Slice(free, func(i, j int) bool { return free[i].String() < free[j].String() })
			pick := free[rng.Intn(len(free))]
			st := domain.TaskDone
			if rng.Intn(4) == 0 {
				st = domain.TaskCancelled
			}
			e.jobs.reset()
			e.closeAs(t, domain.HumanActor, byID[pick], st)
			status[pick] = st
			want := domain.ReleasedBy(stored, status, pick)
			woke := e.wokeTasks()
			if len(woke) != len(want) {
				t.Fatalf("seed %d: acordou %d tasks, esperado %d", seed, len(woke), len(want))
			}
			for _, w := range want {
				if woke[w] != 1 {
					t.Fatalf("seed %d: %v deveria ter sido acordada uma vez (%v)", seed, w, woke[w])
				}
			}
		}
	}
}

// ---------- árvore ----------

func TestParentWakesWhenLastChildCloses(t *testing.T) {
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	parent := e.task(t, service.CreateTaskInput{Title: "pai", PersonaID: &p.ID, Status: domain.TaskInProgress})
	c1 := e.task(t, service.CreateTaskInput{Title: "f1", ParentID: &parent.ID})
	c2 := e.task(t, service.CreateTaskInput{Title: "f2", ParentID: &parent.ID})
	if v := e.get(t, parent); v.ChildrenTotal != 2 || v.ChildrenOpen != 2 {
		t.Fatalf("filhos: %+v", v.TaskCoord)
	}
	e.jobs.reset()
	e.closeAs(t, domain.HumanActor, c1, domain.TaskDone)
	if e.wokeTasks()[parent.ID] != 0 {
		t.Fatal("ainda há f2 aberta")
	}
	e.closeAs(t, domain.HumanActor, c2, domain.TaskCancelled)
	if e.wokeTasks()[parent.ID] != 1 || !has(e.verbs(t, parent), "task.children_done") {
		t.Fatalf("o pai acorda quando o último filho fecha: woke=%v verbs=%v", e.wokeTasks(), e.verbs(t, parent))
	}
	// pai já fechado não acorda
	e.closeAs(t, domain.HumanActor, parent, domain.TaskDone)
	c3 := e.task(t, service.CreateTaskInput{Title: "f3", ParentID: &parent.ID})
	e.jobs.reset()
	e.closeAs(t, domain.HumanActor, c3, domain.TaskDone)
	if e.wokeTasks()[parent.ID] != 0 {
		t.Fatal("pai fechado não é acordado")
	}
}

func TestParentCannotCompleteWithOpenChild(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	parent := e.task(t, service.CreateTaskInput{Title: "pai", PersonaID: &p.ID})
	child := e.task(t, service.CreateTaskInput{Title: "filho", ParentID: &parent.ID})
	done := domain.TaskDone
	_, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, parent.ID, service.UpdateTaskInput{Status: &done})
	if !errors.Is(err, domain.ErrChildrenOpen) || !strings.Contains(err.Error(), child.Key) {
		t.Fatalf("pai com filho aberto: %v", err)
	}
	// a persona nunca força
	_, err = e.svc.UpdateTask(ctx, domain.PersonaActor(p.ID), e.co.ID, parent.ID, service.UpdateTaskInput{Status: &done, ForceClose: true, OverrideReason: "quero"})
	wantErr(t, err, domain.ErrNotAllowed)
	// humano: o motivo é obrigatório
	_, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, parent.ID, service.UpdateTaskInput{Status: &done, ForceClose: true})
	wantErr(t, err, domain.ErrInvalid)
	if got := e.get(t, parent); got.Status != domain.TaskTodo {
		t.Fatalf("nada mudou: %s", got.Status)
	}
	out, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, parent.ID, service.UpdateTaskInput{Status: &done, ForceClose: true, OverrideReason: "o filho virou outra frente"})
	if err != nil || out.Status != domain.TaskDone {
		t.Fatalf("override humano com motivo: %v %+v", err, out.Task)
	}
	evs, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "task", SubjectID: &parent.ID})
	var seen bool
	for _, ev := range evs {
		if ev.Verb == "task.close_override" && strings.Contains(string(ev.Data), "o filho virou outra frente") && ev.Actor.Type == domain.ActorHuman {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("task.close_override não foi registrado: %+v", evs)
	}
	// cancelar o pai é permitido mesmo com filho aberto
	p2 := e.task(t, service.CreateTaskInput{Title: "outro pai"})
	e.task(t, service.CreateTaskInput{Title: "filho", ParentID: &p2.ID})
	e.closeAs(t, domain.HumanActor, p2, domain.TaskCancelled)
	// fechados os filhos, concluir é normal
	p3 := e.task(t, service.CreateTaskInput{Title: "pai 3"})
	c3 := e.task(t, service.CreateTaskInput{Title: "f", ParentID: &p3.ID})
	e.closeAs(t, domain.HumanActor, c3, domain.TaskDone)
	e.closeAs(t, domain.HumanActor, p3, domain.TaskDone)
}

func TestReparentRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.task(t, service.CreateTaskInput{Title: "A"})
	b := e.task(t, service.CreateTaskInput{Title: "B", ParentID: &a.ID})
	c := e.task(t, service.CreateTaskInput{Title: "C", ParentID: &b.ID})
	upd := func(tv service.TaskView, in service.UpdateTaskInput) (service.TaskView, error) {
		return e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, in)
	}
	if _, err := upd(a, service.UpdateTaskInput{ParentID: &c.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("ciclo de pai: %v", err)
	}
	if _, err := upd(a, service.UpdateTaskInput{ParentID: &a.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("pai de si mesma: %v", err)
	}
	other, _ := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Beta"})
	ot, _ := e.svc.CreateTask(ctx, domain.HumanActor, other.ID, service.CreateTaskInput{Title: "alheia"})
	if _, err := upd(c, service.UpdateTaskInput{ParentID: &ot.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("pai de outra empresa: %v", err)
	}
	out, err := upd(c, service.UpdateTaskInput{ParentID: &a.ID})
	if err != nil || out.ParentID == nil || *out.ParentID != a.ID {
		t.Fatalf("reparentar: %v %+v", err, out.Task)
	}
	out, err = upd(c, service.UpdateTaskInput{ClearParent: true})
	if err != nil || out.ParentID != nil {
		t.Fatalf("virar raiz: %v %+v", err, out.Task)
	}
	if !has(e.verbs(t, c), "task.parent_changed") {
		t.Fatalf("fatos: %v", e.verbs(t, c))
	}
	_, err = upd(c, service.UpdateTaskInput{ParentID: &a.ID, ClearParent: true})
	wantErr(t, err, domain.ErrInvalid)

	// profundidade: 10 níveis cabem, o 11º não
	cur := e.task(t, service.CreateTaskInput{Title: "n1"})
	for lvl := 2; lvl <= domain.MaxTaskDepth; lvl++ {
		cur = e.task(t, service.CreateTaskInput{Title: "n", ParentID: &cur.ID})
	}
	_, err = e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "fundo demais", ParentID: &cur.ID})
	wantErr(t, err, domain.ErrInvalid)
	// reparentar uma subárvore para baixo também respeita o teto (conta o peso dos descendentes)
	root := e.task(t, service.CreateTaskInput{Title: "sub"})
	e.task(t, service.CreateTaskInput{Title: "sub-f", ParentID: &root.ID})
	_, err = upd(root, service.UpdateTaskInput{ParentID: &cur.ID})
	wantErr(t, err, domain.ErrInvalid)
}

func TestRelationsAndCounters(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	root := e.task(t, service.CreateTaskInput{Title: "raiz"})
	mid := e.task(t, service.CreateTaskInput{Title: "meio", ParentID: &root.ID})
	leaf := e.task(t, service.CreateTaskInput{Title: "folha", ParentID: &mid.ID})
	dep := e.task(t, service.CreateTaskInput{Title: "dep"})
	if _, err := e.svc.AddTaskDependency(ctx, domain.HumanActor, e.co.ID, leaf.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	rel, err := e.svc.TaskRelations(ctx, e.co.ID, leaf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rel.Ancestors) != 2 || rel.Ancestors[0].Key != root.Key || rel.Ancestors[1].Key != mid.Key {
		t.Fatalf("ancestrais (raiz primeiro): %+v", rel.Ancestors)
	}
	if len(rel.DependsOn) != 1 || rel.DependsOn[0].Key != dep.Key || rel.DependsOn[0].Closed {
		t.Fatalf("depende de: %+v", rel.DependsOn)
	}
	rel, _ = e.svc.TaskRelations(ctx, e.co.ID, mid.ID)
	if len(rel.Children) != 1 || rel.Children[0].Key != leaf.Key {
		t.Fatalf("filhos: %+v", rel.Children)
	}
	rel, _ = e.svc.TaskRelations(ctx, e.co.ID, dep.ID)
	if len(rel.Dependents) != 1 || rel.Dependents[0].Key != leaf.Key {
		t.Fatalf("dependentes: %+v", rel.Dependents)
	}
	// a lista traz os contadores derivados
	list, err := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tv := range list {
		switch tv.ID {
		case leaf.ID:
			if !tv.Blocked {
				t.Errorf("folha deveria estar bloqueada na lista")
			}
		case mid.ID:
			if tv.ChildrenTotal != 1 {
				t.Errorf("meio: %+v", tv.TaskCoord)
			}
		}
	}
	if got, err := e.svc.GetTask(ctx, e.co.ID, leaf.Key); err != nil || !got.Blocked {
		t.Fatalf("GetTask por chave traz os derivados: %v %+v", err, got.TaskCoord)
	}
}

// ---------- pausa ----------

func TestHoldIsInheritedAndRefusesDispatch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	root := e.task(t, service.CreateTaskInput{Title: "raiz"})
	kid := e.task(t, service.CreateTaskInput{Title: "filha", ParentID: &root.ID, PersonaID: &p.ID})
	grand := e.task(t, service.CreateTaskInput{Title: "neta", ParentID: &kid.ID, PersonaID: &p.ID})
	e.jobs.reset()

	rv, err := e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, root.ID, "congelamento do release")
	if err != nil {
		t.Fatal(err)
	}
	if rv.Hold == nil || rv.Hold.Inherited || rv.Hold.OriginKey != root.Key {
		t.Fatalf("hold da raiz: %+v", rv.Hold)
	}
	for _, tv := range []service.TaskView{kid, grand} {
		got := e.get(t, tv)
		if got.Hold == nil || !got.Hold.Inherited || got.Hold.OriginKey != root.Key || got.Hold.Reason != "congelamento do release" || got.Hold.By.Type != domain.ActorHuman {
			t.Fatalf("%s deveria herdar a pausa (calculada): %+v", tv.Key, got.Hold)
		}
	}
	if e.count(t, "SELECT count(*) FROM task_holds") != 1 {
		t.Fatal("só a raiz da pausa tem linha")
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, grand, domain.TriggerAssign)); !errors.Is(err, domain.ErrTaskHeld) || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("despacho de task pausada: %v", err)
	}
	if e.count(t, "SELECT count(*) FROM runs") != 0 {
		t.Fatal("nenhuma run pode abrir")
	}
	if e.count(t, "SELECT count(*) FROM held_wakes WHERE task_id = $1", grand.ID) != 1 {
		t.Fatal("o wake recusado fica guardado para o retomar")
	}
	if err := e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, kid.ID); !errors.Is(err, domain.ErrTaskHeld) {
		t.Fatalf("wake manual: %v", err)
	}
	// um wake pedido durante a pausa (reatribuição) não vai para a fila: fica guardado
	p2 := e.persona(t, "Bia", nil)
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, kid.ID, service.UpdateTaskInput{PersonaID: &p2.ID}); err != nil {
		t.Fatal(err)
	}
	if e.wokeTasks()[kid.ID] != 0 {
		t.Fatalf("wake de task pausada não entra na fila: %v", e.wokeTasks())
	}
	// a lista de pausas da Caixa mostra só as raízes
	held, err := e.svc.ListHeldTasks(ctx, e.co.ID)
	if err != nil || len(held) != 1 || held[0].Task.ID != root.ID || held[0].Hold.Reason == "" {
		t.Fatalf("raízes de pausa: %v %+v", err, held)
	}
	in, err := e.svc.Inbox(ctx, e.co.ID)
	if err != nil || len(in.HeldTasks) != 1 {
		t.Fatalf("Caixa: %v %+v", err, in.HeldTasks)
	}

	// retomar acorda os wakes pendentes (e só eles)
	e.jobs.reset()
	if _, err := e.svc.ResumeTask(ctx, domain.HumanActor, e.co.ID, root.ID); err != nil {
		t.Fatal(err)
	}
	woke := e.wokeTasks()
	if woke[grand.ID] != 1 || woke[kid.ID] != 1 || len(woke) != 2 {
		t.Fatalf("retomar acorda os wakes pendentes: %v", woke)
	}
	if e.count(t, "SELECT count(*) FROM held_wakes") != 0 || e.count(t, "SELECT count(*) FROM task_holds") != 0 {
		t.Fatal("retomar limpa a pausa e os wakes guardados")
	}
	if got := e.get(t, grand); got.Hold != nil {
		t.Fatalf("neta ainda pausada: %+v", got.Hold)
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, grand, domain.TriggerAssign)); err != nil {
		t.Fatalf("depois de retomada despacha: %v", err)
	}
	for _, v := range []string{"task.held", "task.resumed"} {
		if !has(e.verbs(t, root), v) {
			t.Errorf("sem o fato %s: %v", v, e.verbs(t, root))
		}
	}
}

func TestHoldCancelsQueuedRunsButLeavesRunningOnes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p1 := e.persona(t, "Ana", nil)
	p2 := e.persona(t, "Bia", nil)
	root := e.task(t, service.CreateTaskInput{Title: "raiz"})
	queued := e.task(t, service.CreateTaskInput{Title: "na fila", ParentID: &root.ID, PersonaID: &p1.ID})
	running := e.task(t, service.CreateTaskInput{Title: "rodando", ParentID: &root.ID, PersonaID: &p2.ID})
	qr, err := e.svc.CheckoutRun(ctx, wakeOf(p1, queued, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rr, err := e.svc.CheckoutRun(ctx, wakeOf(p2, running, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, rr.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, root.ID, "parar tudo"); err != nil {
		t.Fatal(err)
	}
	q, _ := e.svc.GetRun(ctx, e.co.ID, qr.ID)
	r, _ := e.svc.GetRun(ctx, e.co.ID, rr.ID)
	if q.Status != domain.RunCancelled {
		t.Fatalf("run na fila deveria ser cancelada: %s", q.Status)
	}
	if r.Status != domain.RunRunning {
		t.Fatalf("run em andamento termina sozinha: %s", r.Status)
	}
}

func TestHoldRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	root := e.task(t, service.CreateTaskInput{Title: "raiz"})
	kid := e.task(t, service.CreateTaskInput{Title: "filha", ParentID: &root.ID, PersonaID: &p.ID})

	_, err := e.svc.HoldTask(ctx, domain.PersonaActor(p.ID), e.co.ID, root.ID, "x")
	wantErr(t, err, domain.ErrNotAllowed)
	_, err = e.svc.ResumeTask(ctx, domain.PersonaActor(p.ID), e.co.ID, root.ID)
	wantErr(t, err, domain.ErrNotAllowed)
	_, err = e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, root.ID, "  ")
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, root.ID, strings.Repeat("x", domain.MaxHoldReason+1))
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.ResumeTask(ctx, domain.HumanActor, e.co.ID, root.ID)
	wantErr(t, err, domain.ErrInvalid) // não está pausada
	if _, err := e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, root.ID, "um"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, root.ID, "dois")
	wantErr(t, err, domain.ErrConflict)
	// retomar uma filha que só herda a pausa aponta para a origem
	_, err = e.svc.ResumeTask(ctx, domain.HumanActor, e.co.ID, kid.ID)
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), root.Key) {
		t.Fatalf("retomar herdada: %v", err)
	}
	// task fechada não se pausa
	closed := e.task(t, service.CreateTaskInput{Title: "fechada"})
	e.closeAs(t, domain.HumanActor, closed, domain.TaskDone)
	_, err = e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, closed.ID, "x")
	wantErr(t, err, domain.ErrInvalid)
}

// Retomar a raiz não acorda quem continua pausado por outra raiz.
func TestResumeKeepsWakesOfTasksHeldElsewhere(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	root := e.task(t, service.CreateTaskInput{Title: "raiz"})
	kid := e.task(t, service.CreateTaskInput{Title: "filha", ParentID: &root.ID, PersonaID: &p.ID})
	if _, err := e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, kid.ID, "só esta"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, root.ID, "tudo"); err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.CheckoutRun(ctx, wakeOf(p, kid, domain.TriggerAssign)) // recusado: guarda o wake
	e.jobs.reset()
	if _, err := e.svc.ResumeTask(ctx, domain.HumanActor, e.co.ID, root.ID); err != nil {
		t.Fatal(err)
	}
	if e.wokeTasks()[kid.ID] != 0 || e.count(t, "SELECT count(*) FROM held_wakes WHERE task_id = $1", kid.ID) != 1 {
		t.Fatalf("filha segue pausada: wake fica guardado (woke=%v)", e.wokeTasks())
	}
	e.jobs.reset()
	if _, err := e.svc.ResumeTask(ctx, domain.HumanActor, e.co.ID, kid.ID); err != nil {
		t.Fatal(err)
	}
	if e.wokeTasks()[kid.ID] != 1 {
		t.Fatalf("agora acorda: %v", e.wokeTasks())
	}
}

// Reparentar muda o efeito da pausa na hora (a herança é calculada, nunca copiada).
func TestHoldFollowsTheTree(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	held := e.task(t, service.CreateTaskInput{Title: "pausada"})
	free := e.task(t, service.CreateTaskInput{Title: "livre"})
	kid := e.task(t, service.CreateTaskInput{Title: "filha", ParentID: &free.ID})
	if _, err := e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, held.ID, "x"); err != nil {
		t.Fatal(err)
	}
	if e.get(t, kid).Hold != nil {
		t.Fatal("não herda de quem não é ancestral")
	}
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, kid.ID, service.UpdateTaskInput{ParentID: &held.ID}); err != nil {
		t.Fatal(err)
	}
	if h := e.get(t, kid).Hold; h == nil || !h.Inherited {
		t.Fatalf("passou a herdar: %+v", h)
	}
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, kid.ID, service.UpdateTaskInput{ClearParent: true}); err != nil {
		t.Fatal(err)
	}
	if e.get(t, kid).Hold != nil {
		t.Fatal("deixou de herdar")
	}
}

// ---------- política de revisão ----------

type policyEnv struct {
	*env
	author   domain.Persona
	reviewer domain.Persona
	project  domain.Project
	task     service.TaskView
	run      domain.Run
}

func newPolicyEnv(t *testing.T, requireOnPersona bool, mod func(*service.HirePersonaInput, domain.Persona)) *policyEnv {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	pe := &policyEnv{env: e}
	pe.reviewer = e.persona(t, "Revisor", func(in *service.HirePersonaInput) { in.Agent = "codex"; in.Model = "m-rev" })
	pe.author = e.persona(t, "Autora", func(in *service.HirePersonaInput) {
		in.Agent = "claude"
		in.Model = "m-aut"
		in.RequireReview = requireOnPersona
		if mod != nil {
			mod(in, pe.reviewer)
		}
	})
	pr, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Proj"})
	if err != nil {
		t.Fatal(err)
	}
	pe.project = pr
	pe.task = e.task(t, service.CreateTaskInput{Title: "entrega", PersonaID: &pe.author.ID, ProjectID: &pr.ID})
	pe.run, err = e.svc.CheckoutRun(ctx, wakeOf(pe.author, pe.task, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, pe.run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	return pe
}

func (pe *policyEnv) personaCloses(t *testing.T) service.TaskView {
	t.Helper()
	return pe.closeAs(t, domain.PersonaActor(pe.author.ID), pe.task, domain.TaskDone)
}

func (pe *policyEnv) pendingGates(t *testing.T) int {
	t.Helper()
	gs, err := pe.svc.ListGates(context.Background(), pe.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if err != nil {
		t.Fatal(err)
	}
	return len(gs)
}

func (pe *policyEnv) origin(t *testing.T) *domain.RunPolicyOrigin {
	t.Helper()
	rv, err := pe.svc.GetRun(context.Background(), pe.co.ID, pe.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rv.PolicyOrigin
}

func TestReviewPolicyTaskOverridesPersona(t *testing.T) {
	ctx := context.Background()
	// persona exige; a task dispensa → a persona fecha direto
	pe := newPolicyEnv(t, true, nil)
	if o := pe.origin(t); o == nil || !o.ReviewRequired || o.ReviewRequiredSource != domain.ReviewLayerPersona {
		t.Fatalf("origem gravada no despacho: %+v", o)
	}
	no := false
	if _, err := pe.svc.SetTaskReviewPolicy(ctx, domain.HumanActor, pe.co.ID, pe.task.ID, domain.ReviewOverride{RequireReview: &no}); err != nil {
		t.Fatal(err)
	}
	if got := pe.personaCloses(t); got.Status != domain.TaskDone {
		t.Fatalf("a task dispensou a revisão: status %s", got.Status)
	}
	if pe.pendingGates(t) != 0 {
		t.Fatal("sem gate")
	}
	if o := pe.origin(t); o == nil || o.ReviewRequired || o.ReviewRequiredSource != domain.ReviewLayerTask {
		t.Fatalf("a origem da run passa a ser a task: %+v", o)
	}

	// persona dispensa; a task exige → in_review + gate
	pe2 := newPolicyEnv(t, false, nil)
	yes := true
	if _, err := pe2.svc.SetTaskReviewPolicy(ctx, domain.HumanActor, pe2.co.ID, pe2.task.ID, domain.ReviewOverride{RequireReview: &yes}); err != nil {
		t.Fatal(err)
	}
	if got := pe2.personaCloses(t); got.Status != domain.TaskInReview {
		t.Fatalf("a task exigiu a revisão: status %s", got.Status)
	}
	if pe2.pendingGates(t) != 1 {
		t.Fatal("o gate task_review deveria estar pendente")
	}
	if o := pe2.origin(t); o == nil || !o.ReviewRequired || o.ReviewRequiredSource != domain.ReviewLayerTask {
		t.Fatalf("origem: %+v", o)
	}
}

func TestReviewPolicyProjectLayerAndPrecedence(t *testing.T) {
	ctx := context.Background()
	pe := newPolicyEnv(t, false, nil)
	yes, no := true, false
	if _, err := pe.svc.SetProjectReviewPolicy(ctx, domain.HumanActor, pe.co.ID, pe.project.ID, domain.ReviewOverride{RequireReview: &yes}); err != nil {
		t.Fatal(err)
	}
	view, err := pe.svc.TaskReviewPolicy(ctx, pe.co.ID, pe.task.ID)
	if err != nil || !view.Effective.Required || view.Effective.RequiredSource != domain.ReviewLayerProject {
		t.Fatalf("projeto exige: %v %+v", err, view.Effective)
	}
	// a task vence o projeto
	if _, err := pe.svc.SetTaskReviewPolicy(ctx, domain.HumanActor, pe.co.ID, pe.task.ID, domain.ReviewOverride{RequireReview: &no}); err != nil {
		t.Fatal(err)
	}
	view, _ = pe.svc.TaskReviewPolicy(ctx, pe.co.ID, pe.task.ID)
	if view.Effective.Required || view.Effective.RequiredSource != domain.ReviewLayerTask {
		t.Fatalf("task dispensa: %+v", view.Effective)
	}
	// limpar a sobreposição da task devolve ao projeto
	if _, err := pe.svc.SetTaskReviewPolicy(ctx, domain.HumanActor, pe.co.ID, pe.task.ID, domain.ReviewOverride{}); err != nil {
		t.Fatal(err)
	}
	if got := pe.personaCloses(t); got.Status != domain.TaskInReview {
		t.Fatalf("o projeto exige: %s", got.Status)
	}
	if o := pe.origin(t); o == nil || o.ReviewRequiredSource != domain.ReviewLayerProject {
		t.Fatalf("origem projeto: %+v", o)
	}
	got, err := pe.svc.ProjectReviewPolicy(ctx, pe.co.ID, pe.project.ID)
	if err != nil || got.RequireReview == nil || !*got.RequireReview {
		t.Fatalf("leitura da política do projeto: %v %+v", err, got)
	}
}

func TestReviewPolicyReviewerOverride(t *testing.T) {
	ctx := context.Background()
	// a persona exige e não tem revisor: a task escolhe uma persona revisora → revisão automática
	pe := newPolicyEnv(t, true, nil)
	if _, err := pe.svc.SetTaskReviewPolicy(ctx, domain.HumanActor, pe.co.ID, pe.task.ID,
		domain.ReviewOverride{ReviewerMode: domain.ReviewerCustom, ReviewerPersonaID: &pe.reviewer.ID}); err != nil {
		t.Fatal(err)
	}
	if got := pe.personaCloses(t); got.Status != domain.TaskInReview {
		t.Fatalf("status %s", got.Status)
	}
	pe.jobs.mu.Lock()
	reviews := len(pe.jobs.reviews)
	pe.jobs.mu.Unlock()
	if reviews != 1 || pe.pendingGates(t) != 0 {
		t.Fatalf("a revisão automática vem antes do gate: reviews=%d gates=%d", reviews, pe.pendingGates(t))
	}
	if e := pe.count(t, "SELECT count(*) FROM reviews WHERE reviewer_persona_id = $1", pe.reviewer.ID); e != 1 {
		t.Fatalf("a revisão deveria apontar para o revisor da task: %d", e)
	}
	if o := pe.origin(t); o == nil || o.ReviewerSource != domain.ReviewLayerTask {
		t.Fatalf("origem do revisor: %+v", o)
	}

	// a persona tem revisor; a task manda direto ao gate humano
	pe2 := newPolicyEnv(t, true, func(in *service.HirePersonaInput, rev domain.Persona) { in.ReviewerPersona = &rev.ID })
	if _, err := pe2.svc.SetTaskReviewPolicy(ctx, domain.HumanActor, pe2.co.ID, pe2.task.ID, domain.ReviewOverride{ReviewerMode: domain.ReviewerNone}); err != nil {
		t.Fatal(err)
	}
	pe2.personaCloses(t)
	pe2.jobs.mu.Lock()
	reviews = len(pe2.jobs.reviews)
	pe2.jobs.mu.Unlock()
	if reviews != 0 || pe2.pendingGates(t) != 1 {
		t.Fatalf("sem revisor automático: gate direto (reviews=%d gates=%d)", reviews, pe2.pendingGates(t))
	}

	// revisor por harness+modelo na política do projeto
	pe3 := newPolicyEnv(t, true, nil)
	if _, err := pe3.svc.SetProjectReviewPolicy(ctx, domain.HumanActor, pe3.co.ID, pe3.project.ID,
		domain.ReviewOverride{ReviewerMode: domain.ReviewerCustom, ReviewerAgent: "codex", ReviewerModel: "m-x"}); err != nil {
		t.Fatal(err)
	}
	pe3.personaCloses(t)
	if e := pe3.count(t, "SELECT count(*) FROM reviews WHERE agent = 'codex' AND model = 'm-x' AND reviewer_persona_id IS NULL"); e != 1 {
		t.Fatalf("revisão do projeto por harness: %d", e)
	}
	if o := pe3.origin(t); o == nil || o.ReviewerSource != domain.ReviewLayerProject {
		t.Fatalf("origem do revisor: %+v", o)
	}
}

func TestReviewPolicyAuthorizationAndValidation(t *testing.T) {
	ctx := context.Background()
	pe := newPolicyEnv(t, false, nil)
	yes := true
	_, err := pe.svc.SetTaskReviewPolicy(ctx, domain.PersonaActor(pe.author.ID), pe.co.ID, pe.task.ID, domain.ReviewOverride{RequireReview: &yes})
	wantErr(t, err, domain.ErrNotAllowed)
	_, err = pe.svc.SetProjectReviewPolicy(ctx, domain.PersonaActor(pe.author.ID), pe.co.ID, pe.project.ID, domain.ReviewOverride{RequireReview: &yes})
	wantErr(t, err, domain.ErrNotAllowed)
	for name, o := range map[string]domain.ReviewOverride{
		"custom vazio":         {ReviewerMode: domain.ReviewerCustom},
		"persona e harness":    {ReviewerMode: domain.ReviewerCustom, ReviewerPersonaID: &pe.reviewer.ID, ReviewerAgent: "codex"},
		"persona inexistente":  {ReviewerMode: domain.ReviewerCustom, ReviewerPersonaID: ptr(domain.NewID())},
		"harness desconhecido": {ReviewerMode: domain.ReviewerCustom, ReviewerAgent: "nenhum"},
	} {
		if _, err := pe.svc.SetTaskReviewPolicy(ctx, domain.HumanActor, pe.co.ID, pe.task.ID, o); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err = pe.svc.SetTaskReviewPolicy(ctx, domain.HumanActor, pe.co.ID, domain.NewID(), domain.ReviewOverride{})
	wantErr(t, err, domain.ErrNotFound)
	// sem sobreposição nenhuma o comportamento é o de sempre: a persona decide
	if got := pe.personaCloses(t); got.Status != domain.TaskDone {
		t.Fatalf("persona sem exigência fecha direto: %s", got.Status)
	}
}
