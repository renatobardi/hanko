package domain

import (
	"testing"
)

func TestSemVerNext(t *testing.T) {
	v := FirstSemVer
	steps := []struct {
		k    ChangeKind
		want string
	}{{ChangeMinor, "1.1"}, {ChangeMinor, "1.2"}, {ChangeMajor, "2.0"}, {ChangeMinor, "2.1"}, {ChangeMajor, "3.0"}}
	for _, s := range steps {
		v = v.Next(s.k)
		if v.String() != s.want {
			t.Fatalf("Next(%s) = %s, quer %s", s.k, v, s.want)
		}
	}
}

func TestParseSemVer(t *testing.T) {
	ok := map[string]SemVer{"1.0": {1, 0}, "v2.3": {2, 3}, " 4 ": {4, 0}, "10.12": {10, 12}}
	for in, want := range ok {
		got, err := ParseSemVer(in)
		if err != nil || got != want {
			t.Errorf("ParseSemVer(%q) = %v, %v; quer %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0.1", "1.-1", "a.b", "1.x", "+1.0", "1.2.3"} {
		if _, err := ParseSemVer(in); err == nil {
			t.Errorf("ParseSemVer(%q) deveria falhar", in)
		}
	}
}

func TestMeasureChange(t *testing.T) {
	cases := []struct {
		name                     string
		removed, added, old, new int
		mime                     bool
		want                     ChangeKind
	}{
		{"typo", 1, 1, 50, 50, false, ChangeMinor},
		{"meio-termo não opina", 15, 15, 50, 50, false, ""},
		{"reescrita", 40, 45, 50, 55, false, ChangeMajor},
		{"no limiar é major", 20, 20, 50, 50, false, ChangeMajor},
		{"tipo mudou", 0, 0, 10, 10, true, ChangeMajor},
		{"vazio", 0, 0, 0, 0, false, ChangeMinor},
	}
	for _, c := range cases {
		m := MeasureChange(c.removed, c.added, c.old, c.new, c.mime)
		if m.Suggested != c.want || !m.Measured || m.Ratio < 0 || m.Ratio > 1 {
			t.Errorf("%s: %+v, quer sugestão %q", c.name, m, c.want)
		}
	}
}

func TestClassifyChange(t *testing.T) {
	major, minor := ChangeMajor, ChangeMinor
	sugMajor := ChangeMeasure{Measured: true, Ratio: .6, Suggested: ChangeMajor}
	sugMinor := ChangeMeasure{Measured: true, Ratio: .02, Suggested: ChangeMinor}
	neutral := ChangeMeasure{Measured: true, Ratio: .2}
	cases := []struct {
		name     string
		declared *ChangeKind
		m        ChangeMeasure
		want     ChangeKind
		disputed bool
	}{
		{"declarou e confere", &major, sugMajor, ChangeMajor, false},
		{"declarou minor, medida diz major", &minor, sugMajor, ChangeMinor, true},
		{"declarou major em poucas linhas", &major, sugMinor, ChangeMajor, true},
		{"declarou e a medida não opina", &major, neutral, ChangeMajor, false},
		{"binário: vale a declaração", &minor, ChangeMeasure{}, ChangeMinor, false},
		{"sem declaração: sugestão", nil, sugMajor, ChangeMajor, false},
		{"sem declaração nem sugestão: minor", nil, neutral, ChangeMinor, false},
	}
	for _, c := range cases {
		got, disputed := ClassifyChange(c.declared, c.m)
		if got != c.want || disputed != c.disputed {
			t.Errorf("%s: %s/%v, quer %s/%v", c.name, got, disputed, c.want, c.disputed)
		}
	}
}

func TestNormalizeTags(t *testing.T) {
	got, err := NormalizeTags([]string{" Arquitetura ", "cliente-x", "arquitetura", ""})
	if err != nil || len(got) != 2 || got[0] != "arquitetura" || got[1] != "cliente-x" {
		t.Fatalf("NormalizeTags = %v, %v", got, err)
	}
	for _, bad := range [][]string{{"com espaço"}, {"ç"}, {"a/b"}, {string(make([]byte, 41))}} {
		if _, err := NormalizeTags(bad); err == nil {
			t.Errorf("NormalizeTags(%q) deveria falhar", bad)
		}
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = "t" + string(rune('a'+i))
	}
	if _, err := NormalizeTags(many); err == nil {
		t.Error("21 tags deveriam falhar")
	}
}
