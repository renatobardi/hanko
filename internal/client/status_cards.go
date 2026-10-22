package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// Cards de status (ADR-0032).

// CreateStatusCardReq cria um card.
type CreateStatusCardReq struct {
	Interest    string                  `json:"interest"`
	Title       string                  `json:"title,omitempty"`
	TitlePinned bool                    `json:"title_pinned,omitempty"`
	Summarizer  string                  `json:"summarizer,omitempty"`
	Policy      *api.StatusCardPolicy   `json:"refresh_policy,omitempty"`
	Engine      string                  `json:"engine,omitempty"`
	Queries     []api.StatusCardQueryIn `json:"queries,omitempty"`
	OnDashboard bool                    `json:"on_dashboard,omitempty"`
}

// UpdateStatusCardReq altera um card; nil fica como está.
type UpdateStatusCardReq struct {
	Interest    *string                  `json:"interest,omitempty"`
	Title       *string                  `json:"title,omitempty"`
	TitlePinned *bool                    `json:"title_pinned,omitempty"`
	Summarizer  string                   `json:"summarizer,omitempty"`
	Policy      *api.StatusCardPolicy    `json:"refresh_policy,omitempty"`
	Archived    *bool                    `json:"archived,omitempty"`
	Queries     *[]api.StatusCardQueryIn `json:"queries,omitempty"`
	OnDashboard *bool                    `json:"on_dashboard,omitempty"`
}

func scPath(company, id string) string { return cp(company) + "/status-cards/" + url.PathEscape(id) }

// StatusCards lista os cards (archived: "false", "true" ou "all").
func (c *Client) StatusCards(ctx context.Context, company, archived string) ([]api.StatusCard, error) {
	q := url.Values{"limit": {"500"}}
	if archived != "" {
		q.Set("archived", archived)
	}
	return list[api.StatusCard](ctx, c, cp(company)+"/status-cards", q)
}

func (c *Client) StatusCard(ctx context.Context, company, id string) (api.StatusCard, error) {
	return get[api.StatusCard](ctx, c, http.MethodGet, scPath(company, id), nil)
}

func (c *Client) CreateStatusCard(ctx context.Context, company string, in CreateStatusCardReq) (api.StatusCard, error) {
	return get[api.StatusCard](ctx, c, http.MethodPost, cp(company)+"/status-cards", in)
}

func (c *Client) UpdateStatusCard(ctx context.Context, company, id string, in UpdateStatusCardReq) (api.StatusCard, error) {
	return get[api.StatusCard](ctx, c, http.MethodPatch, scPath(company, id), in)
}

func (c *Client) DeleteStatusCard(ctx context.Context, company, id string) error {
	return c.do(ctx, http.MethodDelete, scPath(company, id), nil, nil, nil)
}

func (c *Client) RefreshStatusCard(ctx context.Context, company, id string, full bool) (api.StatusCard, error) {
	return get[api.StatusCard](ctx, c, http.MethodPost, scPath(company, id)+"/refresh", map[string]bool{"full": full})
}

func (c *Client) RecompileStatusCard(ctx context.Context, company, id string) (api.StatusCard, error) {
	return get[api.StatusCard](ctx, c, http.MethodPost, scPath(company, id)+"/recompile", nil)
}

func (c *Client) StatusCardUpdates(ctx context.Context, company, id string) ([]api.StatusCardUpdate, error) {
	return list[api.StatusCardUpdate](ctx, c, scPath(company, id)+"/updates", url.Values{"limit": {"200"}})
}

func (c *Client) StatusCardPreview(ctx context.Context, company, id string) (api.StatusCardPreview, error) {
	return get[api.StatusCardPreview](ctx, c, http.MethodGet, scPath(company, id)+"/preview", nil)
}
