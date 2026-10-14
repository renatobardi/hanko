package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) }

func TestParserLines(t *testing.T) {
	long := strings.Repeat("é", 3000) // 6000 bytes
	tests := []struct {
		name string
		line string
		want []domain.RunEventKind
		text string // substring of first event text
	}{
		{"non-json", "hello world", []domain.RunEventKind{domain.EventStdout}, "hello world"},
		{"broken json", `{"type":"assist`, []domain.RunEventKind{domain.EventStdout}, `{"type":"assist`},
		{"blank", "   ", nil, ""},
		{"init", `{"type":"system","subtype":"init","session_id":"s1","model":"m1"}`, []domain.RunEventKind{domain.EventSystem}, "s1"},
		{"system other", `{"type":"system","subtype":"hook_started"}`, nil, ""},
		{"text", `{"type":"assistant","message":{"content":[{"type":"text","text":"oi"}]}}`, []domain.RunEventKind{domain.EventAssistant}, "oi"},
		{"empty text", `{"type":"assistant","message":{"content":[{"type":"text","text":" "}]}}`, nil, ""},
		{"thinking", `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`, []domain.RunEventKind{domain.EventThinking}, "hmm"},
		{"tool_use", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"Read","input":{"file_path":"/a/b.go"}}]}}`, []domain.RunEventKind{domain.EventToolUse}, "Read: /a/b.go"},
		{"tool_use generic", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"X","input":{"b":1,"a":"z"}}]}}`, []domain.RunEventKind{domain.EventToolUse}, `X: a="z", b=1`},
		{"mixed", `{"type":"assistant","message":{"content":[{"type":"text","text":"a"},{"type":"tool_use","id":"t","name":"Bash","input":{"command":"ls\n-l"}}]}}`, []domain.RunEventKind{domain.EventAssistant, domain.EventToolUse}, "a"},
		{"content string", `{"type":"assistant","message":{"content":"texto puro"}}`, []domain.RunEventKind{domain.EventAssistant}, "texto puro"},
		{"tool_result string", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"saida"}]}}`, []domain.RunEventKind{domain.EventToolResult}, "saida"},
		{"tool_result blocks", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"l1"},{"type":"text","text":"l2"}]}]}}`, []domain.RunEventKind{domain.EventToolResult}, "l1\nl2"},
		{"tool_result error", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"x","is_error":true}]}}`, []domain.RunEventKind{domain.EventToolResult}, "erro: x"},
		{"tool_result long", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"` + long + `"}]}}`, []domain.RunEventKind{domain.EventToolResult}, "…[truncado]"},
		{"user plain", `{"type":"user","message":{"content":"oi"}}`, nil, ""},
		{"unknown type", `{"type":"rate_limit_event"}`, nil, ""},
		{"no type", `{"foo":1}`, []domain.RunEventKind{domain.EventStdout}, `{"foo":1}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newParser(fixedNow)
			evs := p.Feed([]byte(tc.line))
			var got []domain.RunEventKind
			for _, e := range evs {
				got = append(got, e.Kind)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("kinds (-want +got):\n%s", diff)
			}
			if len(evs) > 0 && !strings.Contains(evs[0].Text, tc.text) {
				t.Fatalf("text %q lacks %q", evs[0].Text, tc.text)
			}
			if tc.name == "tool_result long" && len(evs[0].Text) > maxToolResultBytes+30 {
				t.Fatalf("not truncated: %d", len(evs[0].Text))
			}
		})
	}
}

func TestParserResult(t *testing.T) {
	p := newParser(fixedNow)
	p.Feed([]byte(`{"type":"system","subtype":"init","session_id":"s1","model":"m1"}`))
	evs := p.Feed([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"fim","session_id":"s1","total_cost_usd":0.5,"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}`))
	if len(evs) != 1 || evs[0].Kind != domain.EventResult || evs[0].Text != "fim" {
		t.Fatalf("evs=%+v", evs)
	}
	r := p.result
	cost := 0.5
	want := adapter.Result{SessionRef: "s1", Summary: "fim", CostUSD: &cost, Model: "m1",
		Usage: domain.Usage{InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheCreationTokens: 4}}
	if diff := cmp.Diff(want, r); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}

	p = newParser(fixedNow)
	p.Feed([]byte(`{"type":"result","is_error":true,"subtype":"error_max_turns","session_id":"s2"}`))
	if !p.result.IsError || p.result.Error == "" || p.result.CostUSD != nil {
		t.Fatalf("%+v", p.result)
	}
}

func TestBuildArgs(t *testing.T) {
	spec := adapter.Spec{Model: "opus", System: "SYS", SessionRef: "ref1"}
	got := BuildArgs(spec, Config{ExtraArgs: []string{"--max-turns", "5"}}, "/tmp/m.json")
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits",
		"--model", "opus", "--append-system-prompt", "SYS", "--mcp-config", "/tmp/m.json", "--resume", "ref1", "--max-turns", "5"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	got = BuildArgs(adapter.Spec{}, Config{PermissionMode: "plan"}, "")
	if diff := cmp.Diff([]string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "plan"}, got); diff != "" {
		t.Fatal(diff)
	}
}

