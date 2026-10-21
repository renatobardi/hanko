// Package pi roda uma persona com o Pi (`pi --mode json -p`), harness nativo do ADR-0056.
//
// O Pi não fala MCP: as tools do Hanko são a CLI `hanko` pelo shell, autenticada pelo run token (HANKO_TOKEN); o
// protocolo no system prompt lista os comandos (adapter.CLIToolsProtocol). O prompt vai pelo stdin (o argv tem teto
// de tamanho) e o stdout é um evento JSON por linha, lido por parser.go.
package pi

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

// readOnlyTools é a lista de tools do Pi numa run de revisão (somente leitura).
const readOnlyTools = "read,grep,find,ls"

// Config é a parte do adapter_config específica do pi.
type Config struct {
	// Provider escolhe o provedor de modelo do Pi (--provider); vazio = o padrão do Pi.
	Provider string `json:"provider,omitempty"`
	// Tools restringe as tools nativas do Pi (--tools); vazio = todas.
	Tools []string `json:"tools,omitempty"`
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
			return c, fmt.Errorf("%s; pi accepts provider, tools, extra_args", strings.TrimPrefix(err.Error(), "json: "))
		}
		return c, fmt.Errorf("invalid pi adapter_config: %w", err)
	}
	if len(c.ExtraArgs) > 64 || len(c.Tools) > 32 {
		return c, errors.New(`"extra_args" accepts at most 64 entries and "tools" at most 32`)
	}
	for _, s := range append(append([]string{c.Provider}, c.Tools...), c.ExtraArgs...) {
		if len(s) > 4096 || strings.ContainsRune(s, 0) {
			return c, errors.New("adapter_config has an entry that is too long or contains NUL")
		}
	}
	return c, nil
}

// BuildArgs monta o argv do pi. O prompt não está nele (vai pelo stdin).
func BuildArgs(spec adapter.Spec, cfg Config) []string {
	args := []string{"--mode", "json", "-p"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if cfg.Provider != "" {
		args = append(args, "--provider", cfg.Provider)
	}
	switch {
	case spec.ReadOnly:
		args = append(args, "--tools", readOnlyTools)
	case len(cfg.Tools) > 0:
		args = append(args, "--tools", strings.Join(cfg.Tools, ","))
	}
	if !spec.ReadOnly {
		args = append(args, cfg.ExtraArgs...)
	}
	return args
}

// Prompt junta o system prompt (com o protocolo da CLI do Hanko) e o prompt da task: o pi lê tudo do stdin.
func Prompt(spec adapter.Spec) string {
	sys := adapter.WithCLITools(spec)
	if strings.TrimSpace(sys) == "" {
		return spec.Prompt
	}
	return sys + "\n\n---\n\n" + spec.Prompt
}

// New devolve o adapter do pi; bin vazio usa "pi".
func New(bin string, o Options) *cliharness.Adapter {
	if bin == "" {
		bin = "pi"
	}
	return cliharness.New(cliharness.Harness{
		Name: "pi", Bin: bin, NewParser: NewParser,
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
