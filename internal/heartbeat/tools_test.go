package heartbeat

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// As integrações MCP http da run apontam para o gateway de tools do Hanko, com o run token (ADR-0036).
func TestBuildSpecRoutesHTTPIntegrationsThroughToolGateway(t *testing.T) {
	h, _ := newTestHB(t, &fakeBackend{}, okAdapter(), &mcpMem{}, Options{PublicURL: "http://h:7420"})
	rc := sampleContext()
	spec, cleanup, err := h.buildSpec(rc, "", "", memory.Scope{Workspace: "acme"}, service.RunExtras{ToolGateway: []string{"docs"}},
		func(domain.RunEventKind, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	srv := mcpServers(t, spec)
	docs := srv["docs"]
	if docs["url"] != "http://h:7420/mcp/run/docs" || docs["type"] != "http" {
		t.Fatalf("docs: %s", spec.MCPConfig)
	}
	hdr, _ := docs["headers"].(map[string]any)
	auth, _ := hdr["Authorization"].(string)
	hankoHdr, _ := srv["hanko"]["headers"].(map[string]any)
	if !strings.HasPrefix(auth, "Bearer ") || auth != hankoHdr["Authorization"] {
		t.Fatalf("o gateway usa o run token: %q vs %v", auth, hankoHdr)
	}
}

func TestToolGatewayMCPWithoutToken(t *testing.T) {
	if got := toolGatewayMCP("http://h", "", []string{"docs"}); got != nil {
		t.Fatalf("sem token não há gateway: %v", got)
	}
	if got := toolGatewayMCP("http://h", "tok", []string{"a b"}); !strings.Contains(string(got["a b"]), "/mcp/run/a%20b") {
		t.Fatalf("slug escapado: %s", got["a b"])
	}
}
