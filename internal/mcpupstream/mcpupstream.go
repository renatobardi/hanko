// Package mcpupstream é o cliente do Hanko para os servidores MCP http das integrações (ADR-0036): descobre o
// catálogo (tools/list) e repassa uma chamada (tools/call) com os headers guardados. Cada operação abre a sua
// própria sessão (initialize + pedido) e a fecha no fim: sem estado entre chamadas, sem conexão longa. O cliente
// HTTP vem de quem chama (o service passa o cliente com a proteção SSRF de netguard).
package mcpupstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/renatobardi/hanko/internal/domain"
)

// Endpoint é um servidor MCP http com as credenciais que o Hanko guarda para ele.
type Endpoint struct {
	URL     string
	Headers map[string]string // segredos: nunca saem daqui
	Client  *http.Client      // obrigatório
	Timeout time.Duration     // teto da operação inteira; zero = 60 s
	Version string            // versão do Hanko, anunciada no initialize
}

// Tool é uma tool como o servidor a anunciou.
type Tool struct {
	Name        string
	Title       string
	Description string
	InputSchema json.RawMessage
	Annotations domain.ToolAnnotations
	// HasAnnotations: o servidor mandou o objeto de anotações.
	HasAnnotations bool
}

// HeaderParamPrefix é o prefixo do cabeçalho HTTP que repete um parâmetro marcado `x-mcp-header` no input schema.
const HeaderParamPrefix = "Mcp-Param-"

