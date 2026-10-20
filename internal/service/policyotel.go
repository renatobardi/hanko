package service

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// OTel da política de tools (ADR-0013, ADR-0053): um contador por chamada decidida e um span por caso de uso. Sem
// provider configurado o SDK global descarta em silêncio, então nada disto pode falhar. Os atributos têm cardinalidade
// baixa (origem, decisão e de onde veio a decisão); nunca o nome da tool, a persona nem os argumentos.

var (
	toolOnce    sync.Once
	toolCounter metric.Int64Counter
)

func toolMeter() metric.Int64Counter {
	toolOnce.Do(func() {
		c, err := otel.Meter("hanko/service").Int64Counter("hanko.tool.invocations",
			metric.WithDescription("Chamadas de tool decididas pelo motor de política: origem, decisão e de onde veio a decisão"))
		if err == nil {
			toolCounter = c
		}
	})
	return toolCounter
}

// countToolDecision conta uma chamada de tool (source: run|gateway|test|hook|acp|plugin; policy: include, exclude,
// default, policy:persona, policy:company ou vazio).
func countToolDecision(source, decision, policy string) {
	if c := toolMeter(); c != nil {
		c.Add(context.Background(), 1, metric.WithAttributes(attribute.String("source", source),
			attribute.String("decision", decision), attribute.String("policy", policy)))
	}
}

// toolSpan abre o span de um caso de uso da política de tools; o retorno fecha.
func toolSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func()) {
	ctx, span := otel.Tracer("hanko/service").Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, func() { span.End() }
}
