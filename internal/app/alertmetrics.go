package app

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/renatobardi/hanko/internal/domain"
)

// alertCounter conta os alertas abertos por tipo (implementado por *service.Service).
type alertCounter interface {
	AlertCounts(ctx context.Context) (map[domain.AlertKind]int, error)
}

const alertMetricTimeout = 3 * time.Second

// registerAlertMetrics expõe o gauge hanko_alerts_open{kind=...}: alertas operacionais abertos,
// um ponto por tipo (zero incluído). O valor é lido do banco a cada coleta, então não depende
// do job que avalia os alertas ter rodado neste processo. Falha de leitura não derruba a coleta.
func registerAlertMetrics(m metric.Meter, c alertCounter) error {
	g, err := m.Int64ObservableGauge("hanko_alerts_open",
		metric.WithDescription("Alertas operacionais abertos, por tipo."))
	if err != nil {
		return fmt.Errorf("app: gauge de alertas: %w", err)
	}
	_, err = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		cctx, cancel := context.WithTimeout(ctx, alertMetricTimeout)
		defer cancel()
		counts, err := c.AlertCounts(cctx)
		if err != nil {
			return err
		}
		for _, k := range domain.AlertKinds {
			o.ObserveInt64(g, int64(counts[k]), metric.WithAttributes(attribute.String("kind", string(k))))
		}
		return nil
	}, g)
	if err != nil {
		return fmt.Errorf("app: callback de alertas: %w", err)
	}
	return nil
}
