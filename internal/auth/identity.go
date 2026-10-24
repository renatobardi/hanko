package auth

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Via diz como o usuário se autenticou.
type Via string

const (
	ViaSession Via = "session"
	ViaAPIKey  Via = "api_key"
	// ViaChannel é uma pessoa que age por um canal externo vinculado (ADR-0046): o service age como ela, com o mesmo
	// papel e a mesma audiência de qualquer superfície.
	ViaChannel Via = "channel"
)

// Identity é o usuário resolvido pelo Guard para a requisição.
type Identity struct {
	User domain.User
	Via  Via
	// KeyID e KeyScope só valem com ViaAPIKey (ADR-0057); escopo vazio = full.
	KeyID    *domain.ID
	KeyScope domain.KeyScope
	// Session só vale com ViaSession: a sessão que autenticou a requisição e a sua reautenticação recente (ADR-0070).
	Session SessionInfo
}

// SessionInfo é o estado da sessão, lido a cada requisição. ReauthAt é nil sem prova feita.
type SessionInfo struct {
	ID           domain.ID
	ReauthAt     *time.Time
	ReauthMethod string
}

type identityKey struct{}

// WithIdentity guarda a identidade no contexto.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom devolve a identidade resolvida pelo Guard, se houver.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// IsDirectLoopback: conexão de loopback sem sinal de proxy reverso à frente.
func IsDirectLoopback(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// SameOrigin é o gate de CSRF estrito para requisições que mudam estado: Sec-Fetch-Site
// same-origin, ou Origin cujo host é o Host da requisição. Sem nenhum dos dois, recusa.
func SameOrigin(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
		return sfs == "same-origin"
	}
	if o := r.Header.Get("Origin"); o != "" {
		return originHost(o) == r.Host
	}
	return false
}

func originHost(origin string) string {
	_, rest, ok := strings.Cut(origin, "://")
	if !ok {
		return ""
	}
	host, _, _ := strings.Cut(rest, "/")
	return host
}
