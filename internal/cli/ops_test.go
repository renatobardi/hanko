package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/backup"
	"github.com/renatobardi/hanko/internal/ops"
	"github.com/renatobardi/hanko/internal/syscmd"
)

// ---------- doctor ----------

func TestDoctorCommand(t *testing.T) {
	rep := ops.Report{Checks: []ops.Check{
		{Name: "postgres", Status: ops.OK, Detail: "conectado"},
		{Name: "otel", Status: ops.Warn, Detail: "coletor inacessível", Hint: "suba um coletor"},
	}}
	rt := Runtime{Doctor: func(context.Context) (ops.Report, error) { return rep, nil }}
	code, out, _ := exec(t, rt, nil, "", "doctor")
	if code != 0 || !strings.Contains(out, "OK") || !strings.Contains(out, "AVISO") || !strings.Contains(out, "→ suba um coletor") || !strings.Contains(out, "1 ok, 1 aviso(s), 0 falha(s)") {
		t.Fatalf("%d\n%s", code, out)
	}
	rep.Checks = append(rep.Checks, ops.Check{Name: "disco", Status: ops.Fail, Detail: "cheio"})
	rt.Doctor = func(context.Context) (ops.Report, error) { return rep, nil }
	code, out, errOut := exec(t, rt, nil, "", "doctor")
	if code != 1 || !strings.Contains(out, "FALHA") || !strings.Contains(errOut, "falhas") {
		t.Fatalf("falha deve sair 1: %d %q %q", code, out, errOut)
	}
	_, out, _ = exec(t, rt, nil, "", "doctor", "--json")
	if !strings.Contains(out, `"status": "fail"`) {
		t.Fatalf("json:\n%s", out)
	}
	if code, _, _ := exec(t, Runtime{}, nil, "", "doctor"); code != 1 {
		t.Fatal("sem runtime deve falhar")
	}
	_, out, _ = exec(t, rt, envLang("en"), "", "doctor")
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "FAIL") {
		t.Fatalf("en:\n%s", out)
	}
}

// ---------- service ----------

type memInstaller struct {
	files map[string]string
	cmd   *syscmd.Fake
}

func newMemInstaller(platform string) (*ops.Installer, *memInstaller) {
	m := &memInstaller{files: map[string]string{}, cmd: &syscmd.Fake{}}
	return &ops.Installer{Platform: platform, Cmd: m.cmd, Home: "/home/ana", UID: 501,
		WriteFile: func(p string, d []byte, _ os.FileMode) error { m.files[p] = string(d); return nil },
		MkdirAll:  func(string, os.FileMode) error { return nil },
		Exists:    func(p string) bool { _, ok := m.files[p]; return ok },
		Remove:    func(p string) error { delete(m.files, p); return nil }}, m
}

func opsRT(ins *ops.Installer) Runtime {
	return Runtime{Ops: OpsRuntime{Installer: ins, Exe: func() (string, error) { return "/usr/local/bin/hanko", nil }, GOOS: "linux", GOARCH: "amd64", InContainer: func() bool { return false }}}
}

