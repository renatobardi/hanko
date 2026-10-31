package service_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/pricecheck"
	"github.com/renatobardi/hanko/internal/service"
)

// fakeSources serve o models.dev e o OpenRouter com o preço que o teste escolher.
type fakeSources struct {
	mu        sync.Mutex
	dev, or   [2]float64 // entrada e saída, em USD por 1M tokens
	orDown    bool
	devServer *httptest.Server
	orServer  *httptest.Server
}

func newFakeSources(t *testing.T) *fakeSources {
	f := &fakeSources{dev: [2]float64{15, 75}, or: [2]float64{15, 75}}
	f.devServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		fmt.Fprintf(w, `{"anthropic":{"models":{"claude-opus-5-5":{"cost":{"input":%v,"output":%v}}}},"openai":{"models":{}}}`, f.dev[0], f.dev[1])
	}))
	f.orServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.orDown {
			http.Error(w, "fora", http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, `{"data":[{"id":"anthropic/claude-opus-5.5","pricing":{"prompt":"%.9f","completion":"%.9f"}}]}`, f.or[0]/1e6, f.or[1]/1e6)
	}))
	t.Cleanup(func() { f.devServer.Close(); f.orServer.Close() })
	return f
}

func (f *fakeSources) set(dev, or [2]float64, orDown bool) {
	f.mu.Lock()
	f.dev, f.or, f.orDown = dev, or, orDown
	f.mu.Unlock()
}

// A conferência troca o preço só quando as duas fontes concordam; a trava, a divergência e a fonte fora deixam o preço como
// está; cada conferência guarda o resultado por fonte e por modelo, e a falha só o código (ADR-0082 item 7).
func TestPriceCheckRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	src := newFakeSources(t)
	e.svc.PriceCheck = service.PriceCheckConfig{Client: pricecheck.NoRedirect(http.DefaultClient), AllowHTTP: true,
		URLs: map[string]string{pricecheck.ModelsDev: src.devServer.URL, pricecheck.OpenRouter: src.orServer.URL}}

	// um modelo em uso: uma chamada externa de claude-opus-5-5
	actor := ingestActor(t, e)
	token, _, err := e.svc.CreateIngestKey(ctx, actor, e.co.ID, "oute-server")
	if err != nil {
		t.Fatal(err)
	}
	key, err := e.svc.ResolveIngestKey(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ingest.SpanRows(spanPayload(t, oneSpan("p1", "claude_code.llm_request", 0, "")), time.Now().UnixNano()+10)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Spans: rows}); err != nil {
		t.Fatal(err)
	}
	exact := func() (n int, in float64, locked bool) {
		if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*), COALESCE(max(input_per_mtok)::float8, 0), COALESCE(bool_or(locked), false) FROM model_prices WHERE model = 'claude-opus-5-5'`).Scan(&n, &in, &locked); err != nil {
			t.Fatal(err)
		}
		return n, in, locked
	}
	status := func(sum service.PriceCheckSummary) (out string) {
		if err := e.svc.Pool.QueryRow(ctx, `SELECT string_agg(DISTINCT status, ',') FROM price_check_result WHERE run_id = $1 AND model = 'claude-opus-5-5'`, sum.RunID).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	sum, err := e.svc.RunPriceCheck(ctx, "manual")
	if err != nil || status(sum) != "new" || len(sum.Failures) != 0 {
		t.Fatalf("1ª conferência: status %q, falhas %v (%v), quer new", status(sum), sum.Failures, err)
	}
	if n, in, _ := exact(); n != 1 || in != 15 {
		t.Fatalf("o preço novo entra como linha: %d linhas, entrada %v", n, in)
	}
	if sum, _ = e.svc.RunPriceCheck(ctx, "daily"); status(sum) != "equal" {
		t.Errorf("sem mudança: %q, quer equal", status(sum))
	}

	time.Sleep(1100 * time.Millisecond) // o preço novo precisa de um início de vigência diferente
	src.set([2]float64{20, 100}, [2]float64{20, 100}, false)
	if sum, _ = e.svc.RunPriceCheck(ctx, "daily"); status(sum) != "changed" || sum.Changes != 1 {
		t.Errorf("as duas fontes concordam num preço novo: %q (%d trocas), quer changed", status(sum), sum.Changes)
	}
	if n, in, _ := exact(); n != 2 || in != 20 {
		t.Errorf("depois da troca: %d linhas, a última com entrada %v", n, in)
	}

	src.set([2]float64{30, 120}, [2]float64{25, 120}, false)
	if sum, _ = e.svc.RunPriceCheck(ctx, "daily"); status(sum) != "diverges" {
		t.Errorf("as fontes divergem: %q, quer diverges", status(sum))
	}
	src.set([2]float64{30, 120}, [2]float64{30, 120}, true)
	sum, _ = e.svc.RunPriceCheck(ctx, "daily")
	if status(sum) != "source_down" || sum.Failures[pricecheck.OpenRouter] != pricecheck.ErrHTTP {
		t.Errorf("OpenRouter fora: %q, falhas %v; quer source_down e o código http", status(sum), sum.Failures)
	}
	if n, in, _ := exact(); n != 2 || in != 20 {
		t.Errorf("fonte fora não troca o preço: %d linhas, entrada %v", n, in)
	}

	// travado: as fontes concordam num valor diferente e o preço fica
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE model_prices SET locked = true WHERE model = 'claude-opus-5-5'`); err != nil {
		t.Fatal(err)
	}
	src.set([2]float64{30, 120}, [2]float64{30, 120}, false)
	if sum, _ = e.svc.RunPriceCheck(ctx, "daily"); status(sum) != "locked_differs" {
		t.Errorf("preço travado: %q, quer locked_differs", status(sum))
	}
	if n, in, locked := exact(); n != 2 || in != 20 || !locked {
		t.Errorf("a fonte nunca troca o preço travado: %d linhas, entrada %v, travado %v", n, in, locked)
	}

	list, err := e.svc.ListPriceChecks(ctx, e.co.ID, 50, 0)
	if err != nil || len(list) != 6 || len(list[0].Sources) != 2 {
		t.Fatalf("ListPriceChecks = %d conferências (%v), quer 6 com 2 fontes cada", len(list), err)
	}
	var codes int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM price_check_source WHERE status = 'down' AND error_code = 'http'`).Scan(&codes); err != nil || codes != 1 {
		t.Errorf("falhas guardadas com o código = %d (%v), quer 1", codes, err)
	}
	detail, err := e.svc.GetPriceCheck(ctx, e.co.ID, list[0].ID)
	if err != nil || len(detail.Results) == 0 {
		t.Errorf("GetPriceCheck = %+v (%v)", detail, err)
	}
	if _, err := e.svc.GetPriceCheck(ctx, e.co.ID, domain.NewID()); err == nil {
		t.Error("conferência inexistente deveria ser recusada")
	}
}
