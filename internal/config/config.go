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

	BlobPostgres = "postgres"
	BlobS3       = "s3"
	BlobFS       = "fs"

	// MaxArtifactBytesPostgres e MaxArtifactBytesS3 são os tetos do limite por arquivo de artefato por driver; sem
	// HANKO_ARTIFACT_MAX_BYTES, o limite é o teto do driver ativo (ADR-0065).
	MaxArtifactBytesPostgres int64 = 16 << 20
	MaxArtifactBytesS3       int64 = 64 << 20
	// DefaultArtifactQuotaBytes é a cota de artefatos por empresa quando a configuração não diz (ADR-0065).
	DefaultArtifactQuotaBytes int64 = 5 << 30

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
	// ReadTool é a tool do provedor que lê uma página por caminho (diff das propostas de curadoria, ADR-0030).
	ReadTool string `yaml:"read_tool" env:"HANKO_MEMORY_READ_TOOL"`
}

// Firebase configura o provider de auth firebase (ADR-0014). Os três valores são
// públicos (vão para o navegador); não há credencial de serviço.
type Firebase struct {
	ProjectID  string `yaml:"project_id" env:"HANKO_FIREBASE_PROJECT_ID"`
	APIKey     string `yaml:"api_key" env:"HANKO_FIREBASE_API_KEY"`
	AuthDomain string `yaml:"auth_domain" env:"HANKO_FIREBASE_AUTH_DOMAIN"`
}

// Integrations configura as integrações da empresa (ADR-0029). Os dois interruptores são decisões do
// operador da instalação, não de quem administra uma empresa, e vêm desligados.
type Integrations struct {
	// AllowStdio libera integrações MCP do tipo stdio (o servidor executa o comando na máquina dele).
	AllowStdio bool `yaml:"allow_stdio" env:"HANKO_INTEGRATIONS_ALLOW_STDIO"`
	// AllowPrivateDestinations libera webhooks e testes de conexão para loopback, link-local e redes privadas
	// (RFC 1918, ULA). Desligado, são recusados (proteção SSRF).
	AllowPrivateDestinations bool `yaml:"allow_private_destinations" env:"HANKO_INTEGRATIONS_ALLOW_PRIVATE"`
}

// Storage configura onde o conteúdo dos artefatos fica (ADR-0051). O driver postgres é o padrão; com s3 as escritas
// novas vão para o bucket. Credenciais só por ambiente.
type Storage struct {
	Driver string `yaml:"driver" env:"HANKO_BLOB_DRIVER"` // postgres (padrão) | s3 | fs
	// Dir é a raiz do driver fs (caminho absoluto; ADR-0065): os arquivos ficam em <Dir>/<empresa>/<artefato>.
	Dir string `yaml:"dir" env:"HANKO_BLOB_DIR"`
	S3  S3     `yaml:"s3"`
}

// S3 configura o driver s3 (qualquer endpoint compatível). AccessKey e SecretKey não vão no yaml: só por ambiente.
type S3 struct {
	Endpoint  string `yaml:"endpoint" env:"HANKO_S3_ENDPOINT"`
	Region    string `yaml:"region" env:"HANKO_S3_REGION"`
	Bucket    string `yaml:"bucket" env:"HANKO_S3_BUCKET"`
	Prefix    string `yaml:"prefix" env:"HANKO_S3_PREFIX"`
	PathStyle bool   `yaml:"path_style" env:"HANKO_S3_PATH_STYLE"`
	AccessKey string `yaml:"-" env:"HANKO_S3_ACCESS_KEY"`
	SecretKey string `yaml:"-" env:"HANKO_S3_SECRET_KEY"`
}

// Vault configura o cofre externo de segredos (ADR-0050), API KV v2. O token só por ambiente.
type Vault struct {
	Addr      string `yaml:"addr" env:"HANKO_VAULT_ADDR"`
	Mount     string `yaml:"mount" env:"HANKO_VAULT_MOUNT"`
	Namespace string `yaml:"namespace" env:"HANKO_VAULT_NAMESPACE"`
	Token     string `yaml:"-" env:"HANKO_VAULT_TOKEN"`
}

// Environments configura os ambientes de execução remotos (ADR-0037). Os interruptores são do operador da
// instalação e vêm desligados.
type Environments struct {
	// SSHAllowHosts liga o driver SSH só para estes hosts (exatos, sufixos ".exemplo.com" ou "*.exemplo.com",
	// IPs, redes CIDR, ou "*"). Vazio = SSH desligado.
	SSHAllowHosts []string `yaml:"ssh_allow_hosts" env:"HANKO_ENV_SSH_ALLOW_HOSTS" envSeparator:","`
	// Sandbox liga o driver sandbox com o motor dado (só "docker"). Vazio = desligado.
	Sandbox string `yaml:"sandbox" env:"HANKO_ENV_SANDBOX"`
	// SandboxHostRoot é, quando o Hanko roda dentro de um contêiner, o caminho no host do docker do
	// diretório que aqui é HANKO_WORKSPACES_ROOT (os volumes do sandbox usam caminhos do host).
	SandboxHostRoot string `yaml:"sandbox_host_root" env:"HANKO_ENV_SANDBOX_HOST_ROOT"`
	// Cloud liga o driver de sandbox em nuvem com o provider dado (só "daytona", ADR-0055). Vazio = desligado.
	Cloud string `yaml:"cloud" env:"HANKO_ENV_CLOUD"`
	// CloudAPIURL e CloudSSHHost apontam o provider (padrões do Daytona hospedado); CloudSSHKnownHosts são as linhas de
	// known_hosts do gateway SSH, obrigatórias quando Cloud está ligado.
	CloudAPIURL        string `yaml:"cloud_api_url" env:"HANKO_ENV_CLOUD_API_URL"`
	CloudSSHHost       string `yaml:"cloud_ssh_host" env:"HANKO_ENV_CLOUD_SSH_HOST"`
	CloudSSHKnownHosts string `yaml:"cloud_ssh_known_hosts" env:"HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS"`
}

