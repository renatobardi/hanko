package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/store"
)

// Settings da instalação (ADR-0032 §C). São da instância, não de uma empresa: só o dono da instalação lê e muda, e a
// trilha é instance_setting_audit (sem company_id), na mesma transação da mudança. Só chaves do registro
// (internal/settings); a variável de ambiente da chave, se definida, vence e trava.

// InstanceSettingAudit é uma mudança registrada.
type InstanceSettingAudit struct {
	ID         uuid.UUID
	Key        string
	Old, New   json.RawMessage // nulo = não havia / voltou ao padrão
	ActorType  string
	ActorID    *uuid.UUID
	OccurredAt time.Time
}

// requireInstanceOwner exige o dono da instalação; sem identidade (modo trusted) passa.
func requireInstanceOwner(ctx context.Context) (*uuid.UUID, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return nil, nil
	}
	if id.User.Role != domain.RoleOwner {
		return nil, domain.ErrForbidden
	}
	uid := id.User.ID
	return &uid, nil
}

func (s *Service) settingsManager() (*settings.Manager, error) {
	if s.Settings == nil {
		return nil, domain.Invalid("settings", "settings da instância indisponíveis neste servidor")
	}
	return s.Settings, nil
}

// LoadInstanceSettings (re)lê o banco para o gerenciador. Roda no boot e depois de cada mudança. Linhas inválidas
// são ignoradas (e devolvidas), nunca derrubam a instalação.
func (s *Service) LoadInstanceSettings(ctx context.Context) ([]string, error) {
	m, err := s.settingsManager()
	if err != nil {
		return nil, err
	}
	rows, err := s.read().ListInstanceSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list instance settings: %w", err)
	}
	raw := make(map[string]json.RawMessage, len(rows))
	for _, r := range rows {
		raw[r.Key] = json.RawMessage(r.Value)
	}
	return m.Load(raw), nil
}

// ListInstanceSettings devolve todas as chaves registradas com o valor efetivo, a origem e o travamento.
func (s *Service) ListInstanceSettings(ctx context.Context) ([]settings.Entry, error) {
	if _, err := requireInstanceOwner(ctx); err != nil {
		return nil, err
	}
	m, err := s.settingsManager()
	if err != nil {
		return nil, err
	}
	return m.Entries(), nil
}

// SetInstanceSetting grava o valor de uma chave. Chave desconhecida é 422; chave travada por env, 409.
func (s *Service) SetInstanceSetting(ctx context.Context, actor domain.Actor, key string, raw json.RawMessage) (settings.Entry, error) {
	return s.writeInstanceSetting(ctx, actor, key, raw, false)
}

// UnsetInstanceSetting remove o valor do banco (volta ao yaml/padrão).
func (s *Service) UnsetInstanceSetting(ctx context.Context, actor domain.Actor, key string) (settings.Entry, error) {
	return s.writeInstanceSetting(ctx, actor, key, nil, true)
}

func (s *Service) writeInstanceSetting(ctx context.Context, actor domain.Actor, key string, raw json.RawMessage, unset bool) (settings.Entry, error) {
	uid, err := requireInstanceOwner(ctx)
	if err != nil {
		return settings.Entry{}, err
	}
	if actor.Type == domain.ActorPersona {
		return settings.Entry{}, domain.ErrForbidden
	}
	m, err := s.settingsManager()
	if err != nil {
		return settings.Entry{}, err
	}
	def, ok := settings.Lookup(key)
	if !ok {
		return settings.Entry{}, domain.Invalid("key", "chave de configuração desconhecida")
	}
	if m.Locked(key) {
		return settings.Entry{}, fmt.Errorf("%s está definida por %s no ambiente do servidor; mude lá: %w", key, def.Env, domain.ErrConflict)
	}
	var newVal json.RawMessage
	if !unset {
		typed, err := m.Check(key, raw)
		if err != nil {
			return settings.Entry{}, domain.Invalid(key, err.Error())
		}
		if err := s.checkSettingsCross(m, key, typed); err != nil {
			return settings.Entry{}, err
		}
		newVal = settings.ToJSON(typed)
	}
	err = s.withTx(ctx, func(t *txc) error {
		var old json.RawMessage
		cur, err := t.q.GetInstanceSettingForUpdate(t.ctx, key)
		switch {
		case err == nil:
			old = json.RawMessage(cur.Value)
		case !isNoRows(err):
			return fmt.Errorf("get instance setting: %w", err)
		}
		if unset {
			if old == nil {
				return nil
			}
			if _, err := t.q.DeleteInstanceSetting(t.ctx, key); err != nil {
				return fmt.Errorf("delete instance setting: %w", err)
			}
		} else {
			if string(old) == string(newVal) {
				return nil
			}
			if err := t.q.UpsertInstanceSetting(t.ctx, store.UpsertInstanceSettingParams{Key: key, Value: newVal, UpdatedBy: uid, Now: t.now}); err != nil {
				return fmt.Errorf("upsert instance setting: %w", err)
			}
		}
		if err := t.q.CreateInstanceSettingAudit(t.ctx, store.CreateInstanceSettingAuditParams{ID: domain.NewID(), Key: key,
			OldValue: old, NewValue: newVal, ActorType: string(actor.Type), ActorID: uid, Now: t.now}); err != nil {
			return fmt.Errorf("audit instance setting: %w", err)
		}
		if s.Log != nil {
			s.Log.Info("instance_settings: alterado", "key", key, "old", string(old), "new", string(newVal), "unset", unset)
		}
		return nil
	})
	if err != nil {
		return settings.Entry{}, err
	}
	if _, err := s.LoadInstanceSettings(ctx); err != nil {
		return settings.Entry{}, err
	}
	for _, e := range m.Entries() {
		if e.Key == key {
			return e, nil
		}
	}
	return settings.Entry{}, errors.New("instance setting: chave sumiu do registro")
}

// checkSettingsCross confere as relações entre chaves: a cota por empresa tem de caber ao menos um arquivo máximo.
func (s *Service) checkSettingsCross(m *settings.Manager, key string, typed any) error {
	switch key {
	case settings.ArtifactsMaxBytes:
		if n := typed.(int64); n > m.Int(settings.ArtifactsQuotaBytes) {
			return domain.Invalid(key, "maior que a cota por empresa (artifacts.quota_bytes)")
		}
	case settings.ArtifactsQuotaBytes:
		if n := typed.(int64); n < m.Int(settings.ArtifactsMaxBytes) {
			return domain.Invalid(key, "menor que o tamanho máximo de artefato (artifacts.max_bytes)")
		}
	}
	return nil
}

// ListInstanceSettingAudit devolve as mudanças mais recentes.
func (s *Service) ListInstanceSettingAudit(ctx context.Context, limit int) ([]InstanceSettingAudit, error) {
	if _, err := requireInstanceOwner(ctx); err != nil {
		return nil, err
	}
	rows, err := s.read().ListInstanceSettingAudit(ctx, i32(clamp(limit, 50, 200)))
	if err != nil {
		return nil, fmt.Errorf("list instance setting audit: %w", err)
	}
	out := make([]InstanceSettingAudit, len(rows))
	for i, r := range rows {
		out[i] = InstanceSettingAudit{ID: r.ID, Key: r.Key, Old: r.OldValue, New: r.NewValue, ActorType: r.ActorType, ActorID: r.ActorID, OccurredAt: r.OccurredAt}
	}
	return out, nil
}
