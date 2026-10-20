package ratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/auth"
)

// BrowserErrorPath é o endpoint que recebe os erros do navegador (ADR-0058 §C).
const BrowserErrorPath = "/telemetry/errors"

// WebhookPrefix é o caminho reservado aos webhooks de entrada (gatilhos de fluxo do M19).
const WebhookPrefix = "/hooks/"

// Limits são os tetos efetivos, lidos a cada requisição (instance_settings: mudar vale sem reiniciar).
type Limits interface {
	Enabled() bool
	// PerMinute devolve o teto da classe; zero ou negativo = sem limite.
	PerMinute(Class) int64
}

// Classify diz a que classe a requisição pertence. ok=false: não é limitada (leituras, probes, estáticos, login e
// claim, que têm limitador próprio contra tentativa e erro).
func Classify(r *http.Request) (Class, bool) {
	p := r.URL.Path
	switch {
	case p == "/healthz", p == "/readyz", p == "/metrics", strings.HasPrefix(p, "/static/"):
		return "", false
	case p == BrowserErrorPath:
		if r.Method != http.MethodPost {
			return "", false
		}
		return ClassBrowserError, true
	case strings.HasPrefix(p, WebhookPrefix):
		return ClassWebhook, true
	case p == "/mcp", strings.HasPrefix(p, "/mcp/"), p == "/memory-mcp", strings.HasPrefix(p, "/memory-mcp/"):
		return ClassMCP, true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return "", false
	}
	if p == auth.LoginPath || strings.HasPrefix(p, auth.LoginPath+"/") || strings.HasPrefix(p, auth.ClaimPath) ||
		strings.HasPrefix(p, "/auth/") || strings.HasPrefix(p, auth.InvitePath) || p == "/sair" {
		return "", false
	}
	return ClassWrite, true
}

// Options configura o middleware.
type Options struct {
	Limiter *Limiter
	Limits  Limits
	Proxies auth.TrustedProxies
	// Tokens, se informado, valida run tokens para limitar por run; sem ele (ou token inválido) cai no IP.
	Tokens *auth.RunTokens
}

// Middleware aplica o rate limit. Fica por dentro do auth.Guard (precisa da identidade resolvida).
func Middleware(o Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			class, ok := Classify(r)
			if !ok || o.Limits == nil || !o.Limits.Enabled() {
				next.ServeHTTP(w, r)
				return
			}
			if d, limited := o.check(r, class); limited {
				trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("ratelimit.class", string(class)), attribute.String("ratelimit.outcome", OutcomeLimited))
				reject(w, r, d)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// check consome o bucket da identidade e, nas classes que têm, o do IP. limited=true quando algum negou.
func (o Options) check(r *http.Request, class Class) (Decision, bool) {
	ip := o.Proxies.ClientIP(r)
	who := o.principal(r.Context(), r)
	perMin := o.Limits.PerMinute(class)
	if who == "" {
		who = "ip:" + ip // anônimo (modo trusted, ou sem credencial): o IP é a identidade
		if perMin <= 0 {
			return Decision{Allowed: true}, false
		}
		d := o.Limiter.Take(r.Context(), class, who, perMin)
		return d, !d.Allowed
	}
	var first Decision
	if perMin > 0 {
		first = o.Limiter.Take(r.Context(), class, who, perMin)
		if !first.Allowed {
			return first, true
		}
	}
	// Teto por IP somando todas as identidades: só escritas (os agentes do MCP compartilham o loopback).
	if class == ClassWrite {
		if ipMin := o.Limits.PerMinute(ClassIP); ipMin > 0 {
			d := o.Limiter.Take(r.Context(), ClassIP, "ip:"+ip, ipMin)
			if !d.Allowed {
				return d, true
			}
		}
	}
	return first, false
}

// principal é a chave da identidade: hash da API key, usuário de sessão ou run token válido. Vazio = anônimo.
func (o Options) principal(ctx context.Context, r *http.Request) string {
	if id, ok := auth.IdentityFrom(ctx); ok {
		if id.Via == auth.ViaAPIKey {
			if tok, ok := auth.BearerToken(r.Header.Get("Authorization")); ok {
				return "key:" + auth.HashToken(tok)[:16]
			}
		}
		return "user:" + id.User.ID.String()
	}
	if o.Tokens != nil {
		if tok, ok := auth.BearerToken(r.Header.Get("Authorization")); ok && !auth.IsAPIKey(tok) {
			if c, err := o.Tokens.Verify(tok); err == nil && c.RunID != "" {
				return "run:" + c.RunID
			}
		}
	}
	return ""
}

func reject(w http.ResponseWriter, r *http.Request, d Decision) {
	secs := RetryAfterSeconds(d.RetryAfter)
	h := w.Header()
	h.Set("Retry-After", strconv.Itoa(secs))
	h.Set("RateLimit-Limit", strconv.FormatInt(d.Limit, 10))
	h.Set("RateLimit-Remaining", "0")
	h.Set("Cache-Control", "no-store")
	en := strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "en")
	msg := fmt.Sprintf("Muitas requisições. Tente de novo em %d s.", secs)
	if en {
		msg = fmt.Sprintf("Too many requests. Try again in %d s.", secs)
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/"), strings.HasPrefix(r.URL.Path, "/mcp"), strings.HasPrefix(r.URL.Path, "/memory-mcp"):
		h.Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusTooManyRequests)
		b, _ := json.Marshal(map[string]any{"title": "Too Many Requests", "status": http.StatusTooManyRequests, "detail": msg, "retry_after": secs})
		_, _ = w.Write(b)
	case r.Header.Get("HX-Request") == "true":
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("HX-Retarget", "#flash")
		h.Set("HX-Reswap", "innerHTML")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprintf(w, `<div class="alert danger" role="alert"><div><b>429</b> %s</div></div>`, html.EscapeString(msg))
	default:
		http.Error(w, msg, http.StatusTooManyRequests)
	}
}
