// Package ingest decodifica a telemetria OTLP que o collector do oute manda ao Hanko (ADR-0076): o gzip, o protobuf e a
// forma de cada span, com as colunas fixas do agent-studio (`otlp.py:14-28,198-240@845764f` do oute-agent). Não toca no
// banco: quem grava é o serviço.
package ingest

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// MaxBodyBytes é o tamanho máximo do corpo depois de descomprimir; o agent-studio usa o mesmo (`app.py:52`).
const MaxBodyBytes = 64 << 20

// ErrBadPayload é corpo que não é OTLP válido. Erro permanente: o collector não reenvia (ADR-0076 item 7).
var ErrBadPayload = errors.New("ingest: corpo inválido")

// fixedAttrs são os atributos que viram coluna fixa, por nome de coluna. Cada um é procurado no registro e, se ausente,
// no resource (`otlp.py` FIXED).
var fixedAttrs = []struct {
	field func(*Span) *string
	key   string
}{
	{func(s *Span) *string { return &s.HostName }, "host.name"},
	{func(s *Span) *string { return &s.OuteInstance }, "oute.instance"},
	{func(s *Span) *string { return &s.OuteAgent }, "oute.agent"},
	{func(s *Span) *string { return &s.ServiceName }, "service.name"},
	{func(s *Span) *string { return &s.SessionID }, "session.id"},
	{func(s *Span) *string { return &s.OuteTaskID }, "oute.task.id"},
	{func(s *Span) *string { return &s.OuteSwarmRound }, "oute.swarm.round"},
	{func(s *Span) *string { return &s.OuteRepo }, "oute.task.repo"},
	{func(s *Span) *string { return &s.OuteSubscription }, "oute.subscription"},
}

// Span é um span como chegou. Texto vazio é atributo ausente; zero em número, o mesmo. O JSON dos atributos é o
// canônico (chaves ordenadas), guardado como está.
type Span struct {
	DedupeKey          string
	TimeUnixNano       int64
	EndUnixNano        int64
	DurationNs         int64
	TraceID            string
	SpanID             string
	ParentSpanID       string
	Name               string
	Kind               int32
	StatusCode         int32
	StatusMessage      string
	ScopeName          string
	HostName           string
	OuteInstance       string
	OuteAgent          string
	ServiceName        string
	SessionID          string
	OuteTaskID         string
	OuteSwarmRound     string
	OuteRepo           string
	OuteSubscription   string
	ResourceAttributes []byte
	Attributes         []byte
	Events             []byte
	Links              []byte
}

// DecodeTraces lê um ExportTraceServiceRequest em protobuf e devolve os spans, na ordem do corpo. received é a hora de
// chegada, em ns, que só entra quando o span não traz hora nenhuma.
func DecodeTraces(raw []byte, received int64) ([]Span, error) {
	var req collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("%w: protobuf: %v", ErrBadPayload, err)
	}
	var out []Span
	for _, rs := range req.GetResourceSpans() {
		res := attrMap(rs.GetResource().GetAttributes())
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				s, err := spanOf(sp, res, ss.GetScope().GetName(), received)
				if err != nil {
					return nil, err
				}
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// Inflate lê o corpo e, se gzipped, descomprime. Passa de MaxBodyBytes descomprimidos é ErrBadPayload (ADR-0076 item 7).
// Erro de leitura do próprio corpo (a conexão caiu) não é ErrBadPayload: o collector retenta.
func Inflate(r io.Reader, gzipped bool) ([]byte, error) {
	if gzipped {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("%w: gzip: %v", ErrBadPayload, err)
		}
		defer func() { _ = gz.Close() }()
		r = gz
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxBodyBytes+1))
	if err != nil {
		if errors.Is(err, gzip.ErrChecksum) || errors.Is(err, gzip.ErrHeader) {
			return nil, fmt.Errorf("%w: gzip: %v", ErrBadPayload, err)
		}
		return nil, err
	}
	if len(raw) > MaxBodyBytes {
		return nil, fmt.Errorf("%w: acima de 64 MB descomprimidos", ErrBadPayload)
	}
	return raw, nil
}

