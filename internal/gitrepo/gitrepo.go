// Package gitrepo executa o git do sistema para os workspaces de execução (ADR-0054): clone espelho (bare) do
// repositório do projeto, worktree + branch por task e a leitura que a aba Git mostra (status, diff, log, árvore).
// Tudo é argv sem shell; o ambiente é mínimo (sem configuração do sistema ou do usuário, sem prompt de credencial,
// sem proxy herdado, sem hooks). A credencial do repositório entra só pelo ambiente do processo da busca
// (GIT_CONFIG_* ou GIT_SSH_COMMAND), nunca na linha de comando nem na configuração gravada no disco, e é removida
// das mensagens de erro. Destinos internos são recusados antes de qualquer conexão, como nas fontes de skills.
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/netguard"
)

// Padrões.
const (
	DefaultTimeout = 5 * time.Minute
	maxStderr      = 600
	maxGitOut      = 32 << 20
)

// Resolver resolve nomes para a checagem de destino.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options é a política do operador.
type Options struct {
	GitBin  string        // padrão "git" (PATH)
	Timeout time.Duration // teto de uma operação de rede; padrão 5 min
	// AllowFile aceita file:// (só testes; nunca exposto ao usuário).
	AllowFile bool
	// AllowPrivate libera hosts internos (HANKO_INTEGRATIONS_ALLOW_PRIVATE).
	AllowPrivate bool
	// Resolver resolve o host para a checagem de destino; nil = net.DefaultResolver.
	Resolver Resolver
}

// Git é o executor. É seguro para uso concorrente.
type Git struct {
	o        Options
	homeOnce sync.Once
	home     string
	homeErr  error
}

// New cria o executor.
func New(o Options) *Git {
	if o.GitBin == "" {
		o.GitBin = "git"
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	return &Git{o: o}
}

// Credential é a credencial já resolvida do repositório. O valor nunca é registrado.
type Credential struct {
	Value string
}

// IsSSHKey diz se a credencial é uma chave privada SSH (PEM); senão é um token HTTPS.
func (c Credential) IsSSHKey() bool { return strings.Contains(c.Value, "-----BEGIN") }

// ExitError é o erro de um git que terminou com código diferente de zero.
type ExitError struct {
	Code   int
	Cmd    string
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("git %s: código %d", e.Cmd, e.Code)
	}
	return fmt.Sprintf("git %s: %s", e.Cmd, e.Stderr)
}

// Remote é a forma reconhecida de um endereço de repositório.
type Remote struct {
	Raw    string
	Scheme string // https, ssh, file
	Host   string
}

