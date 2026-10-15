package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// JSON-RPC 2.0 over newline-delimited stdio, in both directions. Standard error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// MaxLineBytes is the largest incoming line processed whole. Longer lines are dropped (the
// process runner truncates them at this size) and reported as invalid.
const MaxLineBytes = 16 << 20

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

// wireMessage is any JSON-RPC message; which one it is depends on the fields present.
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// handler serves what the peer sends. HandleRequest's result is marshaled into the response;
// a nil *rpcError means success. Methods the handler does not know must return codeMethodNotFound.
type handler interface {
	HandleRequest(method string, params json.RawMessage) (any, *rpcError)
	HandleNotification(method string, params json.RawMessage)
	// BadLine receives a line that is not a JSON-RPC message (or is oversized).
	BadLine(line []byte, reason string)
}

var errConnClosed = errors.New("connection closed")

// conn is a JSON-RPC peer over a line transport. Incoming lines are queued by Feed (which never
// blocks, so it is safe to call from a process output callback) and dispatched in order by one
// goroutine; outgoing messages are queued and written by another, so a handler can never
// deadlock against an agent that is not reading its stdin while it writes to stdout.
type conn struct {
	h handler

	in  *queue[[]byte]
	out *queue[[]byte]
	w   io.WriteCloser

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan wireMessage
	failErr error // set once the connection is dead
	done    chan struct{}
	readWG  sync.WaitGroup
	writeWG sync.WaitGroup
}

func newConn(w io.WriteCloser, h handler) *conn {
	c := &conn{h: h, w: w, in: newQueue[[]byte](), out: newQueue[[]byte](), pending: map[string]chan wireMessage{}, done: make(chan struct{})}
	c.readWG.Add(1)
	c.writeWG.Add(1)
	go c.readLoop()
	go c.writeLoop()
	return c
}

// Feed hands one received line (without the newline) to the dispatcher.
func (c *conn) Feed(line []byte) {
	c.in.push(append([]byte(nil), line...))
}

// Close ends the connection: pending calls fail with cause, the dispatcher drains what was fed,
// and the writer flushes what was queued before closing the pipe. It is idempotent.
func (c *conn) Close(cause error) {
	if cause == nil {
		cause = errConnClosed
	}
	// Drain first: lines the process wrote right before exiting (the last update, the answer
	// to the prompt) must reach their waiters before the connection is declared dead.
	c.in.close()
	c.readWG.Wait()
	c.mu.Lock()
	if c.failErr == nil {
		c.failErr = cause
		close(c.done)
	}
	c.mu.Unlock()
	c.out.close()
	c.writeWG.Wait()
	// Anything still waiting for an answer will never get one.
	c.mu.Lock()
	for k, ch := range c.pending {
		close(ch)
		delete(c.pending, k)
	}
	c.mu.Unlock()
}

// Err is why the connection died (nil while alive).
func (c *conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failErr
}

// Done is closed once Close was called.
func (c *conn) Done() <-chan struct{} { return c.done }

// Call sends a request and waits for its response. A JSON-RPC error comes back as *rpcError.
func (c *conn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	wait, err := c.CallAsync(method, params)
	if err != nil {
		return nil, err
	}
	return wait(ctx)
}

// CallAsync queues the request (so it is on the wire before anything sent afterwards) and returns
// the function that waits for the response.
func (c *conn) CallAsync(method string, params any) (func(context.Context) (json.RawMessage, error), error) {
	c.mu.Lock()
	if c.failErr != nil {
		err := c.failErr
		c.mu.Unlock()
		return nil, err
	}
	c.nextID++
	id := c.nextID
	key := fmt.Sprint(id)
	ch := make(chan wireMessage, 1)
	c.pending[key] = ch
	c.mu.Unlock()

	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.dropPending(key)
		return nil, err
	}
	return func(ctx context.Context) (json.RawMessage, error) {
		select {
		case m, ok := <-ch:
			return c.answer(m, ok)
		case <-ctx.Done():
			c.dropPending(key)
			return nil, ctx.Err()
		case <-c.done:
			select {
			case m, ok := <-ch:
				if ok {
					return c.answer(m, true)
				}
			default:
			}
			c.dropPending(key)
			return nil, c.Err()
		}
	}, nil
}

func (c *conn) answer(m wireMessage, ok bool) (json.RawMessage, error) {
	if !ok {
		if err := c.Err(); err != nil {
			return nil, err
		}
		return nil, errConnClosed
	}
	if m.Error != nil {
		return nil, m.Error
	}
	return m.Result, nil
}

// Notify sends a notification (no id, no response).
func (c *conn) Notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *conn) dropPending(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

func (c *conn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("acp: encode message: %w", err)
	}
	if !c.out.push(append(b, '\n')) {
		if e := c.Err(); e != nil {
			return e
		}
		return errConnClosed
	}
	return nil
}

func (c *conn) reply(id json.RawMessage, result any, rerr *rpcError) {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		m["error"] = rerr
	} else {
		if result == nil {
			result = struct{}{}
		}
		m["result"] = result
	}
	_ = c.send(m) // a dead connection has nobody to answer
}

func (c *conn) writeLoop() {
	defer c.writeWG.Done()
	defer func() { _ = c.w.Close() }()
	for {
		b, ok := c.out.pop()
		if !ok {
			return
		}
		if _, err := c.w.Write(b); err != nil {
			// The agent closed its stdin: nothing more can be sent. Pending calls end when the
			// process exits or on their own deadline; here we only stop writing.
			c.out.close()
			return
		}
	}
}

func (c *conn) readLoop() {
	defer c.readWG.Done()
	for {
		line, ok := c.in.pop()
		if !ok {
			return
		}
		c.dispatch(line)
	}
}

func (c *conn) dispatch(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	if len(line) >= MaxLineBytes {
		c.h.BadLine(line[:256], fmt.Sprintf("line exceeds the %d byte limit", MaxLineBytes))
		return
	}
	var m wireMessage
	if line[0] != '{' || json.Unmarshal(line, &m) != nil {
		c.h.BadLine(line, "not a JSON object")
		return
	}
	hasID := len(m.ID) > 0
	switch {
	case m.Method != "" && hasID:
		res, rerr := c.h.HandleRequest(m.Method, m.Params)
		c.reply(m.ID, res, rerr)
	case m.Method != "":
		c.h.HandleNotification(m.Method, m.Params)
	case hasID && (m.Error != nil || len(m.Result) > 0):
		key := strings.Trim(string(m.ID), `"`)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	default:
		c.h.BadLine(line, "not a JSON-RPC message")
	}
}

// queue is an unbounded FIFO with blocking pop.
type queue[T any] struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []T
	closed bool
}

func newQueue[T any]() *queue[T] {
	q := &queue[T]{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends; false if the queue is closed.
func (q *queue[T]) push(v T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.items = append(q.items, v)
	q.cond.Signal()
	return true
}

// pop blocks for the next item; false once the queue is closed and drained.
func (q *queue[T]) pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	var zero T
	if len(q.items) == 0 {
		return zero, false
	}
	v := q.items[0]
	q.items[0] = zero
	q.items = q.items[1:]
	return v, true
}

func (q *queue[T]) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}
