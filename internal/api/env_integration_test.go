//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// apiEnv é um servidor HTTP real sobre o service e um Postgres limpo, com relógio controlável
// (atômico: o servidor lê o relógio em outras goroutines) e run tokens habilitados.
type apiEnv struct {
	t      *testing.T
	svc    *service.Service
	url    string
	tokens *auth.RunTokens
	clock  atomic.Int64
	co     domain.Company
}

func newAPIEnv(t *testing.T) *apiEnv { return newAPIEnvWith(t, nil) }

// newAPIEnvWith permite ajustar as Deps (ex.: prazos e tetos de export/import) antes de montar o servidor.
func newAPIEnvWith(t *testing.T, mod func(*api.Deps)) *apiEnv {
	t.Helper()
	pool := testdb.New(t)
	broker := events.NewBroker()
	svc := service.New(pool, noopJobs{}, broker)
	e := &apiEnv{t: t, svc: svc, tokens: &auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}}
	e.setNow(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))
	svc.Now = func() time.Time { return time.Unix(0, e.clock.Load()).UTC() }

	mux := http.NewServeMux()
	deps := api.Deps{Svc: svc, Broker: broker, Logger: slog.New(slog.DiscardHandler), Version: "it", DB: pool, Tokens: e.tokens}
	if mod != nil {
		mod(&deps)
	}
	api.Mount(mux, deps)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	e.url = ts.URL

	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	e.co = co
	return e
}

func (e *apiEnv) setNow(tm time.Time) { e.clock.Store(tm.UnixNano()) }

// call faz uma requisição e devolve status e corpo.
func (e *apiEnv) call(method, path, body string, hdr map[string]string) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, e.url+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// want faz a requisição, exige o status e decodifica o corpo em out (se não nil).
func (e *apiEnv) want(status int, method, path, body string, out any) []byte {
	e.t.Helper()
	got, b := e.call(method, path, body, nil)
	if got != status {
		e.t.Fatalf("%s %s: status %d, quer %d: %s", method, path, got, status, b)
	}
	if out != nil {
		reflect.ValueOf(out).Elem().SetZero() // campos omitidos não podem herdar o valor de uma leitura anterior
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s %s: corpo inválido: %v: %s", method, path, err, b)
		}
	}
	return b
}

func (e *apiEnv) base() string { return "/api/v1/companies/" + e.co.Slug }

func (e *apiEnv) company(name, slug string) domain.Company {
	e.t.Helper()
	co, err := e.svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: name, Slug: slug})
	if err != nil {
		e.t.Fatal(err)
	}
	return co
}

func (e *apiEnv) persona(co domain.Company, name string, mod func(*service.HirePersonaInput)) domain.Persona {
	e.t.Helper()
	in := service.HirePersonaInput{Name: name, Agent: "fake", Model: "m1"}
	if mod != nil {
		mod(&in)
	}
	p, err := e.svc.HirePersona(context.Background(), domain.HumanActor, co.ID, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *apiEnv) task(co domain.Company, title string, p *domain.Persona, mod func(*service.CreateTaskInput)) service.TaskView {
	e.t.Helper()
	in := service.CreateTaskInput{Title: title}
	if p != nil {
		in.PersonaID = &p.ID
	}
	if mod != nil {
		mod(&in)
	}
	tv, err := e.svc.CreateTask(context.Background(), domain.HumanActor, co.ID, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return tv
}

func wakeOf(p domain.Persona, tv service.TaskView) service.Wake {
	return service.Wake{CompanyID: tv.CompanyID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign}
}

// spend roda uma execução inteira da persona no instante at e a termina com res.
func (e *apiEnv) spend(p domain.Persona, at time.Time, res adapter.Result) domain.Run {
	e.t.Helper()
	ctx := context.Background()
	e.setNow(at)
	tv := e.task(e.co, "gasto", &p, nil)
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv))
	if err != nil {
		e.t.Fatalf("checkout: %v", err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		e.t.Fatal(err)
	}
	done, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: res})
	if err != nil {
		e.t.Fatalf("finish: %v", err)
	}
	return done
}

// personaToken emite o run token de uma execução ativa da persona na empresa co.
func (e *apiEnv) personaToken(co domain.Company, p domain.Persona) map[string]string {
	e.t.Helper()
	tv := e.task(co, "da persona", &p, nil)
	run, err := e.svc.CheckoutRun(context.Background(), wakeOf(p, tv))
	if err != nil {
		e.t.Fatal(err)
	}
	tok, err := e.tokens.Issue(co.ID, p.ID, run.ID, tv.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + tok}
}

func usdRes(v float64) adapter.Result { return adapter.Result{CostUSD: &v} }

func ptr[T any](v T) *T { return &v }

func esc(s string) string { return url.PathEscape(s) }
