package service

import (
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

func testBook() *priceBook {
	row := func(from int64, in, out float64) priceRow {
		return priceRow{id: domain.NewID(), from: from, input: in, output: out, cacheRead: in / 10, cacheCreation: in * 1.25}
	}
	b := &priceBook{by: map[string][]priceRow{
		"claude-opus":   {row(100, 15, 75), row(200, 10, 50)},
		"claude-opus-5": {row(0, 20, 100)},
		"gpt-":          {row(0, 5, 20)},
	}}
	b.keys = []string{"claude-opus-5", "claude-opus", "gpt-"}
	return b
}

func TestPriceBookLookup(t *testing.T) {
	b := testBook()
	for _, c := range []struct {
		model string
		at    int64
		in    float64
		ok    bool
	}{
		{"claude-opus-5-5", 0, 20, true},   // o prefixo mais longo ganha
		{"claude-opus-4-1", 50, 15, true},  // antes da primeira linha vale a primeira
		{"claude-opus-4-1", 150, 15, true}, // dentro da primeira vigência
		{"claude-opus-4-1", 250, 10, true}, // a linha nova vale a partir do início dela
		{"CLAUDE-OPUS-4-1", 250, 10, true}, // sem diferenciar a caixa
		{"openai/gpt-6-luna", 0, 5, true},  // tira o prefixo do provedor
		{"openrouter/x/gpt-6", 0, 5, true}, // só o último trecho
		{"glm-5.3", 0, 0, false},           // sem preço
		{"", 0, 0, false},
	} {
		p, ok := b.lookup(c.model, c.at)
		if ok != c.ok || (ok && p.input != c.in) {
			t.Errorf("lookup(%q, %d) = %v %v; quer %v %v", c.model, c.at, p.input, ok, c.in, c.ok)
		}
	}
}

func TestClassifyCost(t *testing.T) {
	b := testBook()
	price, ok := b.lookup("claude-opus-4-1", 150)
	if !ok {
		t.Fatal("preço")
	}
	real := 0.5
	for _, c := range []struct {
		name       string
		reported   *float64
		sub, agent string
		price      bool
		want       domain.CallCostClass
		wantReal   bool
	}{
		{"custo real", &real, "claude", "claude", true, domain.CallCostReal, true},
		{"custo real sem preço", &real, "claude", "claude", false, domain.CallCostReal, true},
		{"assinatura sem custo real: de lista", nil, "codex", "codex", true, domain.CallCostListed, false},
		{"histórico sem a chave: o agente claude é de assinatura", nil, "", "claude", true, domain.CallCostListed, false},
		{"fora das assinaturas: estimado", nil, "", "ai-memory", true, domain.CallCostEstimated, false},
		{"sem preço: unpriced", nil, "claude", "claude", false, domain.CallCostUnpriced, false},
		{"zai descarta o custo informado", &real, "zai", "claude", true, domain.CallCostListed, false},
		{"zai sem preço: unpriced", &real, "zai", "claude", false, domain.CallCostUnpriced, false},
	} {
		p := priceRow{}
		if c.price {
			p = price
		}
		got := classifyCost(c.reported, c.sub, c.agent, p, c.price, 1000, 100, 500, 0)
		if got.class != c.want || (got.real != nil) != c.wantReal {
			t.Errorf("%s: classe %q (real %v); quer %q (real %v)", c.name, got.class, got.real, c.want, c.wantReal)
		}
		if c.price && got.list == nil {
			t.Errorf("%s: com preço o custo de lista tem de ser gravado", c.name)
		}
		if !c.price && got.list != nil {
			t.Errorf("%s: sem preço o custo de lista fica vazio, nunca zero", c.name)
		}
	}
	// (1000×15 + 100×75 + 500×1,5) ÷ 1e6
	if got := price.estimate(1000, 100, 500, 0); got < 0.02324 || got > 0.02326 {
		t.Errorf("estimate = %v, quer 0,02325", got)
	}
	if got := price.estimate(-5, 0, 0, 0); got != 0 {
		t.Errorf("token negativo conta zero: %v", got)
	}
}
