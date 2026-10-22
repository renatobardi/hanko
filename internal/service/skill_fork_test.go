package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestForkSkillCopiesContentAndKeepsHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	orig := mkSkill(t, e, "Revisar PR", "v1: leia o diff")
	body2 := "v2: leia o diff e rode os testes"
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, orig.ID, service.UpdateSkillInput{Body: &body2}); err != nil {
		t.Fatal(err)
	}
	ana := e.persona(t, "Ana", nil)
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, ana.ID, orig.ID, true); err != nil {
		t.Fatal(err)
	}

	// fork da revisão 1, com slug automático
	f, err := e.svc.ForkSkill(ctx, domain.HumanActor, e.co.ID, orig.ID, service.ForkSkillInput{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if f.Slug != "revisar-pr-fork" || f.Body != "v1: leia o diff" || f.Version != 1 || f.ForkedFromID == nil || *f.ForkedFromID != orig.ID || f.ForkedFromRevision != 1 || f.SourceID != nil {
		t.Fatalf("fork: %+v", f)
	}
	// o segundo fork pega o próximo slug livre; o original não muda
	f2, err := e.svc.ForkSkill(ctx, domain.HumanActor, e.co.ID, orig.ID, service.ForkSkillInput{})
	if err != nil || f2.Slug != "revisar-pr-fork-2" || f2.Body != body2 || f2.ForkedFromRevision != 2 {
		t.Fatalf("segundo fork: %+v %v", f2, err)
	}
	cur, _ := e.svc.GetSkill(ctx, e.co.ID, orig.ID.String())
	if cur.Version != 2 || cur.Body != body2 {
		t.Fatalf("o original foi tocado: %+v", cur)
	}
	// a cópia tem a primeira versão própria
	vs, err := e.svc.ListSkillVersions(ctx, e.co.ID, f.ID)
	if err != nil || len(vs) != 1 {
		t.Fatalf("versões da cópia: %+v %v", vs, err)
	}
	forks, err := e.svc.ListSkillForks(ctx, e.co.ID, orig.ID)
	if err != nil || len(forks) != 2 {
		t.Fatalf("forks: %+v %v", forks, err)
	}
	// slug explícito; slug repetido e reservado são recusados
	if _, err := e.svc.ForkSkill(ctx, domain.HumanActor, e.co.ID, orig.ID, service.ForkSkillInput{Slug: "meu-fork", Name: "Meu fork"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ForkSkill(ctx, domain.HumanActor, e.co.ID, orig.ID, service.ForkSkillInput{Slug: "meu-fork"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("slug repetido: %v", err)
	}
	if _, err := e.svc.ForkSkill(ctx, domain.HumanActor, e.co.ID, orig.ID, service.ForkSkillInput{Slug: "nova"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("slug reservado: %v", err)
	}
	if _, err := e.svc.ForkSkill(ctx, domain.HumanActor, e.co.ID, orig.ID, service.ForkSkillInput{Revision: 99}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revisão inexistente: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'skill.forked'`, e.co.ID); n != 3 {
		t.Fatalf("skill.forked = %d", n)
	}
}

func TestForkSkillMovesPersonas(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	orig := mkSkill(t, e, "Deploy", "rode o deploy")
	ana, bia := e.persona(t, "Ana", nil), e.persona(t, "Bia", nil)
	for _, p := range []domain.Persona{ana, bia} {
		if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, p.ID, orig.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	f, err := e.svc.ForkSkill(ctx, domain.HumanActor, e.co.ID, orig.ID, service.ForkSkillInput{MovePersonas: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []domain.Persona{ana, bia} {
		list, err := e.svc.PersonaSkills(ctx, e.co.ID, p.ID)
		if err != nil || len(list) != 1 || list[0].Slug != f.Slug {
			t.Fatalf("%s deveria ter só a cópia: %+v %v", p.Name, list, err)
		}
	}
	// o vínculo com a original continua existindo, desligado
	ps, err := e.svc.ListSkillPersonas(ctx, e.co.ID, orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range ps {
		if sp.Enabled {
			t.Fatalf("a original deveria estar desligada para %s", sp.Name)
		}
	}
	// só admin bifurca
	u := mkUser(t, e, "m@example.com", domain.RoleMember)
	join(t, e, u, domain.MemberMember)
	if _, err := e.svc.ForkSkill(as(u), domain.HumanActor, e.co.ID, orig.ID, service.ForkSkillInput{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro bifurcando: %v", err)
	}
}

func TestRecordRunSkillsSnapshot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	k := mkSkill(t, e, "Deploy", "rode o deploy")
	body2 := "rode o deploy v2"
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, k.ID, service.UpdateSkillInput{Body: &body2}); err != nil {
		t.Fatal(err)
	}
	ana := e.persona(t, "Ana", nil)
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, ana.ID, k.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetPersonaSkillPin(ctx, domain.HumanActor, e.co.ID, ana.ID, k.ID, intp(1)); err != nil {
		t.Fatal(err)
	}
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &ana.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(ana, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := e.svc.RunExtras(ctx, e.co.ID, ana.ID)
	if err != nil || len(ex.Skills) != 1 {
		t.Fatalf("extras: %+v %v", ex, err)
	}
	if err := e.svc.RecordRunSkills(ctx, e.co.ID, run.ID, ex.Skills); err != nil {
		t.Fatal(err)
	}
	// gravar de novo não duplica
	if err := e.svc.RecordRunSkills(ctx, e.co.ID, run.ID, ex.Skills); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.ListRunSkills(ctx, e.co.ID, run.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("run skills: %+v %v", got, err)
	}
	// a persona estava na revisão 1 (fixada), embora a atual seja a 2
	if got[0].Slug != "deploy" || got[0].Revision != 1 || !got[0].Pinned || len(got[0].SHA256) != 64 {
		t.Fatalf("snapshot: %+v", got[0])
	}
	if err := e.svc.RecordRunSkills(ctx, e.co.ID, run.ID, nil); err != nil {
		t.Fatal(err)
	}
	// a skill pode mudar depois: o retrato da run não
	body3 := "v3"
	if _, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, k.ID, service.UpdateSkillInput{Body: &body3}); err != nil {
		t.Fatal(err)
	}
	again, _ := e.svc.ListRunSkills(ctx, e.co.ID, run.ID)
	if again[0].Revision != 1 || again[0].SHA256 != got[0].SHA256 {
		t.Fatalf("o retrato mudou: %+v", again)
	}
}

func intp(n int) *int { return &n }
