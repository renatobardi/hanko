package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/service"
)

// ---------- versões, fontes e estúdio de skills (ADR-0035) ----------

func skillPath(company, skill string) string { return cp(company) + "/skills/" + url.PathEscape(skill) }

func (c *Client) SkillVersions(ctx context.Context, company, skill string) ([]api.SkillVersion, error) {
	return list[api.SkillVersion](ctx, c, skillPath(company, skill)+"/versions", nil)
}

// SkillVersion devolve uma versão; against > 0 inclui o diff daquela versão para esta.
func (c *Client) SkillVersion(ctx context.Context, company, skill string, revision, against int) (api.SkillVersion, error) {
	var out api.SkillVersion
	q := url.Values{}
	if against > 0 {
		q.Set("against", strconv.Itoa(against))
	}
	err := c.do(ctx, http.MethodGet, skillPath(company, skill)+"/versions/"+strconv.Itoa(revision), q, nil, &out)
	return out, err
}

func (c *Client) RestoreSkill(ctx context.Context, company, skill string, revision int) (api.Skill, error) {
	return get[api.Skill](ctx, c, http.MethodPost, skillPath(company, skill)+"/restore", map[string]int{"revision": revision})
}

type pinReq struct {
	Enabled        bool `json:"enabled"`
	PinnedRevision int  `json:"pinned_revision"`
}

// PinPersonaSkill fixa (revision ≥ 1) ou solta (0) a versão da skill para a persona, mantendo o liga/desliga.
func (c *Client) PinPersonaSkill(ctx context.Context, company, persona, skill string, enabled bool, revision int) error {
	return c.do(ctx, http.MethodPut, cp(company)+"/personas/"+url.PathEscape(persona)+"/skills/"+url.PathEscape(skill), nil,
		pinReq{Enabled: enabled, PinnedRevision: revision}, nil)
}

type SkillSourceReq struct {
	URL        string   `json:"url"`
	Ref        string   `json:"ref,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Excluded   []string `json:"excluded_paths,omitempty"`
	AutoUpdate *bool    `json:"auto_update,omitempty"`
	ImportNew  bool     `json:"import_new,omitempty"`
	Select     []string `json:"select,omitempty"`
}

func (c *Client) DiscoverSkillSource(ctx context.Context, company string, in SkillSourceReq) (service.SkillSourceDiscovery, error) {
	return get[service.SkillSourceDiscovery](ctx, c, http.MethodPost, cp(company)+"/skill-sources/discover",
		SkillSourceReq{URL: in.URL, Ref: in.Ref, PathPrefix: in.PathPrefix, Excluded: in.Excluded})
}

func (c *Client) CreateSkillSource(ctx context.Context, company string, in SkillSourceReq) (api.SkillSource, error) {
	return get[api.SkillSource](ctx, c, http.MethodPost, cp(company)+"/skill-sources", in)
}

func (c *Client) SkillSources(ctx context.Context, company string) ([]api.SkillSource, error) {
	return list[api.SkillSource](ctx, c, cp(company)+"/skill-sources", nil)
}

func (c *Client) SkillSource(ctx context.Context, company, id string) (api.SkillSource, error) {
	return get[api.SkillSource](ctx, c, http.MethodGet, cp(company)+"/skill-sources/"+url.PathEscape(id), nil)
}

func (c *Client) SyncSkillSource(ctx context.Context, company, id string, force bool) (service.SkillSyncResult, error) {
	return get[service.SkillSyncResult](ctx, c, http.MethodPost, cp(company)+"/skill-sources/"+url.PathEscape(id)+"/sync", map[string]bool{"force": force})
}

func (c *Client) DeleteSkillSource(ctx context.Context, company, id string, keepSkills bool) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/skill-sources/"+url.PathEscape(id), url.Values{"keep_skills": {strconv.FormatBool(keepSkills)}}, nil, nil)
}

type SkillTestReq struct {
	Persona  string `json:"persona"`
	Input    string `json:"input,omitempty"`
	InputID  string `json:"input_id,omitempty"`
	Revision *int   `json:"revision,omitempty"`
}

func (c *Client) RunSkillTest(ctx context.Context, company, skill string, in SkillTestReq) (api.SkillTestRun, error) {
	return get[api.SkillTestRun](ctx, c, http.MethodPost, skillPath(company, skill)+"/test-runs", in)
}

func (c *Client) SkillTestRun(ctx context.Context, company, id string) (api.SkillTestRun, error) {
	return get[api.SkillTestRun](ctx, c, http.MethodGet, cp(company)+"/skill-test-runs/"+url.PathEscape(id), nil)
}

func (c *Client) SkillTestRuns(ctx context.Context, company, skill string) ([]api.SkillTestRun, error) {
	return list[api.SkillTestRun](ctx, c, skillPath(company, skill)+"/test-runs", nil)
}

func (c *Client) CancelSkillTest(ctx context.Context, company, id string) (api.SkillTestRun, error) {
	return get[api.SkillTestRun](ctx, c, http.MethodPost, cp(company)+"/skill-test-runs/"+url.PathEscape(id)+"/cancel", nil)
}
