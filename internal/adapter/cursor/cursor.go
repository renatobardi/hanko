// Package cursor roda uma persona com o Cursor CLI (`cursor-agent -p --output-format stream-json`), harness nativo do
// ADR-0056. As tools do Hanko vêm pela CLI `hanko` (run token em HANKO_TOKEN), como no pi; o prompt vai pelo stdin.
package cursor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/cliharness"
)

// Options configura o adapter.
type Options = cliharness.Options

// Config é a parte do adapter_config específica do cursor.
type Config struct {
	// Force deixa o agente aplicar mudanças e rodar comandos sem pedir aprovação (--force). Sem isso, o modo
	// não interativo só propõe alterações. Ignorado em run de revisão.
	Force bool `json:"force,omitempty"`
	// ExtraArgs são argumentos extras (ignorados em run de revisão).
	ExtraArgs []string `json:"extra_args,omitempty"`
}

// ParseConfig valida o adapter_config. Vazio vale; chaves desconhecidas são recusadas.
func ParseConfig(raw json.RawMessage) (Config, error) {
	var c Config
	if len(bytes.TrimSpace(raw)) == 0 {
		return c, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			return c, fmt.Errorf("%s; cursor accepts force, extra_args", strings.TrimPrefix(err.Error(), "json: "))
		}
		return c, fmt.Errorf("invalid cursor adapter_config: %w", err)
	}
	if len(c.ExtraArgs) > 64 {
		return c, errors.New(`"extra_args" accepts at most 64 entries`)
	}
	for _, s := range c.ExtraArgs {
		if len(s) > 4096 || strings.ContainsRune(s, 0) {
			return c, errors.New(`"extra_args" has an entry that is too long or contains NUL`)
		}
	}
	return c, nil
}

// BuildArgs monta o argv do cursor-agent. O prompt não está nele (vai pelo stdin).
func BuildArgs(spec adapter.Spec, cfg Config) []string {
	args := []string{"-p", "--output-format", "stream-json"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.SessionRef != "" {
		args = append(args, "--resume", spec.SessionRef)
	}
	if !spec.ReadOnly {
		if cfg.Force {
			args = append(args, "--force")
		}
		args = append(args, cfg.ExtraArgs...)
	}
	return args
}

// Prompt junta o system prompt (com o protocolo da CLI do Hanko) e o prompt da task.
func Prompt(spec adapter.Spec) string {
	sys := adapter.WithCLITools(spec)
	if strings.TrimSpace(sys) == "" {
		return spec.Prompt
	}
	return sys + "\n\n---\n\n" + spec.Prompt
}

// New devolve o adapter do cursor; bin vazio usa "cursor-agent".
func New(bin string, o Options) *cliharness.Adapter {
	if bin == "" {
		bin = "cursor-agent"
	}
	return cliharness.New(cliharness.Harness{
		Name: "cursor", Bin: bin, NewParser: NewParser, Resumes: true,
		Args: func(spec adapter.Spec, _ string) ([]string, error) {
			cfg, err := ParseConfig(spec.Config)
			if err != nil {
				return nil, fmt.Errorf("adapter_config: %w", err)
			}
			return BuildArgs(spec, cfg), nil
		},
		Stdin:     Prompt,
		ProbeArgs: []string{"--version"},
	}, o)
}
