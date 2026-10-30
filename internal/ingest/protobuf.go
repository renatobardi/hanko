package ingest

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Signal é o sinal de um corpo OTLP.
type Signal string

const (
	SignalTraces  Signal = "traces"
	SignalLogs    Signal = "logs"
	SignalMetrics Signal = "metrics"
)

// Valid informa se o sinal é conhecido.
func (s Signal) Valid() bool { return s == SignalTraces || s == SignalLogs || s == SignalMetrics }

// FromProtobuf lê um corpo OTLP/HTTP em protobuf e o devolve no mesmo modelo genérico do OTLP JSON (nomes em camelCase,
// int64 como texto, enum como número, ids em hexadecimal), para que as linhas e as chaves de dedupe saiam do mesmo código
// que lê o JSON do replay (ADR-0076 itens 4 e 6).
func FromProtobuf(sig Signal, body []byte) (any, error) {
	var msg proto.Message
	switch sig {
	case SignalTraces:
		msg = &coltrace.ExportTraceServiceRequest{}
	case SignalLogs:
		msg = &collogs.ExportLogsServiceRequest{}
	case SignalMetrics:
		msg = &colmetrics.ExportMetricsServiceRequest{}
	default:
		return nil, bad("sinal desconhecido %q", sig)
	}
	if err := proto.Unmarshal(body, msg); err != nil {
		return nil, bad("protobuf: %v", err)
	}
	js, err := protojson.MarshalOptions{UseEnumNumbers: true}.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("protojson: %w", err)
	}
	v, err := DecodeJSON(js)
	if err != nil {
		return nil, err
	}
	hexIDs(v)
	return v, nil
}

// hexIDs troca, em todo o documento, os ids de trace e de span de base64 (o JSON do protobuf) para hexadecimal (o
// OTLP JSON do collector). Os nomes `traceId`, `spanId` e `parentSpanId` só aparecem como chave de objeto nas
// estruturas do OTLP; os atributos citam esses nomes como valor, nunca como chave.
func hexIDs(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if k == "traceId" || k == "spanId" || k == "parentSpanId" {
				if s, ok := e.(string); ok {
					if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
						x[k] = hex.EncodeToString(raw)
					}
				}
				continue
			}
			hexIDs(e)
		}
	case []any:
		for _, e := range x {
			hexIDs(e)
		}
	}
}
