package acp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPolicyCheck(t *testing.T) {
	if err := (Policy{}).Check("meu-agente"); err == nil || !strings.Contains(err.Error(), "HANKO_ACP_ALLOW") {
		t.Fatalf("o padrão recusa: %v", err)
	}
	if err := (Policy{Allow: true}).Check("/bin/sh"); err != nil {
		t.Fatalf("liberado sem lista: %v", err)
	}
	p := Policy{Allow: true, Commands: []string{"meu-agente", "/opt/agentes/outro", " "}}
	for cmd, ok := range map[string]bool{
		"meu-agente":                    true,
		"/opt/agentes/outro":            true,
		"/opt/agentes/../agentes/outro": true,
		"/bin/sh":                       false,
		"sh":                            false,
		"/usr/bin/meu-agente":           false, // nome simples na lista não vale para outro caminho
		"./meu-agente":                  false,
		"sub/meu-agente":                false,
		"outro":                         false,
	} {
		err := p.Check(cmd)
		if (err == nil) != ok {
			t.Errorf("Check(%q) = %v, quer ok=%v", cmd, err, ok)
		}
		if err != nil && !strings.Contains(err.Error(), "HANKO_ACP_COMMANDS") {
			t.Errorf("Check(%q): motivo sem a variável: %v", cmd, err)
		}
	}
}

// Um FIFO (ou device) dentro do workspace travaria o open e, com ele, o readLoop do agente.
func TestFileRootRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skipf("sem mkfifo: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := newFileRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	type result struct {
		op  string
		err error
	}
	done := make(chan result, 4)
	go func() {
		_, err := f.Read(filepath.Join(dir, "pipe"), 0, 0)
		done <- result{"read fifo", err}
		_, err = f.Write(filepath.Join(dir, "pipe"), "x")
		done <- result{"write fifo", err}
		_, err = f.Read("/dev/null", 0, 0) // fora da raiz: recusado sem abrir
		done <- result{"read /dev/null", err}
		_, err = f.Read(filepath.Join(dir, "sub"), 0, 0)
		done <- result{"read dir", err}
	}()
	for i := 0; i < 4; i++ {
		select {
		case r := <-done:
			if r.err == nil {
				t.Errorf("%s: deveria ser recusado", r.op)
			}
			if r.op != "read /dev/null" && !errors.Is(r.err, errNotRegular) {
				t.Errorf("%s: err = %v, quer 'not a regular file'", r.op, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("travou abrindo arquivo não regular")
		}
	}
	if _, err := f.Write(filepath.Join(dir, "ok.txt"), "oi\n"); err != nil {
		t.Fatalf("arquivo comum: %v", err)
	}
	if _, err := f.Write(filepath.Join(dir, "ok.txt"), "x"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Read(filepath.Join(dir, "ok.txt"), 0, 0); err != nil || got != "x" {
		t.Fatalf("sobrescrever trunca: %q %v", got, err)
	}
}

func TestRunRefusedByOperatorPolicy(t *testing.T) {
	spec := fakeSpec(t, "happy", nil, nil)
	for name, pol := range map[string]Policy{
		"desligado":     {},
		"fora da lista": {Allow: true, Commands: []string{"outro-agente"}},
	} {
		a := New(Options{Grace: 300 * time.Millisecond, Policy: pol})
		col := &collector{}
		res, err := a.Run(context.Background(), spec, col)
		if err == nil || !res.IsError || res.ExitCode != -1 || !strings.Contains(res.Error, "HANKO_ACP_") {
			t.Errorf("%s: err=%v res=%+v", name, err, res)
		}
		if n := len(col.all()); n != 0 {
			t.Errorf("%s: o agente não pode ter sido iniciado (%d eventos)", name, n)
		}
	}
}
