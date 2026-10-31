package domain

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// As listas do gerador único de nome (ADR-0084 item 1) são dado do contrato: 256 adjetivos e 256 substantivos, só letras
// ASCII com a primeira maiúscula, sem repetir (nem trocando a caixa), com as 64 + 64 palavras do oute à frente.
func TestExternalNameWordLists(t *testing.T) {
	raw, err := os.ReadFile("names/names.txt")
	if err != nil {
		t.Fatal(err)
	}
	lists := map[string][]string{}
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "# adjectives":
			section = "adjectives"
		case line == "# nouns":
			section = "nouns"
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			lists[section] = append(lists[section], line)
		}
	}
	word := regexp.MustCompile(`^[A-Z][a-z]+$`)
	for name, first := range map[string][2]string{"adjectives": {"Agile", "Zesty"}, "nouns": {"Badger", "Writer"}} {
		words := lists[name]
		if len(words) != 256 {
			t.Fatalf("%s = %d palavras, quer 256", name, len(words))
		}
		seen := map[string]bool{}
		for _, w := range words {
			if !word.MatchString(w) {
				t.Errorf("%s: %q fora do formato ^[A-Z][a-z]+$", name, w)
			}
			if seen[strings.ToLower(w)] {
				t.Errorf("%s: %q repetida", name, w)
			}
			seen[strings.ToLower(w)] = true
		}
		if words[0] != first[0] || words[63] != first[1] {
			t.Errorf("%s: as 64 primeiras são as do oute (de %s a %s), tem %s a %s", name, first[0], first[1], words[0], words[63])
		}
	}
}
