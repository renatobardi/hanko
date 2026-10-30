package ingest

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func kv(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

// request monta um ExportTraceServiceRequest com um resource e um escopo, e devolve o protobuf em bytes.
func request(t *testing.T, res []*commonpb.KeyValue, spans ...*tracepb.Span) []byte {
	t.Helper()
	req := &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   &resourcepb.Resource{Attributes: res},
		ScopeSpans: []*tracepb.ScopeSpans{{Scope: &commonpb.InstrumentationScope{Name: "claude-code"}, Spans: spans}},
	}}}
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func span(traceID, spanID []byte, start, end uint64, attrs ...*commonpb.KeyValue) *tracepb.Span {
	return &tracepb.Span{TraceId: traceID, SpanId: spanID, Name: "claude_code.llm_request", StartTimeUnixNano: start,
		EndTimeUnixNano: end, Attributes: attrs}
}

var (
	traceA = []byte{0xAB, 0xCD, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E}
	spanA  = []byte{0x1A, 0x2B, 0x3C, 0x4D, 0x5E, 0x6F, 0x70, 0x81}
)

func mustDecode(t *testing.T, raw []byte, received int64) []Span {
	t.Helper()
	spans, err := DecodeTraces(raw, received)
	if err != nil {
		t.Fatalf("DecodeTraces: %v", err)
	}
	return spans
}

func TestDecodeTracesDedupeKeyIsTraceAndSpanInLowerHex(t *testing.T) {
	spans := mustDecode(t, request(t, nil, span(traceA, spanA, 100, 200)), 1)
	if len(spans) != 1 {
		t.Fatalf("spans = %d, quero 1", len(spans))
	}
	want := "s:" + hex.EncodeToString(traceA) + ":" + hex.EncodeToString(spanA)
	if spans[0].DedupeKey != want || spans[0].DedupeKey != strings.ToLower(spans[0].DedupeKey) {
		t.Fatalf("dedupe = %q, quero %q em minúsculas", spans[0].DedupeKey, want)
	}
}

// O mesmo span reenviado pelo collector leva a mesma chave e a mesma hora: é isso que o banco usa para não duplicar.
func TestDecodeTracesRepeatedSpanKeepsKeyAndTime(t *testing.T) {
	raw := request(t, nil, span(traceA, spanA, 100, 200, kv("session.id", "s-1")))
	first := mustDecode(t, raw, 10)[0]
	again := mustDecode(t, raw, 999)[0]
	if first.DedupeKey != again.DedupeKey || first.TimeUnixNano != again.TimeUnixNano {
		t.Fatalf("reenvio mudou a chave ou a hora: %+v e %+v", first, again)
	}
}

func TestDecodeTracesFixedColumnsPreferTheRecordOverTheResource(t *testing.T) {
	res := []*commonpb.KeyValue{kv("oute.agent", "do-resource"), kv("service.name", "oute-agent"), kv("host.name", "oute-server")}
	raw := request(t, res, span(traceA, spanA, 100, 200, kv("oute.agent", "do-registro"), kv("session.id", "s-1")))
	got := mustDecode(t, raw, 1)[0]
	if got.OuteAgent != "do-registro" {
		t.Errorf("oute.agent = %q: o registro deveria vencer o resource", got.OuteAgent)
	}
	if got.ServiceName != "oute-agent" || got.HostName != "oute-server" {
		t.Errorf("service/host = %q/%q: sem atributo no registro, vale o resource", got.ServiceName, got.HostName)
	}
	if got.SessionID != "s-1" || got.OuteRepo != "" {
		t.Errorf("session = %q, repo = %q: ausente deve ficar vazio", got.SessionID, got.OuteRepo)
	}
}

func TestDecodeTracesFactTimeStartThenEndThenReceived(t *testing.T) {
	for _, c := range []struct {
		name       string
		start, end uint64
		want       int64
	}{
		{"com início", 100, 200, 100},
		{"sem início, com fim", 0, 500, 500},
		{"sem hora nenhuma", 0, 0, 7000},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := mustDecode(t, request(t, nil, span(traceA, spanA, c.start, c.end)), 7000)[0]
			if got.TimeUnixNano != c.want {
				t.Fatalf("hora = %d, quero %d", got.TimeUnixNano, c.want)
			}
		})
	}
}

