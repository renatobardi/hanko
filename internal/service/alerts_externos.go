package service

import (
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// hostNoDataConditions abre um alerta host_no_data por host externo sempre ligado, sem fato há mais do limite da empresa
// (ADR-0077 item 4; ADR-0083 itens 2 e 5). Sem fato nenhum, o prazo conta da criação do host. Quando o fato chega, a
// condição some e o alerta fecha pelo motor (EvaluateAlerts).
func (t *txc) hostNoDataConditions() ([]alertCond, error) {
	rows, err := t.q.ListAlwaysOnHostsLimits(t.ctx)
	if err != nil {
		return nil, fmt.Errorf("list always-on hosts: %w", err)
	}
	var out []alertCond
	for _, h := range rows {
		last := h.CreatedAt
		if h.LastSeenAt != nil {
			last = *h.LastSeenAt
		}
		limit := time.Duration(h.LimitMinutes * float64(time.Minute))
		silent := t.now.Sub(last)
		if silent <= limit {
			continue
		}
		out = append(out, alertCond{companyID: h.CompanyID, kind: domain.AlertHostNoData, subjectType: domain.AlertSubjectExternalHost,
			subjectID: h.ID, severity: domain.ExternalAlertSeverity(domain.AlertHostNoData),
			message: fmt.Sprintf("Host %s sem dado há %s (limite %s)", h.Name, humanDuration(silent), humanDuration(limit)),
			data:    map[string]any{"host": h.Name, "last_seen_at": last, "limit_minutes": h.LimitMinutes}})
	}
	return out, nil
}
