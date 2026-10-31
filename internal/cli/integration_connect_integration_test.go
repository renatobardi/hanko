//go:build integration

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// hanko integration connect (ADR-0089) contra o servidor de verdade: do zero até a persona com escrita em aprovação.
func TestCLIIntegrationConnectContraServidorReal(t *testing.T) {
	svc, co, env := realServer(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-connect", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations = service.IntegrationsConfig{AllowPrivate: true, Secrets: box}
	up := sdk.NewServer(&sdk.Implementation{Name: "up", Version: "1"}, nil)
	up.AddTool(&sdk.Tool{Name: "ler", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{}, nil
		})
	notDestructive := false
	up.AddTool(&sdk.Tool{Name: "escrever", InputSchema: json.RawMessage(`{"type":"object"}`),
		Annotations: &sdk.ToolAnnotations{DestructiveHint: &notDestructive}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{}, nil
		})
	hs := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return up },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true}))
	t.Cleanup(hs.Close)
	if _, err := svc.HirePersona(context.Background(), domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake", Model: "m"}); err != nil {
		t.Fatal(err)
	}

	// passos 1 e 2: começa, descobre e mostra as tools por risco; a escrita nasce em quarentena.
	out := mustOK(t, env, "integration", "connect", "Docs", "--url", hs.URL+"/mcp", "--header", "Authorization=Bearer teste")
	if !strings.Contains(out, "ler") || !strings.Contains(out, "escrever") || !strings.Contains(out, "[quarentena]") {
		t.Fatalf("descoberta: %q", out)
	}

	// sem modelo nem persona: mostra o próximo passo e os modelos; não grava.
	out = mustOK(t, env, "integration", "connect", "docs")
	if !strings.Contains(out, "Nada foi gravado") || !strings.Contains(out, "modelos disponíveis: leitura-livre-escrita-com-aprovacao, somente-leitura") {
		t.Fatalf("próximo passo: %q", out)
	}
	// modelo faltando com persona escolhida: erro, nada gravado.
	mustFail(t, env, 1, "escolha o modelo de acesso com --template", "integration", "connect", "docs", "--persona", "ana")

	// passo 5 sem --yes: mostra o que a persona poderá fazer e não grava.
	code, out, errOut := exec(t, Runtime{}, env, "", "integration", "connect", "docs",
		"--template", "leitura-livre-escrita-com-aprovacao", "--persona", "ana")
	if code != 1 || !strings.Contains(errOut, "nada foi gravado") {
		t.Fatalf("prévia: código %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "liberada (ask)") || !strings.Contains(out, "escrever") {
		t.Fatalf("prévia sem o modo de escrita: %q", out)
	}

	// modelo sem persona: para antes de gravar, com o motivo.
	mustFail(t, env, 1, "escolha as personas com --persona", "integration", "connect", "docs", "--template", "somente-leitura")

	// conclusão com --yes: a persona recebe a integração; a escrita sai da quarentena com modo ask.
	out = mustOK(t, env, "integration", "connect", "docs", "--template", "leitura-livre-escrita-com-aprovacao", "--persona", "ana", "--yes")
	if !strings.Contains(out, "Conectado: Docs") {
		t.Fatalf("conclusão: %q", out)
	}
	out = mustOK(t, env, "integration", "connect", "docs")
	if strings.Contains(out, "[quarentena]") {
		t.Fatalf("depois de concluir, ainda há tool em quarentena: %q", out)
	}
}

// falha de descoberta: o comando para com o erro e oferece tentar de novo ou descartar (ADR-0089, item 11).
func TestCLIIntegrationConnectFalhaDeDescoberta(t *testing.T) {
	_, _, env := realServer(t)
	mustFail(t, env, 1, "descoberta falhou", "integration", "connect", "ruim", "--url", "http://127.0.0.1:1/mcp")
	// repetir a descoberta de um rascunho que continua falhando: o mesmo erro, nada gravado.
	mustFail(t, env, 1, "descoberta falhou", "integration", "connect", "ruim", "--discover")
	out := mustOK(t, env, "integration", "connect", "ruim", "--discard")
	if !strings.Contains(out, "Rascunho descartado: ruim") {
		t.Fatalf("descarte: %q", out)
	}
}
