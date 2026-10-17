package domain

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

// ---- precedência de ambiente (propriedade) ----

func TestChooseEnvironmentProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 2000; i++ {
		mk := func(src EnvSource) EnvCandidate {
			c := EnvCandidate{Source: src}
			if rng.Intn(3) > 0 { // 2/3 escolhem um ambiente
				id := NewID()
				c.ID = &id
			}
			c.Active = rng.Intn(4) > 0 // 3/4 ativos
			return c
		}
		p, pr, d := mk(EnvFromPersona), mk(EnvFromProject), mk(EnvFromDefault)
		got := ChooseEnvironment(p, pr, d)

		// oráculo: o primeiro nível, na ordem persona > projeto > padrão, que escolheu um ambiente ativo
		var want *EnvCandidate
		var skipped []EnvSource
		for _, c := range []EnvCandidate{p, pr, d} {
			c := c
			if c.ID == nil {
				continue
			}
			if !c.Active {
				skipped = append(skipped, c.Source)
				continue
			}
			want = &c
			break
		}
		if want == nil {
			if got.ID != nil || got.Source != EnvFromLocal {
				t.Fatalf("sem candidato ativo deveria cair no local: %+v (%+v %+v %+v)", got, p, pr, d)
			}
		} else if got.ID == nil || *got.ID != *want.ID || got.Source != want.Source {
			t.Fatalf("escolha errada: got %+v want %+v", got, want)
		}
		if len(got.Skipped) != len(skipped) {
			t.Fatalf("arquivados ignorados: got %v want %v", got.Skipped, skipped)
		}
		for j := range skipped {
			if got.Skipped[j] != skipped[j] {
				t.Fatalf("ordem dos ignorados: got %v want %v", got.Skipped, skipped)
			}
		}
		// invariantes: a origem é sempre válida e a persona só perde para outro nível se não escolheu ou está arquivada
		if !got.Source.Valid() {
			t.Fatalf("origem inválida %q", got.Source)
		}
		if p.ID != nil && p.Active && got.Source != EnvFromPersona {
			t.Fatalf("a persona ativa sempre vence: %+v", got)
		}
		if (got.ID == nil) != (got.Source == EnvFromLocal) {
			t.Fatalf("id nulo se e só se local: %+v", got)
		}
	}
}

func TestChooseEnvironmentTable(t *testing.T) {
	a, b, c := NewID(), NewID(), NewID()
	cases := []struct {
		name          string
		p, pr, d      EnvCandidate
		wantID        *ID
		wantSrc       EnvSource
		wantSkipCount int
	}{
		{"nada", EnvCandidate{}, EnvCandidate{}, EnvCandidate{}, nil, EnvFromLocal, 0},
		{"só o padrão", EnvCandidate{}, EnvCandidate{}, EnvCandidate{ID: &c, Active: true}, &c, EnvFromDefault, 0},
		{"projeto vence o padrão", EnvCandidate{}, EnvCandidate{ID: &b, Active: true}, EnvCandidate{ID: &c, Active: true}, &b, EnvFromProject, 0},
		{"persona vence tudo", EnvCandidate{ID: &a, Active: true}, EnvCandidate{ID: &b, Active: true}, EnvCandidate{ID: &c, Active: true}, &a, EnvFromPersona, 0},
		{"persona arquivada cai no projeto", EnvCandidate{ID: &a}, EnvCandidate{ID: &b, Active: true}, EnvCandidate{}, &b, EnvFromProject, 1},
		{"tudo arquivado é local", EnvCandidate{ID: &a}, EnvCandidate{ID: &b}, EnvCandidate{ID: &c}, nil, EnvFromLocal, 3},
	}
	for _, tc := range cases {
		tc.p.Source, tc.pr.Source, tc.d.Source = EnvFromPersona, EnvFromProject, EnvFromDefault
		got := ChooseEnvironment(tc.p, tc.pr, tc.d)
		if got.Source != tc.wantSrc || (got.ID == nil) != (tc.wantID == nil) || (got.ID != nil && *got.ID != *tc.wantID) || len(got.Skipped) != tc.wantSkipCount {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}

func TestEnvironmentDriversAndCloudConfig(t *testing.T) {
	for _, d := range []EnvironmentDriver{EnvLocal, EnvSSH, EnvSandbox, EnvCloud} {
		if !d.Valid() {
			t.Errorf("%s deveria ser válido", d)
		}
	}
	if EnvironmentDriver("x").Valid() || EnvLocal.Remote() || !EnvCloud.Remote() || !EnvSandbox.Remote() || !EnvSSH.Remote() {
		t.Fatal("Valid/Remote")
	}
	var c CloudConfig
	if err := c.Normalize(); err != nil || c.Provider != CloudDaytona || c.Network != "full" || c.Root != DefaultCloudRoot {
		t.Fatalf("padrões: %+v %v", c, err)
	}
	for _, bad := range []CloudConfig{{Provider: "outro"}, {Network: "x"}, {CPUs: 99}, {MemoryGiB: 999}, {DiskGiB: 9999}, {Root: "rel"}, {Root: "/"}, {Root: "/a/../b"}, {Snapshot: "-x y"}} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("aceitou %+v", bad)
		}
	}
	for _, s := range []EnvSource{EnvFromPersona, EnvFromProject, EnvFromDefault, EnvFromLocal} {
		if !s.Valid() {
			t.Errorf("origem %s", s)
		}
	}
	if EnvSource("x").Valid() {
		t.Fatal("origem desconhecida")
	}
}

