package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Limites e prazos de alerta por empresa (ADR-0083 itens 3 e 6). A tabela `company_alert_settings` só guarda o que a empresa
// mudou; o padrão vale pelo código (`domain.AlertLimitSpecs` e `domain.AlertDueSpecs`), com os valores da Oute.

// AlertSettingView é um limite ou um prazo com o valor em vigor.
type AlertSettingView struct {
	Spec       domain.AlertSettingSpec
	Value      float64
	Overridden bool
	UpdatedAt  *time.Time
}

func alertSettingViews(overrides map[string]alertOverride) []AlertSettingView {
	specs := append(append([]domain.AlertSettingSpec{}, domain.AlertLimitSpecs...), domain.AlertDueSpecs()...)
	out := make([]AlertSettingView, 0, len(specs))
	for _, sp := range specs {
		v := AlertSettingView{Spec: sp, Value: sp.Default}
		if o, ok := overrides[sp.Key]; ok {
			at := o.updatedAt
			v.Value, v.Overridden, v.UpdatedAt = o.value, true, &at
		}
		out = append(out, v)
	}
	return out
}

type alertOverride struct {
	value     float64
	updatedAt time.Time
}

func (s *Service) alertOverrides(ctx context.Context, companyID domain.ID) (map[string]alertOverride, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key, value::float8, updated_at FROM company_alert_settings WHERE company_id = $1`, companyID)
	if err != nil {
		return nil, fmt.Errorf("alert settings: %w", err)
	}
	defer rows.Close()
	out := map[string]alertOverride{}
	for rows.Next() {
		var k string
		var o alertOverride
		if err := rows.Scan(&k, &o.value, &o.updatedAt); err != nil {
			return nil, fmt.Errorf("alert settings: %w", err)
		}
		out[k] = o
	}
	return out, rows.Err()
}

// AlertSettingValue devolve o valor em vigor de um ajuste da empresa (o da empresa ou o padrão). Chave desconhecida é erro.
func (s *Service) AlertSettingValue(ctx context.Context, companyID domain.ID, key string) (float64, error) {
	spec, ok := domain.AlertSettingSpecOf(key)
	if !ok {
		return 0, domain.Invalid("key", "unknown alert setting")
	}
	var v float64
	err := s.Pool.QueryRow(ctx, `SELECT value::float8 FROM company_alert_settings WHERE company_id = $1 AND key = $2`, companyID, key).Scan(&v)
	if err != nil {
		if isNoRows(err) {
			return spec.Default, nil
		}
		return 0, fmt.Errorf("alert settings: %w", err)
	}
	return v, nil
}

// ListAlertSettings lista os limites e os prazos de alerta da empresa, com o valor em vigor e o padrão.
func (s *Service) ListAlertSettings(ctx context.Context, companyID domain.ID) ([]AlertSettingView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	ov, err := s.alertOverrides(ctx, companyID)
	if err != nil {
		return nil, err
	}
	return alertSettingViews(ov), nil
}

func (s *Service) alertSettingView(ctx context.Context, companyID domain.ID, key string) (AlertSettingView, error) {
	ov, err := s.alertOverrides(ctx, companyID)
	if err != nil {
		return AlertSettingView{}, err
	}
	for _, v := range alertSettingViews(ov) {
		if v.Spec.Key == key {
			return v, nil
		}
	}
	return AlertSettingView{}, domain.ErrNotFound
}

// PutAlertSetting troca um limite ou um prazo da empresa (owner ou admin), com `activity_event`.
func (s *Service) PutAlertSetting(ctx context.Context, actor domain.Actor, companyID domain.ID, key string, value float64) (AlertSettingView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return AlertSettingView{}, err
	}
	if err := domain.ValidateAlertSetting(key, value); err != nil {
		return AlertSettingView{}, err
	}
	actorType := "system"
	var actorID *domain.ID
	if actor.Type == domain.ActorHuman {
		actorType, actorID = "human", actor.ID
	}
	err := s.withTx(ctx, func(t *txc) error {
		if _, err := t.tx.Exec(ctx, `INSERT INTO company_alert_settings (company_id, key, value, updated_by_type, updated_by_id, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (company_id, key) DO UPDATE SET value = EXCLUDED.value, updated_by_type = EXCLUDED.updated_by_type,
			  updated_by_id = EXCLUDED.updated_by_id, updated_at = EXCLUDED.updated_at`, companyID, key, value, actorType, actorID, t.now); err != nil {
			return mapErr(err, "alert setting")
		}
		return t.activity(actor, companyID, "alert_setting.updated", "company", companyID, map[string]any{"key": key, "value": value}, "")
	})
	if err != nil {
		return AlertSettingView{}, err
	}
	return s.alertSettingView(ctx, companyID, key)
}

// ResetAlertSetting volta um limite ou um prazo ao padrão (owner ou admin). Voltar o que já é o padrão não faz nada.
func (s *Service) ResetAlertSetting(ctx context.Context, actor domain.Actor, companyID domain.ID, key string) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	if _, ok := domain.AlertSettingSpecOf(key); !ok {
		return domain.ErrNotFound
	}
	return s.withTx(ctx, func(t *txc) error {
		tag, err := t.tx.Exec(ctx, `DELETE FROM company_alert_settings WHERE company_id = $1 AND key = $2`, companyID, key)
		if err != nil {
			return mapErr(err, "alert setting")
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return t.activity(actor, companyID, "alert_setting.reset", "company", companyID, map[string]any{"key": key}, "")
	})
}
