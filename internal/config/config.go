// Package config carrega a configuração do Hanko: defaults, depois hanko.yaml
// (caminho em HANKO_CONFIG), depois variáveis de ambiente HANKO_* (que vencem).
package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"gopkg.in/yaml.v3"
)

// Valores válidos para os campos enumerados.
const (
	ModeTrusted       = "trusted"
	ModeAuthenticated = "authenticated"

	AuthLocal    = "local"
	AuthFirebase = "firebase"

	MemoryAIMemory = "ai-memory"
	MemoryNone     = "none"

	LogJSON = "json"
	LogText = "text"

	DefaultConfigFile = "hanko.yaml"

	// MinSecretLen é o tamanho mínimo (bytes) de HANKO_SECRET quando definido.
	MinSecretLen = 32
)

// Memory configura o provedor de memória (ADR-0015).
type Memory struct {
	Provider string `yaml:"provider" env:"HANKO_MEMORY_PROVIDER"`
	URL      string `yaml:"url" env:"HANKO_MEMORY_URL"`
	Token    string `yaml:"token" env:"HANKO_MEMORY_TOKEN"`
}

// Firebase configura o provider de auth firebase (ADR-0014). Os três valores são
// públicos (vão para o navegador); não há credencial de serviço.
type Firebase struct {
	ProjectID  string `yaml:"project_id" env:"HANKO_FIREBASE_PROJECT_ID"`
	APIKey     string `yaml:"api_key" env:"HANKO_FIREBASE_API_KEY"`
	AuthDomain string `yaml:"auth_domain" env:"HANKO_FIREBASE_AUTH_DOMAIN"`
}

// Auth configura a autenticação de humanos (vale no modo authenticated).
type Auth struct {
	Provider       string   `yaml:"provider" env:"HANKO_AUTH_PROVIDER"` // local (default) | firebase
	Firebase       Firebase `yaml:"firebase"`
	AllowedEmails  []string `yaml:"allowed_emails" env:"HANKO_AUTH_ALLOWED_EMAILS" envSeparator:","`
	AllowedDomains []string `yaml:"allowed_domains" env:"HANKO_AUTH_ALLOWED_DOMAINS" envSeparator:","`
}

// Config é a configuração completa do processo.
type Config struct {
	DatabaseURL       string        `yaml:"database_url" env:"HANKO_DATABASE_URL"`
	Addr              string        `yaml:"addr" env:"HANKO_ADDR"`
	PublicURL         string        `yaml:"public_url" env:"HANKO_PUBLIC_URL"`
	Mode              string        `yaml:"mode" env:"HANKO_MODE"`
	Secret            string        `yaml:"secret" env:"HANKO_SECRET"`
	MigrateOnBoot     bool          `yaml:"migrate_on_boot" env:"HANKO_MIGRATE_ON_BOOT"`
	Memory            Memory        `yaml:"memory"`
	Auth              Auth          `yaml:"auth"`
	ClaudeBin         string        `yaml:"claude_bin" env:"HANKO_CLAUDE_BIN"`
	CodexBin          string        `yaml:"codex_bin" env:"HANKO_CODEX_BIN"`
	Workers           int           `yaml:"workers" env:"HANKO_WORKERS"`
	RunTimeout        time.Duration `yaml:"run_timeout" env:"HANKO_RUN_TIMEOUT"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval" env:"HANKO_HEARTBEAT_INTERVAL"`
	LogLevel          string        `yaml:"log_level" env:"HANKO_LOG_LEVEL"`
	LogFormat         string        `yaml:"log_format" env:"HANKO_LOG_FORMAT"`

	// AgentEnvAllow lista variáveis extras do ambiente do servidor repassadas aos
	// harnesses (nomes exatos ou prefixos terminados em "*"); HANKO_* nunca passa.
	AgentEnvAllow []string `yaml:"agent_env_allow" env:"HANKO_AGENT_ENV_ALLOW" envSeparator:","`
	// AllowedHosts são hosts extras aceitos no cabeçalho Host (proteção contra DNS
	// rebinding), além de localhost/loopback e do host de PublicURL.
	AllowedHosts []string `yaml:"allowed_hosts" env:"HANKO_ALLOWED_HOSTS" envSeparator:","`
	// WorkspacesRoot é a raiz sob a qual todo workspace precisa estar (default $HOME/hanko-workspaces).
	WorkspacesRoot string `yaml:"workspaces_root" env:"HANKO_WORKSPACES_ROOT"`

	// SecretGenerated indica que Secret foi gerado neste boot (run tokens não
	// sobrevivem a restart). Preenchido por Load.
	SecretGenerated bool `yaml:"-" env:"-"`
	// Warnings são avisos de configuração para o chamador logar após montar o logger.
	Warnings []string `yaml:"-" env:"-"`
}

