package execenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// Layout no host remoto: <raiz>/<empresa>/<workspace>/<run>/{work,.hanko}. work é a cópia do workspace (o
// diretório de trabalho do harness) e .hanko guarda os auxiliares da run (config MCP, skills, pid).
const (
	remoteWork  = "work"
	remoteAux   = ".hanko"
	noWorkspace = "_sem-workspace"
	// sshOpTimeout limita cada operação de apoio (subir, descer, limpar, matar).
	sshOpTimeout = 10 * time.Minute
)

type sshSession struct {
	cfg      Config
	t        Target
	run      RunInfo
	tmp      string // diretório local da sessão: chave, known_hosts, HOME do ssh
	key, kh  string
	aux      string // auxiliares locais (sobem para <run>/.hanko)
	dir      string // <raiz>/<empresa>/<workspace>/<run>
	mu       sync.Mutex
	uploaded bool
	manifest map[string]bool
	closed   bool
}

// sshOpts afina a sessão SSH para o gateway de um provider cloud (ADR-0055): o host e o token só existem depois de
// criar o sandbox, o acesso é por token (sem chave privada) e quem libera o destino é o operador (HANKO_ENV_CLOUD),
// não a lista de hosts SSH.
type sshOpts struct{ gateway bool }

func openSSH(cfg Config, t Target, run RunInfo) (*sshSession, error) {
	return openSSHOpts(cfg, t, run, sshOpts{})
}

func openSSHOpts(cfg Config, t Target, run RunInfo, o sshOpts) (*sshSession, error) {
	c := t.SSH
	if !o.gateway {
		if err := cfg.SSHStatus(); err != nil {
			return nil, err
		}
		if err := c.Normalize(); err != nil {
			return nil, fmt.Errorf("configuração SSH do ambiente %q: %w", t.Name, err)
		}
		if err := cfg.CheckSSHHost(c.Host); err != nil {
			return nil, err
		}
		if err := domain.ValidPrivateKey(t.PrivateKey); err != nil {
			return nil, fmt.Errorf("ambiente %q sem chave privada utilizável: %w", t.Name, err)
		}
	} else if err := domain.ValidKnownHosts(c.KnownHosts); err != nil {
		return nil, fmt.Errorf("ambiente %q: known_hosts do gateway: %w", t.Name, err)
	}
	t.SSH = c
	tmp, err := localTemp("", "hanko-ssh-*")
	if err != nil {
		return nil, fmt.Errorf("ssh: diretório temporário: %w", err)
	}
	s := &sshSession{cfg: cfg, t: t, run: run, tmp: tmp, kh: filepath.Join(tmp, "known_hosts"), aux: filepath.Join(tmp, "aux")}
	if !o.gateway {
		s.key = filepath.Join(tmp, "id")
		key := t.PrivateKey
		if len(key) > 0 && key[len(key)-1] != '\n' {
			key += "\n" // o ssh recusa chave sem a quebra final
		}
		if err := os.WriteFile(s.key, []byte(key), 0o600); err != nil {
			s.Close()
			return nil, fmt.Errorf("ssh: gravar chave: %w", err)
		}
	}
	if err := os.WriteFile(s.kh, []byte(c.KnownHosts+"\n"), 0o600); err != nil {
		s.Close()
		return nil, fmt.Errorf("ssh: gravar known_hosts: %w", err)
	}
	if err := os.Mkdir(s.aux, 0o700); err != nil {
		s.Close()
		return nil, fmt.Errorf("ssh: diretório auxiliar: %w", err)
	}
	ws := run.Workspace
	if ws == "" {
		ws = noWorkspace
	}
	s.dir = path.Join(c.Root, run.Company, ws, run.RunID)
	return s, nil
}

