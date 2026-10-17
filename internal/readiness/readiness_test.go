package readiness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
)

// fakeHarness grava um "claude"/"codex" de teste no PATH: --version imprime a versão e `auth status`/`login status`
// sai com authExit.
func fakeHarness(t *testing.T, dir, name string, authExit int) {
	t.Helper()
	script := "#!/bin/sh\ncase \"$1\" in\n--version) echo \"" + name + " 9.9.9\";;\nauth|login) exit " + string(rune('0'+authExit)) + ";;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { //nolint:gosec // script de teste
		t.Fatal(err)
	}
}

// localRunner roda os scripts no servidor com um PATH e um HOME controlados.
func localRunner(pathDir, home string) Runner {
	return func(ctx context.Context, script string, env map[string]string) ([]byte, error) {
		e := map[string]string{"PATH": pathDir + ":/usr/bin:/bin", "HOME": home}
		for k, v := range env {
			e[k] = v
		}
		return execenv.RunScript(ctx, execenv.Config{}, execenv.Target{Driver: domain.EnvLocal}, script, e)
	}
}

func step(r Result, name string) domain.ReadinessStep {
	for _, s := range r.Steps {
		if s.Name == name {
			return s
		}
	}
	return domain.ReadinessStep{}
}

func TestCheckClaudeStates(t *testing.T) {
	bin, home := t.TempDir(), t.TempDir()
	prices := []string{"claude-sonnet"}
	in := Input{Agent: "claude", Model: "claude-sonnet-4-5", PriceModels: prices, EnvironmentName: "local"}

	// sem o binário
	r := Check(context.Background(), Config{}, in, localRunner(bin, home))
	if s := step(r, domain.StepBinary); s.Status != domain.StepFail || !strings.Contains(s.Detail, "PATH") {
		t.Fatalf("binário ausente: %+v", r.Steps)
	}
	if s := step(r, domain.StepAuth); s.Status != domain.StepSkip {
		t.Fatalf("sem binário o auth é skip: %+v", r.Steps)
	}
	if st, reason := domain.SummarizeReadiness(r.Steps); st != domain.ReadinessNotReady || !strings.HasPrefix(reason, "binary:") {
		t.Fatalf("veredito: %v %q", st, reason)
	}

	// binário presente, login quebrado (exit 1) e sem chave: auth falha com a dica
	fakeHarness(t, bin, "claude", 1)
	r = Check(context.Background(), Config{}, in, localRunner(bin, home))
	if s := step(r, domain.StepBinary); s.Status != domain.StepOK || !strings.Contains(s.Detail, "9.9.9") {
		t.Fatalf("binário: %+v", s)
	}
	if s := step(r, domain.StepAuth); s.Status != domain.StepFail || !strings.Contains(s.Detail, "claude auth login") {
		t.Fatalf("auth quebrada deve ser detectada: %+v", r.Steps)
	}
	if len(r.ReportedModels) != 0 {
		t.Fatal("persona que não passa não relata modelo")
	}

	// chave de API no env da persona resolve o auth
	in2 := in
	in2.AdapterConfig = json.RawMessage(`{"env":{"ANTHROPIC_API_KEY":"sk-x"}}`)
	r = Check(context.Background(), Config{}, in2, localRunner(bin, home))
	if s := step(r, domain.StepAuth); s.Status != domain.StepOK || !strings.Contains(s.Detail, "chave") {
		t.Fatalf("auth por env: %+v", r.Steps)
	}
	if st, _ := domain.SummarizeReadiness(r.Steps); st != domain.ReadinessReady || len(r.ReportedModels) != 1 || r.ReportedModels[0] != "claude-sonnet-4-5" {
		t.Fatalf("pronta: %+v %v", r.Steps, r.ReportedModels)
	}

	// login do harness (exit 0)
	fakeHarness(t, bin, "claude", 0)
	r = Check(context.Background(), Config{}, in, localRunner(bin, home))
	if s := step(r, domain.StepAuth); s.Status != domain.StepOK || !strings.Contains(s.Detail, "login") {
		t.Fatalf("auth por login: %+v", r.Steps)
	}

	// arquivo de credenciais também vale
	fakeHarness(t, bin, "claude", 1)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = Check(context.Background(), Config{}, in, localRunner(bin, home))
	if s := step(r, domain.StepAuth); s.Status != domain.StepOK {
		t.Fatalf("credenciais em arquivo: %+v", r.Steps)
	}
}

