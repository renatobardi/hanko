package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Pipelines sobre tasks (ADR-0048).

func stagesPath(company, project string) string {
	return cp(company) + "/projects/" + url.PathEscape(project) + "/stages"
}

func taskStagePath(company, task string) string {
	return cp(company) + "/tasks/" + url.PathEscape(task) + "/stage"
}

// ProjectStages devolve o pipeline do projeto.
func (c *Client) ProjectStages(ctx context.Context, company, project string) (api.ProjectStages, error) {
	return get[api.ProjectStages](ctx, c, http.MethodGet, stagesPath(company, project), nil)
}

// SetProjectStages troca o pipeline do projeto pela lista informada.
func (c *Client) SetProjectStages(ctx context.Context, company, project string, stages []api.ProjectStageSpec) (api.ProjectStages, error) {
	return get[api.ProjectStages](ctx, c, http.MethodPut, stagesPath(company, project), map[string]any{"stages": stages})
}

// ClearProjectStages desliga o pipeline do projeto.
func (c *Client) ClearProjectStages(ctx context.Context, company, project string) error {
	return c.do(ctx, http.MethodDelete, stagesPath(company, project), nil, nil, nil)
}

// StageLearnings lista os aprendizados das etapas de um projeto (stage vazio = todas).
func (c *Client) StageLearnings(ctx context.Context, company, project, stage string, limit int) ([]api.StageLearning, error) {
	q := url.Values{}
	if stage != "" {
		q.Set("stage", stage)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.StageLearning](ctx, c, cp(company)+"/projects/"+url.PathEscape(project)+"/stage-learnings", q)
}

// TaskStage devolve a posição da task no pipeline.
func (c *Client) TaskStage(ctx context.Context, company, task string) (api.TaskStage, error) {
	return get[api.TaskStage](ctx, c, http.MethodGet, taskStagePath(company, task), nil)
}

// StartTaskStage põe a task no pipeline do projeto dela.
func (c *Client) StartTaskStage(ctx context.Context, company, task, stage string) (api.TaskStage, error) {
	return get[api.TaskStage](ctx, c, http.MethodPost, taskStagePath(company, task)+"/start", map[string]string{"stage": stage})
}

// CompleteTaskStage conclui a etapa da task.
func (c *Client) CompleteTaskStage(ctx context.Context, company, task, note string) (api.TaskStage, error) {
	return get[api.TaskStage](ctx, c, http.MethodPost, taskStagePath(company, task)+"/complete", map[string]string{"note": note})
}

// AdvanceTaskStage move a task à mão (to vazio = a etapa seguinte).
func (c *Client) AdvanceTaskStage(ctx context.Context, company, task, to, note string) (api.TaskStage, error) {
	return get[api.TaskStage](ctx, c, http.MethodPost, taskStagePath(company, task)+"/advance", map[string]string{"to": to, "note": note})
}

// LeaveTaskStage tira a task do pipeline.
func (c *Client) LeaveTaskStage(ctx context.Context, company, task, note string) error {
	return c.do(ctx, http.MethodPost, taskStagePath(company, task)+"/leave", nil, map[string]string{"note": note}, nil)
}

// DashboardStatusCards lista os cards de status do Painel.
func (c *Client) DashboardStatusCards(ctx context.Context, company string) ([]api.DashboardStatusCard, error) {
	return list[api.DashboardStatusCard](ctx, c, cp(company)+"/dashboard/status-cards", nil)
}
