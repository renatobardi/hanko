package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/api"
)

// Tags de persona (ADR-0087).

// PersonaTagReq cria uma tag. Cor vazia vale gray.
type PersonaTagReq struct {
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
}

// UpdatePersonaTagReq edita uma tag; campo nil não muda.
type UpdatePersonaTagReq struct {
	Name        *string `json:"name,omitempty"`
	Color       *string `json:"color,omitempty"`
	Description *string `json:"description,omitempty"`
}

// OrgPersonaQuery é o recorte do organograma.
type OrgPersonaQuery struct {
	TagIDs   []string
	MatchAll bool
	Untagged bool
	Levels   []int
	MaxDepth int
	Archived bool
}

func tagsPath(company string) string { return cp(company) + "/persona-tags" }

// maxListLimit é o teto de itens por página da API; o cadastro tem no máximo 200 tags.
const maxListLimit = "500"

// ListPersonaTags lista o cadastro; archived inclui as arquivadas.
func (c *Client) ListPersonaTags(ctx context.Context, company string, archived bool) ([]api.PersonaTag, error) {
	q := url.Values{"limit": {maxListLimit}}
	if archived {
		q.Set("archived", "true")
	}
	return list[api.PersonaTag](ctx, c, tagsPath(company), q)
}

// GetPersonaTag devolve uma tag com a contagem de personas.
func (c *Client) GetPersonaTag(ctx context.Context, company, id string) (api.PersonaTag, error) {
	return get[api.PersonaTag](ctx, c, http.MethodGet, tagsPath(company)+"/"+url.PathEscape(id), nil)
}

// CreatePersonaTag cria uma tag.
func (c *Client) CreatePersonaTag(ctx context.Context, company string, req PersonaTagReq) (api.PersonaTag, error) {
	return get[api.PersonaTag](ctx, c, http.MethodPost, tagsPath(company), req)
}

// UpdatePersonaTag edita nome, cor ou descrição.
func (c *Client) UpdatePersonaTag(ctx context.Context, company, id string, req UpdatePersonaTagReq) (api.PersonaTag, error) {
	return get[api.PersonaTag](ctx, c, http.MethodPatch, tagsPath(company)+"/"+url.PathEscape(id), req)
}

// SetPersonaTagArchived arquiva (archive=true) ou desarquiva a tag.
func (c *Client) SetPersonaTagArchived(ctx context.Context, company, id string, archive bool) (api.PersonaTag, error) {
	action := "/unarchive"
	if archive {
		action = "/archive"
	}
	return get[api.PersonaTag](ctx, c, http.MethodPost, tagsPath(company)+"/"+url.PathEscape(id)+action, nil)
}

// DeletePersonaTag apaga a tag e as ligações.
func (c *Client) DeletePersonaTag(ctx context.Context, company, id string) error {
	return c.do(ctx, http.MethodDelete, tagsPath(company)+"/"+url.PathEscape(id), nil, nil, nil)
}

func personaTagLinkPath(company, persona, tagID string) string {
	return cp(company) + "/personas/" + url.PathEscape(persona) + "/tags/" + url.PathEscape(tagID)
}

// TagPersona aplica uma tag existente à persona.
func (c *Client) TagPersona(ctx context.Context, company, persona, tagID string) error {
	return c.do(ctx, http.MethodPut, personaTagLinkPath(company, persona, tagID), nil, nil, nil)
}

// UntagPersona tira a tag da persona.
func (c *Client) UntagPersona(ctx context.Context, company, persona, tagID string) error {
	return c.do(ctx, http.MethodDelete, personaTagLinkPath(company, persona, tagID), nil, nil, nil)
}

// CreateAndTagPersona aplica a tag de nome dado, criando-a se não existe.
func (c *Client) CreateAndTagPersona(ctx context.Context, company, persona string, req PersonaTagReq) (api.PersonaTagRef, error) {
	return get[api.PersonaTagRef](ctx, c, http.MethodPost, cp(company)+"/personas/"+url.PathEscape(persona)+"/tags", req)
}

// ListOrgPersonas lista as personas do recorte do organograma.
func (c *Client) ListOrgPersonas(ctx context.Context, company string, f OrgPersonaQuery) ([]api.OrgPersona, error) {
	q := url.Values{"limit": {maxListLimit}}
	if len(f.TagIDs) > 0 {
		q.Set("tag", strings.Join(f.TagIDs, ","))
	}
	if f.MatchAll {
		q.Set("match", "all")
	}
	if f.Untagged {
		q.Set("untagged", "true")
	}
	if len(f.Levels) > 0 {
		ls := make([]string, len(f.Levels))
		for i, l := range f.Levels {
			ls[i] = strconv.Itoa(l)
		}
		q.Set("level", strings.Join(ls, ","))
	}
	if f.MaxDepth > 0 {
		q.Set("max_depth", strconv.Itoa(f.MaxDepth))
	}
	if f.Archived {
		q.Set("archived", "true")
	}
	return list[api.OrgPersona](ctx, c, cp(company)+"/org-chart/personas", q)
}
