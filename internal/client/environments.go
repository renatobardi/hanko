package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- ambientes de execução (ADR-0037) ----------

// EnvironmentReq cadastra um ambiente (ou testa um rascunho).
type EnvironmentReq struct {
	Name        string             `json:"name"`
	Slug        string             `json:"slug,omitempty"`
	Description string             `json:"description,omitempty"`
	Driver      string             `json:"driver"`
	SSH         *api.SSHConfig     `json:"ssh,omitempty"`
	PrivateKey  string             `json:"private_key,omitempty"`
	Sandbox     *api.SandboxConfig `json:"sandbox,omitempty"`
	Env         map[string]string  `json:"env,omitempty"`
}

// UpdateEnvironmentReq altera um ambiente; nil fica como está.
type UpdateEnvironmentReq struct {
	Name        *string            `json:"name,omitempty"`
	Description *string            `json:"description,omitempty"`
	SSH         *api.SSHConfig     `json:"ssh,omitempty"`
	PrivateKey  *string            `json:"private_key,omitempty"`
	Sandbox     *api.SandboxConfig `json:"sandbox,omitempty"`
	SetEnv      map[string]string  `json:"set_env,omitempty"`
	DropEnv     []string           `json:"drop_env,omitempty"`
	Reactivate  bool               `json:"reactivate,omitempty"`
}

func envPath(company, ref string) string {
	return cp(company) + "/environments/" + url.PathEscape(ref)
}

func (c *Client) Environments(ctx context.Context, company string) ([]api.Environment, error) {
	return list[api.Environment](ctx, c, cp(company)+"/environments", url.Values{"limit": {"500"}})
}

func (c *Client) Environment(ctx context.Context, company, ref string) (api.Environment, error) {
	return get[api.Environment](ctx, c, http.MethodGet, envPath(company, ref), nil)
}

func (c *Client) CreateEnvironment(ctx context.Context, company string, in EnvironmentReq) (api.Environment, error) {
	return get[api.Environment](ctx, c, http.MethodPost, cp(company)+"/environments", in)
}

func (c *Client) UpdateEnvironment(ctx context.Context, company, ref string, in UpdateEnvironmentReq) (api.Environment, error) {
	return get[api.Environment](ctx, c, http.MethodPatch, envPath(company, ref), in)
}

// ArchiveEnvironment arquiva o ambiente e move as personas dele para moveTo ("" ou local = o padrão).
func (c *Client) ArchiveEnvironment(ctx context.Context, company, ref, moveTo string) (api.Environment, error) {
	var out api.Environment
	q := url.Values{}
	if moveTo != "" {
		q.Set("move_personas_to", moveTo)
	}
	err := c.do(ctx, http.MethodDelete, envPath(company, ref), q, nil, &out)
	return out, err
}

func (c *Client) ProbeEnvironment(ctx context.Context, company, ref string) (api.Environment, error) {
	return get[api.Environment](ctx, c, http.MethodPost, envPath(company, ref)+"/probe", nil)
}

func (c *Client) ProbeEnvironmentDraft(ctx context.Context, company string, in EnvironmentReq) (api.ProbeReport, error) {
	return get[api.ProbeReport](ctx, c, http.MethodPost, cp(company)+"/environments/probe", in)
}

// SetDefaultEnvironment escolhe o padrão da empresa (ref "local" volta ao local).
func (c *Client) SetDefaultEnvironment(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodPut, cp(company)+"/environments/default", nil, map[string]string{"environment": ref}, nil)
}

func (c *Client) EnvironmentLeases(ctx context.Context, company, ref string, limit int) ([]api.EnvironmentLease, error) {
	return list[api.EnvironmentLease](ctx, c, envPath(company, ref)+"/leases", url.Values{"limit": {strconv.Itoa(limit)}})
}
