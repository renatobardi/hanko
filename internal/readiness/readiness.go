// Package readiness decide se o harness de uma persona consegue rodar (ADR-0055, #10): o binário existe no ambiente
// de execução, há credencial utilizável e o modelo é conhecido. O pacote não conhece banco nem rede: monta o script
// POSIX de cada harness, entrega a um Runner (que o executa no ambiente da persona) e traduz a saída em passos
// nomeados. O probe nunca chama o modelo: não gasta tokens.
package readiness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Config são os binários dos harnesses locais (HANKO_CLAUDE_BIN, HANKO_CODEX_BIN).
type Config struct {
	ClaudeBin, CodexBin string
}

func (c Config) claude() string {
	if c.ClaudeBin != "" {
		return c.ClaudeBin
	}
	return "claude"
}

func (c Config) codex() string {
	if c.CodexBin != "" {
		return c.CodexBin
	}
	return "codex"
}

// Runner executa um script POSIX curto no ambiente de execução da persona, com as variáveis dela, e devolve o
// stdout. Um erro com saída parcial ainda é lido (o script termina com exit != 0 quando o binário falta).
type Runner func(ctx context.Context, script string, env map[string]string) ([]byte, error)

// Input descreve a persona a testar.
type Input struct {
	Agent string
	Model string
	// AdapterConfig é o adapter_config da persona já com as referências ${secret:} resolvidas.
	AdapterConfig json.RawMessage
	// EnvironmentName só aparece no detalhe do passo "environment".
	EnvironmentName string
	// PriceModels são os prefixos de model_prices (para o aviso de modelo sem preço).
	PriceModels []string
}

// Result é o resultado do probe.
type Result struct {
	Steps      []domain.ReadinessStep
	DurationMs int
	// ReportedModels são os modelos que o probe viu funcionando (hoje o da persona, quando todos os passos passam).
	ReportedModels []string
}

var modelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\[\]-]{0,127}$`)
var cmdRe = regexp.MustCompile(`^[A-Za-z0-9._/+-]{1,200}$`)

// Script devolve o script POSIX que descobre binário, versão e credencial do harness. exported para os testes e
// para quem quiser mostrar o que o probe executa.
func Script(cfg Config, agent string, acpCommand string) (string, error) {
	switch agent {
	case "claude":
		return script(cfg.claude(), "claude", `
if [ -n "$ANTHROPIC_API_KEY" ] || [ -n "$ANTHROPIC_AUTH_TOKEN" ] || [ -n "$CLAUDE_CODE_OAUTH_TOKEN" ]; then echo "auth=env"
elif [ -n "$b" ] && "$b" auth status >/dev/null 2>&1; then echo "auth=login"
elif [ -s "$HOME/.claude/.credentials.json" ]; then echo "auth=login"
else echo "auth=none"; fi`), nil
	case "codex":
		return script(cfg.codex(), "codex", `
if [ -n "$OPENAI_API_KEY" ] || [ -n "$CODEX_API_KEY" ]; then echo "auth=env"
elif [ -n "$b" ] && "$b" login status >/dev/null 2>&1; then echo "auth=login"
elif [ -s "$HOME/.codex/auth.json" ]; then echo "auth=login"
else echo "auth=none"; fi`), nil
	case "acp":
		if !cmdRe.MatchString(acpCommand) {
			return "", fmt.Errorf("adapter_config.command ausente ou inválido para o harness acp")
		}
		return script(acpCommand, "acp", "\necho \"auth=unknown\""), nil
	case "fake":
		return `echo "bin=fake"; echo "version=fake"; echo "auth=skip"`, nil
	}
	return "", fmt.Errorf("harness desconhecido: %q", agent)
}

func script(bin, label, authPart string) string {
	q := quote(bin)
	return `b=$(command -v ` + q + ` 2>/dev/null) || b=
echo "bin=$b"
if [ -n "$b" ]; then v=$("$b" --version 2>/dev/null | head -n 1); echo "version=$v"; fi` + authPart + `
echo "label=` + label + `"`
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// parsed é a saída do script.
type parsed struct {
	bin, version, auth string
}

func parse(out []byte) parsed {
	var p parsed
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		switch k {
		case "bin":
			p.bin = strings.TrimSpace(v)
		case "version":
			p.version = strings.TrimSpace(v)
		case "auth":
			p.auth = strings.TrimSpace(v)
		}
	}
	return p
}

// envOf extrai o env do adapter_config (a env da persona, igual para claude, codex e acp).
func envOf(raw json.RawMessage) map[string]string {
	var c struct {
		Env     map[string]string `json:"env"`
		Command string            `json:"command"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil {
		return nil
	}
	return c.Env
}

