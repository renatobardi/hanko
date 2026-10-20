package heartbeat

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/service"
)

// Telemetria da resiliência de run (ADR-0039). Os instrumentos vêm do meter global: antes de o provedor ser
// instalado eles delegam para o provedor real quando ele aparece, e sem provedor não fazem nada.
var (
	resilMeter        = otel.Meter("hanko/heartbeat")
	continuationCount = mustCounter("hanko.run.continuations", "Execuções abertas como continuação de outra, por motivo.")
	steerCount        = mustCounter("hanko.run.steers", "Mensagens do operador entregues a execuções ativas, por modo e resultado.")
)

func mustCounter(name, desc string) metric.Int64Counter {
	c, err := resilMeter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		// um nome inválido é erro de programação, mas telemetria nunca derruba a run: o instrumento vira no-op
		c, _ = noop.NewMeterProvider().Meter("hanko/heartbeat").Int64Counter(name)
	}
	return c
}

// recordContinuation marca no span da run e na métrica que ela continua outra execução.
func recordContinuation(ctx context.Context, span trace.Span, c *service.ContinuationContext, resumed bool) {
	if c == nil {
		return
	}
	span.SetAttributes(
		attribute.String("hanko.run.continuation_of", c.OfRunID.String()),
		attribute.String("hanko.run.continuation_reason", string(c.Reason)),
		attribute.Int("hanko.run.continuation_attempt", c.Attempt),
		attribute.Bool("hanko.run.session_resumed", resumed),
	)
	continuationCount.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", string(c.Reason)), attribute.Bool("session_resumed", resumed)))
}

// recordSteer conta uma mensagem de steer entregue (ou recusada) e deixa um evento no span da run.
func recordSteer(ctx context.Context, mode, outcome string) {
	steerCount.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", mode), attribute.String("outcome", outcome)))
	trace.SpanFromContext(ctx).AddEvent("hanko.run.steer", trace.WithAttributes(attribute.String("mode", mode), attribute.String("outcome", outcome)))
}
