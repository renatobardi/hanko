package evals

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
)

// DefaultLiveModel é o modelo pequeno da persona nos evals com modelo real (HANKO_EVALS_MODEL troca).
const DefaultLiveModel = "claude-haiku-4-5"

// Anthropic é o Model real: a API de mensagens com tool use. SÓ é construído quando o runner é ligado ao modo
// live (HANKO_EVALS_LIVE=1 ou --live); cada Next custa tokens. BaseURL e HTTP existem para os testes.
type Anthropic struct {
	APIKey  string
	Model   string
	BaseURL string // padrão https://api.anthropic.com
	HTTP    *http.Client
	// MaxTokens por resposta (padrão 1024).
	MaxTokens int
}

func (a *Anthropic) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 90 * time.Second}
}

type aContent struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type aMessage struct {
	Role    string     `json:"role"`
	Content []aContent `json:"content"`
}

type aTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type aRequest struct {
	Model     string     `json:"model"`
	MaxTokens int        `json:"max_tokens"`
	System    string     `json:"system,omitempty"`
	Messages  []aMessage `json:"messages"`
	Tools     []aTool    `json:"tools,omitempty"`
}

type aResponse struct {
	Content []aContent `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// buildRequest converte o Request neutro no corpo da API.
func (a *Anthropic) buildRequest(req Request) aRequest {
	out := aRequest{Model: a.Model, MaxTokens: a.MaxTokens, System: req.System}
	if out.Model == "" {
		out.Model = DefaultLiveModel
	}
	if out.MaxTokens == 0 {
		out.MaxTokens = 1024
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, aTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	for _, m := range req.Messages {
		am := aMessage{Role: m.Role}
		if m.Text != "" {
			am.Content = append(am.Content, aContent{Type: "text", Text: m.Text})
		}
		for _, c := range m.Calls {
			in := c.Args
			if in == nil {
				in = map[string]any{}
			}
			am.Content = append(am.Content, aContent{Type: "tool_use", ID: c.ID, Name: c.Name, Input: in})
		}
		for _, r := range m.Results {
			am.Content = append(am.Content, aContent{Type: "tool_result", ToolUseID: r.CallID, Content: r.Content, IsError: r.IsError})
		}
		out.Messages = append(out.Messages, am)
	}
	return out
}

// Next implementa Model.
func (a *Anthropic) Next(ctx context.Context, req Request) (Reply, error) {
	if a.APIKey == "" {
		return Reply{}, errors.New("sem chave da API para o modelo real")
	}
	body, err := json.Marshal(a.buildRequest(req))
	if err != nil {
		return Reply{}, err
	}
	base := strings.TrimRight(a.BaseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	hr.Header.Set("content-type", "application/json")
	hr.Header.Set("x-api-key", a.APIKey)
	hr.Header.Set("anthropic-version", "2023-06-01")
	resp, err := a.client().Do(hr)
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Reply{}, err
	}
	var ar aResponse
	if err := json.Unmarshal(raw, &ar); err != nil && resp.StatusCode < 300 {
		return Reply{}, fmt.Errorf("resposta ilegível: %w", err)
	}
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if ar.Error != nil {
			msg = ar.Error.Type + ": " + ar.Error.Message
		}
		return Reply{}, fmt.Errorf("API %d: %s", resp.StatusCode, msg)
	}
	rep := Reply{Usage: Usage{InputTokens: ar.Usage.InputTokens, OutputTokens: ar.Usage.OutputTokens}}
	var texts []string
	for _, c := range ar.Content {
		switch c.Type {
		case "text":
			texts = append(texts, c.Text)
		case "tool_use":
			rep.Calls = append(rep.Calls, ToolCall{ID: c.ID, Name: c.Name, Args: c.Input})
		}
	}
	rep.Text = strings.Join(texts, "\n")
	return rep, nil
}