// ACP configura o harness acp (ADR-0023). Quem escreve o adapter_config é o admin de uma empresa, e o comando
// roda na máquina do servidor; por isso é decisão do operador, e vem desligado.
type ACP struct {
	// Allow liga o harness acp (HANKO_ACP_ALLOW). Desligado, persona acp não pode ser contratada/importada e as
	// existentes falham ao rodar com um motivo claro.
	Allow bool `yaml:"allow" env:"HANKO_ACP_ALLOW"`
	// Commands restringe Allow a estes executáveis (nome simples, resolvido pelo PATH, ou caminho absoluto).
	// Vazio = qualquer comando.
	Commands []string `yaml:"commands" env:"HANKO_ACP_COMMANDS" envSeparator:","`
}

// Plugins configura os plugins de terceiros (ADR-0038). Desligados até o operador apontar Dir para um diretório
// local com os plugins; o Hanko nunca baixa plugin.
type Plugins struct {
	// Dir é HANKO_PLUGINS_DIR: um subdiretório por plugin, com hanko-plugin.json na raiz.
	Dir string `yaml:"dir" env:"HANKO_PLUGINS_DIR"`
	// DataDir é HANKO_PLUGINS_DATA_DIR: onde ficam os diretórios de trabalho (<DataDir>/plugins/<id>); padrão
	// $HOME/hanko-data.
	DataDir string `yaml:"data_dir" env:"HANKO_PLUGINS_DATA_DIR"`
}

// Auth configura a autenticação de humanos (vale no modo authenticated).
type Auth struct {
	Provider       string   `yaml:"provider" env:"HANKO_AUTH_PROVIDER"` // local (default) | firebase
	Firebase       Firebase `yaml:"firebase"`
	AllowedEmails  []string `yaml:"allowed_emails" env:"HANKO_AUTH_ALLOWED_EMAILS" envSeparator:","`
	AllowedDomains []string `yaml:"allowed_domains" env:"HANKO_AUTH_ALLOWED_DOMAINS" envSeparator:","`
	// OwnerEmail (provider firebase): o único e-mail que vira dono. Com HANKO_PUBLIC_URL fora de
	// loopback e sem listas de admissão, é obrigatório (senão o primeiro visitante viraria dono).
	OwnerEmail string `yaml:"owner_email" env:"HANKO_AUTH_OWNER_EMAIL"`
	// WebBootstrap (provider local) libera o formulário web "Criar conta de dono" mesmo com
	// HANKO_PUBLIC_URL fora de loopback; sem ele a tela manda usar `hanko auth create-owner`.
	WebBootstrap bool `yaml:"web_bootstrap" env:"HANKO_AUTH_WEB_BOOTSTRAP"`
}

// RateLimit são os padrões do rate limit geral (ADR-0058 §B); viram chaves de instance_settings (grupo ratelimit).
type RateLimit struct {
	Enabled          bool  `yaml:"enabled" env:"HANKO_RATELIMIT_ENABLED"`
	WritesPerMin     int64 `yaml:"writes_per_min" env:"HANKO_RATELIMIT_WRITES_PER_MIN"`
	IPPerMin         int64 `yaml:"ip_per_min" env:"HANKO_RATELIMIT_IP_PER_MIN"`
	MCPPerMin        int64 `yaml:"mcp_per_min" env:"HANKO_RATELIMIT_MCP_PER_MIN"`
	WebhooksPerMin   int64 `yaml:"webhooks_per_min" env:"HANKO_RATELIMIT_WEBHOOKS_PER_MIN"`
	BrowserErrPerMin int64 `yaml:"browser_errors_per_min" env:"HANKO_RATELIMIT_BROWSER_ERRORS_PER_MIN"`
}

// Ops reúne o que a operação da instância precisa (ADR-0062): onde ficam os backups e os binários do Postgres cliente.
type Ops struct {
	// Home é HANKO_HOME: diretório de dados do Hanko (padrão ~/.hanko); backups e o Postgres embutido ficam abaixo dele.
	Home string `yaml:"home" env:"HANKO_HOME"`
	// BackupDir é HANKO_BACKUP_DIR: onde `hanko backup` grava os arquivos (padrão <Home>/backups).
	BackupDir string `yaml:"backup_dir" env:"HANKO_BACKUP_DIR"`
	// DBDiskPath é HANKO_DB_DISK_PATH: um caminho no mesmo disco dos dados do Postgres (por exemplo o volume dele montado só para
	// leitura). Com ele o Hanko mede o espaço livre e abre o alerta `db_disk_low`; vazio = não mede (ADR-0083, comentário da #66).
	DBDiskPath string `yaml:"db_disk_path" env:"HANKO_DB_DISK_PATH"`
	// BackupKeep é HANKO_BACKUP_KEEP: quantos backups agendados ficam (os mais antigos são podados); 0 = padrão (7).
	BackupKeep int `yaml:"backup_keep" env:"HANKO_BACKUP_KEEP"`
	// PGDumpBin e PGRestoreBin são HANKO_PG_DUMP_BIN / HANKO_PG_RESTORE_BIN (padrão: pg_dump e pg_restore no PATH).
	PGDumpBin    string `yaml:"pg_dump_bin" env:"HANKO_PG_DUMP_BIN"`
	PGRestoreBin string `yaml:"pg_restore_bin" env:"HANKO_PG_RESTORE_BIN"`
	// PSQLBin é HANKO_PSQL_BIN: o psql que aplica o restore em uma transação (padrão: psql no PATH).
	PSQLBin string `yaml:"psql_bin" env:"HANKO_PSQL_BIN"`
}

