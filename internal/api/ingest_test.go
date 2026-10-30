package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/service"
)

const (
	ingestKey = "hk_ingest"
	fullKey   = "hk_full"
	badKey    = "hk_bad"
)

// fakeIngest é o serviço do listener com as chaves de teste: ingestKey é de ingestão; fullKey tem outro escopo.
type fakeIngest struct {
	company  domain.ID
	keyID    domain.ID
	err      error
	recorded []ingest.Span
	calls    int
}

func (f *fakeIngest) ResolveIngestKey(_ context.Context, key string) (domain.ID, domain.ID, error) {
	switch key {
	case ingestKey:
		return f.company, f.keyID, nil
	case fullKey:
		return domain.ID{}, domain.ID{}, domain.ErrNotAllowed
	}
	return domain.ID{}, domain.ID{}, domain.ErrUnauthenticated
}

func (f *fakeIngest) IngestTraces(_ context.Context, company, _ domain.ID, spans []ingest.Span) (service.TraceIngestResult, error) {
	f.calls++
	if f.err != nil {
		return service.TraceIngestResult{}, f.err
	}
	if company != f.company {
		return service.TraceIngestResult{}, errors.New("empresa errada")
	}
	f.recorded = append(f.recorded, spans...)
	return service.TraceIngestResult{Accepted: len(spans)}, nil
}

func oneSpanBody(t *testing.T, spans int) []byte {
	t.Helper()
	var ss []*tracepb.Span
	for i := range spans {
		ss = append(ss, &tracepb.Span{TraceId: []byte{1, 2, 3, 4}, SpanId: []byte{byte(i + 1)}, Name: "llm"})
	}
	raw, err := proto.Marshal(&collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: ss}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func gzipped(t *testing.T, raw []byte) []byte {
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

func postIngest(t *testing.T, h http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func protoHeaders(key string) map[string]string {
	h := map[string]string{"Content-Type": "application/x-protobuf"}
	if key != "" {
		h["Authorization"] = "Bearer " + key
	}
	return h
}

func newIngestHandler(f *fakeIngest) http.Handler {
	return IngestHandler(f, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// Chave sem o escopo ingest não grava nada (critério de aceite do #125).
func TestIngestRefusesKeysWithoutTheIngestScope(t *testing.T) {
	f := &fakeIngest{company: domain.NewID(), keyID: domain.NewID()}
	h := newIngestHandler(f)
	for _, c := range []struct {
		name, key string
		want      int
	}{
		{"sem chave", "", http.StatusUnauthorized},
		{"chave inválida", badKey, http.StatusUnauthorized},
		{"chave full", fullKey, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := postIngest(t, h, http.MethodPost, "/v1/traces", oneSpanBody(t, 2), protoHeaders(c.key))
			if rec.Code != c.want {
				t.Fatalf("status = %d, quero %d", rec.Code, c.want)
			}
		})
	}
	if f.calls != 0 || len(f.recorded) != 0 {
		t.Fatalf("chave recusada chegou ao serviço: %d chamadas, %d spans", f.calls, len(f.recorded))
	}
}

func TestIngestAcceptsTracesWithTheIngestKey(t *testing.T) {
	company, key := domain.NewID(), domain.NewID()
	f := &fakeIngest{company: company, keyID: key}
	h := newIngestHandler(f)
	headers := protoHeaders(ingestKey)
	headers["Content-Encoding"] = "gzip"

	rec := postIngest(t, h, http.MethodPost, "/v1/traces", gzipped(t, oneSpanBody(t, 2)), headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-protobuf" {
		t.Errorf("Content-Type da resposta = %q", ct)
	}
	var ack collectortrace.ExportTraceServiceResponse
	if err := proto.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
		t.Fatalf("resposta não é ExportTraceServiceResponse: %v", err)
	}
	if len(f.recorded) != 2 {
		t.Fatalf("spans gravados = %d, quero 2", len(f.recorded))
	}
}

func TestIngestRejectsBadRequests(t *testing.T) {
	for _, c := range []struct {
		name    string
		body    []byte
		headers map[string]string
		want    int
	}{
		{"tipo de corpo", []byte("{}"), map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + ingestKey}, http.StatusUnsupportedMediaType},
		{"codificação desconhecida", oneSpanBody(t, 1), map[string]string{"Content-Type": "application/x-protobuf", "Content-Encoding": "br", "Authorization": "Bearer " + ingestKey}, http.StatusBadRequest},
		{"gzip inválido", []byte("não é gzip"), map[string]string{"Content-Type": "application/x-protobuf", "Content-Encoding": "gzip", "Authorization": "Bearer " + ingestKey}, http.StatusBadRequest},
		{"protobuf inválido", []byte{0xff, 0xff, 0xff}, protoHeaders(ingestKey), http.StatusBadRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeIngest{company: domain.NewID(), keyID: domain.NewID()}
			rec := postIngest(t, newIngestHandler(f), http.MethodPost, "/v1/traces", c.body, c.headers)
			if rec.Code != c.want {
				t.Fatalf("status = %d, quero %d", rec.Code, c.want)
			}
			if f.calls != 0 {
				t.Fatal("corpo recusado chegou ao serviço")
			}
		})
	}
}

// Falha de gravação é 503 com Retry-After: o collector retenta sem prazo (ADR-0076 item 7).
func TestIngestAnswers503WhenTheWriteFails(t *testing.T) {
	f := &fakeIngest{company: domain.NewID(), keyID: domain.NewID(), err: domain.ErrConflict}
	rec := postIngest(t, newIngestHandler(f), http.MethodPost, "/v1/traces", oneSpanBody(t, 1), protoHeaders(ingestKey))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quero 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("503 sem Retry-After")
	}
}

func TestIngestNotYetImplementedRoutesAnswer501AfterAuth(t *testing.T) {
	f := &fakeIngest{company: domain.NewID(), keyID: domain.NewID()}
	h := newIngestHandler(f)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/logs"}, {http.MethodPost, "/v1/metrics"}, {http.MethodPost, "/v1/replay/traces"}, {http.MethodGet, "/v1/ready"},
	} {
		if rec := postIngest(t, h, c.method, c.path, nil, protoHeaders(fullKey)); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s com chave full = %d, quero 403", c.method, c.path, rec.Code)
		}
		if rec := postIngest(t, h, c.method, c.path, nil, protoHeaders(ingestKey)); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, quero 501", c.method, c.path, rec.Code)
		}
	}
	if !strings.Contains(postIngest(t, h, http.MethodGet, "/v1/ready", nil, protoHeaders(ingestKey)).Body.String(), "implementada") {
		t.Error("o 501 deveria dizer que a rota não está implementada")
	}
}
