package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/mcp"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui"
	"github.com/renatobardi/hanko/internal/ui/views"
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
	Memory  memory.Provider // opcional; lê páginas para o diff das propostas de curadoria
	Metrics http.Handler    // opcional
	// JWKSURL substitui a origem das chaves públicas do Firebase (testes).
	JWKSURL string
}

// buildHandler monta o mux real (API, UI, MCP, proxy de memória, métricas) com o
// host check por fora. Separado de Serve para que os testes de wiring suban o mesmo mux.
func buildHandler(d handlerDeps) (http.Handler, error) {
	cfg := d.Cfg
	d.Svc.Admission = auth.Admission{Emails: cfg.Auth.AllowedEmails, Domains: cfg.Auth.AllowedDomains}
	d.Svc.OwnerEmail = cfg.Auth.OwnerEmail
	d.Svc.ExternalIdentity = cfg.Auth.Provider == config.AuthFirebase
	mux := http.NewServeMux()
	api.Mount(mux, api.Deps{Svc: d.Svc, Broker: d.Broker, Logger: d.Logger, Version: d.Version, Tokens: &d.Tokens, DB: d.Pool,
		Authenticated: cfg.Authenticated()})
	ui.Mount(mux, ui.Deps{Svc: d.Svc, Broker: d.Broker, Logger: d.Logger, Version: d.Version, Auth: uiAuth(d), Secure: cfg.SecureCookies(),
		Platform: platformInfo(cfg)})
	mux.Handle("/mcp", mcp.Handler(mcp.Deps{Svc: d.Svc, Tokens: d.Tokens, Logger: d.Logger, Version: d.Version, Trusted: cfg.TrustedMCP(), ArtifactMaxBytes: cfg.MaxArtifactBytes()}))
	if cfg.Memory.Provider == config.MemoryAIMemory {
		mp, err := mcp.MemoryProxy(mcp.MemoryProxyDeps{Backend: d.Svc, Tokens: d.Tokens, URL: cfg.Memory.URL, Token: cfg.Memory.Token,
			Pages: pageReader(d.Memory), Logger: d.Logger})
		if err != nil {
			return nil, fmt.Errorf("app: proxy de memória: %w", err)
		}
		mux.Handle(mcp.MemoryPath, mp)
	}
	if d.Metrics != nil {
		mux.Handle("GET /metrics", d.Metrics)
	}

	var inner http.Handler = mux
	if cfg.Authenticated() {
		inner = auth.Guard(d.Svc, d.Logger)(inner)
	}
	h := redactForTrace(otelhttp.NewHandler(restoreRequest(inner), serviceName,
		otelhttp.WithFilter(traceRequest),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " " + r.URL.Path }),
	))
	return hostHandler(cfg.PublicURL, cfg.AllowedHosts, h), nil
}

// uiAuth monta a configuração de login da UI; nil no modo trusted.
func uiAuth(d handlerDeps) *ui.AuthDeps {
	cfg := d.Cfg
	if !cfg.Authenticated() {
		return nil
	}
	proxies, _ := auth.ParseTrustedProxies(cfg.TrustedProxies) // validado em config.Validate
	a := &ui.AuthDeps{
		Provider:     cfg.Auth.Provider,
		Secure:       cfg.SecureCookies(),
		Admission:    auth.Admission{Emails: cfg.Auth.AllowedEmails, Domains: cfg.Auth.AllowedDomains},
		Proxies:      proxies,
		OwnerEmail:   cfg.Auth.OwnerEmail,
		WebBootstrap: cfg.WebBootstrapAllowed(),
	}
	if cfg.Auth.Provider == config.AuthFirebase {
		fb := cfg.Auth.Firebase
		a.Firebase = views.FirebaseWeb{APIKey: fb.APIKey, AuthDomain: fb.AuthDomain, ProjectID: fb.ProjectID}
		a.Verifier = &auth.FirebaseVerifier{ProjectID: fb.ProjectID, JWKSURL: d.JWKSURL, Log: d.Logger}
	}
	return a
}

type origRequestKey struct{}

// redactForTrace entrega ao otelhttp uma cópia da requisição com o caminho mascarado
// (/convite/<token> → /convite/…): nome do span e atributos de URL nunca veem o token. O
// restoreRequest, por dentro do otelhttp, devolve a requisição original (com o contexto do span)
// ao mux.
func redactForTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if red := auth.RedactPath(r.URL.Path); red != r.URL.Path {
			r2 := r.Clone(context.WithValue(r.Context(), origRequestKey{}, r))
			r2.URL.Path, r2.URL.RawPath, r2.URL.RawQuery, r2.URL.Fragment = red, "", "", ""
			r2.RequestURI = red
			r = r2
		}
		next.ServeHTTP(w, r)
	})
}

func restoreRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if orig, ok := r.Context().Value(origRequestKey{}).(*http.Request); ok {
			r = orig.WithContext(r.Context())
		}
		next.ServeHTTP(w, r)
	})
}

// pageReader devolve o provedor de memória como leitor de páginas, quando ele sabe ler (senão nil).
func pageReader(p memory.Provider) memory.PageReader {
	pr, _ := p.(memory.PageReader)
	return pr
}

// platformInfo é o retrato da instalação para Configurações › Ambientes, Adapters e Plugins (sem segredos).
func platformInfo(cfg config.Config) ui.PlatformInfo {
	return ui.PlatformInfo{Mode: cfg.Mode, AuthProvider: cfg.Auth.Provider, PublicURL: cfg.PublicURL, AgentURL: cfg.AgentURL,
		ClaudeBin: cfg.ClaudeBin, CodexBin: cfg.CodexBin, ACPAllow: cfg.ACP.Allow, ACPCommands: cfg.ACP.Commands,
		MemoryProvider: cfg.Memory.Provider, MemoryURL: cfg.Memory.URL, OTelEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		IntegrationsAllowStdio: cfg.Integrations.AllowStdio, IntegrationsAllowPrivate: cfg.Integrations.AllowPrivateDestinations,
		SecretKeySet: cfg.SecretKey != "", VaultConfigured: cfg.Vault.Addr != "", Workers: cfg.Workers, RunTimeout: cfg.RunTimeout, HeartbeatInterval: cfg.HeartbeatInterval}
}