func commandOf(raw json.RawMessage) string {
	var c struct {
		Command string `json:"command"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil {
		return ""
	}
	return c.Command
}

// Check roda o probe e devolve os passos, sempre na ordem de domain.ReadinessSteps. Se o ambiente não responde, os
// passos seguintes ficam "skip" (não dá para saber). Nunca devolve erro: falhas são passos.
func Check(ctx context.Context, cfg Config, in Input, run Runner) Result {
	start := time.Now()
	var res Result
	add := func(name string, st domain.ReadinessStepStatus, detail string) {
		res.Steps = append(res.Steps, domain.ReadinessStep{Name: name, Status: st, Detail: detail})
	}
	finish := func() Result {
		res.DurationMs = int(time.Since(start).Milliseconds())
		return res
	}
	scriptText, err := Script(cfg, in.Agent, commandOf(in.AdapterConfig))
	if err != nil {
		add(domain.StepEnvironment, domain.StepSkip, "")
		add(domain.StepBinary, domain.StepFail, err.Error())
		add(domain.StepAuth, domain.StepSkip, "")
		res.Steps = append(res.Steps, modelStep(in))
		return finish()
	}
	out, err := run(ctx, scriptText, envOf(in.AdapterConfig))
	p := parse(out)
	if err != nil && p.bin == "" && p.auth == "" && len(out) == 0 {
		add(domain.StepEnvironment, domain.StepFail, clip(err.Error()))
		add(domain.StepBinary, domain.StepSkip, "")
		add(domain.StepAuth, domain.StepSkip, "")
		res.Steps = append(res.Steps, skipModel(in))
		return finish()
	}
	d := in.EnvironmentName
	if d == "" {
		d = "local"
	}
	add(domain.StepEnvironment, domain.StepOK, d)
	if p.bin == "" {
		add(domain.StepBinary, domain.StepFail, fmt.Sprintf("o binário do harness %s não foi encontrado no PATH do ambiente", in.Agent))
	} else {
		d := p.bin
		if p.version != "" {
			d += " (" + clip(p.version) + ")"
		}
		add(domain.StepBinary, domain.StepOK, d)
	}
	switch {
	case p.bin == "":
		add(domain.StepAuth, domain.StepSkip, "sem o binário não há como verificar")
	case p.auth == "env":
		add(domain.StepAuth, domain.StepOK, "chave de API no ambiente da persona ou do servidor")
	case p.auth == "login":
		add(domain.StepAuth, domain.StepOK, "login do harness")
	case p.auth == "skip":
		add(domain.StepAuth, domain.StepSkip, "não se aplica")
	case p.auth == "unknown":
		add(domain.StepAuth, domain.StepSkip, "o harness acp autentica por conta própria; não dá para verificar daqui")
	default:
		add(domain.StepAuth, domain.StepFail, authHint(in.Agent))
	}
	res.Steps = append(res.Steps, modelStep(in))
	if st, _ := domain.SummarizeReadiness(res.Steps); st == domain.ReadinessReady && in.Model != "" {
		res.ReportedModels = []string{in.Model}
	}
	return finish()
}

func authHint(agent string) string {
	switch agent {
	case "claude":
		return "sem login do claude nem chave de API (ANTHROPIC_API_KEY): rode `claude auth login` no host de execução ou defina a chave no env da persona"
	case "codex":
		return "sem login do codex nem chave de API (OPENAI_API_KEY): rode `codex login` no host de execução ou defina a chave no env da persona"
	}
	return "sem credencial utilizável para o harness " + agent
}

// modelStep valida o modelo sem rede: formato e preço.
func modelStep(in Input) domain.ReadinessStep {
	m := strings.TrimSpace(in.Model)
	switch {
	case in.Agent == "fake":
		return domain.ReadinessStep{Name: domain.StepModel, Status: domain.StepSkip, Detail: "não se aplica"}
	case m == "":
		return domain.ReadinessStep{Name: domain.StepModel, Status: domain.StepOK, Detail: "padrão do harness"}
	case !modelRe.MatchString(m):
		return domain.ReadinessStep{Name: domain.StepModel, Status: domain.StepFail, Detail: fmt.Sprintf("nome de modelo inválido: %q", clip(m))}
	}
	if _, ok := domain.MatchPrice(in.PriceModels, m); !ok {
		return domain.ReadinessStep{Name: domain.StepModel, Status: domain.StepWarn, Detail: m + ": sem preço próprio; o custo é estimado com o preço de reserva"}
	}
	return domain.ReadinessStep{Name: domain.StepModel, Status: domain.StepOK, Detail: m}
}

// skipModel é o passo de modelo quando o ambiente nem respondeu: só o formato pode ser conferido.
func skipModel(in Input) domain.ReadinessStep {
	s := modelStep(in)
	if s.Status == domain.StepFail {
		return s
	}
	return domain.ReadinessStep{Name: domain.StepModel, Status: domain.StepSkip, Detail: s.Detail}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
