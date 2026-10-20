package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Erros via OTel (ADR-0058 §C): a exceção vira span event com a pilha, log ERROR com a pilha e contador; o erro do
// navegador chega pelo endpoint e vira span "browser.error".

func setupTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev); _ = tp.Shutdown(context.Background()) })
	return rec
}

func attrOf(attrs []attribute.KeyValue, key string) string {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.String()
		}
	}
	return ""
}

func TestRecordAddsSpanEventLogAndStatus(t *testing.T) {
	rec := setupTracer(t)
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	ctx, span := otel.Tracer("t").Start(context.Background(), "req")
	cause := errors.New("conexão recusada")
	Record(ctx, log, FromError(SourceServer, fmt.Errorf("op x: %w", cause)), slog.String("request_id", "r1"))
	span.End()

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	s := spans[0]
	if s.Status().Code != codes.Error {
		t.Errorf("status = %v, quer Error", s.Status().Code)
	}
	if len(s.Events()) != 1 || s.Events()[0].Name != "exception" {
		t.Fatalf("eventos = %+v", s.Events())
	}
	ev := s.Events()[0]
	if got := attrOf(ev.Attributes, "exception.message"); got != "op x: conexão recusada" {
		t.Errorf("message = %q", got)
	}
	if got := attrOf(ev.Attributes, "exception.type"); got != "*errors.errorString" {
		t.Errorf("type = %q (deve ser o do erro mais interno)", got)
	}
	if st := attrOf(ev.Attributes, "exception.stacktrace"); !strings.Contains(st, "TestRecordAddsSpanEventLogAndStatus") {
		t.Errorf("a pilha deveria conter o chamador:\n%s", st)
	}
	line := buf.String()
	for _, want := range []string{`"level":"ERROR"`, `"exception.stacktrace"`, `"exception.source":"server"`, `"request_id":"r1"`} {
		if !strings.Contains(line, want) {
			t.Errorf("log sem %s: %s", want, line)
		}
	}
}

func TestRecordWithoutSpanOrLoggerDoesNotPanic(t *testing.T) {
	Record(context.Background(), nil, FromPanic(SourceServer, "boom", []byte("stack")))
	Record(context.Background(), slog.New(slog.DiscardHandler), FromError(SourceJob, errors.New("x")))
}

func TestFromPanic(t *testing.T) {
	ex := FromPanic(SourceServer, fmt.Errorf("deu ruim"), []byte("goroutine 1"))
	if ex.Message != "deu ruim" || !strings.HasPrefix(ex.Type, "panic(") || ex.Stacktrace != "goroutine 1" {
		t.Errorf("%+v", ex)
	}
}

func TestMetricType(t *testing.T) {
	cases := map[string]string{"TypeError": "TypeError", "*pgconn.PgError": "*pgconn.PgError", "": "other", "com espaço": "other",
		strings.Repeat("a", 49): "other", "x\ny": "other", "panic(string)": "panic(string)"}
	for in, want := range cases {
		if got := MetricType(in); got != want {
			t.Errorf("MetricType(%q) = %q, quer %q", in, got, want)
		}
	}
}

func postBrowser(h http.Handler, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, BrowserErrorPath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if mod != nil {
		mod(r)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestBrowserErrorBecomesSpanWithException(t *testing.T) {
	rec := setupTracer(t)
	var buf bytes.Buffer
	h := BrowserErrors(slog.New(slog.NewJSONHandler(&buf, nil)))

	body := `{"kind":"error","name":"TypeError","message":"x is undefined","stack":"TypeError: x is undefined\n    at f (http://h/static/hanko.js:10:5)",` +
		`"source":"http://h/static/hanko.js?v=3","line":10,"col":5,"url":"https://h/c/acme/tasks?token=SEGREDO#frag"}`
	out := postBrowser(h, body, nil)
	if out.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", out.Code, out.Body.String())
	}
	spans := rec.Ended()
	if len(spans) != 1 || spans[0].Name() != "browser.error" {
		t.Fatalf("spans = %+v", spans)
	}
	s := spans[0]
	if len(s.Events()) != 1 || s.Events()[0].Name != "exception" {
		t.Fatalf("eventos = %+v", s.Events())
	}
	if got := attrOf(s.Events()[0].Attributes, "exception.type"); got != "TypeError" {
		t.Errorf("type = %q", got)
	}
	if got := attrOf(s.Events()[0].Attributes, "exception.source"); got != "browser" {
		t.Errorf("source = %q", got)
	}
	if got := attrOf(s.Attributes(), "url.path"); got != "/c/acme/tasks" {
		t.Errorf("url.path = %q (sem query nem fragmento)", got)
	}
	if got := attrOf(s.Attributes(), "code.filepath"); got != "/static/hanko.js" {
		t.Errorf("code.filepath = %q", got)
	}
	if strings.Contains(buf.String(), "SEGREDO") {
		t.Errorf("a query da URL vazou para o log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "at f (http://h/static/hanko.js:10:5)") || !strings.Contains(buf.String(), `"exception.source":"browser"`) {
		t.Errorf("log deveria ter a pilha do navegador: %s", buf.String())
	}
}

func TestBrowserErrorValidation(t *testing.T) {
	h := BrowserErrors(nil)
	if rec := postBrowser(h, `{"message":"x"}`, func(r *http.Request) { r.Header.Del("Sec-Fetch-Site"); r.Header.Set("Origin", "https://evil.example") }); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin = %d", rec.Code)
	}
	if rec := postBrowser(h, `não é json`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("json inválido = %d", rec.Code)
	}
	if rec := postBrowser(h, `{}`, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("relato vazio = %d", rec.Code)
	}
	big := `{"message":"` + strings.Repeat("a", 20<<10) + `"}`
	if rec := postBrowser(h, big, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("corpo grande = %d", rec.Code)
	}
	get := httptest.NewRequest(http.MethodGet, BrowserErrorPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d", rec.Code)
	}
}

func TestSanitizeBrowserReportTruncatesAndCleans(t *testing.T) {
	in := browserReport{Kind: "xss<script>", Name: "<img onerror>", Message: strings.Repeat("é", 900) + "\x00\x1b[31m", Stack: "a\nb\tc\x07" + strings.Repeat("z", 9000),
		URL: "/convite/TOKENSECRETO", Line: -4, Col: 99999999}
	got := sanitizeBrowserReport(in)
	if got.Kind != "error" || got.Name != "other" {
		t.Errorf("kind/name = %q/%q", got.Kind, got.Name)
	}
	if n := len([]rune(got.Message)); n > maxMessageRunes {
		t.Errorf("message com %d runas", n)
	}
	if strings.ContainsAny(got.Message, "\x00\x1b") || strings.ContainsRune(got.Stack, '\x07') {
		t.Errorf("controles não removidos")
	}
	if !strings.Contains(got.Stack, "a\nb\tc") || len([]rune(got.Stack)) > maxStackRunes {
		t.Errorf("stack: mantém \\n e \\t e corta em %d (veio %d)", maxStackRunes, len([]rune(got.Stack)))
	}
	if got.URL != "/convite/…" {
		t.Errorf("o segredo do caminho deveria ser mascarado: %q", got.URL)
	}
	if got.Line != 0 || got.Col != 0 {
		t.Errorf("line/col fora da faixa deveriam virar 0: %d/%d", got.Line, got.Col)
	}
}