// ---- readiness ----

func TestSummarizeReadiness(t *testing.T) {
	ok := []ReadinessStep{{Name: StepEnvironment, Status: StepOK}, {Name: StepBinary, Status: StepOK, Detail: "/usr/bin/claude"}, {Name: StepAuth, Status: StepSkip}, {Name: StepModel, Status: StepWarn, Detail: "sem preço"}}
	if st, reason := SummarizeReadiness(ok); st != ReadinessReady || reason != "" {
		t.Fatalf("warn/skip não bloqueiam: %v %q", st, reason)
	}
	bad := append([]ReadinessStep(nil), ok...)
	bad[2] = ReadinessStep{Name: StepAuth, Status: StepFail, Detail: "sem login"}
	bad[1] = ReadinessStep{Name: StepBinary, Status: StepFail}
	if st, reason := SummarizeReadiness(bad); st != ReadinessNotReady || reason != "binary" {
		t.Fatalf("o primeiro passo que falhou é o motivo: %v %q", st, reason)
	}
	bad[1].Status = StepOK
	if st, reason := SummarizeReadiness(bad); st != ReadinessNotReady || reason != "auth: sem login" {
		t.Fatalf("%v %q", st, reason)
	}
	if st, _ := SummarizeReadiness(nil); st != ReadinessReady {
		t.Fatal("sem passos não há falha")
	}
}

func TestReadinessBlocks(t *testing.T) {
	r := Readiness{Status: ReadinessNotReady, Agent: "claude", Model: "sonnet"}
	if !r.Blocks("claude", "sonnet") {
		t.Fatal("mesmo harness e modelo bloqueia")
	}
	if r.Blocks("codex", "sonnet") || r.Blocks("claude", "opus") {
		t.Fatal("teste de outro harness/modelo não vale")
	}
	r.Status = ReadinessReady
	if r.Blocks("claude", "sonnet") {
		t.Fatal("pronta não bloqueia")
	}
	if !ReadinessReady.Valid() || ReadinessStatus("x").Valid() || !ReadinessPeriodic.Valid() || ReadinessTrigger("x").Valid() {
		t.Fatal("Valid")
	}
	err := BlockedError("Ana", "auth: sem login")
	if !errors.Is(err, ErrPersonaNotReady) || !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "Ana") || !strings.Contains(err.Error(), "sem login") {
		t.Fatalf("erro de bloqueio: %v", err)
	}
}

