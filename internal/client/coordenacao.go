package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// Coordenação de tasks (ADR-0040).

// ReviewOverrideReq é a sobreposição de revisão enviada para task ou projeto.
type ReviewOverrideReq struct {
	RequireReview   *bool  `json:"require_review,omitempty"`
	ReviewerMode    string `json:"reviewer_mode,omitempty"`
	ReviewerPersona string `json:"reviewer_persona,omitempty"`
	ReviewerAgent   string `json:"reviewer_agent,omitempty"`
	ReviewerModel   string `json:"reviewer_model,omitempty"`
}

func (c *Client) TaskRelations(ctx context.Context, company, ref string) (api.TaskRelations, error) {
	return get[api.TaskRelations](ctx, c, http.MethodGet, cp(company)+"/tasks/"+url.PathEscape(ref)+"/relations", nil)
}

func (c *Client) AddTaskDependency(ctx context.Context, company, ref, dependsOn string) (api.Task, error) {
	return get[api.Task](ctx, c, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(ref)+"/dependencies",
		map[string]string{"depends_on": dependsOn})
}

func (c *Client) RemoveTaskDependency(ctx context.Context, company, ref, dependsOn string) (api.Task, error) {
	return get[api.Task](ctx, c, http.MethodDelete, cp(company)+"/tasks/"+url.PathEscape(ref)+"/dependencies/"+url.PathEscape(dependsOn), nil)
}

func (c *Client) HoldTask(ctx context.Context, company, ref, reason string) (api.Task, error) {
	return get[api.Task](ctx, c, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(ref)+"/hold", map[string]string{"reason": reason})
}

func (c *Client) ResumeTask(ctx context.Context, company, ref string) (api.Task, error) {
	return get[api.Task](ctx, c, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(ref)+"/resume", nil)
}

func (c *Client) HeldTasks(ctx context.Context, company string) ([]api.Task, error) {
	return list[api.Task](ctx, c, cp(company)+"/held-tasks", nil)
}

func (c *Client) TaskReviewPolicy(ctx context.Context, company, ref string) (api.TaskReviewPolicy, error) {
	return get[api.TaskReviewPolicy](ctx, c, http.MethodGet, cp(company)+"/tasks/"+url.PathEscape(ref)+"/review-policy", nil)
}

func (c *Client) SetTaskReviewPolicy(ctx context.Context, company, ref string, in ReviewOverrideReq) (api.TaskReviewPolicy, error) {
	return get[api.TaskReviewPolicy](ctx, c, http.MethodPut, cp(company)+"/tasks/"+url.PathEscape(ref)+"/review-policy", in)
}

func (c *Client) ProjectReviewPolicy(ctx context.Context, company, ref string) (api.ReviewOverride, error) {
	return get[api.ReviewOverride](ctx, c, http.MethodGet, cp(company)+"/projects/"+url.PathEscape(ref)+"/review-policy", nil)
}

func (c *Client) SetProjectReviewPolicy(ctx context.Context, company, ref string, in ReviewOverrideReq) (api.ReviewOverride, error) {
	return get[api.ReviewOverride](ctx, c, http.MethodPut, cp(company)+"/projects/"+url.PathEscape(ref)+"/review-policy", in)
}
