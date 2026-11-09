//go:build integration

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

// Banco externo (ADR-0092): o backup.sh e o restore.sh leem a HANKO_DATABASE_URL do .env, falam com o banco por um
// cliente descartável (aqui, o docker falso roda o binário local) e só tocam no schema corrente da conexão.
func TestBackupAndRestoreWithExternalSchema(t *testing.T) {
	base := os.Getenv("HANKO_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definido")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	dsnFor := func(db string) string { c := *u; c.Path = "/" + db; return c.String() }
	admin := dsnFor("postgres")
	requirePGClient(t, admin)

	const db, schema = "hanko_ext", "hanko_ext_prd"
	psql(t, admin, "", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", db))
	psql(t, admin, "", "CREATE DATABASE "+db)
	t.Cleanup(func() { psql(t, admin, "", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", db)) })
	// o schema corrente vem do search_path fixado no banco (no oute-supabase-db, no papel)
	psql(t, dsnFor(db), db, "CREATE SCHEMA "+schema)
	psql(t, admin, "", fmt.Sprintf("ALTER DATABASE %s SET search_path = %s", db, schema))
	psql(t, dsnFor(db), db, "CREATE TABLE public.vizinho(x int)") // de outro projeto: nunca entra no dump nem é tocado
	psql(t, dsnFor(db), db, "INSERT INTO public.vizinho VALUES (7)")
	psql(t, dsnFor(db), db, "CREATE TYPE estado AS ENUM ('a', 'b')")
	psql(t, dsnFor(db), db, "CREATE TABLE company(id int primary key, e estado)")
	psql(t, dsnFor(db), db, "INSERT INTO company VALUES (1, 'a'), (2, 'b')")
	for i := 0; i < 11; i++ { // o backup.sh recusa dump com menos de 11 linhas no pg_restore -l
		psql(t, dsnFor(db), db, fmt.Sprintf("CREATE TABLE t%d(x int)", i))
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("HANKO_DATABASE_URL="+dsnFor(db)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	bk := filepath.Join(dir, "bk")
	run := func(t *testing.T, script string, args ...string) string {
		t.Helper()
		path, err := filepath.Abs("../../deploy/oute-server/" + script)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", append([]string{path}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+filepath.Join(dir, "fake.log"),
			"HANKO_APP_DIR="+dir, "HANKO_BACKUP_DIR="+bk,
			"HANKO_READYZ_URL=http://"+ln.Addr().String()+"/readyz", "HANKO_READYZ_WAIT=4")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", script, args, err, out)
		}
		if pw, _ := u.User.Password(); pw != "" && strings.Contains(string(out), pw) {
			t.Fatalf("%s mostrou a senha do banco:\n%s", script, out)
		}
		return string(out)
	}

	out := run(t, "backup.sh")
	if !strings.Contains(out, "schema "+schema) {
		t.Fatalf("backup sem o schema:\n%s", out)
	}
	dumps, _ := filepath.Glob(filepath.Join(bk, "hanko-*.pgdump"))
	if len(dumps) != 1 {
		t.Fatalf("dumps = %v", dumps)
	}
	list, err := exec.Command("pg_restore", "-l", dumps[0]).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(list), "vizinho") || !strings.Contains(string(list), "TABLE "+schema+" company") {
		t.Fatalf("dump fora do schema:\n%s", list)
	}

	if out := run(t, "restore.sh", "--check"); !strings.Contains(out, "drill ok") {
		t.Fatalf("check:\n%s", out)
	}

	// estragos depois do backup: o restore devolve o schema ao estado do dump e não toca no vizinho
	psql(t, dsnFor(db), db, "DELETE FROM company WHERE id = 2")
	psql(t, dsnFor(db), db, "DROP TABLE t3")
	psql(t, dsnFor(db), db, "CREATE TABLE sobra(x int)")
	out = run(t, "restore.sh", "--apply", "--yes", "--pgdump", dumps[0])
	if !strings.Contains(out, "readyz ok") {
		t.Fatalf("restore:\n%s", out)
	}
	got := psql(t, dsnFor(db), db, "select (select count(*) from company)||','||(to_regclass('t3') is not null)||','||(to_regclass('sobra') is null)||','||(select x from public.vizinho)")
	if got != "2,true,true,7" {
		t.Fatalf("estado depois do restore = %q\n%s", got, out)
	}
	if pre, _ := filepath.Glob(filepath.Join(bk, "pre-restore-*.pgdump")); len(pre) != 1 {
		t.Fatalf("faltou o dump do estado anterior: %v", pre)
	}
}
