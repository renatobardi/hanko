package skillmd

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name, in string
		want     Doc
		wantErr  bool
	}{
		{"completo", "---\nname: revisar-pr\ndescription: Revisa um PR com critério\n---\n\n# Passos\n\n1. Leia o diff\n",
			Doc{Name: "revisar-pr", Description: "Revisa um PR com critério", Body: "# Passos\n\n1. Leia o diff", HasFrontmatter: true}, false},
		{"sem frontmatter", "# Só corpo\n\ntexto", Doc{Body: "# Só corpo\n\ntexto"}, false},
		{"crlf e bom", "\ufeff---\r\nname: a\r\ndescription: \"b: com dois-pontos\"\r\n---\r\ncorpo\r\n",
			Doc{Name: "a", Description: "b: com dois-pontos", Body: "corpo", HasFrontmatter: true}, false},
		{"descrição multilinha", "---\nname: a\ndescription: >\n  uma\n  duas\n---\nx", Doc{Name: "a", Description: "uma duas", Body: "x", HasFrontmatter: true}, false},
		{"frontmatter vazio", "---\n---\ncorpo", Doc{Body: "corpo", HasFrontmatter: true}, false},
		{"não fechado", "---\nname: a\ncorpo", Doc{}, true},
		{"yaml inválido", "---\nname: [a\n---\ncorpo", Doc{}, true},
		{"--- no meio do corpo não é frontmatter", "texto\n---\nmais", Doc{Body: "texto\n---\nmais"}, false},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

// O teto vale antes do parse YAML: um texto enorme nem chega ao decoder.
func TestParseRejectsOversizedSource(t *testing.T) {
	big := "---\nname: a\ndescription: " + strings.Repeat("x ", MaxSourceBytes) + "\n---\ncorpo"
	if _, err := Parse(big); err == nil || !strings.Contains(err.Error(), "KiB") {
		t.Fatalf("frontmatter gigante: %v", err)
	}
	if _, err := Parse(strings.Repeat("y", MaxSourceBytes+1)); err == nil {
		t.Fatal("corpo gigante sem frontmatter")
	}
	if _, err := Parse("---\nname: a\ndescription: d\n---\n" + strings.Repeat("y", domain.MaxSkillBodyBytes)); err != nil {
		t.Fatalf("corpo no limite com frontmatter: %v", err)
	}
}

func TestRoundTripWithDomainMD(t *testing.T) {
	s := domain.Skill{Slug: "meu-skill", Description: "Faz: \"coisas\" com acentuação", Body: "# Título\n\ncorpo --- com traços"}
	d, err := Parse(s.MD())
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != s.Slug || d.Description != s.Description || d.Body != s.Body {
		t.Fatalf("round trip: %+v", d)
	}
}
