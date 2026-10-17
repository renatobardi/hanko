// Package execenv tira a execução de uma run deste servidor (ADR-0037): um host remoto pelo binário ssh do
// sistema, com o workspace sincronizado por tar, ou um contêiner descartável por run. O pacote monta os
// comandos (argv, nunca shell local), sincroniza os arquivos, faz o teste de conexão e implementa
// adapter.ExecTarget para os adapters. Quem decide se cada driver pode ser usado é o operador da instalação
// (Config), não quem administra uma empresa.
package execenv

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
)

// Config é a política do operador e o que a execução remota precisa saber da instalação.
type Config struct {
	// SSHAllowHosts é HANKO_ENV_SSH_ALLOW_HOSTS: hosts exatos, sufixos (".exemplo.com" ou "*.exemplo.com"),
	// IPs e redes CIDR (só valem para hosts informados como IP) ou "*" para qualquer host. Vazio = SSH desligado.
	SSHAllowHosts []string
	// Sandbox é HANKO_ENV_SANDBOX: "docker" liga o driver sandbox; vazio = desligado.
	Sandbox string
	// SandboxHostRoot é HANKO_ENV_SANDBOX_HOST_ROOT: quando o Hanko roda dentro de um contêiner, o caminho, no
	// host do docker, do diretório que aqui é WorkspacesRoot (os volumes do sandbox usam caminhos do host).
	SandboxHostRoot string
	// WorkspacesRoot é HANKO_WORKSPACES_ROOT.
	WorkspacesRoot string
	// RemoteURL é a URL pública da instalação (HANKO_PUBLIC_URL), pela qual o harness remoto alcança o MCP.
	RemoteURL string
	// SSHBin e DockerBin são os binários usados ("ssh" e "docker" quando vazios). Só testes trocam.
	SSHBin, DockerBin string
	// LookPath é exec.LookPath trocável nos testes.
	LookPath func(string) (string, error)
}

// SandboxDocker é o único motor de sandbox suportado.
const SandboxDocker = "docker"

func (c Config) sshBin() string {
	if c.SSHBin != "" {
		return c.SSHBin
	}
	return "ssh"
}

func (c Config) dockerBin() string {
	if c.DockerBin != "" {
		return c.DockerBin
	}
	return "docker"
}

func (c Config) lookPath(bin string) (string, error) {
	if c.LookPath != nil {
		return c.LookPath(bin)
	}
	return exec.LookPath(bin)
}

// ErrSSHOff e ErrSandboxOff são os interruptores do operador desligados.
var (
	ErrSSHOff     = errors.New("SSH está desligado neste servidor (HANKO_ENV_SSH_ALLOW_HOSTS vazio)")
	ErrSandboxOff = errors.New("sandbox está desligado neste servidor (HANKO_ENV_SANDBOX=docker não definido)")
)

// SSHEnabled informa se o operador ligou o driver SSH.
func (c Config) SSHEnabled() bool { return len(cleanList(c.SSHAllowHosts)) > 0 }

// SSHStatus diz se o driver SSH pode rodar agora e, se não, por quê.
func (c Config) SSHStatus() error {
	if !c.SSHEnabled() {
		return ErrSSHOff
	}
	if _, err := c.lookPath(c.sshBin()); err != nil {
		return fmt.Errorf("o binário %q não está no PATH do servidor (instale openssh-client)", c.sshBin())
	}
	return nil
}

// SandboxStatus diz se o driver sandbox pode rodar agora e, se não, por quê.
func (c Config) SandboxStatus() error {
	if strings.ToLower(strings.TrimSpace(c.Sandbox)) != SandboxDocker {
		return ErrSandboxOff
	}
	if _, err := c.lookPath(c.dockerBin()); err != nil {
		return fmt.Errorf("o binário %q não está no PATH do servidor", c.dockerBin())
	}
	return nil
}

// DriverStatus diz se o driver pode rodar neste servidor (nil = pode).
func (c Config) DriverStatus(d domain.EnvironmentDriver) error {
	switch d {
	case domain.EnvLocal:
		return nil
	case domain.EnvSSH:
		return c.SSHStatus()
	case domain.EnvSandbox:
		return c.SandboxStatus()
	}
	return fmt.Errorf("driver desconhecido %q", d)
}

// CheckSSHHost confere host contra a lista do operador.
func (c Config) CheckSSHHost(host string) error {
	allow := cleanList(c.SSHAllowHosts)
	if len(allow) == 0 {
		return ErrSSHOff
	}
	if hostAllowed(allow, host) {
		return nil
	}
	return fmt.Errorf("o host %q não está na lista de hosts permitidos pelo operador (HANKO_ENV_SSH_ALLOW_HOSTS)", host)
}

// hostAllowed aplica as regras da lista: "*", host exato, sufixo e CIDR/IP (só para hosts informados como IP;
// nome nunca é resolvido aqui, porque o ssh resolveria de novo e o resultado poderia ser outro).
func hostAllowed(allow []string, host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h == "" {
		return false
	}
	ip := net.ParseIP(h)
	for _, rule := range allow {
		r := strings.ToLower(strings.TrimSpace(rule))
		switch {
		case r == "*":
			return true
		case strings.Contains(r, "/"):
			if _, n, err := net.ParseCIDR(r); err == nil && ip != nil && n.Contains(ip) {
				return true
			}
		case net.ParseIP(r) != nil:
			if ip != nil && net.ParseIP(r).Equal(ip) {
				return true
			}
		case strings.HasPrefix(r, "*."):
			if ip == nil && strings.HasSuffix(h, r[1:]) {
				return true
			}
		case strings.HasPrefix(r, "."):
			if ip == nil && strings.HasSuffix(h, r) {
				return true
			}
		default:
			if ip == nil && h == r {
				return true
			}
		}
	}
	return false
}

// RemoteURLStatus confere a URL pública que o harness remoto usa para alcançar o MCP: precisa existir e não
// pode ser loopback (o host remoto não alcança o "localhost" deste servidor).
func (c Config) RemoteURLStatus() error {
	u, err := url.Parse(strings.TrimSpace(c.RemoteURL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("sem URL pública configurada (HANKO_PUBLIC_URL): o harness remoto não teria como alcançar o MCP do Hanko")
	}
	h := u.Hostname()
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return errors.New("HANKO_PUBLIC_URL aponta para localhost: o harness remoto não alcança este servidor por ela")
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return errors.New("HANKO_PUBLIC_URL aponta para loopback: o harness remoto não alcança este servidor por ela")
	}
	return nil
}

func cleanList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
