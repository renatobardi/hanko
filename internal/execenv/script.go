package execenv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// ProbeLocalTimeout limita um script curto no servidor.
const ProbeLocalTimeout = time.Minute

// RunScript executa um script POSIX curto no ambiente de t (local, ssh, sandbox ou cloud) e devolve o stdout. É a
// base do teste de conexão e do readiness da persona (ADR-0055): as variáveis de env entram no ambiente do
// script, nunca na linha de comando (no alvo remoto o script e as variáveis vão por stdin: "sh -s"). O stderr
// do alvo só aparece no erro, truncado.
func RunScript(ctx context.Context, cfg Config, t Target, script string, env map[string]string) ([]byte, error) {
	var out bytes.Buffer
	w := scriptOut(&out)
	merged := mergeEnv(t.Env, env)
	switch t.Driver {
	case domain.EnvLocal, "":
		err := runLocalScript(ctx, cfg, script, merged, w)
		return out.Bytes(), err
	case domain.EnvSSH:
		s, err := openSSH(cfg, t, RunInfo{RunID: "probe", Company: "probe"})
		if err != nil {
			return nil, err
		}
		defer s.Close()
		in, err := stdinScript(merged, script)
		if err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(ctx, ProbeSSHTimeout)
		defer cancel()
		err = s.exec(cctx, "sh -s", in, w)
		return out.Bytes(), err
	case domain.EnvSandbox:
		s, err := openSandbox(cfg, t, RunInfo{RunID: "probe", Company: "probe", LocalDir: os.TempDir(), TempDir: true})
		if err != nil {
			return nil, err
		}
		defer s.Close()
		in, err := stdinScript(merged, script)
		if err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(ctx, ProbeSandboxTimeout)
		defer cancel()
		c := s.t.Sandbox
		err = s.dockerStdin(cctx, in, w, "run", "--rm", "-i", "--network", c.Network, "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
			"--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m", "--memory", fmt.Sprint(c.MemoryMiB)+"m", "--pids-limit", fmt.Sprint(c.PIDsLimit),
			"-e", "HOME=/tmp", "--entrypoint", "sh", c.Image, "-s")
		return out.Bytes(), err
	case domain.EnvCloud:
		in, err := stdinScript(merged, script)
		if err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(ctx, ProbeCloudTimeout)
		defer cancel()
		err = withCloudBox(cctx, cfg, t, func(inner *sshSession) error { return inner.exec(cctx, "sh -s", in, w) })
		return out.Bytes(), err
	}
	return nil, fmt.Errorf("driver %q não executa scripts", t.Driver)
}

// stdinScript junta as variáveis (export, aspas simples) e o script num único stdin para "sh -s".
func stdinScript(env map[string]string, script string) (io.Reader, error) {
	ef, err := envFile(env)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(ef + script + "\n"), nil
}

// StdinDocker é o que um DockerClient oferece a mais para rodar um script por stdin (o cliente padrão e o fake dos
// testes implementam).
type StdinDocker interface {
	DoStdin(ctx context.Context, timeout time.Duration, stdin io.Reader, out io.Writer, args ...string) error
}

func (s *sandboxSession) dockerStdin(ctx context.Context, stdin io.Reader, out io.Writer, args ...string) error {
	c, ok := s.cfg.dockerClient().(StdinDocker)
	if !ok {
		return fmt.Errorf("o cliente docker não executa scripts")
	}
	return c.DoStdin(ctx, ProbeSandboxTimeout, stdin, out, args...)
}

// DoStdin é o Do do cliente padrão com stdin.
func (d cliDocker) DoStdin(ctx context.Context, timeout time.Duration, stdin io.Reader, out io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.bin, args...) //nolint:gosec // argv montado aqui, sem shell
	cmd.Env = dockerEnv()
	cmd.Stdin = stdin
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

// runLocalScript roda o script com "sh -s" neste servidor, com o ambiente que o harness local receberia.
func runLocalScript(ctx context.Context, cfg Config, script string, env map[string]string, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, ProbeLocalTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-s") //nolint:gosec // argv fixo; o script vai por stdin
	cmd.Env = procrun.HarnessEnv(os.Environ(), cfg.AgentEnvAllow, env)
	cmd.Stdin = strings.NewReader(script + "\n")
	cmd.Stdout = out
	var errb bytes.Buffer
	cmd.Stderr = &limitedBuffer{b: &errb, max: 4 << 10}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Run(); err != nil {
		if m := bytes.TrimSpace(errb.Bytes()); len(m) > 0 {
			return fmt.Errorf("%w: %s", err, m)
		}
		return err
	}
	return nil
}

// probeLocal testa o local (o servidor): sistema e binários, sem escrever nada.
func probeLocal(ctx context.Context, cfg Config, _ Target) domain.ProbeReport {
	var rep domain.ProbeReport
	out, err := RunScript(ctx, cfg, Target{Driver: domain.EnvLocal}, probeScript(""), nil)
	if err != nil && len(out) == 0 {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "shell", OK: false, Detail: err.Error()})
		return rep
	}
	rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "shell", OK: true, Detail: "sh"})
	parseProbe(out, &rep, "")
	return rep
}