func TestServiceInstallDryRunAndReal(t *testing.T) {
	ins, mem := newMemInstaller(ops.PlatformSystemd)
	code, out, errOut := exec(t, opsRT(ins), nil, "", "service", "install", "--dry-run", "--run-as", "hanko")
	if code != 0 || !strings.Contains(out, "# /etc/systemd/system/hanko.service") || !strings.Contains(out, "ExecStart=/usr/local/bin/hanko serve") ||
		!strings.Contains(out, "User=hanko") || !strings.Contains(out, "$ systemctl enable --now hanko.service") {
		t.Fatalf("%d %s\n%s", code, errOut, out)
	}
	if len(mem.files) != 0 || len(mem.cmd.Calls()) != 0 {
		t.Fatal("dry-run não pode gravar nem executar")
	}
	code, out, errOut = exec(t, opsRT(ins), nil, "", "service", "install")
	if code != 0 || len(mem.files) != 2 || !strings.Contains(out, "Serviço ativado.") || !strings.Contains(out, "/etc/hanko/hanko.env") {
		t.Fatalf("%d %s\n%s", code, errOut, out)
	}
	if got := len(mem.cmd.Calls()); got != 2 {
		t.Fatalf("comandos = %d", got)
	}
	// --user usa os caminhos do usuário
	ins2, mem2 := newMemInstaller(ops.PlatformSystemd)
	code, _, _ = exec(t, opsRT(ins2), nil, "", "service", "install", "--user", "--no-enable")
	if _, ok := mem2.files["/home/ana/.config/systemd/user/hanko.service"]; code != 0 || !ok || len(mem2.cmd.Calls()) != 0 {
		t.Fatalf("user: %d %v", code, mem2.files)
	}
	code, _, _ = exec(t, opsRT(ins), nil, "", "service", "uninstall")
	if code != 0 {
		t.Fatal(code)
	}
	if _, ok := mem.files["/etc/systemd/system/hanko.service"]; ok {
		t.Fatal("unit não removida")
	}
	// nome inválido
	if code, _, errOut := exec(t, opsRT(ins), nil, "", "service", "install", "--name", "Ruim!"); code == 0 || !strings.Contains(errOut, "inválido") {
		t.Fatalf("nome: %d %s", code, errOut)
	}
}

func TestServicePrint(t *testing.T) {
	code, out, _ := exec(t, Runtime{}, nil, "", "service", "print", "compose", "--dir", "/opt/app")
	if code != 0 || !strings.Contains(out, "WorkingDirectory=/opt/app") {
		t.Fatalf("%d\n%s", code, out)
	}
	args := []string{"service", "print", "timer", "--name", "hanko-backup", "--exec", "/opt/app/backup.sh", "--calendar", "*-*-* 01:30:00", "--timeout", "30min"}
	_, out, _ = exec(t, Runtime{}, nil, "", args...)
	if !strings.Contains(out, "ExecStart=/opt/app/backup.sh") || !strings.Contains(out, "TimeoutStartSec=30min") {
		t.Fatalf("service:\n%s", out)
	}
	_, out, _ = exec(t, Runtime{}, nil, "", append(args, "--part", "timer")...)
	if !strings.Contains(out, "OnCalendar=*-*-* 01:30:00") {
		t.Fatalf("timer:\n%s", out)
	}
	if code, _, _ := exec(t, Runtime{}, nil, "", "service", "print", "compose", "--dir", "relativo"); code != 2 {
		t.Fatalf("dir relativo deve ser erro de uso: %d", code)
	}
	if code, _, _ := exec(t, Runtime{}, nil, "", append(args, "--part", "x")...); code != 2 {
		t.Fatalf("part inválida: %d", code)
	}
}

// ---------- backup ----------

type fakeBackupAdmin struct {
	entries  []backup.Entry
	created  []backup.CreateOptions
	restored []string
	sessions int
	verErr   error
	closed   int
}

func (f *fakeBackupAdmin) Create(_ context.Context, o backup.CreateOptions) (backup.Entry, error) {
	f.created = append(f.created, o)
	return backup.Entry{Name: "hanko-20260310T030000Z.tar", Path: "/b/hanko-20260310T030000Z.tar", Size: 2048, Manifest: backup.Manifest{SchemaVersion: 180, Kind: o.Kind, Label: o.Label, CreatedAt: time.Now()}}, nil
}
func (f *fakeBackupAdmin) List() ([]backup.Entry, error) { return f.entries, nil }
func (f *fakeBackupAdmin) Verify(_ context.Context, name string) (backup.Manifest, error) {
	return backup.Manifest{SchemaVersion: 180, CreatedAt: time.Now(), DumpBytes: 10}, f.verErr
}
func (f *fakeBackupAdmin) Restore(_ context.Context, name string, o backup.RestoreOptions) (backup.RestoreReport, error) {
	f.restored = append(f.restored, fmt.Sprintf("%s force=%v skip=%v", name, o.Force, o.SkipBlobs))
	return backup.RestoreReport{Name: name, SchemaVersion: 150, BlobsRestored: 2, NeedsMigrate: true}, nil
}
func (f *fakeBackupAdmin) OtherSessions(context.Context) (int, error) { return f.sessions, nil }
func (f *fakeBackupAdmin) Close()                                     { f.closed++ }

