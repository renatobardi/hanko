package app

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// hostHandler rejeita (421) requisições cujo cabeçalho Host não esteja na lista de
// hosts permitidos: defesa contra DNS rebinding, em que um site externo faz o
// navegador do operador falar com o Hanko em loopback sob outro nome.
// Permitidos: localhost, 127.0.0.1, [::1], o host de publicURL e os extras
// (HANKO_ALLOWED_HOSTS). A porta é ignorada na comparação.
func hostHandler(publicURL string, extra []string, next http.Handler) http.Handler {
	allowed := map[string]struct{}{"localhost": {}, "127.0.0.1": {}, "::1": {}}
	if u, err := url.Parse(publicURL); err == nil && u.Hostname() != "" {
		allowed[strings.ToLower(u.Hostname())] = struct{}{}
	}
	for _, h := range extra {
		if name := hostname(h); name != "" {
			allowed[name] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[hostname(r.Host)]; !ok {
			http.Error(w, "host não permitido (veja HANKO_ALLOWED_HOSTS)", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostname extrai o nome do host de "host", "host:porta", "[::1]" ou "[::1]:porta", em minúsculas.
func hostname(hostport string) string {
	h := strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	return strings.ToLower(strings.TrimSuffix(h, "."))
}
