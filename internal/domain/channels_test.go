package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestChannelKind(t *testing.T) {
	if !ChannelTelegram.Valid() || !ChannelGitHub.Valid() || ChannelKind("slack").Valid() {
		t.Error("Valid")
	}
	if ChannelTelegram.Label() != "Telegram" || ChannelGitHub.Label() != "GitHub" || ChannelKind("x").Label() != "x" {
		t.Error("Label")
	}
	c := Channel{InboundToken: "abc"}
	if c.ChannelHookPath() != "/hooks/channels/abc" {
		t.Error("hook path")
	}
}

func TestNormalizeChannelConfig(t *testing.T) {
	gh := func(c ChannelConfig) (ChannelConfig, error) { return NormalizeChannelConfig(ChannelGitHub, c) }
	got, err := gh(ChannelConfig{Repos: []string{"acme/api", " acme/api ", "Acme/API", "kubo/*"}, APIBase: "https://gh.example.com/api/v3/"})
	if err != nil || len(got.Repos) != 2 || got.Mention != "@hanko" || got.APIBase != "https://gh.example.com/api/v3" {
		t.Fatalf("%+v %v", got, err)
	}
	for name, c := range map[string]ChannelConfig{
		"sem repos":     {},
		"repo ruim":     {Repos: []string{"acme"}},
		"repo curinga":  {Repos: []string{"*/*"}},
		"mention ruim":  {Repos: []string{"a/b"}, Mention: "hanko"},
		"base ruim":     {Repos: []string{"a/b"}, APIBase: "ftp://x"},
		"base com user": {Repos: []string{"a/b"}, APIBase: "https://u:p@x.com"},
	} {
		if _, err := gh(c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	many := make([]string, MaxChannelRepos+1)
	for i := range many {
		many[i] = "a/r" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if _, err := gh(ChannelConfig{Repos: many}); err == nil {
		t.Error("passou do teto de repositórios")
	}
	if _, err := NormalizeChannelConfig(ChannelTelegram, ChannelConfig{Repos: []string{"a/b"}}); err == nil {
		t.Error("telegram com repos")
	}
	if c, err := NormalizeChannelConfig(ChannelTelegram, ChannelConfig{}); err != nil || c.APIBase != "" {
		t.Errorf("telegram vazio: %+v %v", c, err)
	}
	if _, err := NormalizeChannelConfig("x", ChannelConfig{}); err == nil {
		t.Error("driver desconhecido")
	}
}

func TestRepoAllowed(t *testing.T) {
	repos := []string{"acme/api", "Kubo/*"}
	for full, want := range map[string]bool{"acme/api": true, "ACME/API": true, "acme/web": false, "kubo/qualquer": true, "outro/api": false, "": false} {
		if RepoAllowed(repos, full) != want {
			t.Errorf("%q: quer %v", full, want)
		}
	}
	if RepoAllowed(nil, "a/b") {
		t.Error("lista vazia libera")
	}
}

func TestValidChannelName(t *testing.T) {
	if n, err := ValidChannelName("  Bot  "); err != nil || n != "Bot" {
		t.Error("nome")
	}
	if _, err := ValidChannelName(" "); err == nil {
		t.Error("vazio")
	}
	if _, err := ValidChannelName(strings.Repeat("é", MaxChannelName+1)); err == nil {
		t.Error("longo")
	}
}

func TestLinkCode(t *testing.T) {
	raw := LinkCodeFromBytes([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8})
	if len(raw) != 8 || NormalizeLinkCode(raw) != raw {
		t.Fatalf("raw = %q", raw)
	}
	if LinkCodeFromBytes([]byte{1, 2}) != "" {
		t.Error("poucos bytes")
	}
	f := FormatLinkCode(raw)
	if len(f) != 9 || f[4] != '-' || NormalizeLinkCode(" "+strings.ToLower(f)+" ") != raw {
		t.Errorf("format = %q", f)
	}
	if FormatLinkCode("abc") != "abc" {
		t.Error("format curto")
	}
	for _, bad := range []string{"", "ABC", "ABCD-EFG0", "ABCD-EFGO", strings.Repeat("A", 9)} {
		if NormalizeLinkCode(bad) != "" {
			t.Errorf("%q aceito", bad)
		}
	}
	if HashLinkCode(raw) == HashLinkCode("ZZZZZZZZ") || len(HashLinkCode(raw)) != 64 {
		t.Error("hash")
	}
}

func TestClipInboundAndPeer(t *testing.T) {
	if ClipInbound("  oi  ") != "oi" || len([]rune(ClipInbound(strings.Repeat("ã", MaxChannelInboundLen+10)))) != MaxChannelInboundLen {
		t.Error("clip")
	}
	repo, n, ok := ParseGitHubPeer("acme/api#42")
	if !ok || repo != "acme/api" || n != 42 {
		t.Errorf("%q %d %v", repo, n, ok)
	}
	for _, bad := range []string{"", "acme/api", "acme/api#0", "acme/api#x", "#4", "a b/c#1", "acme/api#1234567890"} {
		if _, _, ok := ParseGitHubPeer(bad); ok {
			t.Errorf("%q aceito", bad)
		}
	}
	if !GHReviewComment.Valid() || !GHReviewApprove.Valid() || !GHReviewRequestChanges.Valid() || GitHubReviewEvent("X").Valid() {
		t.Error("review events")
	}
}
