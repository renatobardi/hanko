package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestSetupShutdownSemCollector(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	start := time.Now()
	var buf bytes.Buffer
	tel, err := Setup(context.Background(), Options{Version: "test", LogWriter: &buf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := otel.Tracer("t").Start(context.Background(), "op")
	tel.Logger.InfoContext(ctx, "olá")
	span.End()

	sctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_ = tel.Shutdown(sctx) // erro de export é aceitável sem collector
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("setup+shutdown levou %v", d)
	}
	if err := tel.Shutdown(context.Background()); err != nil && sctx.Err() == nil {
		// idempotente: segunda chamada devolve o mesmo resultado, sem bloquear.
		t.Logf("segundo shutdown: %v", err)
	}
}

func TestLoggerTraceID(t *testing.T) {
	var buf bytes.Buffer
	tel, err := Setup(context.Background(), Options{LogWriter: &buf, LogLevel: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tel.Shutdown(c)
	}()
	ctx, span := otel.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	tel.Logger.With("k", "v").InfoContext(ctx, "com span")
	tel.Logger.Info("sem span")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("linhas = %d: %q", len(lines), buf.String())
	}
	var withSpan, without map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &withSpan); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(lines[1]), &without)
	sc := trace.SpanContextFromContext(ctx)
	if withSpan["trace_id"] != sc.TraceID().String() || withSpan["span_id"] != sc.SpanID().String() || withSpan["k"] != "v" {
		t.Fatalf("registro com span: %v", withSpan)
	}
	if _, ok := without["trace_id"]; ok {
		t.Fatalf("trace_id inesperado: %v", without)
	}
}

func TestLevelsEFormato(t *testing.T) {
	var buf bytes.Buffer
	tel, err := Setup(context.Background(), Options{LogWriter: &buf, LogLevel: "warn", LogFormat: "text"})
	if err != nil {
		t.Fatal(err)
	}
	tel.Logger.Info("não aparece")
	tel.Logger.Warn("aparece")
	if out := buf.String(); strings.Contains(out, "não aparece") || !strings.Contains(out, "level=WARN") {
		t.Fatalf("saída: %q", out)
	}
	c, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tel.Shutdown(c)

	if _, err := Setup(context.Background(), Options{LogLevel: "x"}); err == nil {
		t.Fatal("esperava erro de nível")
	}
	if _, err := Setup(context.Background(), Options{LogFormat: "xml"}); err == nil {
		t.Fatal("esperava erro de formato")
	}
}

func TestMetricsHandler(t *testing.T) {
	tel, err := Setup(context.Background(), Options{LogWriter: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tel.Shutdown(c)
	}()
	ctr, _ := otel.Meter("t").Int64Counter("hanko_teste")
	ctr.Add(context.Background(), 3)

	rec := httptest.NewRecorder()
	tel.MetricsHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "hanko_teste_total{") || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("code=%d body=%.300s", rec.Code, body)
	}
}

func TestFanoutNivel(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(newFanout(slog.LevelError, newLocalHandler(&buf, "json", slog.LevelDebug)))
	l.Warn("x")
	if buf.Len() != 0 {
		t.Fatal("nível do fanout deveria filtrar")
	}
}
