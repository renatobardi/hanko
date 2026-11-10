//go:build integration

package deploy

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Banco externo (ADR-0093): o backup.sh e o restore.sh não tocam no banco (ele é coberto pelo backup do banco
// compartilhado) e não precisam de cliente do Postgres; ficam só memória e artefatos. O restore recusa --pgdump.
func TestBackupAndRestoreWithExternalDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	// a URL nunca é usada: o modo externo não conecta
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("HANKO_DATABASE_URL=postgresql://app_x:s3nha@10.0.0.9:5433/postgres\n"), 0o600); err != nil {
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
	fakeLog := filepath.Join(dir, "fake.log")
	run := func(t *testing.T, script string, args ...string) (string, error) {
		t.Helper()
		path, err := filepath.Abs("../../deploy/oute-server/" + script)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", append([]string{path}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+fakeLog,
			"HANKO_APP_DIR="+dir, "HANKO_BACKUP_DIR="+bk,
			"HANKO_READYZ_URL=http://"+ln.Addr().String()+"/readyz", "HANKO_READYZ_WAIT=4")
		out, err := cmd.CombinedOutput()
		if strings.Contains(string(out), "s3nha") {
			t.Fatalf("%s mostrou a senha do banco:\n%s", script, out)
		}
		return string(out), err
	}

	out, err := run(t, "backup.sh")
	if err != nil || !strings.Contains(out, "banco externo: sem dump aqui") {
		t.Fatalf("backup.sh: %v\n%s", err, out)
	}
	if dumps, _ := filepath.Glob(filepath.Join(bk, "hanko-*.pgdump")); len(dumps) != 0 {
		t.Fatalf("banco externo não gera dump: %v", dumps)
	}

	// sem memória nem artefatos (o docker falso não tem contêineres), o restore não tem o que repor
	if out, err := run(t, "restore.sh", "--check"); err == nil || !strings.Contains(out, "sem tar da memória nem dos artefatos") {
		t.Fatalf("check sem arquivos devia falhar: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(bk, "blobs-20260101T000000Z.tar"), tarOf(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "restore.sh", "--check"); err != nil || !strings.Contains(out, "drill ok") {
		t.Fatalf("check: %v\n%s", err, out)
	}
	if out, err := run(t, "restore.sh", "--apply", "--yes", "--pgdump", filepath.Join(bk, "x.pgdump")); err == nil || !strings.Contains(out, "banco compartilhado") {
		t.Fatalf("--pgdump com banco externo devia ser recusado: %v\n%s", err, out)
	}
	_ = os.Remove(fakeLog)
	out, err = run(t, "restore.sh", "--apply", "--yes")
	if err != nil || !strings.Contains(out, "readyz ok") {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	l, _ := os.ReadFile(fakeLog)
	if !strings.Contains(string(l), "stop hanko") || !strings.Contains(string(l), "up -d") {
		t.Fatalf("o apply devia parar e subir a stack: %q", l)
	}
}

// tarOf devolve um tar válido com um arquivo.
func tarOf(t *testing.T) []byte {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("tar", "cf", "-", "-C", src, ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}
