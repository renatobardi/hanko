package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPHarness é o Harness de um Hanko de verdade: as tools vêm do servidor MCP (a lista real, não a do World) e
// cada chamada vai para ele. Serve ao modo live contra um ambiente de teste (HANKO_EVALS_URL); o padrão do
// runner continua sendo o World em memória.
type MCPHarness struct {
	sess  *sdk.ClientSession
	tools []ToolSpec
}

// NewMCPHarness conecta pelo transporte dado (HTTP com token ou, nos testes, em memória) e lê as tools.
func NewMCPHarness(ctx context.Context, t sdk.Transport) (*MCPHarness, error) {
	cl := sdk.NewClient(&sdk.Implementation{Name: "hanko-evals", Version: "1"}, nil)
	sess, err := cl.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("conectar ao MCP: %w", err)
	}
	h := &MCPHarness{sess: sess}
	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("listar tools: %w", err)
	}
	for _, tl := range res.Tools {
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		if b, err := json.Marshal(tl.InputSchema); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil && len(m) > 0 {
				schema = m
			}
		}
		h.tools = append(h.tools, ToolSpec{Name: tl.Name, Description: tl.Description, InputSchema: schema})
	}
	return h, nil
}

// Close encerra a sessão.
func (h *MCPHarness) Close() error { return h.sess.Close() }

// Tools implementa Harness.
func (h *MCPHarness) Tools() []ToolSpec { return h.tools }

// Call implementa Harness. Erro de tool (IsError) volta para o modelo; falha de transporte é erro de Go.
func (h *MCPHarness) Call(ctx context.Context, c ToolCall) (string, bool, error) {
	res, err := h.sess.CallTool(ctx, &sdk.CallToolParams{Name: c.Name, Arguments: c.Args})
	if err != nil {
		return "", false, err
	}
	var parts []string
	for _, ct := range res.Content {
		if tc, ok := ct.(*sdk.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n"), res.IsError, nil
}
