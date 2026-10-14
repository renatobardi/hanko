package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJydW46MSJ9.c2lnbmF0dXJlX2hlcmU"
	tests := []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		{"valor exato", "token=s3cr3t-value ok", []string{"s3cr3t-value"}, "token=" + Mask + " ok"},
		{"vazio e curto ignorados", "abc def", []string{"", "ab"}, "abc def"},
		{"bearer", "Authorization: Bearer abcdef1234567890", nil, "Authorization: Bearer " + Mask},
		{"bearer minúsculo", "bearer abcdef1234567890xyz", nil, "bearer " + Mask},
		{"jwt", "run " + jwt + " fim", nil, "run " + Mask + " fim"},
		{"anthropic", "key sk-ant-api03-AbCdEf_123456-xyz", nil, "key " + Mask},
		{"sk genérico", "OPENAI sk-abcdefghijklmnop1234", nil, "OPENAI " + Mask},
		{"github", "ghp_" + strings.Repeat("a", 36) + " e github_pat_" + strings.Repeat("B", 30), nil, Mask + " e " + Mask},
		{"slack", "xoxb-1234567890-abcdefghij", nil, Mask},
		{"aws", "AKIAABCDEFGHIJKLMNOP", nil, Mask},
		{"postgres", "postgres://hanko:p4ss@db:5432/x e postgresql://u:senha@h/d", nil,
			"postgres://hanko:" + Mask + "@db:5432/x e postgresql://u:" + Mask + "@h/d"},
		{"url sem senha intacta", "postgres://db:5432/x", nil, "postgres://db:5432/x"},
		{"maior primeiro", "abcdefgh e abcd", []string{"abcd", "abcdefgh"}, Mask + " e " + Mask},
		{"texto limpo", "listando arquivos", nil, "listando arquivos"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in, tc.secrets...); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRedactingSinkTextoEPayload(t *testing.T) {
	var got []Event
	sink := RedactingSink(SinkFunc(func(_ context.Context, e Event) error { got = append(got, e); return nil }), "run-token-123456")
	err := sink.Emit(context.Background(), Event{
		Text:    "usando run-token-123456",
		Payload: json.RawMessage(`{"cmd":"curl -H 'Authorization: Bearer run-token-123456'","env":"postgres://u:pw@h/d"}`),
	})
	if err != nil || len(got) != 1 {
		t.Fatalf("err=%v events=%d", err, len(got))
	}
	if strings.Contains(got[0].Text, "run-token") || strings.Contains(string(got[0].Payload), "run-token") || strings.Contains(string(got[0].Payload), ":pw@") {
		t.Errorf("vazou: %q %s", got[0].Text, got[0].Payload)
	}
	if !json.Valid(got[0].Payload) {
		t.Errorf("payload inválido: %s", got[0].Payload)
	}
}

func TestSpecSecrets(t *testing.T) {
	spec := Spec{
		Env:       map[string]string{"HANKO_RUN_TOKEN": "tok-run", "HANKO_URL": "http://x", "MY_API_KEY": "k-123456"},
		MCPConfig: json.RawMessage(`{"mcpServers":{"hanko":{"headers":{"Authorization":"Bearer tok-mcp-abc"}}}}`),
	}
	s := strings.Join(SpecSecrets(spec), ",")
	for _, want := range []string{"tok-run", "k-123456", "tok-mcp-abc", "Bearer tok-mcp-abc"} {
		if !strings.Contains(s, want) {
			t.Errorf("faltou %q em %q", want, s)
		}
	}
	if strings.Contains(s, "http://x") {
		t.Error("HANKO_URL não é segredo")
	}
}
