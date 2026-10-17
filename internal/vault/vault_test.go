package vault

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeKV2 imita a API KV v2: GET /v1/<mount>/data/<path>.
func fakeKV2(t *testing.T, token, mount string, data map[string]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != token {
			http.Error(w, `{"errors":["permission denied"]}`, http.StatusForbidden)
			return
		}
		prefix := "/v1/" + mount + "/data/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		fields, ok := data[strings.TrimPrefix(r.URL.Path, prefix)]
		if !ok {
			http.Error(w, `{"errors":[]}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := `{"data":{"data":{`
		first := true
		for k, v := range fields {
			if !first {
				out += ","
			}
			first = false
			switch x := v.(type) {
			case string:
				out += `"` + k + `":"` + x + `"`
			default:
				out += `"` + k + `":123`
			}
		}
		out += `},"metadata":{"version":3}}}`
		_, _ = w.Write([]byte(out))
	}))
}

func TestKV2Get(t *testing.T) {
	srv := fakeKV2(t, "tok-1", "kv", map[string]map[string]any{
		"apps/hanko": {"token": "s3cr3t-value", "value": "default-val", "num": 1},
	})
	defer srv.Close()
	p, err := NewKV2(KV2Options{Addr: srv.URL + "/", Token: "tok-1", Mount: "kv"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != KV2Name {
		t.Fatalf("Name = %q", p.Name())
	}
	ctx := context.Background()
	if v, err := p.Get(ctx, "apps/hanko#token"); err != nil || v != "s3cr3t-value" {
		t.Fatalf("Get campo = %q, %v", v, err)
	}
	if v, err := p.Get(ctx, "/apps/hanko"); err != nil || v != "default-val" {
		t.Fatalf("Get padrão = %q, %v", v, err)
	}
	if _, err := p.Get(ctx, "apps/hanko#ausente"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("campo ausente: %v", err)
	}
	if _, err := p.Get(ctx, "outro/caminho"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("caminho ausente: %v", err)
	}
	if _, err := p.Get(ctx, "apps/hanko#num"); err == nil {
		t.Fatal("campo que não é texto deve falhar")
	}
	if _, err := p.Get(ctx, "../etc#x"); err == nil {
		t.Fatal("referência com .. deve falhar")
	}
}

func TestKV2Errors(t *testing.T) {
	srv := fakeKV2(t, "right", "secret", map[string]map[string]any{"a": {"value": "x"}})
	defer srv.Close()
	bad, _ := NewKV2(KV2Options{Addr: srv.URL, Token: "wrong"})
	_, err := bad.Get(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "permissão") {
		t.Fatalf("token errado: %v", err)
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Fatal("o erro não pode citar a URL do cofre")
	}
	down, _ := NewKV2(KV2Options{Addr: "http://127.0.0.1:1", Token: "x"})
	if _, err := down.Get(context.Background(), "a"); err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("cofre fora do ar: %v", err)
	}
	for _, o := range []KV2Options{{Addr: "", Token: "x"}, {Addr: "ftp://x", Token: "x"}, {Addr: "http://x"}} {
		if _, err := NewKV2(o); err == nil {
			t.Fatalf("NewKV2(%+v) deveria falhar", o)
		}
	}
}

func TestKV2Namespace(t *testing.T) {
	var ns string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ns = r.Header.Get("X-Vault-Namespace")
		_, _ = w.Write([]byte(`{"data":{"data":{"value":"ok"}}}`))
	}))
	defer srv.Close()
	p, _ := NewKV2(KV2Options{Addr: srv.URL, Token: "t", Namespace: "time-a"})
	if v, err := p.Get(context.Background(), "x"); err != nil || v != "ok" || ns != "time-a" {
		t.Fatalf("Get = %q, %v, ns=%q", v, err, ns)
	}
}
