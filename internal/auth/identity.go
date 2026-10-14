package auth

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
)

// Via diz como o usuário se autenticou.
type Via string

const (
	ViaSession Via = "session"
	ViaAPIKey  Via = "api_key"
)

// Identity é o usuário resolvido pelo Guard para a requisição.
type Identity struct {
	User domain.User
	Via  Via
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

// ClientIP é o IP do cliente. X-Forwarded-For só vale quando a conexão vem de um proxy
// local ou privado (o último valor é o que o proxy mais próximo anexou); de fora, é ignorado.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(last) != nil {
				return last
			}
		}
	}
	return host
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
