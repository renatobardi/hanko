package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func mkSkill(t *testing.T, e *env, name, body string) domain.Skill {
	t.Helper()
	sk, err := e.svc.CreateSkill(context.Background(), domain.HumanActor, e.co.ID, service.CreateSkillInput{Name: name, Description: "d " + name, Body: body})
	if err != nil {
		t.Fatalf("create skill %s: %v", name, err)
	}
	return sk
}

func TestSkillCRUDAndVersion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	sk := mkSkill(t, e, "Revisar PR", "# Passos\n\n1. Leia o diff")
	if sk.Slug != "revisar-pr" || sk.Version != 1 || sk.Body != "# Passos\n\n1. Leia o diff" {
		t.Fatalf("skill criada: %+v", sk)
	}
	// slug repetido na empresa
	_, err := e.svc.CreateSkill(ctx, domain.HumanActor, e.co.ID, service.CreateSkillInput{Name: "Outra", Slug: "revisar-pr", Body: "x"})
	wantErr(t, err, domain.ErrConflict)

	// sem mudança real: versão não sobe
	same, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, sk.ID, service.UpdateSkillInput{Name: ptr("Revisar PR")})
	if err != nil || same.Version != 1 {
		t.Fatalf("update sem mudança: %+v %v", same, err)
	}
	up, err := e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, sk.ID, service.UpdateSkillInput{Body: ptr("# Novo corpo")})
	if err != nil || up.Version != 2 || up.Body != "# Novo corpo" || up.Slug != "revisar-pr" {
		t.Fatalf("update: %+v %v", up, err)
	}
	up, err = e.svc.UpdateSkill(ctx, domain.HumanActor, e.co.ID, sk.ID, service.UpdateSkillInput{Name: ptr("Revisão de PR"), Description: ptr("nova")})
	if err != nil || up.Version != 3 || up.Name != "Revisão de PR" {
		t.Fatalf("update 2: %+v %v", up, err)
	}

	// leitura por id e por slug
	for _, ref := range []string{sk.ID.String(), "revisar-pr"} {
		got, err := e.svc.GetSkill(ctx, e.co.ID, ref)
		if err != nil || got.ID != sk.ID {
			t.Fatalf("GetSkill(%s): %+v %v", ref, got, err)
		}
	}
	_, err = e.svc.GetSkill(ctx, e.co.ID, "nao-existe")
	wantErr(t, err, domain.ErrNotFound)

	list, err := e.svc.ListSkills(ctx, e.co.ID)
	if err != nil || len(list) != 1 || list[0].Personas != 0 {
		t.Fatalf("list: %+v %v", list, err)
	}

	// o feed registra os fatos
	acts, err := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "skill"})
	if err != nil {
		t.Fatal(err)
	}
	verbs := map[string]int{}
	for _, a := range acts {
		verbs[a.Verb]++
	}
	if verbs["skill.created"] != 1 || verbs["skill.updated"] != 2 {
		t.Fatalf("atividade: %v", verbs)
	}

	if err := e.svc.DeleteSkill(ctx, domain.HumanActor, e.co.ID, sk.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.DeleteSkill(ctx, domain.HumanActor, e.co.ID, sk.ID), domain.ErrNotFound)
}

func TestSkillValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	big := strings.Repeat("x", domain.MaxSkillBodyBytes+1)
	cases := map[string]service.CreateSkillInput{
		"sem nome":           {Body: "x"},
		"sem corpo":          {Name: "a"},
		"corpo enorme":       {Name: "a", Body: big},
		"nome enorme":        {Name: strings.Repeat("n", domain.MaxSkillNameRunes+1), Body: "x"},
		"descrição longa":    {Name: "a", Description: strings.Repeat("d", domain.MaxSkillDescRunes+1), Body: "x"},
		"slug inválido":      {Name: "a", Slug: "Slug Ruim", Body: "x"},
		"frontmatter aberto": {Name: "a", Body: "---\nname: x\ncorpo sem fechar"},
	}
	for name, in := range cases {
		_, err := e.svc.CreateSkill(ctx, domain.HumanActor, e.co.ID, in)
		if err == nil {
			t.Errorf("%s: deveria falhar", name)
			continue
		}
		wantErr(t, err, domain.ErrInvalid)
	}
	// no limite exato passa
	if _, err := e.svc.CreateSkill(ctx, domain.HumanActor, e.co.ID, service.CreateSkillInput{Name: "limite", Body: strings.Repeat("x", domain.MaxSkillBodyBytes)}); err != nil {
		t.Errorf("corpo no limite: %v", err)
	}
}