// Defaults devolve a configuração padrão.
func Defaults() Config {
	return Config{
		Addr:              "127.0.0.1:7420",
		Mode:              ModeTrusted,
		MigrateOnBoot:     true,
		Memory:            Memory{Provider: MemoryAIMemory, URL: "http://127.0.0.1:49374"},
		Auth:              Auth{Provider: AuthLocal},
		ClaudeBin:         "claude",
		CodexBin:          "codex",
		Workers:           4,
		RunTimeout:        30 * time.Minute,
		HeartbeatInterval: time.Minute,
		LogLevel:          "info",
		LogFormat:         LogJSON,
	}
}

// Load lê o ambiente do processo. Equivale a LoadFrom(os.Getenv).
func Load() (Config, error) {
	return LoadFrom(os.Getenv, os.ReadFile)
}

// LoadFrom é Load com ambiente e leitura de arquivo injetáveis (testes).
func LoadFrom(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	cfg := Defaults()

	path := getenv("HANKO_CONFIG")
	explicit := path != ""
	if !explicit {
		path = DefaultConfigFile
	}
	data, err := readFile(path)
	switch {
	case err == nil:
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("config: arquivo %s inválido: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
		// sem arquivo padrão: só env.
	default:
		return Config{}, fmt.Errorf("config: não foi possível ler %s: %w", path, err)
	}

	if err := env.ParseWithOptions(&cfg, env.Options{
		Environment: environFor(getenv),
	}); err != nil {
		return Config{}, fmt.Errorf("config: variável de ambiente inválida: %w", err)
	}

	if err := cfg.finalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// environFor monta o mapa de ambiente para as chaves HANKO_* conhecidas.
func environFor(getenv func(string) string) map[string]string {
	keys := []string{
		"HANKO_DATABASE_URL", "HANKO_ADDR", "HANKO_PUBLIC_URL", "HANKO_MODE", "HANKO_SECRET",
		"HANKO_MIGRATE_ON_BOOT", "HANKO_MEMORY_PROVIDER", "HANKO_MEMORY_URL", "HANKO_MEMORY_TOKEN",
		"HANKO_CLAUDE_BIN", "HANKO_CODEX_BIN", "HANKO_WORKERS", "HANKO_RUN_TIMEOUT",
		"HANKO_HEARTBEAT_INTERVAL", "HANKO_LOG_LEVEL", "HANKO_LOG_FORMAT",
		"HANKO_AGENT_ENV_ALLOW", "HANKO_ALLOWED_HOSTS", "HANKO_WORKSPACES_ROOT",
		"HANKO_AUTH_PROVIDER", "HANKO_AUTH_ALLOWED_EMAILS", "HANKO_AUTH_ALLOWED_DOMAINS",
		"HANKO_FIREBASE_PROJECT_ID", "HANKO_FIREBASE_API_KEY", "HANKO_FIREBASE_AUTH_DOMAIN",
	}
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		if v := getenv(k); v != "" {
			m[k] = v
		}
	}
	return m
}

func (c *Config) finalize() error {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	c.Memory.Provider = strings.ToLower(strings.TrimSpace(c.Memory.Provider))
	c.Auth.Provider = strings.ToLower(strings.TrimSpace(c.Auth.Provider))
	if c.Auth.Provider == "" {
		c.Auth.Provider = AuthLocal
	}
	c.Auth.Firebase.ProjectID = strings.TrimSpace(c.Auth.Firebase.ProjectID)
	c.Auth.Firebase.APIKey = strings.TrimSpace(c.Auth.Firebase.APIKey)
	c.Auth.Firebase.AuthDomain = strings.TrimSpace(c.Auth.Firebase.AuthDomain)
	c.Auth.AllowedEmails = lowerList(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = lowerList(c.Auth.AllowedDomains)
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	c.LogFormat = strings.ToLower(strings.TrimSpace(c.LogFormat))
	c.Addr = strings.TrimSpace(c.Addr)

	if c.PublicURL == "" {
		c.PublicURL = "http://" + c.Addr
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	c.AgentEnvAllow = cleanList(c.AgentEnvAllow)
	c.AllowedHosts = cleanList(c.AllowedHosts)
	c.WorkspacesRoot = strings.TrimSpace(c.WorkspacesRoot)
	if c.WorkspacesRoot == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c.WorkspacesRoot = filepath.Join(home, "hanko-workspaces")
		}
	}

	if err := c.Validate(); err != nil {
		return err
	}

	if c.Secret == "" {
		s, err := randomSecret()
		if err != nil {
			return fmt.Errorf("config: gerar HANKO_SECRET: %w", err)
		}
		c.Secret = s
		c.SecretGenerated = true
		c.Warnings = append(c.Warnings,
			"HANKO_SECRET não definido: gerado aleatoriamente neste boot; run tokens de runs em andamento não sobrevivem a restart")
	}
	if c.Mode == ModeAuthenticated && c.Auth.Provider == AuthFirebase && len(c.Auth.AllowedEmails)+len(c.Auth.AllowedDomains) == 0 {
		c.Warnings = append(c.Warnings,
			"auth firebase sem HANKO_AUTH_ALLOWED_EMAILS/HANKO_AUTH_ALLOWED_DOMAINS: só o primeiro usuário a entrar (dono) é admitido; os demais são recusados")
	}
	if c.Mode == ModeTrusted && !c.IsLoopback() {
		c.Warnings = append(c.Warnings,
			"modo trusted com bind fora de loopback: qualquer cliente que alcance "+c.Addr+" age como humano; /mcp sem token fica desabilitado")
	}
	return nil
}

// Validate checa os campos; mensagens em pt-BR. Agrega todos os erros.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if strings.TrimSpace(c.DatabaseURL) == "" {
		add("HANKO_DATABASE_URL é obrigatório (ex.: postgres://usuario:senha@host:5432/hanko)")
	} else if u, err := url.Parse(c.DatabaseURL); err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		add("HANKO_DATABASE_URL deve ser uma URL postgres:// ou postgresql://")
	}
	if _, _, err := net.SplitHostPort(c.Addr); err != nil {
		add("HANKO_ADDR %q inválido: use host:porta (ex.: 127.0.0.1:7420)", c.Addr)
	}
	if u, err := url.Parse(c.PublicURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		add("HANKO_PUBLIC_URL %q inválido: use http(s)://host[:porta]", c.PublicURL)
	}
	if c.Mode != ModeTrusted && c.Mode != ModeAuthenticated {
		add("HANKO_MODE %q inválido: use %q ou %q", c.Mode, ModeTrusted, ModeAuthenticated)
	}
	if c.Mode == ModeAuthenticated {
		c.validateAuthenticated(add)
	}
	switch c.Memory.Provider {
	case MemoryNone:
	case MemoryAIMemory:
		if u, err := url.Parse(c.Memory.URL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			add("HANKO_MEMORY_URL %q inválido: use http(s)://host[:porta]", c.Memory.URL)
		}
	default:
		add("HANKO_MEMORY_PROVIDER %q inválido: use %q ou %q", c.Memory.Provider, MemoryAIMemory, MemoryNone)
	}
	if c.Secret != "" && len(c.Secret) < MinSecretLen {
		add("HANKO_SECRET tem %d bytes: use no mínimo %d (ex.: openssl rand -hex 32)", len(c.Secret), MinSecretLen)
	}
	if c.WorkspacesRoot != "" && !filepath.IsAbs(c.WorkspacesRoot) {
		add("HANKO_WORKSPACES_ROOT %q deve ser um caminho absoluto", c.WorkspacesRoot)
	}
	if c.Workers < 1 || c.Workers > 256 {
		add("HANKO_WORKERS %d inválido: use um valor entre 1 e 256", c.Workers)
	}
	if c.RunTimeout <= 0 {
		add("HANKO_RUN_TIMEOUT deve ser positivo (ex.: 30m)")
	}
	if c.HeartbeatInterval <= 0 {
		add("HANKO_HEARTBEAT_INTERVAL deve ser positivo (ex.: 1m)")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add("HANKO_LOG_LEVEL %q inválido: use debug, info, warn ou error", c.LogLevel)
	}
	switch c.LogFormat {
	case LogJSON, LogText:
	default:
		add("HANKO_LOG_FORMAT %q inválido: use %q ou %q", c.LogFormat, LogJSON, LogText)
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("configuração inválida: %w", errors.Join(errs...))
}

