package domain

import (
	"fmt"
	"strings"
	"testing"
)

func TestExternalNameDerivedFromID(t *testing.T) {
	if got := ExternalNameSpace(); got != 256*256 {
		t.Fatalf("ExternalNameSpace = %d, quer 65536", got)
	}
	id := NewID()
	a := ExternalName(id, 0)
	if a == "" || a != ExternalName(id, 0) {
		t.Fatalf("o mesmo id e a mesma tentativa devem dar o mesmo nome: %q", a)
	}
	if !ValidExternalConversationName(a) {
		t.Errorf("%q fora do formato do gerador", a)
	}
	distinct := map[string]bool{}
	for attempt := 0; attempt < ExternalNameAttempts; attempt++ {
		distinct[ExternalName(id, attempt)] = true
	}
	if len(distinct) < ExternalNameAttempts-2 {
		t.Errorf("as %d tentativas deram só %d nomes distintos", ExternalNameAttempts, len(distinct))
	}
	// 1000 ids: o espaço de 65.536 nomes dá poucas colisões (paradoxo do aniversário, ~7 esperadas)
	seen := map[string]int{}
	for i := 0; i < 1000; i++ {
		seen[ExternalName(NewID(), 0)]++
	}
	if len(seen) < 960 {
		t.Errorf("1000 ids deram só %d nomes distintos", len(seen))
	}
}

func TestExternalNameFallbackFormat(t *testing.T) {
	id := NewID()
	for n := 0; n < 50; n++ {
		f := ExternalNameFallback(id, n)
		if !ValidExternalConversationName(f) || !strings.HasPrefix(f, ExternalName(id, 0)+"_") {
			t.Fatalf("fallback %q fora do formato ou sem o nome da tentativa 0", f)
		}
		if suffix := f[len(f)-3:]; suffix[0] == '0' {
			t.Errorf("sufixo %q começa em zero", suffix)
		}
	}
}

func TestChooseExternalName(t *testing.T) {
	id := NewID()
	if got := ChooseExternalName(id, func(string) bool { return false }); got != ExternalName(id, 0) {
		t.Errorf("sem colisão, quer a tentativa 0, tem %q", got)
	}
	// as 3 primeiras tentativas já existem: vale a quarta
	taken := map[string]bool{}
	for a := 0; a < 3; a++ {
		taken[ExternalName(id, a)] = true
	}
	if got := ChooseExternalName(id, func(n string) bool { return taken[n] }); taken[got] || got == "" {
		t.Errorf("devia sair uma variação livre, saiu %q", got)
	}
	// as 16 tentativas já existem: entra o sufixo de 3 dígitos
	for a := 0; a < ExternalNameAttempts; a++ {
		taken[ExternalName(id, a)] = true
	}
	got := ChooseExternalName(id, func(n string) bool { return taken[n] })
	if len(got) < 4 || !ValidExternalConversationName(got) || strings.Count(got, "_") != 2 {
		t.Errorf("com as 16 tentativas tomadas, quer o sufixo de 3 dígitos, tem %q", got)
	}
	// nada serve: devolve vazio
	if got := ChooseExternalName(id, func(string) bool { return true }); got != "" {
		t.Errorf("com tudo tomado, quer vazio, tem %q", got)
	}
}

func TestDrawExternalName(t *testing.T) {
	first := ExternalName(NewID(), 0)
	// com um nome tomado, o sorteio nunca o devolve, mesmo apontando para o índice dele
	adjs, nouns := externalNameLists()
	taken := []string{adjs[0] + "_" + nouns[0]}
	got, ok := DrawExternalName(taken, func(int) int { return 0 })
	if !ok || got == taken[0] || !ValidExternalConversationName(got) {
		t.Errorf("sorteio = %q %v; o primeiro estava tomado", got, ok)
	}
	for i := 0; i < 200; i++ {
		n, ok := DrawExternalName([]string{first, taken[0]}, RandomIndex)
		if !ok || n == first || n == taken[0] {
			t.Fatalf("sorteio devolveu %q (tomados: %q, %q)", n, first, taken[0])
		}
	}
	// o último índice livre é alcançável
	last := adjs[len(adjs)-1] + "_" + nouns[len(nouns)-1]
	if n, _ := DrawExternalName(nil, func(n int) int { return n - 1 }); n != last {
		t.Errorf("o último nome livre é %q, saiu %q", last, n)
	}
	// nada livre
	all := make([]string, 0, len(adjs)*len(nouns))
	for _, a := range adjs {
		for _, n := range nouns {
			all = append(all, fmt.Sprintf("%s_%s", a, n))
		}
	}
	if _, ok := DrawExternalName(all, RandomIndex); ok {
		t.Error("com os 65.536 nomes tomados, não devia sobrar nenhum")
	}
	// texto que não é nome é ignorado
	if _, ok := DrawExternalName([]string{"lixo", "A_B_C", ""}, func(int) int { return 0 }); !ok {
		t.Error("texto fora do formato não ocupa nome")
	}
}
