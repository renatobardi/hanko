package domain

import (
	"errors"
	"fmt"
	"regexp"
	"testing"
)

var externalWordPattern = regexp.MustCompile(`^[A-Z][a-z]+$`)

func TestExternalNameWordsFollowTheListFormat(t *testing.T) {
	adjectives, nouns := ExternalNameWords()
	if len(adjectives) == 0 || len(nouns) == 0 {
		t.Fatalf("listas vazias: %d adjetivos, %d substantivos", len(adjectives), len(nouns))
	}
	for _, w := range append(append([]string(nil), adjectives...), nouns...) {
		if !externalWordPattern.MatchString(w) {
			t.Errorf("palavra %q fora do formato Aaaa (ASCII, maiúscula inicial)", w)
		}
	}
	if got, want := ExternalNameSpace(), len(adjectives)*len(nouns); got != want {
		t.Errorf("ExternalNameSpace() = %d, quero %d", got, want)
	}
}

func TestExternalNameFollowsTheContractPattern(t *testing.T) {
	for i := range 200 {
		seed := fmt.Sprintf("sess-%d", i)
		for a := range 3 {
			if name := ExternalName(seed, a); !ValidExternalConversationName(name) {
				t.Fatalf("ExternalName(%q, %d) = %q não segue o formato do contrato", seed, a, name)
			}
		}
		if name := ExternalNameFallback(seed, i); !ValidExternalConversationName(name) {
			t.Fatalf("ExternalNameFallback(%q) = %q não segue o formato do contrato", seed, name)
		}
	}
}

func TestExternalNameIsStableForTheSameText(t *testing.T) {
	if a, b := ExternalName("sess-abc", 0), ExternalName("sess-abc", 0); a != b {
		t.Fatalf("o mesmo texto deu nomes diferentes: %q e %q", a, b)
	}
	if ExternalName("sess-abc", 0) == ExternalName("sess-abc", 1) {
		t.Fatal("a tentativa 1 deveria dar outro nome que a 0 para este texto")
	}
}

// Um nome só se repete dentro da empresa se o predicado disser que está livre. Com 3000 textos num espaço de 4096
// nomes, há colisões de hash: o teste confere que a escolha nunca devolve um nome já usado.
func TestChooseExternalNameNeverRepeatsInACompany(t *testing.T) {
	used := map[string]bool{}
	taken := func(name string) (bool, error) { return used[name], nil }
	seen := map[string]bool{}
	for i := range 3000 {
		name, err := ChooseExternalName(fmt.Sprintf("sess-%d", i), taken)
		if err != nil {
			t.Fatalf("texto %d: %v", i, err)
		}
		if seen[name] {
			t.Fatalf("texto %d repetiu o nome %q", i, name)
		}
		if !ValidExternalConversationName(name) {
			t.Fatalf("texto %d: nome %q fora do formato", i, name)
		}
		seen[name] = true
		used[name] = true
	}
}

// Força a colisão: acha dois textos cuja tentativa 0 é o mesmo nome. O segundo tem de sair na tentativa seguinte.
func TestChooseExternalNameRetriesOnCollision(t *testing.T) {
	first := map[string]string{}
	var a, b string
	for i := 0; a == ""; i++ {
		seed := fmt.Sprintf("sess-%d", i)
		name := ExternalName(seed, 0)
		if prev, ok := first[name]; ok {
			a, b = prev, seed
		}
		first[name] = seed
	}
	taken := map[string]bool{ExternalName(a, 0): true}
	name, err := ChooseExternalName(b, func(n string) (bool, error) { return taken[n], nil })
	if err != nil {
		t.Fatal(err)
	}
	if want := ExternalName(b, 1); name != want {
		t.Fatalf("texto colidido saiu como %q, quero a tentativa 1 %q", name, want)
	}
}

func TestChooseExternalNameFallsBackToASuffix(t *testing.T) {
	seed := "sess-x"
	taken := map[string]bool{}
	for a := range RunNameAttempts {
		taken[ExternalName(seed, a)] = true
	}
	name, err := ChooseExternalName(seed, func(n string) (bool, error) { return taken[n], nil })
	if err != nil {
		t.Fatal(err)
	}
	if !ValidExternalConversationName(name) {
		t.Fatalf("nome com sufixo %q fora do formato", name)
	}
	if got := len(name); got <= len(ExternalName(seed, 0)) {
		t.Fatalf("o sufixo deveria acrescentar três dígitos: %q", name)
	}
}

func TestChooseExternalNameExhausted(t *testing.T) {
	_, err := ChooseExternalName("sess-y", func(string) (bool, error) { return true, nil })
	if !errors.Is(err, ErrExternalNameExhausted) || !errors.Is(err, ErrConflict) {
		t.Fatalf("erro = %v, quero ErrExternalNameExhausted que é ErrConflict", err)
	}
}

func TestChooseExternalNamePropagatesTheLookupError(t *testing.T) {
	boom := errors.New("banco caiu")
	_, err := ChooseExternalName("sess-z", func(string) (bool, error) { return false, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("erro = %v, quero o do predicado", err)
	}
}
