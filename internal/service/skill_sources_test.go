package service_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func resultWithSummary(s string) adapter.Result { return adapter.Result{Summary: s} }

func failedResult(msg string) adapter.Result { return adapter.Result{IsError: true, Error: msg, ExitCode: 1} }

// ---- versões e versão fixada ----

func TestSkillVersionsAndRestore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sk := mkSkill(t, e, "Revisar PR", "# v1")
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, sk.ID, service.UpdateSkillInput{Body: ptr("# v2")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, sk.ID, service.UpdateSkillInput{Description: ptr("nova descrição")}); err != nil {
		t.Fatal(err)
	}
	vs, err := e.svc.ListSkillVersions(ctx, e.co.ID, sk.ID)
	if err != nil || len(vs) != 3 || vs[0].Revision != 3 || vs[2].Revision != 1 || vs[0].Body != "" {
		t.Fatalf("versões: %+v %v", vs, err)
	}
	if len(vs[2].SHA256) != 64 || vs[2].SHA256 == vs[1].SHA256 {
		t.Fatalf("sha: %+v", vs)
	}
	v1, err := e.svc.GetSkillVersion(ctx, e.co.ID, sk.ID, 1)
	if err != nil || v1.Body != "# v1" || v1.Description != "d Revisar PR" {
		t.Fatalf("v1: %+v %v", v1, err)
	}
	_, err = e.svc.GetSkillVersion(ctx, e.co.ID, sk.ID, 9)
	wantErr(t, err, domain.ErrNotFound)

	// restaurar cria a versão 4 com o conteúdo da 1
	got, err := e.svc.RestoreSkillVersion(ctx, domain.HumanActor, e.co.ID, sk.ID, 1)
	if err != nil || got.Version != 4 || got.Body != "# v1" || got.Description != "d Revisar PR" {
		t.Fatalf("restaurar: %+v %v", got, err)
	}
	// restaurar o que já é o atual não muda nada
	again, err := e.svc.RestoreSkillVersion(ctx, domain.HumanActor, e.co.ID, sk.ID, 4)
	if err != nil || again.Version != 4 {
		t.Fatalf("restaurar a atual: %+v %v", again, err)
	}
	if n := countVerb(t, e, "skill.restored"); n != 1 {
		t.Fatalf("skill.restored = %d", n)
	}
	vs, _ = e.svc.ListSkillVersions(ctx, e.co.ID, sk.ID)
	if len(vs) != 4 {
		t.Fatalf("versões depois de restaurar: %d", len(vs))
	}
}

func TestSkillPinDeliveredToRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	sk := mkSkill(t, e, "Deploy", "# corpo v1")
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, sk.ID, service.UpdateSkillInput{Body: ptr("# corpo v2")}); err != nil {
		t.Fatal(err)
	}
	// fixar antes de ligar cria o vínculo desligado
	if err := e.svc.SetPersonaSkillPin(ctx, domain.HumanActor, e.co.ID, p.ID, sk.ID, ptr(1)); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.PersonaSkills(ctx, e.co.ID, p.ID); len(list) != 0 {
		t.Fatalf("fixar não deveria ligar: %+v", list)
	}
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, p.ID, sk.ID, true); err != nil {
		t.Fatal(err)
	}
	ex, err := e.svc.RunExtras(ctx, e.co.ID, p.ID)
	if err != nil || len(ex.Skills) != 1 || ex.Skills[0].Body != "# corpo v1" || ex.Skills[0].Version != 1 || !ex.Skills[0].Pinned {
		t.Fatalf("run com a versão fixada: %+v %v", ex.Skills, err)
	}
	got, err := e.svc.PersonaSkill(ctx, e.co.ID, p.ID, sk.Slug)
	if err != nil || got.Body != "# corpo v1" {
		t.Fatalf("hanko_skill_get com versão fixada: %+v %v", got, err)
	}
	links, _ := e.svc.ListPersonaSkills(ctx, e.co.ID, p.ID)
	if len(links) != 1 || links[0].PinnedRevision == nil || *links[0].PinnedRevision != 1 || links[0].Version != 2 {
		t.Fatalf("vínculo: %+v", links)
	}
	sp, _ := e.svc.ListSkillPersonas(ctx, e.co.ID, sk.ID)
	if len(sp) != 1 || sp[0].PinnedRevision == nil {
		t.Fatalf("personas da skill: %+v", sp)
	}
	// versão inexistente é inválida; soltar volta para a mais nova
	wantErr(t, e.svc.SetPersonaSkillPin(ctx, domain.HumanActor, e.co.ID, p.ID, sk.ID, ptr(7)), domain.ErrInvalid)
	if err := e.svc.SetPersonaSkillPin(ctx, domain.HumanActor, e.co.ID, p.ID, sk.ID, nil); err != nil {
		t.Fatal(err)
	}
	ex, _ = e.svc.RunExtras(ctx, e.co.ID, p.ID)
	if len(ex.Skills) != 1 || ex.Skills[0].Body != "# corpo v2" || ex.Skills[0].Pinned {
		t.Fatalf("sem fixação: %+v", ex.Skills)
	}
	if countVerb(t, e, "persona.skill_pinned") != 1 || countVerb(t, e, "persona.skill_unpinned") != 1 {
		t.Fatal("atividade de fixação")
	}
}

