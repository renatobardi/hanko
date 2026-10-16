package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// Sala (ADR-0033): conversa com várias personas, pelas rotas de conversas.

// CreateRoomReq abre uma sala (Message é a primeira mensagem).
type CreateRoomReq struct {
	Kind              string   `json:"kind"`
	Title             string   `json:"title,omitempty"`
	Message           string   `json:"message,omitempty"`
	Personas          []string `json:"personas"`
	Moderator         string   `json:"moderator,omitempty"`
	Mode              string   `json:"mode,omitempty"`
	MaxHops           int      `json:"max_hops,omitempty"`
	MaxRunsPerMessage int      `json:"max_runs_per_message,omitempty"`
}

// RoomPatch muda a configuração de uma sala; campos nil ficam como estão.
type RoomPatch struct {
	Mode              *string `json:"mode,omitempty"`
	Moderator         *string `json:"moderator,omitempty"`
	MaxHops           *int    `json:"max_hops,omitempty"`
	MaxRunsPerMessage *int    `json:"max_runs_per_message,omitempty"`
}

// CreateRoom abre uma sala com as personas (ids ou slugs).
func (c *Client) CreateRoom(ctx context.Context, company string, in CreateRoomReq) (api.Conversation, error) {
	in.Kind = "room"
	return get[api.Conversation](ctx, c, http.MethodPost, cp(company)+"/conversations", in)
}

// AddRoomParticipant põe uma persona (id ou slug) na sala.
func (c *Client) AddRoomParticipant(ctx context.Context, company, id, persona string) (api.Conversation, error) {
	return get[api.Conversation](ctx, c, http.MethodPost, cp(company)+"/conversations/"+url.PathEscape(id)+"/participants",
		struct {
			Persona string `json:"persona"`
		}{persona})
}

// RemoveRoomParticipant tira uma persona (id ou slug) da sala.
func (c *Client) RemoveRoomParticipant(ctx context.Context, company, id, persona string) (api.Conversation, error) {
	return get[api.Conversation](ctx, c, http.MethodDelete, cp(company)+"/conversations/"+url.PathEscape(id)+"/participants/"+url.PathEscape(persona), nil)
}

// UpdateRoom muda o modo, a moderadora ou os limites da sala.
func (c *Client) UpdateRoom(ctx context.Context, company, id string, in RoomPatch) (api.Conversation, error) {
	return get[api.Conversation](ctx, c, http.MethodPatch, cp(company)+"/conversations/"+url.PathEscape(id)+"/room", in)
}
