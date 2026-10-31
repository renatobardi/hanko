package service

import (
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

func TestMonthlyHistoryValueOn(t *testing.T) {
	id := domain.NewID()
	rec := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := monthlyHistory{id: {
		{startsOn: "2026-03-01", recordedAt: rec, monthly: 100},
		{startsOn: "2026-05-01", recordedAt: rec, monthly: 150},
		{startsOn: "2026-05-01", recordedAt: rec.Add(time.Hour), monthly: 170}, // a registrada por último desempata
	}}
	for _, c := range []struct {
		day  string
		want float64
	}{
		{"2026-02-10", 100}, // antes da primeira linha vale a primeira
		{"2026-03-01", 100}, // no início da linha
		{"2026-04-30", 100},
		{"2026-05-01", 170},
		{"2026-12-31", 170},
	} {
		if got := h.valueOn(id, c.day, 999); got != c.want {
			t.Errorf("valueOn(%s) = %v, quer %v", c.day, got, c.want)
		}
	}
	if got := h.valueOn(domain.NewID(), "2026-05-01", 42); got != 42 {
		t.Errorf("assinatura sem histórico vale o valor atual: %v", got)
	}
}
