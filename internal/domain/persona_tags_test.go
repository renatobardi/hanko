package domain

import (
	"strings"
	"testing"
)

func TestPersonaTagColorValid(t *testing.T) {
	for _, c := range PersonaTagColors {
		if !c.Valid() {
			t.Errorf("%q deveria ser válida", c)
		}
	}
	for _, c := range []PersonaTagColor{"", "#ff0000", "Blue", "primary"} {
		if c.Valid() {
			t.Errorf("%q está fora da paleta: deveria ser inválida", c)
		}
	}
}

func TestNormalizePersonaTagName(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"  Revisão de código ", "Revisão de código", true},
		{strings.Repeat("ç", PersonaTagNameMax), strings.Repeat("ç", PersonaTagNameMax), true},
		{strings.Repeat("ç", PersonaTagNameMax+1), "", false},
		{"   ", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := NormalizePersonaTagName(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("NormalizePersonaTagName(%q) = %q, %v; quer %q, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestPersonaTagKeyIgnoraCaixa(t *testing.T) {
	if PersonaTagKey(" REVISÃO ") != PersonaTagKey("revisão") {
		t.Fatal("a chave deve ignorar caixa e espaço nas pontas")
	}
}

func TestValidPersonaTagDescription(t *testing.T) {
	if err := ValidPersonaTagDescription(strings.Repeat("é", PersonaTagDescriptionMax)); err != nil {
		t.Fatalf("200 caracteres: %v", err)
	}
	if err := ValidPersonaTagDescription(strings.Repeat("é", PersonaTagDescriptionMax+1)); err == nil {
		t.Fatal("201 caracteres deveria falhar")
	}
}

func TestOrgFiltros(t *testing.T) {
	if !OrgTagAny.Valid() || !OrgTagAll.Valid() || OrgTagMatch("or").Valid() {
		t.Fatal("OrgTagMatch aceita só any e all")
	}
	if !OrgHighlight.Valid() || !OrgPrune.Valid() || OrgFilterMode("hide").Valid() {
		t.Fatal("OrgFilterMode aceita só highlight e prune")
	}
	if OrgLevel(0) != 1 || OrgLevel(2) != 3 {
		t.Fatal("a raiz é o nível 1")
	}
}
