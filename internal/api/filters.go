package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// splitCSV separa "a, b,,c" em ["a","b","c"].
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseTaskStatuses converte o filtro `status` (csv) validando cada valor.
func parseTaskStatuses(csv string) ([]domain.TaskStatus, error) {
	var out []domain.TaskStatus
	for _, p := range splitCSV(csv) {
		st := domain.TaskStatus(p)
		if !st.Valid() {
			return nil, domain.Invalid("status", fmt.Sprintf("valor desconhecido %q", p))
		}
		out = append(out, st)
	}
	return out, nil
}

// parseRunStatuses converte o filtro `status` (csv) de execuções.
func parseRunStatuses(csv string) ([]domain.RunStatus, error) {
	var out []domain.RunStatus
	for _, p := range splitCSV(csv) {
		st := domain.RunStatus(p)
		if !st.Valid() {
			return nil, domain.Invalid("status", fmt.Sprintf("valor desconhecido %q", p))
		}
		out = append(out, st)
	}
	return out, nil
}

// parseTime lê um instante RFC 3339 opcional.
func parseTime(field, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil, domain.Invalid(field, "esperado RFC 3339")
	}
	return &t, nil
}
