package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1", "10.0.0.5", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "0.0.0.0", "::", "fe80::1", "fc00::1", "fd12:3456::1",
		"100.64.0.1", "224.0.0.1", "255.255.255.255", "198.18.0.1", "::ffff:127.0.0.1", "::ffff:10.1.1.1",
		"::ffff:169.254.169.254", "64:ff9b::7f00:1",
	}
	for _, s := range blocked {
		if !BlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s deveria ser bloqueado", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111", "172.32.0.1", "172.15.255.255", "::ffff:8.8.8.8"}
	for _, s := range allowed {
		if BlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s deveria ser permitido", s)
		}
	}
}

func TestValidateURL(t *testing.T) {
	bad := map[string]string{
		"":                               "vazia",
		"ftp://example.com/x":            "esquema",
		"http://example.com/x":           "http sem allowHTTP",
		"https://user:pw@example.com/x":  "credenciais na URL",
		"https://127.0.0.1/hook":         "loopback",
		"https://[::1]/hook":             "loopback v6",
		"https://10.0.0.1/hook":          "rfc1918",
		"https://169.254.169.254/latest": "metadados",
		"https://[::ffff:127.0.0.1]/x":   "v4 mapeado",
		"https://localhost/x":            "localhost",
		"https://app.localhost:8443/x":   "subdomínio localhost",
		"https://db.internal/x":          "internal",
		"https:///x":                     "sem host",
	}
	for u, why := range bad {
		if _, err := ValidateURL(u, false, Policy{}); err == nil {
			t.Errorf("%q (%s) deveria ser recusada", u, why)
		}
	}
	if _, err := ValidateURL("https://hooks.example.com/in?x=1", false, Policy{}); err != nil {
		t.Errorf("URL pública: %v", err)
	}
	if _, err := ValidateURL("http://mcp.example.com/mcp", true, Policy{}); err != nil {
		t.Errorf("http permitido com allowHTTP: %v", err)
	}
	// o operador libera
	for _, u := range []string{"https://127.0.0.1/hook", "https://localhost/x", "https://10.0.0.1/hook"} {
		if _, err := ValidateURL(u, false, Policy{AllowPrivate: true}); err != nil {
			t.Errorf("%q com AllowPrivate: %v", u, err)
		}
	}
	if _, err := ValidateURL("https://127.0.0.1/hook", false, Policy{}); !errors.Is(err, ErrBlocked) {
		t.Errorf("erro esperado ErrBlocked, got %v", err)
	}
}

// O servidor de teste escuta em loopback: a conexão é recusada pelo Dialer, mesmo que a URL tenha passado.
func TestClientBlocksLoopbackAtDial(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()

	c := NewClient(Policy{}, 2*time.Second)
	_, err := c.Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), ErrBlocked.Error()) {
		t.Fatalf("esperava ErrBlocked, got %v", err)
	}
	if hits != 0 {
		t.Fatal("o servidor interno recebeu a requisição")
	}

	// as formas exóticas de escrever 127.0.0.1 também caem no hook do socket
	for _, host := range []string{"2130706433", "0x7f.1", "127.1", "[::ffff:127.0.0.1]"} {
		port := srv.Listener.Addr().String()[strings.LastIndex(srv.Listener.Addr().String(), ":"):]
		if _, err := c.Get("http://" + host + port + "/"); err == nil {
			t.Errorf("%s deveria falhar", host)
		}
	}
	if hits != 0 {
		t.Fatal("o servidor interno recebeu a requisição por uma forma exótica de IP")
	}

	// liberado pelo operador
	c = NewClient(Policy{AllowPrivate: true}, 2*time.Second)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
}

// Um redirecionamento nunca é seguido, nem para um destino público: a resposta 3xx chega ao chamador.
func TestClientDoesNotFollowRedirects(t *testing.T) {
	var target int
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { target++ }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dst.URL, http.StatusFound)
	}))
	defer src.Close()
	c := NewClient(Policy{AllowPrivate: true}, 2*time.Second)
	resp, err := c.Get(src.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || target != 0 {
		t.Fatalf("status %d, alvo atingido %d vezes", resp.StatusCode, target)
	}
}

func TestDialContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DialContext(Policy{AllowPrivate: true})(ctx, "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("contexto cancelado deveria falhar")
	}
}
