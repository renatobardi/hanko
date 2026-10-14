package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestServeShutdownGracioso(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/lento", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "ok")
	})
	srv := newHTTPServer("127.0.0.1:0", mux, discard())
	if srv.WriteTimeout != 0 || srv.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("timeouts: %+v", srv)
	}

	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, srv, 5*time.Second, discard(), func(a net.Addr) error { addrCh <- a; return nil })
	}()
	addr := <-addrCh

	resp := make(chan string, 1)
	go func() {
		r, err := http.Get("http://" + addr.String() + "/lento")
		if err != nil {
			resp <- err.Error()
			return
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		resp <- string(b)
	}()
	<-started
	cancel() // pede shutdown com request em voo
	select {
	case <-done:
		t.Fatal("serve retornou antes de drenar a requisição em voo")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if got := <-resp; got != "ok" {
		t.Fatalf("resposta = %q", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServePortaOcupada(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := newHTTPServer(ln.Addr().String(), http.NewServeMux(), discard())
	if err := serve(context.Background(), srv, time.Second, discard(), nil); err == nil {
		t.Fatal("esperava erro de bind")
	}
}

func TestSkipTelemetry(t *testing.T) {
	for path, want := range map[string]bool{"/api/v1/x": true, "/metrics": false, "/healthz": false, "/": true} {
		if got := traceRequest(httptest.NewRequest("GET", path, nil)); got != want {
			t.Errorf("%s: %v", path, got)
		}
	}
}
