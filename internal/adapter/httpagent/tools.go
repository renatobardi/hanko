package httpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/provider"
)

// toolHost é de onde o modelo tira as tools: o servidor MCP do Hanko da run (Spec.MCPConfig, entrada "hanko").
type toolHost interface {
	// List devolve as tools no formato OpenAI (filtradas por allow, se não vazio).
	List(ctx context.Context, allow []string) ([]provider.Tool, error)
	// Call executa uma tool. isErr marca resultado de erro da própria tool (o modelo vê o texto e pode tentar de novo).
	Call(ctx context.Context, name string, args json.RawMessage) (text string, isErr bool, err error)
	Close()
}

// mcpEndpoint é a entrada "hanko" de Spec.MCPConfig.
type mcpEndpoint struct {
	URL     string
	Headers map[string]string
}

func hankoEndpoint(raw json.RawMessage) (mcpEndpoint, bool) {
	if len(raw) == 0 {
		return mcpEndpoint{}, false
	}
	var cfg struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return mcpEndpoint{}, false
	}
	s, ok := cfg.Servers["hanko"]
	if !ok || s.URL == "" {
		return mcpEndpoint{}, false
	}
	return mcpEndpoint{URL: s.URL, Headers: s.Headers}, true
}

type headerRT struct {
	base http.RoundTripper
	h    map[string]string
}

func (t headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.h {
		r.Header.Set(k, v)
	}
	rt := t.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(r)
}

type mcpHost struct {
	cs *sdk.ClientSession
}

// connectMCP conecta ao servidor MCP do Hanko por HTTP (o transporte já usado pelas personas).
// O MCP é do próprio Hanko (URL pública do servidor, que pode ser loopback ou rede privada): vai por um cliente
// comum, não pelo cliente com bloqueio de destinos internos que o modelo externo usa.
func connectMCP(ctx context.Context, ep mcpEndpoint) (toolHost, error) {
	client := &http.Client{Transport: headerRT{h: ep.Headers}}
	c := sdk.NewClient(&sdk.Implementation{Name: "hanko-http-harness", Version: "1"}, nil)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cs, err := c.Connect(cctx, &sdk.StreamableClientTransport{Endpoint: ep.URL, HTTPClient: client, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, fmt.Errorf("conectar ao MCP do Hanko: %w", err)
	}
	return &mcpHost{cs: cs}, nil
}

func (m *mcpHost) List(ctx context.Context, allow []string) ([]provider.Tool, error) {
	var out []provider.Tool
	for t, err := range m.cs.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("listar tools: %w", err)
		}
		if len(allow) > 0 && !contains(allow, t.Name) {
			continue
		}
		params, merr := json.Marshal(t.InputSchema)
		if merr != nil || string(params) == "null" {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, provider.Tool{Type: "function", Function: provider.FuncDef{Name: t.Name, Description: t.Description, Parameters: params}})
	}
	return out, nil
}

func (m *mcpHost) Call(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	var a map[string]any
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", false, fmt.Errorf("argumentos não são um objeto JSON: %w", err)
		}
	}
	res, err := m.cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: a})
	if err != nil {
		return "", false, err
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if sb.Len() == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			sb.Write(b)
		}
	}
	return sb.String(), res.IsError, nil
}

func (m *mcpHost) Close() { _ = m.cs.Close() }

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
