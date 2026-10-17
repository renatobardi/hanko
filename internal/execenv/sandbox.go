package execenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
)

// No contêiner: o workspace em /work (leitura e escrita) e os auxiliares em /hanko (só leitura).
const (
	sandboxWork = "/work"
	sandboxAux  = "/hanko"
	// sandboxStage é a pasta, sob a raiz dos workspaces, dos diretórios de runs de sandbox que precisam ser
	// enxergados pelo host do docker (HANKO_ENV_SANDBOX_HOST_ROOT).
	sandboxStage = ".hanko-sandbox"
	dockerOpTime = 30 * time.Second
)

type sandboxSession struct {
	cfg     Config
	t       Target
	run     RunInfo
	stage   string // diretório local da sessão (removido em Close)
	aux     string
	workDir string // diretório local montado em /work
	mu      sync.Mutex
	started bool
	closed  bool
}

func openSandbox(cfg Config, t Target, run RunInfo) (*sandboxSession, error) {
	if err := cfg.SandboxStatus(); err != nil {
		return nil, err
	}
	c := t.Sandbox
	if err := c.Normalize(); err != nil {
		return nil, fmt.Errorf("configuração do sandbox %q: %w", t.Name, err)
	}
	t.Sandbox = c
	s := &sandboxSession{cfg: cfg, t: t, run: run, workDir: run.LocalDir}
	base := ""
	if cfg.SandboxHostRoot != "" {
		if cfg.WorkspacesRoot == "" {
			return nil, errors.New("HANKO_ENV_SANDBOX_HOST_ROOT exige HANKO_WORKSPACES_ROOT")
		}
		base = filepath.Join(cfg.WorkspacesRoot, sandboxStage)
	}
	stage, err := localTemp(base, "run-*")
	if err != nil {
		return nil, fmt.Errorf("sandbox: diretório da execução: %w", err)
	}
	s.stage = stage
	s.aux = filepath.Join(stage, "aux")
	if err := os.Mkdir(s.aux, 0o700); err != nil {
		s.Close()
		return nil, fmt.Errorf("sandbox: diretório auxiliar: %w", err)
	}
	if cfg.SandboxHostRoot != "" && (run.TempDir || !under(cfg.WorkspacesRoot, run.LocalDir)) {
		if !run.TempDir {
			s.Close()
			return nil, fmt.Errorf("o workspace %s não está sob HANKO_WORKSPACES_ROOT, então o host do docker não o enxerga", run.LocalDir)
		}
		// run sem workspace: o diretório temporário dela também precisa estar onde o host enxerga
		s.workDir = filepath.Join(stage, "work")
		if err := os.Mkdir(s.workDir, 0o700); err != nil {
			s.Close()
			return nil, fmt.Errorf("sandbox: diretório de trabalho: %w", err)
		}
	}
	for _, p := range []string{s.workDir, s.aux} {
		if strings.ContainsAny(p, ",\n") {
			s.Close()
			return nil, fmt.Errorf("caminho não suportado num volume do sandbox: %q", p)
		}
	}
	return s, nil
}

func (s *sandboxSession) name() string { return "hanko-run-" + s.run.RunID }

// hostPath traduz um caminho local para o do host do docker (HANKO_ENV_SANDBOX_HOST_ROOT).
func (s *sandboxSession) hostPath(p string) string {
	if s.cfg.SandboxHostRoot == "" {
		return p
	}
	rel, err := filepath.Rel(s.cfg.WorkspacesRoot, p)
	if err != nil {
		return p
	}
	return filepath.Join(s.cfg.SandboxHostRoot, rel)
}

func (s *sandboxSession) WorkDir() string                { return sandboxWork }
func (s *sandboxSession) AuxDir() (local, remote string) { return s.aux, sandboxAux }
func (s *sandboxSession) LocalWorkDir() string           { return s.workDir }
func (s *sandboxSession) RemoteDir() string              { return s.name() }

func (s *sandboxSession) Describe() string {
	return fmt.Sprintf("%s (sandbox %s, rede %s)", s.t.Name, s.t.Sandbox.Image, s.t.Sandbox.Network)
}

