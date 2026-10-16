package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Tools de plugin (ADR-0038). Aparecem como plugin_<id>_<tool> só para a empresa do token de run, e só com o
// plugin ativo e ligado nela. Como o conjunto muda por empresa, cada empresa com tools de plugin recebe um
// servidor MCP próprio (as tools hanko_* mais as dos plugins dela), guardado num cache pela impressão digital do
// conjunto; as demais conexões usam o servidor comum.

// pluginToolsBackend é o que as tools de plugin pedem ao service; opcional como skillBackend.
type pluginToolsBackend interface {
	PluginTools(ctx context.Context, companyID domain.ID) ([]service.PluginToolView, error)
	CallPluginTool(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.PluginToolCall) (service.PluginToolResult, error)
}

const pluginServerCacheMax = 128

type pluginServers struct {
	base  *sdk.Server
	build func() *sdk.Server
	pb    pluginToolsBackend
	log   *slog.Logger

	mu    sync.Mutex
	cache map[string]*sdk.Server
}

func newPluginServers(base *sdk.Server, build func() *sdk.Server, b Backend, log *slog.Logger) *pluginServers {
	pb, _ := b.(pluginToolsBackend)
	return &pluginServers{base: base, build: build, pb: pb, log: log, cache: map[string]*sdk.Server{}}
}

// server escolhe o servidor MCP da requisição (o handler já pôs a identidade no contexto).
func (ps *pluginServers) server(r *http.Request) *sdk.Server {
	if ps.pb == nil {
		return ps.base
	}
	id, ok := identityFrom(r.Context())
	if !ok || id.CompanyID == nil {
		return ps.base
	}
	tools, err := ps.pb.PluginTools(r.Context(), *id.CompanyID)
	if err != nil {
		ps.log.Warn("mcp: tools de plugin indisponíveis", "company_id", *id.CompanyID, "err", err)
		return ps.base
	}
	if len(tools) == 0 {
		return ps.base
	}
	h := sha256.New()
	_ = json.NewEncoder(h).Encode(tools)
	key := id.CompanyID.String() + ":" + hex.EncodeToString(h.Sum(nil))
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if s, ok := ps.cache[key]; ok {
		return s
	}
	s := ps.build()
	for _, t := range tools {
		s.AddTool(&sdk.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}, ps.call(t.Name))
	}
	if len(ps.cache) >= pluginServerCacheMax {
		ps.cache = map[string]*sdk.Server{}
	}
	ps.cache[key] = s
	return s
}

// call encaminha a chamada ao service. Falha do plugin vira resultado de erro da tool, não erro de protocolo.
func (ps *pluginServers) call(name string) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		id, ok := identityFrom(ctx)
		if !ok || id.CompanyID == nil {
			return toolError("tools de plugin exigem uma conexão com token de run"), nil
		}
		var args json.RawMessage
		if req != nil && req.Params != nil {
			args = req.Params.Arguments
		}
		res, err := ps.pb.CallPluginTool(ctx, id.Actor, *id.CompanyID, service.PluginToolCall{Name: name, Arguments: args,
			PersonaID: id.PersonaID, RunID: id.RunID})
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return toolError("a tool " + name + " não está disponível (plugin desligado ou desinstalado)"), nil
			}
			return toolError(err.Error()), nil
		}
		out := &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: res.Text}}, IsError: res.IsError}
		if len(res.Data) > 0 {
			out.StructuredContent = res.Data
		}
		return out, nil
	}
}

func toolError(msg string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: msg}}, IsError: true}
}
