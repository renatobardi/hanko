package cursor

import (
	"bufio"
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

func newFake(t *testing.T) interface {
	Run(context.Context, adapter.Spec, adapter.Sink) (adapter.Result, error)
} {
	t.Helper()
	bin, err := filepath.Abs("testdata/fake-cursor.sh")
	if err != nil {
		t.Fatal(err)
	}
	return New(bin, Options{Grace: 500 * time.Millisecond, EnvAllow: []string{"FAKE_*"}})
}

func TestParserOnRecordedRun(t *testing.T) {
	f, err := os.Open("testdata/run.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := newParser()
	var kinds []domain.RunEventKind
	var texts []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		for _, e := range p.Feed(sc.Bytes()) {
			kinds = append(kinds, e.Kind)
			texts = append(texts, e.Text)
		}
	}
	want := []domain.RunEventKind{domain.EventSystem, domain.EventAssistant, domain.EventToolUse, domain.EventToolResult,
		domain.EventToolUse, domain.EventToolResult, domain.EventAssistant}
	if diff := cmp.Diff(want, kinds); diff != "" {
		t.Fatalf("%s\n%q", diff, texts)
	}
	if !strings.HasPrefix(texts[2], "shell {") || !strings.Contains(texts[3], "HNK-7") || !strings.HasPrefix(texts[4], "read ") || !strings.Contains(texts[5], "erro:") {
		t.Fatalf("tools: %q", texts)
	}
	if texts[6] != "Pronto, login corrigido." {
		t.Fatalf("mensagem juntada: %q", texts[6])
	}
	o := p.Outcome()
	if o.SessionRef != "cur-sess-3" || o.Summary != "Pronto, login corrigido." || !o.Completed || o.Failed || o.Model != "Claude 4 Sonnet" {
		t.Fatalf("outcome = %+v", o)
	}
	if o.CostUSD != nil || o.Usage != (domain.Usage{}) {
		t.Fatalf("o cursor não informa custo nem tokens: %+v", o)
	}
}

func TestParserEdgeCases(t *testing.T) {
	cases := []struct {
		name, line string
		want       []domain.RunEventKind
	}{
		{"não é json", "Update available", []domain.RunEventKind{domain.EventStdout}},
		{"desconhecido", `{"type":"thinking","subtype":"delta","text":"x"}`, nil},
		{"system sem init", `{"type":"system","subtype":"other"}`, nil},
		{"assistente sem texto", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"image"}]}}`, nil},
		{"tool genérica", `{"type":"tool_call","subtype":"started","tool_call":{"function":{"name":"mcp_x","arguments":"{}"}}}`, []domain.RunEventKind{domain.EventToolUse}},
		{"tool_call sem corpo", `{"type":"tool_call","subtype":"completed"}`, []domain.RunEventKind{domain.EventToolResult}},
		{"erro", `{"type":"error","error":"boom"}`, []domain.RunEventKind{domain.EventSystem}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []domain.RunEventKind
			for _, e := range newParser().Feed([]byte(c.line)) {
				got = append(got, e.Kind)
			}
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Fatal(diff)
			}
		})
	}
	p := newParser()
	p.Feed([]byte(`{"type":"assistant","message":{"role":"assistant","content":"rascunho"}}`))
	p.Feed([]byte(`{"type":"result","subtype":"error","is_error":true,"result":""}`))
	if o := p.Outcome(); !o.Failed || o.Summary != "rascunho" || !strings.Contains(o.Error, "terminou com erro") {
		t.Fatalf("outcome = %+v", o)
	}
}

func TestBuildArgsAndConfig(t *testing.T) {
	got := BuildArgs(adapter.Spec{Model: "sonnet-4", SessionRef: "s1"}, Config{Force: true, ExtraArgs: []string{"--x"}})
	want := []string{"-p", "--output-format", "stream-json", "--model", "sonnet-4", "--resume", "s1", "--force", "--x"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	// revisão: sem --force nem extra_args
	if diff := cmp.Diff([]string{"-p", "--output-format", "stream-json"}, BuildArgs(adapter.Spec{ReadOnly: true}, Config{Force: true, ExtraArgs: []string{"--force"}})); diff != "" {
		t.Fatal(diff)
	}
	if _, err := ParseConfig(json.RawMessage(`{"forse":true}`)); err == nil || !strings.Contains(err.Error(), "cursor accepts") {
		t.Fatalf("chave desconhecida: %v", err)
	}
	if c, err := ParseConfig(json.RawMessage(`{"force":true}`)); err != nil || !c.Force {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestRunProcess(t *testing.T) {
	c := &collector{}
	co := domain.NewID()
	spec := adapter.Spec{Dir: t.TempDir(), CompanyID: co, TaskKey: "HNK-7", Model: "sonnet-4", System: "SYS", Prompt: "PROMPT",
		Env: map[string]string{"HANKO_RUN_TOKEN": "run-token-SENTINELA-123456", "HANKO_URL": "http://hanko.test"}}
	res, err := newFake(t).Run(context.Background(), spec, c)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.SessionRef != "cur-sess-3" || res.Summary != "Pronto, login corrigido." {
		t.Fatalf("res = %+v", res)
	}
	if k := c.kinds(); k[len(k)-1] != domain.EventResult || len(k) != 8 {
		t.Fatalf("kinds = %v", k)
	}
	if a := c.stderr("ARGS: "); a != "ARGS: -p --output-format stream-json --model sonnet-4" {
		t.Errorf("args = %q", a)
	}
	if s := c.stderr("STDIN: "); !strings.Contains(s, "SYS") || !strings.Contains(s, "PROMPT") || !strings.Contains(s, "hanko task show HNK-7") {
		t.Errorf("stdin = %q", s)
	}
	if env := c.stderr("ENV: "); strings.Contains(env, "SENTINELA") || !strings.Contains(env, "company="+co.String()) {
		t.Errorf("env = %q", env)
	}
}

func TestRunFailureAndMissingBinary(t *testing.T) {
	t.Setenv("FAKE_CURSOR_FAIL", "1")
	res, err := newFake(t).Run(context.Background(), adapter.Spec{Prompt: "x"}, &collector{})
	if err != nil || !res.IsError || !strings.Contains(res.Error, "Authentication required") || res.ExitCode != 1 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if _, err := New("/nonexistent/cursor-agent", Options{}).Run(context.Background(), adapter.Spec{Prompt: "x"}, &collector{}); err == nil {
		t.Fatal("binário inexistente deve falhar")
	}
}
