package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Sem prova recente, a API responde 401 com o código reauth_required: a UI usa isso para abrir o pedido de prova e
// repetir a ação (ADR-0070 §2). Provas erradas demais respondem 429.
func TestReauthErrosViramHTTP(t *testing.T) {
	reauth := fmt.Errorf("ação: %w", &domain.ReauthError{WindowMinutes: 5})
	p, known := toProblem(reauth)
	if !known {
		t.Fatal("erro de reauth é conhecido, não 500")
	}
	if p.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, quer 401", p.Status)
	}
	if !strings.HasPrefix(p.Detail, "reauth_required") || !strings.Contains(p.Detail, "5 min") {
		t.Fatalf("detail = %q, quer reauth_required com a janela", p.Detail)
	}

	for _, c := range []struct {
		name   string
		err    error
		status int
	}{
		{"muitas provas erradas", domain.ErrTooManyAttempts, http.StatusTooManyRequests},
		{"credencial recusada", domain.ErrUnauthenticated, http.StatusUnauthorized},
		{"API key de escopo read", fmt.Errorf("x: %w", domain.ErrNotAllowed), http.StatusForbidden},
		{"passkey desligada", service.ErrPasskeyOff, http.StatusServiceUnavailable},
	} {
		p, known := toProblem(c.err)
		if !known || p.Status != c.status {
			t.Errorf("%s: status = %d (conhecido=%v), quer %d", c.name, p.Status, known, c.status)
		}
	}
}