func TestSkillStudioRoles(t *testing.T) {
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	sk := mkSkill(t, e, "Deploy", "# corpo")
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin} {
		u := mkUser(t, e, string(r)+"@x.com", domain.RoleMember)
		join(t, e, u, r)
		users[r] = u
	}
	outsider := mkUser(t, e, "fora@x.com", domain.RoleMember)
	writes := map[string]func(ctx context.Context, u domain.User) error{
		"Restore": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.RestoreSkillVersion(ctx, u.Actor(), e.co.ID, sk.ID, 1)
			return err
		},
		"Pin": func(ctx context.Context, u domain.User) error {
			return e.svc.SetPersonaSkillPin(ctx, u.Actor(), e.co.ID, p.ID, sk.ID, ptr(1))
		},
		"Input": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.CreateSkillTestInput(ctx, u.Actor(), e.co.ID, sk.ID, "entrada "+u.Email, "oi")
			return err
		},
		"RunTest": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.RunSkillTest(ctx, u.Actor(), e.co.ID, sk.ID, service.RunSkillTestInput{PersonaID: p.ID, Input: "oi"})
			return err
		},
		"Discover": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.DiscoverSkillSource(ctx, e.co.ID, service.SkillSourceInput{URL: "https://example.com/r.git"})
			if errors.Is(err, domain.ErrInvalid) { // passou da barreira de papel (fontes desligadas aqui)
				return nil
			}
			return err
		},
	}
	for name, run := range writes {
		for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember} {
			wantErr(t, run(as(users[r]), users[r]), domain.ErrForbidden)
		}
		if err := run(as(users[domain.MemberAdmin]), users[domain.MemberAdmin]); err != nil {
			t.Errorf("%s como admin: %v", name, err)
		}
		wantErr(t, run(as(outsider), outsider), domain.ErrNotFound)
	}
	// leitura é de qualquer membro
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember} {
		if _, err := e.svc.ListSkillVersions(as(users[r]), e.co.ID, sk.ID); err != nil {
			t.Errorf("versões como %s: %v", r, err)
		}
		if _, err := e.svc.ListSkillTestRuns(as(users[r]), e.co.ID, &sk.ID, 0); err != nil {
			t.Errorf("testes como %s: %v", r, err)
		}
		if _, err := e.svc.ListSkillSources(as(users[r]), e.co.ID); err != nil {
			t.Errorf("fontes como %s: %v", r, err)
		}
	}
}

// ---- estúdio ----

func TestSkillStudioRunLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	other := mkSkill(t, e, "Outra", "# outra")
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, p.ID, other.ID, true); err != nil {
		t.Fatal(err)
	}
	sk := mkSkill(t, e, "Deploy", "# corpo v1")
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, sk.ID, service.UpdateSkillInput{Body: ptr("# corpo v2")}); err != nil {
		t.Fatal(err)
	}
	in, err := e.svc.CreateSkillTestInput(ctx, domain.HumanActor, e.co.ID, sk.ID, "pedido simples", "Faça o deploy do serviço X")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateSkillTestInput(ctx, domain.HumanActor, e.co.ID, sk.ID, "pedido simples", "outra")
	wantErr(t, err, domain.ErrConflict)
	_, err = e.svc.CreateSkillTestInput(ctx, domain.HumanActor, e.co.ID, sk.ID, "grande", strings.Repeat("x", domain.MaxSkillTestInputBytes+1))
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.RunSkillTest(ctx, domain.HumanActor, e.co.ID, sk.ID, service.RunSkillTestInput{PersonaID: p.ID, Input: "x", InputID: &in.ID})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.RunSkillTest(ctx, domain.HumanActor, e.co.ID, sk.ID, service.RunSkillTestInput{PersonaID: p.ID, Input: "x", Revision: ptr(9)})
	wantErr(t, err, domain.ErrInvalid)

	e.jobs.reset()
	tr, err := e.svc.RunSkillTest(ctx, domain.HumanActor, e.co.ID, sk.ID, service.RunSkillTestInput{PersonaID: p.ID, InputID: &in.ID, Revision: ptr(1)})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != domain.SkillTestQueued || tr.SkillRevision != 1 || tr.Input != "Faça o deploy do serviço X" || tr.TaskID == nil {
		t.Fatalf("teste: %+v", tr)
	}
	// a task de sistema acorda a persona com o gatilho skill_test e não vira memória
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].Trigger != domain.TriggerSkillTest || e.jobs.outbox != nil {
		t.Fatalf("wakes %+v outbox %v", e.jobs.wakes, e.jobs.outbox)
	}
	// acordar à mão é recusado
	wantErr(t, e.svc.WakeTask(ctx, domain.HumanActor, e.co.ID, *tr.TaskID), domain.ErrInvalid)

	// a rede de segurança do heartbeat acorda com outro gatilho: a run continua sendo de teste
	run, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: *tr.TaskID, Trigger: domain.TriggerHeartbeat})
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger != domain.TriggerSkillTest {
		t.Fatalf("gatilho da run = %s", run.Trigger)
	}
	// o executor recebe só a skill testada, na versão do teste
	spec, err := e.svc.SkillTestRunSpec(ctx, e.co.ID, *tr.TaskID)
	if err != nil || spec.Skill.Slug != "deploy" || spec.Skill.Body != "# corpo v1" || spec.Skill.Version != 1 || spec.Input != tr.Input {
		t.Fatalf("spec: %+v %v", spec, err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.GetSkillTestRun(ctx, e.co.ID, tr.ID); got.Status != domain.SkillTestRunning || got.RunID == nil {
		t.Fatalf("ao vivo: %+v", got)
	}
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone,
		Result: resultWithSummary("## Saída\n\nDeploy feito.")}); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.GetSkillTestRun(ctx, e.co.ID, tr.ID)
	if err != nil || got.Status != domain.SkillTestDone || got.Output != "## Saída\n\nDeploy feito." || got.RunID == nil || *got.RunID != run.ID || got.FinishedAt == nil {
		t.Fatalf("fim: %+v %v", got, err)
	}
	tv, _ := e.svc.GetTask(ctx, e.co.ID, tr.TaskID.String())
	if tv.Status != domain.TaskDone {
		t.Fatalf("task de sistema = %s", tv.Status)
	}

	// reexecutar fixa a mesma versão, mesmo com uma versão nova da skill
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, sk.ID, service.UpdateSkillInput{Body: ptr("# corpo v3")}); err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.RerunSkillTest(ctx, domain.HumanActor, e.co.ID, tr.ID)
	if err != nil || again.SkillRevision != 1 || again.ID == tr.ID || again.Input != tr.Input {
		t.Fatalf("reexecutar: %+v %v", again, err)
	}
	// cancelar antes da run: o teste e a task fecham
	c, err := e.svc.CancelSkillTest(ctx, domain.HumanActor, e.co.ID, again.ID)
	if err != nil || c.Status != domain.SkillTestCancelled {
		t.Fatalf("cancelar: %+v %v", c, err)
	}
	if tv, _ := e.svc.GetTask(ctx, e.co.ID, again.TaskID.String()); tv.Status != domain.TaskCancelled {
		t.Fatalf("task cancelada = %s", tv.Status)
	}
	// cancelar com a run na fila cancela a run
	third, _ := e.svc.RunSkillTest(ctx, domain.HumanActor, e.co.ID, sk.ID, service.RunSkillTestInput{PersonaID: p.ID, Input: "outra entrada"})
	r3, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: *third.TaskID, Trigger: domain.TriggerSkillTest})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := e.svc.CancelSkillTest(ctx, domain.HumanActor, e.co.ID, third.ID); err != nil || c.Status != domain.SkillTestCancelled {
		t.Fatalf("cancelar com run: %+v %v", c, err)
	}
	if rv, _ := e.svc.GetRun(ctx, e.co.ID, r3.ID); rv.Status != domain.RunCancelled {
		t.Fatalf("run = %s", rv.Status)
	}
	// falha da run vira falha do teste
	fourth, _ := e.svc.RunSkillTest(ctx, domain.HumanActor, e.co.ID, sk.ID, service.RunSkillTestInput{PersonaID: p.ID, Input: "x"})
	r4, _ := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: *fourth.TaskID, Trigger: domain.TriggerSkillTest})
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, r4.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishRun(ctx, e.co.ID, r4.ID, service.FinishRunInput{Status: domain.RunFailed, Result: failedResult("quebrou")}); err != nil {
		t.Fatal(err)
	}
	if g, _ := e.svc.GetSkillTestRun(ctx, e.co.ID, fourth.ID); g.Status != domain.SkillTestFailed || g.Error != "quebrou" {
		t.Fatalf("falha: %+v", g)
	}
	list, _ := e.svc.ListSkillTestRuns(ctx, e.co.ID, &sk.ID, 0)
	if len(list) != 4 || list[0].ID != fourth.ID {
		t.Fatalf("lista: %d", len(list))
	}
	if countVerb(t, e, "skill.test_started") != 4 || countVerb(t, e, "skill.test_finished") != 4 {
		t.Fatal("atividade dos testes")
	}
}

