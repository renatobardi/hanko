package ingest

import (
	"testing"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func sv(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}

func kv(k string, v *commonpb.AnyValue) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: v}
}

func TestFromProtobufLogsAndSpans(t *testing.T) {
	res := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{kv("host.name", sv("oute-mac")), kv("oute.agent", sv("codex"))}}
	traceID := []byte{0x0a, 0xf7, 0x65, 0x19, 0x16, 0xcd, 0x43, 0xdd, 0x84, 0x48, 0xeb, 0x21, 0x1c, 0x80, 0x31, 0x9c}
	spanID := []byte{0xb7, 0xad, 0x6b, 0x71, 0x69, 0x20, 0x33, 0x31}

	logsBody, err := proto.Marshal(&collogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		Resource: res,
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{
			TimeUnixNano: 1790000000000000001, SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_INFO, Body: sv("olá"),
			Attributes: []*commonpb.KeyValue{kv("oute.event.id", sv("e-1")), kv("session.id", sv("s-1"))},
			TraceId:    traceID, SpanId: spanID,
		}}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := FromProtobuf(SignalLogs, logsBody)
	if err != nil {
		t.Fatal(err)
	}
	lr, err := LogRows(doc, 5)
	if err != nil || len(lr) != 1 {
		t.Fatalf("linhas = %d (%v)", len(lr), err)
	}
	r := lr[0]
	if r.DedupeKey != "ev:e-1" || r.TimeUnixNano != 1790000000000000001 || *r.HostName != "oute-mac" || *r.SessionID != "s-1" {
		t.Errorf("linha de log = %+v", r)
	}
	if r.SeverityNumber == nil || *r.SeverityNumber != 9 {
		t.Errorf("severityNumber = %v, quer 9 (enum como número)", r.SeverityNumber)
	}
	if r.TraceID == nil || *r.TraceID != "0af7651916cd43dd8448eb211c80319c" || *r.SpanID != "b7ad6b7169203331" {
		t.Errorf("ids = %v %v, quer hexadecimal", r.TraceID, r.SpanID)
	}

	spansBody, err := proto.Marshal(&coltrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: res,
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
			TraceId: traceID, SpanId: spanID, ParentSpanId: []byte{1, 2, 3, 4, 5, 6, 7, 8}, Name: "claude_code.llm_request",
			StartTimeUnixNano: 10, EndTimeUnixNano: 90,
			Attributes: []*commonpb.KeyValue{kv("gen_ai.usage.input_tokens", &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 100}})},
		}}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err = FromProtobuf(SignalTraces, spansBody)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := SpanRows(doc, 5)
	if err != nil || len(sr) != 1 {
		t.Fatalf("spans = %d (%v)", len(sr), err)
	}
	s := sr[0]
	if s.DedupeKey != "s:0af7651916cd43dd8448eb211c80319c:b7ad6b7169203331" || *s.ParentSpanID != "0102030405060708" ||
		s.InputTokens == nil || *s.InputTokens != 100 || *s.DurationNs != 80 {
		t.Errorf("span = %+v", s)
	}
}

func TestFromProtobufRejectsGarbage(t *testing.T) {
	if _, err := FromProtobuf(SignalLogs, []byte{0xff, 0xff, 0xff}); err == nil {
		t.Error("protobuf inválido deveria ser recusado")
	}
	if _, err := FromProtobuf(Signal("x"), nil); err == nil {
		t.Error("sinal desconhecido deveria ser recusado")
	}
}
