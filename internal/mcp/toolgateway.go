package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Gateway de tools (ADR-0036). Dois endpoints MCP (streamable http, sem sessão, respostas JSON):
//
//   - /mcp/run/{integração}: o de uma run. Autenticado pelo run token (de run ativa); expõe as tools de UMA
//     integração MCP http da persona, filtradas pelo perfil efetivo, com os nomes originais.
//   - /mcp/gw/{empresa}/{gateway}: o gateway nomeado, para clientes de fora. Autenticado por um token hkgw_ do
//     gateway; agrega as integrações com o nome "<integração>__<tool>" e respeita as ações do token.
//
// O servidor de verdade e os segredos ficam no Hanko: o cliente só fala com estes endpoints. Os argumentos das
// chamadas nunca vão para o log.

const (
	// RunToolGatewayPattern e NamedToolGatewayPattern são os padrões do mux.
	RunToolGatewayPattern   = "/mcp/run/{integration}"
	NamedToolGatewayPattern = "/mcp/gw/{company}/{gateway}"

	maxGatewayBody = 2 << 20

	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
)

// supportedProtocolVersions são as versões do protocolo que o gateway aceita ecoar no initialize.
var supportedProtocolVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

const latestProtocolVersion = "2025-06-18"

// ToolGatewayBackend é o que os endpoints precisam do service.
type ToolGatewayBackend interface {
	GetRun(ctx context.Context, companyID, runID domain.ID) (service.RunView, error)
	RunToolCaller(ctx context.Context, companyID, runID domain.ID) (service.ToolCaller, error)
	GatewayCaller(ctx context.Context, companySlug, gatewaySlug, token string) (service.ToolCaller, error)
	GatewayListTools(ctx context.Context, c service.ToolCaller, integrationSlug string) ([]service.GatewayTool, error)
	GatewayCallTool(ctx context.Context, c service.ToolCaller, integrationSlug, name string, args json.RawMessage) (service.GatewayCallResult, error)
}

var _ ToolGatewayBackend = (*service.Service)(nil)

// ToolGatewayDeps configura os endpoints do gateway de tools.
type ToolGatewayDeps struct {
	Backend ToolGatewayBackend
	Tokens  auth.RunTokens
	Logger  *slog.Logger
	Version string
}

type toolGateway struct {
	d     ToolGatewayDeps
	log   *slog.Logger
	named bool
}

// RunToolGateway devolve o handler de /mcp/run/{integração}.
func RunToolGateway(d ToolGatewayDeps) http.Handler { return newToolGateway(d, false) }

// NamedToolGateway devolve o handler de /mcp/gw/{empresa}/{gateway}.
func NamedToolGateway(d ToolGatewayDeps) http.Handler { return newToolGateway(d, true) }

func newToolGateway(d ToolGatewayDeps, named bool) *toolGateway {
	log := d.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &toolGateway{d: d, log: log, named: named}
}