func TestSkillStudioStaleTests(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	sk := mkSkill(t, e, "Deploy", "# corpo")
	tr, err := e.svc.RunSkillTest(ctx, domain.HumanActor, e.co.ID, sk.ID, service.RunSkillTestInput{PersonaID: p.ID, Input: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.ResolveStaleSkillTests(ctx); err != nil || n != 0 {
		t.Fatalf("cedo demais: %d %v", n, err)
	}
	base := e.svc.Now
	e.svc.Now = func() time.Time { return base().Add(7 * time.Hour) }
	if n, err := e.svc.ResolveStaleSkillTests(ctx); err != nil || n != 1 {
		t.Fatalf("parado: %d %v", n, err)
	}
	if g, _ := e.svc.GetSkillTestRun(ctx, e.co.ID, tr.ID); g.Status != domain.SkillTestFailed || g.Error == "" {
		t.Fatalf("teste parado: %+v", g)
	}
}

// ---- fontes ----

// gitRepo cria um repositório local (main) com os arquivos (prefixo "x:" = executável) e devolve a URL file:// e
// uma função que grava um novo commit com mudanças (conteúdo vazio apaga o arquivo).
func gitRepo(t *testing.T, files map[string]string) (string, func(map[string]string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está no PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(fs map[string]string) {
		for p, c := range fs {
			full := filepath.Join(dir, filepath.FromSlash(p))
			if c == "" {
				_ = os.Remove(full)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			mode := os.FileMode(0o644)
			if strings.HasPrefix(c, "x:") {
				c, mode = c[2:], 0o755
			}
			if err := os.WriteFile(full, []byte(c), mode); err != nil {
				t.Fatal(err)
			}
		}
		run("add", "-A")
		run("commit", "-q", "--allow-empty", "-m", "c")
	}
	run("init", "-q", "-b", "main")
	write(files)
	return "file://" + dir, write
}

func sourcesEnv(t *testing.T) *env {
	e := newEnv(t)
	e.svc.SkillSources = service.SkillSourcesConfig{Enabled: true}
	service.AllowFileSkillSources(e.svc)
	return e
}

func skillMD(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
}

func TestSkillSourcesSwitchOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.svc.DiscoverSkillSource(ctx, e.co.ID, service.SkillSourceInput{URL: "https://example.com/r.git"})
	wantErr(t, err, domain.ErrInvalid)
	if !strings.Contains(err.Error(), "HANKO_SKILL_SOURCES") {
		t.Fatalf("motivo: %v", err)
	}
	_, _, err = e.svc.CreateSkillSource(ctx, domain.HumanActor, e.co.ID, service.CreateSkillSourceInput{SkillSourceInput: service.SkillSourceInput{URL: "https://example.com/r.git"}})
	wantErr(t, err, domain.ErrInvalid)
	if refs, err := e.svc.DueSkillSources(ctx, time.Now(), 0); err != nil || refs != nil {
		t.Fatalf("tick com fontes desligadas: %v %v", refs, err)
	}
	// em produção file:// nunca vale, mesmo ligado
	e.svc.SkillSources.Enabled = true
	_, err = e.svc.DiscoverSkillSource(ctx, e.co.ID, service.SkillSourceInput{URL: "file:///tmp/x"})
	wantErr(t, err, domain.ErrInvalid)
	for _, bad := range []string{"http://example.com/r", "https://u:p@example.com/r", "https://10.0.0.1/r"} {
		_, err = e.svc.DiscoverSkillSource(ctx, e.co.ID, service.SkillSourceInput{URL: bad})
		wantErr(t, err, domain.ErrInvalid)
	}
}

func TestSkillSourceDiscoverCreateSync(t *testing.T) {
	e := sourcesEnv(t)
	ctx := context.Background()
	local := mkSkill(t, e, "Local", "# local") // slug local, que a fonte não pode tomar
	url, commit := gitRepo(t, map[string]string{
		"skills/revisar/SKILL.md": skillMD("revisar-pr", "Use ao revisar PR", "# Passos v1"),
		"skills/deploy/SKILL.md":  skillMD("deploy", "Deploy", "# Deploy"),
		"skills/deploy/run.sh":    "x:#!/bin/sh\necho oi",
		"skills/local/SKILL.md":   skillMD("local", "conflito", "# x"),
		"skills/grande/SKILL.md":  skillMD("grande", "grande", strings.Repeat("a", domain.MaxSkillBodyBytes+10)),
		"README.md":               "# repo",
	})
	src := service.SkillSourceInput{URL: url, Ref: "main", PathPrefix: "skills"}
	d, err := e.svc.DiscoverSkillSource(ctx, e.co.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]service.DiscoveredSkill{}
	for _, s := range d.Skills {
		byPath[s.Path] = s
	}
	if len(d.Commit) != 40 || len(byPath) != 4 || !byPath["skills/deploy"].HasScripts || !byPath["skills/local"].Conflict ||
		byPath["skills/grande"].Error == "" || byPath["skills/revisar"].Slug != "revisar-pr" {
		t.Fatalf("descoberta: %+v", d)
	}
	if list, _ := e.svc.ListSkills(ctx, e.co.ID); len(list) != 1 {
		t.Fatalf("a descoberta não grava: %d skills", len(list))
	}

	// cria importando só revisar e deploy
	v, res, err := e.svc.CreateSkillSource(ctx, domain.HumanActor, e.co.ID, service.CreateSkillSourceInput{SkillSourceInput: src,
		Select: []string{"skills/revisar", "skills/deploy", "skills/local"}})
	if err != nil || res.Error != "" {
		t.Fatalf("criar: %+v %v", res, err)
	}
	if res.Imported != 2 || v.Status != domain.SourceOK || v.LastCommit != d.Commit || v.NextSyncAt == nil || v.Imported != 2 {
		t.Fatalf("primeira sincronização: res=%+v fonte=%+v", res, v.SkillSource)
	}
	entries := map[string]domain.SkillSourceEntry{}
	for _, en := range v.Entries {
		entries[en.Path] = en
	}
	if entries["skills/grande"].Selection != domain.EntryExcluded || entries["skills/revisar"].SkillSlug != "revisar-pr" ||
		!entries["skills/deploy"].HasScripts || entries["skills/local"].Error == "" || entries["skills/local"].SkillID != nil {
		t.Fatalf("entradas: %+v", v.Entries)
	}
	rev, err := e.svc.GetSkill(ctx, e.co.ID, "revisar-pr")
	if err != nil || rev.SourceID == nil || *rev.SourceID != v.ID || rev.SourcePath != "skills/revisar" || rev.SourceCommit != d.Commit ||
		rev.Body != "# Passos v1" || rev.Description != "Use ao revisar PR" {
		t.Fatalf("skill importada: %+v %v", rev, err)
	}
	if got, _ := e.svc.GetSkill(ctx, e.co.ID, local.Slug); got.SourceID != nil {
		t.Fatal("a skill local não pode ser tomada pela fonte")
	}
	_, _, err = e.svc.CreateSkillSource(ctx, domain.HumanActor, e.co.ID, service.CreateSkillSourceInput{SkillSourceInput: src})
	wantErr(t, err, domain.ErrConflict)

	// sem mudança na fonte: nada é reimportado
	res, err = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, false)
	if err != nil || res.Unchanged != 2 || res.Updated != 0 || res.Imported != 0 {
		t.Fatalf("sync sem mudança: %+v %v", res, err)
	}

	// mudança na fonte: a skill sobe de versão, com o commit novo
	commit(map[string]string{"skills/revisar/SKILL.md": skillMD("revisar-pr", "Use ao revisar PR", "# Passos v2"),
		"skills/novidade/SKILL.md": skillMD("novidade", "novidade", "# novidade")})
	res, err = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, false)
	if err != nil || res.Updated != 1 || res.Imported != 0 {
		t.Fatalf("sync com mudança: %+v %v", res, err)
	}
	rev2, _ := e.svc.GetSkill(ctx, e.co.ID, "revisar-pr")
	if rev2.Body != "# Passos v2" || rev2.Version != 2 || rev2.SourceCommit == rev.SourceCommit {
		t.Fatalf("atualizada: %+v", rev2)
	}
	vs, _ := e.svc.ListSkillVersions(ctx, e.co.ID, rev2.ID)
	if len(vs) != 2 || vs[0].SourceCommit != rev2.SourceCommit {
		t.Fatalf("versões da fonte: %+v", vs)
	}
	// import_new desligado: a skill nova fica para revisão
	v, _ = e.svc.GetSkillSource(ctx, e.co.ID, v.ID)
	for _, en := range v.Entries {
		if en.Path == "skills/novidade" && (en.Selection != domain.EntryNew || en.SkillID != nil) {
			t.Fatalf("nova: %+v", en)
		}
	}
	// selecionar a nova a importa na sincronização seguinte (sem mudança de commit)
	if _, err := e.svc.UpdateSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateSkillSourceInput{
		Selection: map[string]domain.EntrySelection{"skills/novidade": domain.EntrySelected}}); err != nil {
		t.Fatal(err)
	}
	if res, err = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, false); err != nil || res.Imported != 1 {
		t.Fatalf("importar selecionada: %+v %v", res, err)
	}

	// editar aqui marca a skill como modificada; a fonte não a sobrescreve sem force
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, rev2.ID, service.UpdateSkillInput{Body: ptr("# editada aqui")}); err != nil {
		t.Fatal(err)
	}
	if k, _ := e.svc.GetSkill(ctx, e.co.ID, "revisar-pr"); !k.SourceModified {
		t.Fatal("deveria estar modificada localmente")
	}
	commit(map[string]string{"skills/revisar/SKILL.md": skillMD("revisar-pr", "Use ao revisar PR", "# Passos v3")})
	res, _ = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, false)
	if k, _ := e.svc.GetSkill(ctx, e.co.ID, "revisar-pr"); k.Body != "# editada aqui" || res.Skipped == 0 {
		t.Fatalf("sobrescreveu sem force: %+v %+v", k, res)
	}
	res, _ = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, true)
	if k, _ := e.svc.GetSkill(ctx, e.co.ID, "revisar-pr"); k.Body != "# Passos v3" || k.SourceModified || res.Updated == 0 {
		t.Fatalf("force: %+v %+v", k, res)
	}

	// excluir uma entrada importada solta a skill (a cópia fica)
	if _, err := e.svc.UpdateSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateSkillSourceInput{
		Selection: map[string]domain.EntrySelection{"skills/deploy": domain.EntryExcluded}}); err != nil {
		t.Fatal(err)
	}
	if k, _ := e.svc.GetSkill(ctx, e.co.ID, "deploy"); k.SourceID != nil {
		t.Fatalf("deploy deveria estar solta: %+v", k)
	}
	// apagar uma skill importada a tira da seleção (a próxima sincronização não a recria)
	nova, _ := e.svc.GetSkill(ctx, e.co.ID, "novidade")
	if err := e.svc.DeleteSkill(ctx, domain.HumanActor, e.co.ID, nova.ID); err != nil {
		t.Fatal(err)
	}
	if res, _ = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, true); res.Imported != 0 {
		t.Fatalf("recriou a skill apagada: %+v", res)
	}
	// desconectar mantendo as cópias
	if err := e.svc.DeleteSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, true); err != nil {
		t.Fatal(err)
	}
	if k, err := e.svc.GetSkill(ctx, e.co.ID, "revisar-pr"); err != nil || k.SourceID != nil {
		t.Fatalf("manter cópias: %+v %v", k, err)
	}
	for _, verb := range []string{"skill_source.created", "skill_source.synced", "skill_source.updated", "skill_source.deleted"} {
		if countVerb(t, e, verb) == 0 {
			t.Errorf("sem atividade %s", verb)
		}
	}
}

