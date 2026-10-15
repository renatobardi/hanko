package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type testHandler struct {
	mu    sync.Mutex
	notes []string
	bad   []string
	req   func(method string, params json.RawMessage) (any, *rpcError)
}

func (h *testHandler) HandleRequest(method string, params json.RawMessage) (any, *rpcError) {
	if h.req != nil {
		return h.req(method, params)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "no such method"}
}

func (h *testHandler) HandleNotification(method string, params json.RawMessage) {
	h.mu.Lock()
	h.notes = append(h.notes, method+" "+string(params))
	h.mu.Unlock()
}

func (h *testHandler) BadLine(line []byte, reason string) {
	h.mu.Lock()
	h.bad = append(h.bad, reason+": "+string(line))
	h.mu.Unlock()
}

func (h *testHandler) notesCopy() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.notes...)
}

// pair wires two conns back to back through in-memory pipes.
func pair(t *testing.T, ha, hb handler) (a, b *conn) {
	t.Helper()
	prAB, pwAB := io.Pipe() // a writes, b reads
	prBA, pwBA := io.Pipe()
	a = newConn(pwAB, ha)
	b = newConn(pwBA, hb)
	pump := func(r io.Reader, to *conn) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
		for sc.Scan() {
			to.Feed(sc.Bytes())
		}
	}
	go pump(prAB, b)
	go pump(prBA, a)
	t.Cleanup(func() { a.Close(nil); b.Close(nil) })
	return a, b
}

func TestJSONRPCBothDirections(t *testing.T) {
	ha := &testHandler{req: func(m string, p json.RawMessage) (any, *rpcError) {
		if m == "client/echo" {
			return map[string]string{"echo": string(p)}, nil
		}
		return nil, &rpcError{Code: codeMethodNotFound, Message: "no such method: " + m}
	}}
	hb := &testHandler{}
	a, b := pair(t, ha, hb)
	hb.req = func(m string, p json.RawMessage) (any, *rpcError) {
		switch m {
		case "agent/ping":
			return map[string]any{"pong": json.RawMessage(p)}, nil
		case "agent/bad":
			return nil, &rpcError{Code: codeInvalidParams, Message: "nope", Data: json.RawMessage(`{"x":1}`)}
		case "agent/null":
			return nil, nil
		}
		return nil, &rpcError{Code: codeMethodNotFound, Message: "no such method"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	raw, err := a.Call(ctx, "agent/ping", map[string]int{"n": 1})
	if err != nil || string(raw) != `{"pong":{"n":1}}` {
		t.Fatalf("ping: %s %v", raw, err)
	}
	// the other direction: the agent side calls a method served by the client side
	raw, err = b.Call(ctx, "client/echo", map[string]int{"n": 2})
	if err != nil || string(raw) != `{"echo":"{\"n\":2}"}` {
		t.Fatalf("echo: %s %v", raw, err)
	}

	_, err = a.Call(ctx, "agent/bad", nil)
	var re *rpcError
	if !errors.As(err, &re) || re.Code != codeInvalidParams || string(re.Data) != `{"x":1}` {
		t.Fatalf("bad: %v", err)
	}

	_, err = a.Call(ctx, "agent/unknown", nil)
	if !errors.As(err, &re) || re.Code != codeMethodNotFound {
		t.Fatalf("unknown method must answer -32601, got %v", err)
	}

	raw, err = a.Call(ctx, "agent/null", nil)
	if err != nil || string(raw) != `{}` {
		t.Fatalf("empty result must be {}: %s %v", raw, err)
	}

	if err := a.Notify("agent/note", map[string]int{"k": 2}); err != nil {
		t.Fatal(err)
	}
	// a request after the notification proves the notification was dispatched before it
	if _, err := a.Call(ctx, "agent/null", nil); err != nil {
		t.Fatal(err)
	}
	if got := hb.notesCopy(); len(got) != 1 || got[0] != `agent/note {"k":2}` {
		t.Fatalf("notes=%v", got)
	}
}

func TestJSONRPCNotificationsGetNoResponse(t *testing.T) {
	var out strings.Builder
	var mu sync.Mutex
	w := writeCloser{func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return out.Write(p) }}
	h := &testHandler{}
	c := newConn(w, h)
	c.Feed([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"a":1}}`))
	c.Feed([]byte(`{"jsonrpc":"2.0","id":7,"method":"x/y"}`))
	c.Feed([]byte(`{"jsonrpc":"2.0","id":"s-1","method":"x/y"}`))
	c.Close(nil)
	mu.Lock()
	defer mu.Unlock()
	want := `{"error":{"code":-32601,"message":"no such method"},"id":7,"jsonrpc":"2.0"}` + "\n" +
		`{"error":{"code":-32601,"message":"no such method"},"id":"s-1","jsonrpc":"2.0"}` + "\n"
	if out.String() != want {
		t.Fatalf("out=%q\nwant %q", out.String(), want)
	}
	if len(h.notesCopy()) != 1 {
		t.Fatalf("notes=%v", h.notesCopy())
	}
}

type writeCloser struct{ w func([]byte) (int, error) }

func (w writeCloser) Write(p []byte) (int, error) { return w.w(p) }
func (w writeCloser) Close() error                { return nil }

func TestJSONRPCInvalidAndOversizedLines(t *testing.T) {
	h := &testHandler{}
	c := newConn(writeCloser{func(p []byte) (int, error) { return len(p), nil }}, h)
	c.Feed([]byte("plain text"))
	c.Feed([]byte(`{"jsonrpc":"2.0"}`))
	c.Feed([]byte(`   `))
	c.Feed([]byte(`{"broken`))
	c.Feed([]byte(`{"jsonrpc":"2.0","id":99,"result":{}}`)) // response nobody waits for: ignored
	c.Feed([]byte(strings.Repeat("x", MaxLineBytes)))
	c.Close(nil)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.bad) != 4 {
		t.Fatalf("bad=%d: %v", len(h.bad), h.bad)
	}
	if !strings.Contains(h.bad[3], "exceeds the") || len(h.bad[3]) > 400 {
		t.Fatalf("oversized line not reported/truncated: %.100s", h.bad[3])
	}
}

func TestJSONRPCCallEnds(t *testing.T) {
	c := newConn(writeCloser{func(p []byte) (int, error) { return len(p), nil }}, &testHandler{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, "never/answered", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	cause := errors.New("process gone")
	done := make(chan error, 1)
	go func() {
		_, err := c.Call(context.Background(), "never/answered", nil)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	c.Close(cause)
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call not released on close")
	}
	if _, err := c.Call(context.Background(), "x", nil); !errors.Is(err, cause) {
		t.Fatalf("call after close: %v", err)
	}
}

func TestJSONRPCResponseBeforeCloseIsDelivered(t *testing.T) {
	// The process writes its answer and exits at once: the answer must win over the close.
	for i := 0; i < 50; i++ {
		c := newConn(writeCloser{func(p []byte) (int, error) { return len(p), nil }}, &testHandler{})
		wait, err := c.CallAsync("x", nil)
		if err != nil {
			t.Fatal(err)
		}
		c.Feed([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
		go c.Close(errors.New("exited"))
		raw, err := wait(context.Background())
		if err != nil || string(raw) != `{"ok":true}` {
			t.Fatalf("iteration %d: %s %v", i, raw, err)
		}
	}
}
