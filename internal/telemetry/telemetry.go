// Package telemetry liga a observabilidade OpenTelemetry do Hanko (ADR-0013):
// traces, métricas (OTLP + /metrics Prometheus) e logs correlacionados.
// É mandatória: não há opção para desligar. Collector ausente nunca derruba o app.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	otellog "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	// DefaultEndpoint é o destino OTLP gRPC quando nenhuma env OTEL_* o define.
	DefaultEndpoint = "localhost:4317"

	exportTimeout = 2 * time.Second
)

// Options configura Setup.
type Options struct {
	ServiceName string // default "hanko"
	Version     string
	Instance    string // service.instance.id; default UUID por boot
	LogLevel    string // debug|info|warn|error (default info)
	LogFormat   string // json|text (default json)

	// LogWriter é o destino do log local (default os.Stderr). Útil em testes.
	LogWriter io.Writer
}

// Telemetry agrupa os providers e o logger.
type Telemetry struct {
	Logger *slog.Logger

	tp       *sdktrace.TracerProvider
	mp       *sdkmetric.MeterProvider
	lp       *sdklog.LoggerProvider
	registry *prometheus.Registry
	once     sync.Once
	shutErr  error
}

// Setup monta OTel. Não faz I/O de rede bloqueante: exporters conectam de forma preguiçosa.
func Setup(ctx context.Context, o Options) (*Telemetry, error) {
	if o.ServiceName == "" {
		o.ServiceName = "hanko"
	}
	if o.Instance == "" {
		o.Instance = uuid.NewString()
	}
	if o.LogWriter == nil {
		o.LogWriter = os.Stderr
	}
	level, err := ParseLevel(o.LogLevel)
	if err != nil {
		return nil, err
	}
	if f := strings.ToLower(o.LogFormat); f != "" && f != "json" && f != "text" {
		return nil, fmt.Errorf("telemetry: formato de log %q inválido (json|text)", o.LogFormat)
	}

	host, _ := os.Hostname()
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", o.ServiceName),
		attribute.String("service.version", o.Version),
		attribute.String("service.instance.id", o.Instance),
		attribute.String("host.name", host),
	))
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource: %w", err)
	}

	t := &Telemetry{registry: prometheus.NewRegistry()}
	// cleanup parcial se algo falhar no meio.
	fail := func(err error) (*Telemetry, error) {
		sctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
		defer cancel()
		_ = t.Shutdown(sctx)
		return nil, err
	}

	// Traces.
	traceOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithTimeout(exportTimeout),
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{Enabled: false}),
	}
	if !endpointFromEnv("TRACES") {
		traceOpts = append(traceOpts, otlptracegrpc.WithEndpoint(DefaultEndpoint), otlptracegrpc.WithInsecure())
	}
	te, err := otlptracegrpc.New(ctx, traceOpts...)
	if err != nil {
		return fail(fmt.Errorf("telemetry: exporter de traces: %w", err))
	}
	t.tp = sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(te, sdktrace.WithExportTimeout(exportTimeout)),
	)

	// Métricas: OTLP periódico + Prometheus pull.
	metricOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithTimeout(exportTimeout),
		otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{Enabled: false}),
	}
	if !endpointFromEnv("METRICS") {
		metricOpts = append(metricOpts, otlpmetricgrpc.WithEndpoint(DefaultEndpoint), otlpmetricgrpc.WithInsecure())
	}
	me, err := otlpmetricgrpc.New(ctx, metricOpts...)
	if err != nil {
		return fail(fmt.Errorf("telemetry: exporter de métricas: %w", err))
	}
	pe, err := promexporter.New(promexporter.WithRegisterer(t.registry))
	if err != nil {
		return fail(fmt.Errorf("telemetry: exporter prometheus: %w", err))
	}
	t.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	t.mp = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(15*time.Second), sdkmetric.WithTimeout(exportTimeout))),
		sdkmetric.WithReader(pe),
	)

	// Logs.
	logOpts := []otlploggrpc.Option{
		otlploggrpc.WithTimeout(exportTimeout),
		otlploggrpc.WithRetry(otlploggrpc.RetryConfig{Enabled: false}),
	}
	if !endpointFromEnv("LOGS") {
		logOpts = append(logOpts, otlploggrpc.WithEndpoint(DefaultEndpoint), otlploggrpc.WithInsecure())
	}
	le, err := otlploggrpc.New(ctx, logOpts...)
	if err != nil {
		return fail(fmt.Errorf("telemetry: exporter de logs: %w", err))
	}
	t.lp = sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(le, sdklog.WithExportTimeout(exportTimeout))),
	)

	otel.SetTracerProvider(t.tp)
	otel.SetMeterProvider(t.mp)
	otellog.SetLoggerProvider(t.lp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	t.Logger = slog.New(newFanout(level,
		newLocalHandler(o.LogWriter, o.LogFormat, level),
		otelslog.NewHandler(o.ServiceName, otelslog.WithLoggerProvider(t.lp)),
	))
	otel.SetErrorHandler(newErrorHandler(t.Logger))
	return t, nil
}

// MetricsHandler serve o formato Prometheus (/metrics).
func (t *Telemetry) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(t.registry, promhttp.HandlerOpts{})
}

// Shutdown descarrega e fecha os providers. Idempotente. Respeita o deadline do ctx.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	t.once.Do(func() {
		var errs []error
		if t.lp != nil {
			errs = append(errs, t.lp.Shutdown(ctx))
		}
		if t.mp != nil {
			errs = append(errs, t.mp.Shutdown(ctx))
		}
		if t.tp != nil {
			errs = append(errs, t.tp.Shutdown(ctx))
		}
		t.shutErr = errors.Join(errs...)
	})
	return t.shutErr
}

// ParseLevel converte o nome do nível (vazio = info).
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("telemetry: nível de log %q inválido (debug|info|warn|error)", s)
}

// endpointFromEnv diz se o operador já configurou o endpoint via env OTEL padrão.
func endpointFromEnv(signal string) bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
}

// newErrorHandler rebaixa erros de exportação (ex.: collector fora) para no máximo
// um aviso a cada 5 min; o resto vai a debug.
func newErrorHandler(l *slog.Logger) otel.ErrorHandler {
	var mu sync.Mutex
	var last time.Time
	return otel.ErrorHandlerFunc(func(err error) {
		mu.Lock()
		now := time.Now()
		warn := now.Sub(last) > 5*time.Minute
		if warn {
			last = now
		}
		mu.Unlock()
		if warn {
			l.Warn("falha de exportação OpenTelemetry (collector indisponível?); demais ocorrências em debug", "error", err.Error())
			return
		}
		l.Debug("falha de exportação OpenTelemetry", "error", err.Error())
	})
}
