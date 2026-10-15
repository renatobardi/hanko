package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/netguard"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func TestSignKnownVector(t *testing.T) {
	// HMAC-SHA256("segredo", `1792065600.{"a":1}`) calculado de forma independente (python hmac).
	got := Sign("segredo", t0, []byte(`{"a":1}`))
	const want = "t=1792065600,v1=eab5cf49205667a53c5b29b4960f20beb843840d6cb08cfe3f8f60e86b0511ac"
	if got != want {
		t.Fatalf("Sign = %q, want %q", got, want)
	}
	if err := Verify("segredo", got, []byte(`{"a":1}`), t0, 0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	body := []byte(`{"event":"task.created"}`)
	sig := Sign("s3cret-value", t0, body)
	cases := map[string]struct {
		secret, hdr string
		body        []byte
		now         time.Time
	}{
		"segredo errado":    {"outro-segredo", sig, body, t0},
		"corpo alterado":    {"s3cret-value", sig, []byte(`{"event":"task.closed"}`), t0},
		"antigo demais":     {"s3cret-value", sig, body, t0.Add(10 * time.Minute)},
		"do futuro":         {"s3cret-value", sig, body, t0.Add(-10 * time.Minute)},
		"sem v1":            {"s3cret-value", "t=1792065600", body, t0},
		"sem t":             {"s3cret-value", "v1=abcd", body, t0},
		"timestamp ruim":    {"s3cret-value", "t=abc,v1=abcd", body, t0},
		"vazio":             {"s3cret-value", "", body, t0},
		"timestamp trocado": {"s3cret-value", strings.Replace(sig, "t=1792065600", "t=1792065601", 1), body, t0},
	}
	for name, c := range cases {
		if err := Verify(c.secret, c.hdr, c.body, c.now, 0); err == nil {
			t.Errorf("%s: deveria falhar", name)
		}
	}
}

func TestSendHeadersBodyAndSignature(t *testing.T) {
	body := []byte(`{"event":"gate.requested","data":{"x":1}}`)
	var got struct {
		hdr  http.Header
		body []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.hdr = r.Header.Clone()
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := netguard.NewClient(netguard.Policy{AllowPrivate: true}, 2*time.Second)
	res := Send(context.Background(), c, srv.URL, "whsec_test", "del-1", "gate.opened", body, t0)
	if !res.OK() || res.Status != 204 {
		t.Fatalf("resultado: %+v", res)
	}
	if string(got.body) != string(body) {
		t.Fatalf("corpo = %q", got.body)
	}
	if got.hdr.Get(HeaderEvent) != "gate.opened" || got.hdr.Get(HeaderDelivery) != "del-1" || got.hdr.Get("User-Agent") != UserAgent {
		t.Fatalf("cabeçalhos: %v", got.hdr)
	}
	if err := Verify("whsec_test", got.hdr.Get(HeaderSignature), got.body, t0, 0); err != nil {
		t.Fatalf("assinatura recebida não confere: %v", err)
	}
}

func TestSendFailures(t *testing.T) {
	c := netguard.NewClient(netguard.Policy{AllowPrivate: true}, 300*time.Millisecond)

	// 500 e 3xx são falhas
	for _, code := range []int{500, 404, 302} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if code == 302 {
				w.Header().Set("Location", "https://example.com/")
			}
			w.WriteHeader(code)
		}))
		res := Send(context.Background(), c, srv.URL, "s", "d", "e", []byte("{}"), t0)
		srv.Close()
		if res.OK() || res.Status != code || res.Err == "" {
			t.Errorf("%d: %+v", code, res)
		}
	}

	// timeout
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	res := Send(context.Background(), c, slow.URL, "s", "d", "e", []byte("{}"), t0)
	if res.OK() || res.Status != 0 || res.Err != "tempo esgotado" {
		t.Errorf("timeout: %+v", res)
	}

	// o erro de transporte não ecoa a URL (pode ter token na query)
	res = Send(context.Background(), c, "http://127.0.0.1:1/hook?token=SEGREDO123", "s", "d", "e", []byte("{}"), t0)
	if res.OK() || strings.Contains(res.Err, "SEGREDO123") || strings.Contains(res.Err, "token=") {
		t.Errorf("erro vaza a URL: %q", res.Err)
	}
}

func TestSendBlocksInternalDestination(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c := netguard.NewClient(netguard.Policy{}, time.Second)
	res := Send(context.Background(), c, srv.URL, "s", "d", "e", []byte("{}"), t0)
	if res.OK() || hits != 0 || !strings.Contains(res.Err, "bloqueado") {
		t.Fatalf("destino interno deveria ser bloqueado: %+v hits=%d", res, hits)
	}
}
