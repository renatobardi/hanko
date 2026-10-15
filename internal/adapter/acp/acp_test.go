package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

func TestMain(m *testing.M) {
	if sc := os.Getenv("ACPFAKE"); sc != "" {
		fakeMain(sc)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// ---- harness --------------------------------------------------------------------------------

type collector struct {
	mu  sync.Mutex
	evs []adapter.Event
}

func (c *collector) Emit(_ context.Context, e adapter.Event) error {
	c.mu.Lock()
	c.evs = append(c.evs, e)
	c.mu.Unlock()
	return nil
}

func (c *collector) all() []adapter.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]adapter.Event(nil), c.evs...)
}

func (c *collector) ofKind(k domain.RunEventKind) []adapter.Event {
	var out []adapter.Event
	for _, e := range c.all() {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

func (c *collector) has(k domain.RunEventKind, sub string) bool {
	for _, e := range c.ofKind(k) {
		if strings.Contains(e.Text, sub) {
			return true
		}
	}
	return false
}

func (c *collector) dump() string {
	var sb strings.Builder
	for _, e := range c.all() {
		sb.WriteString(string(e.Kind) + ": " + e.Text + "\n")
	}
	return sb.String()
}

func testAdapter() *Adapter {
	return New(Options{Grace: 300 * time.Millisecond, CancelWait: time.Second, ExitWait: time.Second, Version: "test"})
}

// fakeSpec builds a Spec whose agent is this test binary in fake mode.
func fakeSpec(t *testing.T, scenario string, env map[string]string, cfg map[string]any) adapter.Spec {
	t.Helper()
	// GORACE: the race runtime sleeps 1 s at exit by default, which would dominate every test.
	e := map[string]string{"ACPFAKE": scenario, "GORACE": "atexit_sleep_ms=0"}
	for k, v := range env {
		e[k] = v
	}
	c := map[string]any{"command": os.Args[0], "env": e}
	for k, v := range cfg {
		c[k] = v
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return adapter.Spec{Agent: "acp", Dir: t.TempDir(), System: "SISTEMA", Prompt: "PROMPT", Config: raw}
}

func run(t *testing.T, spec adapter.Spec) (adapter.Result, *collector, error) {
	t.Helper()
	col := &collector{}
	res, err := testAdapter().Run(context.Background(), spec, col)
	if t.Failed() || testing.Verbose() {
		t.Logf("events:\n%s", col.dump())
	}
	return res, col, err
}

func mustOK(t *testing.T, res adapter.Result, err error) {
	t.Helper()
	if err != nil || res.IsError {
		t.Fatalf("err=%v res=%+v", err, res)
	}
}

// ---- tests ----------------------------------------------------------------------------------

func TestHappyPath(t *testing.T) {
	res, col, err := run(t, fakeSpec(t, "happy", nil, nil))
	mustOK(t, res, err)
	if res.SessionRef != "sess-1" || res.ExitCode != 0 {
		t.Fatalf("res=%+v", res)
	}
	if want := "Feito: perm=no unk=-32601 blocks=2"; res.Summary != want {
		t.Fatalf("summary=%q want %q", res.Summary, want)
	}
	if res.CostUSD == nil || *res.CostUSD != 0.25 {
		t.Fatalf("cost=%v", res.CostUSD)
	}
	if want := (domain.Usage{InputTokens: 11, OutputTokens: 22, CacheReadTokens: 3, CacheCreationTokens: 4}); res.Usage != want {
		t.Fatalf("usage=%+v", res.Usage)
	}

	// chunks are aggregated per message: no event per token
	asst := col.ofKind(domain.EventAssistant)
	if len(asst) != 2 || asst[0].Text != "Vou ler o arquivo." {
		t.Fatalf("assistant events=%v", asst)
	}
	if th := col.ofKind(domain.EventThinking); len(th) != 1 || th[0].Text != "pensando mais" {
		t.Fatalf("thinking=%v", th)
	}
	if uses := col.ofKind(domain.EventToolUse); len(uses) != 2 || uses[0].Text != "Reading a.txt" || uses[1].Text != "Editing b.txt" {
		t.Fatalf("tool_use=%v", uses)
	}
	results := col.ofKind(domain.EventToolResult)
	if len(results) != 2 || results[0].Text != "conteúdo do arquivo" || results[1].Text != "erro: diff /x/b.txt (+2/-1 linhas)" {
		t.Fatalf("tool_result=%v", results)
	}
	if !col.has(domain.EventSystem, "permissão negada (política read_only): Editing b.txt [edit]") {
		t.Error("missing permission audit event")
	}
	if !col.has(domain.EventSystem, "plano do agente:\n- [pending] passo 1") || !col.has(domain.EventSystem, "aviso do agente (warning): Atenção — detalhe") {
		t.Error("missing plan/notice events")
	}
	if !col.has(domain.EventSystem, "agente ACP conectado: fake-agent 0.0.1") {
		t.Error("missing connected event")
	}
	last := col.all()[len(col.all())-1]
	if last.Kind != domain.EventResult || last.Text != res.Summary {
		t.Fatalf("last event=%+v", last)
	}
	if col.has(domain.EventSystem, "something_new") {
		t.Error("unknown update must be ignored")
	}
}

func TestPermissionPolicies(t *testing.T) {
	kinds := "read,search,think,fetch,edit,delete,move,execute,other,switch_mode,none"
	tests := []struct {
		name   string
		policy string
		ro     bool
		only   string
		want   string
	}{
		{"allow", "allow", false, "", "read=selected/y1 search=selected/y1 think=selected/y1 fetch=selected/y1 edit=selected/y1 delete=selected/y1 move=selected/y1 execute=selected/y1 other=selected/y1 switch_mode=selected/y1 none=selected/y1"},
		{"reject", "reject", false, "", "read=selected/n1 search=selected/n1 think=selected/n1 fetch=selected/n1 edit=selected/n1 delete=selected/n1 move=selected/n1 execute=selected/n1 other=selected/n1 switch_mode=selected/n1 none=selected/n1"},
		{"read_only", "read_only", false, "", "read=selected/y1 search=selected/y1 think=selected/y1 fetch=selected/y1 edit=selected/n1 delete=selected/n1 move=selected/n1 execute=selected/n1 other=selected/n1 switch_mode=selected/n1 none=selected/n1"},
		{"default is read_only", "", false, "", "read=selected/y1 search=selected/y1 think=selected/y1 fetch=selected/y1 edit=selected/n1 delete=selected/n1 move=selected/n1 execute=selected/n1 other=selected/n1 switch_mode=selected/n1 none=selected/n1"},
		{"review run forces read_only", "allow", true, "", "read=selected/y1 search=selected/y1 think=selected/y1 fetch=selected/y1 edit=selected/n1 delete=selected/n1 move=selected/n1 execute=selected/n1 other=selected/n1 switch_mode=selected/n1 none=selected/n1"},
		{"no compatible option is cancelled", "reject", false, "allow", "read=cancelled/<nil> search=cancelled/<nil> think=cancelled/<nil> fetch=cancelled/<nil> edit=cancelled/<nil> delete=cancelled/<nil> move=cancelled/<nil> execute=cancelled/<nil> other=cancelled/<nil> switch_mode=cancelled/<nil> none=cancelled/<nil>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{}
			if tc.policy != "" {
				cfg["permission"] = tc.policy
			}
			spec := fakeSpec(t, "perm", map[string]string{"ACPFAKE_KINDS": kinds, "ACPFAKE_ONLY": tc.only}, cfg)
			spec.ReadOnly = tc.ro
			res, _, err := run(t, spec)
			mustOK(t, res, err)
			if res.Summary != tc.want {
				t.Fatalf("got  %q\nwant %q", res.Summary, tc.want)
			}
		})
	}
}

func TestDecideAndPickOption(t *testing.T) {
	if !decide(PermReadOnly, "read") || decide(PermReadOnly, "edit") || decide(PermReadOnly, "") || !decide(PermAllow, "execute") || decide(PermReject, "read") {
		t.Fatal("decide")
	}
	opts := []permissionOption{{OptionID: "a", Kind: "allow_always"}, {OptionID: "r", Kind: "reject_always"}}
	if o, ok := pickOption(opts, true); !ok || o.OptionID != "a" {
		t.Fatalf("fallback to allow_always: %+v", o)
	}
	if o, ok := pickOption(opts, false); !ok || o.OptionID != "r" {
		t.Fatalf("fallback to reject_always: %+v", o)
	}
	if _, ok := pickOption(opts[:1], false); ok {
		t.Fatal("no reject option must not match")
	}
}

func TestFileAccessConfined(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("segredo"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := fakeSpec(t, "fs", nil, map[string]any{"permission": "allow"})
	dir := spec.Dir
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("l1\nl2\nl3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{1, 0, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	ops := []string{
		"read:a.txt",
		"readlines:a.txt",
		"read:@" + secret,
		"read:../" + filepath.Base(outside) + "/secret.txt",
		"read:link",
		"read:dirlink/secret.txt",
		"write:dirlink/new.txt",
		"read:missing.txt",
		"read:bin.dat",
		"write:sub/dir/n.txt",
		"write:@" + filepath.Join(outside, "evil.txt"),
		"writebig:big.txt",
	}
	spec.Config = withEnv(t, spec.Config, map[string]string{"ACPFAKE_OPS": strings.Join(ops, ";")})
	res, _, err := run(t, spec)
	mustOK(t, res, err)
	want := []string{
		`read:a.txt=OK"l1\nl2\nl3\n"`,
		`readlines:a.txt=OK"l2\n"`,
		`read:@` + secret + `=ERR-32602`,
		`read:../` + filepath.Base(outside) + `/secret.txt=ERR-32602`,
		`read:link=ERR-32602`,
		`read:dirlink/secret.txt=ERR-32602`,
		`write:dirlink/new.txt=ERR-32602`,
		`read:missing.txt=ERR-32002`,
		`read:bin.dat=ERR-32602`,
		`write:sub/dir/n.txt=OK`,
		`write:@` + filepath.Join(outside, "evil.txt") + `=ERR-32602`,
		`writebig:big.txt=ERR-32602`,
	}
	if got := strings.Split(res.Summary, " "); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "sub", "dir", "n.txt")); err != nil || string(b) != "novo\n" {
		t.Fatalf("written file: %q %v", b, err)
	}
	for _, p := range []string{filepath.Join(outside, "evil.txt"), filepath.Join(outside, "new.txt")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s must not exist", p)
		}
	}
	if b, _ := os.ReadFile(secret); string(b) != "segredo" {
		t.Fatal("outside file changed")
	}
}