// Config é a configuração completa do processo.
type Config struct {
	// Ops: backup e operação da instância (ADR-0062).
	Ops Ops `yaml:"ops"`

	// RateLimit: limites por minuto do rate limit geral (padrões seguros; env vence o banco).
	RateLimit RateLimit `yaml:"rate_limit"`

	DatabaseURL string `yaml:"database_url" env:"HANKO_DATABASE_URL"`
	Addr        string `yaml:"addr" env:"HANKO_ADDR"`
	// IngestAddr é o listener próprio da ingestão de telemetria de agente externo (ADR-0076), separado do público e
	// pensado para a tailnet. Vazio = a ingestão fica desligada.
	IngestAddr string `yaml:"ingest_addr" env:"HANKO_INGEST_ADDR"`
	PublicURL  string `yaml:"public_url" env:"HANKO_PUBLIC_URL"`
	// AgentURL é a base pela qual os agentes (processos filhos, no mesmo container) chamam /mcp.
	// Vazio = PublicURL. Em prd use http://127.0.0.1:7420 para não depender de DNS/proxy/TLS públicos.
	AgentURL string `yaml:"agent_url" env:"HANKO_AGENT_URL"`
	Mode     string `yaml:"mode" env:"HANKO_MODE"`
	Secret   string `yaml:"secret" env:"HANKO_SECRET"`
	// SecretKey cifra em repouso os segredos das integrações (AES-256-GCM, ADR-0029); SecretKeyPrevious é a
	// chave anterior durante uma rotação (só decifra). Sem SecretKey, integrações com segredo não podem ser salvas.
	// PushSubject identifica o operador perante o serviço de web push (VAPID sub, ADR-0042): "mailto:..." ou uma URL
	// https. Vazio = mailto do dono (HANKO_AUTH_OWNER_EMAIL) ou, sem ele, a HANKO_PUBLIC_URL quando é https.
	PushSubject       string        `yaml:"push_subject" env:"HANKO_PUSH_SUBJECT"`
	SecretKey         string        `yaml:"secret_key" env:"HANKO_SECRET_KEY"`
	SecretKeyPrevious string        `yaml:"secret_key_previous" env:"HANKO_SECRET_KEY_PREVIOUS"`
	Integrations      Integrations  `yaml:"integrations"`
	Storage           Storage       `yaml:"storage"`
	Vault             Vault         `yaml:"vault"`
	ACP               ACP           `yaml:"acp"`
	Plugins           Plugins       `yaml:"plugins"`
	Environments      Environments  `yaml:"environments"`
	MigrateOnBoot     bool          `yaml:"migrate_on_boot" env:"HANKO_MIGRATE_ON_BOOT"`
	Memory            Memory        `yaml:"memory"`
	Auth              Auth          `yaml:"auth"`
	ClaudeBin         string        `yaml:"claude_bin" env:"HANKO_CLAUDE_BIN"`
	CodexBin          string        `yaml:"codex_bin" env:"HANKO_CODEX_BIN"`
	DevinBin          string        `yaml:"devin_bin" env:"HANKO_DEVIN_BIN"`
	PiBin             string        `yaml:"pi_bin" env:"HANKO_PI_BIN"`
	CursorBin         string        `yaml:"cursor_bin" env:"HANKO_CURSOR_BIN"`
	Workers           int           `yaml:"workers" env:"HANKO_WORKERS"`
	RunTimeout        time.Duration `yaml:"run_timeout" env:"HANKO_RUN_TIMEOUT"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval" env:"HANKO_HEARTBEAT_INTERVAL"`
	LogLevel          string        `yaml:"log_level" env:"HANKO_LOG_LEVEL"`
	LogFormat         string        `yaml:"log_format" env:"HANKO_LOG_FORMAT"`

	// SkillSources liga as fontes de skills (o servidor clona repositórios públicos por https, ADR-0035).
	SkillSources bool `yaml:"skill_sources" env:"HANKO_SKILL_SOURCES"`

	// GitWorkspaces liga os workspaces de execução com Git (o servidor clona o repositório do workspace e cria um
	// worktree por task sob HANKO_WORKSPACES_ROOT, ADR-0054).
	GitWorkspaces bool `yaml:"git_workspaces" env:"HANKO_GIT_WORKSPACES"`

	// AgentEnvAllow lista variáveis extras do ambiente do servidor repassadas aos
	// harnesses (nomes exatos ou prefixos terminados em "*"); HANKO_* nunca passa.
	AgentEnvAllow []string `yaml:"agent_env_allow" env:"HANKO_AGENT_ENV_ALLOW" envSeparator:","`
	// AllowedHosts são hosts extras aceitos no cabeçalho Host (proteção contra DNS
	// rebinding), além de localhost/loopback e do host de PublicURL.
	AllowedHosts []string `yaml:"allowed_hosts" env:"HANKO_ALLOWED_HOSTS" envSeparator:","`
	// TrustedProxies são as redes (CIDR) cujos pares podem informar o IP do cliente por
	// X-Forwarded-For (HANKO_TRUSTED_PROXIES). Fora delas, o IP do cliente é o RemoteAddr.
	// Atrás de proxy em outra rede (o Caddy do compose, via rede docker), inclua a sub-rede dela.
	TrustedProxies []string `yaml:"trusted_proxies" env:"HANKO_TRUSTED_PROXIES" envSeparator:","`
	// WorkspacesRoot é a raiz sob a qual todo workspace precisa estar (default $HOME/hanko-workspaces).
	WorkspacesRoot string `yaml:"workspaces_root" env:"HANKO_WORKSPACES_ROOT"`
	// ToolsSingleEndpoint faz o harness receber um único endpoint do gateway de tools (/mcp/run) em vez de um servidor MCP
	// por integração; as tools saem como <integração>__<tool> (ADR-0053).
	ToolsSingleEndpoint bool `yaml:"tools_single_endpoint" env:"HANKO_TOOLS_SINGLE_ENDPOINT"`
	// RunReserveUSD é o piso (USD) da reserva de orçamento por run aberta: antes de abrir uma
	// run, gasto + reservas das runs abertas + a reserva da nova não podem passar do teto. A reserva
	// de cada run é o maior entre a média das últimas 10 runs da persona e este piso.
	RunReserveUSD float64 `yaml:"run_reserve_usd" env:"HANKO_RUN_RESERVE_USD"`
	// ArtifactMaxBytes é o tamanho máximo de um artefato: de 1 byte ao teto do driver de blobs (16 MiB no postgres,
	// 64 MiB no s3); sem valor, vale o teto do driver (ADR-0065). ArtifactQuotaBytes é a cota por empresa somando
	// todas as versões (padrão 5 GiB; ADR-0028).
	ArtifactMaxBytes   int64 `yaml:"artifact_max_bytes" env:"HANKO_ARTIFACT_MAX_BYTES"`
	ArtifactQuotaBytes int64 `yaml:"artifact_quota_bytes" env:"HANKO_ARTIFACT_QUOTA_BYTES"`
	// AlertSilentFactor é o N do alerta "persona sem heartbeat": a persona com trabalho esperando e
	// sem ser acordada além de N vezes o intervalo dela abre o alerta. AlertRunStuckAfter é o tempo
	// sem nenhum evento a partir do qual uma run em running vira alerta "execução presa".
	AlertSilentFactor  int           `yaml:"alert_silent_factor" env:"HANKO_ALERT_SILENT_FACTOR"`
	AlertRunStuckAfter time.Duration `yaml:"alert_run_stuck_after" env:"HANKO_ALERT_RUN_STUCK_AFTER"`
	// ReauthMinutes é a janela da reautenticação recente das ações sensíveis do cofre (ADR-0070): 1 a 60 min.
	ReauthMinutes int `yaml:"reauth_minutes" env:"HANKO_REAUTH_MINUTES"`
	// ReadinessInterval é a idade a partir da qual o teste de readiness de uma persona é refeito pela checagem
	// periódica (ADR-0055). 0 desliga a checagem periódica ("Testar agora" segue valendo).
	ReadinessInterval time.Duration `yaml:"readiness_interval" env:"HANKO_READINESS_INTERVAL"`

	// EnvSet marca as variáveis HANKO_* definidas no ambiente (valor não vazio). As que equivalem a uma chave de
	// instance_settings travam essa chave: env vence o banco (ADR-0050). Preenchido por Load.
	EnvSet map[string]bool `yaml:"-" env:"-"`

	// SecretGenerated indica que Secret foi gerado neste boot (run tokens não
	// sobrevivem a restart). Preenchido por Load.
	SecretGenerated bool `yaml:"-" env:"-"`
	// Warnings são avisos de configuração para o chamador logar após montar o logger.
	Warnings []string `yaml:"-" env:"-"`
}