func adminRT(f *fakeBackupAdmin) Runtime {
	return Runtime{Backup: func(context.Context) (BackupAdmin, error) { return f, nil }}
}

func TestBackupCreateListVerify(t *testing.T) {
	f := &fakeBackupAdmin{entries: []backup.Entry{{Name: "hanko-20260310T030000Z.tar", Size: 5 << 20, Manifest: backup.Manifest{Kind: "manual", SchemaVersion: 180, Label: "x", CreatedAt: time.Now()}}}}
	code, out, errOut := exec(t, adminRT(f), nil, "", "backup", "create", "--label", "antes")
	if code != 0 || !strings.Contains(out, "Backup criado: /b/hanko-20260310T030000Z.tar") || f.created[0].Label != "antes" || f.created[0].Kind != backup.KindManual {
		t.Fatalf("%d %s\n%s", code, errOut, out)
	}
	_, out, _ = exec(t, adminRT(f), nil, "", "backup", "list")
	if !strings.Contains(out, "ARQUIVO") || !strings.Contains(out, "5.0 MiB") || !strings.Contains(out, "hanko-20260310T030000Z.tar") {
		t.Fatalf("list:\n%s", out)
	}
	_, out, _ = exec(t, adminRT(&fakeBackupAdmin{}), nil, "", "backup", "list")
	if !strings.Contains(out, "Nenhum backup") {
		t.Fatalf("vazio:\n%s", out)
	}
	_, out, _ = exec(t, adminRT(f), nil, "", "backup", "verify", "latest")
	if !strings.Contains(out, "Íntegro") {
		t.Fatalf("verify:\n%s", out)
	}
	f.verErr = backup.ErrCorrupt
	if code, _, errOut := exec(t, adminRT(f), nil, "", "backup", "verify", "latest"); code != 1 || !strings.Contains(errOut, "corrompido") {
		t.Fatalf("corrompido: %d %s", code, errOut)
	}
	if f.closed == 0 {
		t.Fatal("o admin precisa ser fechado")
	}
}

func TestBackupRestoreSafety(t *testing.T) {
	f := &fakeBackupAdmin{}
	// sem --yes: erro de uso, nada restaurado
	code, _, errOut := exec(t, adminRT(f), nil, "", "backup", "restore", "latest")
	if code != 2 || !strings.Contains(errOut, "--yes") || len(f.restored) != 0 {
		t.Fatalf("sem --yes: %d %s", code, errOut)
	}
	// servidor ainda no ar
	f.sessions = 3
	code, _, errOut = exec(t, adminRT(f), nil, "", "backup", "restore", "latest", "--yes")
	if code != 1 || !strings.Contains(errOut, "3 outra(s) conexão") || len(f.restored) != 0 {
		t.Fatalf("sessões: %d %s", code, errOut)
	}
	// --force passa
	code, out, _ := exec(t, adminRT(f), nil, "", "backup", "restore", "latest", "--yes", "--force", "--skip-blobs")
	if code != 0 || f.restored[0] != "latest force=true skip=true" || !strings.Contains(out, "hanko migrate") {
		t.Fatalf("force: %d %v\n%s", code, f.restored, out)
	}
	// banco livre
	f.sessions, f.restored = 0, nil
	if code, _, _ := exec(t, adminRT(f), nil, "", "backup", "restore", "latest", "--yes"); code != 0 || len(f.restored) != 1 {
		t.Fatalf("livre: %d", code)
	}
	// arquivo corrompido nunca chega ao restore
	f.verErr, f.restored = backup.ErrCorrupt, nil
	if code, _, _ := exec(t, adminRT(f), nil, "", "backup", "restore", "latest", "--yes"); code != 1 || len(f.restored) != 0 {
		t.Fatalf("corrompido: %d", code)
	}
}

