package aidlc

import "testing"

func TestDecideOrderOfSignals(t *testing.T) {
	cases := []struct {
		name string
		sig  Signals
		want Result
	}{
		{"manual vale sobre tudo", Signals{Manual: "ship", Opening: "spec", Label: "plan"},
			Result{Class: "ship", Origin: OriginManual, Confidence: ConfidenceHigh}},
		{"abertura vale sobre label e skill", Signals{Opening: "spec", Label: "plan", Skills: map[string]SkillUse{"qa": {3, 1}}},
			Result{Class: "spec", Origin: OriginOpening, Confidence: ConfidenceHigh}},
		{"label vale sobre skill", Signals{Label: "plan", Skills: map[string]SkillUse{"qa": {3, 1}}},
			Result{Class: "plan", Origin: OriginLabel, Confidence: ConfidenceHigh}},
		{"papel de revisor é qa", Signals{Reviewer: true, Dispatcher: true},
			Result{Class: "qa", Origin: OriginRole, Confidence: ConfidenceHigh}},
		{"papel de dispatcher é plan", Signals{Dispatcher: true},
			Result{Class: "plan", Origin: OriginRole, Confidence: ConfidenceHigh}},
	}
	for _, c := range cases {
		if got := Decide(c.sig); got != c.want {
			t.Errorf("%s: Decide = %+v; quero %+v", c.name, got, c.want)
		}
	}
}

func TestDecideSkillMajorityAndTie(t *testing.T) {
	if got := Decide(Signals{Skills: map[string]SkillUse{"build": {5, 9}, "qa": {2, 1}}}); got.Class != "build" || got.Confidence != ConfidenceHigh {
		t.Errorf("maioria de skill = %+v; quero build alta", got)
	}
	// empate de usos: vale a skill usada primeiro, com confiança baixa
	if got := Decide(Signals{Skills: map[string]SkillUse{"build": {2, 50}, "qa": {2, 10}}}); got.Class != "qa" || got.Confidence != ConfidenceLow || got.Origin != OriginSkill {
		t.Errorf("empate de skill = %+v; quero qa, baixa, skill (a usada primeiro)", got)
	}
}

func TestDecideActionStrongAndWeak(t *testing.T) {
	if got := Decide(Signals{Actions: map[string]int{"qa": 6, "build": 2}}); got.Class != "qa" || got.Origin != OriginAction || got.Confidence != ConfidenceHigh || got.NeedsJev {
		t.Errorf("ação forte = %+v; quero qa, ação, alta, sem Jev", got)
	}
	// 3 pontos contra 2: não é forte (margem de 2×); a mais provável vale com confiança baixa e pede o Jev
	if got := Decide(Signals{Actions: map[string]int{"qa": 3, "build": 2}}); got.Class != "qa" || got.Confidence != ConfidenceLow || !got.NeedsJev {
		t.Errorf("ação fraca = %+v; quero qa, baixa, precisa de Jev", got)
	}
}

func TestDecideDefaultWithoutSignal(t *testing.T) {
	got := Decide(Signals{})
	if got.Class != DefaultPhase || got.Origin != OriginAction || got.Confidence != ConfidenceLow || !got.NeedsJev {
		t.Errorf("sem sinal = %+v; quero build, ação, baixa, precisa de Jev", got)
	}
}

func TestActionPoints(t *testing.T) {
	cases := []struct {
		a     Action
		phase string
		pts   int
		ok    bool
	}{
		{Action{Tool: "Bash", Command: "gh pr create --title x"}, "build", 4, true},
		{Action{Tool: "Bash", Command: "gh pr review 9"}, "qa", 3, true},
		{Action{Tool: "Edit", Path: "/repo/docs/adr/0079.md"}, "arch", 3, true},
		{Action{Tool: "Edit", Path: "/repo/main.go"}, "build", 1, true},
		{Action{Tool: "WebSearch"}, "strat", 1, true},
		{Action{Tool: "Bash", Command: "ls -la"}, "", 0, false},
	}
	for _, c := range cases {
		phase, pts, ok := ActionPoints(c.a)
		if phase != c.phase || pts != c.pts || ok != c.ok {
			t.Errorf("ActionPoints(%+v) = %q, %d, %v; quero %q, %d, %v", c.a, phase, pts, ok, c.phase, c.pts, c.ok)
		}
	}
}

func TestSkillPhase(t *testing.T) {
	if got := SkillPhase("/home/u/.claude/skills/oute-aidlc-plan-criar/SKILL.md"); got != "plan" {
		t.Errorf("SkillPhase = %q, quero plan", got)
	}
	if got := SkillPhase("outra-skill"); got != "" {
		t.Errorf("SkillPhase = %q, quero vazio", got)
	}
}