// Um SKILL.md inteiro no corpo: o frontmatter completa nome e descrição e nunca é gravado.
func TestSkillAbsorbsFrontmatter(t *testing.T) {
	e := newEnv(t)
	sk, err := e.svc.CreateSkill(context.Background(), domain.HumanActor, e.co.ID, service.CreateSkillInput{
		Body: "---\nname: Escrever ADR\ndescription: Guia para redigir um ADR curto\n---\n\n# Como\n\nSiga o formato."})
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "Escrever ADR" || sk.Slug != "escrever-adr" || sk.Description != "Guia para redigir um ADR curto" ||
		sk.Body != "# Como\n\nSiga o formato." {
		t.Fatalf("%+v", sk)
	}
	md := sk.MD()
	if !strings.HasPrefix(md, "---\nname: escrever-adr\ndescription: \"Guia para redigir um ADR curto\"\n---\n\n# Como") {
		t.Fatalf("SKILL.md:\n%s", md)
	}
}

func TestPersonaSkillLinks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	bia := e.persona(t, "Bia", nil)
	s1 := mkSkill(t, e, "Alfa", "corpo alfa")
	s2 := mkSkill(t, e, "Beta", "corpo beta")

	// nada ligado ainda
	ps, err := e.svc.ListPersonaSkills(ctx, e.co.ID, ana.ID)
	if err != nil || len(ps) != 2 || ps[0].Enabled || ps[1].Enabled {
		t.Fatalf("estado inicial: %+v %v", ps, err)
	}
	_, err = e.svc.PersonaSkill(ctx, e.co.ID, ana.ID, "alfa")
	wantErr(t, err, domain.ErrNotFound)

	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, ana.ID, s1.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, bia.ID, s1.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, ana.ID, s2.ID, true); err != nil {
		t.Fatal(err)
	}
	// idempotente
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, ana.ID, s2.ID, true); err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.PersonaSkills(ctx, e.co.ID, ana.ID)
	if err != nil || len(got) != 2 || got[0].Slug != "alfa" || got[1].Slug != "beta" {
		t.Fatalf("skills da Ana: %+v %v", got, err)
	}
	sk, err := e.svc.PersonaSkill(ctx, e.co.ID, ana.ID, "alfa")
	if err != nil || sk.Body != "corpo alfa" {
		t.Fatalf("PersonaSkill: %+v %v", sk, err)
	}
	// a Bia não enxerga a Beta
	_, err = e.svc.PersonaSkill(ctx, e.co.ID, bia.ID, "beta")
	wantErr(t, err, domain.ErrNotFound)

	list, _ := e.svc.ListSkills(ctx, e.co.ID)
	counts := map[string]int{}
	for _, v := range list {
		counts[v.Slug] = v.Personas
	}
	if counts["alfa"] != 2 || counts["beta"] != 1 {
		t.Fatalf("contagem: %v", counts)
	}
	sp, err := e.svc.ListSkillPersonas(ctx, e.co.ID, s1.ID)
	if err != nil || len(sp) != 2 || !sp[0].Enabled || !sp[1].Enabled {
		t.Fatalf("personas da skill: %+v %v", sp, err)
	}

	// desligar tira da persona, sem apagar a skill
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, ana.ID, s1.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.PersonaSkills(ctx, e.co.ID, ana.ID)
	if len(got) != 1 || got[0].Slug != "beta" {
		t.Fatalf("depois de desligar: %+v", got)
	}

	// apagar a skill remove as ligações
	if err := e.svc.DeleteSkill(ctx, domain.HumanActor, e.co.ID, s2.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.PersonaSkills(ctx, e.co.ID, ana.ID)
	if len(got) != 0 {
		t.Fatalf("após apagar: %+v", got)
	}

	// persona ou skill de outra empresa: não existe
	other, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra Co"})
	if err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.SetPersonaSkill(ctx, domain.HumanActor, other.ID, ana.ID, s1.ID, true), domain.ErrNotFound)
}

