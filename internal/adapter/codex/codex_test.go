package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

func TestParser(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []domain.RunEventKind
		text string
	}{
		{"non json", "WARN foo", []domain.RunEventKind{domain.EventStdout}, "WARN foo"},
		{"thread", `{"type":"thread.started","thread_id":"t1"}`, []domain.RunEventKind{domain.EventSystem}, "t1"},
		{"turn started", `{"type":"turn.started"}`, nil, ""},
		{"reasoning", `{"type":"item.completed","item":{"id":"1","type":"reasoning","text":"hm"}}`, []domain.RunEventKind{domain.EventThinking}, "hm"},
		{"message", `{"type":"item.completed","item":{"id":"2","type":"agent_message","text":"ola"}}`, []domain.RunEventKind{domain.EventAssistant}, "ola"},
		{"cmd start", `{"type":"item.started","item":{"id":"3","type":"command_execution","command":"ls"}}`, []domain.RunEventKind{domain.EventToolUse}, "ls"},
		{"cmd done no start", `{"type":"item.completed","item":{"id":"4","type":"command_execution","command":"pwd","aggregated_output":"/x\n","exit_code":0}}`, []domain.RunEventKind{domain.EventToolUse, domain.EventToolResult}, "pwd"},
		{"mcp", `{"type":"item.started","item":{"id":"5","type":"mcp_tool_call","server":"hanko","tool":"hanko_whoami","arguments":{}}}`, []domain.RunEventKind{domain.EventToolUse}, "hanko.hanko_whoami"},
		{"file change", `{"type":"item.completed","item":{"id":"6","type":"file_change","status":"completed","changes":[{"path":"a.go","kind":"update"}]}}`, []domain.RunEventKind{domain.EventToolUse, domain.EventToolResult}, "update a.go"},
		{"unknown item", `{"type":"item.completed","item":{"id":"7","type":"todo_list"}}`, nil, ""},
		{"error", `{"type":"error","message":"boom"}`, []domain.RunEventKind{domain.EventSystem}, "boom"},
		{"turn failed", `{"type":"turn.failed","error":{"message":"x"}}`, []domain.RunEventKind{domain.EventSystem}, "x"},
		{"unknown", `{"type":"whatever","a":1}`, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newParser(nil)
			evs := p.Feed([]byte(tc.line))
			var got []domain.RunEventKind
			for _, e := range evs {
				got = append(got, e.Kind)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatal(diff)
			}
			if len(evs) > 0 && !strings.Contains(evs[0].Text, tc.text) {
				t.Fatalf("%q lacks %q", evs[0].Text, tc.text)
			}
		})
	}
}

func TestParserUsageAndSummary(t *testing.T) {
	p := newParser(nil)
	for _, l := range []string{
		`{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"um"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":2}}`,
		`{"type":"item.completed","item":{"id":"b","type":"agent_message","text":"dois"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":5,"cached_input_tokens":1,"output_tokens":3}}`,
	} {
		p.Feed([]byte(l))
	}
	if p.lastMessage != "dois" || p.usage != (domain.Usage{InputTokens: 15, CacheReadTokens: 5, OutputTokens: 5}) {
		t.Fatalf("%+v %q", p.usage, p.lastMessage)
	}
}

func TestMCPOverridesAndArgs(t *testing.T) {
	raw := json.RawMessage(`{"mcpServers":{"hanko":{"type":"http","url":"http://h/mcp","headers":{"Authorization":"Bearer tok","X-A":"b"}},"stdio":{"command":"x"}}}`)
	args, env := mcpOverrides(raw)
	joined := strings.Join(args, " ")
	for _, w := range []string{`mcp_servers.hanko.url="http://h/mcp"`, `bearer_token_env_var="HANKO_MCP_TOKEN_HANKO"`, `env_http_headers."X-A"="HANKO_MCP_HDR_HANKO_X_A"`} {
		if !strings.Contains(joined, w) {
			t.Errorf("%q lacks %q", joined, w)
		}
	}
	if strings.Contains(joined, "tok") && !strings.Contains(joined, "HANKO_MCP_TOKEN_HANKO") {
		t.Error("token leaked")
	}
	if env["HANKO_MCP_TOKEN_HANKO"] != "tok" || env["HANKO_MCP_HDR_HANKO_X_A"] != "b" {
		t.Errorf("env=%v", env)
	}
	got := BuildArgs(adapter.Spec{Model: "gpt"}, Config{ExtraArgs: []string{"--x"}}, "/w", []string{"-c", "a=1"})
	want := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "workspace-write", "-m", "gpt", "-C", "/w", "-c", "a=1", "--x", "-"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	if Prompt(adapter.Spec{System: "S", Prompt: "P"}) != "S\n\n---\n\nP" || Prompt(adapter.Spec{Prompt: "P"}) != "P" {
		t.Error("prompt")
	}
}

func TestBuildArgsReadOnly(t *testing.T) {
	got := BuildArgs(adapter.Spec{ReadOnly: true}, Config{Sandbox: "danger-full-access", ExtraArgs: []string{"--sandbox", "danger-full-access"}}, "/w", nil)
	want := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only", "-C", "/w", "-"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
}