func TestBackupSchedule(t *testing.T) {
	ts, fa := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/instance/backup-schedule": jsonResp(200, api.BackupSchedule{Enabled: false, Cron: "0 3 * * *", Timezone: "UTC", Available: true}),
		"PUT /api/v1/instance/backup-schedule": jsonResp(200, api.BackupSchedule{Enabled: true, Cron: "30 2 * * *", Timezone: "America/Sao_Paulo"}),
	})
	e := map[string]string{"HANKO_URL": ts.URL}
	code, out, _ := exec(t, Runtime{}, e, "", "backup", "schedule")
	if code != 0 || !strings.Contains(out, "0 3 * * *") || !strings.Contains(out, "pausado") {
		t.Fatalf("get: %d\n%s", code, out)
	}
	code, out, _ = exec(t, Runtime{}, e, "", "backup", "schedule", "--enable", "--cron", "30 2 * * *", "--timezone", "America/Sao_Paulo")
	gotPut := fa.last().Body
	if code != 0 || !strings.Contains(gotPut, `"enabled":true`) || !strings.Contains(gotPut, `"cron":"30 2 * * *"`) || !strings.Contains(out, "ativo") {
		t.Fatalf("put: %d %s\n%s", code, gotPut, out)
	}
	if code, _, _ := exec(t, Runtime{}, e, "", "backup", "schedule", "--enable", "--disable"); code != 2 {
		t.Fatalf("conflito de flags: %d", code)
	}
}

// ---------- update ----------

type cliSource struct {
	rel   ops.Release
	files map[string][]byte
}

func (s cliSource) Latest(context.Context, string) (ops.Release, error) { return s.rel, nil }
func (s cliSource) Download(_ context.Context, a ops.Asset, w io.Writer) error {
	_, err := w.Write(s.files[a.URL])
	return err
}

func cliRelease(t *testing.T, version, bin string) cliSource {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "hanko", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(bin))
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(buf.Bytes())
	name := ops.ArchiveName(version, "linux", "amd64")
	return cliSource{
		rel:   ops.Release{Version: version, Assets: []ops.Asset{{Name: name, URL: "a"}, {Name: "checksums.txt", URL: "s"}}},
		files: map[string][]byte{"a": buf.Bytes(), "s": []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")},
	}
}