var scpRe = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):([A-Za-z0-9._~/-]+)$`)

// ParseRemote reconhece https://, ssh://, git@host:dono/repo e (só com AllowFile) file:///caminho. Recusa
// credenciais embutidas, espaços, caracteres de controle e opções disfarçadas de endereço ("-o...").
func ParseRemote(raw string, o Options) (Remote, error) {
	r := strings.TrimSpace(raw)
	if r == "" {
		return Remote{}, errors.New("endereço do repositório vazio")
	}
	if len(r) > 500 {
		return Remote{}, errors.New("endereço longo demais")
	}
	for _, c := range r {
		if c <= ' ' || c == 0x7f {
			return Remote{}, errors.New("o endereço não pode ter espaços nem caracteres de controle")
		}
	}
	if strings.HasPrefix(r, "-") {
		return Remote{}, errors.New("endereço inválido")
	}
	if strings.HasPrefix(r, "file://") {
		if !o.AllowFile {
			return Remote{}, errors.New("use https:// ou ssh://")
		}
		u, err := url.Parse(r)
		if err != nil || u.Host != "" || !filepath.IsAbs(u.Path) {
			return Remote{}, errors.New("URL file:// inválida")
		}
		return Remote{Raw: r, Scheme: "file"}, nil
	}
	if m := scpRe.FindStringSubmatch(r); m != nil {
		if strings.HasPrefix(m[3], "-") {
			return Remote{}, errors.New("endereço inválido")
		}
		return Remote{Raw: r, Scheme: "ssh", Host: m[2]}, nil
	}
	u, err := url.Parse(r)
	if err != nil || u.Hostname() == "" {
		return Remote{}, errors.New("use https://, ssh:// ou git@host:dono/repo")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if _, err := netguard.ValidateURL(r, false, netguard.Policy{AllowPrivate: o.AllowPrivate}); err != nil {
			return Remote{}, err
		}
		if u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			return Remote{}, errors.New("a URL não pode ter query nem fragmento")
		}
		return Remote{Raw: r, Scheme: "https", Host: u.Hostname()}, nil
	case "ssh":
		if _, hasPw := u.User.Password(); hasPw {
			return Remote{}, errors.New("não ponha senha na URL")
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return Remote{}, errors.New("a URL não pode ter query nem fragmento")
		}
		return Remote{Raw: r, Scheme: "ssh", Host: u.Hostname()}, nil
	}
	return Remote{}, errors.New("use https://, ssh:// ou git@host:dono/repo")
}

// checkHost resolve o host e recusa destinos internos (o git conecta por conta própria, então a checagem é feita
// antes; há uma janela de rebinding de DNS entre ela e a conexão, registrada no ADR-0054).
func (g *Git) checkHost(ctx context.Context, rm Remote) error {
	if g.o.AllowPrivate || rm.Scheme == "file" || rm.Host == "" {
		return nil
	}
	if ip, err := netip.ParseAddr(rm.Host); err == nil {
		if netguard.BlockedIP(ip) {
			return netguard.ErrBlocked
		}
		return nil
	}
	lh := strings.TrimSuffix(strings.ToLower(rm.Host), ".")
	if lh == "localhost" || strings.HasSuffix(lh, ".localhost") || strings.HasSuffix(lh, ".internal") || strings.HasSuffix(lh, ".local") {
		return netguard.ErrBlocked
	}
	var res Resolver = net.DefaultResolver
	if g.o.Resolver != nil {
		res = g.o.Resolver
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, err := res.LookupNetIP(lctx, "ip", rm.Host)
	if err != nil {
		return fmt.Errorf("resolver %s: %w", rm.Host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolver %s: sem endereço", rm.Host)
	}
	for _, ip := range ips {
		if netguard.BlockedIP(ip) {
			return netguard.ErrBlocked
		}
	}
	return nil
}

func (g *Git) homeDir() (string, error) {
	g.homeOnce.Do(func() {
		g.home, g.homeErr = os.MkdirTemp("", "hanko-git-home-*") // 0700 por construção
	})
	return g.home, g.homeErr
}

// baseConfig é o -c de toda chamada: nada de hooks, helpers de credencial, monitor de sistema de arquivos nem
// submódulos. Os protocolos de rede só são liberados por chamada (allowProto).
func baseConfig(allowProto ...string) []string {
	args := []string{
		"-c", "protocol.allow=never", "-c", "http.followRedirects=false", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "credential.helper=", "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false",
		"-c", "core.askPass=", "-c", "gc.auto=0",
	}
	for _, p := range allowProto {
		args = append(args, "-c", "protocol."+p+".allow=always")
	}
	return args
}

// baseEnv é o ambiente mínimo do git.
func baseEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "LANG=C", "LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ASKPASS=",
		"SSH_ASKPASS=", "GIT_LFS_SKIP_SMUDGE=1", "GIT_PROTOCOL_FROM_USER=0",
	}
}

// call é uma chamada ao git.
type call struct {
	dir    string
	args   []string
	env    []string // acrescenta ao ambiente mínimo
	proto  []string // protocolos de rede liberados
	redact []string // valores removidos das mensagens de erro
}

// run executa e devolve a saída padrão. Código diferente de zero é *ExitError; o timeout vem do ctx.
func (g *Git) run(ctx context.Context, c call) ([]byte, error) {
	home, err := g.homeDir()
	if err != nil {
		return nil, fmt.Errorf("diretório temporário do git: %w", err)
	}
	var out, errb bytes.Buffer
	full := append(baseConfig(c.proto...), c.args...)
	res, err := procrun.Run(ctx, procrun.Options{Bin: g.o.GitBin, Args: full, Dir: c.dir,
		Env: append(baseEnv(home), c.env...), Grace: time.Second,
		OnStdout: func(l []byte) {
			if out.Len() < maxGitOut {
				out.Write(l)
				out.WriteByte('\n')
			}
		},
		OnStderr: func(l []byte) {
			if errb.Len() < 4096 {
				errb.Write(l)
				errb.WriteByte('\n')
			}
		}})
	if err != nil {
		return nil, fmt.Errorf("git: %w", scrub(err.Error(), c.redact))
	}
	if res.Cancelled {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, errors.New("git: interrompido")
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > maxStderr {
			msg = msg[len(msg)-maxStderr:]
		}
		cmd := "?"
		if len(c.args) > 0 {
			cmd = c.args[0]
		}
		return nil, &ExitError{Code: res.ExitCode, Cmd: cmd, Stderr: scrub(msg, c.redact).Error()}
	}
	b := out.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// scrub tira dos textos os valores secretos.
func scrub(msg string, secrets []string) error {
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return errors.New(msg)
}

// opCtx aplica o teto de tempo das operações de rede.
func (g *Git) opCtx(ctx context.Context, what string) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, g.o.Timeout, fmt.Errorf("tempo limite excedido (%s)", what))
}
