// Package syscmd isola a execução de processos externos (pg_dump, pg_ctl, systemctl, launchctl...) atrás de uma
// interface pequena. O código de operação (backup, doctor, update, service, Postgres embutido) depende só da
// interface; os testes usam Fake e nunca chamam um binário de verdade.
package syscmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Cmd descreve uma execução. Env soma-se ao ambiente do processo (chave=valor); Stdout/Stderr nulos descartam.
type Cmd struct {
	Name   string
	Args   []string
	Env    []string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// String é o comando sem ambiente, para mensagens e logs (os argumentos nunca carregam segredos: senhas vão por Env).
func (c Cmd) String() string { return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " ")) }

// Runner executa processos e resolve executáveis.
type Runner interface {
	Run(ctx context.Context, c Cmd) error
	LookPath(name string) (string, error)
}

// ExitError acompanha o erro de um comando com o final do stderr (limitado), para diagnóstico.
type ExitError struct {
	Cmd    string
	Err    error
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s: %v", e.Cmd, e.Err)
	}
	return fmt.Sprintf("%s: %v: %s", e.Cmd, e.Err, e.Stderr)
}

func (e *ExitError) Unwrap() error { return e.Err }

const stderrTail = 2048

// OS executa de verdade (os/exec).
type OS struct{}

// LookPath procura o executável no PATH (ou valida um caminho).
func (OS) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Run executa o comando e espera. O stderr é capturado (os últimos 2 KiB entram no erro) além de ir para c.Stderr.
func (OS) Run(ctx context.Context, c Cmd) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...) //nolint:gosec // o chamador controla nome e argumentos; sem shell
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Dir = c.Dir
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	var tail bytes.Buffer
	if c.Stderr != nil {
		cmd.Stderr = io.MultiWriter(&tail, c.Stderr)
	} else {
		cmd.Stderr = &tail
	}
	if err := cmd.Run(); err != nil {
		s := strings.TrimSpace(tail.String())
		if len(s) > stderrTail {
			s = s[len(s)-stderrTail:]
		}
		return &ExitError{Cmd: c.String(), Err: err, Stderr: s}
	}
	return nil
}

// Call é uma chamada registrada pelo Fake.
type Call struct {
	Cmd Cmd
}

// Fake registra as chamadas e responde por um roteador; sem Handler todas dão certo sem saída.
type Fake struct {
	mu    sync.Mutex
	calls []Call
	// Handler decide o resultado de cada Run; pode escrever em c.Stdout. Nulo = sucesso.
	Handler func(c Cmd) error
	// Paths mapeia nome -> caminho para LookPath; ausente = não encontrado.
	Paths map[string]string
}

// Run registra e delega ao Handler.
func (f *Fake) Run(_ context.Context, c Cmd) error {
	f.mu.Lock()
	f.calls = append(f.calls, Call{Cmd: c})
	h := f.Handler
	f.mu.Unlock()
	if h == nil {
		return nil
	}
	return h(c)
}

// LookPath devolve o caminho de Paths.
func (f *Fake) LookPath(name string) (string, error) {
	if p, ok := f.Paths[name]; ok {
		return p, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// Calls devolve uma cópia das chamadas feitas.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}
