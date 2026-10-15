//go:build integration

// Package deploy tests the operational scripts under deploy/oute-server against a real Postgres,
// with a fake `docker compose` on PATH that forwards `exec postgres <cmd>` to the local server.
package deploy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeDocker = `#!/usr/bin/env bash
# docker compose -f F --project-directory D <sub> ...
[[ "$1" == compose ]] || { echo "fake docker: $*" >&2; exit 0; }
shift
while [[ "$1" == -f || "$1" == --project-directory ]]; do shift 2; done
sub="$1"; shift
case "$sub" in
  stop|up) echo "$sub $*" >> "$FAKE_LOG" ;;
  ps) ;;
  exec)
    shift 2 # -T postgres
    cmd="$1"; shift
    if [[ "$cmd" == pg_restore && "${FAKE_PG_RESTORE_FAIL:-}" == 1 && " $* " != *" -l "* ]]; then
      echo "fake pg_restore: falha induzida" >&2; exit 1
    fi
    exec "$cmd" "$@" ;;
esac
`

func psql(t *testing.T, dsn, _, q string) string {
	t.Helper()
	out, err := exec.Command("psql", "-X", "-At", "-d", dsn, "-c", q).CombinedOutput()
	if err != nil {
		t.Fatalf("psql %q: %v\n%s", q, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRestoreApplyKeepsOldDatabase(t *testing.T) {
	base := os.Getenv("HANKO_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definido")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	dsnFor := func(db string) string { c := *u; c.Path = "/" + db; return c.String() }
	admin := dsnFor("postgres")

	script, err := filepath.Abs("../../deploy/oute-server/restore.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	const db, src = "hanko_rt", "hanko_rt_src"
	for _, d := range []string{db, src} {
		psql(t, admin, "", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", d))
		psql(t, admin, "", "CREATE DATABASE "+d)
	}
	t.Cleanup(func() {
		for _, d := range strings.Fields(psql(t, admin, "", "select datname from pg_database where datname like 'hanko_rt%'")) {
			psql(t, admin, "", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", d))
		}
	})
	psql(t, dsnFor(db), db, "CREATE TABLE old_marker(x int)")
	psql(t, dsnFor(src), src, "CREATE TABLE new_marker(x int)")
	dump := filepath.Join(dir, "hanko-dump")
	if out, err := exec.Command("pg_dump", "-Fc", "-f", dump, dsnFor(src)).CombinedOutput(); err != nil {
		t.Fatalf("pg_dump: %v\n%s", err, out)
	}

	// /readyz stub
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	run := func(t *testing.T, extra ...string) (string, string, error) {
		t.Helper()
		log := filepath.Join(dir, "fake.log")
		_ = os.Remove(log)
		cmd := exec.Command("bash", append([]string{script, "--apply", "--yes", "--pgdump", dump}, extra...)...)
		host, port, _ := net.SplitHostPort(u.Host)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+log, "HANKO_APP_DIR="+dir,
			"HANKO_PG_USER="+u.User.Username(), "HANKO_PG_DB="+db, "HANKO_BACKUP_DIR="+dir,
			"PGHOST="+host, "PGPORT="+port, "PGPASSWORD="+pw,
			"HANKO_READYZ_URL=http://"+ln.Addr().String()+"/readyz", "HANKO_READYZ_WAIT=4")
		out, err := cmd.CombinedOutput()
		l, _ := os.ReadFile(log)
		return string(out), string(l), err
	}
	preRestore := func() []string {
		return strings.Fields(psql(t, admin, "", "select datname from pg_database where datname like 'hanko_rt_pre_restore_%'"))
	}

	// 1) pg_restore fails: the old database comes back under its name, the stack is brought up, nothing is left behind.
	t.Setenv("FAKE_PG_RESTORE_FAIL", "1")
	out, fakeLog, err := run(t)
	if err == nil {
		t.Fatalf("restore deveria falhar:\n%s", out)
	}
	if got := psql(t, dsnFor(db), db, "select to_regclass('old_marker') is not null"); got != "t" {
		t.Fatalf("banco antigo não voltou:\n%s", out)
	}
	if n := len(preRestore()); n != 0 {
		t.Fatalf("sobrou banco pre_restore: %v", preRestore())
	}
	if !strings.Contains(fakeLog, "up -d") {
		t.Fatalf("a stack deveria subir após a falha: %q\n%s", fakeLog, out)
	}

	// 2) success: new data under the original name, old one kept (never dropped) and a hint to drop it.
	t.Setenv("FAKE_PG_RESTORE_FAIL", "")
	out, fakeLog, err = run(t)
	if err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	if got := psql(t, dsnFor(db), db, "select to_regclass('new_marker') is not null and to_regclass('old_marker') is null"); got != "t" {
		t.Fatalf("banco restaurado errado:\n%s", out)
	}
	pre := preRestore()
	if len(pre) != 1 {
		t.Fatalf("pre_restore = %v\n%s", pre, out)
	}
	if got := psql(t, dsnFor(pre[0]), pre[0], "select to_regclass('old_marker') is not null"); got != "t" {
		t.Fatal("o banco antigo não foi preservado")
	}
	if !strings.Contains(out, "readyz ok") || !strings.Contains(out, "DROP DATABASE "+pre[0]) {
		t.Fatalf("faltou aviso de descarte após o readyz:\n%s", out)
	}
	if strings.Contains(fakeLog, "DROP") || !strings.Contains(fakeLog, "stop") {
		t.Fatalf("log do docker: %q", fakeLog)
	}
}