func TestDecodeTracesDurationOnlyWhenStartIsBeforeEnd(t *testing.T) {
	ok := mustDecode(t, request(t, nil, span(traceA, spanA, 100, 350)), 1)[0]
	if ok.DurationNs != 250 {
		t.Errorf("duração = %d, quero 250", ok.DurationNs)
	}
	back := mustDecode(t, request(t, nil, span(traceA, spanA, 350, 100)), 1)[0]
	if back.DurationNs != 0 {
		t.Errorf("fim antes do início deu duração %d: deveria ser ausente", back.DurationNs)
	}
}

func TestDecodeTracesKeepsAttributesAsJSON(t *testing.T) {
	res := []*commonpb.KeyValue{kv("service.name", "oute-agent")}
	got := mustDecode(t, request(t, res, span(traceA, spanA, 100, 200, kv("model", "sonnet"))), 1)[0]
	var attrs map[string]any
	if err := json.Unmarshal(got.Attributes, &attrs); err != nil || attrs["model"] != "sonnet" {
		t.Errorf("atributos do span = %s (%v)", got.Attributes, err)
	}
	if !bytes.Contains(got.ResourceAttributes, []byte(`"service.name":"oute-agent"`)) {
		t.Errorf("atributos do resource = %s", got.ResourceAttributes)
	}
	if string(got.Events) != "[]" || string(got.Links) != "[]" {
		t.Errorf("eventos e links sem dado devem ser []: %s %s", got.Events, got.Links)
	}
}

func TestDecodeTracesRejectsSpanWithoutIDs(t *testing.T) {
	_, err := DecodeTraces(request(t, nil, span(nil, spanA, 1, 2)), 1)
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("erro = %v, quero ErrBadPayload", err)
	}
}

func TestDecodeTracesRejectsTimeOutsideInt64(t *testing.T) {
	_, err := DecodeTraces(request(t, nil, span(traceA, spanA, math.MaxInt64+1, 0)), 1)
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("erro = %v, quero ErrBadPayload", err)
	}
}

func TestDecodeTracesRejectsGarbage(t *testing.T) {
	_, err := DecodeTraces([]byte{0xff, 0xff, 0xff}, 1)
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("erro = %v, quero ErrBadPayload", err)
	}
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInflateGzipRoundTrip(t *testing.T) {
	raw := []byte("payload")
	got, err := Inflate(bytes.NewReader(gzipBytes(t, raw)), true)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("Inflate = %q, %v", got, err)
	}
	plain, err := Inflate(bytes.NewReader(raw), false)
	if err != nil || !bytes.Equal(plain, raw) {
		t.Fatalf("Inflate sem gzip = %q, %v", plain, err)
	}
}

func TestInflateRejectsBrokenGzip(t *testing.T) {
	_, err := Inflate(strings.NewReader("não é gzip"), true)
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("erro = %v, quero ErrBadPayload", err)
	}
}

// Passar de 64 MB descomprimidos é erro permanente (400): o collector não reenvia (ADR-0076 item 7).
func TestInflateRejectsBodyOverTheLimit(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := io.Copy(w, io.LimitReader(zeros{}, MaxBodyBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Inflate(&buf, true)
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("erro = %v, quero ErrBadPayload", err)
	}
}

// Queda da conexão no meio do corpo não é corpo inválido: o collector tem de retentar (503 no listener).
func TestInflateReadErrorIsNotBadPayload(t *testing.T) {
	boom := errors.New("conexão caiu")
	_, err := Inflate(io.MultiReader(strings.NewReader("pela metade"), errReader{boom}), false)
	if errors.Is(err, ErrBadPayload) || !errors.Is(err, boom) {
		t.Fatalf("erro = %v, quero o da leitura sem ErrBadPayload", err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
