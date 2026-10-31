package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/service"
)

// Limites e prazos de alerta da empresa (ADR-0083 itens 3 e 6).

func (s *server) registerAlertSettings(api huma.API) {
	const tag = "external-cost"
	b := "/companies/{company}/alert-settings"
	huma.Register(api, op("list-alert-settings", http.MethodGet, b, tag,
		"Lista os limites e os prazos de alerta da empresa, com o valor em vigor e o padrão"), s.listAlertSettings)
	huma.Register(api, op("put-alert-setting", http.MethodPut, b+"/{key}", tag, "Troca um limite ou um prazo de alerta (owner ou admin)"),
		s.putAlertSetting)
	del := op("reset-alert-setting", http.MethodDelete, b+"/{key}", tag, "Volta um limite ou um prazo ao padrão (owner ou admin)")
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, s.resetAlertSetting)
}

func alertSettingOut(v service.AlertSettingView) AlertSetting {
	return AlertSetting{Key: v.Spec.Key, Value: v.Value, Default: v.Spec.Default, Min: v.Spec.Min, Max: v.Spec.Max, Unit: v.Spec.Unit,
		Overridden: v.Overridden, UpdatedAt: v.UpdatedAt}
}

func (s *server) listAlertSettings(ctx context.Context, in *CompanyPath) (*ListOut[AlertSetting], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListAlertSettings(ctx, sc.Company.ID)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]AlertSetting, len(rows))
	for i, r := range rows {
		items[i] = alertSettingOut(r)
	}
	return &ListOut[AlertSetting]{Body: ListBody[AlertSetting]{Items: items, Limit: len(items)}}, nil
}

func (s *server) putAlertSetting(ctx context.Context, in *putAlertSettingIn) (*ItemOut[AlertSetting], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.PutAlertSetting(ctx, sc.Actor, sc.Company.ID, in.Key, in.Body.Value)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[AlertSetting]{Body: alertSettingOut(v)}, nil
}

func (s *server) resetAlertSetting(ctx context.Context, in *AlertSettingPath) (*struct{}, error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	if err := s.svc.ResetAlertSetting(ctx, sc.Actor, sc.Company.ID, in.Key); err != nil {
		return nil, s.fail(ctx, err)
	}
	return nil, nil
}
