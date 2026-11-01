package service

import (
	"strings"
	"testing"
)

// Vetores gerados pelo `approve_sha` do agent-studio (`proposals.py@845764f`), com o sha256 inteiro.
func TestApproveSHAMatchesAgentStudio(t *testing.T) {
	const at = int64(1_793_000_000_123_456_789)
	for _, c := range []struct {
		name, title, as, agent, session, script, want string
	}{
		{"completo", "Deploy", "oute", "claude", "", "echo oi\n", "8479cd7aab1c537c53741d9ea4d1b234ad3d674488d96063e10b1041da89d744"},
		{"sem agente e sem como, com sessão", "Deploy", "", "", "sessao-1", "echo oi\n", "d23f2338d5fb1dfbd5eb017a6bdc0ac20d6e8924fe6f5fdc595aa44ab1fd0144"},
		{"acima de 64 KiB, só os primeiros bytes", "Á", "oute", "claude", "", strings.Repeat("x", 70000), "20b27b4602f7b09c726fbc7570e88b36a2f029132838bb5dd5f258c57681ee3d"},
	} {
		if got := approveSHA(c.title, c.as, c.agent, c.session, c.script, at); got != c.want {
			t.Errorf("%s: %s, quer %s", c.name, got, c.want)
		}
	}
}
