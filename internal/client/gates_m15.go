package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Gates estendidos (ADR-0041).

// AnswerGateReq é a resposta a um gate de pergunta.
type AnswerGateReq struct {
	Answers map[string][]string `json:"answers"`
	Note    string              `json:"note,omitempty"`
}

// ResubmitGateReq é o texto novo de um gate devolvido com mudanças pedidas.
type ResubmitGateReq struct {
	Title    string            `json:"title,omitempty"`
	Body     string            `json:"body,omitempty"`
	Artifact string            `json:"artifact,omitempty"`
	Hire     *api.GateHireSpec `json:"hire,omitempty"`
	Note     string            `json:"note,omitempty"`
}

// SetPlanPolicyReq liga, desliga ou limpa (Require nulo) a política de plano de uma camada.
type SetPlanPolicyReq struct {
	Scope   string `json:"scope"`
	Target  string `json:"target"`
	Require *bool  `json:"require,omitempty"`
}

// ResolveIncidentReq é a decisão sobre um incidente de orçamento.
type ResolveIncidentReq struct {
	Resolution    string `json:"resolution"`
	BudgetCents   *int64 `json:"budget_cents,omitempty"`
	RemoveCeiling bool   `json:"remove_ceiling,omitempty"`
	Note          string `json:"note,omitempty"`
}

func gatePath(company, id string) string { return cp(company) + "/gates/" + url.PathEscape(id) }

func (c *Client) AnswerGate(ctx context.Context, company, id string, in AnswerGateReq) (api.Gate, error) {
	return get[api.Gate](ctx, c, http.MethodPost, gatePath(company, id)+"/answer", in)
}

func (c *Client) DismissGate(ctx context.Context, company, id, note string) (api.Gate, error) {
	return get[api.Gate](ctx, c, http.MethodPost, gatePath(company, id)+"/dismiss", map[string]string{"note": note})
}

func (c *Client) ReopenGate(ctx context.Context, company, id string) (api.Gate, error) {
	return get[api.Gate](ctx, c, http.MethodPost, gatePath(company, id)+"/reopen", struct{}{})
}

func (c *Client) ResubmitGate(ctx context.Context, company, id string, in ResubmitGateReq) (api.Gate, error) {
	return get[api.Gate](ctx, c, http.MethodPost, gatePath(company, id)+"/resubmit", in)
}

func (c *Client) GateRevisions(ctx context.Context, company, id string) (api.GateHistoryOut, error) {
	return get[api.GateHistoryOut](ctx, c, http.MethodGet, gatePath(company, id)+"/revisions", nil)
}

func (c *Client) TaskPlanPolicy(ctx context.Context, company, task string) (api.PlanPolicyOut, error) {
	return get[api.PlanPolicyOut](ctx, c, http.MethodGet, cp(company)+"/tasks/"+url.PathEscape(task)+"/plan-policy", nil)
}

func (c *Client) SetPlanPolicy(ctx context.Context, company string, in SetPlanPolicyReq) error {
	return c.do(ctx, http.MethodPut, cp(company)+"/plan-policy", nil, in, nil)
}

func (c *Client) CompanyPolicies(ctx context.Context, company string) (api.CompanyPolicies, error) {
	return get[api.CompanyPolicies](ctx, c, http.MethodGet, cp(company)+"/policies", nil)
}

func (c *Client) SetCompanyPolicies(ctx context.Context, company string, requireHireGate bool) (api.CompanyPolicies, error) {
	return get[api.CompanyPolicies](ctx, c, http.MethodPut, cp(company)+"/policies", map[string]bool{"require_hire_gate": requireHireGate})
}

func (c *Client) PrimaryPersona(ctx context.Context, company string) (api.Persona, error) {
	return get[api.Persona](ctx, c, http.MethodGet, cp(company)+"/primary-persona", nil)
}

func (c *Client) SetPrimaryPersona(ctx context.Context, company, persona string) (api.Persona, error) {
	return get[api.Persona](ctx, c, http.MethodPut, cp(company)+"/primary-persona", map[string]string{"persona": persona})
}

func (c *Client) SetPersonaSystem(ctx context.Context, company, persona string, system bool) (api.Persona, error) {
	return get[api.Persona](ctx, c, http.MethodPut, cp(company)+"/personas/"+url.PathEscape(persona)+"/system", map[string]bool{"system": system})
}

func (c *Client) BudgetIncidents(ctx context.Context, company string, onlyOpen bool) ([]api.BudgetIncidentOut, error) {
	q := url.Values{"limit": {strconv.Itoa(500)}}
	if onlyOpen {
		q.Set("open", "true")
	}
	return list[api.BudgetIncidentOut](ctx, c, cp(company)+"/budget-incidents", q)
}

func (c *Client) ResolveBudgetIncident(ctx context.Context, company, id string, in ResolveIncidentReq) (api.BudgetIncidentOut, error) {
	return get[api.BudgetIncidentOut](ctx, c, http.MethodPost, cp(company)+"/budget-incidents/"+url.PathEscape(id)+"/resolve", in)
}
