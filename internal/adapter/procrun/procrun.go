// Package procrun runs an agent harness as a child process: prompt on stdin,
// line-oriented stdout/stderr callbacks, and process-group termination on cancel.
package procrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// MaxLine is the largest stdout/stderr line kept whole; longer lines are truncated.
const MaxLine = 16 << 20

// DefaultGrace is how long a process group gets between SIGTERM and SIGKILL.
const DefaultGrace = 5 * time.Second

// Options describes one process execution.
type Options struct {
	Bin   string
	Args  []string
	Dir   string
	Env   []string // full environment; nil = HarnessEnv(os.Environ()) (allowlist, sem HANKO_*)
	Stdin string
	// OnStdin, when set, switches to interactive mode: it is called once after the process
	// starts with the pipe to the child's stdin and Stdin is ignored. The caller owns writes
	// and must close the pipe to signal EOF (it is closed anyway when the process exits).
	OnStdin func(io.WriteCloser)
	// Grace between SIGTERM and SIGKILL on cancellation; 0 = DefaultGrace.
	Grace time.Duration
	// OnStdout / OnStderr receive one line each (without the newline). They are never
	// called concurrently with each other, so callers may share state between them.
	OnStdout func(line []byte)
	OnStderr func(line []byte)
}

// Result of a finished process.
type Result struct {
	ExitCode  int
	Cancelled bool // ctx ended before the process exited on its own
}

// Run starts the process and blocks until it exits. A non-zero exit is not an error;
// failure to start is. If ctx ends first the process group is terminated and
// Result.Cancelled is set (the error is nil; the caller inspects ctx).
func Run(ctx context.Context, o Options) (Result, error) {
	cmd := exec.Command(o.Bin, o.Args...) //nolint:gosec // executar o CLI do harness configurado é o propósito do adapter; argv sem shell
	cmd.Dir = o.Dir
	if o.Env != nil {
		cmd.Env = o.Env
	} else {
		cmd.Env = HarnessEnv(os.Environ(), nil, nil)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdinPipe io.WriteCloser
	if o.OnStdin != nil {
		p, err := cmd.StdinPipe()
		if err != nil {
			return Result{ExitCode: -1}, fmt.Errorf("stdin %s: %w", o.Bin, err)
		}
		stdinPipe = p
	} else {
		cmd.Stdin = bytes.NewReader([]byte(o.Stdin))
	}

	var mu sync.Mutex
	cmd.Stdout = &lineWriter{mu: &mu, fn: o.OnStdout}
	cmd.Stderr = &lineWriter{mu: &mu, fn: o.OnStderr}
	cmd.WaitDelay = 3 * time.Second

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("start %s: %w", o.Bin, err)
	}
	pgid := cmd.Process.Pid
	if stdinPipe != nil {
		o.OnStdin(stdinPipe)
	}

	done := make(chan struct{})
	var cancelled bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		cancelled = true
		grace := o.Grace
		if grace <= 0 {
			grace = DefaultGrace
		}
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		t := time.NewTimer(grace)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
		}
		// Always sweep the group: SIGTERM may have stopped the leader but not its children.
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()

	waitErr := cmd.Wait()
	close(done)
	wg.Wait()
	// Leader is gone; make sure no stragglers keep running. Not only after a cancel: a child the leader
	// started (an stdio MCP server, a helper) can outlive a normal exit, and nobody else would reap it.
	// While any member lives the pgid cannot be reused, so the signal cannot reach an unrelated group.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	flush(cmd.Stdout, cmd.Stderr)

	res := Result{Cancelled: cancelled, ExitCode: cmd.ProcessState.ExitCode()}
	var ee *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &ee) && !errors.Is(waitErr, exec.ErrWaitDelay) {
		return res, fmt.Errorf("wait %s: %w", o.Bin, waitErr)
	}
	return res, nil
}

func flush(ws ...io.Writer) {
	for _, w := range ws {
		if lw, ok := w.(*lineWriter); ok {
			lw.flush()
		}
	}
}

// lineWriter splits writes into lines and invokes fn under the shared mutex.
type lineWriter struct {
	mu   *sync.Mutex
	fn   func([]byte)
	buf  []byte
	over bool // current line exceeded MaxLine; rest is dropped until newline
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.add(p)
			break
		}
		w.add(p[:i])
		w.emit()
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) add(p []byte) {
	if w.over {
		return
	}
	if len(w.buf)+len(p) > MaxLine {
		p = p[:MaxLine-len(w.buf)]
		w.over = true
	}
	w.buf = append(w.buf, p...)
}

func (w *lineWriter) emit() {
	line := bytes.TrimSuffix(w.buf, []byte("\r"))
	if w.fn != nil {
		w.mu.Lock()
		w.fn(line)
		w.mu.Unlock()
	}
	w.buf = w.buf[:0]
	w.over = false
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit()
	}
}
