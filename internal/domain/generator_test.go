package domain

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestGenerateDefaultIs32BytesBase64(t *testing.T) {
	g, err := Generate(GenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Value) != 43 { // 32 bytes em base64url sem preenchimento
		t.Fatalf("len = %d, quer 43", len(g.Value))
	}
	if g.EntropyBits != 256 {
		t.Fatalf("entropia = %d, quer 256", g.EntropyBits)
	}
}

func TestGenerateCharsHonorsLengthAndClasses(t *testing.T) {
	g, err := Generate(GenOptions{Length: 20, Classes: []GenClass{GenDigits, GenSymbols}})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Value) != 20 {
		t.Fatalf("len = %d, quer 20", len(g.Value))
	}
	if !strings.ContainsAny(g.Value, genAlphabets[GenDigits]) || !strings.ContainsAny(g.Value, genAlphabets[GenSymbols]) {
		t.Fatalf("falta uma das classes pedidas: %q", g.Value)
	}
	if strings.ContainsAny(g.Value, genAlphabets[GenLower]+genAlphabets[GenUpper]) {
		t.Fatalf("caractere fora das classes pedidas: %q", g.Value)
	}
	if want := 91; g.EntropyBits != want { // 20 × log2(24)
		t.Fatalf("entropia = %d, quer %d", g.EntropyBits, want)
	}
}

func TestGenerateCharsDefaultClassesCoverAllThree(t *testing.T) {
	// sem classes, vale minúsculas, maiúsculas e dígitos: cada uma aparece ao menos uma vez
	for i := 0; i < 20; i++ {
		g, err := Generate(GenOptions{Length: 12})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.ContainsAny(g.Value, genAlphabets[GenLower]) || !strings.ContainsAny(g.Value, genAlphabets[GenUpper]) ||
			!strings.ContainsAny(g.Value, genAlphabets[GenDigits]) {
			t.Fatalf("classe padrão faltando: %q", g.Value)
		}
	}
}

func TestGeneratePassphraseWordsFromTheList(t *testing.T) {
	g, err := Generate(GenOptions{PassphraseWords: 5})
	if err != nil {
		t.Fatal(err)
	}
	words := strings.Split(g.Value, passphraseSep)
	if len(words) != 5 {
		t.Fatalf("palavras = %d, quer 5: %q", len(words), g.Value)
	}
	for _, w := range words {
		if !slices.Contains(passphraseWords, w) {
			t.Fatalf("palavra %q não está na lista embutida", w)
		}
	}
	if want := 49; g.EntropyBits != want { // 5 × log2(1023)
		t.Fatalf("entropia = %d, quer %d", g.EntropyBits, want)
	}
}

func TestPassphraseListIsCleanAndUnique(t *testing.T) {
	if len(passphraseWords) != 1023 {
		t.Fatalf("lista tem %d palavras, quer 1023", len(passphraseWords))
	}
	seen := map[string]bool{}
	for _, w := range passphraseWords {
		if seen[w] {
			t.Fatalf("palavra repetida: %q", w)
		}
		seen[w] = true
		if len(w) < 3 || len(w) > 12 {
			t.Fatalf("palavra fora de 3 a 12 letras: %q", w)
		}
	}
}

func TestGenOptionsValidation(t *testing.T) {
	cases := []struct {
		name  string
		opts  GenOptions
		field string
	}{
		{"tamanho abaixo do mínimo", GenOptions{Length: 7}, "length"},
		{"tamanho acima do máximo", GenOptions{Length: 129}, "length"},
		{"classe desconhecida", GenOptions{Length: 12, Classes: []GenClass{"emoji"}}, "classes"},
		{"frase com menos de 3 palavras", GenOptions{PassphraseWords: 2}, "passphrase_words"},
		{"frase com mais de 12 palavras", GenOptions{PassphraseWords: 13}, "passphrase_words"},
		{"frase combinada com tamanho", GenOptions{PassphraseWords: 4, Length: 12}, "generator"},
		{"frase combinada com classes", GenOptions{PassphraseWords: 4, Classes: []GenClass{GenLower}}, "generator"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Generate(c.opts)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("erro = %v, quer ValidationError", err)
			}
			if ve.Field != c.field {
				t.Fatalf("campo = %q, quer %q", ve.Field, c.field)
			}
		})
	}
}

func TestGenOptionsIsZero(t *testing.T) {
	if !(GenOptions{}).IsZero() {
		t.Fatal("zero value deveria ser IsZero")
	}
	if (GenOptions{Length: 8}).IsZero() {
		t.Fatal("tamanho pedido não é zero")
	}
}

func TestEstimateEntropy(t *testing.T) {
	cases := []struct {
		value string
		want  int
	}{
		{"", 0},
		{"abc", 14},      // 3 × log2(26)
		{"senha123", 41}, // 8 × log2(36): minúsculas e dígitos
		{"password", 37}, // 8 × log2(26)
		{"Abc-123!", 52}, // 8 × log2(95): as quatro classes
	}
	for _, c := range cases {
		if got := EstimateEntropy(c.value); got != c.want {
			t.Errorf("EstimateEntropy(%q) = %d, quer %d", c.value, got, c.want)
		}
	}
}

func TestIsPasswordField(t *testing.T) {
	for _, name := range []string{"password", "senha", "Senha", "PASSWORD"} {
		if !IsPasswordField(name) {
			t.Errorf("%q deveria ser campo de senha", name)
		}
	}
	for _, name := range []string{"usuario", "token", "password_hash"} {
		if IsPasswordField(name) {
			t.Errorf("%q não deveria ser campo de senha", name)
		}
	}
}