// headerParamName aceita só nomes que cabem num cabeçalho: letras, dígitos, hífen e sublinhado.
var headerParamName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// HeaderParams lê no input schema da tool as propriedades marcadas `x-mcp-header` e devolve, para as que vieram nos
// argumentos, o cabeçalho `Mcp-Param-<nome>` com o valor. Alguns servidores MCP remotos (o do GitHub, por exemplo)
// exigem esses parâmetros também como cabeçalho e recusam a chamada sem eles ("header mismatch: missing
// Mcp-Param-owner header"). O argumento continua no corpo. Só valores escalares entram; um valor com quebra de linha
// é ignorado (não cabe num cabeçalho) e um nome fora do padrão também.
func HeaderParams(schema, args json.RawMessage) map[string]string {
	var sc struct {
		Properties map[string]struct {
			Header string `json:"x-mcp-header"`
		} `json:"properties"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &sc) != nil || len(sc.Properties) == 0 {
		return nil
	}
	var in map[string]json.RawMessage
	if len(args) == 0 || json.Unmarshal(args, &in) != nil {
		return nil
	}
	var out map[string]string
	for prop, p := range sc.Properties {
		if p.Header == "" || !headerParamName.MatchString(p.Header) {
			continue
		}
		raw, ok := in[prop]
		if !ok {
			continue
		}
		v, ok := scalarText(raw)
		if !ok || strings.ContainsAny(v, "\r\n") {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[HeaderParamPrefix+p.Header] = v
	}
	return out
}

// scalarText é o texto de um valor JSON escalar: string sem aspas, número e booleano como vieram.
func scalarText(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	t := strings.TrimSpace(string(raw))
	switch {
	case t == "true", t == "false":
		return t, true
	case t != "" && t != "null" && (t[0] == '-' || (t[0] >= '0' && t[0] <= '9')):
		return t, true
	}
	return "", false
}

// WithHeaders devolve uma cópia do endpoint com cabeçalhos a mais para uma chamada. Os guardados no endpoint vencem:
// um parâmetro nunca sobrescreve a credencial.
func (ep Endpoint) WithHeaders(extra map[string]string) Endpoint {
	if len(extra) == 0 {
		return ep
	}
	h := map[string]string{}
	for k, v := range extra {
		h[k] = v
	}
	for k, v := range ep.Headers {
		h[k] = v
	}
	ep.Headers = h
	return ep
}

// RemoteError é a recusa do servidor remoto (erro JSON-RPC): a conexão funcionou, a chamada não.
type RemoteError struct {
	Code    int64
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("servidor MCP recusou (%d): %s", e.Code, e.Message)
}

// MaxPages limita a paginação do tools/list.
const MaxPages = 20

func (ep Endpoint) timeout() time.Duration {
	if ep.Timeout > 0 {
		return ep.Timeout
	}
	return 60 * time.Second
}

type headerTransport struct {
	headers map[string]string
	next    http.RoundTripper
}

func (h *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return h.next.RoundTrip(r)
}

func (ep Endpoint) httpClient() *http.Client {
	c := *ep.Client
	next := c.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	c.Transport = &headerTransport{headers: ep.Headers, next: next}
	return &c
}

// bounded roda f e desiste no prazo do ctx (o SDK pode demorar a desistir de um servidor que não responde).
func bounded[T any](ctx context.Context, f func() (T, error), discard func(T)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := f()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.err == nil && discard != nil {
				discard(r.v)
			}
		}()
		var zero T
		return zero, ctx.Err()
	}
}

func (ep Endpoint) connect(ctx context.Context) (*sdk.ClientSession, error) {
	if ep.Client == nil {
		return nil, errors.New("mcpupstream: cliente HTTP ausente")
	}
	v := ep.Version
	if v == "" {
		v = "dev"
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "hanko-tool-gateway", Title: "Hanko", Version: v}, nil)
	cs, err := bounded(ctx, func() (*sdk.ClientSession, error) {
		return c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: ep.URL, HTTPClient: ep.httpClient(),
			MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	}, func(cs *sdk.ClientSession) { _ = cs.Close() })
	if err != nil {
		return nil, fmt.Errorf("conectar ao servidor MCP: %w", err)
	}
	return cs, nil
}

func remote(err error) error {
	var je *jsonrpc.Error
	if errors.As(err, &je) {
		return &RemoteError{Code: je.Code, Message: je.Message}
	}
	return err
}

// ListTools descobre as tools do servidor (todas as páginas, até MaxPages).
func ListTools(ctx context.Context, ep Endpoint) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, ep.timeout())
	defer cancel()
	cs, err := ep.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { go cs.Close() }()
	var out []Tool
	cursor := ""
	for page := 0; page < MaxPages; page++ {
		params := &sdk.ListToolsParams{}
		if cursor != "" {
			params.Cursor = cursor
		}
		res, err := bounded(ctx, func() (*sdk.ListToolsResult, error) { return cs.ListTools(ctx, params) }, nil)
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", remote(err))
		}
		for _, t := range res.Tools {
			if t == nil {
				continue
			}
			out = append(out, fromSDK(t))
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return out, nil
}

func fromSDK(t *sdk.Tool) Tool {
	o := Tool{Name: t.Name, Title: t.Title, Description: t.Description}
	if raw, err := json.Marshal(t.InputSchema); err == nil && t.InputSchema != nil {
		o.InputSchema = raw
	}
	if a := t.Annotations; a != nil {
		ro, idem := a.ReadOnlyHint, a.IdempotentHint
		o.HasAnnotations = true
		o.Annotations = domain.ToolAnnotations{Title: a.Title, ReadOnlyHint: &ro, IdempotentHint: &idem,
			DestructiveHint: a.DestructiveHint, OpenWorldHint: a.OpenWorldHint}
		if o.Title == "" {
			o.Title = a.Title
		}
	}
	return o
}

// CallTool repassa uma chamada e devolve o resultado (o CallToolResult do protocolo, em JSON). Uma recusa do
// servidor (erro JSON-RPC) volta como *RemoteError; um resultado com isError=true volta normalmente.
func CallTool(ctx context.Context, ep Endpoint, name string, args json.RawMessage) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, ep.timeout())
	defer cancel()
	cs, err := ep.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { go cs.Close() }()
	var a any = map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		a = args
	}
	res, err := bounded(ctx, func() (*sdk.CallToolResult, error) {
		return cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: a})
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("tools/call: %w", remote(err))
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("tools/call: resultado ilegível: %w", err)
	}
	return raw, nil
}
