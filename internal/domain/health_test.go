package domain

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var healthNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestDuplicateFindingsPairsOnlyRepeatedMACs(t *testing.T) {
	refs := []HMACRef{
		{Secret: "a", Field: "x", MAC: "mac-1"},
		{Secret: "b", Field: "y", MAC: "mac-1"},
		{Secret: "c", Field: "z", MAC: "mac-2"},
		{Secret: "d", Field: "w", MAC: ""},
		{Secret: "e", Field: "v", MAC: ""},
	}
	got := DuplicateFindings(refs)
	if len(got) != 2 {
		t.Fatalf("achados = %d, quer 2: %+v", len(got), got)
	}
	if got[0].Secret != "a" || !slices.Equal(got[0].Related, []string{"b#y"}) {
		t.Fatalf("primeiro achado: %+v", got[0])
	}
	if got[1].Secret != "b" || !slices.Equal(got[1].Related, []string{"a#x"}) {
		t.Fatalf("segundo achado: %+v", got[1])
	}
}

// O achado de repetido nunca leva o HMAC que o comparou, nem em detalhe nem em relacionados.
func TestDuplicateFindingsDoNotExposeMAC(t *testing.T) {
	const mac = "9f8e7d6c5b4a39281706f5e4d3c2b1a0"
	refs := []HMACRef{{Secret: "a", Field: "x", MAC: mac}, {Secret: "b", Field: "y", MAC: mac}}
	for _, f := range DuplicateFindings(refs) {
		if strings.Contains(f.Detail, mac) || slices.ContainsFunc(f.Related, func(s string) bool { return strings.Contains(s, mac) }) {
			t.Fatalf("achado vaza o MAC: %+v", f)
		}
	}
}

func TestStaleFindingsOverRotationLimit(t *testing.T) {
	facts := []RotationFact{
		{Secret: "old", Field: "value", RotatedAt: healthNow.AddDate(0, 0, -100), LimitDays: 90},
		{Secret: "fresh", Field: "value", RotatedAt: healthNow.AddDate(0, 0, -80), LimitDays: 90},
		{Secret: "no-limit", Field: "value", RotatedAt: healthNow.AddDate(0, 0, -400), LimitDays: 0},
	}
	got := StaleFindings(facts, healthNow)
	if len(got) != 1 || got[0].Secret != "old" || got[0].Kind != HealthStale {
		t.Fatalf("achados = %+v, quer só o item vencido", got)
	}
	if got[0].Detail != "sem rotação há 100 dias (prazo 90)" {
		t.Fatalf("detalhe = %q", got[0].Detail)
	}
}

func TestUnusedFindingsNeverOrRecently(t *testing.T) {
	recent := healthNow.AddDate(0, 0, -10)
	long := healthNow.AddDate(0, 0, -120)
	facts := []UseFact{
		{Secret: "never", Field: "value", LastUsedAt: nil},
		{Secret: "recent", Field: "value", LastUsedAt: &recent},
		{Secret: "stale", Field: "value", LastUsedAt: &long},
	}
	got := UnusedFindings(facts, healthNow)
	if len(got) != 2 {
		t.Fatalf("achados = %+v, quer 2", got)
	}
	if got[0].Secret != "never" || got[0].Detail != "nunca usado" {
		t.Fatalf("nunca usado: %+v", got[0])
	}
	if got[1].Secret != "stale" || got[1].Detail != "sem uso há 120 dias" {
		t.Fatalf("sem uso: %+v", got[1])
	}
}

func TestOrphanFindingsOnlyUnreferenced(t *testing.T) {
	got := OrphanFindings([]string{"usado", "solto"}, map[string]bool{"usado": true})
	if len(got) != 1 || got[0].Secret != "solto" || got[0].Kind != HealthOrphan {
		t.Fatalf("achados = %+v, quer só o solto", got)
	}
}

func TestWeakFindingOnlyPasswordFieldsBelowThreshold(t *testing.T) {
	f, ok := WeakFinding("conta", "password", "abc123")
	if !ok || f.Kind != HealthWeak || f.Secret != "conta" || f.Field != "password" {
		t.Fatalf("senha fraca não marcada: %+v %v", f, ok)
	}
	if strings.Contains(f.Detail, "abc123") {
		t.Fatalf("achado de fraco vaza o valor: %q", f.Detail)
	}
	if _, ok := WeakFinding("conta", "password", "Tr0ub4dor&3-horse-battery"); ok {
		t.Fatal("senha forte marcada como fraca")
	}
	if _, ok := WeakFinding("conta", "usuario", "abc123"); ok {
		t.Fatal("campo que não é senha entrou na conta de fracos")
	}
}

func TestHealthKindsCoverEveryFinding(t *testing.T) {
	want := []HealthKind{HealthDuplicate, HealthStale, HealthUnused, HealthOrphan, HealthWeak}
	if !slices.Equal(HealthKinds, want) {
		t.Fatalf("HealthKinds = %v, quer %v", HealthKinds, want)
	}
}