// validateAuthenticated aplica as exigências do modo authenticated (ADR-0007, ADR-0017).
func (c Config) validateAuthenticated(add func(string, ...any)) {
	if len(c.Secret) < MinSecretLen {
		add("modo authenticated exige HANKO_SECRET com no mínimo %d bytes (ex.: openssl rand -hex 32)", MinSecretLen)
	}
	if u, err := url.Parse(c.PublicURL); err == nil && u.Host != "" && u.Scheme != "https" && !loopbackHost(u.Hostname()) {
		add("modo authenticated exige HANKO_PUBLIC_URL https (recebido %q); http só vale em localhost/loopback", c.PublicURL)
	}
	switch c.Auth.Provider {
	case AuthLocal:
	case AuthFirebase:
		if c.Auth.Firebase.ProjectID == "" || c.Auth.Firebase.APIKey == "" || c.Auth.Firebase.AuthDomain == "" {
			add("HANKO_AUTH_PROVIDER=firebase exige HANKO_FIREBASE_PROJECT_ID, HANKO_FIREBASE_API_KEY e HANKO_FIREBASE_AUTH_DOMAIN")
		}
	default:
		add("HANKO_AUTH_PROVIDER %q inválido: use %q ou %q", c.Auth.Provider, AuthLocal, AuthFirebase)
	}
}

// Authenticated informa se o servidor exige login de humanos.
func (c Config) Authenticated() bool { return c.Mode == ModeAuthenticated }

// SecureCookies informa se os cookies devem levar o atributo Secure (PublicURL https).
func (c Config) SecureCookies() bool { return strings.HasPrefix(c.PublicURL, "https://") }

func loopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// IsLoopback informa se Addr escuta apenas em loopback.
func (c Config) IsLoopback() bool {
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// TrustedMCP informa se /mcp aceita chamadas sem token (modo trusted em loopback).
func (c Config) TrustedMCP() bool {
	return c.Mode == ModeTrusted && c.IsLoopback()
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// cleanList apara espaços e descarta entradas vazias.
func cleanList(in []string) []string {
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// lowerList apara, descarta vazios e passa para minúsculas.
func lowerList(in []string) []string {
	var out []string
	for _, v := range cleanList(in) {
		out = append(out, strings.ToLower(v))
	}
	return out
}

// PublicHost devolve o host (com porta, se houver) de PublicURL.
func (c Config) PublicHost() string {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return ""
	}
	return u.Host
}
