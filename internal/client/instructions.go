package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- revisões das instruções da persona (ADR-0052) ----------

func personaPath(company, persona string) string {
	return cp(company) + "/personas/" + url.PathEscape(persona)
}

// InstructionRevisions lista as revisões das instruções (mais nova primeiro), sem o texto.
func (c *Client) InstructionRevisions(ctx context.Context, company, persona string) ([]api.InstructionRevision, error) {
	return list[api.InstructionRevision](ctx, c, personaPath(company, persona)+"/instructions/revisions", nil)
}

// InstructionRevision devolve uma revisão com o texto; against > 0 inclui o diff daquela revisão para esta.
func (c *Client) InstructionRevision(ctx context.Context, company, persona string, revision, against int) (api.InstructionRevision, error) {
	var out api.InstructionRevision
	q := url.Values{}
	if against > 0 {
		q.Set("against", strconv.Itoa(against))
	}
	err := c.do(ctx, http.MethodGet, personaPath(company, persona)+"/instructions/revisions/"+strconv.Itoa(revision), q, nil, &out)
	return out, err
}

// RestoreInstructions volta as instruções ao texto de uma revisão (cria uma revisão nova).
func (c *Client) RestoreInstructions(ctx context.Context, company, persona string, revision int, reason string) (api.Persona, error) {
	return get[api.Persona](ctx, c, http.MethodPost, personaPath(company, persona)+"/instructions/restore",
		map[string]any{"revision": revision, "reason": reason})
}
