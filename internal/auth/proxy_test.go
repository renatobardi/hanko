package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func reqFrom(remote, xff string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func mustProxies(t *testing.T, entries ...string) TrustedProxies {
	t.Helper()
	p, err := ParseTrustedProxies(entries)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTrustedProxiesClientIP(t *testing.T) {
	def := mustProxies(t, DefaultTrustedProxies...)
	docker := mustProxies(t, "127.0.0.1/32", "172.18.0.0/16")
	for _, c := range []struct {
		name   string
		p      TrustedProxies
		remote string
		xff    string
		want   string
	}{
		{"sem lista: ignora XFF mesmo de loopback", TrustedProxies{}, "127.0.0.1:1", "9.9.9.9", "127.0.0.1"},
		{"par público não é proxy: ignora XFF", def, "203.0.113.9:5555", "1.2.3.4", "203.0.113.9"},
		{"par privado fora da lista: ignora XFF (antes valia)", def, "10.0.0.2:1", "1.2.3.4", "10.0.0.2"},
		{"par privado da rede docker fora do default", def, "172.18.0.3:1", "198.51.100.7", "172.18.0.3"},
		{"loopback confiável: último XFF", def, "127.0.0.1:5555", "9.9.9.9, 198.51.100.7", "198.51.100.7"},
		{"rede docker listada: cliente real", docker, "172.18.0.3:1", "198.51.100.7", "198.51.100.7"},
		{"cadeia com proxy confiável à direita: pula", docker, "172.18.0.3:1", "198.51.100.7, 172.18.0.9", "198.51.100.7"},
		{"cliente forja XFF à esquerda: vale o que o proxy viu", docker, "172.18.0.3:1", "6.6.6.6, 198.51.100.7", "198.51.100.7"},
		{"XFF ilegível: o próprio par", docker, "172.18.0.3:1", "lixo", "172.18.0.3"},
		{"sem XFF: o próprio par", docker, "172.18.0.3:1", "", "172.18.0.3"},
		{"IPv6 loopback no default", def, "[::1]:9", "198.51.100.7", "198.51.100.7"},
	} {
		if got := c.p.ClientIP(reqFrom(c.remote, c.xff)); got != c.want {
			t.Errorf("%s: ClientIP = %q, quer %q", c.name, got, c.want)
		}
	}
}

func TestParseTrustedProxies(t *testing.T) {
	if p, err := ParseTrustedProxies([]string{" 10.0.0.0/8 ", "192.168.1.5", "fd00::/8", ""}); err != nil || len(p.nets) != 3 {
		t.Fatalf("válidos: %v %d", err, len(p.nets))
	}
	for _, bad := range []string{"10.0.0.0/33", "não-ip", "1.2.3"} {
		if _, err := ParseTrustedProxies([]string{bad}); err == nil {
			t.Errorf("%q deveria ser recusado", bad)
		}
	}
}
