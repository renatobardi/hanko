package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
)

// GateConfig faz o fake pedir uma decisão humana como faria um agente de verdade: chama a tool MCP hanko_gate_request
// do próprio Hanko, com o token da run (do Spec.MCPConfig). Serve às jornadas e2e do fluxo de gates (ADR-0058 §D),
// sem processo externo e sem rede além do Hanko.
type GateConfig struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// maxGateReply é o teto do que se lê da resposta do MCP.
const maxGateReply = 1 << 20

// requestGate chama hanko_gate_request e devolve um resumo curto da resposta (para o log da run).
func requestGate(ctx context.Context, spec adapter.Spec, g GateConfig, client *http.Client) (string, error) {
	url, headers, err := hankoMCP(spec.MCPConfig)
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(g.Title)
	if title == "" {
		title = "Aprovar o resultado do fake"
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "hanko_gate_request", "arguments": map[string]any{
			"title": title, "body": g.Body, "event_id": "fake-gate-" + spec.RunID.String(),
		}},
	})
	if err != nil {
		return "", fmt.Errorf("fake: gate: %w", err)
	}
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("fake: gate: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req) //nolint:gosec // o destino é o MCP do próprio Hanko, vindo da configuração da run
	if err != nil {
		return "", fmt.Errorf("fake: gate: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxGateReply))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fake: gate: MCP respondeu %d", resp.StatusCode)
	}
	var rpc struct {
		Error  *struct{ Message string } `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return "", fmt.Errorf("fake: gate: resposta MCP ilegível: %w", err)
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("fake: gate: %s", rpc.Error.Message)
	}
	text := ""
	if len(rpc.Result.Content) > 0 {
		text = rpc.Result.Content[0].Text
	}
	if rpc.Result.IsError {
		return "", fmt.Errorf("fake: gate recusado: %s", text)
	}
	return "gate pedido: " + title, nil
}

// hankoMCP extrai a URL e os headers do servidor "hanko" do Spec.MCPConfig.
func hankoMCP(raw json.RawMessage) (string, map[string]string, error) {
	var cfg struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &cfg) != nil {
		return "", nil, errors.New("fake: gate: a run não tem configuração MCP")
	}
	s, ok := cfg.Servers["hanko"]
	if !ok || s.URL == "" {
		return "", nil, errors.New("fake: gate: a configuração MCP não tem o servidor hanko")
	}
	return s.URL, s.Headers, nil
}
