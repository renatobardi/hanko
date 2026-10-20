package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Caixa por pessoa, avisos e atividade por ator (ADR-0042).

// Inbox devolve a Caixa na visão pedida (all, unread ou archived; vazio = all).
func (c *Client) Inbox(ctx context.Context, company, view string) (api.InboxList, error) {
	q := url.Values{}
	if view != "" {
		q.Set("view", view)
	}
	var out api.InboxList
	err := c.do(ctx, http.MethodGet, cp(company)+"/inbox", q, nil, &out)
	return out, err
}

// InboxState muda o estado de itens: action = read, unread, archive, unarchive ou read_all.
func (c *Client) InboxState(ctx context.Context, company, action string, items []api.InboxRef) (api.InboxStateResult, error) {
	body := map[string]any{"action": action}
	if len(items) > 0 {
		body["items"] = items
	}
	return get[api.InboxStateResult](ctx, c, http.MethodPost, cp(company)+"/inbox/state", body)
}

// NotifyPrefs lista as preferências de aviso da pessoa.
func (c *Client) NotifyPrefs(ctx context.Context, company string) ([]api.NotifyPref, error) {
	return list[api.NotifyPref](ctx, c, cp(company)+"/notifications", nil)
}

// SetNotifyPref define os canais de um tipo de aviso.
func (c *Client) SetNotifyPref(ctx context.Context, company, kind string, toast, push bool) (api.NotifyPref, error) {
	return get[api.NotifyPref](ctx, c, http.MethodPut, cp(company)+"/notifications/"+url.PathEscape(kind), map[string]bool{"toast": toast, "push": push})
}

// TestNotification manda um aviso de teste à própria pessoa.
func (c *Client) TestNotification(ctx context.Context, company string) error {
	return c.do(ctx, http.MethodPost, cp(company)+"/notifications/test", nil, nil, nil)
}

// ActivityQuery são os filtros de Activity.
type ActivityQuery struct {
	SubjectType string
	// Actor: system, persona:<slug>, human:<e-mail> (ou persona/human, para todos do tipo).
	Actor string
	Limit int
}

// Activity lista a atividade da empresa, mais nova primeiro.
func (c *Client) Activity(ctx context.Context, company string, f ActivityQuery) ([]api.Activity, error) {
	q := url.Values{}
	if f.SubjectType != "" {
		q.Set("subject_type", f.SubjectType)
	}
	if f.Actor != "" {
		q.Set("actor", f.Actor)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return list[api.Activity](ctx, c, cp(company)+"/activity", q)
}
