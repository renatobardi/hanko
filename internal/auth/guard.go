package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
)

// Resolver resolve credenciais de usuário. Implementado por *service.Service.
// ErrUnauthenticated (domain) = credencial desconhecida, expirada ou revogada.
type Resolver interface {
	ResolveSession(ctx context.Context, token string) (domain.User, error)
	ResolveAPIKey(ctx context.Context, key string) (domain.User, error)
}

// KeyInfo descreve a API key que autenticou a requisição.
type KeyInfo struct {
	ID    domain.ID
	Scope domain.KeyScope
}

// KeyResolver é um Resolver que também informa o id e o escopo da API key (ADR-0057). Quando o Resolver não o
// implementa, a chave vale com escopo full.
type KeyResolver interface {
	ResolveAPIKeyInfo(ctx context.Context, key string) (domain.User, KeyInfo, error)
}

// LoginPath é a tela de entrada.
const LoginPath = "/entrar"

// InvitePath é o prefixo público de aceite de convite (/convite/{token}); a própria página decide o que exigir.
const InvitePath = "/convite/"

// CLIAuthPath é o prefixo da página de autorização do login da CLI (/cli-auth/<código>); exige sessão.
const CLIAuthPath = "/cli-auth/"

// JoinPath é o prefixo público do link de pedido de entrada (/pedir-acesso/<token>); a página exige login.
const JoinPath = "/pedir-acesso/"

// CLIAuthAPIPrefix são os endpoints públicos do login da CLI (criação e consulta); quem chama ainda não tem credencial.
const CLIAuthAPIPrefix = "/api/v1/cli-auth"

// RedactPath esconde o segredo que viaja no caminho: /convite/<token> vira /convite/…, e o mesmo para o código do
// login da CLI e o token do link de entrada. Use em qualquer log, nome de span ou atributo que carregue o caminho
// da requisição.
func RedactPath(path string) string {
	for _, p := range []string{InvitePath, CLIAuthPath, JoinPath} {
		if strings.HasPrefix(path, p) {
			return p + "…"
		}
	}
	return path
}

// isPWAPath: manifest, service worker e página offline são públicos (o navegador os busca sem cookie
// e o SW precisa deles antes de qualquer login). Não carregam dado algum.
func isPWAPath(p string) bool {
	return p == "/manifest.webmanifest" || p == "/sw.js" || p == "/offline"
}

// Guard é o middleware do modo authenticated. Resolve a identidade (cookie de sessão ou
// API key) e aplica a regra por rota:
//
//	livres:      /healthz, /readyz, /static/, /manifest.webmanifest, /sw.js, /offline (PWA), /entrar (e /entrar/…), /sair, /idioma, /auth/, /convite/,
//	             /pedir-acesso/ (a página exige login) e os endpoints públicos do login da CLI (/api/v1/cli-auth[/poll])
//	/metrics:    loopback direto ou API key
//	/api/…:      sessão (CSRF same-origin em métodos que mudam estado), API key ou run token
//	/mcp, /memory-mcp: API key ou run token (run token é validado pelo handler)
//	demais (UI): sessão; senão redireciona para /entrar
func Guard(res Resolver, log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	g := &guard{res: res, log: log}
	return g.wrap
}

type guard struct {
	res Resolver
	log *slog.Logger
}

