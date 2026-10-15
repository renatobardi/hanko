package heartbeat

import (
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/service"
)

// O briefing da run traz as metas ativas, curtas (ADR-0028).
func TestBuildPromptGoals(t *testing.T) {
	rc := sampleContext()
	system, prompt := BuildPrompt(rc, "", "")
	if strings.Contains(prompt, "## Metas ativas") {
		t.Errorf("sem metas não há seção:\n%s", prompt)
	}
	for _, w := range []string{"hanko_artifact_put", "hanko_goal_update", "hanko_goal_list"} {
		if !strings.Contains(system, w) {
			t.Errorf("o protocolo não menciona %s", w)
		}
	}

	due := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	rc.Goals = []service.GoalBrief{
		{Slug: "cem", Title: "Chegar a 100 usuários", Metric: "usuários ativos", Unit: "usuários", Start: 0, Target: 100, Current: 25.5, Progress: 25.5, DueDate: &due, Linked: true},
		{Slug: "latencia", Title: "Reduzir a latência", Start: 200, Target: 50, Current: 125, Progress: 50},
	}
	_, prompt = BuildPrompt(rc, "", "")
	for _, w := range []string{"## Metas ativas", "- `cem` Chegar a 100 usuários: 26% (25.5 de 100 usuários), mede usuários ativos, prazo 2026-12-31 (esta task serve a ela)",
		"- `latencia` Reduzir a latência: 50% (125 de 50)"} {
		if !strings.Contains(prompt, w) {
			t.Errorf("prompt sem %q\n%s", w, prompt)
		}
	}
	// a seção vem antes da memória do projeto e depois da thread
	_, withMem := BuildPrompt(rc, "briefing X", "")
	if g, m, th := strings.Index(withMem, "## Metas ativas"), strings.Index(withMem, "## Memória do projeto"), strings.Index(withMem, "## Thread"); th >= g || g >= m {
		t.Errorf("ordem das seções: thread=%d metas=%d memória=%d", th, g, m)
	}
	// título gigante é cortado
	rc.Goals = []service.GoalBrief{{Slug: "x", Title: strings.Repeat("a", 500), Target: 1}}
	if _, prompt = BuildPrompt(rc, "", ""); strings.Contains(prompt, strings.Repeat("a", 200)) {
		t.Error("título da meta deveria ser cortado")
	}
}
