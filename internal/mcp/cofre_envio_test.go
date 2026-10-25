package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// envioFake é o fake de handler_test.go com a tool de envio: a resposta vem do que o teste pôs, e a entrada fica guardada.
type envioFake struct {
	*fake
	res  service.PersonaSendResult
	last service.PersonaSendInput
}

func (f *envioFake) PersonaSendCreate(_ context.Context, _, _ domain.ID, in service.PersonaSendInput) (service.PersonaSendResult, error) {
	f.last = in
	return f.res, nil
}

func setupEnvio(t *testing.T, res service.PersonaSendResult) (*env, *envioFake) {
	t.Helper()
	ef := &envioFake{fake: newFake(), res: res}
	return startToolEnv(t, ef.fake, ef), ef
}

// A tool aparece na lista (ADR-0069 §4: tool MCP da persona, política ask).
func TestSendCreateAparecNaLista(t *testing.T) {
	e, _ := setupEnvio(t, service.PersonaSendResult{})
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name == service.HankoSendCreateTool {
			return
		}
	}
	t.Fatalf("a tool %s não aparece na lista", service.HankoSendCreateTool)
}

// Com a política liberada, a chamada devolve o link e leva os argumentos ao service (prazo em segundos, limite de acessos).
func TestSendCreateEntregaOLink(t *testing.T) {
	const link = "https://hanko.exemplo.com/s/AAAAAAAAAAAAAAAAAAAAAA#chave"
	expira := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	e, ef := setupEnvio(t, service.PersonaSendResult{Decision: service.NativeAllow, Link: link, ExpiresAt: expira})
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, service.HankoSendCreateTool, map[string]any{"item": "api-token", "campo": "password", "expira_em": 3600, "max_acessos": 5})
	if res.IsError || !strings.Contains(text(res), link) {
		t.Fatalf("resposta: %s", text(res))
	}
	var out sendCreateOut
	structured(t, res, &out)
	if out.Decision != service.NativeAllow || out.Link != link {
		t.Fatalf("saída: %+v", out)
	}
	if ef.last.Item != "api-token" || ef.last.Field != "password" || ef.last.ExpiresIn != time.Hour ||
		ef.last.MaxAccesses == nil || *ef.last.MaxAccesses != 5 {
		t.Fatalf("entrada do service: %+v", ef.last)
	}
}

// Sem aprovação: a chamada volta com erro e manda repetir com os mesmos argumentos, e o link não sai.
func TestSendCreateAskVoltaComErroParaRepetir(t *testing.T) {
	e, _ := setupEnvio(t, service.PersonaSendResult{Decision: service.NativeAsk, Reason: "aguardando aprovação: a chamada abriu o gate g1"})
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, service.HankoSendCreateTool, map[string]any{"item": "api-token"})
	if !res.IsError || !strings.Contains(text(res), "repita a chamada com exatamente os mesmos argumentos") || strings.Contains(text(res), "Link") {
		t.Fatalf("ask: %+v", res)
	}
}

// Negado pela política: a chamada volta com o motivo e sem o pedido de repetir.
func TestSendCreateNegadoVoltaComMotivo(t *testing.T) {
	e, _ := setupEnvio(t, service.PersonaSendResult{Decision: service.NativeDeny, Reason: "política da empresa: tool desligada"})
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, service.HankoSendCreateTool, map[string]any{"item": "api-token"})
	if !res.IsError || !strings.Contains(text(res), "tool desligada") || strings.Contains(text(res), "repita") {
		t.Fatalf("deny: %+v", res)
	}
}
