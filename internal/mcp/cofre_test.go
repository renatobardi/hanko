package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// secretFake é o fake de handler_test.go com a tool de cofre: a resposta vem do que o teste pôs, e a chamada fica guardada.
type secretFake struct {
	*fake
	res  service.SecretGetResult
	last service.SecretGetInput
}

func (f *secretFake) SecretGet(_ context.Context, _ domain.ID, in service.SecretGetInput) (service.SecretGetResult, error) {
	f.last = in
	return f.res, nil
}

func setupSecret(t *testing.T, res service.SecretGetResult) (*env, *secretFake) {
	t.Helper()
	f := newFake()
	sf := &secretFake{fake: f, res: res}
	return startToolEnv(t, f, sf), sf
}

// startToolEnv sobe o servidor MCP com o backend dado e um token de run da persona. A run fica ativa (o token só vale
// com ela, como em setup, handler_test.go).
func startToolEnv(t *testing.T, f *fake, b Backend) *env {
	t.Helper()
	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	runID := domain.NewID()
	tok, err := tokens.Issue(f.company.ID, f.personas[1].ID, runID, f.tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{f: f, tokens: tokens, tok: tok, runID: runID}
	started := time.Now()
	f.runs[runID] = service.RunView{Run: domain.Run{ID: runID, Status: domain.RunRunning, Trigger: domain.TriggerAssign, Agent: "fake", CreatedAt: started, StartedAt: &started},
		TaskKey: "HNK-1", TaskTitle: "Tarefa A", PersonaName: "Engenheira"}
	e.srv = httptest.NewServer(newHandler(b, Deps{Tokens: tokens, Version: "test"}))
	t.Cleanup(e.srv.Close)
	return e
}

// A tool aparece na lista (é a tool MCP da fatia 3 do cofre, ADR-0068 §4.1).
func TestSecretGetAparecNaLista(t *testing.T) {
	e, _ := setupSecret(t, service.SecretGetResult{})
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
		if tl.Name == service.HankoSecretGetTool {
			return
		}
	}
	t.Fatal("hanko_secret_get não está na lista de tools")
}

// Campo texto: a tool entrega o valor e manda o item e o campo para o service.
func TestSecretGetEntregaValorQuandoAllow(t *testing.T) {
	e, sf := setupSecret(t, service.SecretGetResult{Decision: service.NativeAllow, Kind: domain.SecretFieldText, Value: "senha-de-teste"})
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, service.HankoSecretGetTool, map[string]any{"item": "gh-login", "campo": "password"})
	if res.IsError || !strings.Contains(text(res), "senha-de-teste") {
		t.Fatalf("resposta: %+v", res)
	}
	if sf.last.Item != "gh-login" || sf.last.Field != "password" || sf.last.RunID != e.runID {
		t.Fatalf("o service recebeu %+v", sf.last)
	}
	var out secretGetOut
	structured(t, res, &out)
	if out.Decision != service.NativeAllow || out.Value != "senha-de-teste" {
		t.Fatalf("saída estruturada: %+v", out)
	}
}

// Campo TOTP: a resposta traz só o código do momento; a semente não aparece em nenhum campo da resposta.
func TestSecretGetTOTPSoCodigo(t *testing.T) {
	e, _ := setupSecret(t, service.SecretGetResult{Decision: service.NativeAllow, Kind: domain.SecretFieldTOTP, Code: "287082", ExpiresIn: 12})
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, service.HankoSecretGetTool, map[string]any{"item": "github", "campo": "totp"})
	if res.IsError || !strings.Contains(text(res), "287082") || !strings.Contains(text(res), "12 s") {
		t.Fatalf("resposta TOTP: %s", text(res))
	}
	var out secretGetOut
	structured(t, res, &out)
	if out.Code != "287082" || out.ExpiresIn != 12 || out.Value != "" {
		t.Fatalf("saída TOTP: %+v", out)
	}
}

// Sem aprovação: a chamada volta com erro e manda repetir com os mesmos argumentos depois da decisão, como as tools de plugin.
func TestSecretGetAskVoltaComErroParaRepetir(t *testing.T) {
	e, _ := setupSecret(t, service.SecretGetResult{Decision: service.NativeAsk, Reason: "aguardando aprovação: a chamada abriu o gate g1"})
	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, service.HankoSecretGetTool, map[string]any{"item": "gh-login"})
	if !res.IsError || !strings.Contains(text(res), "repita a chamada com exatamente os mesmos argumentos") || strings.Contains(text(res), "senha") {
		t.Fatalf("ask: %+v", res)
	}
}
