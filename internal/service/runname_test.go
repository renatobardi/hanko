package service_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

var runNameRe = regexp.MustCompile(`^[\p{L}]+_[\p{L}]+$`)

// Toda run nasce com um nome Adjetivo_Substantivo derivado do id, único na empresa, e dá para
// achá-la por esse nome (sem caixa nem acento).
func TestRunNasceComNome(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	seen := map[string]domain.ID{}
	for i := 0; i < 25; i++ {
		tv := e.task(t, service.CreateTaskInput{PersonaID: &p.ID})
		run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
		if err != nil {
			t.Fatal(err)
		}
		if !runNameRe.MatchString(run.Name) {
			t.Fatalf("nome fora do formato Adjetivo_Substantivo: %q", run.Name)
		}
		derived := false
		for at := 0; at < domain.RunNameAttempts; at++ {
			derived = derived || run.Name == domain.RunName(run.ID, at)
		}
		if !derived {
			t.Fatalf("o nome %q não deriva do id %s", run.Name, run.ID)
		}
		if other, dup := seen[run.Name]; dup {
			t.Fatalf("nome %q repetido nas runs %s e %s", run.Name, other, run.ID)
		}
		seen[run.Name] = run.ID
		if _, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, run.ID); err != nil {
			t.Fatal(err)
		}
	}

	var name string
	var id domain.ID
	for n, i := range seen {
		name, id = n, i
		break
	}
	got, err := e.svc.GetRun(ctx, e.co.ID, id)
	if err != nil || got.Name != name {
		t.Fatalf("GetRun: %+v %v", got, err)
	}
	for _, q := range []string{name, strings.ToLower(name), strings.ToUpper(name)} {
		rs, err := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{Name: q})
		if err != nil || len(rs) != 1 || rs[0].ID != id {
			t.Fatalf("busca por %q: %+v %v", q, rs, err)
		}
	}
	if rs, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{Name: "Inexistente_Nome"}); len(rs) != 0 {
		t.Fatalf("nome inexistente: %+v", rs)
	}
}

// Backfill: runs de antes dos nomes ganham um, sem repetir o de outra run da empresa.
func TestBackfillRunNames(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{PersonaID: &p.ID})
	old1, old2, holder := domain.NewID(), domain.NewID(), domain.NewID()
	insert := func(id domain.ID, name string) {
		t.Helper()
		_, err := e.svc.Pool.Exec(ctx, `INSERT INTO runs (id, company_id, task_id, persona_id, status, trigger, agent, model, name, created_at, finished_at)
			VALUES ($1,$2,$3,$4,'done','manual','fake','m1',$5,$6,$6)`, id, e.co.ID, tv.ID, p.ID, name, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
	}
	// "holder" já tem o nome que old2 teria na primeira tentativa: old2 precisa ir para a seguinte
	insert(holder, domain.RunName(old2, 0))
	insert(old1, "")
	insert(old2, "")

	n, err := e.svc.BackfillRunNames(ctx)
	if err != nil || n != 2 {
		t.Fatalf("backfill = %d, %v", n, err)
	}
	r1, _ := e.svc.GetRun(ctx, e.co.ID, old1)
	r2, _ := e.svc.GetRun(ctx, e.co.ID, old2)
	if r1.Name != domain.RunName(old1, 0) {
		t.Fatalf("old1 = %q, quero %q", r1.Name, domain.RunName(old1, 0))
	}
	if r2.Name == domain.RunName(old2, 0) || r2.Name != domain.RunName(old2, 1) {
		t.Fatalf("old2 = %q deveria usar a tentativa 1 (%q)", r2.Name, domain.RunName(old2, 1))
	}
	// idempotente
	if n, err := e.svc.BackfillRunNames(ctx); err != nil || n != 0 {
		t.Fatalf("segunda passada = %d, %v", n, err)
	}
}

// O mesmo nome pode existir em empresas diferentes; só é único dentro da empresa.
func TestRunNameUnicoPorEmpresa(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	co2, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	p1 := e.persona(t, "Eng", nil)
	t1 := e.task(t, service.CreateTaskInput{PersonaID: &p1.ID})
	p2, err := e.svc.HirePersona(ctx, domain.HumanActor, co2.ID, service.HirePersonaInput{Name: "Eng", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := e.svc.CreateTask(ctx, domain.HumanActor, co2.ID, service.CreateTaskInput{Title: "x", PersonaID: &p2.ID})
	if err != nil {
		t.Fatal(err)
	}
	insert := func(co, task, persona domain.ID, name string) error {
		_, err := e.svc.Pool.Exec(ctx, `INSERT INTO runs (id, company_id, task_id, persona_id, status, trigger, agent, name, created_at)
			VALUES ($1,$2,$3,$4,'done','manual','fake',$5,now())`, domain.NewID(), co, task, persona, name)
		return err
	}
	if err := insert(e.co.ID, t1.ID, p1.ID, "Veloz_Raposa"); err != nil {
		t.Fatal(err)
	}
	if err := insert(co2.ID, t2.ID, p2.ID, "Veloz_Raposa"); err != nil {
		t.Fatalf("outra empresa pode repetir o nome: %v", err)
	}
	if err := insert(e.co.ID, t1.ID, p1.ID, "Veloz_Raposa"); err == nil {
		t.Fatal("o índice único deveria barrar o nome repetido na mesma empresa")
	}
	// nome vazio (runs antigas) não conta para a unicidade
	if err := insert(e.co.ID, t1.ID, p1.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := insert(e.co.ID, t1.ID, p1.ID, ""); err != nil {
		t.Fatalf("vários nomes vazios convivem: %v", err)
	}
}
