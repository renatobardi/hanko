package textdiff

import (
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	a := "um\ndois\ntres\nquatro\ncinco\nseis\nsete\noito\nnove\ndez"
	b := "um\ndois\ntres\nquatro\ncinco\nseis\nsete\noito\nnove\nDEZ\nonze"
	got := Unified(Lines(a, b), 1, 0)
	for _, want := range []string{"@@ 8 linhas iguais @@\n", "  nove\n", "- dez\n", "+ DEZ\n", "+ onze\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
	if strings.Contains(got, "um\n") {
		t.Errorf("o contexto distante não deveria aparecer:\n%s", got)
	}

	// sem mudança: nada além do trecho igual omitido
	if same := Unified(Lines("a\nb", "a\nb"), 1, 0); strings.ContainsAny(same, "+-") && strings.Contains(same, "\n- ") {
		t.Errorf("sem mudança não há linha alterada: %q", same)
	}

	// corte por tamanho: termina com o aviso e cabe no teto (mais a linha do aviso)
	long := Unified(Lines("", strings.Repeat("linha longa de teste\n", 200)), 0, 100)
	if !strings.Contains(long, "diff cortado") || len(long) > 100+60 {
		t.Errorf("corte: len=%d %q", len(long), long[max(0, len(long)-80):])
	}
}
