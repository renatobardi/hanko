package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Rastreamento de erros via OpenTelemetry, sem SaaS (ADR-0058 §C): toda exceção vira span event "exception" (convenção
// semântica do OTel), log ERROR com a pilha (exportado por OTLP junto dos demais logs) e o contador hanko.errors. O
// painel "Erros" do Grafana lê os três.

// Origens de uma exceção.
const (
	SourceServer  = "server"  // erro inesperado (5xx) ou panic de um handler
	SourceBrowser = "browser" // erro de JavaScript relatado por hanko.js
	SourceJob     = "job"     // job em segundo plano
)

// Exception descreve uma exceção já pronta para registrar.
type Exception struct {
	Source     string // SourceServer | SourceBrowser | SourceJob
	Type       string // tipo (ex.: *pgconn.PgError, TypeError)
	Message    string
	Stacktrace string
}

var (
	errCounterOnce sync.Once
	errCounter     metric.Int64Counter
)

func errorCounter() metric.Int64Counter {
	errCounterOnce.Do(func() {
		c, err := otel.Meter("hanko/errors").Int64Counter("hanko.errors",
			metric.WithDescription("Exceções registradas, por origem (server, browser, job) e tipo"))
		if err == nil {
			errCounter = c
		}
	})
	return errCounter
}

// FromError monta a Exception de um erro; o tipo é o do erro mais interno da cadeia e a pilha é a do ponto da chamada.
func FromError(source string, err error) Exception {
	root := err
	for {
		next := errors.Unwrap(root)
		if next == nil {
			break
		}
		root = next
	}
	return Exception{Source: source, Type: fmt.Sprintf("%T", root), Message: err.Error(), Stacktrace: string(debug.Stack())}
}

// FromPanic monta a Exception de um panic recuperado (stack é o debug.Stack() tirado dentro do defer).
func FromPanic(source string, rec any, stack []byte) Exception {
	return Exception{Source: source, Type: fmt.Sprintf("panic(%T)", rec), Message: fmt.Sprint(rec), Stacktrace: string(stack)}
}

// Record registra a exceção no span do contexto (se estiver gravando), no log e no contador. log nil = só span e métrica.
func Record(ctx context.Context, log *slog.Logger, ex Exception, attrs ...slog.Attr) {
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.AddEvent("exception", trace.WithAttributes(
			attribute.String("exception.type", ex.Type),
			attribute.String("exception.message", ex.Message),
			attribute.String("exception.stacktrace", ex.Stacktrace),
			attribute.String("exception.source", ex.Source),
		))
		span.SetStatus(codes.Error, ex.Message)
	}
	if c := errorCounter(); c != nil {
		c.Add(ctx, 1, metric.WithAttributes(attribute.String("source", ex.Source), attribute.String("exception_type", MetricType(ex.Type))))
	}
	if log != nil {
		args := make([]any, 0, 8+len(attrs))
		args = append(args, "exception.source", ex.Source, "exception.type", ex.Type, "exception.message", ex.Message,
			"exception.stacktrace", ex.Stacktrace)
		for _, a := range attrs {
			args = append(args, a)
		}
		log.ErrorContext(ctx, "exceção registrada", args...)
	}
}

// MetricType normaliza o tipo para um rótulo de métrica de cardinalidade baixa: só [A-Za-z0-9_.*()], até 48
// caracteres; qualquer outra coisa vira "other".
func MetricType(t string) string {
	t = strings.TrimSpace(t)
	if t == "" || len(t) > 48 {
		return "other"
	}
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '*', r == '(', r == ')':
		default:
			return "other"
		}
	}
	return t
}
