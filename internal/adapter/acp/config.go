package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Permission is the policy applied to session/request_permission (and to fs writes).
type Permission string

const (
	// PermAllow approves every request (picks an allow_* option).
	PermAllow Permission = "allow"
	// PermReject rejects every request (picks a reject_* option).
	PermReject Permission = "reject"
	// PermReadOnly approves only read, search, think and fetch tool calls and rejects the rest
	// (edit, delete, move, execute, switch_mode, other and unknown kinds). It is the default.
	PermReadOnly Permission = "read_only"
)

// Limits of adapter_config.
const (
	maxArgs         = 64
	maxArgBytes     = 4096
	maxEnvEntries   = 64
	maxEnvValueSize = 8192
	defaultInitSec  = 30
	maxInitSec      = 300
)

// Config is the ACP-specific part of persona.adapter_config.
//
// Example (illustrative only; any command that speaks ACP v1 over stdio works):
//
//	{"command": "my-acp-agent", "args": ["--stdio"], "env": {"MY_AGENT_HOME": "/data/agent"},
//	 "permission": "read_only", "model": "best-effort-model-id", "init_timeout_sec": 30}
type Config struct {
	// Preset names a known ACP agent (presets.go) and fills Command/Args; exclusive with Command.
	Preset string `json:"preset,omitempty"`
	// Command is the executable (resolved through PATH), without arguments. Required (or Preset).
	Command string `json:"command,omitempty"`
	// Args are the arguments, one per element (no shell).
	Args []string `json:"args,omitempty"`
	// Env is extra environment for the agent, on top of the server allowlist (procrun.HarnessEnv).
	// Names starting with HANKO_ are reserved.
	Env map[string]string `json:"env,omitempty"`
	// Permission is allow | reject | read_only. Empty = read_only.
	Permission string `json:"permission,omitempty"`
	// Model is a best-effort model id used when the persona has none; see the package docs.
	Model string `json:"model,omitempty"`
	// InitTimeoutSec bounds the wait for the initialize answer. 0 = 30.
	InitTimeoutSec int `json:"init_timeout_sec,omitempty"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseConfig decodes and validates an adapter_config. Unknown keys are rejected (a typo such as
// "permision" would otherwise silently fall back to the default policy). The returned error text
// is meant to be shown to the person who hires the persona.
func ParseConfig(raw json.RawMessage) (Config, error) {
	var c Config
	if len(bytes.TrimSpace(raw)) == 0 {
		return c, errors.New(`acp needs adapter_config with "command" (the executable of an agent that speaks ACP over stdio) or a "preset"`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			return c, fmt.Errorf("%s; acp accepts command, args, env, permission, model, init_timeout_sec (or preset instead of command)", strings.TrimPrefix(err.Error(), "json: "))
		}
		return c, fmt.Errorf("invalid acp adapter_config: %w", err)
	}
	if err := c.applyPreset(); err != nil {
		return c, err
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// Validate checks (and normalizes) the configuration.
func (c *Config) Validate() error {
	c.Command = strings.TrimSpace(c.Command)
	if c.Command == "" {
		return errors.New(`"command" is required: the executable of an agent that speaks ACP over stdio`)
	}
	if strings.ContainsRune(c.Command, 0) {
		return errors.New(`"command" must not contain NUL`)
	}
	if strings.ContainsAny(c.Command, " \t\r\n") && !filepath.IsAbs(c.Command) {
		return errors.New(`"command" must be only the executable; put the arguments in "args"`)
	}
	if len(c.Args) > maxArgs {
		return fmt.Errorf(`"args" accepts at most %d entries`, maxArgs)
	}
	for _, a := range c.Args {
		if len(a) > maxArgBytes || strings.ContainsRune(a, 0) {
			return errors.New(`"args" has an entry that is too long or contains NUL`)
		}
	}
	if len(c.Env) > maxEnvEntries {
		return fmt.Errorf(`"env" accepts at most %d entries`, maxEnvEntries)
	}
	for k, v := range c.Env {
		switch {
		case !envName.MatchString(k):
			return fmt.Errorf(`"env" has an invalid variable name %q`, k)
		case strings.HasPrefix(k, "HANKO_"):
			return fmt.Errorf(`"env" cannot set %s: HANKO_* names are reserved for the server`, k)
		case len(v) > maxEnvValueSize || strings.ContainsRune(v, 0):
			return fmt.Errorf(`"env" value of %s is too long or contains NUL`, k)
		}
	}
	switch Permission(c.Permission) {
	case "", PermAllow, PermReject, PermReadOnly:
	default:
		return fmt.Errorf(`"permission" must be allow, reject or read_only (got %q)`, c.Permission)
	}
	c.Model = strings.TrimSpace(c.Model)
	if c.InitTimeoutSec < 0 || c.InitTimeoutSec > maxInitSec {
		return fmt.Errorf(`"init_timeout_sec" must be between 0 and %d`, maxInitSec)
	}
	return nil
}

// Policy is the effective permission policy; readOnly (review runs) forces PermReadOnly.
func (c Config) Policy(readOnly bool) Permission {
	if readOnly {
		return PermReadOnly
	}
	if p := Permission(c.Permission); p != "" {
		return p
	}
	return PermReadOnly
}

func (c Config) initTimeout() time.Duration {
	if c.InitTimeoutSec <= 0 {
		return defaultInitSec * time.Second
	}
	return time.Duration(c.InitTimeoutSec) * time.Second
}
