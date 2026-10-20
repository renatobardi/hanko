package httpagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Limites do adapter_config.
const (
	defaultMaxSteps   = 20
	maxMaxSteps       = 50
	defaultTimeoutSec = 120
	maxTimeoutSec     = 600
	maxToolNames      = 64
)

// Config é o adapter_config do harness http.
//
//	{"base_url": "http://mac.tailnet:8000/v1", "api_key_ref": "${secret:omlx}", "model": "Qwen3-8B-4bit",
//	 "max_steps": 20, "temperature": 0.2, "timeout_sec": 120, "tools": ["hanko_task_get", "hanko_task_comment"]}
type Config struct {
	// BaseURL é a raiz da API OpenAI-compatível (".../v1"). Obrigatória.
	BaseURL string `json:"base_url"`
	// APIKeyRef é a referência ${secret:NOME} à chave (resolvida no despacho); vazio = endpoint sem chave.
	APIKeyRef string `json:"api_key_ref,omitempty"`
	// Model é o modelo usado quando a persona/task não define um.
	Model string `json:"model,omitempty"`
	// MaxSteps é o teto de idas e vindas de tool-use (padrão 20, máximo 50).
	MaxSteps int `json:"max_steps,omitempty"`
	// Temperature (opcional, 0 a 2).
	Temperature *float64 `json:"temperature,omitempty"`
	// TimeoutSec é o prazo de cada chamada ao modelo (padrão 120).
	TimeoutSec int `json:"timeout_sec,omitempty"`
	// Tools restringe as tools do Hanko oferecidas ao modelo (nomes `hanko_*`); vazio = todas.
	Tools []string `json:"tools,omitempty"`
}

// ParseConfig lê e valida o adapter_config. Chaves desconhecidas são recusadas.
func ParseConfig(raw json.RawMessage) (Config, error) {
	var c Config
	if len(bytes.TrimSpace(raw)) == 0 {
		return c, errors.New(`http needs adapter_config with "base_url" (an OpenAI-compatible endpoint)`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			return c, fmt.Errorf("%s; http accepts base_url, api_key_ref, model, max_steps, temperature, timeout_sec, tools", strings.TrimPrefix(err.Error(), "json: "))
		}
		return c, fmt.Errorf("invalid http adapter_config: %w", err)
	}
	return c, c.Validate()
}

// Validate confere e normaliza os campos.
func (c *Config) Validate() error {
	c.BaseURL = strings.TrimSpace(c.BaseURL)
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New(`"base_url" must be an http(s) URL without credentials (e.g. https://openrouter.ai/api/v1)`)
	}
	c.APIKeyRef = strings.TrimSpace(c.APIKeyRef)
	c.Model = strings.TrimSpace(c.Model)
	if len(c.Model) > 200 || len(c.APIKeyRef) > 500 {
		return errors.New(`"model" or "api_key_ref" is too long`)
	}
	if c.MaxSteps < 0 || c.MaxSteps > maxMaxSteps {
		return fmt.Errorf(`"max_steps" must be between 1 and %d`, maxMaxSteps)
	}
	if c.TimeoutSec < 0 || c.TimeoutSec > maxTimeoutSec {
		return fmt.Errorf(`"timeout_sec" must be between 1 and %d`, maxTimeoutSec)
	}
	if c.Temperature != nil && (*c.Temperature < 0 || *c.Temperature > 2) {
		return errors.New(`"temperature" must be between 0 and 2`)
	}
	if len(c.Tools) > maxToolNames {
		return fmt.Errorf(`"tools" accepts at most %d names`, maxToolNames)
	}
	for _, t := range c.Tools {
		if strings.TrimSpace(t) == "" || len(t) > 100 {
			return errors.New(`"tools" has an empty or too long name`)
		}
	}
	return nil
}

// ValidateHire é a checagem da contratação: a chave só pode ser uma referência ${secret:NOME} (nunca o valor em
// claro no banco). Na execução o campo já vem resolvido, por isso esta regra não está em Validate.
func (c Config) ValidateHire(isRef func(string) bool) error {
	if c.APIKeyRef != "" && !isRef(c.APIKeyRef) {
		return errors.New(`"api_key_ref" must be a ${secret:NAME} reference to a company secret, not the key itself`)
	}
	return nil
}

func (c Config) maxSteps() int {
	if c.MaxSteps <= 0 {
		return defaultMaxSteps
	}
	return c.MaxSteps
}

func (c Config) timeout() time.Duration {
	if c.TimeoutSec <= 0 {
		return defaultTimeoutSec * time.Second
	}
	return time.Duration(c.TimeoutSec) * time.Second
}
