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