// ---- catálogo de modelos ----

func TestMatchPrice(t *testing.T) {
	prefixes := []string{"", "claude-opus", "claude-sonnet", "claude-sonnet-4", "gpt-"}
	cases := []struct {
		model string
		want  string
		ok    bool
	}{
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4", true},
		{"claude-sonnet-3-7", "claude-sonnet", true},
		{"sonnet", "claude-sonnet", true}, // alias do harness casa pela família
		{"opus", "claude-opus", true},
		{"haiku", "", false}, // sem prefixo claude-haiku na lista
		{"gpt-5", "gpt-", true},
		{"llama-3", "", false}, // só o preço de reserva (prefixo vazio) casaria: não conta
		{"", "", false},
		{"  CLAUDE-OPUS-4  ", "claude-opus", true},
	}
	for _, c := range cases {
		got, ok := MatchPrice(prefixes, c.model)
		if got != c.want || ok != c.ok {
			t.Errorf("MatchPrice(%q) = %q,%v; want %q,%v", c.model, got, ok, c.want, c.ok)
		}
	}
}

func TestAgentOfModel(t *testing.T) {
	for m, want := range map[string]string{"claude-opus-4": "claude", "sonnet": "claude", "gpt-5-codex": "codex", "o3-mini": "codex", "codex-mini": "codex", "llama": ""} {
		if got := AgentOfModel(m); got != want {
			t.Errorf("AgentOfModel(%q) = %q, want %q", m, got, want)
		}
	}
}

func TestBuildModelCatalog(t *testing.T) {
	prices := []string{"", "claude-opus", "claude-sonnet", "claude-sonnet-4", "gpt-", "gpt-5"}
	reports := []ModelReport{
		{Agent: "claude", Model: "claude-sonnet-4-5", Origin: ModelProbed},
		{Agent: "claude", Model: "claude-sonnet-4-5", Origin: ModelUsed}, // duplicado: junta as origens
		{Agent: "codex", Model: "gpt-5-codex", Origin: ModelUsed},
		{Agent: "claude", Model: "modelo-novo", Origin: ModelProbed}, // sem preço próprio
		{Model: "claude-haiku-4", Origin: ModelUsed},                 // agent inferido pelo prefixo
	}
	all := BuildModelCatalog("", prices, reports)
	by := map[string]CatalogModel{}
	for _, m := range all {
		by[m.Agent+"/"+m.Model] = m
	}
	if m := by["claude/claude-sonnet-4-5"]; !m.Priced || m.PriceModel != "claude-sonnet-4" || len(m.Origins) != 2 {
		t.Errorf("sonnet-4-5: %+v", m)
	}
	if m := by["claude/modelo-novo"]; m.Priced {
		t.Errorf("modelo-novo não tem preço próprio: %+v", m)
	}
	if m := by["claude/sonnet"]; !m.Priced || m.Origins[0] != ModelBuiltin {
		t.Errorf("alias do harness: %+v", m)
	}
	if m := by["claude/haiku"]; m.Priced {
		t.Errorf("haiku sem linha em model_prices: %+v", m)
	}
	if _, ok := by["codex/gpt-"]; ok {
		t.Error("prefixo de família não é modelo selecionável")
	}
	if m := by["codex/gpt-5"]; !m.Priced {
		t.Errorf("gpt-5 vem de model_prices: %+v", m)
	}
	if _, ok := by["claude/claude-haiku-4"]; !ok {
		t.Error("o agent deveria ser inferido pelo prefixo")
	}
	claude := BuildModelCatalog("claude", prices, reports)
	for _, m := range claude {
		if m.Agent != "claude" && m.Agent != "" {
			t.Errorf("filtro por harness vazou %+v", m)
		}
	}
	for i := 1; i < len(all); i++ {
		a, b := all[i-1], all[i]
		if a.Agent > b.Agent || a.Agent == b.Agent && a.Model > b.Model {
			t.Fatalf("fora de ordem: %v antes de %v", a, b)
		}
	}
}
