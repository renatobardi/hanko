package ingest

import (
	"testing"

	"github.com/google/uuid"
)

// Cada lista de colunas tem o mesmo número de valores: um descompasso quebraria o COPY só em produção.
func TestColumnsMatchValues(t *testing.T) {
	k := Key{ID: uuid.New(), CompanyID: uuid.New()}
	if n, m := len(SpanColumns), len((SpanRow{}).Values(k)); n != m {
		t.Errorf("spans: %d colunas, %d valores", n, m)
	}
	if n, m := len(LogColumns), len((LogRow{}).Values(k)); n != m {
		t.Errorf("logs: %d colunas, %d valores", n, m)
	}
	if n, m := len(MetricColumns), len((MetricRow{}).Values(k)); n != m {
		t.Errorf("metrics: %d colunas, %d valores", n, m)
	}
}