func (g *guard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/healthz" || path == "/readyz" || strings.HasPrefix(path, "/static/") || isPWAPath(path):
			next.ServeHTTP(w, r)
			return
		}

		bearer, hasBearer := BearerToken(r.Header.Get("Authorization"))
		hasAuthz := r.Header.Get("Authorization") != ""

		// 1. API key (Bearer hk_…).
		var id *Identity
		if hasBearer && IsAPIKey(bearer) {
			u, info, err := g.resolveKey(r.Context(), bearer)
			switch {
			case err == nil:
				id = &Identity{User: u, Via: ViaAPIKey, KeyID: info.id, KeyScope: info.scope}
			case errors.Is(err, domain.ErrUnauthenticated):
				g.deny(w, r, "API key inválida ou revogada")
				return
			default:
				g.unavailable(w, r, err)
				return
			}
		}

		// 2. Sessão por cookie (só quando não há Authorization).
		if id == nil && !hasAuthz {
			if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
				u, err := g.res.ResolveSession(r.Context(), c.Value)
				switch {
				case err == nil:
					id = &Identity{User: u, Via: ViaSession}
				case errors.Is(err, domain.ErrUnauthenticated):
					// cookie velho: trata como anônimo
				default:
					g.unavailable(w, r, err)
					return
				}
			}
		}
		if id != nil {
			if id.Via == ViaAPIKey && !id.KeyScope.Allows(r.Method, path) {
				g.deny(w, r, "a API key é só de leitura")
				return
			}
			r = r.WithContext(WithIdentity(r.Context(), *id))
		}

		switch {
		case path == "/metrics":
			if id != nil && id.Via == ViaAPIKey || IsDirectLoopback(r) {
				next.ServeHTTP(w, r)
				return
			}
			g.deny(w, r, "autenticação necessária")
		case path == LoginPath || strings.HasPrefix(path, LoginPath+"/") || path == "/sair" || path == "/idioma" || strings.HasPrefix(path, "/auth/") || strings.HasPrefix(path, InvitePath) || strings.HasPrefix(path, JoinPath):
			next.ServeHTTP(w, r)
		case path == CLIAuthAPIPrefix || path == CLIAuthAPIPrefix+"/poll":
			// criação e consulta do login da CLI: públicas (o segredo é o código/poll token); ficam atrás do limitador
			next.ServeHTTP(w, r)
		case strings.HasPrefix(path, "/api/"):
			switch {
			case id != nil && id.Via == ViaSession:
				if !safeMethod(r.Method) && !SameOrigin(r) {
					writeProblem(w, http.StatusForbidden, "requisição de origem diferente bloqueada")
					return
				}
				next.ServeHTTP(w, r)
			case id != nil, hasBearer && !IsAPIKey(bearer): // API key válida, ou run token (validado pela API)
				next.ServeHTTP(w, r)
			default:
				g.deny(w, r, "autenticação necessária")
			}
		case path == "/mcp" || strings.HasPrefix(path, "/mcp/") || path == "/memory-mcp" || strings.HasPrefix(path, "/memory-mcp/"):
			if id != nil && id.Via == ViaAPIKey || hasBearer && !IsAPIKey(bearer) {
				next.ServeHTTP(w, r)
				return
			}
			g.deny(w, r, "autenticação necessária")
		default:
			if id != nil && id.Via == ViaSession {
				next.ServeHTTP(w, r)
				return
			}
			g.redirectToLogin(w, r)
		}
	})
}

type keyMeta struct {
	id    *domain.ID
	scope domain.KeyScope
}

// resolveKey resolve a API key com id e escopo quando o Resolver sabe (senão full).
func (g *guard) resolveKey(ctx context.Context, key string) (domain.User, keyMeta, error) {
	if kr, ok := g.res.(KeyResolver); ok {
		u, info, err := kr.ResolveAPIKeyInfo(ctx, key)
		if err != nil {
			return domain.User{}, keyMeta{}, err
		}
		kid := info.ID
		return u, keyMeta{id: &kid, scope: info.Scope}, nil
	}
	u, err := g.res.ResolveAPIKey(ctx, key)
	return u, keyMeta{scope: domain.KeyScopeFull}, err
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func (g *guard) deny(w http.ResponseWriter, r *http.Request, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="hanko"`)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeProblem(w, http.StatusUnauthorized, msg)
		return
	}
	http.Error(w, "unauthorized: "+msg, http.StatusUnauthorized)
}

func (g *guard) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	g.log.ErrorContext(r.Context(), "auth: resolver credencial falhou", "err", err)
	http.Error(w, "autenticação indisponível", http.StatusServiceUnavailable)
}

func (g *guard) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	to := LoginPath
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.RequestURI() != "/" {
		to += "?next=" + url.QueryEscape(r.URL.RequestURI())
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]any{"title": http.StatusText(status), "status": status, "detail": detail})
	_, _ = w.Write(b)
}

// SafeNext valida o destino pós-login: só caminho local absoluto (sem //, sem esquema).
func SafeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") ||
		strings.ContainsAny(next, "\r\n\\") {
		return "/"
	}
	if u, err := url.Parse(next); err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	if strings.HasPrefix(next, LoginPath) {
		return "/"
	}
	return next
}
