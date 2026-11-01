package heartbeat

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Gateway de tools (ADR-0036): as integrações MCP http da run entram no mcpServers apontando para o Hanko
// (/mcp/run/{integração}), autenticadas pelo run token; o servidor de verdade e os segredos ficam no Hanko.

// ToolGatewayPath é o prefixo do gateway de tools das runs (montado no mux pelo app).
const ToolGatewayPath = "/mcp/run/"

// ToolGatewayAggregatePath é o endpoint único das runs (ADR-0053).
const ToolGatewayAggregatePath = "/mcp/run"

// toolGatewayMCP monta as entradas do mcpServers para as integrações que passam pelo gateway. Com single, uma só
// entrada (o endpoint único) leva as tools de todas elas, com o nome "<integração>__<tool>".
func toolGatewayMCP(publicURL, token string, slugs []string, single bool) map[string]json.RawMessage {
	if token == "" || len(slugs) == 0 {
		return nil
	}
	if single {
		raw, err := json.Marshal(map[string]any{
			"type":    "http",
			"url":     publicURL + ToolGatewayAggregatePath,
			"headers": map[string]string{"Authorization": "Bearer " + token},
		})
		if err != nil {
			return nil
		}
		return map[string]json.RawMessage{"hanko-tools": raw}
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

// ---- política de tools para as tools nativas do harness (ADR-0053) ----

// nativeToolBackend é o que o guarda precisa do service; opcional como os demais.
type nativeToolBackend interface {
	RunToolCaller(ctx context.Context, companyID, runID domain.ID) (service.ToolCaller, error)
	NativeToolCheck(ctx context.Context, c service.ToolCaller, req service.NativeToolRequest) (service.NativeToolDecision, error)
	AwaitNativeToolGate(ctx context.Context, c service.ToolCaller, gateID domain.ID) (service.NativeToolDecision, error)
}

// attachToolPolicy liga a política de tools às tools nativas da run: o claude recebe o hook PreToolUse (o executável
// `hanko`), o ACP recebe o guarda do permission request. Outros harnesses seguem o modo de permissão deles; o aviso diz isso.
func (h *Heartbeat) attachToolPolicy(spec *adapter.Spec, rc service.RunContext, emit func(domain.RunEventKind, string)) {
	switch spec.Agent {
	case "claude":
		bin := h.opts.ToolHookBin
		if bin == "" {
			if exe, err := os.Executable(); err == nil {
				bin = exe
			}
		}
		if bin == "" {
			emit(domain.EventSystem, "política de tools: não foi possível localizar o executável do hanko; as tools nativas seguem o modo de permissão da persona")
			return
		}
		spec.ToolHookBin = bin
	case "acp":
		nb, ok := h.svc.(nativeToolBackend)
		if !ok {
			return
		}
		spec.ToolGuard = &nativeGuard{be: nb, company: rc.Company.ID, run: rc.Run.ID}
	default:
		emit(domain.EventSystem, "política de tools: o harness "+spec.Agent+" não expõe as tools nativas; a política vale só para as tools de integração e de plugin")
	}
}

// deniedToolsPrefix é o começo do evento que o adapter do claude emite quando o harness recusou tools por permissão.
const deniedToolsPrefix = "tools negadas por permissão: "

// nativeDenialHint explica a recusa quando ela veio do modo de permissão do próprio claude: a persona não tem política de
// tools nativa, então o hook não entrou e, em modo headless, o claude recusa o que não libera sozinho (Bash fora da lista
// dele, por exemplo). Sem isso o operador só vê a recusa seca e procura o erro no lugar errado (ADR-0053).
func nativeDenialHint(spec adapter.Spec, personaSlug string) string {
	if spec.Agent != "claude" || spec.ReadOnly || spec.ToolHookBin != "" {
		return ""
	}
	return " (sem política de tools nativa para esta persona: valeu o modo de permissão do claude, que em modo headless recusa o que não libera sozinho; " +
		"para liberar, `hanko tool-policy set allow --persona " + personaSlug + " --native <tool>` ou a aba Política em Integrações)"
}

// denialHintSink completa o evento de tools negadas com nativeDenialHint; os demais eventos passam como estão.
func denialHintSink(next adapter.Sink, hint string) adapter.Sink {
	if hint == "" {
		return next
	}
	return adapter.SinkFunc(func(ctx context.Context, e adapter.Event) error {
		if e.Kind == domain.EventSystem && strings.HasPrefix(e.Text, deniedToolsPrefix) {
			e.Text += hint
		}
		return next.Emit(ctx, e)
	})
}

// nativeGuard consulta o motor de política a cada permission request do ACP. Em ask espera o gate dentro da run.
type nativeGuard struct {
	be      nativeToolBackend
	company domain.ID
	run     domain.ID
}

func (g *nativeGuard) Check(ctx context.Context, req adapter.ToolRequest) adapter.ToolVerdict {
	c, err := g.be.RunToolCaller(ctx, g.company, g.run)
	if err != nil {
		return adapter.ToolVerdict{Decision: adapter.ToolDeny, Reason: "política de tools indisponível: " + err.Error()}
	}
	c.Source = domain.SourceACP
	d, err := g.be.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: req.Name, Title: req.Title, Input: req.Input})
	if err != nil {
		return adapter.ToolVerdict{Decision: adapter.ToolDeny, Reason: "política de tools indisponível: " + err.Error()}
	}
	if d.Decision == service.NativeAsk && d.GateID != nil {
		if d, err = g.be.AwaitNativeToolGate(ctx, c, *d.GateID); err != nil {
			return adapter.ToolVerdict{Decision: adapter.ToolDeny, Reason: "não foi possível esperar o gate: " + err.Error()}
		}
	}
	switch d.Decision {
	case service.NativeAllow:
		return adapter.ToolVerdict{Decision: adapter.ToolAllow, Reason: d.Reason}
	case service.NativeDeny:
		return adapter.ToolVerdict{Decision: adapter.ToolDeny, Reason: d.Reason}
	}
	return adapter.ToolVerdict{}
}

// toolGateExpirer vence os gates tool pendentes cujo prazo passou; opcional como os demais.
type toolGateExpirer interface {
	ExpireToolGates(ctx context.Context) (int, error)
}

// expireToolGates é melhor esforço: uma falha só vai para o log.
func (h *Heartbeat) expireToolGates(ctx context.Context) {
	x, ok := h.svc.(toolGateExpirer)
	if !ok {
		return
	}
	if n, err := x.ExpireToolGates(ctx); err != nil {
		h.log.Warn("tool_gate_expire", "err", err)
	} else if n > 0 {
		h.log.Debug("tool_gate_expire", "expired", n)
	}
}
