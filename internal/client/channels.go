package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Canais externos (ADR-0046).

// CreateChannelReq cadastra um canal.
type CreateChannelReq struct {
	Kind             string            `json:"kind"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled,omitempty"`
	Persona          string            `json:"persona,omitempty"`
	Project          string            `json:"project,omitempty"`
	TokenRef         string            `json:"token_ref,omitempty"`
	WebhookSecretRef string            `json:"webhook_secret_ref,omitempty"`
	Config           api.ChannelConfig `json:"config"`
}

// UpdateChannelReq muda um canal.
type UpdateChannelReq struct {
	Name             *string            `json:"name,omitempty"`
	Enabled          *bool              `json:"enabled,omitempty"`
	Persona          string             `json:"persona,omitempty"`
	ClearPersona     bool               `json:"clear_persona,omitempty"`
	TokenRef         *string            `json:"token_ref,omitempty"`
	WebhookSecretRef *string            `json:"webhook_secret_ref,omitempty"`
	Config           *api.ChannelConfig `json:"config,omitempty"`
}

func chanPath(company, ref string) string { return cp(company) + "/channels/" + url.PathEscape(ref) }

// Channels lista os canais.
func (c *Client) Channels(ctx context.Context, company string) ([]api.Channel, error) {
	return list[api.Channel](ctx, c, cp(company)+"/channels", url.Values{"limit": {"500"}})
}

// Channel lê um canal por id ou nome.
func (c *Client) Channel(ctx context.Context, company, ref string) (api.Channel, error) {
	return get[api.Channel](ctx, c, http.MethodGet, chanPath(company, ref), nil)
}

// CreateChannel cadastra um canal.
func (c *Client) CreateChannel(ctx context.Context, company string, in CreateChannelReq) (api.Channel, error) {
	return get[api.Channel](ctx, c, http.MethodPost, cp(company)+"/channels", in)
}

// UpdateChannel muda um canal.
func (c *Client) UpdateChannel(ctx context.Context, company, ref string, in UpdateChannelReq) (api.Channel, error) {
	return get[api.Channel](ctx, c, http.MethodPatch, chanPath(company, ref), in)
}

// DeleteChannel apaga um canal.
func (c *Client) DeleteChannel(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodDelete, chanPath(company, ref), nil, nil, nil)
}

// ConnectChannel registra o webhook no provedor ou devolve a URL a configurar.
func (c *Client) ConnectChannel(ctx context.Context, company, ref string) (api.ChannelConnection, error) {
	return get[api.ChannelConnection](ctx, c, http.MethodPost, chanPath(company, ref)+"/connect", nil)
}

// LinkChannel gera o código de vínculo da pessoa autenticada.
func (c *Client) LinkChannel(ctx context.Context, company, ref string) (api.ChannelLink, error) {
	return get[api.ChannelLink](ctx, c, http.MethodPost, chanPath(company, ref)+"/link", nil)
}

// ChannelIdentities lista os vínculos do canal.
func (c *Client) ChannelIdentities(ctx context.Context, company, ref string) ([]api.ChannelIdentity, error) {
	return list[api.ChannelIdentity](ctx, c, chanPath(company, ref)+"/identities", nil)
}

// RevokeChannelIdentity revoga um vínculo.
func (c *Client) RevokeChannelIdentity(ctx context.Context, company, ref, identity string) error {
	return c.do(ctx, http.MethodDelete, chanPath(company, ref)+"/identities/"+url.PathEscape(identity), nil, nil, nil)
}

// ChannelMessages lista as mensagens recentes do canal.
func (c *Client) ChannelMessages(ctx context.Context, company, ref string, limit int) ([]api.ChannelMessage, error) {
	return list[api.ChannelMessage](ctx, c, chanPath(company, ref)+"/messages", url.Values{"limit": {strconv.Itoa(limit)}})
}

// SendChannelMessage envia uma mensagem avulsa.
func (c *Client) SendChannelMessage(ctx context.Context, company, ref, peer, text string, artifacts []string) (api.ChannelMessage, error) {
	body := map[string]any{"peer": peer, "text": text, "artifacts": artifacts}
	return get[api.ChannelMessage](ctx, c, http.MethodPost, chanPath(company, ref)+"/messages", body)
}

// ResendChannelMessage reenvia uma mensagem que falhou.
func (c *Client) ResendChannelMessage(ctx context.Context, company, ref, message string) (api.ChannelMessage, error) {
	return get[api.ChannelMessage](ctx, c, http.MethodPost, chanPath(company, ref)+"/messages/"+url.PathEscape(message)+"/resend", nil)
}

// PublishChannelReview publica uma review num pull request.
func (c *Client) PublishChannelReview(ctx context.Context, company, ref, peer, task, event, body string) (api.ChannelMessage, error) {
	in := map[string]string{"peer": peer, "task": task, "event": event, "body": body}
	return get[api.ChannelMessage](ctx, c, http.MethodPost, chanPath(company, ref)+"/reviews", in)
}
