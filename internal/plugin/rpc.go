// Package plugin roda os plugins de terceiros (ADR-0038): um processo externo por plugin, falando JSON-RPC 2.0
// por stdin/stdout, uma mensagem por linha. O pacote só cuida do processo e do protocolo; o que o plugin pode
// fazer no Hanko (capacidades, empresa ligada) é decidido pelo Handler que o service fornece.
package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// Códigos de erro JSON-RPC. Os negativos padrão e os do host (-32001…) que o plugin pode tratar.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// CodeDenied: a capacidade não foi concedida, ou o plugin não está ligado na empresa.
	CodeDenied = -32001
	// CodeBusy: chamadas demais ao mesmo tempo ou por minuto.
	CodeBusy = -32002
	// CodeNotFound: o registro pedido não existe na empresa.
	CodeNotFound = -32004
	// CodeInvalid: dados recusados pela validação do Hanko.
	CodeInvalid = -32005
)

// DefaultMaxMessage é o maior tamanho de uma mensagem, em cada direção.
const DefaultMaxMessage = 1 << 20

// RPCError é o objeto de erro do JSON-RPC.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("plugin rpc %d: %s", e.Code, e.Message) }

// Errorf monta um RPCError.
func Errorf(code int, format string, a ...any) *RPCError {
	return &RPCError{Code: code, Message: fmt.Sprintf(format, a...)}
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// handlerFunc atende uma requisição (ou notificação) do plugin.
type handlerFunc func(ctx context.Context, method string, params json.RawMessage) (any, *RPCError)

var (
	errClosed   = errors.New("plugin: conexão encerrada")
	errTooLarge = errors.New("plugin: mensagem maior que o limite")
	errQueue    = errors.New("plugin: fila de envio cheia (o plugin não está lendo o stdin)")
)

// conn é um par JSON-RPC sobre as pipes do processo. A escrita é feita por uma goroutine com fila limitada, para
// que um plugin que não lê o stdin nunca trave quem chama; as requisições do plugin são atendidas em goroutines,
// com teto de simultâneas, para que uma chamada lenta ao banco não segure as respostas que chegam atrás dela.
type conn struct {
	max    int
	handle handlerFunc
	bad    func(reason string)
	ctx    context.Context // cancelado quando a conexão morre
	cancel context.CancelFunc

	out    chan []byte
	sem    chan struct{}
	wg     sync.WaitGroup
	wDone  chan struct{}
	closer sync.Once

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan wireMessage
	err     error
	done    chan struct{}
}

func newConn(w io.WriteCloser, max, inflight int, h handlerFunc, bad func(string)) *conn {
	if max <= 0 {
		max = DefaultMaxMessage
	}
	if inflight <= 0 {
		inflight = 8
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{max: max, handle: h, bad: bad, ctx: ctx, cancel: cancel, out: make(chan []byte, 256),
		sem: make(chan struct{}, inflight), wDone: make(chan struct{}), pending: map[string]chan wireMessage{}, done: make(chan struct{})}
	go c.writeLoop(w)
	return c
}

func (c *conn) writeLoop(w io.WriteCloser) {
	defer close(c.wDone)
	defer func() { _ = w.Close() }()
	for {
		select {
		case b := <-c.out:
			if _, err := w.Write(b); err != nil {
				c.close(fmt.Errorf("plugin: escrever no stdin: %w", err))
				return
			}
		case <-c.done:
			return
		}
	}
}

// readLoop lê as linhas do stdout do plugin até EOF. Linhas maiores que o limite são descartadas inteiras.
func (c *conn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	var line []byte
	over := false
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 && !over {
			if len(line)+len(chunk) > c.max+1 {
				over = true
				line = nil
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err != nil:
			if len(line) > 0 && !over {
				c.dispatch(line)
			}
			return
		}
		if over {
			c.bad(fmt.Sprintf("mensagem maior que %d bytes descartada", c.max))
		} else {
			c.dispatch(line)
		}
		line, over = line[:0:0], false
	}
}

func (c *conn) dispatch(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var m wireMessage
	if line[0] != '{' || json.Unmarshal(line, &m) != nil || m.JSONRPC != "2.0" {
		c.bad("linha que não é JSON-RPC 2.0 ignorada")
		return
	}
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch {
	case m.Method != "":
		select {
		case c.sem <- struct{}{}:
		default:
			if hasID {
				c.reply(m.ID, nil, Errorf(CodeBusy, "chamadas simultâneas demais"))
			}
			return
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			defer func() { <-c.sem }()
			res, rerr := c.handle(c.ctx, m.Method, m.Params)
			if hasID {
				c.reply(m.ID, res, rerr)
			}
		}()
	case hasID:
		key := string(bytes.Trim(m.ID, `"`))
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	default:
		c.bad("mensagem JSON-RPC sem método nem id ignorada")
	}
}

func (c *conn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("plugin: codificar mensagem: %w", err)
	}
	if len(b) > c.max {
		return errTooLarge
	}
	b = append(b, '\n')
	select {
	case <-c.done:
		return c.Err()
	default:
	}
	select {
	case c.out <- b:
		return nil
	case <-c.done:
		return c.Err()
	default:
		return errQueue
	}
}

func (c *conn) reply(id json.RawMessage, result any, rerr *RPCError) {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		m["error"] = rerr
	} else {
		if result == nil {
			result = struct{}{}
		}
		m["result"] = result
	}
	if err := c.send(m); errors.Is(err, errTooLarge) {
		_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": Errorf(CodeInternalError, "resposta maior que o limite de mensagem")})
	}
}

// Call manda uma requisição e espera a resposta até ctx acabar. Um erro do plugin volta como *RPCError.
func (c *conn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	c.nextID++
	key := strconv.FormatInt(c.nextID, 10)
	ch := make(chan wireMessage, 1)
	c.pending[key] = ch
	id := c.nextID
	c.mu.Unlock()
	drop := func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}
	if params == nil {
		params = struct{}{}
	}
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		drop()
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-c.done:
		drop()
		return nil, c.Err()
	}
}

// Notify manda uma notificação (sem resposta).
func (c *conn) Notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// Err é o motivo do fim da conexão (nil enquanto viva).
func (c *conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// close encerra a conexão: chamadas pendentes falham com cause, as requisições em andamento recebem o contexto
// cancelado. Idempotente.
func (c *conn) close(cause error) {
	c.closer.Do(func() {
		if cause == nil {
			cause = errClosed
		}
		c.mu.Lock()
		c.err = cause
		close(c.done)
		c.mu.Unlock()
		c.cancel()
	})
}

// wait espera as requisições do plugin em andamento terminarem e o escritor parar.
func (c *conn) wait() {
	c.wg.Wait()
	<-c.wDone
}
