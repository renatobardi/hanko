package backup

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/syscmd"
)

const testDSN = "postgres://hanko:s3nha-secreta@db.local:5433/hanko?sslmode=disable"

// fakeBlobs é um driver externo em memória.
type fakeBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
	list []BlobEntry
}

func newFakeBlobs(items map[string]string) *fakeBlobs {
	f := &fakeBlobs{data: map[string][]byte{}}
	for k, v := range items {
		parts := strings.SplitN(k, "/", 2)
		b := []byte(v)
		e := BlobEntry{CompanyID: parts[0], ArtifactID: parts[1], Size: int64(len(b)), SHA256: sumHex(b), Mime: "text/plain", Driver: "s3"}
		f.list = append(f.list, e)
		f.data[k] = b
	}
	return f
}

func (f *fakeBlobs) List(context.Context) ([]BlobEntry, error) { return f.list, nil }
func (f *fakeBlobs) Get(_ context.Context, b BlobEntry) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[b.CompanyID+"/"+b.ArtifactID]
	if !ok {
		return nil, errors.New("sem conteúdo")
	}
	return d, nil
}
func (f *fakeBlobs) Put(_ context.Context, b BlobEntry, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[b.CompanyID+"/"+b.ArtifactID] = data
	return nil
}

// pgFake simula pg_dump (escreve um dump) e pg_restore (guarda o arquivo recebido).
func pgFake(dump string, restored *string) *syscmd.Fake {
	return &syscmd.Fake{
		Paths: map[string]string{"pg_dump": "/bin/pg_dump", "pg_restore": "/bin/pg_restore", "psql": "/bin/psql"},
		Handler: func(c syscmd.Cmd) error {
			switch c.Name {
			case "pg_dump":
				_, err := io.WriteString(c.Stdout, dump)
				return err
			case "pg_restore": // gera o script SQL a partir do dump (--file=-)
				b, err := os.ReadFile(c.Args[len(c.Args)-1])
				if err != nil {
					return err
				}
				_, err = io.WriteString(c.Stdout, "-- script\n"+string(b)+"\n")
				return err
			case "psql": // aplica o script
				for _, a := range c.Args {
					if f, ok := strings.CutPrefix(a, "--file="); ok {
						b, err := os.ReadFile(f)
						if err != nil {
							return err
						}
						*restored = string(b)
					}
				}
			}
			return nil
		},
	}
}

func newMgr(t *testing.T, fake *syscmd.Fake, blobs Blobs) *Manager {
	t.Helper()
	n := 0
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return &Manager{Dir: filepath.Join(t.TempDir(), "backups"), Keep: 2, Version: "v0.4.0", DSN: testDSN, Cmd: fake, Blobs: blobs,
		SchemaFn: func(context.Context) (int64, error) { return 171, nil }, KnownSchema: 171,
		Now: func() time.Time { n++; return base.Add(time.Duration(n) * time.Hour) }}
}

