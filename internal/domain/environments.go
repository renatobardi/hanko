package domain

import (
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"
	"time"
)

// Ambientes de execução (ADR-0037): onde as runs de uma persona rodam. O ambiente local (processos filhos
// neste servidor) é implícito e é o padrão; os demais são linhas de environments por empresa.

// EnvironmentDriver é o tipo de um ambiente.
type EnvironmentDriver string

const (
	// EnvLocal roda o harness como processo filho do servidor (o comportamento de sempre).
	EnvLocal EnvironmentDriver = "local"
	// EnvSSH roda o harness num host remoto pelo binário ssh do sistema, com o workspace sincronizado.
	EnvSSH EnvironmentDriver = "ssh"
	// EnvSandbox roda o harness num contêiner descartável por run, com o workspace montado.
	EnvSandbox EnvironmentDriver = "sandbox"
)

// Valid informa se d é um driver conhecido.
func (d EnvironmentDriver) Valid() bool { return d == EnvLocal || d == EnvSSH || d == EnvSandbox }

// Remote informa se o driver tira a run deste servidor.
func (d EnvironmentDriver) Remote() bool { return d == EnvSSH || d == EnvSandbox }

// EnvironmentStatus é o estado de um ambiente cadastrado.
type EnvironmentStatus string

const (
	EnvActive   EnvironmentStatus = "active"
	EnvArchived EnvironmentStatus = "archived"
)

// Valid informa se st é um estado conhecido.
func (st EnvironmentStatus) Valid() bool { return st == EnvActive || st == EnvArchived }

// Estados de um uso (lease) de ambiente por uma run.
const (
	LeaseActive   = "active"
	LeaseReleased = "released"
	LeaseFailed   = "failed"
)

// Redes de um sandbox.
const (
	SandboxNetworkNone   = "none"
	SandboxNetworkBridge = "bridge"
)

// Tetos e padrões dos ambientes.
const (
	MaxEnvironmentNameRunes = 120
	MaxEnvironmentDescRunes = 500
	MaxKnownHostsBytes      = 64 << 10
	MaxPrivateKeyBytes      = 16 << 10
	MaxEnvironmentEnvVars   = 30
	MaxEnvironmentEnvValue  = 4096
	MaxRemoteRootBytes      = 512

	DefaultSSHPort          = 22
	DefaultSandboxMemoryMiB = 2048
	DefaultSandboxCPUs      = 2.0
	DefaultSandboxPIDs      = 512
	MaxSandboxMemoryMiB     = 64 << 10
	MaxSandboxCPUs          = 64.0
	MaxSandboxPIDs          = 65536
)

// SSHConfig é a parte não secreta de um ambiente SSH (a chave privada fica cifrada).
type SSHConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"username"`
	// Root é o diretório absoluto do host remoto sob o qual cada run ganha a sua pasta.
	Root string `json:"remote_workspace_root"`
	// KnownHosts são as linhas de known_hosts que provam a chave do host (obrigatório: nunca se aceita uma
	// chave desconhecida).
	KnownHosts string `json:"known_hosts"`
}

// SandboxConfig é a configuração de um ambiente sandbox (contêiner por run).
type SandboxConfig struct {
	Image     string  `json:"image"`
	Network   string  `json:"network"`
	MemoryMiB int     `json:"memory_mib"`
	CPUs      float64 `json:"cpus"`
	PIDsLimit int     `json:"pids_limit"`
}

// EnvironmentConfig é o que um ambiente guarda em config (jsonb), sem segredos.
type EnvironmentConfig struct {
	SSH     *SSHConfig     `json:"ssh,omitempty"`
	Sandbox *SandboxConfig `json:"sandbox,omitempty"`
	// EnvNames são os nomes das variáveis do ambiente (os valores ficam cifrados).
	EnvNames []string `json:"env_names,omitempty"`
}

// ProbeStep é uma verificação do teste de conexão.
type ProbeStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// ProbeReport é o resultado de um teste de conexão de ambiente.
type ProbeReport struct {
	OK         bool              `json:"ok"`
	Steps      []ProbeStep       `json:"steps"`
	System     string            `json:"system,omitempty"`
	Binaries   map[string]string `json:"binaries,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
	DurationMs int64             `json:"duration_ms"`
}

