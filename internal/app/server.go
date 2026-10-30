// Package app monta o processo Hanko: telemetria, banco, migrations, heartbeat,
// adapters, memória e o servidor HTTP. Este arquivo contém as partes sem
// dependência de outras trilhas (servidor HTTP, shutdown, filtros OTel).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	// ShutdownGrace é o tempo máximo do shutdown gracioso.
	ShutdownGrace = 30 * time.Second

	readHeaderTimeout = 10 * time.Second
	// ingestReadTimeout cobre cabeçalho e corpo do listener de ingestão (ADR-0076); o público não tem.
	ingestReadTimeout = 2 * time.Minute
	idleTimeout       = 120 * time.Second
)

// newHTTPServer cria o servidor sem WriteTimeout global (SSE precisa de respostas longas).
func newHTTPServer(addr string, h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

// serve escuta em srv.Addr e bloqueia até ctx ser cancelado (ou o servidor falhar),
// quando faz shutdown gracioso limitado por grace. onListening (opcional) roda assim
// que o socket está aberto.
func serve(ctx context.Context, srv *http.Server, grace time.Duration, logger *slog.Logger, onListening func(net.Addr) error) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("app: escutar em %s: %w", srv.Addr, err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	logger.Info("servidor HTTP no ar", "addr", ln.Addr().String())
	if onListening != nil {
		if err := onListening(ln.Addr()); err != nil {
			_ = srv.Close()
			return err
		}
	}

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("app: servidor HTTP: %w", err)
	case <-ctx.Done():
	}

	logger.Info("encerrando servidor HTTP", "grace", grace.String())
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("app: shutdown HTTP: %w", err)
	}
	return nil
}

// traceRequest diz se a rota não deve gerar span (ruído de probes e scrapes).
func traceRequest(r *http.Request) bool {
	switch r.URL.Path {
	case "/healthz", "/readyz", "/metrics":
		return false
	}
	return true
}
