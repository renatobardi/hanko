package aimemory

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/memory"
)

type noIn struct{}

func fakeWithTool(t *testing.T, name string) *httptest.Server {
	t.Helper()
	s := sdk.NewServer(&sdk.Implementation{Name: "fake-ai-memory", Version: "1"}, nil)
	sdk.AddTool(s, &sdk.Tool{Name: name, Description: "x"}, func(context.Context, *sdk.CallToolRequest, noIn) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil, nil
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchSupportedPresente(t *testing.T) {
	srv := fakeWithTool(t, SearchTool)
	p := New(Options{URL: srv.URL}).(memory.SearchCapable)
	if !p.SearchSupported(context.Background()) {
		t.Fatal("a tool existe: deveria ser suportado")
	}
}

func TestSearchSupportedAusenteLogaUmaVezECacheia(t *testing.T) {
	srv := fakeWithTool(t, "outra_tool")
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	p := New(Options{URL: srv.URL, Logger: log})
	prov := p.(*provider)
	clock := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	prov.now = func() time.Time { return clock }
	ctx := context.Background()
	for i := 0; i < 5; i++ { // várias "teclas": uma só consulta e um só aviso
		if prov.SearchSupported(ctx) {
			t.Fatal("sem a tool memory_search: não deveria ser suportado")
		}
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Errorf("avisos = %d, quer 1:\n%s", n, buf.String())
	}
	// passado o TTL relê a lista, mas não repete o aviso
	clock = clock.Add(capTTL + time.Second)
	if prov.SearchSupported(ctx) {
		t.Fatal("continua ausente")
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Errorf("aviso repetido: %d", n)
	}
}

func TestSearchSupportedServidorForaSupoeSim(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	p := New(Options{URL: url, Timeout: 500 * time.Millisecond}).(memory.SearchCapable)
	if !p.SearchSupported(context.Background()) {
		t.Fatal("sem conseguir listar, a busca é quem marca indisponível: deve supor suportado")
	}
}
