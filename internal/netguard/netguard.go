// Package netguard protege o servidor contra SSRF nas chamadas de saída das integrações (ADR-0029):
// webhooks e testes de conexão só alcançam destinos públicos, a menos que o operador libere
// (HANKO_INTEGRATIONS_ALLOW_PRIVATE).
//
// A barreira de verdade é a conexão: o DialContext confere o IP efetivo de CADA tentativa de conexão
// (depois da resolução de DNS, no hook Control do socket), então rebinding de DNS, nomes que resolvem para
// endereços internos e formas exóticas de escrever um IP (decimal, hexadecimal, IPv4 mapeado em IPv6) caem
// todos na mesma verificação. ValidateURL só dá retorno rápido e barra o que nem precisa de DNS.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked: o destino é interno e a instalação não o liberou.
var ErrBlocked = errors.New("destino interno bloqueado (loopback, link-local ou rede privada)")

// Policy é a decisão do operador.
type Policy struct {
	// AllowPrivate libera loopback, link-local e redes privadas.
	AllowPrivate bool
}

var blockedPrefixes = mustPrefixes(
	"0.0.0.0/8",      // "esta rede"
	"10.0.0.0/8",     // RFC 1918
	"100.64.0.0/10",  // CGNAT (RFC 6598)
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local (inclui metadados de nuvem 169.254.169.254)
	"172.16.0.0/12",  // RFC 1918
	"192.0.0.0/24",   // IETF protocol assignments
	"192.168.0.0/16", // RFC 1918
	"198.18.0.0/15",  // benchmarking
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // reservado + broadcast
	"::/128",         // não especificado
	"::1/128",        // loopback
	"fc00::/7",       // ULA
	"fe80::/10",      // link-local
	"ff00::/8",       // multicast
	"64:ff9b::/96",   // NAT64: embute um IPv4 qualquer
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// BlockedIP informa se ip é um destino interno/reservado. IPv4 mapeado em IPv6 (::ffff:a.b.c.d) vale como o IPv4.
func BlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateURL confere forma e destinos óbvios: esquema (https; http só com allowHTTP), host presente, sem
// credenciais na URL, e IP literal ou nome local bloqueados. Não resolve DNS (isso é do Dialer).
func ValidateURL(raw string, allowHTTP bool, p Policy) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("URL inválida")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !allowHTTP {
			return nil, errors.New("use https")
		}
	default:
		return nil, errors.New("use uma URL https")
	}
	if u.User != nil {
		return nil, errors.New("não ponha usuário e senha na URL")
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("URL sem host")
	}
	if p.AllowPrivate {
		return u, nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if BlockedIP(ip) {
			return nil, ErrBlocked
		}
		return u, nil
	}
	lh := strings.TrimSuffix(strings.ToLower(host), ".")
	if lh == "localhost" || strings.HasSuffix(lh, ".localhost") || strings.HasSuffix(lh, ".internal") || strings.HasSuffix(lh, ".local") {
		return nil, ErrBlocked
	}
	return u, nil
}

// control é o hook do socket: roda com o endereço já resolvido, imediatamente antes do connect.
func control(p Policy) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		if p.AllowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("endereço inválido: %w", err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("endereço inválido: %w", err)
		}
		if BlockedIP(ip) {
			return ErrBlocked
		}
		return nil
	}
}

// DialContext é um DialContext que recusa conexões a destinos internos.
func DialContext(p Policy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: control(p)}
	return d.DialContext
}

// NewClient devolve um cliente HTTP para chamadas de saída: sem proxy do ambiente (um proxy alcançável
// contornaria a verificação), sem seguir redirecionamentos (um 3xx para um destino interno nunca é seguido;
// a resposta 3xx é entregue ao chamador, que a trata como falha) e com teto de tempo.
func NewClient(p Policy, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            DialContext(p),
		TLSHandshakeTimeout:    timeout,
		ResponseHeaderTimeout:  timeout,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
