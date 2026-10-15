package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// A trava semanal da assinatura e o hard stop de orçamento chegam ao cliente como 409 (conflito)
// com o motivo no detalhe, sem vazar erro interno.
func TestTravaSemanalEOrcamentoSao409(t *testing.T) {
	for name, err := range map[string]error{
		"trava semanal": fmt.Errorf("trava semanal da assinatura %q: uso 8.50 de 10.00 (80%%): %w", "Plano", domain.ErrSubscriptionGuard),
		"orçamento":     fmt.Errorf("persona sem orçamento: %w", domain.ErrBudgetExceeded),
	} {
		status, known := statusFor(err)
		if status != http.StatusConflict || !known {
			t.Errorf("%s: status %d known=%v, quer 409", name, status, known)
		}
		p, known := toProblem(err)
		if p.Status != http.StatusConflict || !known || p.Title != "Conflito" {
			t.Errorf("%s: problem %+v", name, p)
		}
	}
	p, _ := toProblem(fmt.Errorf("trava semanal da assinatura %q: %w", "Plano", domain.ErrSubscriptionGuard))
	if !strings.Contains(p.Detail, "trava semanal") || !strings.Contains(p.Detail, "Plano") {
		t.Errorf("o detalhe deve dizer o motivo: %q", p.Detail)
	}
	if g, b := errors.Is(domain.ErrSubscriptionGuard, domain.ErrBudgetExceeded), errors.Is(domain.ErrBudgetExceeded, domain.ErrSubscriptionGuard); g || b {
		t.Error("trava e orçamento têm motivos distintos")
	}
}
