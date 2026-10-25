//go:build integration

package ui_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Pendências do #72 e do #86 pela UI (issue #95): o formulário de segredo escolhe a coleção, e a página da persona
// mostra o aviso de alcance do adapter_config, sem o valor.
func TestUIPendenciasIssue95(t *testing.T) {
	u := newSecretsUI(t)
	ctx := context.Background()
	const page = "/c/acme/configuracoes/segredos"
	if _, err := u.svc.CreateCollection(ctx, domain.HumanActor, u.co.ID, service.CollectionInput{Name: "infra"}); err != nil {
		t.Fatal(err)
	}

	// o formulário da empresa oferece as coleções; a padrão vem marcada
	b := bodyOf(u.get(page))
	if !strings.Contains(b, `name="collection"`) || !strings.Contains(b, `value="infra"`) ||
		!strings.Contains(b, `value="`+domain.DefaultCollectionName+`" selected`) {
		t.Fatalf("formulário sem a escolha de coleção: %s", b)
	}

	// criar escolhendo a coleção: o segredo entra nela e o valor não volta na tela
	rec := u.form(page, url.Values{"name": {"gh"}, "value": {uiSentinel}, "collection": {"infra"}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=criado") {
		t.Fatalf("criar na coleção: %d %s", rec.Code, bodyOf(rec))
	}
	items, err := u.svc.ListCollectionSecrets(ctx, u.co.ID, "infra")
	if err != nil || len(items) != 1 || items[0].Name != "gh" {
		t.Fatalf("itens de infra = %v (%v), quer gh", items, err)
	}

	// persona com referência a item que ela não alcança: a página mostra o aviso, nunca o valor
	if rec := u.form(page, url.Values{"name": {"restrito"}, "value": {uiSentinel}, "collection": {"infra"}}); rec.Code != 303 {
		t.Fatalf("criar restrito: %d", rec.Code)
	}
	p, err := u.svc.HirePersona(ctx, domain.HumanActor, u.co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake", Model: "m1",
		AdapterConfig: json.RawMessage(`{"api_key_ref":"${secret:restrito}"}`)})
	if err != nil {
		t.Fatal(err)
	}
	rec = u.get("/c/acme/personas/" + p.Slug)
	b = bodyOf(rec)
	if rec.Code != 200 || !strings.Contains(b, "não alcança") || !strings.Contains(b, "${secret:restrito}") || strings.Contains(b, uiSentinel) {
		t.Fatalf("aviso na página da persona: %d (valor na página? %v)\n%s", rec.Code, strings.Contains(b, uiSentinel), b)
	}
}
