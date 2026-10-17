package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// RunAction decide uma execução que parou: action é retry, continue ou close (ADR-0039).
func (c *Client) RunAction(ctx context.Context, company, id, action, note string) (api.Run, error) {
	return get[api.Run](ctx, c, http.MethodPost, cp(company)+"/runs/"+url.PathEscape(id)+"/actions",
		struct {
			Action string `json:"action"`
			Note   string `json:"note,omitempty"`
		}{action, note})
}

// SteerRun entrega uma mensagem do operador a uma execução ativa.
func (c *Client) SteerRun(ctx context.Context, company, id, message string) (api.SteerResult, error) {
	return get[api.SteerResult](ctx, c, http.MethodPost, cp(company)+"/runs/"+url.PathEscape(id)+"/steer",
		struct {
			Message string `json:"message"`
		}{message})
}

// TaskFollowups lista a fila de follow-ups de uma task.
func (c *Client) TaskFollowups(ctx context.Context, company, task string) ([]api.Followup, error) {
	return list[api.Followup](ctx, c, cp(company)+"/tasks/"+url.PathEscape(task)+"/followups", nil)
}

// ConversationFollowups lista as mensagens de uma conversa que esperam a resposta da persona.
func (c *Client) ConversationFollowups(ctx context.Context, company, conversation string) ([]api.Followup, error) {
	return list[api.Followup](ctx, c, cp(company)+"/conversations/"+url.PathEscape(conversation)+"/followups", nil)
}

// QueueTaskFollowup enfileira uma instrução para a task.
func (c *Client) QueueTaskFollowup(ctx context.Context, company, task, message string) (api.Followup, error) {
	return get[api.Followup](ctx, c, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(task)+"/followups",
		struct {
			Message string `json:"message"`
		}{message})
}

// EditFollowup troca o texto de um follow-up que ainda não foi entregue.
func (c *Client) EditFollowup(ctx context.Context, company, id, message string) (api.Followup, error) {
	return get[api.Followup](ctx, c, http.MethodPatch, cp(company)+"/followups/"+url.PathEscape(id),
		struct {
			Message string `json:"message"`
		}{message})
}

// DiscardFollowup descarta um follow-up que ainda não foi entregue.
func (c *Client) DiscardFollowup(ctx context.Context, company, id string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/followups/"+url.PathEscape(id), nil, nil, nil)
}
