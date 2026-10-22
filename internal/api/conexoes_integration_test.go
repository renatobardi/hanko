//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/oauth/oauthtest"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// API das conexões OAuth (ADR-0043): criar o rascunho, autorizar (URL), voltar do navegador, probe, o pedido da persona
// por run token e a garantia de que nenhuma resposta traz token.
func TestAPIConnectionsFlow(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-two", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{AllowPrivate: true, Secrets: box}
	e.svc.PublicURL = "https://hanko.example.test"
	fake := oauthtest.New()
	defer fake.Close()
	base := e.base()

	var c api.Connection
	e.want(201, "POST", base+"/connections", `{"name":"Docs remoto","url":"`+fake.MCPURL()+`"}`, &c)
	if c.Slug != "docs-remoto" || c.Status != "draft" || c.Setup != "authorize" || c.IntegrationSlug != "" && c.IntegrationID == "" {
		t.Fatalf("rascunho: %+v", c)
	}
	e.want(409, "POST", base+"/connections", `{"name":"Docs remoto","url":"`+fake.MCPURL()+`"}`, nil)
	e.want(422, "POST", base+"/connections", `{"name":"Ruim","url":"https://u:p@x.example/mcp"}`, nil)
	e.want(404, "GET", base+"/connections/nao-existe", "", nil)

	// a persona pede (run token): abre o gate; a persona não autoriza
	ana := e.persona(e.co, "Ana", nil)
	hdr := e.personaToken(e.co, ana)
	code, body := e.call("POST", base+"/connections/request", `{"slug":"docs-remoto","reason":"ler a documentação"}`, hdr)
	if code != 200 {
		t.Fatalf("request: %d %s", code, body)
	}
	if !strings.Contains(string(body), `"kind":"connection"`) || !strings.Contains(string(body), `"connected":false`) {
		t.Fatalf("o pedido deveria abrir um gate connection: %s", body)
	}
	if code, _ := e.call("POST", base+"/connections/docs-remoto/authorize", "", hdr); code != 403 {
		t.Fatalf("persona autorizando: %d", code)
	}
	if code, _ := e.call("POST", base+"/connections", `{"name":"X","url":"`+fake.MCPURL()+`"}`, hdr); code != 403 {
		t.Fatalf("persona criando conexão: %d", code)
	}

	// o humano autoriza e volta do navegador
	var st api.ConnectionAuthStart
	e.want(200, "POST", base+"/connections/docs-remoto/authorize", "", &st)
	state, challenge, _, err := oauthtest.ParseAuthURL(st.AuthorizationURL)
	if err != nil || state == "" || !strings.Contains(st.AuthorizationURL, "code_challenge_method=S256") {
		t.Fatalf("url: %s %v", st.AuthorizationURL, err)
	}
	if _, err := e.svc.CompleteConnectionAuth(t.Context(), domain.HumanActor, service.CompleteConnectionInput{State: state, Code: fake.IssueCode(challenge)}); err != nil {
		t.Fatal(err)
	}

	raw := e.want(200, "GET", base+"/connections/docs-remoto", "", &c)
	if c.Status != "connected" || c.ExpiresAt == nil || c.GateID == "" {
		t.Fatalf("conectada: %+v", c)
	}
	if tok := fake.AccessToken(); strings.Contains(string(raw), tok) {
		t.Fatalf("a API vazou o token: %s", raw)
	}
	var g api.Gate
	e.want(200, "GET", base+"/gates/"+c.GateID, "", &g)
	if g.Status != "approved" || g.Kind != "connection" {
		t.Fatalf("o gate deveria estar aprovado: %+v", g)
	}

	e.want(200, "POST", base+"/connections/docs-remoto/probe", "", &c)
	if c.LastProbeStatus != "ok" {
		t.Fatalf("probe: %+v", c)
	}
	e.want(200, "POST", base+"/connections/docs-remoto/refresh", "", &c)
	var list api.ListBody[api.Connection]
	e.want(200, "GET", base+"/connections", "", &list)
	if len(list.Items) != 1 {
		t.Fatalf("lista: %+v", list.Items)
	}
	e.want(200, "POST", base+"/connections/docs-remoto/disconnect", "", &c)
	if c.Status != "draft" {
		t.Fatalf("desconectada: %+v", c)
	}
	e.want(204, "DELETE", base+"/connections/docs-remoto", "", nil)
	e.want(404, "GET", base+"/connections/docs-remoto", "", nil)
}
