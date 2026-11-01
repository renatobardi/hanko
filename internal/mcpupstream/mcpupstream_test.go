package mcpupstream

import (
	"encoding/json"
	"testing"
)

func TestHeaderParams(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"owner":{"type":"string","x-mcp-header":"owner"},
		"repo":{"type":"string","x-mcp-header":"repo"},
		"page":{"type":"number","x-mcp-header":"page"},
		"draft":{"type":"boolean","x-mcp-header":"draft"},
		"labels":{"type":"array","x-mcp-header":"labels"},
		"bad":{"type":"string","x-mcp-header":"não vale"},
		"title":{"type":"string"}}}`)
	args := json.RawMessage(`{"owner":"renatobardi","page":2,"draft":false,"labels":["a"],"bad":"x","title":"t","ignorado":"y"}`)
	got := HeaderParams(schema, args)
	want := map[string]string{"Mcp-Param-owner": "renatobardi", "Mcp-Param-page": "2", "Mcp-Param-draft": "false"}
	if len(got) != len(want) {
		t.Fatalf("HeaderParams = %v, quer %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, quer %q", k, got[k], v)
		}
	}

	// repo marcado mas ausente nos argumentos: fora; valor com quebra de linha: fora
	if h := HeaderParams(schema, json.RawMessage(`{"owner":"a\nb"}`)); len(h) != 0 {
		t.Errorf("quebra de linha devia ficar de fora: %v", h)
	}
	// sem marca, sem schema ou sem argumentos: nada
	if h := HeaderParams(json.RawMessage(`{"type":"object","properties":{"x":{"type":"number"}}}`), json.RawMessage(`{"x":1}`)); h != nil {
		t.Errorf("sem x-mcp-header devia ser nil: %v", h)
	}
	if h := HeaderParams(nil, args); h != nil {
		t.Errorf("sem schema devia ser nil: %v", h)
	}
	if h := HeaderParams(schema, nil); h != nil {
		t.Errorf("sem argumentos devia ser nil: %v", h)
	}
}

func TestWithHeadersKeepsStoredOnes(t *testing.T) {
	ep := Endpoint{Headers: map[string]string{"Authorization": "Bearer segredo"}}
	got := ep.WithHeaders(map[string]string{"Mcp-Param-owner": "o", "Authorization": "Bearer forjado"})
	if got.Headers["Authorization"] != "Bearer segredo" || got.Headers["Mcp-Param-owner"] != "o" {
		t.Fatalf("WithHeaders = %v", got.Headers)
	}
	if ep.Headers["Mcp-Param-owner"] != "" {
		t.Fatal("o endpoint original não pode mudar")
	}
	if same := ep.WithHeaders(nil); same.Headers["Authorization"] != "Bearer segredo" {
		t.Fatal("sem extras devolve o endpoint como está")
	}
}