func TestFileWritesRefusedWithoutPermission(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		ro   bool
		msg  string
	}{
		{"default policy", nil, false, "política read_only"},
		{"reject policy", map[string]any{"permission": "reject"}, false, "política reject"},
		{"review run", map[string]any{"permission": "allow"}, true, "somente leitura"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := fakeSpec(t, "fs", map[string]string{"ACPFAKE_OPS": "write:x.txt;read:y.txt"}, tc.cfg)
			spec.ReadOnly = tc.ro
			if err := os.WriteFile(filepath.Join(spec.Dir, "y.txt"), []byte("ok"), 0o600); err != nil {
				t.Fatal(err)
			}
			res, col, err := run(t, spec)
			mustOK(t, res, err)
			if res.Summary != `write:x.txt=ERR-32000 read:y.txt=OK"ok"` {
				t.Fatalf("summary=%q", res.Summary)
			}
			if !col.has(domain.EventSystem, tc.msg) {
				t.Fatalf("no audit event with %q", tc.msg)
			}
			if _, err := os.Stat(filepath.Join(spec.Dir, "x.txt")); err == nil {
				t.Fatal("file must not be written")
			}
		})
	}
}

func withEnv(t *testing.T, cfg json.RawMessage, extra map[string]string) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
	e, _ := m["env"].(map[string]any)
	for k, v := range extra {
		e[k] = v
	}
	m["env"] = e
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoadSession(t *testing.T) {
	t.Run("resumes and ignores the replay", func(t *testing.T) {
		spec := fakeSpec(t, "load", map[string]string{"ACPFAKE_LOAD": "1"}, nil)
		spec.SessionRef = "old-sess"
		res, col, err := run(t, spec)
		mustOK(t, res, err)
		if res.SessionRef != "old-sess" || res.Summary != "loaded=true" {
			t.Fatalf("res=%+v", res)
		}
		for _, e := range col.all() {
			if strings.Contains(e.Text, "REPLAY") {
				t.Fatalf("replay leaked into the log: %+v", e)
			}
		}
		if res.CostUSD == nil || *res.CostUSD != 0.5 {
			t.Fatalf("cost=%v, want 0.5 (1.5 accumulated - 1.0 replayed)", res.CostUSD)
		}
		if !col.has(domain.EventSystem, "sessão retomada: old-sess") {
			t.Error("missing resume event")
		}
	})
	t.Run("agent without loadSession starts a new session", func(t *testing.T) {
		spec := fakeSpec(t, "load", nil, nil)
		spec.SessionRef = "old-sess"
		res, col, err := run(t, spec)
		mustOK(t, res, err)
		if res.SessionRef != "sess-1" || res.Summary != "loaded=false" {
			t.Fatalf("res=%+v", res)
		}
		if !col.has(domain.EventSystem, "não anuncia loadSession") {
			t.Error("missing warning")
		}
	})
}

