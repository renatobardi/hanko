package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// O próprio binário de teste faz o papel de plugin (TestHelperProcess), copiado para um diretório de plugin.
var helperDir string

func TestMain(m *testing.M) {
	if os.Getenv("GO_PLUGIN_HELPER") != "" {
		helperMain(os.Getenv("GO_PLUGIN_HELPER"))
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "hanko-plugin-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	self, err := os.ReadFile(os.Args[0])
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "plugin"), self, 0o755) //nolint:gosec // binário de teste
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	helperDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// helperMain é um plugin mínimo controlado pelo modo.
func helperMain(mode string) {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 4<<20)
	var mu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		_, _ = os.Stdout.Write(append(b, '\n'))
		mu.Unlock()
	}
	var nextID int64 = 1000
	pending := map[int64]chan json.RawMessage{}
	var pmu sync.Mutex
	callHost := func(method string, params any) json.RawMessage {
		pmu.Lock()
		nextID++
		id := nextID
		ch := make(chan json.RawMessage, 1)
		pending[id] = ch
		pmu.Unlock()
		write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		return <-ch
	}
	marker := filepath.Join(os.Getenv("HOME"), "crashed")
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" {
			var id int64
			_ = json.Unmarshal(m.ID, &id)
			pmu.Lock()
			ch := pending[id]
			pmu.Unlock()
			if ch != nil {
				if len(m.Error) > 0 {
					ch <- m.Error
				} else {
					ch <- m.Result
				}
			}
			continue
		}
		reply := func(res any) { write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": res}) }
		switch m.Method {
		case "initialize":
			if mode == "badinit" {
				write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32000, "message": "não quero"}})
				continue
			}
			reply(map[string]any{"api_version": 1})
		case "health":
			if mode == "sick" {
				reply(map[string]any{"ok": false, "message": "doente"})
				continue
			}
			reply(map[string]any{"ok": true})
		case "shutdown":
			reply(nil)
			if mode == "stubborn" {
				continue // não sai: o host precisa mandar SIGTERM
			}
			os.Exit(0)
		case "echo":
			reply(json.RawMessage(m.Params))
		case "env":
			reply(os.Environ())
		case "slow":
			// nunca responde
		case "huge":
			_, _ = os.Stdout.Write([]byte(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":"` + strings.Repeat("x", 2<<20) + "\"}\n"))
		case "crash_once":
			if _, err := os.Stat(marker); err != nil {
				_ = os.WriteFile(marker, []byte("1"), 0o600)
				os.Exit(3)
			}
			reply("vivo")
		case "ask_host":
			go func(id json.RawMessage) {
				res := callHost("tasks.list", map[string]any{"company": "acme"})
				write(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
			}(m.ID)
		case "stderr":
			fmt.Fprintln(os.Stderr, "linha de erro do plugin")
			reply(nil)
		default:
			write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "método desconhecido"}})
		}
	}
}

type recorder struct {
	mu     sync.Mutex
	logs   []string
	states []string
	calls  []string
}

func (r *recorder) log(_, level, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, level+": "+msg)
}

func (r *recorder) state(_ string, ready bool, errText string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, fmt.Sprintf("%v:%s", ready, errText))
}

func (r *recorder) hasLog(sub string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func newTestManager(t *testing.T, rec *recorder, mod func(*Options)) *Manager {
	t.Helper()
	o := Options{HostVersion: "test", OnLog: rec.log, OnState: rec.state,
		Handler: func(_ context.Context, key, method string, params json.RawMessage) (any, *RPCError) {
			rec.mu.Lock()
			rec.calls = append(rec.calls, key+" "+method+" "+string(params))
			rec.mu.Unlock()
			if method == "tasks.list" {
				return []string{"HNK-1"}, nil
			}
			return nil, Errorf(CodeMethodNotFound, "não existe")
		},
		BackoffMin: 50 * time.Millisecond, BackoffMax: 200 * time.Millisecond, ShutdownWait: 2 * time.Second, KillGrace: time.Second,
		InitTimeout: 5 * time.Second, HealthTimeout: 2 * time.Second}
	if mod != nil {
		mod(&o)
	}
	m := NewManager(o)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m.StopAll(ctx)
	})
	return m
}

func helperSpec(t *testing.T, mode string) Spec {
	work := t.TempDir()
	return Spec{Key: "hello", Version: "1.0.0", Dir: helperDir, Command: "plugin", Args: []string{"-test.run=^$"}, WorkDir: work,
		Env: append(Env(os.Environ(), work, false), "GO_PLUGIN_HELPER="+mode)}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("esperando %s", what)
}

func TestStartCallStop(t *testing.T) {
	rec := &recorder{}
	m := newTestManager(t, rec, nil)
	spec := helperSpec(t, "ok")
	ctx := context.Background()
	m.Start(ctx, spec)
	if err := m.Ready(ctx, "hello"); err != nil {
		t.Fatalf("ready: %v", err)
	}
	st := m.Status("hello")
	if !st.Running || !st.Ready || st.PID == 0 || st.StartedAt == nil {
		t.Fatalf("status = %+v", st)
	}
	res, err := m.Call(ctx, "hello", "echo", map[string]string{"a": "b"}, 0)
	if err != nil || string(res) != `{"a":"b"}` {
		t.Fatalf("echo = %s, %v", res, err)
	}
	// ambiente: allowlist mínima, sem HANKO_*, HOME no diretório de trabalho
	raw, err := m.Call(ctx, "hello", "env", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	_ = json.Unmarshal(raw, &env)
	for _, kv := range env {
		if strings.HasPrefix(kv, "HANKO_") || strings.HasPrefix(kv, "ANTHROPIC_") || strings.HasPrefix(kv, "AWS_") {
			t.Errorf("variável vazou para o plugin: %s", kv)
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "HOME="+spec.WorkDir) {
		t.Errorf("HOME não é o diretório de trabalho: %v", env)
	}
	// erro do plugin volta como RPCError
	_, err = m.Call(ctx, "hello", "nada", nil, 0)
	var re *RPCError
	if !errors.As(err, &re) || re.Code != CodeMethodNotFound {
		t.Fatalf("err = %v", err)
	}
	// stderr vai para o log
	if _, err := m.Call(ctx, "hello", "stderr", nil, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stderr no log", func() bool { return rec.hasLog("stderr: linha de erro do plugin") })

	pid := st.PID
	if err := m.Stop(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if st := m.Status("hello"); st.Running {
		t.Fatalf("ainda rodando: %+v", st)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("processo %d continua vivo", pid)
	}
	if _, err := m.Call(ctx, "hello", "echo", nil, 0); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("call depois do stop = %v", err)
	}
}

func TestCrashRestartsWithBackoff(t *testing.T) {
	rec := &recorder{}
	m := newTestManager(t, rec, nil)
	ctx := context.Background()
	m.Start(ctx, helperSpec(t, "ok"))
	if err := m.Ready(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	first := m.Status("hello").PID
	_, err := m.Call(ctx, "hello", "crash_once", nil, 2*time.Second)
	if err == nil {
		t.Fatal("o processo devia ter caído")
	}
	waitFor(t, "reinício", func() bool { st := m.Status("hello"); return st.Ready && st.PID != first && st.Restarts >= 1 })
	res, err := m.Call(ctx, "hello", "crash_once", nil, 0)
	if err != nil || string(res) != `"vivo"` {
		t.Fatalf("depois do reinício = %s, %v", res, err)
	}
	if !rec.hasLog("processo caiu") {
		t.Errorf("queda não registrada: %v", rec.logs)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.states) < 3 || !strings.HasPrefix(rec.states[1], "false:") {
		t.Errorf("estados = %v", rec.states)
	}
}

func TestInitFailureIsReported(t *testing.T) {
	for _, mode := range []string{"badinit", "sick"} {
		t.Run(mode, func(t *testing.T) {
			rec := &recorder{}
			m := newTestManager(t, rec, nil)
			ctx := context.Background()
			m.Start(ctx, helperSpec(t, mode))
			err := m.Ready(ctx, "hello")
			if err == nil {
				t.Fatal("ready devia falhar")
			}
			if mode == "sick" && !strings.Contains(err.Error(), "doente") {
				t.Errorf("err = %v", err)
			}
			if _, err := m.Call(ctx, "hello", "echo", nil, 0); !errors.Is(err, ErrNotRunning) {
				t.Errorf("call sem estar pronto = %v", err)
			}
		})
	}
}

func TestCallTimeoutAndMessageLimit(t *testing.T) {
	rec := &recorder{}
	m := newTestManager(t, rec, nil)
	ctx := context.Background()
	m.Start(ctx, helperSpec(t, "ok"))
	if err := m.Ready(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := m.Call(ctx, "hello", "slow", nil, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "tempo esgotado") || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout = %v (%s)", err, time.Since(start))
	}
	// resposta de 2 MiB é descartada (a chamada estoura o prazo) e a conexão segue viva
	_, err = m.Call(ctx, "hello", "huge", nil, 500*time.Millisecond)
	if err == nil {
		t.Fatal("mensagem acima do limite foi aceita")
	}
	waitFor(t, "aviso de mensagem grande", func() bool { return rec.hasLog("maior que") })
	if _, err := m.Call(ctx, "hello", "echo", "ok", 0); err != nil {
		t.Fatalf("conexão morreu: %v", err)
	}
	// mensagem de saída acima do limite é recusada antes de ir para o fio
	if _, err := m.Call(ctx, "hello", "echo", strings.Repeat("y", 2<<20), 0); !errors.Is(err, errTooLarge) {
		t.Fatalf("saída grande = %v", err)
	}
}

func TestPluginCallsHost(t *testing.T) {
	rec := &recorder{}
	m := newTestManager(t, rec, nil)
	ctx := context.Background()
	m.Start(ctx, helperSpec(t, "ok"))
	if err := m.Ready(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	res, err := m.Call(ctx, "hello", "ask_host", nil, 0)
	if err != nil || string(res) != `["HNK-1"]` {
		t.Fatalf("ask_host = %s, %v", res, err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) != 1 || !strings.HasPrefix(rec.calls[0], "hello tasks.list") {
		t.Fatalf("calls = %v", rec.calls)
	}
}

func TestStubbornPluginIsTerminated(t *testing.T) {
	rec := &recorder{}
	m := newTestManager(t, rec, func(o *Options) { o.ShutdownWait = 300 * time.Millisecond })
	ctx := context.Background()
	m.Start(ctx, helperSpec(t, "stubborn"))
	if err := m.Ready(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	pid := m.Status("hello").PID
	start := time.Now()
	if err := m.Stop(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("stop demorou %s", time.Since(start))
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("processo %d sobreviveu", pid)
	}
}

func TestSpecChangeRestarts(t *testing.T) {
	rec := &recorder{}
	m := newTestManager(t, rec, nil)
	ctx := context.Background()
	s := helperSpec(t, "ok")
	m.Start(ctx, s)
	if err := m.Ready(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	pid := m.Status("hello").PID
	m.Start(ctx, s) // mesma spec: nada muda
	if m.Status("hello").PID != pid {
		t.Fatal("mesma spec reiniciou o processo")
	}
	s.Version = "1.1.0"
	m.Start(ctx, s)
	if err := m.Ready(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if st := m.Status("hello"); st.PID == pid || st.Version != "1.1.0" {
		t.Fatalf("versão nova não reiniciou: %+v", st)
	}
}

func TestCommandMustStayInsideDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/bin/sh", filepath.Join(dir, "fora")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCommand(dir, "fora"); err == nil {
		t.Fatal("link simbólico para fora do diretório foi aceito")
	}
	if err := os.WriteFile(filepath.Join(dir, "dados"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCommand(dir, "dados"); err == nil {
		t.Fatal("arquivo não executável foi aceito")
	}
}

func TestEnvAllowlist(t *testing.T) {
	env := Env([]string{"PATH=/x", "HANKO_SECRET=s", "ANTHROPIC_API_KEY=k", "HTTPS_PROXY=p", "LC_ALL=C", "HOME=/root"}, "/w", false)
	got := strings.Join(env, " ")
	if got != "HOME=/w LC_ALL=C PATH=/x TMPDIR=/w/tmp" {
		t.Fatalf("env = %s", got)
	}
	if !strings.Contains(strings.Join(Env([]string{"HTTPS_PROXY=p"}, "", true), " "), "HTTPS_PROXY=p") {
		t.Fatal("proxy não passou com http.outbound")
	}
}

