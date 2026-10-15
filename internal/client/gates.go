package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// RequestGateReq abre um gate.
type RequestGateReq struct {
	Kind    string `json:"kind,omitempty"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Task    string `json:"task,omitempty"`
	EventID string `json:"event_id,omitempty"`
}

// DecideGateReq é a decisão humana sobre um gate pendente.
type DecideGateReq struct {
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
}

// GateQuery são os filtros de ListGates.
type GateQuery struct {
	Status, Kind, Task, Persona string
	Limit, Offset               int
}

func (g GateQuery) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"status": g.Status, "kind": g.Kind, "task": g.Task, "persona": g.Persona} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if g.Limit > 0 {
		q.Set("limit", strconv.Itoa(g.Limit))
	}
	if g.Offset > 0 {
		q.Set("offset", strconv.Itoa(g.Offset))
	}
	return q
}

func (c *Client) RequestGate(ctx context.Context, company string, in RequestGateReq) (api.Gate, error) {
	return get[api.Gate](ctx, c, http.MethodPost, cp(company)+"/gates", in)
}

func (c *Client) Gates(ctx context.Context, company string, q GateQuery) ([]api.Gate, error) {
	return list[api.Gate](ctx, c, cp(company)+"/gates", q.values())
}

func (c *Client) Gate(ctx context.Context, company, id string) (api.Gate, error) {
	return get[api.Gate](ctx, c, http.MethodGet, cp(company)+"/gates/"+url.PathEscape(id), nil)
}

func (c *Client) DecideGate(ctx context.Context, company, id string, in DecideGateReq) (api.Gate, error) {
	return get[api.Gate](ctx, c, http.MethodPost, cp(company)+"/gates/"+url.PathEscape(id)+"/decide", in)
}

func (c *Client) OrgChart(ctx context.Context, company string) (api.OrgChart, error) {
	return get[api.OrgChart](ctx, c, http.MethodGet, cp(company)+"/org-chart", nil)
}
