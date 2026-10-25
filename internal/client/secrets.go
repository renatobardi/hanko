package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Segredos, settings da instância e armazenamento de blobs (ADR-0032, ADR-0033).

// SecretScopePath devolve o segmento de rota do escopo: "secrets" (empresa) ou "user-secrets" (do usuário).
func SecretScopePath(user bool) string {
	if user {
		return "user-secrets"
	}
	return "secrets"
}

// CreateSecretReq cria um segredo.
type CreateSecretReq struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Value       string `json:"value,omitempty"`
	Generate    bool   `json:"generate,omitempty"`
	ExternalRef string `json:"external_ref,omitempty"`
	Collection  string `json:"collection,omitempty"`
}

// RotateSecretReq troca o valor de um segredo.
type RotateSecretReq struct {
	Provider    string  `json:"provider,omitempty"`
	Value       string  `json:"value,omitempty"`
	Generate    bool    `json:"generate,omitempty"`
	ExternalRef string  `json:"external_ref,omitempty"`
	Description *string `json:"description,omitempty"`
}

func secretPath(company string, user bool) string {
	return cp(company) + "/" + SecretScopePath(user)
}

// CreateSecret cria um segredo da empresa (user=false) ou do usuário.
func (c *Client) CreateSecret(ctx context.Context, company string, user bool, req CreateSecretReq) (api.Secret, error) {
	return get[api.Secret](ctx, c, http.MethodPost, secretPath(company, user), req)
}

// ListSecrets lista os segredos (metadados).
func (c *Client) ListSecrets(ctx context.Context, company string, user bool) ([]api.Secret, error) {
	return list[api.Secret](ctx, c, secretPath(company, user), nil)
}

// GetSecret devolve os metadados de um segredo.
func (c *Client) GetSecret(ctx context.Context, company string, user bool, name string) (api.Secret, error) {
	return get[api.Secret](ctx, c, http.MethodGet, secretPath(company, user)+"/"+url.PathEscape(name), nil)
}

// RotateSecret troca o valor.
func (c *Client) RotateSecret(ctx context.Context, company string, user bool, name string, req RotateSecretReq) (api.Secret, error) {
	return get[api.Secret](ctx, c, http.MethodPost, secretPath(company, user)+"/"+url.PathEscape(name)+"/rotate", req)
}

// RevealSecret mostra o valor uma única vez por versão.
func (c *Client) RevealSecret(ctx context.Context, company string, user bool, name string) (api.Secret, error) {
	return get[api.Secret](ctx, c, http.MethodPost, secretPath(company, user)+"/"+url.PathEscape(name)+"/reveal", nil)
}

// DeleteSecret apaga um segredo.
func (c *Client) DeleteSecret(ctx context.Context, company string, user bool, name string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	return c.do(ctx, http.MethodDelete, secretPath(company, user)+"/"+url.PathEscape(name), q, nil, nil)
}

// MigratePersonaEnv move o env em claro das personas para segredos cifrados.
func (c *Client) MigratePersonaEnv(ctx context.Context) (api.MigrateEnvResult, error) {
	return get[api.MigrateEnvResult](ctx, c, http.MethodPost, "/secrets/migrate-persona-env", nil)
}

// ListInstanceSettings lista as configurações da instalação.
func (c *Client) ListInstanceSettings(ctx context.Context) ([]api.InstanceSetting, error) {
	return list[api.InstanceSetting](ctx, c, "/instance/settings", nil)
}

// SetInstanceSetting grava uma configuração (value é o JSON do valor).
func (c *Client) SetInstanceSetting(ctx context.Context, key string, value json.RawMessage) (api.InstanceSetting, error) {
	return get[api.InstanceSetting](ctx, c, http.MethodPut, "/instance/settings/"+url.PathEscape(key), struct {
		Value json.RawMessage `json:"value"`
	}{value})
}

// UnsetInstanceSetting remove a configuração do banco.
func (c *Client) UnsetInstanceSetting(ctx context.Context, key string) (api.InstanceSetting, error) {
	return get[api.InstanceSetting](ctx, c, http.MethodDelete, "/instance/settings/"+url.PathEscape(key), nil)
}

// InstanceSettingsAudit devolve as mudanças recentes de configuração.
func (c *Client) InstanceSettingsAudit(ctx context.Context, limit int) ([]api.InstanceSettingAudit, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.InstanceSettingAudit](ctx, c, "/instance/settings-audit", q)
}

// BlobStatus devolve onde o conteúdo dos artefatos está guardado.
func (c *Client) BlobStatus(ctx context.Context) (api.BlobStatus, error) {
	return get[api.BlobStatus](ctx, c, http.MethodGet, "/instance/blobs", nil)
}
