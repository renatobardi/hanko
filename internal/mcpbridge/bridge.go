// Package mcpbridge liga um cliente MCP local (stdio) ao servidor MCP remoto do Hanko (streamable HTTP):
// `hanko mcp`. É uma ponte de mensagens JSON-RPC, sem interpretar o protocolo: o que chega de um lado sai
// do outro, então tools novas do servidor aparecem sem mudar o binário.
package mcpbridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RemoteTransport monta o transporte HTTP até o endpoint MCP remoto, com o token no Authorization.
// hc nil usa o cliente padrão.
func RemoteTransport(endpoint, token string, hc *http.Client) *sdk.StreamableClientTransport {
	base := http.DefaultTransport
	if hc != nil && hc.Transport != nil {
		base = hc.Transport
	}
	c := &http.Client{Transport: bearerTransport{base: base, token: token}}
	if hc != nil {
		c.Timeout, c.CheckRedirect, c.Jar = hc.Timeout, hc.CheckRedirect, hc.Jar
	}
	return &sdk.StreamableClientTransport{Endpoint: endpoint, HTTPClient: c}
}

// Endpoint devolve a URL do MCP a partir da URL base do servidor.
func Endpoint(base string) string { return strings.TrimRight(base, "/") + "/mcp" }

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.token == "" || r.Header.Get("Authorization") != "" {
		return b.base.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// pendentes guarda os ids das requisições já enviadas ao servidor e ainda sem resposta.
type pendentes struct {
	mu   sync.Mutex
	ids  map[jsonrpc.ID]struct{}
	idle chan struct{} // fechado quando fecha o fluxo local e não resta resposta a esperar
	done bool
}

func (p *pendentes) add(id jsonrpc.ID) {
	p.mu.Lock()
	p.ids[id] = struct{}{}
	p.mu.Unlock()
}

func (p *pendentes) del(id jsonrpc.ID) {
	p.mu.Lock()
	delete(p.ids, id)
	p.release()
	p.mu.Unlock()
}

// finish marca que o lado local não manda mais nada; devolve o canal que fecha quando as respostas chegarem.
func (p *pendentes) finish() <-chan struct{} {
	p.mu.Lock()
	p.done = true
	p.release()
	p.mu.Unlock()
	return p.idle
}

func (p *pendentes) release() { // com p.mu
	if p.done && len(p.ids) == 0 {
		select {
		case <-p.idle:
		default:
			close(p.idle)
		}
	}
}

// Run conecta os dois lados e repassa mensagens nos dois sentidos até um deles encerrar (servidor fechar a
// sessão, erro de transporte ou ctx cancelado). Se o lado local acaba (EOF do stdin, como em `echo '{...}' |
// hanko mcp`), espera as respostas das requisições já enviadas antes de fechar. O fim normal volta nil.
func Run(ctx context.Context, local, remote sdk.Transport) error {
	lc, err := local.Connect(ctx)
	if err != nil {
		return err
	}
	rc, err := remote.Connect(ctx)
	if err != nil {
		_ = lc.Close()
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			cancel()
			_ = lc.Close()
			_ = rc.Close()
		})
	}
	pend := &pendentes{ids: map[jsonrpc.ID]struct{}{}, idle: make(chan struct{})}
	errs := make(chan error, 2)

	go func() { // local -> remoto
		for {
			msg, err := lc.Read(ctx)
			if err == nil {
				if req, ok := msg.(*jsonrpc.Request); ok && req.ID.IsValid() {
					pend.add(req.ID)
				}
				err = rc.Write(ctx, msg)
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					select { // stdin acabou: deixa as respostas pendentes chegarem
					case <-pend.finish():
					case <-ctx.Done():
					}
				}
				errs <- err
				closeAll()
				return
			}
		}
	}()
	go func() { // remoto -> local
		for {
			msg, err := rc.Read(ctx)
			if err == nil {
				err = lc.Write(ctx, msg)
				if resp, ok := msg.(*jsonrpc.Response); ok {
					pend.del(resp.ID)
				}
			}
			if err != nil {
				errs <- err
				closeAll()
				return
			}
		}
	}()
	first := <-errs
	second := <-errs // o outro lado sai porque closeAll destrava o Read; o erro dele é consequência do primeiro
	_ = second
	if errors.Is(first, io.EOF) || errors.Is(first, context.Canceled) || errors.Is(first, sdk.ErrConnectionClosed) {
		return nil
	}
	return first
}
