package pricecheck

import (
	"math"
	"sort"
)

// Resultado de um modelo numa conferência (ADR-0082 item 7).
const (
	StatusEqual         = "equal"
	StatusChanged       = "changed"
	StatusNew           = "new"
	StatusDiverges      = "diverges"
	StatusLockedDiffers = "locked_differs"
	StatusNoSource      = "no_source"
	StatusSourceDown    = "source_down"
)

// Price é um preço de modelo, em USD por 1M tokens nos quatro eixos.
type Price struct{ Input, Output, CacheRead, CacheCreation float64 }

func (p Price) fields() map[string]float64 {
	return map[string]float64{"input": p.Input, "output": p.Output, "cache_read": p.CacheRead, "cache_creation": p.CacheCreation}
}

func close64(x, y float64) bool {
	return math.Abs(x-y) <= 1e-9*math.Max(1, math.Max(math.Abs(x), math.Abs(y)))
}

// Same diz se dois preços são iguais nos quatro eixos (tolerância de arredondamento, 1e-9 relativo).
func Same(a, b Price) bool {
	return close64(a.Input, b.Input) && close64(a.Output, b.Output) && close64(a.CacheRead, b.CacheRead) && close64(a.CacheCreation, b.CacheCreation)
}

// Agree junta duas leituras parciais: se concordam, devolve o preço; senão, os campos que divergem. `input` e `output` têm
// de ser iguais. Cache presente nas duas é igual; só numa fonte vale a que tem; em nenhuma, o preço de `input`.
func Agree(a, b Partial) (Price, []string, bool) {
	var bad []string
	vals := map[string]*float64{}
	for _, f := range Fields {
		x, hasX := a[f]
		y, hasY := b[f]
		if hasX && hasY && !close64(x, y) {
			bad = append(bad, f)
		}
		switch {
		case hasX:
			v := x
			vals[f] = &v
		case hasY:
			v := y
			vals[f] = &v
		}
	}
	if len(bad) > 0 {
		return Price{}, bad, false
	}
	in := *vals["input"]
	get := func(f string) float64 {
		if v := vals[f]; v != nil {
			return *v
		}
		return in
	}
	return Price{Input: in, Output: *vals["output"], CacheRead: get("cache_read"), CacheCreation: get("cache_creation")}, nil, true
}

// Read é o que uma fonte disse de um modelo: o preço parcial, "não tem o modelo" ou o código da falha da fonte.
type Read struct {
	Partial Partial
	Missing bool
	Err     string
}

// SourceResult é o resultado de buscar uma fonte inteira.
type SourceResult struct {
	Data map[SourceID]Partial
	Err  string
}

// ReadsFor devolve a leitura de cada fonte para o modelo, pelo mapeamento de id. Modelo sem regra de id vale como "a fonte
// não tem".
func ReadsFor(model string, results map[string]SourceResult) map[string]Read {
	ids := SourceIDs(model)
	out := map[string]Read{}
	for source, res := range results {
		if res.Err != "" {
			out[source] = Read{Err: res.Err}
			continue
		}
		id, ok := ids[source]
		if !ok {
			out[source] = Read{Missing: true}
			continue
		}
		if p, found := res.Data[id]; found {
			out[source] = Read{Partial: p}
		} else {
			out[source] = Read{Missing: true}
		}
	}
	return out
}

// Decision é o resultado de um modelo: o status, o detalhe e o preço novo (só quando as duas fontes concordam, o valor é novo
// e o modelo não está travado).
type Decision struct {
	Status string
	Detail map[string]any
	New    *Price
}

// Decide aplica as regras de troca (`prices.py:decide@845764f`). `current` é o preço vigente (nil sem preço); `locked` é a trava
// manual; `reads` é a leitura de cada fonte. Fonte fora do ar, divergência ou modelo que uma fonte não tem deixam o preço como
// está. Modelo travado com fontes que divergem entre si traz `locked` no detalhe, e o alerta não dispara.
func Decide(current *Price, locked bool, reads map[string]Read) Decision {
	var down, missing []string
	for s, r := range reads {
		switch {
		case r.Err != "":
			down = append(down, s)
		case r.Missing:
			missing = append(missing, s)
		}
	}
	sort.Strings(down)
	sort.Strings(missing)
	if len(down) > 0 {
		return Decision{Status: StatusSourceDown, Detail: map[string]any{"sources": down}}
	}
	if len(missing) > 0 {
		return Decision{Status: StatusNoSource, Detail: map[string]any{"missing": missing}}
	}
	agreed, bad, ok := Agree(reads[ModelsDev].Partial, reads[OpenRouter].Partial)
	if !ok {
		fields := map[string]any{}
		for _, f := range bad {
			fields[f] = map[string]any{ModelsDev: reads[ModelsDev].Partial[f], OpenRouter: reads[OpenRouter].Partial[f]}
		}
		return Decision{Status: StatusDiverges, Detail: map[string]any{"fields": fields, "locked": locked}}
	}
	if current == nil {
		return Decision{Status: StatusNew, Detail: map[string]any{"new": agreed.fields()}, New: &agreed}
	}
	if Same(*current, agreed) {
		return Decision{Status: StatusEqual, Detail: map[string]any{}}
	}
	if locked {
		return Decision{Status: StatusLockedDiffers, Detail: map[string]any{"locked": current.fields(), "sources": agreed.fields()}}
	}
	return Decision{Status: StatusChanged, Detail: map[string]any{"old": current.fields(), "new": agreed.fields()}, New: &agreed}
}
