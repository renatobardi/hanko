package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/service"
)

// hostAlertRows conta os alertas host_no_data da empresa, abertos ou fechados.
func hostAlertRows(t *testing.T, e *env, aberto bool) int {
	t.Helper()
	q := `SELECT count(*) FROM alerts WHERE company_id = $1 AND kind = 'host_no_data' AND closed_at IS NULL`
	if !aberto {
		q = `SELECT count(*) FROM alerts WHERE company_id = $1 AND kind = 'host_no_data' AND closed_at IS NOT NULL`
	}
	return rowCount(t, e, q, e.co.ID)
}

// envHost grava um fato do host oute-server e devolve o ambiente, a chave de ingestão, o admin e o id do host.
func envHost(t *testing.T, email string) (*env, ingest.Key, domain.User, domain.ID) {
	t.Helper()
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, email, domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "n1", agent: "claude", host: "oute-server", at: time.Now().UnixNano()}))
	return e, key, admin, entityID(t, e, "external_host", "oute-server")
}

// envarelha põe o último fato do host há `minutos` minutos (o teste não espera o relógio).
func envarelha(t *testing.T, e *env, host domain.ID, minutos int) {
	t.Helper()
	if _, err := e.svc.Pool.Exec(context.Background(), `UPDATE external_host SET last_seen_at = now() - make_interval(mins => $2) WHERE id = $1`, host, minutos); err != nil {
		t.Fatal(err)
	}
}

// Sempre ligado e sem fato há mais de 30 min: abre host_no_data; o fato que chega fecha o alerta (ADR-0077 item 4).
func TestHostNoDataAlertOpensAndClosesWithTheFacts(t *testing.T) {
	e, key, admin, host := envHost(t, "admin.alerta@x.com")
	sempre := true
	if _, err := e.svc.UpdateExternalHost(as(admin), domain.HumanActor, e.co.ID, host, service.ExternalHostPatch{AlwaysOn: &sempre}); err != nil {
		t.Fatal(err)
	}
	envarelha(t, e, host, 40)
	if _, err := e.svc.EvaluateAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := hostAlertRows(t, e, true); n != 1 {
		t.Fatalf("alertas host_no_data abertos = %d, quero 1", n)
	}

	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "n2", agent: "claude", host: "oute-server", at: at}))
	if _, err := e.svc.EvaluateAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := hostAlertRows(t, e, true); n != 0 {
		t.Errorf("alerta ainda aberto depois do fato = %d, quero 0", n)
	}
	if n := hostAlertRows(t, e, false); n != 1 {
		t.Errorf("alerta fechado = %d, quero 1", n)
	}
}

