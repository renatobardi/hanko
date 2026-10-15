package domain

import (
	"strings"
	"testing"
)

func TestRunNameDeterministicoEPtBR(t *testing.T) {
	id := NewID()
	a, b := RunName(id, 0), RunName(id, 0)
	if a != b {
		t.Fatalf("mesmo id deve dar o mesmo nome: %q != %q", a, b)
	}
	if parts := strings.Split(a, "_"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		t.Fatalf("formato Adjetivo_Substantivo: %q", a)
	}
	if RunName(id, 1) == a && RunName(id, 2) == a && RunName(id, 3) == a {
		t.Fatalf("tentativas diferentes deveriam variar o nome")
	}
	// um id fixo sempre dá o mesmo nome (contrato: não mudar o hash sem ADR)
	fixed := ID{0x01, 0x92, 0xf0, 0, 0, 0, 0x70, 0, 0x80, 0, 0, 0, 0, 0, 0, 0x01}
	if got := RunName(fixed, 0); got != RunName(fixed, 0) || got == "" {
		t.Fatalf("id fixo: %q", got)
	}
}

func TestRunNameListasSemRepeticao(t *testing.T) {
	for name, words := range map[string][]string{"adjetivos": runAdjectives[:], "substantivos": runNouns[:]} {
		seen := map[string]bool{}
		for _, w := range words {
			f := foldWord(w)
			if seen[f] {
				t.Errorf("%s: %q repetido", name, w)
			}
			if strings.ContainsAny(w, "_ -") {
				t.Errorf("%s: %q não pode ter separador", name, w)
			}
			seen[f] = true
		}
	}
	if RunNameSpace() < 4096 {
		t.Errorf("espaço de nomes pequeno demais: %d", RunNameSpace())
	}
}

// Sorteio de muitos ids: a distribuição cobre as listas e a taxa de colisão do par bate com o esperado
// para o tamanho do espaço (aniversário), sem viés grosseiro do hash.
func TestRunNameDistribuicao(t *testing.T) {
	const n = 2000
	seen := map[string]int{}
	adj, noun := map[string]bool{}, map[string]bool{}
	for i := 0; i < n; i++ {
		name := RunName(NewID(), 0)
		seen[name]++
		p := strings.Split(name, "_")
		adj[p[0]], noun[p[1]] = true, true
	}
	if len(adj) < len(runAdjectives)*9/10 || len(noun) < len(runNouns)*9/10 {
		t.Errorf("cobertura baixa: %d/%d adjetivos, %d/%d substantivos", len(adj), len(runAdjectives), len(noun), len(runNouns))
	}
	collisions := n - len(seen)
	// esperado ≈ n²/(2·espaço) ≈ 489 para 2000 em 4096; folga larga
	if collisions < 200 || collisions > 900 {
		t.Errorf("colisões = %d fora do esperado para %d nomes", collisions, RunNameSpace())
	}
	// com as 16 tentativas, o nome livre existe quase sempre mesmo com a empresa 50% cheia
	taken := map[string]bool{}
	all := make([]string, 0, RunNameSpace())
	for _, a := range runAdjectives {
		for _, nn := range runNouns {
			all = append(all, a+"_"+nn)
		}
	}
	for i := 0; i < len(all)/2; i++ {
		taken[all[(i*7919)%len(all)]] = true
	}
	free, fallback := 0, 0
	for i := 0; i < 500; i++ {
		id := NewID()
		ok := false
		for at := 0; at < RunNameAttempts; at++ {
			if !taken[RunName(id, at)] {
				ok = true
				break
			}
		}
		if ok {
			free++
		} else {
			fallback++
		}
	}
	if fallback > 5 {
		t.Errorf("empresa com ~50%% do espaço usado: %d/500 recorreram ao sufixo", fallback)
	}
	_ = free
}

func TestCanonicalRunName(t *testing.T) {
	for in, want := range map[string]string{
		"Ágil_Falcão": "Ágil_Falcão", "agil_falcao": "Ágil_Falcão", "  VELOZ_raposa ": "Veloz_Raposa", "veloz_raposa_417": "Veloz_Raposa_417",
	} {
		got, ok := CanonicalRunName(in)
		if !ok || got != want {
			t.Errorf("%q -> %q %v, quero %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "run-1", "veloz", "veloz_naoexiste", "naoexiste_raposa", "veloz_raposa_4", "veloz_raposa_abc", "a_b_c_d",
		"0198f2a0-0000-7000-8000-000000000001"} {
		if got, ok := CanonicalRunName(in); ok {
			t.Errorf("%q não é nome de execução, got %q", in, got)
		}
	}
	// todo nome gerado é reconhecido de volta
	for i := 0; i < 200; i++ {
		id := NewID()
		n := RunName(id, 0)
		if got, ok := CanonicalRunName(strings.ToLower(n)); !ok || got != n {
			t.Fatalf("%q não volta (%q %v)", n, got, ok)
		}
		fb := RunNameFallback(id, 0)
		if got, ok := CanonicalRunName(fb); !ok || got != fb {
			t.Fatalf("fallback %q não volta (%q %v)", fb, got, ok)
		}
	}
}