func TestCancelAsksAgentAndAnswersPermissionsCancelled(t *testing.T) {
	spec := fakeSpec(t, "cancel", nil, nil)
	col := &collector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for i := 0; i < 500 && !col.has(domain.EventSystem, "agente ACP conectado"); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond) // let the turn start
		cancel()
	}()
	t0 := time.Now()
	res, err := testAdapter().Run(ctx, spec, col)
	t.Logf("events:\n%s", col.dump())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Error, "interrompida") {
		t.Fatalf("res=%+v", res)
	}
	if res.Summary != "after-cancel=cancelled" {
		t.Fatalf("summary=%q", res.Summary)
	}
	if !col.has(domain.EventSystem, "permissão cancelada") {
		t.Error("missing cancelled permission event")
	}
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestCancelKillsAgentThatIgnoresIt(t *testing.T) {
	spec := fakeSpec(t, "cancel_ignore", nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	col := &collector{}
	t0 := time.Now()
	res, err := testAdapter().Run(ctx, spec, col)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Error, "interrompida") {
		t.Fatalf("res=%+v", res)
	}
	if d := time.Since(t0); d > 6*time.Second {
		t.Fatalf("took %v: the stubborn agent was not killed", d)
	}
}

func TestMuteAgent(t *testing.T) {
	spec := fakeSpec(t, "mute", nil, map[string]any{"init_timeout_sec": 1})
	t0 := time.Now()
	res, _, err := run(t, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Error, "não respondeu ao initialize em 1s") || !strings.Contains(res.Error, "fala ACP v1 em stdio") {
		t.Fatalf("res=%+v", res)
	}
	if d := time.Since(t0); d > 8*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestNotACPAgent(t *testing.T) {
	res, col, err := run(t, fakeSpec(t, "notacp", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Error, "código 2") || !strings.Contains(res.Error, "usage: notacp") || !strings.Contains(res.Error, "fatal: this is not an ACP agent") {
		t.Fatalf("res=%+v", res)
	}
	if res.ExitCode != 2 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	if !col.has(domain.EventStdout, "usage: notacp") || !col.has(domain.EventStderr, "fatal:") {
		t.Error("agent output not logged")
	}
}

func TestInvalidLinesAreLoggedAndSkipped(t *testing.T) {
	res, col, err := run(t, fakeSpec(t, "badline", nil, nil))
	mustOK(t, res, err)
	out := col.ofKind(domain.EventStdout)
	if len(out) != 3 || out[0].Text != "this is not json" || out[1].Text != `{"foo":1}` || out[2].Text != "[1,2,3]" {
		t.Fatalf("stdout events=%v", out)
	}
}

func TestIncompatibleVersionAndNotACP(t *testing.T) {
	res, _, err := run(t, fakeSpec(t, "badversion", nil, nil))
	if err != nil || !res.IsError || !strings.Contains(res.Error, "versão do ACP incompatível") || !strings.Contains(res.Error, "protocolVersion 2") {
		t.Fatalf("badversion: err=%v res=%+v", err, res)
	}
	res, _, err = run(t, fakeSpec(t, "noversion", nil, nil))
	if err != nil || !res.IsError || !strings.Contains(res.Error, "não é de um agente ACP") {
		t.Fatalf("noversion: err=%v res=%+v", err, res)
	}
}

func TestAuthRequired(t *testing.T) {
	res, _, err := run(t, fakeSpec(t, "auth", nil, nil))
	if err != nil || !res.IsError || !strings.Contains(res.Error, "exige autenticação") || !strings.Contains(res.Error, "oauth") || !strings.Contains(res.Error, "adapter_config.env") {
		t.Fatalf("err=%v res=%+v", err, res)
	}
}

func TestStopReasons(t *testing.T) {
	for sc, want := range map[string]string{"stop_max": "max_tokens", "stop_refusal": "refusal"} {
		res, col, err := run(t, fakeSpec(t, sc, nil, nil))
		if err != nil || !res.IsError || !strings.Contains(res.Error, want) {
			t.Fatalf("%s: err=%v res=%+v", sc, err, res)
		}
		if last := col.all()[len(col.all())-1]; last.Kind != domain.EventResult || !strings.Contains(last.Text, want) {
			t.Fatalf("%s: last event %+v", sc, last)
		}
	}
}

func TestAgentCrashDuringPrompt(t *testing.T) {
	res, _, err := run(t, fakeSpec(t, "crash", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.ExitCode != 3 || !strings.Contains(res.Error, "código 3") || !strings.Contains(res.Error, "panic: boom") {
		t.Fatalf("res=%+v", res)
	}
	if res.Summary != "começando" {
		t.Fatalf("summary=%q: partial work must be kept", res.Summary)
	}
}

func TestMCPServersConversion(t *testing.T) {
	mcp := json.RawMessage(`{"mcpServers":{
		"hanko":{"type":"http","url":"https://h.example/mcp","headers":{"Authorization":"Bearer tok-1234567890"}},
		"mem":{"command":"mem-mcp","args":["--x"],"env":{"B":"2","A":"1"}}}}`)
	t.Run("agent with http", func(t *testing.T) {
		spec := fakeSpec(t, "mcp", map[string]string{"ACPFAKE_HTTP": "1"}, nil)
		spec.MCPConfig = mcp
		res, _, err := run(t, spec)
		mustOK(t, res, err)
		if !strings.HasPrefix(res.Summary, "servers=[") || !strings.Contains(res.Summary, `"name":"hanko","type":"http","url":"https://h.example/mcp"`) ||
			!strings.Contains(res.Summary, `"env":[{"name":"A","value":"1"},{"name":"B","value":"2"}]`) {
			t.Fatalf("summary=%q", res.Summary)
		}
		if strings.Contains(res.Summary, "tok-1234567890") {
			t.Fatal("bearer token leaked into the result")
		}
	})
	t.Run("agent without http skips it with a warning", func(t *testing.T) {
		spec := fakeSpec(t, "mcp", nil, nil)
		spec.MCPConfig = mcp
		res, col, err := run(t, spec)
		mustOK(t, res, err)
		if strings.Contains(res.Summary, "hanko") || !strings.Contains(res.Summary, `"name":"mem"`) {
			t.Fatalf("summary=%q", res.Summary)
		}
		if !col.has(domain.EventSystem, `servidor MCP "hanko" ignorado`) {
			t.Error("missing warning")
		}
		for _, e := range col.all() {
			if strings.Contains(e.Text, "h.example") || strings.Contains(e.Text, "tok-1234567890") {
				t.Fatalf("credential-bearing detail in the log: %+v", e)
			}
		}
	})
	t.Run("no config sends an empty array", func(t *testing.T) {
		res, _, err := run(t, fakeSpec(t, "mcp", nil, nil))
		mustOK(t, res, err)
		if res.Summary != "servers=[]" {
			t.Fatalf("summary=%q", res.Summary)
		}
	})
}

func TestModelBestEffort(t *testing.T) {
	spec := fakeSpec(t, "model", nil, nil)
	spec.Model = "fast-1"
	res, _, err := run(t, spec)
	mustOK(t, res, err)
	if res.Model != "fast-1" {
		t.Fatalf("model=%q", res.Model)
	}

	spec = fakeSpec(t, "model", nil, map[string]any{"model": "Fast"}) // config fallback, matched by display name
	res, _, err = run(t, spec)
	mustOK(t, res, err)
	if res.Model != "fast-1" {
		t.Fatalf("config model: %q", res.Model)
	}

	spec = fakeSpec(t, "model", nil, nil)
	spec.Model = "nonexistent"
	res, col, err := run(t, spec)
	mustOK(t, res, err)
	if res.Model != "" || !col.has(domain.EventSystem, `o modelo "nonexistent" não está entre os que o agente oferece (default, fast-1)`) {
		t.Fatalf("model=%q events:\n%s", res.Model, col.dump())
	}

	spec = fakeSpec(t, "happy", nil, nil) // agent without model selection
	spec.Model = "anything"
	res, col, err = run(t, spec)
	mustOK(t, res, err)
	if !col.has(domain.EventSystem, "não anuncia escolha de modelo") {
		t.Error("missing warning")
	}
}

func TestCostOnlyInUSD(t *testing.T) {
	res, col, err := run(t, fakeSpec(t, "cost_brl", nil, nil))
	mustOK(t, res, err)
	if res.CostUSD != nil {
		t.Fatalf("cost=%v", *res.CostUSD)
	}
	n := 0
	for _, e := range col.ofKind(domain.EventSystem) {
		if strings.Contains(e.Text, `custo reportado em "BRL" ignorado`) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("warnings=%d, want 1", n)
	}
}

func TestStartFailureAndBadConfig(t *testing.T) {
	spec := fakeSpec(t, "happy", nil, map[string]any{"command": "/nonexistent/acp-agent-xyz"})
	res, _, err := run(t, spec)
	if err == nil || !res.IsError || !strings.Contains(res.Error, "não foi possível iniciar o agente ACP") {
		t.Fatalf("err=%v res=%+v", err, res)
	}

	spec.Config = json.RawMessage(`{"args":["x"]}`)
	res, _, err = run(t, spec)
	if err == nil || !res.IsError || !strings.Contains(res.Error, `"command" is required`) {
		t.Fatalf("err=%v res=%+v", err, res)
	}
}

func TestSecretsAreRedacted(t *testing.T) {
	spec := fakeSpec(t, "happy", map[string]string{"MY_API_TOKEN": "supersecretvalue123", "ACPFAKE_LEAK_ENV": "MY_API_TOKEN"}, nil)
	res, col, err := run(t, spec)
	mustOK(t, res, err)
	if !col.has(domain.EventStderr, "leak:"+adapter.Mask) {
		t.Fatalf("stderr not redacted:\n%s", col.dump())
	}
	for _, e := range col.all() {
		if strings.Contains(e.Text, "supersecretvalue123") || strings.Contains(string(e.Payload), "supersecretvalue123") {
			t.Fatalf("secret leaked: %+v", e)
		}
	}
}

func TestEmptyDirUsesTempDir(t *testing.T) {
	spec := fakeSpec(t, "fs", map[string]string{"ACPFAKE_OPS": "read:nope.txt"}, nil)
	spec.Dir = ""
	res, _, err := run(t, spec)
	mustOK(t, res, err)
	if res.Summary != "read:nope.txt=ERR-32002" {
		t.Fatalf("summary=%q", res.Summary)
	}
}
