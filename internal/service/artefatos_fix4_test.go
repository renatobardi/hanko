package service_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// Correções da revisão de Artefatos + Metas (ADR-0028).

// Uploads simultâneos de nomes diferentes (cadeias diferentes) não furam a cota da empresa.
func TestArtifactQuotaConcurrentUploads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.ArtifactMaxBytes = 10
	e.svc.ArtifactQuotaBytes = 30
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{
				Name: fmt.Sprintf("arq-%d.txt", i), Content: bytes.Repeat([]byte{byte('a' + i)}, 10)})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		if err == nil {
			ok++
		} else if !strings.Contains(err.Error(), "quota") {
			t.Fatalf("erro inesperado: %v", err)
		}
	}
	u, err := e.svc.GetArtifactUsage(ctx, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok != 3 || u.UsedBytes != 30 || u.Versions != 3 {
		t.Fatalf("%d uploads aceitos, uso %+v: a cota de 30 bytes só comporta 3 de 10", ok, u)
	}
}

// A meta da própria task entra no briefing mesmo quando o projeto tem metas demais com vencimento antes dela.
func TestGoalBriefingKeepsTaskGoal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	pr := e.project(t, "Crescimento", nil)
	for i := 0; i < 7; i++ {
		due := time.Date(2027, 1, 1+i, 0, 0, 0, 0, time.UTC)
		e.goal(t, func(in *service.CreateGoalInput) {
			in.Title = fmt.Sprintf("Meta do projeto %d", i)
			in.ProjectID = &pr.ID
			in.DueDate = &due
		})
	}
	mine := e.goal(t, func(in *service.CreateGoalInput) { in.Title = "Meta da task" }) // sem vencimento: iria por último
	tv := e.task(t, service.CreateTaskInput{Title: "trabalho", PersonaID: &p.ID, ProjectID: &pr.ID, GoalID: &mine.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Goals) != 5 {
		t.Fatalf("%d metas no briefing, quer 5", len(rc.Goals))
	}
	if !rc.Goals[0].Linked || rc.Goals[0].Slug != mine.Slug {
		t.Fatalf("a meta da task deveria vir primeiro: %+v", rc.Goals)
	}
}

// Nomes com caracteres de formatação (bidi, largura zero) são recusados.
func TestArtifactNameRejectsFormatChars(t *testing.T) {
	e := newEnv(t)
	for _, r := range []rune{0x202e, 0x200b, 0x2066, 0xfeff} {
		name := "a" + string(r) + "b.md"
		_, err := e.svc.PutArtifact(context.Background(), domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: name, Content: []byte("x")})
		wantErr(t, err, domain.ErrInvalid)
	}
	e.put(t, domain.HumanActor, service.PutArtifactInput{Name: "relatório-ação.md", Content: []byte("x")})
}

// Apagar a task apaga as versões dos artefatos dela, em vez de fundi-las à cadeia "sem task" do mesmo nome (que
// colidiria no índice único de versão).
func TestArtifactTaskDeleteCascades(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := service.New(pool, &fakeJobs{}, events.NewBroker())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme Corp"})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []service.PutArtifactInput{
		{Name: "plano.md", Content: []byte("solto 1")},
		{Name: "plano.md", Content: []byte("task 1"), TaskID: &tk.ID},
	} {
		if _, err := svc.PutArtifact(ctx, domain.HumanActor, co.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", tk.ID); err != nil {
		t.Fatalf("apagar a task: %v", err)
	}
	all, err := svc.ListArtifacts(ctx, co.ID, service.ArtifactFilter{AllVersions: true})
	if err != nil || len(all) != 1 || all[0].TaskID != nil || all[0].Version != 1 {
		t.Fatalf("depois de apagar a task sobra só a cadeia sem task: %+v %v", all, err)
	}
	var blobs int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM artifact_blobs").Scan(&blobs); err != nil || blobs != 1 {
		t.Fatalf("blobs órfãos: %d %v", blobs, err)
	}
}