func spanOf(sp *tracepb.Span, res map[string]any, scope string, received int64) (Span, error) {
	if len(sp.GetTraceId()) == 0 || len(sp.GetSpanId()) == 0 {
		return Span{}, fmt.Errorf("%w: span sem traceId ou spanId", ErrBadPayload)
	}
	start, err := nanos(sp.GetStartTimeUnixNano())
	if err != nil {
		return Span{}, err
	}
	end, err := nanos(sp.GetEndTimeUnixNano())
	if err != nil {
		return Span{}, err
	}
	trace, span := hex.EncodeToString(sp.GetTraceId()), hex.EncodeToString(sp.GetSpanId())
	s := Span{
		DedupeKey:     "s:" + trace + ":" + span,
		TimeUnixNano:  factTime(start, end, received),
		EndUnixNano:   end,
		TraceID:       trace,
		SpanID:        span,
		ParentSpanID:  hex.EncodeToString(sp.GetParentSpanId()),
		Name:          sp.GetName(),
		Kind:          int32(sp.GetKind()),
		StatusCode:    int32(sp.GetStatus().GetCode()),
		StatusMessage: sp.GetStatus().GetMessage(),
		ScopeName:     scope,
	}
	if start > 0 && end >= start {
		s.DurationNs = end - start
	}
	rec := attrMap(sp.GetAttributes())
	for _, f := range fixedAttrs {
		*f.field(&s) = text(lookup(rec, res, f.key))
	}
	if err := s.encodeJSON(res, rec, sp); err != nil {
		return Span{}, err
	}
	return s, nil
}

// encodeJSON guarda os atributos completos, do resource, do registro, dos eventos e dos links, como JSON canônico.
func (s *Span) encodeJSON(res, rec map[string]any, sp *tracepb.Span) error {
	var err error
	if s.ResourceAttributes, err = json.Marshal(res); err != nil {
		return fmt.Errorf("%w: atributos do resource: %v", ErrBadPayload, err)
	}
	if s.Attributes, err = json.Marshal(rec); err != nil {
		return fmt.Errorf("%w: atributos do span: %v", ErrBadPayload, err)
	}
	events := make([]map[string]any, 0, len(sp.GetEvents()))
	for _, e := range sp.GetEvents() {
		events = append(events, map[string]any{"time_unix_nano": e.GetTimeUnixNano(), "name": e.GetName(),
			"attributes": attrMap(e.GetAttributes())})
	}
	if s.Events, err = json.Marshal(events); err != nil {
		return fmt.Errorf("%w: eventos: %v", ErrBadPayload, err)
	}
	links := make([]map[string]any, 0, len(sp.GetLinks()))
	for _, l := range sp.GetLinks() {
		links = append(links, map[string]any{"trace_id": hex.EncodeToString(l.GetTraceId()),
			"span_id": hex.EncodeToString(l.GetSpanId()), "attributes": attrMap(l.GetAttributes())})
	}
	if s.Links, err = json.Marshal(links); err != nil {
		return fmt.Errorf("%w: links: %v", ErrBadPayload, err)
	}
	return nil
}

// factTime é a hora do fato: o início do span; sem ele, o fim; só em último caso, a chegada (`otlp.py` fact_time).
func factTime(start, end, received int64) int64 {
	switch {
	case start > 0:
		return start
	case end > 0:
		return end
	}
	return received
}

// nanos converte um tempo do protobuf (uint64) sem estourar o bigint do banco.
func nanos(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("%w: tempo fora da faixa", ErrBadPayload)
	}
	return int64(v), nil
}

// lookup procura o atributo no registro e, se ausente, no resource.
func lookup(rec, res map[string]any, key string) any {
	if v := rec[key]; v != nil {
		return v
	}
	return res[key]
}

// text é o valor de coluna de texto: string como está, outro valor em JSON, ausente = vazio.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func attrMap(kvs []*commonpb.KeyValue) map[string]any {
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		out[kv.GetKey()] = anyValue(kv.GetValue())
	}
	return out
}

// anyValue é o AnyValue do OTLP como valor JSON simples: int64 vira número; bytes, base64, como no OTLP JSON.
func anyValue(v *commonpb.AnyValue) any {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue
	case *commonpb.AnyValue_ArrayValue:
		out := make([]any, 0, len(x.ArrayValue.GetValues()))
		for _, e := range x.ArrayValue.GetValues() {
			out = append(out, anyValue(e))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		return attrMap(x.KvlistValue.GetValues())
	case *commonpb.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(x.BytesValue)
	}
	return nil
}
