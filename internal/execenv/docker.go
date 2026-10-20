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
)

// DockerClient é o cliente do motor docker que o driver sandbox usa (ADR-0055). A implementação padrão chama o
// binário `docker` do servidor (por argv, sem shell); os testes trocam por um fake que não precisa de daemon.
// A interface é estreita de propósito: tudo que o driver precisa é montar o comando da run, matar/remover o
// contêiner e fazer as perguntas curtas do teste de conexão.
type DockerClient interface {
	// Available diz se o cliente pode falar com um motor (nil = pode); o motivo vai para a tela do operador.
	Available() error
	// Command devolve o processo local que executa `docker args...` com containerEnv (KEY=VALUE) só no ambiente do
	// processo: os valores nunca vão para o argv. É esse processo que o procrun roda e cujo stdio é o do harness.
	Command(args []string, containerEnv []string) procrun.Invocation
	// Do executa uma operação curta (version, kill, rm, run de teste) e escreve o stdout em out.
	Do(ctx context.Context, timeout time.Duration, out io.Writer, args ...string) error
}

// cliDocker é o DockerClient que chama o binário docker.
type cliDocker struct {
	bin      string
	lookPath func(string) (string, error)
}

func (d cliDocker) Available() error {
	lp := d.lookPath
	if lp == nil {
		lp = exec.LookPath
	}
	if _, err := lp(d.bin); err != nil {
		return fmt.Errorf("o binário %q não está no PATH do servidor", d.bin)
	}
	return nil
}

func (d cliDocker) Command(args []string, containerEnv []string) procrun.Invocation {
	return procrun.Invocation{Bin: d.bin, Args: args, Env: append(dockerEnv(), containerEnv...)}
}

func (d cliDocker) Do(ctx context.Context, timeout time.Duration, out io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.bin, args...) //nolint:gosec // argv montado aqui, sem shell
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
