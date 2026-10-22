package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter/acp"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
	"github.com/renatobardi/hanko/internal/webhook"
)

// No modo authenticated as rotas /hooks/ ficam fora do login: quem se autentica é o HMAC do gatilho e o segredo do canal.
func TestWiringHooksArePublic(t *testing.T) {
	pool := testdb.New(t)
	svc := service.New(pool, nil, events.NewBroker())
	svc.ACP = acp.Policy{Allow: true}
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-chave-de-teste", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations.Secrets = box
	ctx := context.Background()
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ada", Agent: "fake", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := svc.CreateFlow(ctx, domain.HumanActor, co.ID, service.CreateFlowInput{Name: "Entrada", PersonaID: p.ID, TaskTitle: "Chegou"})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := svc.CreateFlowTrigger(ctx, domain.HumanActor, co.ID, f.ID, service.CreateFlowTriggerInput{Kind: domain.TriggerKindWebhook, Name: "h"})
	if err != nil {
		t.Fatal(err)
	}

	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: 3600e9}
	h, err := buildHandler(handlerDeps{Cfg: authCfg(), Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool, Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	// sem credencial de login: o gatilho responde por si (401 sem assinatura, 202 com ela), nunca redireciona ao login
	resp, body := send(t, ts.URL, call{method: "POST", path: tr.Path, body: `{}`})
	if resp.StatusCode != 401 {
		t.Fatalf("sem assinatura: %d %s", resp.StatusCode, body)
	}
	sig := webhook.Sign(tr.SecretValue, svc.Now(), []byte(`{}`))
	resp, body = send(t, ts.URL, call{method: "POST", path: tr.Path, body: `{}`, hdr: map[string]string{webhook.HeaderSignature: sig}})
	if resp.StatusCode != 202 || !strings.Contains(body, "task_key") {
		t.Fatalf("com assinatura: %d %s", resp.StatusCode, body)
	}
	// canal inexistente: 404 do próprio hook (não a página de login)
	resp, body = send(t, ts.URL, call{method: "POST", path: "/hooks/channels/nao-existe", body: `{}`})
	if resp.StatusCode != 404 || !strings.Contains(body, "canal") {
		t.Fatalf("canal inexistente: %d %s", resp.StatusCode, body)
	}
	// GET nessas rotas não existe
	if resp, _ = send(t, ts.URL, call{method: "GET", path: tr.Path}); resp.StatusCode == 202 || resp.StatusCode == 200 {
		t.Fatalf("GET no hook: %d", resp.StatusCode)
	}
}