func TestCheckCodexAndCustomBin(t *testing.T) {
	bin, home := t.TempDir(), t.TempDir()
	fakeHarness(t, bin, "meu-codex", 1)
	cfg := Config{CodexBin: "meu-codex"}
	in := Input{Agent: "codex", Model: "gpt-5"}
	r := Check(context.Background(), cfg, in, localRunner(bin, home))
	if s := step(r, domain.StepAuth); s.Status != domain.StepFail || !strings.Contains(s.Detail, "codex login") {
		t.Fatalf("codex sem login: %+v", r.Steps)
	}
	in.AdapterConfig = json.RawMessage(`{"env":{"OPENAI_API_KEY":"k"}}`)
	r = Check(context.Background(), cfg, in, localRunner(bin, home))
	if s := step(r, domain.StepAuth); s.Status != domain.StepOK {
		t.Fatalf("codex por env: %+v", r.Steps)
	}
}

func TestCheckFakeAndACP(t *testing.T) {
	bin, home := t.TempDir(), t.TempDir()
	r := Check(context.Background(), Config{}, Input{Agent: "fake"}, localRunner(bin, home))
	if st, _ := domain.SummarizeReadiness(r.Steps); st != domain.ReadinessReady {
		t.Fatalf("fake está sempre pronto: %+v", r.Steps)
	}
	if s := step(r, domain.StepModel); s.Status != domain.StepSkip {
		t.Fatalf("modelo do fake: %+v", s)
	}
	// acp: precisa de command; auth não verificável
	r = Check(context.Background(), Config{}, Input{Agent: "acp"}, localRunner(bin, home))
	if s := step(r, domain.StepBinary); s.Status != domain.StepFail || !strings.Contains(s.Detail, "command") {
		t.Fatalf("acp sem command: %+v", r.Steps)
	}
	fakeHarness(t, bin, "meu-agente", 0)
	r = Check(context.Background(), Config{}, Input{Agent: "acp", AdapterConfig: json.RawMessage(`{"command":"meu-agente"}`)}, localRunner(bin, home))
	if s := step(r, domain.StepBinary); s.Status != domain.StepOK {
		t.Fatalf("acp com command: %+v", r.Steps)
	}
	if s := step(r, domain.StepAuth); s.Status != domain.StepSkip {
		t.Fatalf("auth do acp não é verificável: %+v", s)
	}
	// comando com caracteres de shell é recusado antes de rodar
	r = Check(context.Background(), Config{}, Input{Agent: "acp", AdapterConfig: json.RawMessage(`{"command":"x; rm -rf /"}`)}, func(context.Context, string, map[string]string) ([]byte, error) {
		t.Fatal("não deveria executar um comando inválido")
		return nil, nil
	})
	if s := step(r, domain.StepBinary); s.Status != domain.StepFail {
		t.Fatalf("comando inválido: %+v", r.Steps)
	}
	if _, err := Script(Config{}, "outro", ""); err == nil {
		t.Fatal("harness desconhecido")
	}
}

func TestCheckEnvironmentDown(t *testing.T) {
	down := func(context.Context, string, map[string]string) ([]byte, error) {
		return nil, errors.New("ssh: connect to host build.example.com port 22: Connection refused")
	}
	r := Check(context.Background(), Config{}, Input{Agent: "claude", Model: "sonnet", EnvironmentName: "build"}, down)
	if s := step(r, domain.StepEnvironment); s.Status != domain.StepFail || !strings.Contains(s.Detail, "Connection refused") {
		t.Fatalf("ambiente fora: %+v", r.Steps)
	}
	for _, n := range []string{domain.StepBinary, domain.StepAuth, domain.StepModel} {
		if step(r, n).Status != domain.StepSkip {
			t.Errorf("%s deveria ser skip: %+v", n, r.Steps)
		}
	}
	if len(r.Steps) != len(domain.ReadinessSteps) {
		t.Fatalf("passos: %+v", r.Steps)
	}
	if st, reason := domain.SummarizeReadiness(r.Steps); st != domain.ReadinessNotReady || !strings.HasPrefix(reason, "environment:") {
		t.Fatalf("%v %q", st, reason)
	}
}

func TestModelStep(t *testing.T) {
	prices := []string{"claude-sonnet", "gpt-"}
	cases := []struct {
		agent, model string
		want         domain.ReadinessStepStatus
	}{
		{"claude", "", domain.StepOK},
		{"claude", "claude-sonnet-4-5", domain.StepOK},
		{"claude", "sonnet", domain.StepOK},
		{"claude", "modelo-novo", domain.StepWarn},
		{"claude", "bad model!", domain.StepFail},
		{"codex", "gpt-5", domain.StepOK},
		{"fake", "qualquer", domain.StepSkip},
	}
	for _, c := range cases {
		s := modelStep(Input{Agent: c.agent, Model: c.model, PriceModels: prices})
		if s.Status != c.want {
			t.Errorf("%s/%q: %+v, quer %s", c.agent, c.model, s, c.want)
		}
	}
	if s := modelStep(Input{Agent: "claude", Model: "modelo-novo"}); !strings.Contains(s.Detail, "preço de reserva") {
		t.Fatalf("aviso de preço: %+v", s)
	}
}
