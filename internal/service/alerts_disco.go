package service

import (
	"fmt"
	"math"

	"github.com/renatobardi/hanko/internal/domain"
)

// DBDiskConfig diz onde medir o espaço livre do disco do Postgres. Vazio = o alerta db_disk_low não é avaliado.
type DBDiskConfig struct {
	Path string
	Free func(path string) (uint64, error)
}

const gib = float64(1 << 30)

// diskConditions abre db_disk_low, um por empresa com telemetria nos últimos 30 dias, quando o espaço livre do disco do
// Postgres fica abaixo do limite de aviso (`limit.db_disk_warn_gib`, severidade warning) ou de falha
// (`limit.db_disk_critical_gib`, critical) da empresa (comentário da #66, Q35; ADR-0083). O disco é um só: a medida vale
// para todas, e cada empresa compara com os próprios limites.
func (t *txc) diskConditions() ([]alertCond, error) {
	cfg := t.s.DBDisk
	if cfg.Path == "" || cfg.Free == nil {
		return nil, nil
	}
	free, err := cfg.Free(cfg.Path)
	if err != nil {
		return nil, nil // não medir não é alerta; o hanko doctor mostra a falha da medição
	}
	freeGiB := float64(free) / gib
	warn, err := t.companyLimit("limit.db_disk_warn_gib")
	if err != nil {
		return nil, err
	}
	critical, err := t.companyLimit("limit.db_disk_critical_gib")
	if err != nil {
		return nil, err
	}
	rows, err := t.tx.Query(t.ctx, `SELECT DISTINCT company_id FROM external_host WHERE last_seen_at >= $1`, t.now.AddDate(0, 0, -usageDays))
	if err != nil {
		return nil, fmt.Errorf("alert conditions: empresas com telemetria: %w", err)
	}
	defer rows.Close()
	var out []alertCond
	for rows.Next() {
		var co domain.ID
		if err := rows.Scan(&co); err != nil {
			return nil, fmt.Errorf("alert conditions: empresas com telemetria: %w", err)
		}
		sev, limit := domain.AlertSeverity(""), 0.0
		switch {
		case freeGiB < critical(co):
			sev, limit = domain.AlertCritical, critical(co)
		case freeGiB < warn(co):
			sev, limit = domain.AlertWarning, warn(co)
		default:
			continue
		}
		out = append(out, alertCond{companyID: co, kind: domain.AlertDBDiskLow, subjectType: "company", subjectID: co, severity: sev,
			message: fmt.Sprintf("O disco do Postgres tem %.1f GiB livres (limite %.1f GiB)", math.Round(freeGiB*10)/10, limit),
			data:    map[string]any{"free_bytes": free, "limit_gib": limit, "path": cfg.Path}})
	}
	return out, rows.Err()
}
