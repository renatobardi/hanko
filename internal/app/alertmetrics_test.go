package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/renatobardi/hanko/internal/domain"
)

type fakeAlertCounter struct {
	counts map[domain.AlertKind]int
	err    error
}

func (f fakeAlertCounter) AlertCounts(context.Context) (map[domain.AlertKind]int, error) {
	return f.counts, f.err
}

func scrape(t *testing.T, c alertCounter) string {
	t.Helper()
	body, _ := scrapeLogged(t, c)
	return body
}

// scrapeLogged coleta /metrics e devolve também o que o gauge logou.
func scrapeLogged(t *testing.T, c alertCounter) (string, string) {
	t.Helper()
	var logs bytes.Buffer
	l := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reg := prometheus.NewRegistry()
	exp, err := promexporter.New(promexporter.WithRegisterer(reg))
	if err != nil {
		t.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exp))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	if err := registerAlertMetrics(mp.Meter("hanko"), c, l); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/metrics", nil)) // segunda coleta: o aviso não repete
	return rec.Body.String(), logs.String()
}

// O gauge sai em /metrics com um ponto por tipo, zero incluído.
func TestAlertGaugeEmMetrics(t *testing.T) {
	body := scrape(t, fakeAlertCounter{counts: map[domain.AlertKind]int{domain.AlertRunStuck: 2, domain.AlertBudgetNearLimit: 1}})
	for _, want := range []string{
		`hanko_alerts_open{kind="persona_silent"`,
		`hanko_alerts_open{kind="run_stuck"`,
		`hanko_alerts_open{kind="budget_near_limit"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("faltou %q em:\n%s", want, body)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, `hanko_alerts_open{kind="run_stuck"`) && !strings.HasSuffix(line, " 2"):
			t.Errorf("run_stuck: %q", line)
		case strings.HasPrefix(line, `hanko_alerts_open{kind="persona_silent"`) && !strings.HasSuffix(line, " 0"):
			t.Errorf("persona_silent: %q", line)
		}
	}
}

// Falha ao ler o banco não derruba a coleta das demais métricas, e é logada pelo próprio gauge (um WARN, depois
// debug), não pelo handler de erros do OpenTelemetry, que a rotularia como falha de exportação.
func TestAlertGaugeErroDeLeitura(t *testing.T) {
	body, logs := scrapeLogged(t, fakeAlertCounter{err: errors.New("db fora")})
	if strings.Contains(body, "hanko_alerts_open{") {
		t.Fatalf("sem leitura não há ponto:\n%s", body)
	}
	if strings.Count(logs, "level=WARN") != 1 || !strings.Contains(logs, "leitura dos alertas falhou") || !strings.Contains(logs, "db fora") {
		t.Fatalf("esperava um WARN do gauge com o erro do banco:\n%s", logs)
	}
	if strings.Count(logs, "level=DEBUG") != 1 {
		t.Fatalf("a segunda coleta vai para debug:\n%s", logs)
	}
	if strings.Contains(logs, "exportação") {
		t.Fatalf("o erro do banco não pode sair como falha de exportação:\n%s", logs)
	}
}
