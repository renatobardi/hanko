//go:build integration

package deploy

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Sem o ai-memory no ar (profile desligado ou container parado) o backup.sh não gera tar da memória; o
// dump do banco precisa sair mesmo assim, com SHA256SUMS e código de saída 0 (o deploy-prd.sh aborta se o
// backup pré-deploy falhar).
func TestBackupWithoutAIMemory(t *testing.T) {
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
	requirePGClient(t, admin)

	script, err := filepath.Abs("../../deploy/oute-server/backup.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	const db = "hanko_bk"
	psql(t, admin, "", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", db))
	psql(t, admin, "", "CREATE DATABASE "+db)
	t.Cleanup(func() { psql(t, admin, "", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", db)) })
	// o backup.sh recusa dump com menos de 11 linhas no pg_restore -l
	for i := 0; i < 12; i++ {
		psql(t, dsnFor(db), db, fmt.Sprintf("CREATE TABLE t%d(x int)", i))
	}

	host, port, _ := net.SplitHostPort(u.Host)
	out := filepath.Join(dir, "bk")
	run := func() string {
		t.Helper()
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+filepath.Join(dir, "fake.log"),
			"HANKO_APP_DIR="+dir, "HANKO_BACKUP_DIR="+out, "HANKO_BACKUP_KEEP=2",
			"HANKO_PG_USER="+u.User.Username(), "HANKO_PG_DB="+db, "PGHOST="+host, "PGPORT="+port, "PGPASSWORD="+pw)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("backup.sh deveria sair 0 sem ai-memory: %v\n%s", err, b)
		}
		return string(b)
	}
	got := run()
	if !strings.Contains(got, "sem tar da memória") {
		t.Fatalf("faltou o aviso de que não há tar da memória:\n%s", got)
	}
	sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
	if err != nil {
		t.Fatalf("SHA256SUMS não foi gerado: %v\n%s", err, got)
	}
	if !strings.Contains(string(sums), ".pgdump") || strings.Contains(string(sums), "aimemory") {
		t.Fatalf("SHA256SUMS inesperado: %q", sums)
	}
	// retenção: a 3ª geração (KEEP=2) apaga a mais velha sem tropeçar na ausência de aimemory-*
	for _, ts := range []string{"20000101T000000Z", "20000102T000000Z"} {
		if err := os.WriteFile(filepath.Join(out, "hanko-"+ts+".pgdump"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run()
	files, _ := filepath.Glob(filepath.Join(out, "hanko-*.pgdump"))
	if len(files) != 2 {
		t.Fatalf("retenção: %d dumps, esperado 2: %v", len(files), files)
	}
}