func TestCreateListVerifyRestoreRoundTrip(t *testing.T) {
	var restored string
	fake := pgFake("PGDMP-conteudo-do-banco", &restored)
	blobs := newFakeBlobs(map[string]string{"c1/a1": "arquivo um", "c1/a2": "arquivo dois"})
	m := newMgr(t, fake, blobs)
	ctx := context.Background()

	e, err := m.Create(ctx, CreateOptions{Label: "antes da migração"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Manifest.Kind != KindManual || e.Manifest.SchemaVersion != 171 || len(e.Manifest.Blobs) != 2 || e.Manifest.Label != "antes da migração" {
		t.Fatalf("manifest = %+v", e.Manifest)
	}
	if !nameRe.MatchString(e.Name) {
		t.Fatalf("nome %q", e.Name)
	}
	// a senha nunca vai em argumentos; vai no ambiente
	for _, c := range fake.Calls() {
		if strings.Contains(c.Cmd.String(), "s3nha-secreta") {
			t.Fatalf("senha em argv: %s", c.Cmd)
		}
	}
	env := strings.Join(fake.Calls()[0].Cmd.Env, " ")
	for _, want := range []string{"PGHOST=db.local", "PGPORT=5433", "PGUSER=hanko", "PGDATABASE=hanko", "PGPASSWORD=s3nha-secreta", "PGSSLMODE=disable"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env sem %q: %s", want, env)
		}
	}

	l, err := m.List()
	if err != nil || len(l) != 1 || l[0].Name != e.Name {
		t.Fatalf("list = %+v, %v", l, err)
	}
	if _, err := m.Verify(ctx, e.Name); err != nil {
		t.Fatal(err)
	}

	// o conteúdo externo some do servidor; o restore devolve
	blobs.data = map[string][]byte{}
	rep, err := m.Restore(ctx, "latest", RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(restored, "DROP SCHEMA IF EXISTS public CASCADE;\nCREATE SCHEMA public;\n") || !strings.Contains(restored, "PGDMP-conteudo-do-banco") || rep.BlobsRestored != 2 || rep.NeedsMigrate {
		t.Fatalf("restored=%q rep=%+v", restored, rep)
	}
	if string(blobs.data["c1/a2"]) != "arquivo dois" {
		t.Fatalf("blob não voltou: %q", blobs.data["c1/a2"])
	}
	last := fake.Calls()[len(fake.Calls())-1].Cmd
	if last.Name != "psql" || !strings.Contains(last.String(), "--single-transaction") || !strings.Contains(last.String(), "ON_ERROR_STOP=1") || !strings.Contains(last.String(), "--dbname=hanko") {
		t.Fatalf("psql = %s", last)
	}
	if strings.Contains(last.String(), "s3nha-secreta") {
		t.Fatal("senha em argv")
	}
}

func TestListWorksWithoutSidecar(t *testing.T) {
	var r string
	m := newMgr(t, pgFake("D", &r), nil)
	e, err := m.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sidecarPath(e.Path)); err != nil {
		t.Fatal(err)
	}
	l, err := m.List()
	if err != nil || len(l) != 1 || l[0].Manifest.DumpBytes != 1 {
		t.Fatalf("list sem sidecar = %+v, %v", l, err)
	}
}

func TestVerifyDetectsCorruptionAndTruncation(t *testing.T) {
	var r string
	m := newMgr(t, pgFake(strings.Repeat("X", 2048), &r), nil)
	e, err := m.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(e.Path)

	// um byte do dump adulterado
	bad := append([]byte(nil), raw...)
	bad[600] ^= 0xff
	if err := os.WriteFile(e.Path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(context.Background(), e.Name); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("adulterado: %v", err)
	}
	if _, err := m.Restore(context.Background(), e.Name, RestoreOptions{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("restore adulterado: %v", err)
	}
	for _, c := range m.Cmd.(*syscmd.Fake).Calls() {
		if c.Cmd.Name == "pg_restore" || c.Cmd.Name == "psql" {
			t.Fatal("o restore não pode rodar com arquivo corrompido")
		}
	}

	// truncado
	if err := os.WriteFile(e.Path, raw[:len(raw)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(context.Background(), e.Name); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncado: %v", err)
	}
}

func TestRestoreRefusesNewerSchemaUnlessForced(t *testing.T) {
	var r string
	m := newMgr(t, pgFake("D", &r), nil)
	m.SchemaFn = func(context.Context) (int64, error) { return 200, nil }
	if _, err := m.Create(context.Background(), CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Restore(context.Background(), "latest", RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "mais novo") {
		t.Fatalf("esperava recusa: %v", err)
	}
	if _, err := m.Restore(context.Background(), "latest", RestoreOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreFlagsNeedsMigrateWhenBackupIsOlder(t *testing.T) {
	var r string
	m := newMgr(t, pgFake("D", &r), nil)
	m.SchemaFn = func(context.Context) (int64, error) { return 150, nil }
	if _, err := m.Create(context.Background(), CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Restore(context.Background(), "latest", RestoreOptions{})
	if err != nil || !rep.NeedsMigrate {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
}

func TestRestoreRequiresExternalDriverForBlobs(t *testing.T) {
	var r string
	m := newMgr(t, pgFake("D", &r), newFakeBlobs(map[string]string{"c/a": "x"}))
	if _, err := m.Create(context.Background(), CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	m.Blobs = nil
	if _, err := m.Restore(context.Background(), "latest", RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "driver externo") {
		t.Fatalf("esperava recusa: %v", err)
	}
	if _, err := m.Restore(context.Background(), "latest", RestoreOptions{SkipBlobs: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFailuresLeaveNothingBehind(t *testing.T) {
	var r string
	cases := map[string]func(*Manager, *syscmd.Fake){
		"pg_dump ausente": func(_ *Manager, f *syscmd.Fake) { delete(f.Paths, "pg_dump") },
		"pg_dump falha": func(_ *Manager, f *syscmd.Fake) {
			f.Handler = func(syscmd.Cmd) error { return errors.New("conexão recusada") }
		},
		"dump vazio": func(_ *Manager, f *syscmd.Fake) { f.Handler = func(syscmd.Cmd) error { return nil } },
		"blob corrompido na origem": func(m *Manager, _ *syscmd.Fake) {
			b := newFakeBlobs(map[string]string{"c/a": "conteudo"})
			b.data["c/a"] = []byte("outro")
			m.Blobs = b
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			fake := pgFake("D", &r)
			m := newMgr(t, fake, nil)
			mod(m, fake)
			if _, err := m.Create(context.Background(), CreateOptions{}); err == nil {
				t.Fatal("esperava erro")
			}
			des, _ := os.ReadDir(m.Dir)
			if len(des) != 0 {
				t.Fatalf("sobras no diretório: %v", des)
			}
		})
	}
}

func TestPrunePrunesOnlyAutomaticBackups(t *testing.T) {
	var r string
	m := newMgr(t, pgFake("D", &r), nil) // Keep = 2
	ctx := context.Background()
	manual, err := m.Create(ctx, CreateOptions{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	var autos []Entry
	for i := 0; i < 4; i++ {
		e, err := m.Create(ctx, CreateOptions{Kind: KindScheduled, Prune: true})
		if err != nil {
			t.Fatal(err)
		}
		autos = append(autos, e)
	}
	l, _ := m.List()
	names := map[string]bool{}
	for _, e := range l {
		names[e.Name] = true
	}
	if !names[manual.Name] || names[autos[0].Name] || names[autos[1].Name] || !names[autos[2].Name] || !names[autos[3].Name] || len(l) != 3 {
		t.Fatalf("restou %v", names)
	}
	if _, err := os.Stat(sidecarPath(autos[0].Path)); !os.IsNotExist(err) {
		t.Fatal("sidecar do podado deveria ter sumido")
	}
}

func TestResolveRejectsTraversalAndUnknownNames(t *testing.T) {
	var r string
	m := newMgr(t, pgFake("D", &r), nil)
	for _, n := range []string{"../etc/passwd", "/etc/passwd", "hanko-x.tar", "", "hanko-20260101T000000Z.tar"} {
		if _, err := m.Verify(context.Background(), n); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", n, err)
		}
	}
	if _, err := m.Restore(context.Background(), "latest", RestoreOptions{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("latest vazio: %v", err)
	}
}

func TestSameSecondGetsDistinctNames(t *testing.T) {
	var r string
	m := newMgr(t, pgFake("D", &r), nil)
	m.Now = func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) }
	a, err := m.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Name == b.Name {
		t.Fatal("nomes iguais")
	}
}

func TestVerifyRejectsUnexpectedMembers(t *testing.T) {
	var r string
	m := newMgr(t, pgFake("D", &r), nil)
	e, err := m.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// reescreve o tar com um membro de caminho perigoso antes do manifest
	f, _ := os.Create(e.Path)
	tw := tar.NewWriter(f)
	for _, mb := range []struct{ n, c string }{{"../../evil", "x"}} {
		_ = tw.WriteHeader(&tar.Header{Name: mb.n, Size: int64(len(mb.c)), Mode: 0o600, Typeflag: tar.TypeReg})
		_, _ = io.WriteString(tw, mb.c)
	}
	_ = tw.Close()
	_ = f.Close()
	if _, err := m.Verify(context.Background(), e.Name); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
}

func TestPgEnvRejectsBadDSN(t *testing.T) {
	if _, err := pgEnv("não é url"); err == nil {
		t.Fatal("esperava erro")
	}
	env, err := pgEnv("postgres://u@/db?host=/var/run/postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(env), "PGHOST=/var/run/postgresql") {
		t.Fatalf("env = %v", env)
	}
}

// A saída de um processo externo pode ecoar o ambiente; a senha do banco nunca sai nos erros de Create nem de Restore.
func TestErrorsNeverCarryTheDatabasePassword(t *testing.T) {
	echo := &syscmd.Fake{
		Paths: map[string]string{"pg_dump": "/bin/pg_dump", "pg_restore": "/bin/pg_restore", "psql": "/bin/psql"},
		Handler: func(c syscmd.Cmd) error {
			return &syscmd.ExitError{Cmd: c.String(), Err: errors.New("exit status 1"), Stderr: "conexão recusada: PGPASSWORD=s3nha-secreta host=db.local"}
		},
	}
	m := newMgr(t, echo, newFakeBlobs(nil))
	_, err := m.Create(context.Background(), CreateOptions{})
	if err == nil {
		t.Fatal("o Create devia falhar")
	}
	if strings.Contains(err.Error(), "s3nha-secreta") || !strings.Contains(err.Error(), "***") {
		t.Fatalf("erro do Create vaza a senha ou perdeu a pista: %v", err)
	}

	// o Restore precisa de um arquivo válido: gera um com o executor bom e restaura com o que ecoa a senha
	var restored string
	good := newMgr(t, pgFake("PGDMP-ok", &restored), newFakeBlobs(nil))
	e, err := good.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bad := &Manager{Dir: good.Dir, Keep: good.Keep, Version: good.Version, DSN: testDSN, Cmd: echo, Blobs: newFakeBlobs(nil),
		SchemaFn: good.SchemaFn, KnownSchema: good.KnownSchema, Now: good.Now}
	if _, err := bad.Restore(context.Background(), e.Name, RestoreOptions{}); err == nil {
		t.Fatal("o Restore devia falhar")
	} else if strings.Contains(err.Error(), "s3nha-secreta") {
		t.Fatalf("erro do Restore vaza a senha: %v", err)
	}
}
