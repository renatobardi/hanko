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
