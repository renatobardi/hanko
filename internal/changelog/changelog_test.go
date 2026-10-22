package changelog_test

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko"
	"github.com/renatobardi/hanko/internal/changelog"
)

const sample = `# Changelog

## [Unreleased]

### Novo
- coisa nova

## [0.2.0] — 2026-10-15

- segunda

## [0.1.0] — 2026-10-14 (marco interno, sem tag)

- primeira
`

func TestParse(t *testing.T) {
	rels := changelog.Parse(sample)
	if len(rels) != 3 {
		t.Fatalf("blocos = %d", len(rels))
	}
	if rels[0].Version != "Unreleased" || !strings.Contains(rels[0].Body, "coisa nova") || strings.Contains(rels[0].Body, "segunda") {
		t.Errorf("unreleased = %+v", rels[0])
	}
	if rels[1].Version != "0.2.0" || rels[1].Date != "2026-10-15" || rels[1].Body != "- segunda" {
		t.Errorf("0.2.0 = %+v", rels[1])
	}
	if rels[2].Note != "marco interno, sem tag" || rels[2].Date != "2026-10-14" {
		t.Errorf("0.1.0 = %+v", rels[2])
	}
}

func TestFor(t *testing.T) {
	rels := changelog.Parse(sample)
	cases := map[string]string{
		"v0.2.0":          "0.2.0",
		"0.2.0":           "0.2.0",
		"0.2.0-5-gabc123": "0.2.0",
		"v0.1.0+meta":     "0.1.0",
		"dev":             "Unreleased",
		"":                "Unreleased",
		"v9.9.9":          "Unreleased",
	}
	for in, want := range cases {
		r, ok := changelog.For(rels, in)
		if !ok || r.Version != want {
			t.Errorf("For(%q) = %q, %v; quer %q", in, r.Version, ok, want)
		}
	}
	// sem [Unreleased] cai no bloco mais novo; sem nada, não há o que mostrar
	r, ok := changelog.For(rels[1:], "dev")
	if !ok || r.Version != "0.2.0" {
		t.Errorf("sem unreleased: %q %v", r.Version, ok)
	}
	if _, ok := changelog.For(nil, "dev"); ok {
		t.Error("changelog vazio deveria dar ok=false")
	}
}

func TestKey(t *testing.T) {
	for in, want := range map[string]string{"v0.2.0": "0.2.0", "dev": "dev", "": "dev", " v1.0.0+x ": "1.0.0"} {
		if got := changelog.Key(in); got != want {
			t.Errorf("Key(%q) = %q, quer %q", in, got, want)
		}
	}
}

// O CHANGELOG embutido no binário é o do repositório e tem o bloco [Unreleased] para as versões de desenvolvimento.
func TestEmbeddedChangelog(t *testing.T) {
	rels := changelog.Parse(hanko.Changelog)
	if len(rels) < 2 {
		t.Fatalf("o CHANGELOG embutido tem %d blocos", len(rels))
	}
	if r, ok := changelog.For(rels, "dev"); !ok || r.Version != changelog.UnreleasedVersion || r.Body == "" {
		t.Errorf("dev deveria cair no [Unreleased] com conteúdo: %+v", r)
	}
	if r, ok := changelog.For(rels, "v0.2.0"); !ok || r.Version != "0.2.0" {
		t.Errorf("0.2.0 deveria existir: %+v", r)
	}
}
