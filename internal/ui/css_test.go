package ui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Uma chave sem par em hanko.css aninha todas as regras seguintes (um @media sem fechar as deixa valendo só no
// celular). O e2e (ADR-0058) achou um caso desses; este teste barra a volta.
func TestStylesheetBracesBalanced(t *testing.T) {
	b, err := os.ReadFile("static/hanko.css")
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(b), "")
	depth := 0
	for i, line := range strings.Split(css, "\n") {
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth < 0 {
			t.Fatalf("hanko.css: '}' sobrando perto da linha %d", i+1)
		}
	}
	if depth != 0 {
		t.Fatalf("hanko.css: %d bloco(s) sem fechar; as regras seguintes ficam aninhadas", depth)
	}
}