func TestSkillRoles(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	p := e.persona(t, "Ana", nil)
	sk := mkSkill(t, e, "Alfa", "corpo")
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
		u := mkUser(t, e, string(r)+"@x.com", domain.RoleMember)
		join(t, e, u, r)
		users[r] = u
	}
	outsider := mkUser(t, e, "fora@x.com", domain.RoleMember)

	reads := map[string]func(ctx context.Context) error{
		"ListSkills": func(ctx context.Context) error { _, err := e.svc.ListSkills(ctx, e.co.ID); return err },
		"GetSkill":   func(ctx context.Context) error { _, err := e.svc.GetSkill(ctx, e.co.ID, "alfa"); return err },
		"ListPersonaSkills": func(ctx context.Context) error {
			_, err := e.svc.ListPersonaSkills(ctx, e.co.ID, p.ID)
			return err
		},
	}
	for name, run := range reads {
		for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
			if err := run(as(users[r])); err != nil {
				t.Errorf("%s como %s: %v", name, r, err)
			}
		}
		wantErr(t, run(as(outsider)), domain.ErrNotFound)
	}

	writes := map[string]func(ctx context.Context, u domain.User) error{
		"CreateSkill": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.CreateSkill(ctx, u.Actor(), e.co.ID, service.CreateSkillInput{Name: "N-" + u.Email, Body: "x"})
			return err
		},
		"UpdateSkill": func(ctx context.Context, u domain.User) error {
			_, err := e.svc.UpdateSkill(ctx, u.Actor(), e.co.ID, sk.ID, service.UpdateSkillInput{Description: ptr("d-" + u.Email)})
			return err
		},
		"SetPersonaSkill": func(ctx context.Context, u domain.User) error {
			return e.svc.SetPersonaSkill(ctx, u.Actor(), e.co.ID, p.ID, sk.ID, true)
		},
	}
	for name, run := range writes {
		for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember} {
			wantErr(t, run(as(users[r]), users[r]), domain.ErrForbidden)
		}
		for _, r := range []domain.MemberRole{domain.MemberAdmin, domain.MemberOwner} {
			if err := run(as(users[r]), users[r]); err != nil {
				t.Errorf("%s como %s: %v", name, r, err)
			}
		}
		wantErr(t, run(as(outsider), outsider), domain.ErrNotFound)
	}
	// apagar também é de admin
	wantErr(t, e.svc.DeleteSkill(as(users[domain.MemberMember]), users[domain.MemberMember].Actor(), e.co.ID, sk.ID), domain.ErrForbidden)
	if err := e.svc.DeleteSkill(as(users[domain.MemberAdmin]), users[domain.MemberAdmin].Actor(), e.co.ID, sk.ID); err != nil {
		t.Fatal(err)
	}
	_ = bg
}

func TestFitSkillsLimits(t *testing.T) {
	mk := func(slug string, size int) domain.Skill {
		return domain.Skill{Slug: slug, Name: slug, Body: strings.Repeat("x", size)}
	}
	// por bytes: cada uma ~30 KiB; cabem 8 (240 KiB), a nona estoura os 256 KiB
	var many []domain.Skill
	for i := 0; i < 10; i++ {
		many = append(many, mk(string(rune('a'+i))+"-skill", 30<<10))
	}
	fit, skipped := service.FitSkills(many)
	if len(fit) != 8 || len(skipped) != 2 || skipped[0] != "i-skill" {
		t.Fatalf("por bytes: fit=%d skipped=%v", len(fit), skipped)
	}
	// por quantidade
	var tiny []domain.Skill
	for i := 0; i < domain.MaxSkillsPerRun+5; i++ {
		tiny = append(tiny, mk("s"+strings.Repeat("a", i%7)+string(rune('a'+i/7)), 10))
	}
	fit, skipped = service.FitSkills(tiny)
	if len(fit) != domain.MaxSkillsPerRun || len(skipped) != 5 {
		t.Fatalf("por quantidade: fit=%d skipped=%d", len(fit), len(skipped))
	}
	// uma grande no meio é pulada e as seguintes ainda entram
	mixed := []domain.Skill{mk("a", 100<<10), mk("b", 200<<10), mk("c", 10)}
	fit, skipped = service.FitSkills(mixed)
	if len(fit) != 2 || fit[1].Slug != "c" || len(skipped) != 1 || skipped[0] != "b" {
		t.Fatalf("misto: fit=%v skipped=%v", fit, skipped)
	}
}
