package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// studioFake é o fake com o estúdio de skills (ADR-0035): a task do token é a de um teste.
type studioFake struct {
	*fake
	specs map[domain.ID]service.SkillTestSpec
}

func (f *studioFake) SkillTestRunSpec(_ context.Context, _, taskID domain.ID) (service.SkillTestSpec, error) {
	if s, ok := f.specs[taskID]; ok {
		return s, nil
	}
	return service.SkillTestSpec{}, domain.ErrNotFound
}

func TestSkillTestRunSeesOnlyTheTestedSkill(t *testing.T) {
	e := setup(t, true)
	eng := e.f.personas[1].ID
	e.f.skills = map[domain.ID][]domain.Skill{eng: {{Slug: "outra", Name: "Outra", Description: "habilitada", Version: 1, Body: "não use"}}}
	sf := &studioFake{fake: e.f, specs: map[domain.ID]service.SkillTestSpec{e.f.tasks[0].ID: {
		Skill: domain.Skill{Slug: "deploy", Name: "Deploy", Description: "Como publicar", Version: 2, Body: "# corpo v2"}, Input: "x"}}}
	e.srv.Close()
	e.srv = httptest.NewServer(newHandler(sf, Deps{Tokens: e.tokens, Trusted: true, Version: "test"}))
	t.Cleanup(e.srv.Close)
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	var list skillListOut
	structured(t, call(t, cs, "hanko_skill_list", map[string]any{}), &list)
	if len(list.Skills) != 1 || list.Skills[0].Slug != "deploy" {
		t.Fatalf("list: %+v", list)
	}
	var got skillGetOut
	res := call(t, cs, "hanko_skill_get", map[string]any{"slug": "deploy"})
	structured(t, res, &got)
	if res.IsError || got.Skill.Body != "# corpo v2" || got.Skill.Version != 2 {
		t.Fatalf("get: %+v %s", got, text(res))
	}
	if res = call(t, cs, "hanko_skill_get", map[string]any{"slug": "outra"}); !res.IsError || strings.Contains(text(res), "não use") {
		t.Fatalf("skill habilitada da persona numa run de teste: %s", text(res))
	}
	// as demais tools são recusadas: a run de teste não age na empresa
	res = call(t, cs, "hanko_task_comment", map[string]any{"task": "HNK-1", "body": "oi"})
	if !res.IsError || !strings.Contains(text(res), "teste de skill") {
		t.Fatalf("tool recusada: %v %s", res.IsError, text(res))
	}
	if res = call(t, cs, "hanko_whoami", map[string]any{}); res.IsError {
		t.Fatalf("whoami: %s", text(res))
	}

	// uma run comum (task que não é de teste) não muda
	delete(sf.specs, e.f.tasks[0].ID)
	structured(t, call(t, cs, "hanko_skill_list", map[string]any{}), &list)
	if len(list.Skills) != 1 || list.Skills[0].Slug != "outra" {
		t.Fatalf("run comum: %+v", list)
	}
}
