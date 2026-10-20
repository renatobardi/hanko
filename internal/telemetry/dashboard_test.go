package telemetry

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// O painel "Erros" do Grafana (ADR-0058 §C) é provisionado pelo compose: o JSON precisa ser válido, usar só os
// datasources do otel-lgtm e consultar as métricas que o código realmente emite.

type dashPanel struct {
	ID         int    `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Datasource struct {
		UID string `json:"uid"`
	} `json:"datasource"`
	Targets []struct {
		Expr  string `json:"expr"`
		Query string `json:"query"`
	} `json:"targets"`
}

func TestErrorsDashboardIsValidAndMatchesEmittedMetrics(t *testing.T) {
	b, err := os.ReadFile("../../deploy/grafana/dashboards/hanko-erros.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		UID    string      `json:"uid"`
		Title  string      `json:"title"`
		Panels []dashPanel `json:"panels"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if d.UID != "hanko-erros" || !strings.Contains(d.Title, "Erros") || len(d.Panels) < 6 {
		t.Fatalf("painel inesperado: uid=%q title=%q panels=%d", d.UID, d.Title, len(d.Panels))
	}
	ids := map[int]bool{}
	var text strings.Builder
	for _, p := range d.Panels {
		if ids[p.ID] {
			t.Errorf("id de painel repetido: %d", p.ID)
		}
		ids[p.ID] = true
		switch p.Datasource.UID {
		case "prometheus", "tempo", "loki":
		default:
			t.Errorf("painel %q usa datasource %q fora do otel-lgtm", p.Title, p.Datasource.UID)
		}
		for _, tg := range p.Targets {
			text.WriteString(tg.Expr + tg.Query + "\n")
		}
	}
	all := text.String()
	// hanko.errors e hanko.ratelimit.decisions viram *_total no Prometheus/Mimir
	for _, want := range []string{"hanko_errors_total", "hanko_ratelimit_decisions_total", "status = error", "detected_level"} {
		if !strings.Contains(all, want) {
			t.Errorf("nenhuma consulta usa %q", want)
		}
	}

	prov, err := os.ReadFile("../../deploy/grafana/provisioning/dashboards/hanko.yaml")
	if err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile("../../deploy/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prov), "/hanko-dashboards") {
		t.Error("o provider deve apontar para /hanko-dashboards")
	}
	for _, want := range []string{"./grafana/dashboards:/hanko-dashboards:ro", "/otel-lgtm/grafana/conf/provisioning/dashboards/hanko.yaml"} {
		if !strings.Contains(string(compose), want) {
			t.Errorf("o compose não monta %q", want)
		}
	}
}
