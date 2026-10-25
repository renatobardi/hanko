package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// hanko_send_create (ADR-0069 §4, #59): a persona cria o envio pela tool. A política decide antes do envio: ask abre
// gate e, aprovado com "sempre permitir", entrega o link; off nega sem gate; pedido inválido nem chega ao gate.

// O link da tool abre o envio do campo: a chave está no fragmento, a página decifra o valor, e o link entra na redação
// da run, como o valor de hanko_secret_get (ADR-0068 §4.4).
func TestHankoSendCreateAskEntregaOLinkDepoisDaAprovacao(t *testing.T) {
	e := newToolsEnv(t)
	e.svc.PublicURL = "https://hanko.exemplo.com"
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "api-token", sentinelSenha)

	d, err := e.svc.PersonaSendCreate(ctx, e.co.ID, *c.RunID, service.PersonaSendInput{Item: "api-token", ExpiresIn: time.Hour})
	if err != nil || d.Decision != service.NativeAsk || d.Link != "" {
		t.Fatalf("sem política: %+v, %v; quer ask sem link", d, err)
	}
	if n := gatesOfKind(t, e, domain.GateTool); n != 1 {
		t.Fatalf("gates de tool abertos = %d, quer 1", n)
	}

	var gate domain.ID
	if err := e.svc.Pool.QueryRow(ctx, `SELECT id FROM gates WHERE company_id=$1 AND kind=$2`, e.co.ID, string(domain.GateTool)).Scan(&gate); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gate, service.DecideGateInput{Decision: domain.GateApproved, Always: true}); err != nil {
		t.Fatal(err)
	}

	d, err = e.svc.PersonaSendCreate(ctx, e.co.ID, *c.RunID, service.PersonaSendInput{Item: "api-token", ExpiresIn: time.Hour})
	if err != nil || d.Decision != service.NativeAllow {
		t.Fatalf("aprovada: %+v, %v; quer allow", d, err)
	}
	const prefixo = "https://hanko.exemplo.com/s/"
	publicID, chave, ok := strings.Cut(strings.TrimPrefix(d.Link, prefixo), "#")
	if !strings.HasPrefix(d.Link, prefixo) || !ok {
		t.Fatalf("link = %q, quer %s<id>#<chave>", d.Link, prefixo)
	}

	aberto, err := e.svc.OpenPublicSend(ctx, publicID, "")
	if err != nil {
		t.Fatalf("abrir o link: %v", err)
	}
	if plain, err := clienteDecifra(clienteAESKey(unb64u(t, chave), nil, nil), publicID, aberto.Ciphertext); err != nil || plain != sentinelSenha {
		t.Fatalf("o link não devolve o valor do campo: %q, %v", plain, err)
	}
	if !strings.Contains(strings.Join(e.svc.RunSecretValues(*c.RunID), "\n"), d.Link) {
		t.Fatal("o link não entrou na redação da run")
	}
}

// Política off nega a chamada: sem link e sem gate aberto.
func TestHankoSendCreatePoliticaOffNegaSemGate(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "api-token", sentinelSenha)
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSendCreateTool, domain.PolicyOff))

	d, err := e.svc.PersonaSendCreate(ctx, e.co.ID, *c.RunID, service.PersonaSendInput{Item: "api-token", ExpiresIn: time.Hour})
	if err != nil || d.Decision != service.NativeDeny || d.Link != "" {
		t.Fatalf("política off: %+v, %v; quer deny sem link", d, err)
	}
	if n := gatesOfKind(t, e, domain.GateTool); n != 0 {
		t.Fatalf("política off abriu %d gate(s)", n)
	}
}

// Pedido inválido não chega ao gate: item pessoal, campo fora do formato, prazo acima do máximo e item que não existe.
func TestHankoSendCreateValidaAntesDoGate(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "api-token", sentinelSenha)

	casos := []service.PersonaSendInput{
		{Item: "user/api-token", ExpiresIn: time.Hour},
		{Item: "api-token", Field: "campo com espaço", ExpiresIn: time.Hour},
		{Item: "api-token", ExpiresIn: domain.MaxSendTTL + time.Hour},
		{Item: "nao-existe", ExpiresIn: time.Hour},
	}
	for _, in := range casos {
		if d, err := e.svc.PersonaSendCreate(ctx, e.co.ID, *c.RunID, in); err == nil {
			t.Errorf("%+v: aceito (%+v); quer erro", in, d)
		}
	}
	if n := gatesOfKind(t, e, domain.GateTool); n != 0 {
		t.Fatalf("pedidos inválidos abriram %d gate(s); quer 0", n)
	}
}
