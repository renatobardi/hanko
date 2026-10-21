package pi

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
	"github.com/renatobardi/hanko/internal/adapter/cliharness"
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

type cliAdapter = cliharness.Adapter

func newAdapter(bin string) *cliAdapter {
	return New(bin, Options{Grace: 500 * time.Millisecond, EnvAllow: []string{"FAKE_*"}})
}

func newFake(t *testing.T) *cliAdapter {
	t.Helper()
	bin, err := filepath.Abs("testdata/fake-pi.sh")
	if err != nil {
		t.Fatal(err)
	}
	return newAdapter(bin)
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
	want := []domain.RunEventKind{domain.EventSystem, domain.EventThinking, domain.EventAssistant, domain.EventToolUse, domain.EventToolResult, domain.EventAssistant}
	if diff := cmp.Diff(want, kinds); diff != "" {
		t.Fatalf("%s\n%q", diff, texts)
	}
	if !strings.Contains(texts[3], "bash") || !strings.Contains(texts[3], "hanko task show HNK-7") || !strings.HasPrefix(texts[4], "bash: HNK-7") {
		t.Fatalf("tool events: %q", texts)
	}
	o := p.Outcome()
	if o.SessionRef != "pi-sess-7" || o.Summary != "Task lida. Corrigi o login e comentei o resultado." || !o.Completed || o.Failed || o.Model != "claude-haiku-4-5" {
		t.Fatalf("outcome = %+v", o)
	}
	if o.Usage != (domain.Usage{InputTokens: 2700, OutputTokens: 120, CacheReadTokens: 1200, CacheCreationTokens: 50}) {
		t.Fatalf("usage = %+v", o.Usage)
	}
	if o.CostUSD == nil || *o.CostUSD < 0.0026 || *o.CostUSD > 0.0028 {
		t.Fatalf("cost = %v", o.CostUSD)
	}
}

