package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
)

// Hook PreToolUse da política de tools (ADR-0053).

func TestBuildArgsWithHookSettings(t *testing.T) {
	spec := adapter.Spec{Prompt: "p"}
	base := BuildArgs(spec, Config{}, "", "")
	for _, a := range base {
		if a == "--settings" {
			t.Fatalf("sem hook não há --settings: %v", base)
		}
	}
	got := BuildArgsWith(spec, Config{}, "/tmp/mcp.json", "", "/tmp/hooks.json")
	i := indexOf(got, "--settings")
	if i < 0 || got[i+1] != "/tmp/hooks.json" {
		t.Fatalf("args: %v", got)
	}
	if !strings.Contains(strings.Join(got, " "), "--mcp-config /tmp/mcp.json") {
		t.Fatalf("o mcp segue valendo: %v", got)
	}
}

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

func TestHookSettingsJSON(t *testing.T) {
	raw, err := hookSettings("/opt/ha nko/it's/hanko")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	h := s.Hooks.PreToolUse
	if len(h) != 1 || h[0].Matcher != "*" || len(h[0].Hooks) != 1 || h[0].Hooks[0].Type != "command" || h[0].Hooks[0].Timeout < 600 {
		t.Fatalf("settings: %s", raw)
	}
	// o caminho com espaço e aspas simples chega inteiro ao shell
	if want := `'/opt/ha nko/it'\''s/hanko' hook pretooluse`; h[0].Hooks[0].Command != want {
		t.Fatalf("comando %q, queria %q", h[0].Hooks[0].Command, want)
	}
}

func TestWriteHookSettingsFile(t *testing.T) {
	dir := t.TempDir()
	f, err := writeHookSettings(dir, "/usr/bin/hanko")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(f) != dir {
		t.Fatalf("arquivo fora do diretório: %s", f)
	}
	st, err := os.Stat(f)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("modo: %v %v", st, err)
	}
	b, _ := os.ReadFile(f)
	if !strings.Contains(string(b), "'/usr/bin/hanko' hook pretooluse") {
		t.Fatalf("conteúdo: %s", b)
	}
}

// fakeClaude é um executável que registra os argumentos e devolve um stream-json mínimo.
func fakeClaudeBin(t *testing.T, argsOut string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsOut + "\n" +
		"cat \"$(printf '%s\\n' \"$@\" | grep -A1 -x -- '--settings' | tail -1)\" > " + argsOut + ".settings 2>/dev/null\n" +
		"echo '{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"ok\",\"is_error\":false}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

type nopSink struct{ texts []string }

func (s *nopSink) Emit(_ context.Context, e adapter.Event) error {
	s.texts = append(s.texts, e.Text)
	return nil
}

func TestRunPassesHookSettingsAndCleansUp(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args.txt")
	a := New(Options{Bin: fakeClaudeBin(t, out)})
	sink := &nopSink{}
	if _, err := a.Run(context.Background(), adapter.Spec{Prompt: "oi", Dir: t.TempDir(), ToolHookBin: "/usr/bin/hanko"}, sink); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(out)
	if !strings.Contains(string(args), "--settings\n") {
		t.Fatalf("o claude deveria receber --settings:\n%s", args)
	}
	settings, _ := os.ReadFile(out + ".settings")
	if !strings.Contains(string(settings), "PreToolUse") || !strings.Contains(string(settings), "hook pretooluse") {
		t.Fatalf("settings lido pelo claude: %s", settings)
	}
	// o arquivo temporário some ao fim da run
	var path string
	for _, l := range strings.Split(string(args), "\n") {
		if strings.HasPrefix(l, "/") && strings.Contains(l, "hanko-hooks-") {
			path = l
		}
	}
	if path == "" {
		t.Fatalf("não achei o caminho do settings em:\n%s", args)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("o settings temporário deveria ter sido removido: %v", err)
	}

	// revisão (somente leitura) nunca leva o hook
	os.Remove(out)
	if _, err := a.Run(context.Background(), adapter.Spec{Prompt: "oi", Dir: t.TempDir(), ToolHookBin: "/usr/bin/hanko", ReadOnly: true}, sink); err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(out)
	if strings.Contains(string(args), "--settings") {
		t.Fatalf("revisão com hook:\n%s", args)
	}
}
