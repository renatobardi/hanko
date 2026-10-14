package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestStatusFor(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		want  int
		known bool
	}{
		{"nil", nil, 200, true},
		{"not found", domain.ErrNotFound, 404, true},
		{"not found embrulhado", fmt.Errorf("get task: %w", domain.ErrNotFound), 404, true},
		{"invalid", domain.ErrInvalid, 422, true},
		{"validation error", domain.Invalid("title", "vazio"), 422, true},
		{"conflict", domain.ErrConflict, 409, true},
		{"active run", domain.ErrActiveRun, 409, true},
		{"not allowed", domain.ErrNotAllowed, 403, true},
		{"token ruim", fmt.Errorf("%w: expirou", auth.ErrBadToken), 401, true},
		{"não implementado", service.ErrNotImplemented, 501, true},
		{"deadline", context.DeadlineExceeded, 504, true},
		{"desconhecido", errors.New("pq: connection refused host=10.0.0.5"), 500, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, known := statusFor(tc.err)
			if got != tc.want || known != tc.known {
				t.Fatalf("statusFor = (%d, %v), quer (%d, %v)", got, known, tc.want, tc.known)
			}
		})
	}
}

func TestToProblemNaoVazaDetalhes(t *testing.T) {
	p, known := toProblem(errors.New("pq: senha do banco = hunter2"))
	if known || p.Status != http.StatusInternalServerError {
		t.Fatalf("esperava 500 desconhecido, veio %d known=%v", p.Status, known)
	}
	if strings.Contains(p.Detail, "hunter2") || strings.Contains(p.Detail, "pq") {
		t.Fatalf("detalhe vazou erro interno: %q", p.Detail)
	}
}

func TestToProblemValidationTemCampo(t *testing.T) {
	p, _ := toProblem(fmt.Errorf("criar: %w", domain.Invalid("title", "obrigatório")))
	if p.Status != 422 || len(p.Errors) != 1 || p.Errors[0].Location != "title" {
		t.Fatalf("problem inesperado: %+v", p)
	}
}

func TestErrosViaHTTP(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{domain.ErrNotFound, 404}, {domain.ErrInvalid, 422}, {domain.ErrConflict, 409},
		{domain.ErrActiveRun, 409}, {domain.ErrNotAllowed, 403}, {errors.New("boom"), 500},
	}
	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			f := &fakeSvc{failWith: tc.err}
			ts, _ := newTestServer(t, f, nil)
			resp, body := do(t, "GET", ts.URL+"/api/v1/companies/acme", "", nil)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, quer %d (%s)", resp.StatusCode, tc.want, body)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "problem+json") {
				t.Fatalf("content-type = %q", ct)
			}
			if strings.Contains(body, "boom") {
				t.Fatalf("erro interno vazou: %s", body)
			}
		})
	}
}