// Defaults devolve a configuração padrão.
func Defaults() Config {
	return Config{
		Addr:               "127.0.0.1:7420",
		Mode:               ModeAuthenticated,
		MigrateOnBoot:      true,
		Memory:             Memory{Provider: MemoryAIMemory, URL: "http://127.0.0.1:49374", ReadTool: "memory_read_page"},
		Auth:               Auth{Provider: AuthLocal},
		Storage:            Storage{Driver: BlobPostgres, S3: S3{Region: "us-east-1", PathStyle: true}},
		Vault:              Vault{Mount: "secret"},
		TrustedProxies:     []string{"127.0.0.1/32", "::1/128"},
		ClaudeBin:          "claude",
		CodexBin:           "codex",
		DevinBin:           "devin",
		PiBin:              "pi",
		CursorBin:          "cursor-agent",
		Workers:            4,
		RunTimeout:         30 * time.Minute,
		HeartbeatInterval:  time.Minute,
		RunReserveUSD:      1.0,
		ArtifactQuotaBytes: DefaultArtifactQuotaBytes,
		AlertSilentFactor:  3,
		AlertRunStuckAfter: 15 * time.Minute,
		ReauthMinutes:      5,
		ReadinessInterval:  15 * time.Minute,
		Ops:                Ops{BackupKeep: 7, PGDumpBin: "pg_dump", PGRestoreBin: "pg_restore", PSQLBin: "psql"},
		RateLimit:          RateLimit{Enabled: true, WritesPerMin: 600, IPPerMin: 1200, MCPPerMin: 1500, WebhooksPerMin: 120, BrowserErrPerMin: 20},
		LogLevel:           "info",
		LogFormat:          LogJSON,
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

	cfg.EnvSet = map[string]bool{}
	for k, v := range environFor(getenv) {
		cfg.EnvSet[k] = v != ""
	}
	if err := cfg.finalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// environFor monta o mapa de ambiente para as chaves HANKO_* conhecidas.
func environFor(getenv func(string) string) map[string]string {
	keys := []string{
		"HANKO_DATABASE_URL", "HANKO_ADDR", "HANKO_INGEST_ADDR", "HANKO_PUBLIC_URL", "HANKO_AGENT_URL", "HANKO_MODE", "HANKO_SECRET",
		"HANKO_MIGRATE_ON_BOOT", "HANKO_MEMORY_PROVIDER", "HANKO_MEMORY_URL", "HANKO_MEMORY_TOKEN", "HANKO_MEMORY_READ_TOOL",
		"HANKO_CLAUDE_BIN", "HANKO_CODEX_BIN", "HANKO_DEVIN_BIN", "HANKO_PI_BIN", "HANKO_CURSOR_BIN", "HANKO_WORKERS", "HANKO_RUN_TIMEOUT",
		"HANKO_HEARTBEAT_INTERVAL", "HANKO_LOG_LEVEL", "HANKO_LOG_FORMAT",
		"HANKO_AGENT_ENV_ALLOW", "HANKO_ALLOWED_HOSTS", "HANKO_WORKSPACES_ROOT", "HANKO_RUN_RESERVE_USD", "HANKO_ARTIFACT_MAX_BYTES", "HANKO_ARTIFACT_QUOTA_BYTES", "HANKO_ALERT_SILENT_FACTOR", "HANKO_ALERT_RUN_STUCK_AFTER", "HANKO_REAUTH_MINUTES",
		"HANKO_RATELIMIT_ENABLED", "HANKO_RATELIMIT_WRITES_PER_MIN", "HANKO_RATELIMIT_IP_PER_MIN", "HANKO_RATELIMIT_MCP_PER_MIN",
		"HANKO_RATELIMIT_WEBHOOKS_PER_MIN", "HANKO_RATELIMIT_BROWSER_ERRORS_PER_MIN",
		"HANKO_AUTH_PROVIDER", "HANKO_AUTH_ALLOWED_EMAILS", "HANKO_AUTH_ALLOWED_DOMAINS",
		"HANKO_FIREBASE_PROJECT_ID", "HANKO_FIREBASE_API_KEY", "HANKO_FIREBASE_AUTH_DOMAIN",
		"HANKO_AUTH_OWNER_EMAIL", "HANKO_AUTH_WEB_BOOTSTRAP", "HANKO_TRUSTED_PROXIES",
		"HANKO_SECRET_KEY", "HANKO_SECRET_KEY_PREVIOUS", "HANKO_PUSH_SUBJECT", "HANKO_INTEGRATIONS_ALLOW_STDIO", "HANKO_INTEGRATIONS_ALLOW_PRIVATE",
		"HANKO_ACP_ALLOW", "HANKO_ACP_COMMANDS", "HANKO_SKILL_SOURCES", "HANKO_GIT_WORKSPACES", "HANKO_TOOLS_SINGLE_ENDPOINT",
		"HANKO_BLOB_DRIVER", "HANKO_BLOB_DIR", "HANKO_S3_ENDPOINT", "HANKO_S3_REGION", "HANKO_S3_BUCKET", "HANKO_S3_PREFIX", "HANKO_S3_PATH_STYLE",
		"HANKO_S3_ACCESS_KEY", "HANKO_S3_SECRET_KEY",
		"HANKO_VAULT_ADDR", "HANKO_VAULT_MOUNT", "HANKO_VAULT_NAMESPACE", "HANKO_VAULT_TOKEN",
		"HANKO_ENV_SSH_ALLOW_HOSTS", "HANKO_ENV_SANDBOX", "HANKO_ENV_SANDBOX_HOST_ROOT",
		"HANKO_ENV_CLOUD", "HANKO_ENV_CLOUD_API_URL", "HANKO_ENV_CLOUD_SSH_HOST", "HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS", "HANKO_READINESS_INTERVAL",
		"HANKO_PLUGINS_DIR", "HANKO_PLUGINS_DATA_DIR",
		"HANKO_HOME", "HANKO_BACKUP_DIR", "HANKO_DB_DISK_PATH", "HANKO_BACKUP_KEEP", "HANKO_PG_DUMP_BIN", "HANKO_PG_RESTORE_BIN", "HANKO_PSQL_BIN",
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
	c.Auth.OwnerEmail = strings.ToLower(strings.TrimSpace(c.Auth.OwnerEmail))
	c.TrustedProxies = cleanList(c.TrustedProxies)
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	c.LogFormat = strings.ToLower(strings.TrimSpace(c.LogFormat))
	c.Addr = strings.TrimSpace(c.Addr)
	c.Storage.Driver = strings.ToLower(strings.TrimSpace(c.Storage.Driver))
	if c.Storage.Driver == "" {
		c.Storage.Driver = BlobPostgres
	}
	if c.ArtifactMaxBytes == 0 && !c.EnvSet["HANKO_ARTIFACT_MAX_BYTES"] { // sem valor: o teto do driver ativo (ADR-0065)
		c.ArtifactMaxBytes = c.MaxArtifactBytes()
	}
	c.Storage.Dir = strings.TrimSpace(c.Storage.Dir)
	c.Storage.S3.Endpoint = strings.TrimSpace(c.Storage.S3.Endpoint)
	c.Storage.S3.Bucket = strings.TrimSpace(c.Storage.S3.Bucket)
	c.Vault.Addr = strings.TrimSpace(c.Vault.Addr)
	c.Vault.Token = strings.TrimSpace(c.Vault.Token)

	if c.PublicURL == "" {
		c.PublicURL = "http://" + c.Addr
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	c.AgentURL = strings.TrimRight(strings.TrimSpace(c.AgentURL), "/")
	if c.AgentURL == "" {
		c.AgentURL = c.PublicURL
	}
	c.AgentEnvAllow = cleanList(c.AgentEnvAllow)
	c.AllowedHosts = cleanList(c.AllowedHosts)
	c.Environments.SSHAllowHosts = cleanList(c.Environments.SSHAllowHosts)
	c.Environments.Sandbox = strings.ToLower(strings.TrimSpace(c.Environments.Sandbox))
	c.Environments.SandboxHostRoot = strings.TrimSpace(c.Environments.SandboxHostRoot)
	c.Environments.Cloud = strings.ToLower(strings.TrimSpace(c.Environments.Cloud))
	c.Environments.CloudAPIURL = strings.TrimSpace(c.Environments.CloudAPIURL)
	c.Environments.CloudSSHHost = strings.TrimSpace(c.Environments.CloudSSHHost)
	c.Environments.CloudSSHKnownHosts = strings.TrimSpace(c.Environments.CloudSSHKnownHosts)
	c.WorkspacesRoot = strings.TrimSpace(c.WorkspacesRoot)
	if c.WorkspacesRoot == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c.WorkspacesRoot = filepath.Join(home, "hanko-workspaces")
		}
	}

	c.Ops.Home = strings.TrimSpace(c.Ops.Home)
	if c.Ops.Home == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c.Ops.Home = filepath.Join(home, ".hanko")
		}
	}
	c.Ops.DBDiskPath = strings.TrimSpace(c.Ops.DBDiskPath)
	c.Ops.BackupDir = strings.TrimSpace(c.Ops.BackupDir)
	if c.Ops.BackupDir == "" && c.Ops.Home != "" {
		c.Ops.BackupDir = filepath.Join(c.Ops.Home, "backups")
	}
	if c.Ops.BackupKeep == 0 {
		c.Ops.BackupKeep = 7
	}
	if c.Ops.PGDumpBin = strings.TrimSpace(c.Ops.PGDumpBin); c.Ops.PGDumpBin == "" {
		c.Ops.PGDumpBin = "pg_dump"
	}
	if c.Ops.PGRestoreBin = strings.TrimSpace(c.Ops.PGRestoreBin); c.Ops.PGRestoreBin == "" {
		c.Ops.PGRestoreBin = "pg_restore"
	}
	if c.Ops.PSQLBin = strings.TrimSpace(c.Ops.PSQLBin); c.Ops.PSQLBin == "" {
		c.Ops.PSQLBin = "psql"
	}

	c.Plugins.Dir = strings.TrimSpace(c.Plugins.Dir)
	c.Plugins.DataDir = strings.TrimSpace(c.Plugins.DataDir)
	if c.Plugins.Dir != "" && c.Plugins.DataDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c.Plugins.DataDir = filepath.Join(home, "hanko-data")
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
	if c.Mode == ModeAuthenticated && c.Auth.Provider == AuthFirebase && !c.Auth.ListsConfigured() && c.Auth.OwnerEmail == "" {
		c.Warnings = append(c.Warnings,
			"auth firebase sem HANKO_AUTH_ALLOWED_EMAILS/HANKO_AUTH_ALLOWED_DOMAINS nem HANKO_AUTH_OWNER_EMAIL (aceito só em loopback): só o primeiro usuário a entrar (dono) é admitido; os demais são recusados")
	}
	if c.Mode == ModeTrusted {
		c.Warnings = append(c.Warnings,
			"modo trusted: sem login; qualquer processo local (inclusive agentes sem token) age como humano — use só para teste descartável (ADR-0017)")
	}
	if c.Mode == ModeTrusted && !c.IsLoopback() {
		c.Warnings = append(c.Warnings,
			"modo trusted com bind fora de loopback: qualquer cliente que alcance "+c.Addr+" age como humano; /mcp sem token fica desabilitado")
	}
	return nil
}

