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

// hanko integration tools, hanko tool-profile e hanko tool-gateway contra o servidor de verdade.
func TestCLIToolsProfilesAndGateways(t *testing.T) {
	svc, co, env := realServer(t)
	box, err := secretbox.New("0123456789abcdef0123456789abcdef-one", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations = service.IntegrationsConfig{AllowPrivate: true, Secrets: box}
	up := sdk.NewServer(&sdk.Implementation{Name: "up", Version: "1"}, nil)
	up.AddTool(&sdk.Tool{Name: "ler", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return &sdk.CallToolResult{}, nil })
	hs := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return up },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true}))
	t.Cleanup(hs.Close)
	if _, err := svc.CreateIntegration(context.Background(), domain.HumanActor, co.ID, service.CreateIntegrationInput{Kind: domain.IntegrationMCP,
		Name: "Docs", URL: hs.URL + "/mcp", AllPersonas: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HirePersona(context.Background(), domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake", Model: "m"}); err != nil {
		t.Fatal(err)
	}

	out := mustOK(t, env, "integration", "tools", "docs")
	if !strings.Contains(out, "Nenhuma tool catalogada") {
		t.Fatalf("antes da descoberta: %q", out)
	}
	out = mustOK(t, env, "integration", "tools", "docs", "--refresh")
	if !strings.Contains(out, "Catálogo atualizado: 1 tools") || !strings.Contains(out, "ler") || !strings.Contains(out, "leitura") {
		t.Fatalf("refresh: %q", out)
	}
	out = mustOK(t, env, "integration", "tools", "docs", "--disable", "ler")
	if !strings.Contains(out, "desligada") {
		t.Fatalf("desligar: %q", out)
	}
	mustOK(t, env, "integration", "tools", "docs", "--enable", "ler")

	out = mustOK(t, env, "tool-profile", "create", "Seguro", "--rate", "30")
	if !strings.Contains(out, "Perfil criado: Seguro (seguro)") {
		t.Fatalf("create: %q", out)
	}
	mustOK(t, env, "tool-profile", "rule", "seguro", "--integration", "docs", "--ask")
	mustFail(t, env, 2, "informe --integration", "tool-profile", "rule", "seguro")
	mustOK(t, env, "tool-profile", "bind", "seguro", "--persona", "ana")
	mustFail(t, env, 2, "escolha um escopo", "tool-profile", "bind", "seguro", "--persona", "ana", "--company-wide")
	out = mustOK(t, env, "tool-profile", "show", "seguro")
	if !strings.Contains(out, "docs (inteira)") || !strings.Contains(out, "persona") || !strings.Contains(out, "30/min") {
		t.Fatalf("show: %q", out)
	}
	out = mustOK(t, env, "tool-profile", "effective", "--persona", "ana")
	if !strings.Contains(out, "seguro (persona Ana)") || !strings.Contains(out, "perguntar antes (regra)") {
		t.Fatalf("effective: %q", out)
	}
	out = mustOK(t, env, "tool-profile", "list")
	if !strings.Contains(out, "seguro") || !strings.Contains(out, "nega") {
		t.Fatalf("list: %q", out)
	}

	out = mustOK(t, env, "tool-gateway", "create", "Minha IDE", "--profile", "seguro")
	if !strings.Contains(out, "Gateway criado: Minha IDE (minha-ide)") {
		t.Fatalf("gateway: %q", out)
	}
	out = mustOK(t, env, "tool-gateway", "token", "minha-ide", "--label", "notebook", "--action", "tools/list")
	if !strings.Contains(out, "mostrado só agora") || !strings.Contains(out, "hkgw_") || !strings.Contains(out, "/mcp/gw/acme/minha-ide") {
		t.Fatalf("token: %q", out)
	}
	tok := out[strings.Index(out, "hkgw_"):]
	tok = tok[:strings.IndexAny(tok, " \n")]
	out = mustOK(t, env, "tool-gateway", "show", "minha-ide")
	if strings.Contains(out, tok) || !strings.Contains(out, tok[:11]) || !strings.Contains(out, "ativo") {
		t.Fatalf("show não pode mostrar o token: %q", out)
	}
	out = mustOK(t, env, "tool-gateway", "revoke", "minha-ide", tok[:11])
	if !strings.Contains(out, "revogado") {
		t.Fatalf("revoke: %q", out)
	}
	out = mustOK(t, env, "tool-gateway", "list")
	if !strings.Contains(out, "minha-ide") || !strings.Contains(out, "seguro") {
		t.Fatalf("list: %q", out)
	}
	mustOK(t, env, "tool-gateway", "delete", "minha-ide")
	mustOK(t, env, "tool-profile", "delete", "seguro")
}
