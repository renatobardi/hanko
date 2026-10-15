package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

func (f *fake) PersonaSkills(_ context.Context, _, personaID domain.ID) ([]domain.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Skill(nil), f.skills[personaID]...), nil
}

func (f *fake) PersonaSkill(_ context.Context, _, personaID domain.ID, slug string) (domain.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.skills[personaID] {
		if s.Slug == slug {
			return s, nil
		}
	}
	return domain.Skill{}, domain.ErrNotFound
}

func TestSkillToolsAreScopedToThePersona(t *testing.T) {
	e := setup(t, true)
	eng, cto := e.f.personas[1].ID, e.f.personas[0].ID
	e.f.skills = map[domain.ID][]domain.Skill{
		eng: {{Slug: "deploy", Name: "Deploy", Description: "Como publicar\num release", Version: 3, Body: "# Deploy\n1. rode o deploy"}},
		cto: {{Slug: "segredo", Name: "Segredo", Description: "só do CTO", Version: 1, Body: "não vaze"}},
	}
	cs, err := e.connect(t, e.tok) // token da persona "eng"
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	var list skillListOut
	res := call(t, cs, "hanko_skill_list", map[string]any{})
	structured(t, res, &list)
	if len(list.Skills) != 1 || list.Skills[0].Slug != "deploy" || list.Skills[0].Body != "" || list.Skills[0].Version != 3 ||
		!strings.Contains(text(res), "- deploy: Como publicar um release") {
		t.Fatalf("list: %+v / %s", list, text(res))
	}

	var got skillGetOut
	res = call(t, cs, "hanko_skill_get", map[string]any{"slug": "deploy"})
	structured(t, res, &got)
	if res.IsError || got.Skill.Body != "# Deploy\n1. rode o deploy" || !strings.Contains(text(res), "1. rode o deploy") || !strings.Contains(text(res), "v3") {
		t.Fatalf("get: %+v / %s", got, text(res))
	}

	// a skill de outra persona não existe para esta
	res = call(t, cs, "hanko_skill_get", map[string]any{"slug": "segredo"})
	if !res.IsError || strings.Contains(text(res), "não vaze") || !strings.Contains(text(res), "hanko_skill_list") {
		t.Fatalf("skill alheia: %v %s", res.IsError, text(res))
	}
	if res = call(t, cs, "hanko_skill_get", map[string]any{"slug": " "}); !res.IsError {
		t.Error("slug vazio deveria ser erro")
	}
}

func TestSkillToolsNeedAPersona(t *testing.T) {
	e := setup(t, true)
	cs, _ := e.connect(t, "") // trusted, sem token: ator humano
	defer cs.Close()
	for tool, args := range map[string]map[string]any{
		"hanko_skill_list": {"company": "acme"},
		"hanko_skill_get":  {"company": "acme", "slug": "x"},
	} {
		res := call(t, cs, tool, args)
		if !res.IsError || !strings.Contains(text(res), "persona") {
			t.Errorf("%s sem persona: %v %s", tool, res.IsError, text(res))
		}
	}
}