// MaxArtifactBytes é o teto do limite por arquivo de artefato para o driver de blobs configurado (ADR-0051).
func (c Config) MaxArtifactBytes() int64 {
	if c.Storage.Driver == BlobS3 || c.Storage.Driver == BlobFS {
		return MaxArtifactBytesS3
	}
	return MaxArtifactBytesPostgres
}

// S3Configured informa se o driver s3 tem tudo para abrir (mesmo com o driver ativo sendo postgres, para ler e migrar).
func (c Config) S3Configured() bool {
	s := c.Storage.S3
	return s.Endpoint != "" && s.Bucket != "" && s.AccessKey != "" && s.SecretKey != ""
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
	if c.IngestAddr != "" {
		host, _, err := net.SplitHostPort(c.IngestAddr)
		if err != nil {
			add("HANKO_INGEST_ADDR %q inválido: use host:porta (ex.: 100.64.0.1:7421)", c.IngestAddr)
		} else if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			add("HANKO_INGEST_ADDR %q escuta em todas as interfaces: use o endereço da tailnet (ex.: 100.64.0.1:7421)", c.IngestAddr)
		} else if c.IngestAddr == c.Addr {
			add("HANKO_INGEST_ADDR não pode ser o mesmo endereço de HANKO_ADDR: a ingestão tem listener próprio (ADR-0076)")
		}
	}
	if sub := strings.TrimSpace(c.PushSubject); sub != "" && !strings.HasPrefix(sub, "mailto:") && !strings.HasPrefix(sub, "https://") {
		add("HANKO_PUSH_SUBJECT %q inválido: use mailto:alguem@exemplo.org ou uma URL https", c.PushSubject)
	}
	if u, err := url.Parse(c.PublicURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		add("HANKO_PUBLIC_URL %q inválido: use http(s)://host[:porta]", c.PublicURL)
	}
	if u, err := url.Parse(c.AgentURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		add("HANKO_AGENT_URL %q inválido: use http(s)://host[:porta]", c.AgentURL)
	}
	if sb := c.Environments.Sandbox; sb != "" && sb != "docker" {
		add("HANKO_ENV_SANDBOX %q inválido: use docker (ou deixe vazio para desligar o sandbox)", sb)
	}
	if cl := c.Environments.Cloud; cl != "" {
		if cl != "daytona" {
			add("HANKO_ENV_CLOUD %q inválido: use daytona (ou deixe vazio para desligar o sandbox em nuvem)", cl)
		} else if c.Environments.CloudSSHKnownHosts == "" {
			add("HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS é obrigatório com HANKO_ENV_CLOUD=daytona: cole a saída de ssh-keyscan do gateway SSH")
		}
	}
	if c.Ops.BackupKeep < 0 {
		add("HANKO_BACKUP_KEEP %d inválido: use um valor >= 1 (padrão 7)", c.Ops.BackupKeep)
	}
	if c.ReadinessInterval < 0 {
		add("HANKO_READINESS_INTERVAL %v inválido: use uma duração >= 0 (0 desliga a checagem periódica)", c.ReadinessInterval)
	}
	if hr := c.Environments.SandboxHostRoot; hr != "" && !filepath.IsAbs(hr) {
		add("HANKO_ENV_SANDBOX_HOST_ROOT %q inválido: use um caminho absoluto do host do docker", hr)
	}
	if c.RunReserveUSD < 0 {
		add("HANKO_RUN_RESERVE_USD %v inválido: use um valor >= 0 (0 usa o padrão de US$ 1,00)", c.RunReserveUSD)
	}
	if maxBytes := c.MaxArtifactBytes(); c.ArtifactMaxBytes < 1 || c.ArtifactMaxBytes > maxBytes {
		add("HANKO_ARTIFACT_MAX_BYTES %d inválido: use de 1 a %d (%d MiB com o driver %s; sem valor, vale esse teto)", c.ArtifactMaxBytes, maxBytes, maxBytes>>20, c.Storage.Driver)
	}
	switch c.Storage.Driver {
	case BlobPostgres:
	case BlobS3:
		if c.Storage.S3.Endpoint == "" || c.Storage.S3.Bucket == "" || c.Storage.S3.AccessKey == "" || c.Storage.S3.SecretKey == "" {
			add("HANKO_BLOB_DRIVER=s3 exige HANKO_S3_ENDPOINT, HANKO_S3_BUCKET, HANKO_S3_ACCESS_KEY e HANKO_S3_SECRET_KEY")
		}
	case BlobFS:
		if c.Storage.Dir == "" {
			add("HANKO_BLOB_DRIVER=fs exige HANKO_BLOB_DIR (caminho absoluto)")
		}
	default:
		add("HANKO_BLOB_DRIVER %q inválido: use %q, %q ou %q", c.Storage.Driver, BlobPostgres, BlobS3, BlobFS)
	}
	if d := c.Storage.Dir; d != "" && !filepath.IsAbs(d) {
		add("HANKO_BLOB_DIR %q inválido: use um caminho absoluto", d)
	}
	if (c.Vault.Addr == "") != (c.Vault.Token == "") {
		add("HANKO_VAULT_ADDR e HANKO_VAULT_TOKEN precisam ser definidos juntos")
	}
	if c.ArtifactQuotaBytes < c.ArtifactMaxBytes {
		add("HANKO_ARTIFACT_QUOTA_BYTES %d inválido: a cota por empresa precisa caber ao menos um artefato máximo (padrão 5 GiB)", c.ArtifactQuotaBytes)
	}
	if c.AlertSilentFactor < 1 {
		add("HANKO_ALERT_SILENT_FACTOR %d inválido: use um valor >= 1 (padrão 3)", c.AlertSilentFactor)
	}
	if c.AlertRunStuckAfter <= 0 {
		add("HANKO_ALERT_RUN_STUCK_AFTER deve ser positivo (ex.: 15m)")
	}
	if c.ReauthMinutes < 1 || c.ReauthMinutes > 60 {
		add("HANKO_REAUTH_MINUTES %d inválido: use de 1 a 60 (padrão 5)", c.ReauthMinutes)
	}
	for name, v := range map[string]int64{"HANKO_RATELIMIT_WRITES_PER_MIN": c.RateLimit.WritesPerMin, "HANKO_RATELIMIT_IP_PER_MIN": c.RateLimit.IPPerMin,
		"HANKO_RATELIMIT_MCP_PER_MIN": c.RateLimit.MCPPerMin, "HANKO_RATELIMIT_WEBHOOKS_PER_MIN": c.RateLimit.WebhooksPerMin,
		"HANKO_RATELIMIT_BROWSER_ERRORS_PER_MIN": c.RateLimit.BrowserErrPerMin} {
		if v < 1 {
			add("%s %d inválido: use um valor >= 1", name, v)
		}
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
	for name, k := range map[string]string{"HANKO_SECRET_KEY": c.SecretKey, "HANKO_SECRET_KEY_PREVIOUS": c.SecretKeyPrevious} {
		if k != "" && len(k) < MinSecretLen {
			add("%s tem %d bytes: use no mínimo %d (ex.: openssl rand -hex 32)", name, len(k), MinSecretLen)
		}
	}
	if c.SecretKeyPrevious != "" && c.SecretKey == "" {
		add("HANKO_SECRET_KEY_PREVIOUS exige HANKO_SECRET_KEY (a chave nova)")
	}
	for _, e := range c.TrustedProxies {
		if !validProxyEntry(e) {
			add("HANKO_TRUSTED_PROXIES: %q inválido: use um CIDR (ex.: 172.18.0.0/16) ou IP", e)
		}
	}
	if c.WorkspacesRoot != "" && !filepath.IsAbs(c.WorkspacesRoot) {
		add("HANKO_WORKSPACES_ROOT %q deve ser um caminho absoluto", c.WorkspacesRoot)
	}
	if c.Plugins.Dir != "" && !filepath.IsAbs(c.Plugins.Dir) {
		add("HANKO_PLUGINS_DIR %q deve ser um caminho absoluto", c.Plugins.Dir)
	}
	if c.Plugins.DataDir != "" && !filepath.IsAbs(c.Plugins.DataDir) {
		add("HANKO_PLUGINS_DATA_DIR %q deve ser um caminho absoluto", c.Plugins.DataDir)
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
		if c.Auth.OwnerEmail != "" && !strings.Contains(c.Auth.OwnerEmail, "@") {
			add("HANKO_AUTH_OWNER_EMAIL %q inválido: use um e-mail", c.Auth.OwnerEmail)
		}
		if !c.PublicLoopback() && !c.Auth.ListsConfigured() && c.Auth.OwnerEmail == "" {
			add("HANKO_AUTH_PROVIDER=firebase com HANKO_PUBLIC_URL público (%q) exige HANKO_AUTH_ALLOWED_EMAILS/HANKO_AUTH_ALLOWED_DOMAINS ou HANKO_AUTH_OWNER_EMAIL: sem isso o primeiro visitante que fizer login viraria dono", c.PublicURL)
		}
	default:
		add("HANKO_AUTH_PROVIDER %q inválido: use %q ou %q", c.Auth.Provider, AuthLocal, AuthFirebase)
	}
}

