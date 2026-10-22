package embeddedpg

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/syscmd"
)

// fakePG simula initdb e pg_ctl sem Postgres: initdb cria PG_VERSION; start/stop mudam um estado em memória.
type fakePG struct {
	*syscmd.Fake
	running bool
	version string
}

func newFakePG(t *testing.T) (*fakePG, Options) {
	t.Helper()
	f := &fakePG{version: "pg_ctl (PostgreSQL) 17.2"}
	f.Fake = &syscmd.Fake{
		Paths: map[string]string{"initdb": "/usr/bin/initdb", "pg_ctl": "/usr/bin/pg_ctl"},
		Handler: func(c syscmd.Cmd) error {
			switch {
			case strings.HasSuffix(c.Name, "initdb"):
				for i, a := range c.Args {
					if a == "-D" {
						if err := os.MkdirAll(c.Args[i+1], 0o700); err != nil {
							return err
						}
						return os.WriteFile(filepath.Join(c.Args[i+1], "PG_VERSION"), []byte("17\n"), 0o600)
					}
				}
			case strings.HasSuffix(c.Name, "pg_ctl"):
				last := c.Args[len(c.Args)-1]
				switch last {
				case "--version":
					_, _ = io.WriteString(c.Stdout, f.version+"\n")
				case "status":
					if !f.running {
						return errors.New("exit status 3")
					}
				case "start":
					f.running = true
				case "stop":
					f.running = false
				}
			}
			return nil
		},
	}
	o := Options{Dir: filepath.Join(t.TempDir(), "pg"), Cmd: f, Euid: func() int { return 1000 }, GOOS: "linux",
		Glob:     func(string) ([]string, error) { return nil, nil },
		FreePort: func() (int, error) { return 54329, nil },
		EnsureDB: func(context.Context, string, string) error { return nil }}
	return f, o
}

func argsOf(f *syscmd.Fake, name string) [][]string {
	var out [][]string
	for _, c := range f.Calls() {
		if strings.HasSuffix(c.Cmd.Name, name) {
			out = append(out, c.Cmd.Args)
		}
	}
	return out
}

