//go:build integration

package ui_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
	"github.com/renatobardi/hanko/internal/ui"
)

// Assistente Conectar na tela (ADR-0089): do servidor à persona lendo issues com escrita pedindo aprovação, num fluxo
// só pela UI; e a descoberta que falha, com tentar de novo e descartar.

// newConnectUI é uma empresa "acme" com a UI montada sobre o service real.
func newConnectUI(t *testing.T) (*realUI, *service.Service) {
	t.Helper()
	pool := testdb.New(t)
	broker := events.NewBroker()
	svc := service.New(pool, noopJobs{}, broker)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-conectar", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations = service.IntegrationsConfig{Secrets: box, AllowPrivate: true}
	svc.PublicURL = "https://hanko.example.test"
	mux := http.NewServeMux()
	ui.Mount(mux, ui.Deps{Svc: svc, Broker: broker, Logger: slog.New(slog.DiscardHandler), Version: "it",
		Platform: ui.PlatformInfo{PublicURL: "https://hanko.example.test"}})
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	return &realUI{t: t, svc: svc, co: co, h: mux}, svc
}

// newConnectUpstream é um servidor MCP de teste com ler (leitura), escrever (escrita) e apagar (destrutiva).
func newConnectUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := sdk.NewServer(&sdk.Implementation{Name: "upstream", Version: "1"}, nil)
	add := func(name string, ann *sdk.ToolAnnotations) {
		srv.AddTool(&sdk.Tool{Name: name, Description: "tool " + name, InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: ann},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok " + name}}}, nil
			})
	}
	no, yes := false, true
	add("ler", &sdk.ToolAnnotations{ReadOnlyHint: true})
	add("escrever", &sdk.ToolAnnotations{DestructiveHint: &no})
	add("apagar", &sdk.ToolAnnotations{DestructiveHint: &yes})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	hs := httptest.NewServer(h)
	t.Cleanup(hs.Close)
	return hs
}

func TestUIConectarFluxoFeliz(t *testing.T) {
	u, svc := newConnectUI(t)
	ctx := context.Background()
	up := newConnectUpstream(t)
	lia, err := svc.HirePersona(ctx, domain.HumanActor, u.co.ID, service.HirePersonaInput{Name: "Lia", Agent: "fake", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "Bearer segredo-do-teste-nao-volta"

	// passo 1 e 2: o servidor e a descoberta; o segredo não volta na tela
	rec := u.form("/c/acme/conectar", url.Values{"name": {"GitHub"}, "url": {up.URL + "/mcp"}, "auth": {"header"},
		"headers": {"Authorization: " + secret}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/c/acme/conectar/github" {
		t.Fatalf("passo 1: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	rec = u.get("/c/acme/conectar/github")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Passo 3 de 5") || !strings.Contains(body, "Lia") {
		t.Fatalf("passo 3 sem persona escolhida: %d", rec.Code)
	}
	if strings.Contains(body, "Passo 5 de 5") || strings.Contains(body, "segredo-do-teste-nao-volta") {
		t.Fatalf("passo 5 aparece sem escolha, ou o segredo vazou")
	}

	// passos 4 e 5: escolhas na URL; nenhum vem marcado sozinho, e o modelo sugerido só aparece com a etiqueta
	tpls, err := svc.ListAccessTemplates(ctx, u.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	var tpl domain.AccessTemplate
	for _, x := range tpls {
		if x.Slug == "leitura-livre-escrita-com-aprovacao" {
			tpl = x
		}
	}
	if tpl.ID == (domain.ID{}) {
		t.Fatal("modelo sugerido não semeado")
	}
	rec = u.get("/c/acme/conectar/github?personas=" + lia.ID.String() + "&template=" + tpl.ID.String())
	body = rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Passo 5 de 5") || !strings.Contains(body, "perguntar antes") {
		t.Fatalf("passo 5: %d, sem 'perguntar antes' para a escrita", rec.Code)
	}

	// concluir: a persona recebe a integração, e a escrita passa a pedir aprovação
	rec = u.form("/c/acme/conectar/github/concluir", url.Values{"template": {tpl.ID.String()}, "personas": {lia.ID.String()}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/c/acme/integracoes/github?ok=conectada" {
		t.Fatalf("concluir: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	eff, err := svc.EffectiveToolsFor(ctx, u.co.ID, lia.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]domain.ToolMode{}
	for _, ei := range eff.Integrations {
		for _, et := range ei.Tools {
			modes[et.Tool.Name] = et.Verdict.Mode
		}
	}
	if modes["escrever"] != domain.ToolModeAsk || modes["apagar"] != domain.ToolModeAsk || modes["ler"] != domain.ToolModeAllow {
		t.Fatalf("modos efetivos da persona: %v", modes)
	}
}

func TestUIConectarDescobertaFalha(t *testing.T) {
	u, svc := newConnectUI(t)
	ctx := context.Background()
	dead := newConnectUpstream(t)
	deadURL := dead.URL + "/mcp"
	dead.Close()

	rec := u.form("/c/acme/conectar", url.Values{"name": {"Morto"}, "url": {deadURL}, "auth": {"header"},
		"headers": {"Authorization: token-qualquer"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/c/acme/conectar/morto" {
		t.Fatalf("begin com servidor fora: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = u.get("/c/acme/conectar/morto")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Passo 2 de 5: a descoberta falhou") ||
		!strings.Contains(body, "Tentar de novo") || !strings.Contains(body, "Descartar rascunho") {
		t.Fatalf("passo 2 sem tentar de novo / descartar: %d", rec.Code)
	}
	if strings.Contains(body, "Passo 3 de 5") {
		t.Fatal("persona e modelo aparecem com a descoberta falha")
	}

	// tentar de novo: continua na mesma integração, sem liberar nada
	rec = u.form("/c/acme/conectar/morto/descobrir", url.Values{})
	if rec.Code != 303 {
		t.Fatalf("tentar de novo: %d %s", rec.Code, rec.Body.String())
	}

	// descartar: a integração criada pelo assistente some
	rec = u.form("/c/acme/conectar/morto/descartar", url.Values{})
	if rec.Code != 303 || rec.Header().Get("Location") != "/c/acme/conectar?ok=descartado" {
		t.Fatalf("descartar: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec = u.get("/c/acme/conectar?ok=descartado"); !strings.Contains(rec.Body.String(), "Rascunho descartado") {
		t.Fatal("aviso de descarte ausente")
	}
	if _, err := svc.GetIntegration(ctx, u.co.ID, "morto"); err == nil {
		t.Fatal("integração do rascunho descartado ainda existe")
	}
}
