package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Preset é um agente ACP conhecido: o executável e os argumentos que o põem em modo ACP. Serve de atalho na
// contratação (`adapter_config: {"preset": "gemini"}`) e tem teste de contrato no CI (ADR-0056 item 10). O
// comando continua passando pelo gate do operador (HANKO_ACP_ALLOW/HANKO_ACP_COMMANDS) quando o harness é `acp`.
type Preset struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Presets são os agentes ACP com preset, na ordem de exibição.
var Presets = []Preset{
	{Name: "devin", Title: "Devin CLI", Command: "devin", Args: []string{"acp"}},
	{Name: "gemini", Title: "Gemini CLI", Command: "gemini", Args: []string{"--experimental-acp"}},
	{Name: "opencode", Title: "opencode", Command: "opencode", Args: []string{"acp"}},
}

// PresetByName procura um preset pelo nome (sem diferenciar maiúsculas).
func PresetByName(name string) (Preset, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

// PresetNames lista os nomes, para mensagens de erro.
func PresetNames() []string {
	out := make([]string, len(Presets))
	for i, p := range Presets {
		out[i] = p.Name
	}
	return out
}

// applyPreset preenche command/args de c a partir do preset. command junto de preset é conflito; args da
// persona vêm depois dos do preset.
func (c *Config) applyPreset() error {
	c.Preset = strings.ToLower(strings.TrimSpace(c.Preset))
	if c.Preset == "" {
		return nil
	}
	p, ok := PresetByName(c.Preset)
	if !ok {
		return fmt.Errorf(`unknown "preset" %q (want one of %s)`, c.Preset, strings.Join(PresetNames(), ", "))
	}
	if strings.TrimSpace(c.Command) != "" {
		return errors.New(`"preset" and "command" are mutually exclusive: the preset already sets the executable`)
	}
	c.Command = p.Command
	c.Args = append(append([]string{}, p.Args...), c.Args...)
	return nil
}

// Fixed é um agente ACP de 1ª classe: o operador fixa o executável e os argumentos (Devin, ADR-0056). A persona só
// ajusta permission, model, env e init_timeout_sec.
type Fixed struct {
	Name    string
	Command string
	Args    []string
}

// ParseFixedConfig lê o adapter_config de um agente de comando fixo. Vazio é válido (tudo padrão); command, args e
// preset são recusados porque o executável é decisão do operador.
func ParseFixedConfig(raw json.RawMessage, f Fixed) (Config, error) {
	var c Config
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			if strings.HasPrefix(err.Error(), "json: unknown field") {
				return c, fmt.Errorf("%s; %s accepts permission, model, env, init_timeout_sec", strings.TrimPrefix(err.Error(), "json: "), f.Name)
			}
			return c, fmt.Errorf("invalid %s adapter_config: %w", f.Name, err)
		}
	}
	if c.Command != "" || len(c.Args) > 0 || c.Preset != "" {
		return c, fmt.Errorf("%s runs a command fixed by the operator (HANKO_%s_BIN); remove command, args and preset from adapter_config", f.Name, strings.ToUpper(f.Name))
	}
	c.Command, c.Args = f.Command, append([]string{}, f.Args...)
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// DevinFixed é o comando padrão do Devin CLI; o executável pode ser trocado por HANKO_DEVIN_BIN.
func DevinFixed(bin string) Fixed {
	if strings.TrimSpace(bin) == "" {
		bin = "devin"
	}
	return Fixed{Name: "devin", Command: bin, Args: []string{"acp"}}
}

// ParseAgentConfig valida o adapter_config de um harness baseado em ACP ("acp" ou "devin").
func ParseAgentConfig(agent string, raw json.RawMessage) (Config, error) {
	if agent == "devin" {
		return ParseFixedConfig(raw, DevinFixed(""))
	}
	return ParseConfig(raw)
}
