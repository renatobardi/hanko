// Package provider é o cliente de endpoints OpenAI-compatíveis (chat completions com tool calls) e do catálogo de
// modelos do OpenRouter. É usado pelo decision model (ADR-0056) e pelo harness http, que compartilham o mesmo
// transporte. Só a stdlib; nenhum I/O além do http.Client recebido (os testes usam httptest, nunca a rede real).
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	// maxResponseBytes limita qualquer resposta lida (catálogo incluso: o do OpenRouter tem centenas de modelos).
	maxResponseBytes = 8 << 20
	maxErrorBytes    = 512
)

// Message é uma mensagem do protocolo chat completions.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall é um pedido de ferramenta do modelo.
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function FuncCall `json:"function"`
}

// FuncCall é a função chamada; Arguments é um JSON em texto, como o protocolo define.
type FuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool declara uma função que o modelo pode chamar.
type Tool struct {
	Type     string  `json:"type"`
	Function FuncDef `json:"function"`
}

// FuncDef é a assinatura (JSON Schema dos parâmetros) de uma função.
type FuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ChatRequest é o pedido de uma resposta.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	Temperature *float64
	MaxTokens   int
	// JSONObject pede resposta em objeto JSON (response_format json_object).
	JSONObject bool
}

// Usage é o consumo reportado pelo provedor. CostUSD só vem preenchido quando o provedor informa o custo
// (OpenRouter, com usage.include); nil = estimar pelo preço do catálogo.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	CostUSD          *float64
}

// ChatResponse é a resposta normalizada: a primeira escolha.
type ChatResponse struct {
	Message      Message
	FinishReason string
	Usage        Usage
	Model        string
}

// Client fala com um endpoint OpenAI-compatível.
type Client struct {
	// BaseURL é a raiz da API (".../v1"); chat em BaseURL+"/chat/completions", catálogo em BaseURL+"/models".
	BaseURL string
	// APIKey vai em Authorization: Bearer; vazio = sem cabeçalho (modelos locais).
	APIKey string
	HTTP   *http.Client
	// Headers extras (OpenRouter aceita HTTP-Referer e X-Title para atribuição).
	Headers map[string]string
}

// HTTPError é uma resposta não-2xx do provedor. O corpo é truncado e nunca inclui a chave.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provedor respondeu %d: %s", e.Status, e.Body)
}

func (c *Client) endpoint(p string) (string, error) {
	u, err := url.Parse(strings.TrimRight(c.BaseURL, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("base_url inválida (use http:// ou https://)")
	}
	return u.String() + p, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	ep, err := c.endpoint(path)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("provider: codificar pedido: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, ep, rd)
	if err != nil {
		return fmt.Errorf("provider: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("provider: %w", redactKey(err, c.APIKey))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("provider: ler resposta: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return errors.New("provider: resposta grande demais")
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > maxErrorBytes {
			msg = msg[:maxErrorBytes] + "…"
		}
		if c.APIKey != "" {
			msg = strings.ReplaceAll(msg, c.APIKey, "***")
		}
		return &HTTPError{Status: resp.StatusCode, Body: msg}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("provider: resposta não é JSON válido: %w", err)
	}
	return nil
}

// redactKey tira a chave de uma mensagem de erro de transporte (a URL do erro não a carrega, mas proxies podem).
func redactKey(err error, key string) error {
	if key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "***"))
}

type chatWire struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	Tools          []Tool         `json:"tools,omitempty"`
	Temperature    *float64       `json:"temperature,omitempty"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
	Usage          map[string]any `json:"usage,omitempty"`
}

type chatReply struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64    `json:"prompt_tokens"`
		CompletionTokens int64    `json:"completion_tokens"`
		Cost             *float64 `json:"cost"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat pede uma resposta ao modelo.
func (c *Client) Chat(ctx context.Context, r ChatRequest) (ChatResponse, error) {
	if strings.TrimSpace(r.Model) == "" {
		return ChatResponse{}, errors.New("provider: modelo não informado")
	}
	w := chatWire{Model: r.Model, Messages: r.Messages, Tools: r.Tools, Temperature: r.Temperature, MaxTokens: r.MaxTokens,
		Usage: map[string]any{"include": true}}
	if r.JSONObject {
		w.ResponseFormat = map[string]any{"type": "json_object"}
	}
	var rep chatReply
	if err := c.do(ctx, http.MethodPost, "/chat/completions", w, &rep); err != nil {
		return ChatResponse{}, err
	}
	if rep.Error != nil && rep.Error.Message != "" {
		return ChatResponse{}, fmt.Errorf("provider: %s", rep.Error.Message)
	}
	if len(rep.Choices) == 0 {
		return ChatResponse{}, errors.New("provider: resposta sem choices")
	}
	ch := rep.Choices[0]
	out := ChatResponse{Message: ch.Message, FinishReason: ch.FinishReason, Model: rep.Model}
	if out.Message.Role == "" {
		out.Message.Role = "assistant"
	}
	if rep.Usage != nil {
		out.Usage = Usage{PromptTokens: rep.Usage.PromptTokens, CompletionTokens: rep.Usage.CompletionTokens, CostUSD: rep.Usage.Cost}
	}
	return out, nil
}

// CatalogModel é um modelo do catálogo (GET /models). Os preços são USD por token.
type CatalogModel struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ContextLength int64   `json:"context_length"`
	PromptUSD     float64 `json:"prompt_usd_token"`
	CompletionUSD float64 `json:"completion_usd_token"`
}

type catalogWire struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextLength int64  `json:"context_length"`
		Pricing       struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
	} `json:"data"`
}

// Catalog busca o catálogo de modelos, ordenado por id. Preço ausente ou ilegível vira 0 (modelo gratuito ou
// sem informação; o custo reportado pela resposta, quando houver, prevalece).
func (c *Client) Catalog(ctx context.Context) ([]CatalogModel, error) {
	var w catalogWire
	if err := c.do(ctx, http.MethodGet, "/models", nil, &w); err != nil {
		return nil, err
	}
	out := make([]CatalogModel, 0, len(w.Data))
	for _, m := range w.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		out = append(out, CatalogModel{ID: m.ID, Name: m.Name, ContextLength: m.ContextLength,
			PromptUSD: parsePrice(m.Pricing.Prompt), CompletionUSD: parsePrice(m.Pricing.Completion)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func parsePrice(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// EstimateCost é o custo em USD de uma chamada pelo preço por token.
func EstimateCost(u Usage, promptUSD, completionUSD float64) float64 {
	return float64(u.PromptTokens)*promptUSD + float64(u.CompletionTokens)*completionUSD
}
