package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// Campos e coleções do cofre (ADR-0068, fatia 1).

// CreateFieldReq cria um campo em um segredo.
type CreateFieldReq struct {
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"`
	Sensitive   *bool  `json:"sensitive,omitempty"`
	Position    int    `json:"position,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Value       string `json:"value,omitempty"`
	Generate    bool   `json:"generate,omitempty"`
	ExternalRef string `json:"external_ref,omitempty"`
}

// RotateFieldReq troca o valor de um campo.
type RotateFieldReq struct {
	Provider    string `json:"provider,omitempty"`
	Value       string `json:"value,omitempty"`
	Generate    bool   `json:"generate,omitempty"`
	ExternalRef string `json:"external_ref,omitempty"`
}

// UpdateFieldReq muda a marca secreto/visível e a posição de um campo.
type UpdateFieldReq struct {
	Sensitive *bool `json:"sensitive,omitempty"`
	Position  *int  `json:"position,omitempty"`
}

// CollectionReq cria ou muda uma coleção; o PATCH troca nome, descrição e prazo de uma vez (a CLI parte da coleção atual).
type CollectionReq struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	RotateAfterDays *int   `json:"rotate_after_days,omitempty"`
}

func fieldsPath(company string, user bool, secret string) string {
	return secretPath(company, user) + "/" + url.PathEscape(secret) + "/fields"
}

func collectionsPath(company string) string { return cp(company) + "/secret-collections" }

func collectionPath(company, collection string) string {
	return collectionsPath(company) + "/" + url.PathEscape(collection)
}

// CreateSecretField cria um campo.
func (c *Client) CreateSecretField(ctx context.Context, company string, user bool, secret string, req CreateFieldReq) (api.SecretField, error) {
	return get[api.SecretField](ctx, c, http.MethodPost, fieldsPath(company, user, secret), req)
}

// ListSecretFields lista os campos de um segredo.
func (c *Client) ListSecretFields(ctx context.Context, company string, user bool, secret string) ([]api.SecretField, error) {
	return list[api.SecretField](ctx, c, fieldsPath(company, user, secret), nil)
}

// UpdateSecretField muda a marca e a posição de um campo.
func (c *Client) UpdateSecretField(ctx context.Context, company string, user bool, secret, field string, req UpdateFieldReq) (api.SecretField, error) {
	return get[api.SecretField](ctx, c, http.MethodPatch, fieldsPath(company, user, secret)+"/"+url.PathEscape(field), req)
}

// RotateSecretField troca o valor de um campo.
func (c *Client) RotateSecretField(ctx context.Context, company string, user bool, secret, field string, req RotateFieldReq) (api.SecretField, error) {
	return get[api.SecretField](ctx, c, http.MethodPost, fieldsPath(company, user, secret)+"/"+url.PathEscape(field)+"/rotate", req)
}

// CreateSecretCollection cria uma coleção.
func (c *Client) CreateSecretCollection(ctx context.Context, company string, req CollectionReq) (api.SecretCollection, error) {
	return get[api.SecretCollection](ctx, c, http.MethodPost, collectionsPath(company), req)
}

// ListSecretCollections lista as coleções.
func (c *Client) ListSecretCollections(ctx context.Context, company string) ([]api.SecretCollection, error) {
	return list[api.SecretCollection](ctx, c, collectionsPath(company), nil)
}

// GetSecretCollection devolve uma coleção (id ou nome).
func (c *Client) GetSecretCollection(ctx context.Context, company, collection string) (api.SecretCollection, error) {
	return get[api.SecretCollection](ctx, c, http.MethodGet, collectionPath(company, collection), nil)
}

// UpdateSecretCollection muda nome, descrição e prazo de rotação.
func (c *Client) UpdateSecretCollection(ctx context.Context, company, collection string, req CollectionReq) (api.SecretCollection, error) {
	return get[api.SecretCollection](ctx, c, http.MethodPatch, collectionPath(company, collection), req)
}

// DeleteSecretCollection apaga uma coleção (a padrão o servidor recusa).
func (c *Client) DeleteSecretCollection(ctx context.Context, company, collection string) error {
	return c.do(ctx, http.MethodDelete, collectionPath(company, collection), nil, nil, nil)
}

// ListCollectionSecrets lista os segredos de uma coleção.
func (c *Client) ListCollectionSecrets(ctx context.Context, company, collection string) ([]api.Secret, error) {
	return list[api.Secret](ctx, c, collectionPath(company, collection)+"/secrets", nil)
}

// AddSecretToCollection põe um segredo na coleção.
func (c *Client) AddSecretToCollection(ctx context.Context, company, collection, secret string) error {
	return c.do(ctx, http.MethodPut, collectionPath(company, collection)+"/secrets/"+url.PathEscape(secret), nil, nil, nil)
}

// RemoveSecretFromCollection tira um segredo da coleção.
func (c *Client) RemoveSecretFromCollection(ctx context.Context, company, collection, secret string) error {
	return c.do(ctx, http.MethodDelete, collectionPath(company, collection)+"/secrets/"+url.PathEscape(secret), nil, nil, nil)
}
