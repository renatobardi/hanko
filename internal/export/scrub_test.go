package export

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestScrubConfig(t *testing.T) {
	var in map[string]any
	raw := `{
	  "permission_mode": "acceptEdits",
	  "extra_args": ["--verbose", "--api-key", "abc123", "--max-turns=5", "--token=xyz", "--model", "m"],
	  "api_key": "sk-ant-aaaaaaaaaaaa",
	  "Authorization": "Bearer abcdefghijkl",
	  "env": {"HOME": "/tmp", "GITHUB_TOKEN": "ghp_aaaaaaaaaaaaaaaaaaaaaa", "db_password": "x", "LOG": "debug"},
	  "note": "usa sk-ant-bbbbbbbbbbbb para tudo",
	  "dsn": "postgres://u:senha@host/db",
	  "sandbox": "workspace-write",
	  "keyboard": "us",
	  "ssh_key": "-----BEGIN"
	}`
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"permission_mode": "acceptEdits",
		"extra_args":      []any{"--verbose", "--max-turns=5", "--model", "m"},
		"env":             map[string]any{"HOME": "/tmp", "LOG": "debug"},
		"sandbox":         "workspace-write",
		"keyboard":        "us",
	}
	got := ScrubConfig(in)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
	if _, ok := in["api_key"]; !ok {
		t.Fatal("a entrada não pode ser alterada")
	}
	if ScrubConfig(nil) != nil || ScrubConfig(map[string]any{"token": "x"}) != nil {
		t.Fatal("vazio e tudo-sensível viram nil")
	}
}

func TestMaskConfigKeepsNamesHidesValues(t *testing.T) {
	in := map[string]any{
		"command":    "meu-agente",
		"permission": "read_only",
		"args":       []any{"--stdio", "--api-key", "abc123", "--token=xyz", "--max-turns=5"},
		"env":        map[string]any{"MY_HOME": "/data", "MY_KEY": "s3cr3t"},
		"api_key":    "sk-ant-aaaaaaaaaaaa",
		"note":       "usa sk-ant-bbbbbbbbbbbb para tudo",
		"sandbox":    "workspace-write",
	}
	want := map[string]any{
		"command":    "meu-agente",
		"permission": "read_only",
		"args":       []any{"--stdio", "--api-key", "***", "--token=***", "--max-turns=5"},
		"env":        map[string]any{"MY_HOME": "***", "MY_KEY": "***"},
		"api_key":    "***",
		"note":       "***",
		"sandbox":    "workspace-write",
	}
	got := MaskConfig(in)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
	if in["env"].(map[string]any)["MY_KEY"] != "s3cr3t" || in["args"].([]any)[2] != "abc123" {
		t.Fatal("a entrada não pode ser alterada")
	}
	if MaskConfig(nil) != nil || len(MaskConfig(map[string]any{})) != 0 {
		t.Fatal("vazio fica vazio")
	}
}
