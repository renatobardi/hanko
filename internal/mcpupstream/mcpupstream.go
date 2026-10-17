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
