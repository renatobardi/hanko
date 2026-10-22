package service

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// OTel do ciclo de vida da empresa, do import com prévia e do acesso (ADR-0013, ADR-0057): um span por caso de uso e
// um contador por operação. Sem provider configurado o SDK global descarta em silêncio.

var (
	lifecycleOnce    sync.Once
	lifecycleCounter metric.Int64Counter
)

// lifecycleSpan abre o span de um caso de uso e conta a operação (hanko.company.lifecycle{op}).
func lifecycleSpan(ctx context.Context, op string) (context.Context, trace.Span) {
	lifecycleOnce.Do(func() {
		c, err := otel.Meter("hanko/service").Int64Counter("hanko.company.lifecycle",
			metric.WithDescription("Operações de ciclo de vida da empresa, import com prévia e acesso (login da CLI, pedidos de entrada)"))
		if err == nil {
			lifecycleCounter = c
		}
	})
	if lifecycleCounter != nil {
		lifecycleCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("op", op)))
	}
	return otel.Tracer("hanko/service").Start(ctx, op)
}