// ListsConfigured informa se há lista de admissão (e-mails e/ou domínios).
func (a Auth) ListsConfigured() bool { return len(a.AllowedEmails)+len(a.AllowedDomains) > 0 }

// PublicLoopback informa se o host de PublicURL é localhost/loopback.
func (c Config) PublicLoopback() bool {
	u, err := url.Parse(c.PublicURL)
	return err == nil && loopbackHost(u.Hostname())
}

// WebBootstrapAllowed informa se a tela de entrada pode criar o primeiro dono (provider local):
// só com PublicURL em loopback ou HANKO_AUTH_WEB_BOOTSTRAP=true.
func (c Config) WebBootstrapAllowed() bool { return c.PublicLoopback() || c.Auth.WebBootstrap }

// Authenticated informa se o servidor exige login de humanos.
func (c Config) Authenticated() bool { return c.Mode == ModeAuthenticated }

// SecureCookies informa se os cookies devem levar o atributo Secure (PublicURL https).
func (c Config) SecureCookies() bool { return strings.HasPrefix(c.PublicURL, "https://") }

func validProxyEntry(e string) bool {
	if strings.Contains(e, "/") {
		_, _, err := net.ParseCIDR(e)
		return err == nil
	}
	return net.ParseIP(e) != nil
}

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

// PushSubjectOrDefault é o contato do VAPID (ADR-0042): HANKO_PUSH_SUBJECT, senão o e-mail do dono, senão a URL
// pública quando é https; vazio se nada disso existe (o service usa um valor neutro).
func (c Config) PushSubjectOrDefault() string {
	switch {
	case strings.TrimSpace(c.PushSubject) != "":
		return strings.TrimSpace(c.PushSubject)
	case c.Auth.OwnerEmail != "":
		return "mailto:" + c.Auth.OwnerEmail
	case strings.HasPrefix(c.PublicURL, "https://"):
		return c.PublicURL
	}
	return ""
}
