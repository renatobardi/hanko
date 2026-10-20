package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestProjectDefaultsInstructionsAndGoal(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	pr := e.project(t, "Loja", nil)
	other := e.project(t, "Blog", nil)
	g := e.goal(t, nil)
	p := e.persona(t, "Ana", nil)

	d, err := e.svc.GetProjectDefaults(bg, e.co.ID, pr.ID)
	if err != nil || d.Instructions != "" || d.GoalID != nil || d.ImportRepoSkills {
		t.Fatalf("padrões iniciais: %+v %v", d, err)
	}
	brief := "  Use Go 1.27. Teste tudo.  "
	d, err = e.svc.SetProjectDefaults(bg, domain.HumanActor, e.co.ID, pr.ID, service.SetProjectDefaultsInput{Instructions: &brief, GoalID: &g.ID})
	if err != nil {
		t.Fatal(err)
	}
	if d.Instructions != "Use Go 1.27. Teste tudo." || d.GoalID == nil || *d.GoalID != g.ID {
		t.Fatalf("padrões: %+v", d)
	}

	// meta padrão: a task nova do projeto herda; a de outro projeto não; a escolhida de propósito vence
	inherited := e.task(t, service.CreateTaskInput{Title: "a", ProjectID: &pr.ID})
	if inherited.GoalID == nil || *inherited.GoalID != g.ID {
		t.Fatalf("a task deveria herdar a meta padrão: %+v", inherited.GoalID)
	}
	if none := e.task(t, service.CreateTaskInput{Title: "b", ProjectID: &other.ID}); none.GoalID != nil {
		t.Fatalf("outro projeto não herda: %+v", none.GoalID)
	}
	mine := e.goal(t, func(in *service.CreateGoalInput) { in.Title = "Outra meta" })
	if chosen := e.task(t, service.CreateTaskInput{Title: "c", ProjectID: &pr.ID, GoalID: &mine.ID}); chosen.GoalID == nil || *chosen.GoalID != mine.ID {
		t.Fatalf("a meta escolhida vence: %+v", chosen.GoalID)
	}

	// o briefing do projeto chega no contexto da run
	tv := e.task(t, service.CreateTaskInput{Title: "d", ProjectID: &pr.ID, PersonaID: &p.ID})
	run, err := e.svc.CheckoutRun(bg, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.LoadRunContext(bg, e.co.ID, run.ID)
	if err != nil || rc.ProjectInstructions != "Use Go 1.27. Teste tudo." {
		t.Fatalf("contexto: %q %v", rc.ProjectInstructions, err)
	}

	// meta abandonada não impede criar tasks
	abandoned := domain.GoalAbandoned
	if _, err := e.svc.UpdateGoal(bg, domain.HumanActor, e.co.ID, g.ID, service.UpdateGoalInput{Status: &abandoned}); err != nil {
		t.Fatal(err)
	}
	if after := e.task(t, service.CreateTaskInput{Title: "e", ProjectID: &pr.ID}); after.GoalID != nil {
		t.Fatalf("meta encerrada não é herdada: %+v", after.GoalID)
	}

	// limpar a meta e o briefing
	empty := ""
	d, err = e.svc.SetProjectDefaults(bg, domain.HumanActor, e.co.ID, pr.ID, service.SetProjectDefaultsInput{Instructions: &empty, ClearGoal: true})
	if err != nil || d.Instructions != "" || d.GoalID != nil {
		t.Fatalf("limpar: %+v %v", d, err)
	}
	if !hasVerb(e.verbsOf(t, pr.ID), "project.defaults_updated") {
		t.Error("faltou project.defaults_updated")
	}
}

func (e *env) verbsOf(t *testing.T, subject domain.ID) []string {
	t.Helper()
	rows, err := e.svc.Pool.Query(context.Background(), `SELECT verb FROM activity_events WHERE company_id = $1 AND subject_id = $2 ORDER BY occurred_at, received_at`, e.co.ID, subject)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestProjectDefaultsValidationAndRoles(t *testing.T) {
	e := newEnv(t)
	e.svc.ExternalIdentity = true
	bg := context.Background()
	pr := e.project(t, "Loja", nil)
	big := strings.Repeat("x", domain.MaxProjectInstructions+1)
	if _, err := e.svc.SetProjectDefaults(bg, domain.HumanActor, e.co.ID, pr.ID, service.SetProjectDefaultsInput{Instructions: &big}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("instruções longas: %v", err)
	}
	bad := "../fora"
	if _, err := e.svc.SetProjectDefaults(bg, domain.HumanActor, e.co.ID, pr.ID, service.SetProjectDefaultsInput{SkillsPath: &bad}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("caminho de skills: %v", err)
	}
	ghost := domain.NewID()
	if _, err := e.svc.SetProjectDefaults(bg, domain.HumanActor, e.co.ID, pr.ID, service.SetProjectDefaultsInput{GoalID: &ghost}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("meta inexistente: %v", err)
	}
	if _, err := e.svc.SetProjectDefaults(bg, domain.HumanActor, e.co.ID, domain.NewID(), service.SetProjectDefaultsInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("projeto inexistente: %v", err)
	}
	member := mkUser(t, e, "m@x.com", domain.RoleMember)
	join(t, e, member, domain.MemberMember)
	txt := "oi"
	if _, err := e.svc.SetProjectDefaults(as(member), member.Actor(), e.co.ID, pr.ID, service.SetProjectDefaultsInput{Instructions: &txt}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro não define padrões: %v", err)
	}
	if _, err := e.svc.GetProjectDefaults(as(member), e.co.ID, pr.ID); err != nil {
		t.Fatalf("membro lê: %v", err)
	}
}

func TestRepoSkillsAutoImport(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	e.enableAndSync(t)
	pr := e.project(t, "Loja", func(in *service.CreateProjectInput) { in.WorkspaceID = &e.ws.ID })
	on := true
	if _, err := e.svc.SetProjectDefaults(bg, domain.HumanActor, e.co.ID, pr.ID, service.SetProjectDefaultsInput{ImportRepoSkills: &on}); err != nil {
		t.Fatal(err)
	}
	sk, err := e.svc.GetSkill(bg, e.co.ID, "revisar-codigo")
	if err != nil || sk.Name != "revisar-codigo" {
		t.Fatalf("a skill do repositório deveria ter sido importada: %+v %v", sk, err)
	}
	srcs, err := e.svc.ListSkillSources(bg, e.co.ID)
	if err != nil || len(srcs) != 1 || !strings.HasPrefix(srcs[0].URL, "workspace://") || srcs[0].AutoUpdate {
		t.Fatalf("fontes: %+v %v", srcs, err)
	}
	if !strings.Contains(srcs[0].Label, "repositório do workspace") {
		t.Fatalf("rótulo: %q", srcs[0].Label)
	}

	// uma skill nova no repositório entra na próxima sincronização do espelho
	writeFile(t, filepath.Join(e.origin, "skills", "doc", "SKILL.md"), "---\nname: escrever-doc\ndescription: Escreve a documentação\n---\n\nEscreva.\n")
	gitCmd(t, e.origin, "add", "-A")
	gitCmd(t, e.origin, "commit", "-q", "-m", "mais uma skill")
	if _, err := e.svc.SyncWorkspaceMirror(bg, domain.HumanActor, e.co.ID, e.ws.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSkill(bg, e.co.ID, "escrever-doc"); err != nil {
		t.Fatalf("a skill nova deveria ter entrado: %v", err)
	}
	if list, _ := e.svc.ListSkillSources(bg, e.co.ID); len(list) != 1 {
		t.Fatalf("a mesma fonte é reaproveitada: %d", len(list))
	}

	// restringir o caminho cria uma segunda fonte, só com o que está lá
	sub := "skills/doc"
	if _, err := e.svc.SetProjectDefaults(bg, domain.HumanActor, e.co.ID, pr.ID, service.SetProjectDefaultsInput{SkillsPath: &sub}); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.ListSkillSources(bg, e.co.ID); len(list) != 2 {
		t.Fatalf("fontes após restringir o caminho: %d", len(list))
	}
}

func TestSessionResetAppliesToNextRunOnly(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	p := e.persona(t, "Ana", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})

	run1, err := e.svc.CheckoutRun(bg, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	rc1, _ := e.svc.LoadRunContext(bg, e.co.ID, run1.ID)
	if rc1.SessionReset != nil {
		t.Fatalf("sem pedido, sem reset: %+v", rc1.SessionReset)
	}
	if _, err := e.svc.FinishRun(bg, e.co.ID, run1.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{SessionRef: "sess-1", Summary: "ok"}}); err != nil {
		t.Fatal(err)
	}

	reason := "o contexto ficou confuso"
	rst, err := e.svc.RequestTaskNewSession(bg, domain.HumanActor, e.co.ID, tv.ID, reason)
	if err != nil || rst.ConsumedRunID != nil {
		t.Fatalf("pedido: %+v %v", rst, err)
	}
	last, err := e.svc.LastSessionReset(bg, e.co.ID, tv.ID)
	if err != nil || last == nil || last.ID != rst.ID {
		t.Fatalf("último pedido: %+v %v", last, err)
	}
	// uma sessão iniciada antes do pedido não pode mais ser retomada
	if domain.SessionUsable(run1.CreatedAt, &last.CreatedAt) {
		t.Fatal("a sessão do run anterior não vale depois do pedido")
	}
	if !hasVerb(e.verbsOf(t, tv.ID), "task.session_reset") {
		t.Error("faltou task.session_reset")
	}

	run2, err := e.svc.CheckoutRun(bg, wakeOf(p, tv, domain.TriggerComment))
	if err != nil {
		t.Fatal(err)
	}
	rc2, err := e.svc.LoadRunContext(bg, e.co.ID, run2.ID)
	if err != nil || rc2.SessionReset == nil || rc2.SessionReset.Reason != reason || rc2.SessionReset.ConsumedRunID == nil || *rc2.SessionReset.ConsumedRunID != run2.ID {
		t.Fatalf("o próximo run aplica o pedido: %+v %v", rc2.SessionReset, err)
	}
	if !hasVerb(e.verbsOf(t, run2.ID), "run.session_reset_applied") {
		t.Error("faltou run.session_reset_applied na atividade do run")
	}
	if _, err := e.svc.FinishRun(bg, e.co.ID, run2.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{SessionRef: "sess-2", Summary: "ok"}}); err != nil {
		t.Fatal(err)
	}
	run3, err := e.svc.CheckoutRun(bg, wakeOf(p, tv, domain.TriggerComment))
	if err != nil {
		t.Fatal(err)
	}
	if rc3, _ := e.svc.LoadRunContext(bg, e.co.ID, run3.ID); rc3.SessionReset != nil {
		t.Fatalf("o pedido vale uma vez só: %+v", rc3.SessionReset)
	}
	if !domain.SessionUsable(run2.CreatedAt, &last.CreatedAt) {
		t.Fatal("a sessão iniciada depois do pedido vale")
	}
}

func TestSessionResetGuards(t *testing.T) {
	e := newEnv(t)
	e.svc.ExternalIdentity = true
	bg := context.Background()
	p := e.persona(t, "Ana", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	if _, err := e.svc.RequestTaskNewSession(bg, domain.PersonaActor(p.ID), e.co.ID, tv.ID, ""); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("persona não pede: %v", err)
	}
	if _, err := e.svc.RequestTaskNewSession(bg, domain.HumanActor, e.co.ID, tv.ID, strings.Repeat("x", domain.MaxResetReason+1)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("motivo longo: %v", err)
	}
	viewer := mkUser(t, e, "v@x.com", domain.RoleMember)
	join(t, e, viewer, domain.MemberViewer)
	if _, err := e.svc.RequestTaskNewSession(as(viewer), viewer.Actor(), e.co.ID, tv.ID, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("viewer não pede: %v", err)
	}
	done := domain.TaskDone
	if _, err := e.svc.UpdateTask(bg, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RequestTaskNewSession(bg, domain.HumanActor, e.co.ID, tv.ID, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("task encerrada: %v", err)
	}
	if last, err := e.svc.LastSessionReset(bg, e.co.ID, tv.ID); err != nil || last != nil {
		t.Fatalf("sem pedidos: %+v %v", last, err)
	}
}
