package ingest

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// Os vetores de testdata/golden.json saem do `otlp.py@845764f` do agent-studio, rodado sobre os mesmos payloads por
// testdata/gen_golden.py. A chave de dedupe e as colunas têm de bater byte a byte: um replay de uma janela já migrada não
// pode gerar uma chave diferente (ADR-0076 item 6). Os payloads são sintéticos; a conferência contra fatos reais
// migrados é do ensaio da migração (fatia 6).
type golden struct {
	Received int64 `json:"received_ns"`
	Logs     goldenSet
	Spans    goldenSet
	Metrics  goldenSet
}

type goldenSet struct {
	Payload json.RawMessage   `json:"payload"`
	Rows    []json.RawMessage `json:"rows"`
}

func loadGolden(t *testing.T) (golden, map[string]goldenSet) {
	t.Helper()
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var all struct {
		Received int64     `json:"received_ns"`
		Logs     goldenSet `json:"logs"`
		Spans    goldenSet `json:"spans"`
		Metrics  goldenSet `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	return golden{Received: all.Received}, map[string]goldenSet{"logs": all.Logs, "spans": all.Spans, "metrics": all.Metrics}
}

func genericWithNumbers(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// sameJSON compara dois valores JSON; números iguais em valor (1e+20 e 100000000000000000000) valem como iguais.
func sameJSON(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		if x == y {
			return true
		}
		fx, e1 := strconv.ParseFloat(string(x), 64)
		fy, e2 := strconv.ParseFloat(string(y), 64)
		return e1 == nil && e2 == nil && fx == fy
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if w, ok := y[k]; !ok || !sameJSON(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameJSON(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

func checkRows[T any](t *testing.T, name string, set goldenSet, rows []T) {
	t.Helper()
	if len(rows) != len(set.Rows) {
		t.Fatalf("%s: %d linhas, quer %d", name, len(rows), len(set.Rows))
	}
	for i, r := range rows {
		got, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if !sameJSON(genericWithNumbers(t, set.Rows[i]), genericWithNumbers(t, got)) {
			t.Errorf("%s[%d] difere do agent-studio\n quer: %s\n  tem: %s", name, i, set.Rows[i], got)
		}
	}
}

func TestGoldenAgainstAgentStudio(t *testing.T) {
	g, sets := loadGolden(t)
	logs, err := DecodeJSON(sets["logs"].Payload)
	if err != nil {
		t.Fatal(err)
	}
	lr, err := LogRows(logs, g.Received)
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "logs", sets["logs"], lr)

	spans, err := DecodeJSON(sets["spans"].Payload)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := SpanRows(spans, g.Received)
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "spans", sets["spans"], sr)

	metrics, err := DecodeJSON(sets["metrics"].Payload)
	if err != nil {
		t.Fatal(err)
	}
	mr, err := MetricRows(metrics, g.Received)
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "metrics", sets["metrics"], mr)
}