type collector struct {
	mu sync.Mutex
	ev []adapter.Event
}

func (c *collector) Emit(_ context.Context, e adapter.Event) error {
	c.mu.Lock()
	c.ev = append(c.ev, e)
	c.mu.Unlock()
	return nil
}

func (c *collector) kinds() (out []domain.RunEventKind) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.ev {
		if e.Kind != domain.EventStderr {
			out = append(out, e.Kind)
		}
	}
	return
}

func (c *collector) stderr(prefix string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.ev {
		if e.Kind == domain.EventStderr && strings.HasPrefix(e.Text, prefix) {
			return e.Text
		}
	}
	return ""
}

func newFake() *Adapter {
	bin, err := filepath.Abs("testdata/fake-codex.sh")
	if err != nil {
		panic(err)
	}
	return New(Options{Bin: bin, Grace: 500 * time.Millisecond, EnvAllow: []string{"FAKE_*"}})
}

func TestRunProcess(t *testing.T) {
	c := &collector{}
	spec := adapter.Spec{Dir: t.TempDir(), Model: "gpt-x", System: "SYS", Prompt: "PROMPT",
		MCPConfig: json.RawMessage(`{"mcpServers":{"hanko":{"type":"http","url":"http://h/mcp","headers":{"Authorization":"Bearer tok"}}}}`)}
	res, err := newFake().Run(context.Background(), spec, c)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.SessionRef != "thr-1" || res.Summary != "Listei os arquivos." ||
		res.Usage != (domain.Usage{InputTokens: 120, CacheReadTokens: 20, OutputTokens: 40}) {
		t.Fatalf("res=%+v", res)
	}
	want := []domain.RunEventKind{domain.EventSystem, domain.EventThinking, domain.EventToolUse, domain.EventToolResult,
		domain.EventAssistant, domain.EventAssistant, domain.EventResult}
	if diff := cmp.Diff(want, c.kinds()); diff != "" {
		t.Fatal(diff)
	}
	if a := c.stderr("ARGS: "); !strings.Contains(a, "exec --json") || !strings.Contains(a, "-m gpt-x") || !strings.HasSuffix(a, " -") {
		t.Errorf("args=%q", a)
	}
	if s := c.stderr("STDIN: "); s != "STDIN: SYS~~---~~PROMPT~" && s != "STDIN: SYS~~---~~PROMPT" {
		t.Errorf("stdin=%q", s)
	}
	if c.stderr("ENV_TOKEN: ") != "ENV_TOKEN: tok" {
		t.Error("token env missing")
	}
}

func TestRunFail(t *testing.T) {
	t.Setenv("FAKE_CODEX_FAIL", "1")
	res, err := newFake().Run(context.Background(), adapter.Spec{Prompt: "x"}, &collector{})
	if err != nil || !res.IsError || res.Error != "falha simulada" || res.ExitCode != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunCancel(t *testing.T) {
	t.Setenv("FAKE_CODEX_SLEEP", "30")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	res, err := newFake().Run(ctx, adapter.Spec{Prompt: "x"}, &collector{})
	if err != nil || !res.IsError || time.Since(t0) > 10*time.Second {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunErrors(t *testing.T) {
	if _, err := New(Options{Bin: "/nonexistent/codex"}).Run(context.Background(), adapter.Spec{}, &collector{}); err == nil {
		t.Fatal("want start error")
	}
	if _, err := newFake().Run(context.Background(), adapter.Spec{Config: json.RawMessage(`{`)}, &collector{}); err == nil {
		t.Fatal("want config error")
	}
}

func TestRunEnvAllowlist(t *testing.T) {
	t.Setenv("HANKO_SECRET", "server-secret-value")
	t.Setenv("HANKO_DATABASE_URL", "postgres://u:dbpass@h/d")
	t.Setenv("HANKO_MEMORY_TOKEN", "memory-token-value")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret-value")
	c := &collector{}
	spec := adapter.Spec{RunID: domain.NewID(), Dir: t.TempDir(), Prompt: "x",
		MCPConfig: json.RawMessage(`{"mcpServers":{"hanko":{"type":"http","url":"http://x/mcp","headers":{"Authorization":"Bearer mcp-token-987654321"}}}}`)}
	if _, err := newFake().Run(context.Background(), spec, c); err != nil {
		t.Fatal(err)
	}
	var errs []string
	for _, e := range c.ev {
		if e.Kind == domain.EventStderr {
			errs = append(errs, e.Text)
		}
	}
	leak := ""
	tok := ""
	for _, s := range errs {
		if strings.HasPrefix(s, "ENV_LEAK: ") {
			leak = s
		}
		if strings.HasPrefix(s, "ENV_TOKEN: ") {
			tok = s
		}
	}
	if leak != "ENV_LEAK: secret=|db=|mem=|aws=|path=set" {
		t.Errorf("env = %q", leak)
	}
	if tok != "ENV_TOKEN: "+adapter.Mask {
		t.Errorf("token do MCP deveria chegar ao processo e sair mascarado: %q", tok)
	}
}
