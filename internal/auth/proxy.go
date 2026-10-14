package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// DefaultTrustedProxies são os pares cujo X-Forwarded-For é aceito quando nada é configurado:
// só o próprio host. Um proxy em outra rede (ex.: o Caddy do compose, via rede docker) precisa
// ser listado em HANKO_TRUSTED_PROXIES.
var DefaultTrustedProxies = []string{"127.0.0.1/32", "::1/128"}

// TrustedProxies é o conjunto de redes (CIDR) cujos pares podem informar o IP do cliente por
// X-Forwarded-For. O valor zero não confia em ninguém: o IP do cliente é sempre o RemoteAddr.
type TrustedProxies struct{ nets []*net.IPNet }

// ParseTrustedProxies aceita CIDRs ("10.0.0.0/8") e IPs soltos ("127.0.0.1").
func ParseTrustedProxies(entries []string) (TrustedProxies, error) {
	var p TrustedProxies
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.Contains(e, "/") {
			ip := net.ParseIP(e)
			if ip == nil {
				return TrustedProxies{}, fmt.Errorf("proxy confiável %q inválido: use um CIDR (ex.: 172.18.0.0/16) ou IP", e)
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			e = fmt.Sprintf("%s/%d", ip, bits)
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			return TrustedProxies{}, fmt.Errorf("proxy confiável %q inválido: use um CIDR (ex.: 172.18.0.0/16) ou IP", e)
		}
		p.nets = append(p.nets, n)
	}
	return p, nil
}

// Contains informa se ip pertence a alguma rede confiável.
func (p TrustedProxies) Contains(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range p.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP é o IP do cliente. X-Forwarded-For só vale quando o par da conexão é um proxy confiável;
// então se percorre a cadeia da direita para a esquerda e vale o primeiro endereço que não é de
// proxy confiável (o que o proxy mais próximo viu). Cadeia ilegível ou toda confiável: o próprio par.
func (p TrustedProxies) ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !p.Contains(net.ParseIP(host)) {
		return host
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			return host // cadeia adulterada: não adivinha
		}
		if !p.Contains(ip) {
			return ip.String()
		}
	}
	return host
}