// Quem não entra no alerta: host que não é sempre ligado, host arquivado, e host dentro do limite da empresa.
func TestHostNoDataAlertStaysQuietWhenItShouldNot(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, e *env, admin domain.User, host domain.ID)
		minuto int
	}{
		{"host que não é sempre ligado", func(*testing.T, *env, domain.User, domain.ID) {}, 40},
		{"host arquivado", func(t *testing.T, e *env, admin domain.User, host domain.ID) {
			sempre, arq := true, true
			if _, err := e.svc.UpdateExternalHost(as(admin), domain.HumanActor, e.co.ID, host, service.ExternalHostPatch{AlwaysOn: &sempre, Archived: &arq}); err != nil {
				t.Fatal(err)
			}
		}, 40},
		{"sem dado dentro do limite da empresa (60 min)", func(t *testing.T, e *env, admin domain.User, host domain.ID) {
			sempre := true
			if _, err := e.svc.UpdateExternalHost(as(admin), domain.HumanActor, e.co.ID, host, service.ExternalHostPatch{AlwaysOn: &sempre}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO company_alert_settings (company_id, key, value, updated_by_type) VALUES ($1, 'limit.host_no_data_minutes', 60, 'human')`, e.co.ID); err != nil {
				t.Fatal(err)
			}
		}, 40},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _, admin, host := envHost(t, "admin.quieto@x.com")
			c.setup(t, e, admin, host)
			envarelha(t, e, host, c.minuto)
			if _, err := e.svc.EvaluateAlerts(context.Background()); err != nil {
				t.Fatal(err)
			}
			if n := hostAlertRows(t, e, true); n != 0 {
				t.Errorf("alerta host_no_data aberto = %d, quero 0", n)
			}
		})
	}
}

// hostDireto põe um host externo sempre ligado, sem nenhum fato, criado `minutosCriado` minutos atrás.
func hostDireto(t *testing.T, e *env, company domain.ID, name string, minutosCriado int) domain.ID {
	t.Helper()
	var id domain.ID
	if err := e.svc.Pool.QueryRow(context.Background(), `INSERT INTO external_host (id, company_id, name, always_on, created_at, updated_at)
		VALUES ($1, $2, $3, true, now() - make_interval(mins => $4), now()) RETURNING id`,
		domain.NewID(), company, name, minutosCriado).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Host sem nenhum fato: o prazo conta da criação (ADR-0077 item 4). Criado há 40 min abre; criado há 5 min não.
func TestHostNoDataWithoutAnyFactCountsFromCreation(t *testing.T) {
	e, _, _ := derivEnv(t)
	parado := hostDireto(t, e, e.co.ID, "sem-fato-antigo", 40)
	hostDireto(t, e, e.co.ID, "sem-fato-novo", 5)
	if _, err := e.svc.EvaluateAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := hostAlertRows(t, e, true); n != 1 {
		t.Fatalf("alertas host_no_data abertos = %d, quero 1 (só o host criado há 40 min)", n)
	}
	if rowCount(t, e, `SELECT count(*) FROM alerts WHERE company_id = $1 AND kind = 'host_no_data' AND closed_at IS NULL AND subject_id = $2`, e.co.ID, parado) != 1 {
		t.Errorf("o alerta aberto não é o do host criado há 40 min")
	}
}

// Outra empresa: o alerta sai com a empresa do host parado, e o host de outra empresa em dia não abre nada.
func TestHostNoDataAlertBelongsToItsOwnCompany(t *testing.T) {
	e, _, _ := derivEnv(t)
	hostDireto(t, e, e.co.ID, "em-dia", 5)
	other, err := e.svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	hostDireto(t, e, other.ID, "parado-outra", 40)
	if _, err := e.svc.EvaluateAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM alerts WHERE company_id = $1 AND kind = 'host_no_data' AND closed_at IS NULL`, e.co.ID); n != 0 {
		t.Errorf("alertas na empresa do host em dia = %d, quero 0", n)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM alerts WHERE company_id = $1 AND kind = 'host_no_data' AND closed_at IS NULL`, other.ID); n != 1 {
		t.Errorf("alertas na empresa do host parado = %d, quero 1", n)
	}
}

// Passada repetida não duplica: o segundo passe atualiza o alerta aberto, sem abrir outro nem gerar outro alert.opened.
func TestHostNoDataRepeatedPassDoesNotDuplicate(t *testing.T) {
	e, _, _ := derivEnv(t)
	hostDireto(t, e, e.co.ID, "parado", 40)
	if _, err := e.svc.EvaluateAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
	ev, err := e.svc.EvaluateAlerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ev.Opened != 0 || ev.Open != 1 {
		t.Errorf("segunda passada = abriu %d, aberto %d; quero 0 e 1", ev.Opened, ev.Open)
	}
	if n := hostAlertRows(t, e, true); n != 1 {
		t.Errorf("alertas abertos depois de duas passadas = %d, quero 1", n)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'alert.opened' AND data->>'kind' = 'host_no_data'`, e.co.ID); n != 1 {
		t.Errorf("eventos alert.opened de host_no_data = %d, quero 1", n)
	}
}
