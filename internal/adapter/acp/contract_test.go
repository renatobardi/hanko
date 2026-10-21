package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
)

// Teste de contrato dos agentes ACP com preset (ADR-0056 itens 8 a 10): para cada preset (devin, gemini,
// opencode) existe um executável falso no PATH com o nome real do comando. Ele registra os argumentos recebidos e
// vira o agente ACP falso deste pacote, que confere tudo o que o cliente envia no handshake e no prompt. Nenhum
// binário real, nenhuma rede.

// shimDir cria um diretório com um script por comando de preset e o põe na frente do PATH.
func shimDir(t *testing.T) (dir string, argsFile func(name string) string) {
	t.Helper()
	dir = t.TempDir()
	for _, p := range Presets {
		script := "#!/bin/sh\necho \"$@\" > \"" + filepath.Join(dir, p.Command+".args") + "\"\nexec \"$ACP_TEST_BIN\"\n"
		if err := os.WriteFile(filepath.Join(dir, p.Command), []byte(script), 0o755); err != nil { //nolint:gosec // script de teste
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name+".args"))
		if err != nil {
			return "<sem chamada: " + err.Error() + ">"
		}
		return strings.TrimSpace(string(b))
	}
}

func contractSpec(t *testing.T, cfg map[string]any, scenario string) adapter.Spec {
	t.Helper()
	env := map[string]string{"ACPFAKE": scenario, "GORACE": "atexit_sleep_ms=0", "ACP_TEST_BIN": os.Args[0]}
	c := map[string]any{"env": env}
	for k, v := range cfg {
		c[k] = v
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return adapter.Spec{Agent: "acp", Dir: t.TempDir(), System: "SISTEMA", Prompt: "PROMPT", Config: raw}
}

func TestPresetsContractAgainstFakeACPAgent(t *testing.T) {
	_, argsOf := shimDir(t)
	for _, p := range Presets {
		t.Run(p.Name, func(t *testing.T) {
			spec := contractSpec(t, map[string]any{"preset": p.Name}, "contract")
			col := &collector{}
			res, err := testAdapter().Run(context.Background(), spec, col)
			if err != nil || res.IsError {
				t.Fatalf("err=%v res=%+v\n%s", err, res, col.dump())
			}
			if res.Summary != "violations=[]" {
				t.Fatalf("o cliente quebrou o contrato ACP v1: %s", res.Summary)
			}
			if got, want := argsOf(p.Command), strings.Join(p.Args, " "); got != want {
				t.Fatalf("argumentos do %s = %q, quer %q", p.Command, got, want)
			}
		})
	}
}

func TestDevinFirstClassUsesFixedCommandWithoutOperatorGate(t *testing.T) {
	_, argsOf := shimDir(t)
	// política do operador FECHADA (zero value): o harness acp recusaria, o devin não passa por ela
	a := NewFixed(Options{Grace: 300 * time.Millisecond, CancelWait: time.Second, ExitWait: time.Second, Version: "test"}, DevinFixed(""))
	if a.Name() != "devin" {
		t.Fatalf("Name = %q", a.Name())
	}
	spec := contractSpec(t, map[string]any{"permission": "read_only"}, "contract")
	col := &collector{}
	res, err := a.Run(context.Background(), spec, col)
	if err != nil || res.IsError || res.Summary != "violations=[]" {
		t.Fatalf("err=%v res=%+v\n%s", err, res, col.dump())
	}
	if got := argsOf("devin"); got != "acp" {
		t.Fatalf("devin foi chamado com %q, quer \"acp\"", got)
	}
	// o mesmo comando por `acp` cai no gate do operador
	_, err = New(Options{Version: "test"}).Run(context.Background(), contractSpec(t, map[string]any{"preset": "devin"}, "contract"), &collector{})
	if err == nil {
		t.Fatal("acp sem HANKO_ACP_ALLOW deveria recusar")
	}
	// a persona não escolhe o executável do harness devin
	for _, cfg := range []map[string]any{{"command": "/bin/sh"}, {"args": []string{"-c", "id"}}, {"preset": "gemini"}} {
		if _, err := a.Run(context.Background(), contractSpec(t, cfg, "contract"), &collector{}); err == nil || !strings.Contains(err.Error(), "fixed by the operator") {
			t.Fatalf("cfg %v: err = %v", cfg, err)
		}
	}
}

func TestParsePresetAndAgentConfig(t *testing.T) {
	c, err := ParseConfig(json.RawMessage(`{"preset":"Gemini","args":["--extra"],"permission":"allow"}`))
	if err != nil || c.Command != "gemini" || strings.Join(c.Args, " ") != "--experimental-acp --extra" {
		t.Fatalf("%+v %v", c, err)
	}
	for in, want := range map[string]string{
		`{"preset":"nada"}`:                     "unknown \"preset\"",
		`{"preset":"gemini","command":"x"}`:     "mutually exclusive",
		`{"preset":"opencode","permision":"x"}`: "unknown field",
	} {
		if _, err := ParseConfig(json.RawMessage(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v (queria %q)", in, err, want)
		}
	}
	// devin: vazio vale; command é do operador
	if c, err := ParseAgentConfig("devin", nil); err != nil || c.Command != "devin" || strings.Join(c.Args, " ") != "acp" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := ParseAgentConfig("devin", json.RawMessage(`{"command":"x"}`)); err == nil {
		t.Fatal("command em devin deve falhar")
	}
	if _, err := ParseAgentConfig("devin", json.RawMessage(`{"permission":"zzz"}`)); err == nil {
		t.Fatal("permission inválida deve falhar")
	}
	if _, err := ParseAgentConfig("acp", nil); err == nil {
		t.Fatal("acp sem command nem preset deve falhar")
	}
	if f := DevinFixed("/opt/devin"); f.Command != "/opt/devin" {
		t.Fatalf("%+v", f)
	}
}

func TestProbeStepsForReadyAuthMissingAndNotACP(t *testing.T) {
	_, _ = shimDir(t)
	a := NewFixed(Options{Grace: 300 * time.Millisecond, CancelWait: time.Second, ExitWait: time.Second, Version: "test"}, DevinFixed(""))
	step := func(r adapter.ProbeResult, name string) *adapter.ProbeStep {
		for i := range r.Steps {
			if r.Steps[i].Name == name {
				return &r.Steps[i]
			}
		}
		return nil
	}

	ready := a.Probe(context.Background(), contractSpec(t, nil, "contract"))
	if !ready.OK || step(ready, "binário") == nil || !step(ready, "autenticação").OK || !step(ready, "protocolo").OK {
		t.Fatalf("pronto: %+v", ready)
	}

	auth := a.Probe(context.Background(), contractSpec(t, nil, "auth"))
	if auth.OK || !step(auth, "protocolo").OK || step(auth, "autenticação").OK ||
		!strings.Contains(step(auth, "autenticação").Detail, "exige autenticação") || !strings.Contains(step(auth, "autenticação").Detail, "oauth") {
		t.Fatalf("sem login: %+v", auth)
	}

	notACP := a.Probe(context.Background(), contractSpec(t, map[string]any{"init_timeout_sec": 2}, "notacp"))
	if notACP.OK || step(notACP, "protocolo") == nil || step(notACP, "protocolo").OK {
		t.Fatalf("não é ACP: %+v", notACP)
	}

	t.Setenv("PATH", t.TempDir()) // devin não está no PATH
	gone := a.Probe(context.Background(), contractSpec(t, nil, "contract"))
	if gone.OK || step(gone, "binário") == nil || step(gone, "binário").OK || !strings.Contains(step(gone, "binário").Detail, "devin") {
		t.Fatalf("binário ausente: %+v", gone)
	}

	bad := a.Probe(context.Background(), adapter.Spec{Config: json.RawMessage(`{"command":"x"}`)})
	if bad.OK || bad.Steps[0].Name != "configuração" {
		t.Fatalf("config ruim: %+v", bad)
	}
	// acp genérico: o gate do operador é um passo do probe
	gated := New(Options{Version: "test"}).Probe(context.Background(), contractSpec(t, map[string]any{"preset": "gemini"}, "contract"))
	if gated.OK || step(gated, "política do operador") == nil || step(gated, "política do operador").OK {
		t.Fatalf("gate: %+v", gated)
	}
}
