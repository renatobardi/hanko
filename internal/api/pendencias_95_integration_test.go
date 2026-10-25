//go:build integration

package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// Pendências do #72 e do #86 pela API (issue #95): a coleção escolhida ao criar segredo e o aviso de alcance na
// contratação de uma persona.
func TestAPIColecaoNaCriacaoEAvisoDeAlcance(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	base := e.base()
	if _, err := e.svc.CreateCollection(context.Background(), domain.HumanActor, e.co.ID, service.CollectionInput{Name: "infra"}); err != nil {
		t.Fatal(err)
	}

	// coleção escolhida: o segredo entra nela; coleção que não existe responde 404 e não grava nada
	var s api.Secret
	e.want(201, "POST", base+"/secrets", `{"name":"restrito","value":"`+apiSentinel+`","collection":"infra"}`, &s)
	e.want(404, "POST", base+"/secrets", `{"name":"perdido","value":"x","collection":"nao-existe"}`, nil)
	var gravados int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM secrets WHERE name = 'perdido'`).Scan(&gravados); err != nil || gravados != 0 {
		t.Fatalf("segredo com coleção inexistente ficou gravado (%d, %v)", gravados, err)
	}

	// contratar com referência a um item que a persona não alcança: a contratação sai com o aviso, sem o valor
	var p api.Persona
	raw := e.want(201, "POST", base+"/personas", `{"name":"Ana","agent":"fake","model":"m1","adapter_config":{"api_key_ref":"${secret:restrito}"}}`, &p)
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "${secret:restrito}") || strings.Contains(string(raw), apiSentinel) {
		t.Fatalf("aviso na contratação: %+v\n%s", p.Warnings, raw)
	}

	// sem referência, a contratação não traz aviso
	var beto api.Persona
	e.want(201, "POST", base+"/personas", `{"name":"Beto","agent":"fake","model":"m1"}`, &beto)
	if len(beto.Warnings) != 0 {
		t.Fatalf("persona sem referência com aviso: %v", beto.Warnings)
	}
}

// A aprovação do gate de contratação traz o aviso de alcance (ADR-0068 §2.5), como a contratação direta: nomeia a
// persona e o item, nunca o valor.
func TestAPIAvisoDeAlcanceNaAprovacaoDoGate(t *testing.T) {
	e := newAPIEnv(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations.Secrets = box
	base := e.base()
	if _, err := e.svc.CreateCollection(context.Background(), domain.HumanActor, e.co.ID, service.CollectionInput{Name: "infra"}); err != nil {
		t.Fatal(err)
	}
	e.want(201, "POST", base+"/secrets", `{"name":"restrito","value":"`+apiSentinel+`","collection":"infra"}`, nil)

	// aprovar a contratação de uma persona cujo adapter_config referencia um item que ela não alcança: a decisão
	// sai com o aviso, sem o valor
	var g api.Gate
	e.want(201, "POST", base+"/gates", `{"title":"Contratar Ciro","kind":"hire","hire":{"name":"Ciro","agent":"fake","model":"m1","adapter_config":{"api_key_ref":"${secret:restrito}"}}}`, &g)
	var dec api.Gate
	raw := e.want(200, "POST", base+"/gates/"+g.ID+"/decide", `{"decision":"approved"}`, &dec)
	if len(dec.Warnings) != 1 || !strings.Contains(dec.Warnings[0], `"ciro"`) || !strings.Contains(dec.Warnings[0], "${secret:restrito}") || strings.Contains(string(raw), apiSentinel) {
		t.Fatalf("aviso na aprovação: %+v\n%s", dec.Warnings, raw)
	}

	// aprovar contratação sem referência inalcançável: sem aviso
	var g2 api.Gate
	e.want(201, "POST", base+"/gates", `{"title":"Contratar Duda","kind":"hire","hire":{"name":"Duda","agent":"fake","model":"m1"}}`, &g2)
	var dec2 api.Gate
	raw2 := e.want(200, "POST", base+"/gates/"+g2.ID+"/decide", `{"decision":"approved"}`, &dec2)
	if len(dec2.Warnings) != 0 || strings.Contains(string(raw2), apiSentinel) {
		t.Fatalf("aprovação sem pendência com aviso: %+v\n%s", dec2.Warnings, raw2)
	}

	// rejeitar não cria persona: sem aviso
	var g3 api.Gate
	e.want(201, "POST", base+"/gates", `{"title":"Contratar Eva","kind":"hire","hire":{"name":"Eva","agent":"fake","model":"m1","adapter_config":{"api_key_ref":"${secret:restrito}"}}}`, &g3)
	var dec3 api.Gate
	e.want(200, "POST", base+"/gates/"+g3.ID+"/decide", `{"decision":"rejected","note":"sem verba"}`, &dec3)
	if len(dec3.Warnings) != 0 {
		t.Fatalf("rejeição com aviso: %v", dec3.Warnings)
	}
}
