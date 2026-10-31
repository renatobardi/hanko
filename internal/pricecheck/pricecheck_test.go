package pricecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourceIDs(t *testing.T) {
	for _, c := range []struct {
		model          string
		devProv, devID string
		router         string
		none           bool
	}{
		{"claude-opus-5-5", "anthropic", "claude-opus-5-5", "anthropic/claude-opus-5.5", false},
		{"anthropic/claude-sonnet-4-5-20250929", "anthropic", "claude-sonnet-4-5-20250929", "anthropic/claude-sonnet-4.5", false},
		{"GPT-6-luna", "openai", "gpt-6-luna", "openai/gpt-6-luna", false},
		{"glm-5.3", "zai", "glm-5.3", "z-ai/glm-5.3", false},
		{"gemini-3", "", "", "", true},
		{"", "", "", "", true},
	} {
		ids := SourceIDs(c.model)
		if c.none {
			if len(ids) != 0 {
				t.Errorf("%q devia ficar sem fonte: %v", c.model, ids)
			}
			continue
		}
		if ids[ModelsDev] != (SourceID{Provider: c.devProv, ID: c.devID}) || ids[OpenRouter].ID != c.router {
			t.Errorf("%q = %+v, quer models.dev %s/%s e openrouter %s", c.model, ids, c.devProv, c.devID, c.router)
		}
	}
}

func TestNumberAndPartial(t *testing.T) {
	data, err := LoadJSON([]byte(`{"a":{"cost":{"input":3,"output":15,"cache_read":0.3,"cache_write":3.75}},
		"b":{"cost":{"input":3,"output":-1}},"c":{"cost":{"input":"3","output":15}},"d":{"cost":{"input":1e9,"output":1}},"e":{"cost":{"input":1,"output":2,"cache_read":-2}}}`))
	if err != nil {
		t.Fatal(err)
	}
	m := data.(map[string]any)
	cost := func(k string) any { return m[k].(map[string]any)["cost"] }
	names := [4]string{"input", "output", "cache_read", "cache_write"}
	if p := partial(cost("a"), names, false); p == nil || p["input"] != 3 || p["cache_creation"] != 3.75 {
		t.Errorf("a = %v", p)
	}
	for _, k := range []string{"b", "c", "d", "e"} {
		if p := partial(cost(k), names, false); p != nil {
			t.Errorf("%s: preço inválido devia derrubar o modelo: %v", k, p)
		}
	}
	// o OpenRouter manda o preço por token como texto: "0.000003" USD por token são 3 USD por 1M
	if v, ok := number("0.000003", true); !ok || v != 3 {
		t.Errorf("número por token = %v %v, quer 3", v, ok)
	}
	if _, ok := number(json.Number("3"), true); ok {
		t.Error("o preço por token tem de vir como texto")
	}
	if _, ok := number("-1", true); ok {
		t.Error("o -1 do OpenRouter (preço dinâmico) não é preço")
	}
}

func TestAgreeAndDecide(t *testing.T) {
	a := Partial{"input": 3, "output": 15, "cache_read": 0.3, "cache_creation": 3.75}
	b := Partial{"input": 3, "output": 15, "cache_read": 0.3}
	got, bad, ok := Agree(a, b)
	if !ok || len(bad) != 0 || got.CacheCreation != 3.75 {
		t.Fatalf("Agree = %+v %v %v; o cache que só uma fonte tem vale", got, bad, ok)
	}
	if _, bad, ok := Agree(a, Partial{"input": 3, "output": 16}); ok || len(bad) != 1 || bad[0] != "output" {
		t.Errorf("divergência em output: %v %v", bad, ok)
	}
	if p, _, _ := Agree(Partial{"input": 2, "output": 8}, Partial{"input": 2, "output": 8}); p.CacheRead != 2 || p.CacheCreation != 2 {
		t.Errorf("sem cache em nenhuma fonte vale o preço de input: %+v", p)
	}

	cur := &Price{Input: 3, Output: 15, CacheRead: 0.3, CacheCreation: 3.75}
	read := func(p Partial) Read { return Read{Partial: p} }
	same := map[string]Read{ModelsDev: read(a), OpenRouter: read(a)}
	diff := Partial{"input": 4, "output": 20, "cache_read": 0.4, "cache_creation": 5}
	newer := map[string]Read{ModelsDev: read(diff), OpenRouter: read(diff)}
	for _, c := range []struct {
		name   string
		cur    *Price
		locked bool
		reads  map[string]Read
		status string
		hasNew bool
	}{
		{"sem preço: novo", nil, false, same, StatusNew, true},
		{"igual", cur, false, same, StatusEqual, false},
		{"as fontes concordam num valor novo: troca", cur, false, newer, StatusChanged, true},
		{"travado: só avisa", cur, true, newer, StatusLockedDiffers, false},
		{"fonte fora", cur, false, map[string]Read{ModelsDev: read(a), OpenRouter: {Err: ErrHTTP}}, StatusSourceDown, false},
		{"uma fonte não tem o modelo", cur, false, map[string]Read{ModelsDev: read(a), OpenRouter: {Missing: true}}, StatusNoSource, false},
		{"as fontes divergem", cur, false, map[string]Read{ModelsDev: read(a), OpenRouter: read(diff)}, StatusDiverges, false},
		{"divergem e está travado: traz a marca", cur, true, map[string]Read{ModelsDev: read(a), OpenRouter: read(diff)}, StatusDiverges, false},
	} {
		d := Decide(c.cur, c.locked, c.reads)
		if d.Status != c.status || (d.New != nil) != c.hasNew {
			t.Errorf("%s: %s (novo %v), quer %s (novo %v)", c.name, d.Status, d.New, c.status, c.hasNew)
		}
	}
	if d := Decide(cur, true, map[string]Read{ModelsDev: read(a), OpenRouter: read(diff)}); d.Detail["locked"] != true {
		t.Error("o detalhe da divergência de um modelo travado traz locked=true")
	}
}