func TestStartInitializesOnceAndStops(t *testing.T) {
	f, o := newFakePG(t)
	ctx := context.Background()
	in, err := Start(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if !f.running || in.Port != 54329 || in.Reused {
		t.Fatalf("estado: running=%v inst=%+v", f.running, in)
	}
	if !strings.HasPrefix(in.DSN, "postgres://hanko:") || !strings.HasSuffix(in.DSN, "@127.0.0.1:54329/hanko?sslmode=disable") {
		t.Fatalf("DSN = %s", in.DSN)
	}
	// a senha vai por arquivo, nunca em argv
	pw := readTrim(filepath.Join(o.Dir, "password"))
	if len(pw) < 30 {
		t.Fatalf("senha curta: %q", pw)
	}
	for _, c := range f.Calls() {
		if strings.Contains(c.Cmd.String(), pw) {
			t.Fatalf("senha em argv: %s", c.Cmd)
		}
	}
	initArgs := strings.Join(argsOf(f.Fake, "initdb")[0], " ")
	for _, want := range []string{"--auth=scram-sha-256", "--pwfile=" + filepath.Join(o.Dir, "password"), "--encoding=UTF8", "--locale=C.UTF-8"} {
		if !strings.Contains(initArgs, want) {
			t.Errorf("initdb sem %q: %s", want, initArgs)
		}
	}
	if st, _ := os.Stat(filepath.Join(o.Dir, "password")); st.Mode().Perm() != 0o600 {
		t.Errorf("modo da senha %v", st.Mode())
	}
	startArgs := strings.Join(argsOf(f.Fake, "pg_ctl")[len(argsOf(f.Fake, "pg_ctl"))-1], " ")
	if !strings.Contains(startArgs, "listen_addresses=127.0.0.1") || !strings.Contains(startArgs, "-p 54329") {
		t.Errorf("start sem loopback/porta: %s", startArgs)
	}

	if err := in.Stop(ctx); err != nil || f.running {
		t.Fatalf("stop: %v running=%v", err, f.running)
	}

	// segunda execução: sem initdb, mesma senha e mesma porta
	in2, err := Start(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(argsOf(f.Fake, "initdb")) != 1 || in2.DSN != in.DSN {
		t.Fatalf("initdb=%d dsn2=%s", len(argsOf(f.Fake, "initdb")), in2.DSN)
	}
}

func TestStartReusesRunningClusterAndDoesNotStopIt(t *testing.T) {
	f, o := newFakePG(t)
	ctx := context.Background()
	first, err := Start(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Start(ctx, o) // outro processo hanko, mesmo cluster
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.DSN != first.DSN {
		t.Fatalf("second = %+v", second)
	}
	if err := second.Stop(ctx); err != nil || !f.running {
		t.Fatalf("reaproveitado não pode ser parado: err=%v running=%v", err, f.running)
	}
	if err := first.Stop(ctx); err != nil || f.running {
		t.Fatal("dono deve parar")
	}
}

func TestStartRefusesRoot(t *testing.T) {
	_, o := newFakePG(t)
	o.Euid = func() int { return 0 }
	if _, err := Start(context.Background(), o); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartMissingBinariesExplainsHowToInstall(t *testing.T) {
	f, o := newFakePG(t)
	f.Paths = map[string]string{}
	_, err := Start(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "sem baixar nada") || !strings.Contains(err.Error(), "--pg-bin-dir") {
		t.Fatalf("err = %v", err)
	}
	if len(f.Calls()) != 0 {
		t.Fatal("não deveria ter executado nada")
	}
}

func TestStartRejectsOldPostgres(t *testing.T) {
	f, o := newFakePG(t)
	f.version = "pg_ctl (PostgreSQL) 12.9"
	if _, err := Start(context.Background(), o); err == nil || !strings.Contains(err.Error(), "antigo demais") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindBinariesUsesBinDirAndGlobbedDirs(t *testing.T) {
	f, o := newFakePG(t)
	f.Paths = map[string]string{"/opt/pg/bin/initdb": "/opt/pg/bin/initdb", "/opt/pg/bin/pg_ctl": "/opt/pg/bin/pg_ctl"}
	o.BinDir = "/opt/pg/bin"
	b, err := FindBinaries(context.Background(), o)
	if err != nil || b.PgCtl != "/opt/pg/bin/pg_ctl" || b.Major != 17 {
		t.Fatalf("%+v %v", b, err)
	}
	// BinDir explícito não cai no PATH
	o.BinDir = "/outro"
	if _, err := FindBinaries(context.Background(), o); err == nil {
		t.Fatal("esperava erro")
	}
	// glob de instalações do sistema: a mais nova primeiro
	f.Paths = map[string]string{"/usr/lib/postgresql/17/bin/initdb": "x", "/usr/lib/postgresql/17/bin/pg_ctl": "x", "/usr/lib/postgresql/15/bin/initdb": "x", "/usr/lib/postgresql/15/bin/pg_ctl": "x"}
	o.BinDir = ""
	o.Glob = func(pat string) ([]string, error) {
		if strings.HasPrefix(pat, "/usr/lib/postgresql") {
			return []string{"/usr/lib/postgresql/15/bin", "/usr/lib/postgresql/17/bin"}, nil
		}
		return nil, nil
	}
	b, err = FindBinaries(context.Background(), o)
	if err != nil || b.PgCtl != "/usr/lib/postgresql/17/bin/pg_ctl" {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestEnsureDBFailureStopsOwnedCluster(t *testing.T) {
	f, o := newFakePG(t)
	o.EnsureDB = func(context.Context, string, string) error { return errors.New("auth falhou") }
	if _, err := Start(context.Background(), o); err == nil || !strings.Contains(err.Error(), "auth falhou") {
		t.Fatalf("err = %v", err)
	}
	if f.running {
		t.Fatal("deveria ter parado o cluster que subiu")
	}
}

func TestStartInitdbFailure(t *testing.T) {
	f, o := newFakePG(t)
	base := f.Handler
	f.Handler = func(c syscmd.Cmd) error {
		if strings.HasSuffix(c.Name, "initdb") {
			return errors.New("locale inexistente")
		}
		return base(c)
	}
	if _, err := Start(context.Background(), o); err == nil || !strings.Contains(err.Error(), "initdb") {
		t.Fatalf("err = %v", err)
	}
}

func TestDarwinDefaultsLocale(t *testing.T) {
	o := Options{GOOS: "darwin"}
	o.defaults()
	if o.Locale != "en_US.UTF-8" {
		t.Fatal(o.Locale)
	}
}

func TestFreePortReal(t *testing.T) {
	p, err := freePort()
	if err != nil || p == 0 || !portFree(p) {
		t.Fatalf("p=%d err=%v", p, err)
	}
}