// Environment é um ambiente de execução cadastrado. Nunca carrega a chave privada nem valores de variáveis.
type Environment struct {
	ID          ID                `json:"id"`
	CompanyID   ID                `json:"company_id"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Driver      EnvironmentDriver `json:"driver"`
	Status      EnvironmentStatus `json:"status"`
	Config      EnvironmentConfig `json:"config"`
	IsDefault   bool              `json:"is_default"`
	// KeySet: há chave privada guardada (SSH).
	KeySet          bool         `json:"key_set"`
	LastProbeAt     *time.Time   `json:"last_probe_at,omitempty"`
	LastProbeOK     *bool        `json:"last_probe_ok,omitempty"`
	LastProbeReport *ProbeReport `json:"last_probe_report,omitempty"`
	CreatedBy       Actor        `json:"created_by"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// EnvironmentLease é o uso de um ambiente por uma run.
type EnvironmentLease struct {
	ID            ID         `json:"id"`
	CompanyID     ID         `json:"company_id"`
	EnvironmentID ID         `json:"environment_id"`
	RunID         ID         `json:"run_id"`
	Status        string     `json:"status"`
	RemoteDir     string     `json:"remote_dir,omitempty"`
	AcquiredAt    time.Time  `json:"acquired_at"`
	ReleasedAt    *time.Time `json:"released_at,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
}

var (
	sshUserRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}$`)
	sshHostRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
	imageRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]{0,254}(@sha256:[a-f0-9]{64})?$`)
	digestRe   = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)
	EnvNameRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	unsafeRoot = regexp.MustCompile("[\x00-\x1f\x7f'\"`$\\\\]")
)

// ValidSSHHost informa se h é um nome de host ou IP aceito (sem opção disfarçada nem espaço).
func ValidSSHHost(h string) bool {
	if ip := net.ParseIP(h); ip != nil {
		return true
	}
	return sshHostRe.MatchString(h) && !strings.Contains(h, "..")
}

// Normalize aplica os padrões e valida a configuração SSH.
func (c *SSHConfig) Normalize() error {
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	c.Root = strings.TrimSpace(c.Root)
	c.KnownHosts = strings.TrimSpace(strings.ReplaceAll(c.KnownHosts, "\r\n", "\n"))
	if c.Port == 0 {
		c.Port = DefaultSSHPort
	}
	switch {
	case c.Host == "" || !ValidSSHHost(c.Host):
		return Invalid("host", "use a host name or an IP address")
	case c.Port < 1 || c.Port > 65535:
		return Invalid("port", "must be between 1 and 65535")
	case !sshUserRe.MatchString(c.User):
		return Invalid("username", "use letters, digits, dot, dash and underscore (up to 32)")
	case c.Root == "" || !path.IsAbs(c.Root) || path.Clean(c.Root) != c.Root || c.Root == "/":
		return Invalid("remote_workspace_root", "must be a clean absolute path other than /")
	case len(c.Root) > MaxRemoteRootBytes || unsafeRoot.MatchString(c.Root):
		return Invalid("remote_workspace_root", "contains characters that are not allowed")
	}
	return ValidKnownHosts(c.KnownHosts)
}

// ValidKnownHosts confere que há pelo menos uma linha de known_hosts bem formada (hosts, tipo e chave).
func ValidKnownHosts(kh string) error {
	if kh == "" {
		return Invalid("known_hosts", "required: paste the host key lines (ssh-keyscan -H host); unknown host keys are never accepted")
	}
	if len(kh) > MaxKnownHostsBytes || strings.ContainsRune(kh, 0) {
		return Invalid("known_hosts", "too long or invalid")
	}
	n := 0
	for _, line := range strings.Split(kh, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if strings.HasPrefix(f[0], "@") { // @cert-authority / @revoked
			f = f[1:]
		}
		if len(f) < 3 {
			return Invalid("known_hosts", fmt.Sprintf("malformed line: %q", clipText(line, 60)))
		}
		n++
	}
	if n == 0 {
		return Invalid("known_hosts", "no host key line found")
	}
	return nil
}

