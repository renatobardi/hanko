package memory

import (
	"reflect"
	"testing"
)

func TestRefusedArgsRecusaOQueSaiDoEscopo(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want []string
	}{
		{QueryTool, map[string]any{"query": "x", "workspace": "a", "project": "b", "limit": 3}, nil},
		{QueryTool, map[string]any{"query": "x", "global": true}, []string{"global"}},
		{QueryTool, map[string]any{"query": "x", "scopes": []any{}}, []string{"scopes"}},
		{"memory_read_page", map[string]any{"path": "a", "scope": "global"}, []string{"scope"}},
		{"memory_delete_page", map[string]any{"path": "a", "scope": "global"}, []string{"scope"}},
		{"memory_write_page", map[string]any{"path": "a", "body": "b", "scope": "global", "relations": []any{}}, []string{"relations", "scope"}},
		{MessageSendTool, map[string]any{"body": "b", "from_workspace": "x", "from_project": "y"}, []string{"from_project", "from_workspace"}},
		{"memory_status", map[string]any{"novo": 1}, []string{"novo"}},
		{"memory_consolidate", map[string]any{"workspace": "x"}, []string{"workspace"}},
	}
	for _, c := range cases {
		if got := RefusedArgs(c.tool, c.args); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v = %v; esperava %v", c.tool, c.args, got, c.want)
		}
	}
}

func TestContratoSoConheceODecidido(t *testing.T) {
	if KnownTool("memory_nova") {
		t.Error("tool fora do contrato não pode ser conhecida")
	}
	if !KnownArg(QueryTool, "global") || !KnownArg(QueryTool, "workspace") || KnownArg(QueryTool, "novo") {
		t.Error("KnownArg deveria cobrir permitidos, recusados e escopo")
	}
	if ScopedTool(MessageSendTool) || !ScopedTool(QueryTool) {
		t.Error("ScopedTool errado")
	}
}
