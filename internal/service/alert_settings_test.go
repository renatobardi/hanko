package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// Os limites e os prazos de alerta são sobrescritas de um padrão do código: só o que a empresa mudou fica guardado.
func TestAlertSettings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	list, err := e.svc.ListAlertSettings(ctx, e.co.ID)
	if err != nil || len(list) != len(domain.AlertLimitSpecs)+len(domain.AlertDueSpecs()) {
		t.Fatalf("ListAlertSettings = %d (%v)", len(list), err)
	}
	for _, v := range list {
		if v.Overridden || v.Value != v.Spec.Default {
			t.Fatalf("%s: sem mudança devia valer o padrão: %+v", v.Spec.Key, v)
		}
	}
	if got, err := e.svc.AlertSettingValue(ctx, e.co.ID, "limit.host_no_data_minutes"); err != nil || got != 30 {
		t.Errorf("padrão do host sem dado = %v (%v), quer 30", got, err)
	}

	v, err := e.svc.PutAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.host_no_data_minutes", 45)
	if err != nil || !v.Overridden || v.Value != 45 || v.UpdatedAt == nil {
		t.Fatalf("PutAlertSetting = %+v (%v)", v, err)
	}
	if got, _ := e.svc.AlertSettingValue(ctx, e.co.ID, "limit.host_no_data_minutes"); got != 45 {
		t.Errorf("valor em vigor = %v, quer 45", got)
	}
	if v, err := e.svc.PutAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.host_no_data_minutes", 60); err != nil || v.Value != 60 {
		t.Errorf("trocar de novo = %+v (%v), quer 60", v, err)
	}
	// "sem prazo" é 0 e vale até 30 dias; fora da faixa ou chave desconhecida é recusado
	if v, err := e.svc.PutAlertSetting(ctx, domain.HumanActor, e.co.ID, "due.queue", 0); err != nil || v.Value != 0 || !v.Overridden {
		t.Errorf("prazo sem prazo = %+v (%v)", v, err)
	}
	if _, err := e.svc.PutAlertSetting(ctx, domain.HumanActor, e.co.ID, "due.queue", 721); err == nil {
		t.Error("prazo acima de 30 dias deveria ser recusado")
	}
	if _, err := e.svc.PutAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.nao_existe", 1); err == nil {
		t.Error("chave desconhecida deveria ser recusada")
	}

	if err := e.svc.ResetAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.host_no_data_minutes"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.AlertSettingValue(ctx, e.co.ID, "limit.host_no_data_minutes"); got != 30 {
		t.Errorf("depois de voltar ao padrão = %v, quer 30", got)
	}
	if err := e.svc.ResetAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.host_no_data_minutes"); err != nil {
		t.Errorf("voltar o que já é o padrão não faz nada: %v", err)
	}
	if err := e.svc.ResetAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.nao_existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("chave desconhecida: %v, quer ErrNotFound", err)
	}
	var events int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb IN ('alert_setting.updated', 'alert_setting.reset')`, e.co.ID).Scan(&events); err != nil || events != 4 {
		t.Errorf("eventos = %d (%v), quer 4 (3 trocas e 1 volta ao padrão)", events, err)
	}
}
