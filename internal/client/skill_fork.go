package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- fork de skill e skills de uma run (ADR-0044) ----------

// ForkSkillReq controla a bifurcação de uma skill.
type ForkSkillReq struct {
	Slug         string `json:"slug,omitempty"`
	Name         string `json:"name,omitempty"`
	Revision     int    `json:"revision,omitempty"`
	MovePersonas bool   `json:"move_personas,omitempty"`
}

func (c *Client) ForkSkill(ctx context.Context, company, skill string, in ForkSkillReq) (api.Skill, error) {
	return get[api.Skill](ctx, c, http.MethodPost, skillPath(company, skill)+"/fork", in)
}

func (c *Client) SkillForks(ctx context.Context, company, skill string) ([]api.Skill, error) {
	return list[api.Skill](ctx, c, skillPath(company, skill)+"/forks", nil)
}

func (c *Client) RunSkills(ctx context.Context, company, run string) ([]api.RunSkill, error) {
	return list[api.RunSkill](ctx, c, cp(company)+"/runs/"+url.PathEscape(run)+"/skills", nil)
}