func TestParserEdgeCases(t *testing.T) {
	cases := []struct {
		name, line string
		want       []domain.RunEventKind
	}{
		{"não é json", "WARN: algo", []domain.RunEventKind{domain.EventStdout}},
		{"vazio", "  ", nil},
		{"desconhecido", `{"type":"quota_update","x":1}`, nil},
		{"mensagem do usuário", `{"type":"message_end","message":{"role":"user","content":"oi"}}`, nil},
		{"conteúdo string", `{"type":"message_end","message":{"role":"assistant","content":"olá"}}`, []domain.RunEventKind{domain.EventAssistant}},
		{"erro", `{"type":"error","error":"boom"}`, []domain.RunEventKind{domain.EventSystem}},
		{"resultado de tool com erro", `{"type":"tool_execution_end","toolName":"bash","isError":true,"result":"falhou"}`, []domain.RunEventKind{domain.EventToolResult}},
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
	p.Feed([]byte(`{"type":"tool_execution_end","toolName":"bash","isError":true,"result":"falhou"}`))
	big := `{"type":"tool_execution_end","toolName":"cat","result":"` + strings.Repeat("é", 5000) + `"}`
	if e := p.Feed([]byte(big)); len(e) != 1 || !strings.Contains(e[0].Text, "truncado") || len(e[0].Text) > maxToolResultBytes+100 {
		t.Fatalf("resultado grande deveria ser truncado: %d", len(e[0].Text))
	}
}

func TestBuildArgsAndConfig(t *testing.T) {
	got := BuildArgs(adapter.Spec{Model: "claude-haiku-4-5"}, Config{Provider: "anthropic", Tools: []string{"read", "bash"}, ExtraArgs: []string{"--x"}})
	want := []string{"--mode", "json", "-p", "--model", "claude-haiku-4-5", "--provider", "anthropic", "--tools", "read,bash", "--x"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	// revisão: somente leitura, sem extra_args da persona
	ro := BuildArgs(adapter.Spec{ReadOnly: true}, Config{Tools: []string{"bash"}, ExtraArgs: []string{"--yolo"}})
	if diff := cmp.Diff([]string{"--mode", "json", "-p", "--tools", readOnlyTools}, ro); diff != "" {
		t.Fatal(diff)
	}
	if _, err := ParseConfig(json.RawMessage(`{"provider":"x","comand":"y"}`)); err == nil || !strings.Contains(err.Error(), "pi accepts") {
		t.Fatalf("chave desconhecida: %v", err)
	}
	if _, err := ParseConfig(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConfig(json.RawMessage(`{`)); err == nil {
		t.Fatal("json inválido")
	}
}

func TestRunProcessUsesHankoCLIWithRunToken(t *testing.T) {
	c := &collector{}
	co := domain.NewID()
	spec := adapter.Spec{Dir: t.TempDir(), CompanyID: co, TaskKey: "HNK-7", Model: "m1", System: "SYS", Prompt: "PROMPT",
		Env: map[string]string{"HANKO_RUN_TOKEN": "run-token-SENTINELA-123456", "HANKO_URL": "http://hanko.test"}}
	res, err := newFake(t).Run(context.Background(), spec, c)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.SessionRef != "pi-sess-7" || !strings.HasPrefix(res.Summary, "Task lida") || res.CostUSD == nil {
		t.Fatalf("res = %+v", res)
	}
	want := []domain.RunEventKind{domain.EventSystem, domain.EventThinking, domain.EventAssistant, domain.EventToolUse, domain.EventToolResult, domain.EventAssistant, domain.EventResult}
	if diff := cmp.Diff(want, c.kinds()); diff != "" {
		t.Fatal(diff)
	}
	if a := c.stderr("ARGS: "); a != "ARGS: --mode json -p --model m1" {
		t.Errorf("args = %q", a)
	}
	stdin := c.stderr("STDIN: ")
	for _, w := range []string{"SYS", "PROMPT", "hanko task show HNK-7", "hanko task comment HNK-7"} {
		if !strings.Contains(stdin, w) {
			t.Errorf("stdin sem %q: %s", w, stdin)
		}
	}
	// o run token chega ao processo como credencial da CLI, mas nunca aparece em claro nos eventos
	env := c.stderr("ENV: ")
	if !strings.Contains(env, "company="+co.String()) || !strings.Contains(env, "url=http://hanko.test") || strings.Contains(env, "SENTINELA") {
		t.Errorf("env = %q (o token deve estar mascarado)", env)
	}
	if !strings.Contains(env, "token="+adapter.Mask) {
		t.Errorf("HANKO_TOKEN deveria chegar ao processo (mascarado no log): %q", env)
	}
	if leak := c.stderr("ENV_LEAK: "); leak != "ENV_LEAK: secret=|db=" {
		t.Errorf("vazamento do ambiente do servidor: %q", leak)
	}
}

func TestRunWithoutRunTokenHasNoCLITools(t *testing.T) {
	c := &collector{}
	if _, err := newFake(t).Run(context.Background(), adapter.Spec{Dir: t.TempDir(), TaskKey: "HNK-1", System: "SYS", Prompt: "P", ReadOnly: true}, c); err != nil {
		t.Fatal(err)
	}
	if s := c.stderr("STDIN: "); strings.Contains(s, "hanko task") {
		t.Errorf("sem credencial a run não conhece a CLI: %q", s)
	}
	if a := c.stderr("ARGS: "); !strings.Contains(a, "--tools "+readOnlyTools) {
		t.Errorf("revisão deve ser somente leitura: %q", a)
	}
}

func TestRunFailureModes(t *testing.T) {
	t.Setenv("FAKE_PI_FAIL", "1")
	a := newFake(t)
	res, err := a.Run(context.Background(), adapter.Spec{Prompt: "x"}, &collector{})
	if err != nil || !res.IsError || !strings.Contains(res.Error, "401 invalid api key") || res.ExitCode != 1 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	t.Setenv("FAKE_PI_FAIL", "")
	t.Setenv("FAKE_PI_EMPTY", "1")
	res, err = a.Run(context.Background(), adapter.Spec{Prompt: "x"}, &collector{})
	if err != nil || !res.IsError || !strings.Contains(res.Error, "sem resposta") {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if _, err := newAdapter("/nonexistent/pi").Run(context.Background(), adapter.Spec{Prompt: "x"}, &collector{}); err == nil {
		t.Fatal("binário inexistente deve falhar")
	}
	if _, err := a.Run(context.Background(), adapter.Spec{Config: json.RawMessage(`{"nada":1}`)}, &collector{}); err == nil {
		t.Fatal("adapter_config inválido deve falhar antes de iniciar")
	}
}

func TestRunCancel(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "slow-pi")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"type\":\"session\",\"id\":\"s\"}'\nsleep 30 &\nwait $!\n"), 0o755); err != nil { //nolint:gosec // script de teste
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	res, err := newAdapter(bin).Run(ctx, adapter.Spec{Prompt: "x"}, &collector{})
	if err != nil || !res.IsError || time.Since(t0) > 10*time.Second {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestProbe(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'pi 1.2.3'\n"), 0o755); err != nil { //nolint:gosec // script de teste
		t.Fatal(err)
	}
	ok := newAdapter(bin).Probe(context.Background(), adapter.Spec{})
	if !ok.OK || ok.Steps[0].Detail != "pi 1.2.3" {
		t.Fatalf("probe = %+v", ok)
	}
	if bad := newAdapter("/nonexistent/pi").Probe(context.Background(), adapter.Spec{}); bad.OK || bad.Steps[0].Name != "binário" {
		t.Fatalf("probe = %+v", bad)
	}
}