func TestUpdateCheckApplyAndRollback(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "hanko")
	if err := os.WriteFile(exe, []byte("velho"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeBackupAdmin{}
	rt := adminRT(f)
	rt.Ops = OpsRuntime{UpdateSource: cliRelease(t, "9.9.9", "novo"), Exe: func() (string, error) { return exe, nil }, GOOS: "linux", GOARCH: "amd64", InContainer: func() bool { return false }}
	env := map[string]string{}

	// binário de teste é "v-test" (não semver): --check diz que é build de desenvolvimento, sem anunciar "versão nova"
	// (num build da main a release do canal costuma ser anterior); apply exige --force
	code, out, _ := exec(t, rt, env, "", "update", "--check")
	if code != 0 || !strings.Contains(out, "Build de desenvolvimento") || !strings.Contains(out, "o canal stable está em 9.9.9") || strings.Contains(out, "Há versão nova") {
		t.Fatalf("check: %d\n%s", code, out)
	}
	code, _, errOut := exec(t, rt, env, "", "update")
	if code != 1 || !strings.Contains(errOut, "--force") {
		t.Fatalf("dev sem force: %d %s", code, errOut)
	}
	code, out, errOut = exec(t, rt, env, "", "update", "--force")
	if code != 0 {
		t.Fatalf("apply: %d %s\n%s", code, errOut, out)
	}
	if b, _ := os.ReadFile(exe); string(b) != "novo" {
		t.Fatalf("binário = %q", b)
	}
	if len(f.created) != 1 || f.created[0].Kind != backup.KindPreUpdate || !f.created[0].Prune || !strings.Contains(out, "Backup antes de atualizar") {
		t.Fatalf("backup pré-atualização: %+v\n%s", f.created, out)
	}
	code, out, _ = exec(t, rt, env, "", "update", "--rollback")
	if b, _ := os.ReadFile(exe); code != 0 || string(b) != "velho" || !strings.Contains(out, "restaurado") {
		t.Fatalf("rollback: %d %q\n%s", code, b, out)
	}
}

func TestUpdateRefusesInContainerAndBadChannel(t *testing.T) {
	rt := adminRT(&fakeBackupAdmin{})
	rt.Ops = OpsRuntime{UpdateSource: cliRelease(t, "9.9.9", "novo"), Exe: func() (string, error) { return filepath.Join(t.TempDir(), "hanko"), nil }, GOOS: "linux", GOARCH: "amd64", InContainer: func() bool { return true }}
	code, _, errOut := exec(t, rt, nil, "", "update", "--force", "--skip-backup")
	if code != 1 || !strings.Contains(errOut, "contêiner") {
		t.Fatalf("contêiner: %d %s", code, errOut)
	}
	if code, _, errOut := exec(t, rt, nil, "", "update", "--check", "--channel", "nightly"); code != 1 || !strings.Contains(errOut, "inválido") {
		t.Fatalf("canal: %d %s", code, errOut)
	}
	// o canal vem do ambiente
	if code, out, _ := exec(t, rt, map[string]string{"HANKO_UPDATE_CHANNEL": "beta"}, "", "update", "--check", "--json"); code != 0 || !strings.Contains(out, `"channel": "beta"`) {
		t.Fatalf("env canal: %d %s", code, out)
	}
}

func TestUpdateBackupFailureAborts(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "hanko")
	_ = os.WriteFile(exe, []byte("velho"), 0o755)
	rt := Runtime{Backup: func(context.Context) (BackupAdmin, error) { return nil, errors.New("sem banco") },
		Ops: OpsRuntime{UpdateSource: cliRelease(t, "9.9.9", "novo"), Exe: func() (string, error) { return exe, nil }, GOOS: "linux", GOARCH: "amd64", InContainer: func() bool { return false }}}
	code, _, errOut := exec(t, rt, nil, "", "update", "--force")
	if code != 1 || !strings.Contains(errOut, "nada foi alterado") {
		t.Fatalf("%d %s", code, errOut)
	}
	if b, _ := os.ReadFile(exe); string(b) != "velho" {
		t.Fatal("binário mudou")
	}
}

// ---------- serve --embedded-pg ----------

func TestServeEmbeddedFlags(t *testing.T) {
	var got EmbeddedOptions
	called := 0
	rt := Runtime{ServeEmbedded: func(_ context.Context, o EmbeddedOptions) error { called++; got = o; return nil }}
	code, _, errOut := exec(t, rt, nil, "", "serve", "--embedded-pg", "--pg-dir", "/tmp/pg", "--pg-bin-dir", "/opt/pg/bin", "--pg-port", "5999")
	if code != 0 || called != 1 || got != (EmbeddedOptions{Dir: "/tmp/pg", BinDir: "/opt/pg/bin", Port: 5999}) || !strings.Contains(errOut, "só para desenvolvimento e trial") {
		t.Fatalf("%d %+v %s", code, got, errOut)
	}
	// com banco próprio configurado, recusa
	code, _, errOut = exec(t, rt, map[string]string{"HANKO_DATABASE_URL": "postgres://x"}, "", "serve", "--embedded-pg")
	if code != 2 || called != 1 || !strings.Contains(errOut, "HANKO_DATABASE_URL") {
		t.Fatalf("com URL: %d %s", code, errOut)
	}
	// sem a flag, serve normal
	served := 0
	rt.Serve = func(context.Context) error { served++; return nil }
	if code, _, _ := exec(t, rt, nil, "", "serve"); code != 0 || served != 1 || called != 1 {
		t.Fatalf("serve normal: %d", code)
	}
	if code, _, _ := exec(t, Runtime{}, nil, "", "serve", "--embedded-pg"); code != 1 {
		t.Fatalf("sem runtime: %d", code)
	}
}
