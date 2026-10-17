package heartbeat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRebaseMCP(t *testing.T) {
	in := `{"mcpServers":{"hanko":{"type":"http","url":"http://127.0.0.1:7420/mcp"},"ai-memory":{"type":"http","url":"http://127.0.0.1:7420/memory-mcp"},` +
		`"docs":{"type":"http","url":"https://docs.interno/mcp"},"local":{"command":"meu-mcp","env":{"K":"segredo"}}}}`
	out, dropped, err := rebaseMCP(json.RawMessage(in), "http://127.0.0.1:7420", "https://hanko.example.com")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"https://hanko.example.com/mcp"`, `"https://hanko.example.com/memory-mcp"`, `"https://docs.interno/mcp"`} {
		if !strings.Contains(s, want) {
			t.Errorf("faltou %s em %s", want, s)
		}
	}
	if strings.Contains(s, "meu-mcp") || strings.Contains(s, "segredo") || len(dropped) != 1 || dropped[0] != "local" {
		t.Fatalf("stdio deveria sair: %s %v", s, dropped)
	}
	if out, _, err := rebaseMCP(nil, "a", "b"); err != nil || out != nil {
		t.Fatalf("vazio: %s %v", out, err)
	}
}
