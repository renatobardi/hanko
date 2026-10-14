package app

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/mcp"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui"
)

// handlerDeps são as peças já construídas que o mux de produção precisa.
type handlerDeps struct {
	Cfg     config.Config
	Version string
	Logger  *slog.Logger
	Svc     *service.Service
	Broker  *events.Broker
	Pool    *pgxpool.Pool
	Tokens  auth.RunTokens
	Metrics http.Handler // opcional
}

// buildHandler monta o mux real (API, UI, MCP, proxy de memória, métricas) com o
// host check por fora. Separado de Serve para que os testes de wiring suban o mesmo mux.
func buildHandler(d handlerDeps) (http.Handler, error) {
	cfg := d.Cfg
	mux := http.NewServeMux()
	api.Mount(mux, api.Deps{Svc: d.Svc, Broker: d.Broker, Logger: d.Logger, Version: d.Version, Tokens: &d.Tokens, DB: d.Pool})
	ui.Mount(mux, ui.Deps{Svc: d.Svc, Broker: d.Broker, Logger: d.Logger, Version: d.Version})
	mux.Handle("/mcp", mcp.Handler(mcp.Deps{Svc: d.Svc, Tokens: d.Tokens, Logger: d.Logger, Version: d.Version, Trusted: cfg.TrustedMCP()}))
	if cfg.Memory.Provider == config.MemoryAIMemory {
		mp, err := mcp.MemoryProxy(mcp.MemoryProxyDeps{Backend: d.Svc, Tokens: d.Tokens, URL: cfg.Memory.URL, Token: cfg.Memory.Token, Logger: d.Logger})
		if err != nil {
			return nil, fmt.Errorf("app: proxy de memória: %w", err)
		}
		mux.Handle(mcp.MemoryPath, mp)
	}
	if d.Metrics != nil {
		mux.Handle("GET /metrics", d.Metrics)
	}

	var h http.Handler = otelhttp.NewHandler(mux, serviceName,
		otelhttp.WithFilter(traceRequest),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " " + r.URL.Path }),
	)
	return hostHandler(cfg.PublicURL, cfg.AllowedHosts, h), nil
}