func TestSkillSourceDeleteSkillsAndImportNew(t *testing.T) {
	e := sourcesEnv(t)
	ctx := context.Background()
	url, commit := gitRepo(t, map[string]string{"a/SKILL.md": skillMD("a", "a", "# a")})
	v, res, err := e.svc.CreateSkillSource(ctx, domain.HumanActor, e.co.ID, service.CreateSkillSourceInput{
		SkillSourceInput: service.SkillSourceInput{URL: url}, ImportNew: true, AutoUpdate: ptr(false)})
	if err != nil || res.Imported != 1 || v.Status != domain.SourcePaused || v.NextSyncAt != nil {
		t.Fatalf("criar: %+v %+v %v", v.SkillSource, res, err)
	}
	commit(map[string]string{"b/SKILL.md": skillMD("b", "b", "# b")})
	if res, _ = e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, false); res.Imported != 1 {
		t.Fatalf("import_new: %+v", res)
	}
	// religar a atualização automática agenda a próxima
	v, err = e.svc.UpdateSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, service.UpdateSkillSourceInput{AutoUpdate: ptr(true)})
	if err != nil || v.NextSyncAt == nil || v.Status == domain.SourcePaused {
		t.Fatalf("religar: %+v %v", v.SkillSource, err)
	}
	if err := e.svc.DeleteSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, false); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.ListSkills(ctx, e.co.ID); len(list) != 0 {
		t.Fatalf("apagar com as skills: sobraram %d", len(list))
	}
}

