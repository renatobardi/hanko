package syscmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOSRunCapturesOutputAndStderrTail(t *testing.T) {
	var out bytes.Buffer
	if err := (OS{}).Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo oi; echo $HANKO_T"}, Env: []string{"HANKO_T=x"}, Stdout: &out}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(out.String()); len(got) != 2 || got[0] != "oi" || got[1] != "x" {
		t.Fatalf("saída = %q", out.String())
	}
	err := (OS{}).Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo falhou >&2; exit 3"}})
	var ee *ExitError
	if !errors.As(err, &ee) || !strings.Contains(ee.Stderr, "falhou") || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("erro = %v", err)
	}
}

func TestFakeRecordsAndLooksUp(t *testing.T) {
	f := &Fake{Paths: map[string]string{"pg_dump": "/usr/bin/pg_dump"}}
	if err := f.Run(context.Background(), Cmd{Name: "x", Args: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls()) != 1 || f.Calls()[0].Cmd.String() != "x a" {
		t.Fatalf("calls = %+v", f.Calls())
	}
	if p, err := f.LookPath("pg_dump"); err != nil || p != "/usr/bin/pg_dump" {
		t.Fatal(p, err)
	}
	if _, err := f.LookPath("nada"); err == nil {
		t.Fatal("esperava erro")
	}
}
