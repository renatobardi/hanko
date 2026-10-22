//go:build integration

package ui_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/oauth/oauthtest"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
	"github.com/renatobardi/hanko/internal/ui"
)

// UI das conexões OAuth (ADR-0043): a aba, o botão Autorizar (redireciona ao servidor de autorização) e o retorno do
// navegador em /oauth/callback.
func TestUIConnectionsTabAndCallback(t *testing.T) {
	pool := testdb.New(t)
	broker := events.NewBroker()
	svc := service.New(pool, noopJobs{}, broker)
	box, _ := secretbox.New("0123456789abcdef0123456789abcdef-ui", "")
	svc.Integrations = service.IntegrationsConfig{Secrets: box, AllowPrivate: true}
	svc.PublicURL = "https://hanko.example.test"
	mux := http.NewServeMux()
	ui.Mount(mux, ui.Deps{Svc: svc, Broker: broker, Logger: slog.New(slog.DiscardHandler), Version: "it",
		Platform: ui.PlatformInfo{PublicURL: "https://hanko.example.test"}})
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	u := &realUI{t: t, svc: svc, co: co, h: mux}
	fake := oauthtest.New()
	defer fake.Close()
	const page = "/c/acme/ferramentas/conexoes"

	rec := u.get(page)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Nenhuma conexão") || !strings.Contains(rec.Body.String(), "Nova conexão") {
		t.Fatalf("aba vazia: %d", rec.Code)
	}
	rec = u.form(page, url.Values{"name": {"Docs remoto"}, "url": {fake.MCPURL()}, "identity": {"company"}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=criada") {
		t.Fatalf("criar: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.get(page)
	if b := rec.Body.String(); !strings.Contains(b, "Docs remoto") || !strings.Contains(b, "rascunho") || !strings.Contains(b, "/autorizar") {
		t.Fatalf("lista: %s", b)
	}

	// Autorizar: redireciona para o servidor de autorização, sem referrer
	rec = u.form(page+"/docs-remoto/autorizar", url.Values{})
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, fake.URL+"/authorize?") || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("autorizar: %d %q", rec.Code, loc)
	}
	state, challenge, redirect, _ := oauthtest.ParseAuthURL(loc)
	if redirect != "https://hanko.example.test/oauth/callback" {
		t.Fatalf("redirect_uri = %q", redirect)
	}

	// o retorno do navegador: conecta e manda para a aba, com uma URL limpa
	rec = u.get("/oauth/callback?state=" + url.QueryEscape(state) + "&code=" + url.QueryEscape(fake.IssueCode(challenge)))
	if rec.Code != 303 || rec.Header().Get("Location") != page+"?ok=conectada" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("callback: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = u.get(page)
	if b := rec.Body.String(); !strings.Contains(b, "conectada") || strings.Contains(b, fake.AccessToken()) {
		t.Fatalf("depois de conectar: %s", b)
	}
	// repetir o retorno (state já usado) não vaza nada e dá 404
	rec = u.get("/oauth/callback?state=" + url.QueryEscape(state) + "&code=x")
	if rec.Code != 404 {
		t.Fatalf("state reusado: %d", rec.Code)
	}
	// testar e desconectar pela UI
	rec = u.form(page+"/docs-remoto/testar", url.Values{})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=testada") {
		t.Fatalf("testar: %d", rec.Code)
	}
	rec = u.form(page+"/docs-remoto/desconectar", url.Values{})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=desconectada") {
		t.Fatalf("desconectar: %d", rec.Code)
	}
	rec = u.form(page+"/docs-remoto/apagar", url.Values{})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=apagada") {
		t.Fatalf("apagar: %d", rec.Code)
	}
}
