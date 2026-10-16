package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// CreateConversationReq abre uma conversa com uma persona (Message é a primeira mensagem).
type CreateConversationReq struct {
	Persona string `json:"persona"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
}

// ConversationQuery são os filtros de Conversations.
type ConversationQuery struct {
	Persona, Status string
	Kind            string // direct | room (ADR-0033); vazio = os dois
	Limit, Offset   int
}

func (c ConversationQuery) values() url.Values {
	q := url.Values{}
	if c.Persona != "" {
		q.Set("persona", c.Persona)
	}
	if c.Status != "" {
		q.Set("status", c.Status)
	}
	if c.Kind != "" {
		q.Set("kind", c.Kind)
	}
	if c.Limit > 0 {
		q.Set("limit", strconv.Itoa(c.Limit))
	}
	if c.Offset > 0 {
		q.Set("offset", strconv.Itoa(c.Offset))
	}
	return q
}

func (c *Client) CreateConversation(ctx context.Context, company string, in CreateConversationReq) (api.Conversation, error) {
	return get[api.Conversation](ctx, c, http.MethodPost, cp(company)+"/conversations", in)
}

func (c *Client) Conversations(ctx context.Context, company string, q ConversationQuery) ([]api.Conversation, error) {
	return list[api.Conversation](ctx, c, cp(company)+"/conversations", q.values())
}

func (c *Client) Conversation(ctx context.Context, company, id string) (api.Conversation, error) {
	return get[api.Conversation](ctx, c, http.MethodGet, cp(company)+"/conversations/"+url.PathEscape(id), nil)
}

// ConversationMessages devolve as últimas limit mensagens (0 = o padrão do servidor), em ordem cronológica.
func (c *Client) ConversationMessages(ctx context.Context, company, id string, limit int) ([]api.ConversationMessage, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.ConversationMessage](ctx, c, cp(company)+"/conversations/"+url.PathEscape(id)+"/messages", q)
}

// SendMessage envia uma mensagem humana; a persona responde por uma execução.
func (c *Client) SendMessage(ctx context.Context, company, id, message string) (api.ConversationMessage, error) {
	return get[api.ConversationMessage](ctx, c, http.MethodPost, cp(company)+"/conversations/"+url.PathEscape(id)+"/messages",
		struct {
			Message string `json:"message"`
		}{message})
}
