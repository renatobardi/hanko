package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/renatobardi/hanko/internal/domain"
)

// alertCounter conta os alertas abertos por tipo (implementado por *service.Service).
type alertCounter interface {
	AlertCounts(ctx context.Context) (map[domain.AlertKind]int, error)
}

const (
	alertMetricTimeout = 3 * time.Second
	// alertMetricWarnEvery espaça o aviso de leitura falha: a coleta roda a cada poucos segundos e o banco fora já é
	// avisado por quem o usa; aqui um WARN a cada 5 min e o resto em debug.
	alertMetricWarnEvery = 5 * time.Minute
)

// registerAlertMetrics expõe o gauge hanko_alerts_open{kind=...}: alertas operacionais abertos,
// um ponto por tipo (zero incluído). O valor é lido do banco a cada coleta, então não depende
// do job que avalia os alertas ter rodado neste processo. Falha de leitura não derruba a coleta
// nem volta ao SDK: o callback a registra ele mesmo e devolve nil. Devolvê-la ao SDK a mandava ao
// handler global de erros do OpenTelemetry, que a logava como "falha de exportação", apontando o
// collector quando o problema era o banco.
func registerAlertMetrics(m metric.Meter, c alertCounter, l *slog.Logger) error {
	g, err := m.Int64ObservableGauge("hanko_alerts_open",
		metric.WithDescription("Alertas operacionais abertos, por tipo."))
	if err != nil {
		return fmt.Errorf("app: gauge de alertas: %w", err)
	}
	warn := alertReadWarner(l)
	_, err = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		cctx, cancel := context.WithTimeout(ctx, alertMetricTimeout)
		defer cancel()
		counts, err := c.AlertCounts(cctx)
		if err != nil {
			warn(err)
			return nil
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

// alertReadWarner loga a falha de leitura do gauge: WARN no máximo a cada alertMetricWarnEvery, debug no resto.
func alertReadWarner(l *slog.Logger) func(error) {
	if l == nil {
		l = slog.Default()
	}
	var mu sync.Mutex
	var last time.Time
	return func(err error) {
		mu.Lock()
		now := time.Now()
		warn := now.Sub(last) > alertMetricWarnEvery
		if warn {
			last = now
		}
		mu.Unlock()
		if warn {
			l.Warn("métrica hanko_alerts_open: leitura dos alertas falhou; o gauge fica sem ponto nesta coleta (demais ocorrências em debug)", "error", err.Error())
			return
		}
		l.Debug("métrica hanko_alerts_open: leitura dos alertas falhou", "error", err.Error())
	}
}