type collector struct {
	mu sync.Mutex
	ev []adapter.Event
}

func (c *collector) Emit(_ context.Context, e adapter.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ev = append(c.ev, e)
	return nil
}

func (c *collector) texts(k domain.RunEventKind) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, e := range c.ev {
		if e.Kind == k {
			out = append(out, e.Text)
		}
	}
	return out
}

func newFake() *Adapter {
	bin, err := filepath.Abs("testdata/fake-claude.sh")
	if err != nil {
		panic(err)
	}
	return New(Options{Bin: bin, Grace: 500 * time.Millisecond})
}

func findPrefix(ss []string, p string) string {
	for _, s := range ss {
		if strings.HasPrefix(s, p) {
			return s
		}
	}
	return ""
}

func TestRunProcess(t *testing.T) {
	dir := t.TempDir()
	c := &collector{}
	spec := adapter.Spec{
		RunID: domain.NewID(), Dir: dir, Model: "sonnet", System: "Você é X", Prompt: "faça a tarefa\nlinha 2",
		SessionRef: "prev-1", Env: map[string]string{"FOO": "bar"},
		MCPConfig: json.RawMessage(`{"mcpServers":{"hanko":{"type":"http","url":"http://x/mcp"}}}`),
		Config:    json.RawMessage(`{"permission_mode":"plan","extra_args":["--max-turns","3"]}`),
	}
	res, err := newFake().Run(context.Background(), spec, c)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.ExitCode != 0 || res.SessionRef != "sess-123" || res.Summary != "Listei os arquivos. Próximo passo: revisar o README." {
		t.Fatalf("res=%+v", res)
	}
	if res.CostUSD == nil || *res.CostUSD != 0.0123 || res.Usage.InputTokens != 100 || res.Usage.CacheCreationTokens != 5 || res.Model != "claude-sonnet-fake" {
		t.Fatalf("res=%+v", res)
	}
	errs := c.texts(domain.EventStderr)
	args := findPrefix(errs, "ARGS: ")
	for _, want := range []string{"-p --output-format stream-json --verbose", "--permission-mode plan", "--model sonnet",
		"--append-system-prompt Você é X", "--resume prev-1", "--max-turns 3", "--mcp-config "} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q lacks %q", args, want)
		}
	}
	if got := findPrefix(errs, "CWD: "); !strings.HasSuffix(got, dir) && !strings.Contains(got, "/") {
		t.Errorf("cwd=%q", got)
	}
	if got := findPrefix(errs, "ENV_FOO: "); got != "ENV_FOO: bar" {
		t.Errorf("env=%q", got)
	}
	if got := findPrefix(errs, "STDIN: "); got != "STDIN: faça a tarefa" {
		t.Errorf("stdin=%q", got)
	}
	if got := findPrefix(errs, "MCP: "); !strings.Contains(got, `"url":"http://x/mcp"`) {
		t.Errorf("mcp=%q", got)
	}
	if f := strings.TrimPrefix(findPrefix(errs, "MCPFILE: "), "MCPFILE: "); f == "" {
		t.Error("no mcp file reported")
	} else if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Errorf("mcp file not removed: %v", err)
	}
	var kinds []domain.RunEventKind
	for _, e := range c.ev {
		if e.Kind != domain.EventStderr {
			kinds = append(kinds, e.Kind)
		}
	}
	want := []domain.RunEventKind{domain.EventStdout, domain.EventSystem, domain.EventThinking, domain.EventAssistant,
		domain.EventToolUse, domain.EventToolResult, domain.EventAssistant, domain.EventResult}
	if diff := cmp.Diff(want, kinds); diff != "" {
		t.Fatalf("kinds (-want +got):\n%s", diff)
	}
}

func TestRunProcessFail(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_FAIL", "1")
	c := &collector{}
	res, err := newFake().Run(context.Background(), adapter.Spec{Prompt: "x"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.ExitCode != 1 || res.Error != "falha simulada" {
		t.Fatalf("res=%+v", res)
	}
}

func TestRunProcessCancel(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_SLEEP", "30")
	c := &collector{}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 100; i++ {
			if len(c.texts(domain.EventSystem)) > 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
	}()
	t0 := time.Now()
	res, err := newFake().Run(ctx, adapter.Spec{Prompt: "x"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(t0) > 10*time.Second {
		t.Fatal("cancel too slow")
	}
	if !res.IsError || ctx.Err() == nil {
		t.Fatalf("res=%+v", res)
	}
}

func TestRunStartError(t *testing.T) {
	_, err := New(Options{Bin: "/nonexistent/claude-xyz"}).Run(context.Background(), adapter.Spec{}, &collector{})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestRunBadConfig(t *testing.T) {
	_, err := newFake().Run(context.Background(), adapter.Spec{Config: json.RawMessage(`{`)}, &collector{})
	if err == nil {
		t.Fatal("want error")
	}
}
