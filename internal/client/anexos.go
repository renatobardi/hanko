package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- anexos e rascunhos (ADR-0052) ----------

// PutAttachmentReq anexa um arquivo a uma task (e a um comentário dela) ou a uma conversa do chat (e a uma mensagem).
type PutAttachmentReq struct {
	Name         string `json:"name"`
	Mime         string `json:"mime,omitempty"`
	Content      []byte `json:"content"`
	Task         string `json:"task,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	CommentID    string `json:"comment_id,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
}

// AttachmentQuery filtra a lista de anexos.
type AttachmentQuery struct {
	Task, Conversation, CommentID, MessageID string
	Limit                                    int
}

func (a AttachmentQuery) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"task": a.Task, "conversation": a.Conversation, "comment_id": a.CommentID, "message_id": a.MessageID} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if a.Limit > 0 {
		q.Set("limit", strconv.Itoa(a.Limit))
	}
	return q
}

func (c *Client) PutAttachment(ctx context.Context, company string, in PutAttachmentReq) (api.Artifact, error) {
	var out api.Artifact
	err := c.doWith(ctx, c.longHTTP(), http.MethodPost, cp(company)+"/attachments", nil, in, &out)
	return out, err
}

func (c *Client) Attachments(ctx context.Context, company string, q AttachmentQuery) ([]api.Artifact, error) {
	return list[api.Artifact](ctx, c, cp(company)+"/attachments", q.values())
}

// Draft lê o seu rascunho; target é "task" ou "conversation".
func (c *Client) Draft(ctx context.Context, company, target, id string) (api.Draft, error) {
	return get[api.Draft](ctx, c, http.MethodGet, cp(company)+"/drafts/"+target+"/"+url.PathEscape(id), nil)
}

// SaveDraft grava o seu rascunho (texto em branco apaga).
func (c *Client) SaveDraft(ctx context.Context, company, target, id, body string) (api.Draft, error) {
	return get[api.Draft](ctx, c, http.MethodPut, cp(company)+"/drafts/"+target+"/"+url.PathEscape(id), map[string]string{"body": body})
}

func (c *Client) DeleteDraft(ctx context.Context, company, target, id string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/drafts/"+target+"/"+url.PathEscape(id), nil, nil, nil)
}
