package app

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// O token do convite viaja no caminho; nem o nome do span nem nenhum atributo pode carregá-lo.
func TestInviteTokenNeverReachesSpans(t *testing.T) {
	pool := testdb.New(t)
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev); _ = tp.Shutdown(t.Context()) })

	svc := service.New(pool, noopJobs{}, events.NewBroker())
	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	h, err := buildHandler(handlerDeps{Cfg: authCfg(), Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool, Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	const secret = "SEGREDO-do-convite-123"
	for _, c := range []call{
		{method: "GET", path: "/convite/" + secret + "?x=" + secret},
		{method: "POST", path: "/convite/" + secret, body: "name=a", hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "application/x-www-form-urlencoded"}},
	} {
		if resp, _ := send(t, ts.URL, c); resp.StatusCode != 404 { // token inexistente: a rota ainda chega ao handler
			t.Fatalf("%s: %d", c.method, resp.StatusCode)
		}
	}
	spans := exp.GetSpans()
	if len(spans) < 2 {
		t.Fatalf("esperava spans das requisições, vieram %d", len(spans))
	}
	for _, sp := range spans {
		if strings.Contains(sp.Name, secret) {
			t.Errorf("nome do span vaza o token: %q", sp.Name)
		}
		if !strings.Contains(sp.Name, "/convite/…") {
			t.Errorf("nome do span deveria mascarar o caminho: %q", sp.Name)
		}
		for _, a := range sp.Attributes {
			if strings.Contains(a.Value.String(), secret) {
				t.Errorf("atributo %s vaza o token: %q", a.Key, a.Value.String())
			}
		}
	}
}
