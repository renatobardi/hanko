package domain

import (
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

func TestProfileApply(t *testing.T) {
	base := UserProfile{Name: "Ana", Timezone: "America/Sao_Paulo", Locale: "pt-BR"}
	cases := []struct {
		name    string
		in      ProfileInput
		wantErr string
		check   func(UserProfile) bool
	}{
		{"nome", ProfileInput{Name: strp("  Ana Lima ")}, "", func(p UserProfile) bool { return p.Name == "Ana Lima" }},
		{"nome vazio", ProfileInput{Name: strp("  ")}, "name", nil},
		{"nome longo", ProfileInput{Name: strp(strings.Repeat("x", 81))}, "name", nil},
		{"glifo", ProfileInput{AvatarGlyph: strp("AL")}, "", func(p UserProfile) bool { return p.AvatarGlyph == "AL" }},
		{"glifo emoji", ProfileInput{AvatarGlyph: strp("🦊")}, "", func(p UserProfile) bool { return p.AvatarGlyph == "🦊" }},
		{"glifo longo", ProfileInput{AvatarGlyph: strp("ABCD")}, "avatar_glyph", nil},
		{"glifo marcação", ProfileInput{AvatarGlyph: strp("<b")}, "avatar_glyph", nil},
		{"tom", ProfileInput{AvatarTone: strp("violet")}, "", func(p UserProfile) bool { return p.AvatarTone == "violet" }},
		{"tom limpo", ProfileInput{AvatarTone: strp("")}, "", func(p UserProfile) bool { return p.AvatarTone == "" }},
		{"tom inválido", ProfileInput{AvatarTone: strp("pink")}, "avatar_tone", nil},
		{"fuso", ProfileInput{Timezone: strp("Europe/Lisbon")}, "", func(p UserProfile) bool { return p.Timezone == "Europe/Lisbon" }},
		{"fuso vazio", ProfileInput{Timezone: strp("")}, "", func(p UserProfile) bool { return p.Timezone == "" }},
		{"fuso inválido", ProfileInput{Timezone: strp("Marte/Olympus")}, "timezone", nil},
		{"fuso traversal", ProfileInput{Timezone: strp("../etc/passwd")}, "timezone", nil},
		{"idioma", ProfileInput{Locale: strp("en")}, "", func(p UserProfile) bool { return p.Locale == "en" }},
		{"idioma inválido", ProfileInput{Locale: strp("fr")}, "locale", nil},
		{"nada", ProfileInput{}, "", func(p UserProfile) bool { return p == base }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := base.Apply(c.in)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("erro = %v, quer %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !c.check(got) {
				t.Fatalf("resultado inesperado: %+v", got)
			}
		})
	}
}

func TestNormalizeNavPath(t *testing.T) {
	ok := map[string]string{
		"/tasks/HNK-1":       "/tasks/HNK-1",
		"/tasks/HNK-1/":      "/tasks/HNK-1",
		"/personas/ana?x=1":  "/personas/ana",
		"/tasks#frag":        "/tasks",
		"  /projetos/site  ": "/projetos/site",
		"/cards-de-status":   "/cards-de-status",
	}
	for in, want := range ok {
		got, err := NormalizeNavPath(in)
		if err != nil || got != want {
			t.Errorf("NormalizeNavPath(%q) = %q, %v; quer %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "tasks", "//evil.com/x", "https://evil.com/tasks", "/tasks/../../x", "/tasks//x", "/desconhecida/1",
		"/tasks/a b", "/tasks\\x", "/" + strings.Repeat("a", 400), "javascript:alert(1)", "/tasks/%zz", "/"} {
		if got, err := NormalizeNavPath(in); err == nil {
			t.Errorf("NormalizeNavPath(%q) = %q, quer erro", in, got)
		}
	}
}

func TestNavHelpers(t *testing.T) {
	if !IsNavDetail("/tasks/HNK-1") || IsNavDetail("/tasks") || IsNavDetail("/personas/nova") {
		t.Error("IsNavDetail")
	}
	if got := CleanNavTitle("  a \n  b\t c "); got != "a b c" {
		t.Errorf("CleanNavTitle = %q", got)
	}
	if got := CleanNavTitle(strings.Repeat("é", 300)); len([]rune(got)) != 200 || !strings.HasSuffix(got, "…") {
		t.Errorf("CleanNavTitle longo: %d", len([]rune(got)))
	}
	if !NavFavorite.Valid() || !NavRecent.Valid() || NavKind("x").Valid() {
		t.Error("NavKind.Valid")
	}
}

func TestComputeOnboarding(t *testing.T) {
	cases := []struct {
		name         string
		f            OnboardingFacts
		current      OnboardingStepKey
		complete     bool
		done         int
		active       bool
		harnessState string
	}{
		{"começo", OnboardingFacts{}, StepHarness, false, 1, true, ""},
		{"probe falhou", OnboardingFacts{HarnessAgent: "claude", HarnessStatus: "not_ready"}, StepHarness, false, 1, true, "not_ready"},
		{"probe ok", OnboardingFacts{HarnessAgent: "claude", HarnessStatus: "ready"}, StepPersona, false, 2, true, "ready"},
		{"harness pulado", OnboardingFacts{Skipped: []string{"harness"}}, StepPersona, false, 2, true, ""},
		{"pular passo que não pula", OnboardingFacts{Skipped: []string{"persona", "task"}}, StepHarness, false, 1, true, ""},
		{"persona sem harness", OnboardingFacts{Personas: 1, Skipped: []string{"harness"}}, StepTask, false, 3, true, ""},
		{"task criada", OnboardingFacts{Personas: 2, HarnessStatus: "ready", FirstTask: true}, "", true, 4, false, "ready"},
		{"run basta", OnboardingFacts{Personas: 1, HasRun: true, Skipped: []string{"harness"}}, "", true, 4, false, ""},
		{"dispensado", OnboardingFacts{Dismissed: true}, StepHarness, false, 1, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := ComputeOnboarding(c.f)
			if s.Current != c.current || s.Complete != c.complete || s.Done() != c.done || s.Active() != c.active || s.HarnessStatus != c.harnessState {
				t.Fatalf("status = %+v (feitos %d, ativo %v)", s, s.Done(), s.Active())
			}
			if len(s.Steps) != len(OnboardingSteps) {
				t.Fatalf("passos = %d", len(s.Steps))
			}
		})
	}
	if !StepHarness.Skippable() || StepPersona.Skippable() || !StepTask.Valid() || OnboardingStepKey("x").Valid() {
		t.Error("Skippable/Valid")
	}
}

func TestValidTimezone(t *testing.T) {
	for tz, want := range map[string]bool{"": true, "UTC": true, "America/Sao_Paulo": true, "x y": false, "Nope/Nope": false} {
		if ValidTimezone(tz) != want {
			t.Errorf("ValidTimezone(%q) != %v", tz, want)
		}
	}
}
