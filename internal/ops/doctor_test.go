package ops

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/db"
	"github.com/renatobardi/hanko/internal/syscmd"
)

type fakeDB struct {
	pingErr   error
	version   string
	applied   []int64
	appliedEr error
}

func (f fakeDB) Ping(context.Context) error { return f.pingErr }
func (f fakeDB) ServerVersion(context.Context) (string, error) {
	return f.version, nil
}
func (f fakeDB) AppliedMigrations(context.Context) ([]int64, error) { return f.applied, f.appliedEr }

type fakeHTTP struct {
	status int
	err    error
	hit    string
}

func (f *fakeHTTP) Do(r *http.Request) (*http.Response, error) {
	f.hit = r.URL.String()
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: f.status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func goodCfg() config.Config {
	c := config.Defaults()
	c.DatabaseURL = "postgres://u:pw-secreta@h/d"
	c.Secret = strings.Repeat("s", 40)
	c.SecretKey = strings.Repeat("k", 40)
	c.WorkspacesRoot = "/tmp"
	c.Ops.BackupDir = "/tmp/backups-nao-existe"
	c.Memory.URL = "http://127.0.0.1:49374"
	return c
}

func goodEnv() DoctorEnv {
	return DoctorEnv{
		Cfg:        goodCfg(),
		Getenv:     func(string) string { return "" },
		DB:         fakeDB{version: "16.4", applied: db.EmbeddedVersions()},
		Cmd:        &syscmd.Fake{Paths: map[string]string{"claude": "/bin/claude", "pg_dump": "/bin/pg_dump", "pg_restore": "/bin/pg_restore", "psql": "/bin/psql"}},
		HTTP:       &fakeHTTP{status: 200},
		Dial:       func(context.Context, string) error { return nil },
		DiskFree:   func(string) (uint64, error) { return 100 << 30, nil },
		LastBackup: func() (time.Time, error) { return time.Now().Add(-3 * time.Hour), nil },
		Now:        time.Now,
	}
}

func byName(r Report, n string) Check {
	for _, c := range r.Checks {
		if c.Name == n {
			return c
		}
	}
	return Check{Name: n, Status: "ausente"}
}

func TestDoctorHealthyInstall(t *testing.T) {
	r := Doctor(context.Background(), goodEnv())
	for _, c := range r.Checks {
		if c.Status != OK {
			t.Errorf("%s = %s (%s)", c.Name, c.Status, c.Detail)
		}
	}
	if !r.Healthy() || len(r.Checks) != 10 {
		t.Fatalf("checks = %d healthy=%v", len(r.Checks), r.Healthy())
	}
}

func TestDoctorPostgresDown(t *testing.T) {
	e := goodEnv()
	e.DB = fakeDB{pingErr: errors.New("dial tcp: connect postgres://u:pw-secreta@h/d refused")}
	r := Doctor(context.Background(), e)
	c := byName(r, "postgres")
	if c.Status != Fail || strings.Contains(c.Detail, "pw-secreta") {
		t.Fatalf("postgres = %+v", c)
	}
	if r.Healthy() {
		t.Fatal("deveria estar doente")
	}
}

func TestDoctorNoDatabaseAndConfigError(t *testing.T) {
	e := goodEnv()
	e.DB = nil
	e.CfgErr = errors.New("HANKO_DATABASE_URL é obrigatório")
	r := Doctor(context.Background(), e)
	if byName(r, "config").Status != Fail || byName(r, "postgres").Status != Fail || byName(r, "migrations").Status != Skip || byName(r, "secrets").Status != Skip {
		t.Fatalf("%+v", r.Checks)
	}
}

func TestDoctorMigrations(t *testing.T) {
	emb := db.EmbeddedVersions()
	t.Run("pendente fora de ordem", func(t *testing.T) {
		e := goodEnv()
		e.Cfg.MigrateOnBoot = false
		e.DB = fakeDB{applied: emb[1:]}
		c := byName(Doctor(context.Background(), e), "migrations")
		if c.Status != Fail || !strings.Contains(c.Hint, "hanko migrate") {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("pendente com migrate no boot vira aviso", func(t *testing.T) {
		e := goodEnv()
		e.Cfg.MigrateOnBoot = true
		e.DB = fakeDB{applied: emb[1:]}
		if c := byName(Doctor(context.Background(), e), "migrations"); c.Status != Warn {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("banco mais novo que o binário", func(t *testing.T) {
		e := goodEnv()
		e.DB = fakeDB{applied: append(append([]int64(nil), emb...), emb[len(emb)-1]+50)}
		c := byName(Doctor(context.Background(), e), "migrations")
		if c.Status != Warn || !strings.Contains(c.Detail, "não conhece") {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("erro ao ler", func(t *testing.T) {
		e := goodEnv()
		e.DB = fakeDB{appliedEr: errors.New("boom")}
		if c := byName(Doctor(context.Background(), e), "migrations"); c.Status != Fail {
			t.Fatalf("%+v", c)
		}
	})
}

func TestDoctorOTel(t *testing.T) {
	e := goodEnv()
	var dialed string
	e.Dial = func(_ context.Context, a string) error { dialed = a; return errors.New("recusado") }
	e.Getenv = func(k string) string {
		if k == "OTEL_EXPORTER_OTLP_ENDPOINT" {
			return "http://otel.local:4318"
		}
		return ""
	}
	c := byName(Doctor(context.Background(), e), "otel")
	if c.Status != Warn || dialed != "otel.local:4318" {
		t.Fatalf("%+v dialed=%q", c, dialed)
	}
	e.Getenv = func(k string) string {
		if k == "OTEL_SDK_DISABLED" {
			return "true"
		}
		return ""
	}
	if c := byName(Doctor(context.Background(), e), "otel"); c.Status != Skip {
		t.Fatalf("%+v", c)
	}
	// sem variável: padrão local
	e.Getenv = func(string) string { return "" }
	Doctor(context.Background(), e)
	if dialed != "localhost:4317" {
		t.Fatalf("dialed = %q", dialed)
	}
}

func TestOtelEndpointForms(t *testing.T) {
	cases := map[string]string{"https://c.example": "c.example:443", "http://c.example": "c.example:4317", "http://c.example:9": "c.example:9", "c.example:7": "c.example:7"}
	for in, want := range cases {
		got, _ := otelEndpoint(func(k string) string {
			if k == "OTEL_EXPORTER_OTLP_ENDPOINT" {
				return in
			}
			return ""
		})
		if got != want {
			t.Errorf("%q -> %q, quer %q", in, got, want)
		}
	}
}

func TestDoctorMemory(t *testing.T) {
	e := goodEnv()
	h := &fakeHTTP{err: errors.New("recusado")}
	e.HTTP = h
	if c := byName(Doctor(context.Background(), e), "ai-memory"); c.Status != Warn || h.hit != "http://127.0.0.1:49374/health" {
		t.Fatalf("%+v hit=%s", c, h.hit)
	}
	h.err, h.status = nil, 503
	if c := byName(Doctor(context.Background(), e), "ai-memory"); c.Status != Warn || !strings.Contains(c.Detail, "503") {
		t.Fatalf("%+v", c)
	}
	e.Cfg.Memory.Provider = config.MemoryNone
	if c := byName(Doctor(context.Background(), e), "ai-memory"); c.Status != Skip {
		t.Fatalf("%+v", c)
	}
}

func TestDoctorHarnessesSecretsDiskTools(t *testing.T) {
	e := goodEnv()
	e.Cmd = &syscmd.Fake{}
	e.Cfg.SecretGenerated = true
	e.Cfg.SecretKey = ""
	e.Cfg.Vault.Addr = "http://vault"
	e.DiskFree = func(p string) (uint64, error) {
		if p == "/tmp" {
			return 100 << 20, nil // workspaces quase cheio
		}
		return 100 << 30, nil
	}
	r := Doctor(context.Background(), e)
	if c := byName(r, "harnesses"); c.Status != Warn {
		t.Errorf("harnesses %+v", c)
	}
	if c := byName(r, "secrets"); c.Status != Warn || !strings.Contains(c.Detail, "VAULT") || !strings.Contains(c.Detail, "HANKO_SECRET_KEY") {
		t.Errorf("secrets %+v", c)
	}
	if c := byName(r, "disco"); c.Status != Fail {
		t.Errorf("disco %+v", c)
	}
	if c := byName(r, "pg_dump"); c.Status != Warn {
		t.Errorf("pg_dump %+v", c)
	}
	// nenhum valor de segredo no relatório
	for _, c := range r.Checks {
		if strings.Contains(c.Detail+c.Hint, strings.Repeat("s", 40)) {
			t.Errorf("segredo vazou em %s", c.Name)
		}
	}
}

func TestDoctorDiskWarnBoundary(t *testing.T) {
	e := goodEnv()
	e.DiskFree = func(string) (uint64, error) { return 4 << 30, nil }
	if c := byName(Doctor(context.Background(), e), "disco"); c.Status != Warn {
		t.Fatalf("%+v", c)
	}
	e.DiskFree = func(string) (uint64, error) { return 0, errors.New("sem statfs") }
	if c := byName(Doctor(context.Background(), e), "disco"); c.Status != Warn {
		t.Fatalf("%+v", c)
	}
}

func TestDoctorBackupAge(t *testing.T) {
	e := goodEnv()
	e.LastBackup = func() (time.Time, error) { return time.Time{}, nil }
	if c := byName(Doctor(context.Background(), e), "backup"); c.Status != Warn {
		t.Fatalf("%+v", c)
	}
	e.LastBackup = func() (time.Time, error) { return time.Now().Add(-5 * 24 * time.Hour), nil }
	if c := byName(Doctor(context.Background(), e), "backup"); c.Status != Warn || !strings.Contains(c.Detail, "5 d") {
		t.Fatalf("%+v", c)
	}
	e.LastBackup = func() (time.Time, error) { return time.Time{}, errors.New("sem permissão") }
	if c := byName(Doctor(context.Background(), e), "backup"); c.Status != Warn {
		t.Fatalf("%+v", c)
	}
	e.LastBackup = nil
	if len(Doctor(context.Background(), e).Checks) != 9 {
		t.Fatal("sem LastBackup a checagem some")
	}
}

func TestHumanFormats(t *testing.T) {
	if HumanBytes(512) != "512 B" || HumanBytes(5<<20) != "5.0 MiB" || HumanBytes(3<<30) != "3.0 GiB" {
		t.Fatal(HumanBytes(5 << 20))
	}
	if HumanAge(10*time.Second) != "menos de 1 min" || HumanAge(30*time.Minute) != "30 min" || HumanAge(5*time.Hour) != "5 h" || HumanAge(72*time.Hour) != "3 d" {
		t.Fatal("HumanAge")
	}
}

func TestNearestExistingFindsParent(t *testing.T) {
	if got := nearestExisting("/tmp/nao/existe/mesmo"); got != "/tmp" {
		t.Fatalf("got %q", got)
	}
}

func TestDiskFreeReal(t *testing.T) {
	n, err := DiskFree("/tmp")
	if err != nil || n == 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