// ValidPrivateKey confere o formato de uma chave privada em PEM/OpenSSH (sem decifrar nada).
func ValidPrivateKey(k string) error {
	switch {
	case k == "":
		return Invalid("private_key", "required")
	case len(k) > MaxPrivateKeyBytes || strings.ContainsRune(k, 0):
		return Invalid("private_key", "too long or invalid")
	case !strings.Contains(k, "-----BEGIN ") || !strings.Contains(k, "PRIVATE KEY-----"):
		return Invalid("private_key", "paste a private key in PEM/OpenSSH format (-----BEGIN ... PRIVATE KEY-----)")
	case strings.Contains(k, "ENCRYPTED"):
		return Invalid("private_key", "keys protected by a passphrase are not supported (batch mode)")
	}
	return nil
}

// Normalize aplica os padrões e valida a configuração do sandbox.
func (c *SandboxConfig) Normalize() error {
	c.Image = strings.TrimSpace(c.Image)
	c.Network = strings.ToLower(strings.TrimSpace(c.Network))
	if c.Network == "" {
		c.Network = SandboxNetworkNone
	}
	if c.MemoryMiB == 0 {
		c.MemoryMiB = DefaultSandboxMemoryMiB
	}
	if c.CPUs == 0 {
		c.CPUs = DefaultSandboxCPUs
	}
	if c.PIDsLimit == 0 {
		c.PIDsLimit = DefaultSandboxPIDs
	}
	switch {
	case !imageRe.MatchString(c.Image) || strings.Contains(c.Image, ".."):
		return Invalid("image", "use an image reference like registry/name:tag or name@sha256:<digest>")
	case c.Network != SandboxNetworkNone && c.Network != SandboxNetworkBridge:
		return Invalid("network", "must be none or bridge")
	case c.MemoryMiB < 64 || c.MemoryMiB > MaxSandboxMemoryMiB:
		return Invalid("memory_mib", fmt.Sprintf("must be between 64 and %d", MaxSandboxMemoryMiB))
	case c.CPUs < 0.1 || c.CPUs > MaxSandboxCPUs:
		return Invalid("cpus", fmt.Sprintf("must be between 0.1 and %g", MaxSandboxCPUs))
	case c.PIDsLimit < 16 || c.PIDsLimit > MaxSandboxPIDs:
		return Invalid("pids_limit", fmt.Sprintf("must be between 16 and %d", MaxSandboxPIDs))
	}
	return nil
}

// Pinned informa se a imagem está fixada por digest (o conteúdo não muda entre runs).
func (c SandboxConfig) Pinned() bool { return digestRe.MatchString(c.Image) }

// ValidateEnvironmentEnv confere nomes e valores das variáveis de um ambiente. HANKO_* é reservado (o Hanko
// define as dele em cada run) e PATH/HOME ficam com o alvo.
func ValidateEnvironmentEnv(env map[string]string) error {
	if len(env) > MaxEnvironmentEnvVars {
		return Invalid("env", fmt.Sprintf("at most %d variables", MaxEnvironmentEnvVars))
	}
	for k, v := range env {
		switch {
		case !EnvNameRe.MatchString(k):
			return Invalid("env", fmt.Sprintf("invalid variable name %q", k))
		case strings.HasPrefix(strings.ToUpper(k), "HANKO_") || k == "PATH" || k == "HOME" || strings.HasPrefix(k, "LD_"):
			return Invalid("env", fmt.Sprintf("variable %q is reserved", k))
		case len(v) > MaxEnvironmentEnvValue || strings.ContainsRune(v, 0):
			return Invalid("env", fmt.Sprintf("invalid value for variable %q", k))
		}
	}
	return nil
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