func TestSkillSourceFailureBackoffAndTick(t *testing.T) {
	e := sourcesEnv(t)
	ctx := context.Background()
	url, _ := gitRepo(t, map[string]string{"a/SKILL.md": skillMD("a", "a", "# a")})
	v, _, err := e.svc.CreateSkillSource(ctx, domain.HumanActor, e.co.ID, service.CreateSkillSourceInput{SkillSourceInput: service.SkillSourceInput{URL: url}})
	if err != nil {
		t.Fatal(err)
	}
	// o repositório some: a sincronização falha, com backoff crescente
	if err := os.RemoveAll(strings.TrimPrefix(url, "file://")); err != nil {
		t.Fatal(err)
	}
	var prev time.Duration
	for i := 1; i <= 6; i++ {
		res, err := e.svc.SyncSkillSource(ctx, domain.HumanActor, e.co.ID, v.ID, false)
		if err != nil || res.Error == "" {
			t.Fatalf("falha %d: %+v %v", i, res, err)
		}
		got, _ := e.svc.GetSkillSource(ctx, e.co.ID, v.ID)
		if got.Status != domain.SourceError || got.FailureCount != i || got.NextSyncAt == nil {
			t.Fatalf("fonte depois da falha %d: %+v", i, got.SkillSource)
		}
		wait := got.NextSyncAt.Sub(*got.LastAttemptAt)
		want := domain.SkillSourceBackoff(i)
		if wait < want-time.Minute || wait > want+time.Minute || wait < prev-time.Second {
			t.Fatalf("backoff %d = %v, want ~%v", i, wait, want)
		}
		prev = wait
	}
	if domain.SkillSourceBackoff(1) != 5*time.Minute || domain.SkillSourceBackoff(5) != time.Hour || domain.SkillSourceBackoff(9) != time.Hour {
		t.Fatal("fórmula do backoff")
	}
	// o tick acha a fonte vencida
	due, err := e.svc.DueSkillSources(ctx, time.Now().Add(2*time.Hour), 0)
	if err != nil || len(due) != 1 || due[0].ID != v.ID {
		t.Fatalf("devidas: %+v %v", due, err)
	}
	if res, err := e.svc.SyncDueSkillSource(ctx, due[0]); err != nil || res.Error == "" {
		t.Fatalf("tick: %+v %v", res, err)
	}
	if countVerb(t, e, "skill_source.sync_failed") != 7 {
		t.Fatal("atividade das falhas")
	}
}
