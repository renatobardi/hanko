package service

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// OTel da coordenação de tasks (ADR-0013, ADR-0040): um contador por operação e um span por caso de uso. Sem
// provider configurado o SDK global descarta em silêncio, então nada disto pode falhar.

var (
	coordOnce    sync.Once
	coordCounter metric.Int64Counter
)

func coordMeter() metric.Int64Counter {
	coordOnce.Do(func() {
		c, err := otel.Meter("hanko/service").Int64Counter("hanko.task.coordination",
			metric.WithDescription("Operações de coordenação de tasks: dependências, pausa, árvore e política de revisão"))
		if err == nil {
			coordCounter = c
		}
	})
	return coordCounter
}

// countCoord conta uma operação de coordenação (kind: dependency|hold|tree|review_policy).
func countCoord(ctx context.Context, kind, op string) {
	if c := coordMeter(); c != nil {
		c.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind), attribute.String("op", op)))
	}
}

// coordSpan abre o span de um caso de uso da coordenação; o retorno fecha.
func coordSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func()) {
	ctx, span := otel.Tracer("hanko/service").Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, func() { span.End() }
}