// Args devolve o argv completo do ssh para executar remoteCmd no host (exportado para os testes de
// embrulho): sem arquivo de config do usuário, sem agente, sem encaminhamentos, chave e known_hosts só da
// sessão e verificação estrita da chave do host.
func (s *sshSession) Args(remoteCmd string) []string {
	c := s.t.SSH
	id := []string{"-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none"}
	if s.key != "" {
		id = []string{"-o", "IdentitiesOnly=yes", "-o", "IdentityFile=" + s.key, "-o", "IdentityAgent=none"}
	}
	args := []string{
		"-F", "none",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + s.kh,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "UpdateHostKeys=no",
	}
	args = append(args, id...)
	return append(args,
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=4",
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "PermitLocalCommand=no",
		"-o", "LogLevel=ERROR",
		"-T",
		"-p", strconv.Itoa(c.Port),
		"-l", c.User,
		"--", c.Host, remoteCmd,
	)
}

// localEnv é o ambiente do processo ssh local: só o PATH e um HOME descartável (nada do servidor vai junto).
func (s *sshSession) localEnv() []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.tmp, "LANG=C.UTF-8"}
}

func (s *sshSession) work() string   { return path.Join(s.dir, remoteWork) }
func (s *sshSession) auxDir() string { return path.Join(s.dir, remoteAux) }

func (s *sshSession) WorkDir() string                { return s.work() }
func (s *sshSession) AuxDir() (local, remote string) { return s.aux, s.auxDir() }
func (s *sshSession) LocalWorkDir() string           { return s.run.LocalDir }
func (s *sshSession) RemoteDir() string              { return s.dir }

func (s *sshSession) Describe() string {
	c := s.t.SSH
	return fmt.Sprintf("%s (ssh %s@%s:%d)", s.t.Name, c.User, c.Host, c.Port)
}

// exec roda um comando de apoio no host, com stdin opcional e stdout para out.
func (s *sshSession) exec(ctx context.Context, remoteCmd string, stdin io.Reader, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, sshOpTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.cfg.sshBin(), s.Args(remoteCmd)...) //nolint:gosec // argv montado aqui, sem shell local
	cmd.Env = s.localEnv()
	cmd.Stdin = stdin
	cmd.Stdout = out
	var errb bytes.Buffer
	cmd.Stderr = &limitedBuffer{b: &errb, max: 8 << 10}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(errb.Bytes())
		if len(msg) > 0 {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// upload sobe o workspace (na primeira vez) e os auxiliares para o host, num tar por stdin.
func (s *sshSession) upload(ctx context.Context) error {
	trees := []tree{{dir: s.aux, prefix: remoteAux}}
	withWork := !s.uploaded
	if withWork && s.run.LocalDir != "" {
		trees = append([]tree{{dir: s.run.LocalDir, prefix: remoteWork}}, trees...)
	}
	pr, pw := io.Pipe()
	var manifest map[string]bool
	werr := make(chan error, 1)
	go func() {
		m, err := writeTar(pw, trees)
		manifest = m
		_ = pw.CloseWithError(err)
		werr <- err
	}()
	cmd := "mkdir -p " + Quote(s.dir) + " && chmod 700 " + Quote(s.dir) + " && tar -C " + Quote(s.dir) + " -xpf -"
	err := s.exec(ctx, cmd, pr, io.Discard)
	_ = pr.Close()
	if e := <-werr; e != nil && !errors.Is(e, io.ErrClosedPipe) {
		return fmt.Errorf("empacotar o workspace: %w", e)
	}
	if err != nil {
		return fmt.Errorf("enviar o workspace para %s: %w", s.t.SSH.Host, err)
	}
	if withWork {
		s.uploaded, s.manifest = true, manifest
	}
	return nil
}

// Prepare sobe o workspace e os auxiliares e devolve o ssh que roda o harness no diretório da run: as
// variáveis vão num arquivo lido por "." e apagado em seguida, e o pid do shell fica em .hanko/pid para a
// limpeza de um cancelamento.
func (s *sshSession) Prepare(ctx context.Context, bin string, args []string, env map[string]string) (procrun.Invocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return procrun.Invocation{}, errors.New("sessão do ambiente encerrada")
	}
	if bin == "" {
		return procrun.Invocation{}, errors.New("comando vazio")
	}
	ef, err := envFile(mergeEnv(s.t.Env, env))
	if err != nil {
		return procrun.Invocation{}, err
	}
	envPath := filepath.Join(s.aux, "env.sh")
	if err := os.WriteFile(envPath, []byte(ef), 0o600); err != nil {
		return procrun.Invocation{}, fmt.Errorf("gravar variáveis: %w", err)
	}
	err = s.upload(ctx)
	_ = os.Remove(envPath) // o valor só existe no alvo, e lá é apagado logo depois de lido
	if err != nil {
		return procrun.Invocation{}, err
	}
	return procrun.Invocation{Bin: s.cfg.sshBin(), Args: s.Args(s.RemoteCommand(bin, args)), Env: s.localEnv()}, nil
}

