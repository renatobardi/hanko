package domain

import (
	"strings"
	"testing"
)

func TestParseStepActions(t *testing.T) {
	text := "## Decisão\nVamos seguir.\n\n## Ações\n- a1: rodar o deploy\n- a2: aprovar `pedido:20261031-101500-deploy` antes\n" +
		"- a1: repetido não vale\n- sem id aqui\n1. b3: item ordenado\n   continuação\n- `x`: código não é id\n\n" +
		"Parágrafo solto.\n\n- c1: depois do parágrafo ainda é lista da seção\n\n## Detalhe\n- d1: fora da seção\n"
	got := ParseStepActions(text)
	want := []StepAction{{"a1", ""}, {"a2", "20261031-101500-deploy"}, {"b3", ""}, {"c1", ""}}
	if len(got) != len(want) {
		t.Fatalf("ações = %+v, quer %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ação %d = %+v, quer %+v", i, got[i], want[i])
		}
	}
}

func TestParseStepActionsEdges(t *testing.T) {
	if got := ParseStepActions("- a1: sem seção"); len(got) != 0 {
		t.Errorf("lista fora da seção Ações = %+v, quer nenhuma", got)
	}
	fenced := "## Ações\n```\n- a1: dentro da cerca\n```\n- a2: depois\n"
	if got := ParseStepActions(fenced); len(got) != 1 || got[0].ID != "a2" {
		t.Errorf("a cerca de código não conta: %+v", got)
	}
	if got := ParseStepActions("## Ações\n- A1: id em maiúscula\n- abcdefghijklmnopq: id longo demais\n"); len(got) != 0 {
		t.Errorf("ids inválidos = %+v, quer nenhuma", got)
	}
	if got := ParseStepActions("## Ações\n- a1: ver `pedido:ID-FORA-DO-FORMATO` aqui\n"); len(got) != 1 || got[0].Approval != "" {
		t.Errorf("pedido fora do formato do canal fica sem chave: %+v", got)
	}
	if got := ParseStepActions("## Ações\n- a1: x\n" + strings.Repeat("y", StepActionMaxText)); got != nil {
		t.Errorf("texto acima de 32 KiB = %+v, quer nada", got)
	}
}