func TestFetchLimits(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"x":1}`)) }))
	defer ok.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/outro", http.StatusFound) }))
	defer redir.Close()
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", MaxBytes+10)))
	}))
	defer big.Close()
	c := NoRedirect(http.DefaultClient)
	ctx := context.Background()

	if b, err := Fetch(ctx, c, ok.URL, true); err != nil || string(b) != `{"x":1}` {
		t.Errorf("busca boa = %q %v", b, err)
	}
	for name, tc := range map[string]struct {
		url  string
		code string
	}{"redirecionamento": {redir.URL, ErrRedirect}, "http": {notFound.URL, ErrHTTP}, "tamanho": {big.URL, ErrSize}} {
		if _, err := Fetch(ctx, c, tc.url, true); CodeOf(err) != tc.code {
			t.Errorf("%s: código %q, quer %q", name, CodeOf(err), tc.code)
		}
	}
	// sem allowHTTP só https passa, e nunca com credencial na URL
	for _, u := range []string{ok.URL, "https://user:pw@example.org/x", "ftp://x/y", "https:///x"} {
		if _, err := Fetch(ctx, c, u, false); CodeOf(err) != ErrURL {
			t.Errorf("%q: código %q, quer %q", u, CodeOf(err), ErrURL)
		}
	}
	if _, err := Fetch(ctx, c, "https://127.0.0.1:1/x", false); CodeOf(err) != ErrNet {
		t.Errorf("porta fechada: código %q, quer %q", CodeOf(err), ErrNet)
	}
}

func TestParseSources(t *testing.T) {
	dev, _ := LoadJSON([]byte(`{"anthropic":{"models":{"claude-opus-5-5":{"cost":{"input":15,"output":75,"cache_read":1.5,"cache_write":18.75}}}},
		"openai":{"models":{"gpt-6-luna":{"cost":{"input":5,"output":20}}}}}`))
	got, err := ParseModelsDev(dev)
	if err != nil || got[SourceID{Provider: "anthropic", ID: "claude-opus-5-5"}]["output"] != 75 || len(got) != 2 {
		t.Fatalf("ParseModelsDev = %v (%v)", got, err)
	}
	if _, err := ParseModelsDev(map[string]any{"anthropic": map[string]any{}}); CodeOf(err) != ErrFormat {
		t.Errorf("provedor obrigatório sem models: %v", err)
	}
	or, _ := LoadJSON([]byte(`{"data":[{"id":"anthropic/claude-opus-5.5","pricing":{"prompt":"0.000015","completion":"0.000075","input_cache_read":"0.0000015"}},
		{"id":"x/dinamico","pricing":{"prompt":"-1","completion":"-1"}}]}`))
	r, err := ParseOpenRouter(or)
	if err != nil || len(r) != 1 || r[SourceID{ID: "anthropic/claude-opus-5.5"}]["input"] != 15 {
		t.Fatalf("ParseOpenRouter = %v (%v)", r, err)
	}
	if _, err := ParseOpenRouter(map[string]any{"data": "x"}); CodeOf(err) != ErrFormat {
		t.Errorf("formato: %v", err)
	}
	if _, err := LoadJSON([]byte(`{`)); CodeOf(err) != ErrJSON {
		t.Errorf("json inválido: %v", err)
	}
}