// RemoteCommand é o comando que o shell do host remoto executa para o harness (exportado para os testes).
func (s *sshSession) RemoteCommand(bin string, args []string) string {
	aux := s.auxDir()
	cmd := "cd " + Quote(s.work()) +
		" && echo $$ > " + Quote(aux+"/pid") +
		" && set -a && . " + Quote(aux+"/env.sh") + " && set +a && rm -f " + Quote(aux+"/env.sh") +
		" && exec " + Quote(bin)
	if len(args) > 0 {
		cmd += " " + QuoteArgs(args)
	}
	return cmd
}

// Abort mata o grupo do harness no host (o processo remoto pode sobreviver ao fim do canal ssh, que não
// tem terminal): SIGTERM, 2 s, SIGKILL.
func (s *sshSession) Abort(ctx context.Context) {
	pid := Quote(s.auxDir() + "/pid")
	cmd := "if [ -f " + pid + " ]; then p=$(cat " + pid + "); kill -TERM -- -$p 2>/dev/null || kill -TERM $p 2>/dev/null; sleep 2; kill -KILL -- -$p 2>/dev/null || kill -KILL $p 2>/dev/null; fi; true"
	_ = s.exec(ctx, cmd, nil, io.Discard)
}

// Finish traz work/ de volta (tar pelo canal, extraído aqui com os.Root) e limpa o host: apaga o diretório da
// run quando ok, senão mantém work/ para diagnóstico e apaga só os auxiliares.
func (s *sshSession) Finish(ctx context.Context, ok bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.uploaded {
		return nil // nada subiu: o harness nem começou
	}
	var syncErr error
	if s.run.LocalDir != "" {
		pr, pw := io.Pipe()
		xerr := make(chan error, 1)
		go func() {
			err := extractTar(pr, s.run.LocalDir, s.manifest)
			if err == nil {
				// o tar remoto completa o último bloco com zeros depois do fim do arquivo; consome o resto
				// (com teto) para o ssh não falhar escrevendo num cano fechado
				_, _ = io.CopyN(io.Discard, pr, 1<<20)
			}
			_ = pr.CloseWithError(err)
			xerr <- err
		}()
		err := s.exec(ctx, "tar -C "+Quote(s.work())+" -cf - .", nil, pw)
		_ = pw.CloseWithError(err)
		switch e := <-xerr; {
		case e != nil && (err == nil || !errors.Is(e, err)):
			syncErr = fmt.Errorf("trazer o workspace de volta: %w", e)
		case err != nil:
			syncErr = fmt.Errorf("trazer o workspace de volta de %s: %w", s.t.SSH.Host, err)
		}
	}
	clean := "rm -rf " + Quote(s.dir)
	if !ok || syncErr != nil {
		clean = "rm -rf " + Quote(s.auxDir())
	}
	if err := s.exec(ctx, clean, nil, io.Discard); err != nil && syncErr == nil {
		return fmt.Errorf("limpar %s em %s: %w", s.dir, s.t.SSH.Host, err)
	}
	return syncErr
}

func (s *sshSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	_ = os.RemoveAll(s.tmp)
}

// limitedBuffer guarda até max bytes e descarta o resto.
type limitedBuffer struct {
	b   *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}
