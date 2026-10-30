package ingest

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Helpers que reproduzem a semântica do Python onde `otlp.py` depende dela.

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func getKey(m map[string]any, k string) any {
	if m == nil {
		return nil
	}
	return m[k]
}

// truthy é o `bool(v)` do Python.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case *big.Int:
		return x.Sign() != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// or devolve a se for verdadeiro, senão b (o `a or b` do Python).
func or(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
}

// toInt é o `int(v)` do Python: texto com número inteiro, número (trunca o float) ou booleano.
func toInt(v any) (*big.Int, bool) {
	switch x := v.(type) {
	case *big.Int:
		return x, true
	case bool:
		if x {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		n, _ := new(big.Float).SetFloat64(math.Trunc(x)).Int(nil)
		return n, true
	case string:
		n, ok := new(big.Int).SetString(strings.TrimSpace(x), 10)
		return n, ok
	}
	return nil, false
}

// toIntOrZero é o `to_int` de otlp.py: ausente ou texto vazio vale 0; o que não é inteiro é corpo inválido.
func toIntOrZero(v any) (*big.Int, error) {
	if v == nil || v == "" {
		return new(big.Int), nil
	}
	n, ok := toInt(v)
	if !ok {
		return nil, bad("inteiro inválido: %v", Canon(v))
	}
	return n, nil
}

// toFloat é o `float(v)` do Python.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

// numberInt é o `number(v, int)`: o valor, ou nil quando não converte.
func numberInt(v any) *int64 {
	if v == nil {
		return nil
	}
	n, ok := toInt(v)
	if !ok || !n.IsInt64() {
		return nil
	}
	i := n.Int64()
	return &i
}

// numberFloat é o `number(v, float)`.
func numberFloat(v any) *float64 {
	if v == nil {
		return nil
	}
	f, ok := toFloat(v)
	if !ok {
		return nil
	}
	return &f
}

// pyStr é o `str(v)` do Python para os valores que chegam do JSON.
func pyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return Canon(v)
}

// pyText é o `text(v)`: texto como está, outro valor em JSON canônico, ausente = nil.
func pyText(v any) *string {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		s = Canon(v)
	}
	return &s
}

// textOrNil é o `v or None` sobre um texto.
func textOrNil(v any) *string {
	if !truthy(v) {
		return nil
	}
	return pyText(v)
}

// first devolve o primeiro valor presente (não nulo) das chaves, na ordem.
func first(d map[string]any, keys []string) any {
	for _, k := range keys {
		if d[k] != nil {
			return d[k]
		}
	}
	return nil
}

func i64(n *big.Int, what string) (int64, error) {
	if !n.IsInt64() {
		return 0, bad("%s fora da faixa", what)
	}
	return n.Int64(), nil
}

func ptrInt64OrNil(n *big.Int, what string) (*int64, error) {
	if n.Sign() == 0 {
		return nil, nil
	}
	v, err := i64(n, what)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func ptrInt32OrNil(n *big.Int, what string) (*int32, error) {
	if n.Sign() == 0 {
		return nil, nil
	}
	if !n.IsInt64() {
		return nil, bad("%s fora da faixa", what)
	}
	wide := n.Int64()
	if wide > math.MaxInt32 || wide < math.MinInt32 {
		return nil, bad("%s fora da faixa", what)
	}
	v := int32(wide)
	return &v, nil
}
