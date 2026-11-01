package service_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O alerta de disco abre por empresa com telemetria recente, na severidade do limite que o espaço livre cruzou, e fecha quando
// o espaço volta (comentário da #66; ADR-0083).
func TestDBDiskLowAlert(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO external_host (id, company_id, name, last_seen_at) VALUES ($1, $2, 'h1', now())`, domain.NewID(), e.co.ID); err != nil {
		t.Fatal(err)
	}
	var free uint64
	e.svc.DBDisk = service.DBDiskConfig{Path: "/dados", Free: func(string) (uint64, error) { return free, nil }}
	severity := func() string {
		var s string
		err := e.svc.Pool.QueryRow(ctx, `SELECT COALESCE((SELECT severity FROM alerts WHERE company_id = $1 AND kind = 'db_disk_low' AND closed_at IS NULL), 'none')`, e.co.ID).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	eval := func(gib uint64) string {
		free = gib << 30
		if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
			t.Fatal(err)
		}
		return severity()
	}
	if got := eval(50); got != "none" {
		t.Errorf("50 GiB livres: %q, quer none (acima dos 20 GiB de aviso)", got)
	}
	if got := eval(10); got != "warning" {
		t.Errorf("10 GiB livres: %q, quer warning (abaixo de 20)", got)
	}
	if got := eval(2); got != "critical" {
		t.Errorf("2 GiB livres: %q, quer critical (abaixo de 5)", got)
	}
	if got := eval(50); got != "none" {
		t.Errorf("50 GiB de novo: %q, quer none (o alerta fecha)", got)
	}
	// o limite da empresa sobe: 50 GiB livres já é pouco
	if _, err := e.svc.PutAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.db_disk_warn_gib", 100); err != nil {
		t.Fatal(err)
	}
	if got := eval(50); got != "warning" {
		t.Errorf("50 GiB livres com aviso em 100: %q, quer warning", got)
	}
	e.svc.DBDisk = service.DBDiskConfig{}
	if got := eval(1); got != "none" {
		t.Errorf("sem caminho configurado o alerta não é avaliado e o aberto fecha: %q", got)
	}
}
