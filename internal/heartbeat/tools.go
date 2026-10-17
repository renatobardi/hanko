package heartbeat

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Gateway de tools (ADR-0036): as integrações MCP http da run entram no mcpServers apontando para o Hanko
// (/mcp/run/{integração}), autenticadas pelo run token; o servidor de verdade e os segredos ficam no Hanko.

// ToolGatewayPath é o prefixo do gateway de tools das runs (montado no mux pelo app).
const ToolGatewayPath = "/mcp/run/"

// toolGatewayMCP monta as entradas do mcpServers para as integrações que passam pelo gateway.
func toolGatewayMCP(publicURL, token string, slugs []string) map[string]json.RawMessage {
	if token == "" || len(slugs) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(slugs))
	for _, slug := range slugs {
		raw, err := json.Marshal(map[string]any{
			"type":    "http",
			"url":     publicURL + ToolGatewayPath + url.PathEscape(slug),
			"headers": map[string]string{"Authorization": "Bearer " + token},
		})
		if err != nil {
			continue
		}
		out[slug] = raw
	}
	return out
}

// runToolsBackend é o RunExtras que conhece a run (para o perfil de ferramentas do projeto valer); opcional.
type runToolsBackend interface {
	RunExtrasForRun(ctx context.Context, companyID, personaID, runID domain.ID) (service.RunExtras, error)
}

func (h *Heartbeat) runExtrasFor(ctx context.Context, eb runExtrasBackend, rc service.RunContext) (service.RunExtras, error) {
	if rb, ok := h.svc.(runToolsBackend); ok {
		return rb.RunExtrasForRun(ctx, rc.Company.ID, rc.Persona.ID, rc.Run.ID)
	}
	return eb.RunExtras(ctx, rc.Company.ID, rc.Persona.ID)
}

// toolAuditPruner aplica a retenção da auditoria do gateway; opcional como os demais.
type toolAuditPruner interface {
	PruneToolInvocations(ctx context.Context) (int64, error)
}

// pruneToolAudit é melhor esforço: uma falha só vai para o log.
func (h *Heartbeat) pruneToolAudit(ctx context.Context) {
	p, ok := h.svc.(toolAuditPruner)
	if !ok {
		return
	}
	if n, err := p.PruneToolInvocations(ctx); err != nil {
		h.log.Warn("tool_audit_prune", "err", err)
	} else if n > 0 {
		h.log.Debug("tool_audit_prune", "deleted", n)
	}
}