func (g *toolGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "use POST (o gateway de tools não abre stream)", http.StatusMethodNotAllowed)
		return
	}
	caller, slug, status, msg := g.authenticate(r)
	if status != 0 {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="hanko"`)
		}
		http.Error(w, msg, status)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGatewayBody))
	if err != nil {
		http.Error(w, "corpo inválido ou grande demais", http.StatusRequestEntityTooLarge)
		return
	}
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		writeRPCError(w, nil, rpcInvalidRequest, "corpo vazio")
		return
	}
	if trim[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(trim, &batch) != nil || len(batch) == 0 {
			writeRPCError(w, nil, rpcParseError, "JSON-RPC inválido")
			return
		}
		var out []map[string]any
		for _, m := range batch {
			if resp := g.handle(r.Context(), caller, slug, m); resp != nil {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, out)
		return
	}
	resp := g.handle(r.Context(), caller, slug, trim)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, resp)
}

// authenticate resolve o chamador. status != 0 é a resposta HTTP de recusa.
func (g *toolGateway) authenticate(r *http.Request) (service.ToolCaller, string, int, string) {
	tok, ok := bearerToken(r)
	if !ok {
		return service.ToolCaller{}, "", http.StatusUnauthorized, "unauthorized: missing bearer token"
	}
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	if g.named {
		c, err := g.d.Backend.GatewayCaller(ctx, r.PathValue("company"), r.PathValue("gateway"), tok)
		switch {
		case errors.Is(err, domain.ErrUnauthenticated):
			return c, "", http.StatusUnauthorized, "unauthorized: invalid, expired or revoked gateway token"
		case err != nil:
			g.log.Error("tool-gateway: autenticar token", "err", err)
			return c, "", http.StatusServiceUnavailable, "autenticação indisponível"
		}
		return c, "", 0, ""
	}
	if strings.HasPrefix(tok, domain.GatewayTokenPrefix) || auth.IsAPIKey(tok) {
		return service.ToolCaller{}, "", http.StatusUnauthorized, "unauthorized: use the run token"
	}
	claims, err := g.d.Tokens.VerifyActive(ctx, tok, func(ctx context.Context, cid, rid domain.ID) (domain.RunStatus, error) {
		rv, err := g.d.Backend.GetRun(ctx, cid, rid)
		return rv.Status, err
	})
	if err != nil {
		if errors.Is(err, auth.ErrBadToken) {
			return service.ToolCaller{}, "", http.StatusUnauthorized, "unauthorized: invalid or expired run token, or run no longer active"
		}
		g.log.Error("tool-gateway: autenticar run token", "err", err)
		return service.ToolCaller{}, "", http.StatusServiceUnavailable, "autenticação indisponível"
	}
	cid, err1 := domain.ParseID(claims.CompanyID)
	rid, err2 := domain.ParseID(claims.RunID)
	if err1 != nil || err2 != nil {
		return service.ToolCaller{}, "", http.StatusUnauthorized, "unauthorized: invalid run token claims"
	}
	c, err := g.d.Backend.RunToolCaller(ctx, cid, rid)
	switch {
	case errors.Is(err, domain.ErrNotAllowed):
		return c, "", http.StatusForbidden, "esta execução não usa o gateway de tools"
	case errors.Is(err, domain.ErrNotFound):
		return c, "", http.StatusUnauthorized, "unauthorized: run not found"
	case err != nil:
		g.log.Error("tool-gateway: resolver run", "err", err, "run_id", rid)
		return c, "", http.StatusServiceUnavailable, "autenticação indisponível"
	}
	return c, r.PathValue("integration"), 0, ""
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result}
}

func rpcErr(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": rawID(id), "error": map[string]any{"code": code, "message": msg}}
}

// handle responde a uma mensagem; nil para notificações e respostas (que não têm resposta).
func (g *toolGateway) handle(ctx context.Context, c service.ToolCaller, slug string, raw json.RawMessage) map[string]any {
	var m rpcMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return rpcErr(nil, rpcParseError, "JSON-RPC inválido")
	}
	if len(m.ID) == 0 || string(m.ID) == "null" {
		return nil // notificação (notifications/initialized, cancelled...) ou resposta do cliente
	}
	if m.Method == "" {
		return nil
	}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		v := latestProtocolVersion
		if slices.Contains(supportedProtocolVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		version := g.d.Version
		if version == "" {
			version = "dev"
		}
		instr := "Hanko: gateway de tools. As chamadas passam pelo perfil de ferramentas da empresa; uma tool em modo " +
			"\"perguntar antes\" abre um gate e só passa depois da aprovação de um humano (repita a chamada com os mesmos argumentos)."
		return rpcResult(m.ID, map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo": map[string]any{"name": "hanko-tools", "title": "Hanko", "version": version}, "instructions": instr})
	case "ping":
		return rpcResult(m.ID, map[string]any{})
	case "tools/list":
		tools, err := g.d.Backend.GatewayListTools(ctx, c, slug)
		if err != nil {
			return g.backendErr(m.ID, err)
		}
		list := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			e := map[string]any{"name": t.Name, "inputSchema": t.InputSchema}
			if t.Title != "" {
				e["title"] = t.Title
			}
			if t.Description != "" {
				e["description"] = t.Description
			}
			if t.Annotations.Declared() {
				e["annotations"] = t.Annotations
			}
			list = append(list, e)
		}
		return rpcResult(m.ID, map[string]any{"tools": list})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil || strings.TrimSpace(p.Name) == "" {
			return rpcErr(m.ID, rpcInvalidParams, "tools/call sem name")
		}
		res, err := g.d.Backend.GatewayCallTool(ctx, c, slug, p.Name, p.Arguments)
		if err != nil {
			return g.backendErr(m.ID, err)
		}
		switch {
		case res.Unknown:
			return rpcErr(m.ID, rpcInvalidParams, res.Message)
		case res.Result != nil:
			return rpcResult(m.ID, res.Result)
		}
		return rpcResult(m.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": res.Message}}, "isError": true})
	}
	return rpcErr(m.ID, rpcMethodNotFound, "método não suportado pelo gateway de tools: "+m.Method)
}

func (g *toolGateway) backendErr(id json.RawMessage, err error) map[string]any {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return rpcErr(id, rpcCodeForbidden, "o token não permite esta ação")
	case errors.Is(err, domain.ErrNotFound):
		return rpcErr(id, rpcInvalidParams, "integração não disponível para esta persona")
	}
	g.log.Error("tool-gateway: falha interna", "err", err)
	return rpcErr(id, rpcCodeInternal, "falha interna do gateway de tools")
}
