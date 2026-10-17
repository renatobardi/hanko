package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Workspace de execução com Git, padrões de projeto e "Nova sessão" (ADR-0054).

type EnableWorkspaceGitReq struct {
	CredentialRef string `json:"credential_ref,omitempty"`
	Sync          bool   `json:"sync,omitempty"`
}

type ProjectDefaultsReq struct {
	Instructions     *string `json:"instructions,omitempty"`
	Goal             string  `json:"goal,omitempty"`
	ClearGoal        bool    `json:"clear_goal,omitempty"`
	ImportRepoSkills *bool   `json:"import_repo_skills,omitempty"`
	SkillsPath       *string `json:"skills_path,omitempty"`
}

func wsGit(company, ref string) string {
	return cp(company) + "/workspaces/" + url.PathEscape(ref) + "/git"
}

func taskGit(company, ref string) string {
	return cp(company) + "/tasks/" + url.PathEscape(ref) + "/git"
}

func (c *Client) WorkspaceGit(ctx context.Context, company, ref string) (api.WorkspaceGit, error) {
	return get[api.WorkspaceGit](ctx, c, http.MethodGet, wsGit(company, ref), nil)
}

func (c *Client) EnableWorkspaceGit(ctx context.Context, company, ref string, in EnableWorkspaceGitReq) (api.WorkspaceGit, error) {
	return get[api.WorkspaceGit](ctx, c, http.MethodPut, wsGit(company, ref), in)
}

func (c *Client) DisableWorkspaceGit(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodDelete, wsGit(company, ref), nil, nil, nil)
}

func (c *Client) SyncWorkspaceGit(ctx context.Context, company, ref string) (api.WorkspaceGit, error) {
	return get[api.WorkspaceGit](ctx, c, http.MethodPost, wsGit(company, ref)+"/sync", nil)
}

func (c *Client) ProjectWorktrees(ctx context.Context, company, ref string) ([]api.Worktree, error) {
	return list[api.Worktree](ctx, c, cp(company)+"/projects/"+url.PathEscape(ref)+"/git", nil)
}

func (c *Client) TaskGit(ctx context.Context, company, ref string) (api.TaskGit, error) {
	return get[api.TaskGit](ctx, c, http.MethodGet, taskGit(company, ref), nil)
}

func (c *Client) TaskGitDiff(ctx context.Context, company, ref string) (api.GitPatch, error) {
	return get[api.GitPatch](ctx, c, http.MethodGet, taskGit(company, ref)+"/diff", nil)
}

func (c *Client) TaskGitLog(ctx context.Context, company, ref string, limit int) ([]api.GitCommit, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.GitCommit](ctx, c, taskGit(company, ref)+"/log", q)
}

func (c *Client) TaskGitTree(ctx context.Context, company, ref, path string) (api.GitTree, error) {
	var out api.GitTree
	err := c.do(ctx, http.MethodGet, taskGit(company, ref)+"/tree", url.Values{"path": {path}}, nil, &out)
	return out, err
}

func (c *Client) TaskGitFile(ctx context.Context, company, ref, path string) (api.GitFile, error) {
	var out api.GitFile
	err := c.do(ctx, http.MethodGet, taskGit(company, ref)+"/file", url.Values{"path": {path}}, nil, &out)
	return out, err
}

func (c *Client) RemoveTaskWorktree(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodDelete, taskGit(company, ref)+"/worktree", nil, nil, nil)
}

func (c *Client) TaskNewSession(ctx context.Context, company, ref, reason string) (api.TaskSessionReset, error) {
	return get[api.TaskSessionReset](ctx, c, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(ref)+"/new-session", map[string]string{"reason": reason})
}

func (c *Client) ProjectDefaults(ctx context.Context, company, ref string) (api.ProjectDefaults, error) {
	return get[api.ProjectDefaults](ctx, c, http.MethodGet, cp(company)+"/projects/"+url.PathEscape(ref)+"/defaults", nil)
}

func (c *Client) SetProjectDefaults(ctx context.Context, company, ref string, in ProjectDefaultsReq) (api.ProjectDefaults, error) {
	return get[api.ProjectDefaults](ctx, c, http.MethodPatch, cp(company)+"/projects/"+url.PathEscape(ref)+"/defaults", in)
}