// RunArgs devolve o argv do docker para rodar bin/args no contêiner da run (exportado para os testes): sem
// capacidades, sem novos privilégios, raiz só leitura, /tmp em memória, limites de memória, CPU e processos,
// usuário deste servidor (os arquivos do workspace continuam dele) e as variáveis só por nome.
func (s *sandboxSession) RunArgs(bin string, args []string, envNames []string) []string {
	c := s.t.Sandbox
	out := []string{
		"run", "--rm", "-i",
		"--name", s.name(),
		"--label", "hanko.run=" + s.run.RunID,
		"--network", c.Network,
		"--memory", strconv.Itoa(c.MemoryMiB) + "m",
		"--memory-swap", strconv.Itoa(c.MemoryMiB) + "m",
		"--cpus", strconv.FormatFloat(c.CPUs, 'f', -1, 64),
		"--pids-limit", strconv.Itoa(c.PIDsLimit),
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--read-only",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=512m",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--mount", "type=bind,src=" + s.hostPath(s.workDir) + ",dst=" + sandboxWork,
		"--mount", "type=bind,src=" + s.hostPath(s.aux) + ",dst=" + sandboxAux + ",readonly",
		"-w", sandboxWork,
		"-e", "HOME=/tmp",
	}
	for _, n := range envNames {
		out = append(out, "-e", n)
	}
	out = append(out, c.Image, bin)
	return append(out, args...)
}

// containerEnv separa as variáveis que entram no contêiner (só nomes no argv; os valores vão no ambiente
// do processo docker) das que ficam com ele: PATH, HOME e DOCKER_* não atravessam.
func containerEnv(env map[string]string) (names []string, values []string) {
	for k, v := range env {
		if k == "PATH" || k == "HOME" || strings.HasPrefix(k, "DOCKER_") {
			continue
		}
		names = append(names, k)
		values = append(values, k+"="+v)
	}
	sort.Strings(names)
	sort.Strings(values)
	return names, values
}

// dockerEnv é o ambiente do processo docker local: o necessário para falar com o daemon.
func dockerEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "PATH" || k == "HOME" || k == "XDG_RUNTIME_DIR" || strings.HasPrefix(k, "DOCKER_") ||
			k == "HTTPS_PROXY" || k == "HTTP_PROXY" || k == "NO_PROXY" {
			out = append(out, kv)
		}
	}
	return out
}

func (s *sandboxSession) Prepare(_ context.Context, bin string, args []string, env map[string]string) (procrun.Invocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return procrun.Invocation{}, errors.New("sessão do ambiente encerrada")
	}
	if bin == "" {
		return procrun.Invocation{}, errors.New("comando vazio")
	}
	names, values := containerEnv(mergeEnv(s.t.Env, env))
	s.started = true
	return procrun.Invocation{Bin: s.cfg.dockerBin(), Args: s.RunArgs(bin, args, names), Env: append(dockerEnv(), values...)}, nil
}

// Abort mata o contêiner da run (matar o processo docker local não para o contêiner).
func (s *sandboxSession) Abort(ctx context.Context) {
	_ = s.docker(ctx, dockerOpTime, nil, "kill", s.name())
}

func (s *sandboxSession) docker(ctx context.Context, timeout time.Duration, out io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.cfg.dockerBin(), args...) //nolint:gosec // argv montado aqui, sem shell
	cmd.Env = dockerEnv()
	if out == nil {
		out = io.Discard
	}
	cmd.Stdout = out
	var errb bytes.Buffer
	cmd.Stderr = &limitedBuffer{b: &errb, max: 4 << 10}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Run(); err != nil {
		if m := bytes.TrimSpace(errb.Bytes()); len(m) > 0 {
			return fmt.Errorf("%w: %s", err, m)
		}
		return err
	}
	return nil
}

// Finish garante que o contêiner não ficou para trás (--rm já o remove quando o processo termina).
func (s *sandboxSession) Finish(ctx context.Context, _ bool) error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if started {
		_ = s.docker(ctx, dockerOpTime, nil, "rm", "-f", s.name())
	}
	return nil
}

func (s *sandboxSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.stage != "" {
		_ = os.RemoveAll(s.stage)
	}
}
