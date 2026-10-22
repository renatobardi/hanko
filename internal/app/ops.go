package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/backup"
	"github.com/renatobardi/hanko/internal/cli"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/db"
	"github.com/renatobardi/hanko/internal/embeddedpg"
	"github.com/renatobardi/hanko/internal/ops"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/syscmd"
)

// Operação da instância (ADR-0062 e ADR-0063): backups, doctor e Postgres embutido.

// cliAppName marca as conexões abertas pelos comandos locais, para o restore não as contar como "outro cliente".
const cliAppName = "hanko-cli"

// newBackupManager monta o executor de backups. Blobs e schema são injetados: o servidor já tem o pool aberto, o
// comando local conecta só quando precisa.
func newBackupManager(cfg config.Config, version string, blobs backup.Blobs, schema func(context.Context) (int64, error), log *slog.Logger) *backup.Manager {
	return &backup.Manager{Dir: cfg.Ops.BackupDir, Keep: cfg.Ops.BackupKeep, Version: version, DSN: cfg.DatabaseURL,
		DumpBin: cfg.Ops.PGDumpBin, RestoreBin: cfg.Ops.PGRestoreBin, PSQLBin: cfg.Ops.PSQLBin, Cmd: syscmd.OS{}, Blobs: blobs, SchemaFn: schema,
		KnownSchema: db.LatestEmbedded(), Log: log}
}

// setupBackups devolve o executor de backups do servidor, ou nil (com o motivo no log) se não há diretório ou pg_dump.
func setupBackups(cfg config.Config, version string, pool *pgxpool.Pool, reg *storage.Registry, log *slog.Logger) *backup.Manager {
	if cfg.Ops.BackupDir == "" {
		log.Info("backups: sem diretório (HANKO_BACKUP_DIR/HANKO_HOME); backups desligados")
		return nil
	}
	if _, err := (syscmd.OS{}).LookPath(cfg.Ops.PGDumpBin); err != nil {
		log.Info("backups: pg_dump não encontrado; backups pela tela desligados (use o backup do host ou instale o cliente do PostgreSQL)", "bin", cfg.Ops.PGDumpBin)
		return nil
	}
	log.Info("backups: ligados", "dir", cfg.Ops.BackupDir, "keep", cfg.Ops.BackupKeep)
	return newBackupManager(cfg, version, backup.PGBlobs{Pool: pool, Reg: reg},
		func(ctx context.Context) (int64, error) { return db.MaxApplied(ctx, pool) }, log)
}

// waitBackups espera os backups em segundo plano terminarem, no máximo grace.
func waitBackups(wait func(), grace time.Duration, log *slog.Logger) {
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
		log.Warn("backups: ainda rodando no desligamento; o registro fica 'em andamento' e é liberado no próximo start")
	}
}

// ---------- comando local `hanko backup` ----------

// backupAdmin implementa cli.BackupAdmin. Listar e conferir só leem o diretório; criar e restaurar abrem a conexão
// ao banco (uma vez, sob demanda).
type backupAdmin struct {
	*backup.Manager
	cfg  config.Config
	log  *slog.Logger
	once sync.Once
	pool *pgxpool.Pool
	reg  *storage.Registry
	err  error
}

// OpenBackupAdmin prepara o acesso local aos backups. Não conecta ao banco ainda.
func OpenBackupAdmin(_ context.Context, cfg config.Config, version string) (cli.BackupAdmin, error) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := &backupAdmin{cfg: cfg, log: log}
	a.Manager = newBackupManager(cfg, version, lazyBlobs{a}, a.schema, log)
	return a, nil
}

// withAppName acrescenta application_name à URL do banco.
func withAppName(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	q.Set("application_name", name)
	u.RawQuery = q.Encode()
	return u.String()
}

func (a *backupAdmin) connect(ctx context.Context) error {
	a.once.Do(func() {
		if a.cfg.DatabaseURL == "" {
			a.err = errors.New("HANKO_DATABASE_URL não definido")
			return
		}
		pool, err := db.Open(ctx, withAppName(a.cfg.DatabaseURL, cliAppName), a.log)
		if err != nil {
			a.err = err
			return
		}
		reg, err := NewBlobRegistry(a.cfg, pool)
		if err != nil {
			pool.Close()
			a.err = err
			return
		}
		a.pool, a.reg = pool, reg
	})
	return a.err
}

func (a *backupAdmin) schema(ctx context.Context) (int64, error) {
	if err := a.connect(ctx); err != nil {
		return 0, err
	}
	return db.MaxApplied(ctx, a.pool)
}

// OtherSessions conta as conexões de outros clientes ao banco (o servidor ainda no ar).
func (a *backupAdmin) OtherSessions(ctx context.Context) (int, error) {
	if err := a.connect(ctx); err != nil {
		return 0, err
	}
	var n int
	err := a.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid() AND application_name <> $1 AND backend_type = 'client backend'`, cliAppName).Scan(&n)
	return n, err
}

// Close libera a conexão, se foi aberta.
func (a *backupAdmin) Close() {
	if a.pool != nil {
		a.pool.Close()
	}
}

// lazyBlobs conecta ao banco na primeira vez que o backup precisa do conteúdo externo.
type lazyBlobs struct{ a *backupAdmin }

func (l lazyBlobs) inner(ctx context.Context) (backup.PGBlobs, error) {
	if err := l.a.connect(ctx); err != nil {
		return backup.PGBlobs{}, err
	}
	return backup.PGBlobs{Pool: l.a.pool, Reg: l.a.reg}, nil
}

func (l lazyBlobs) List(ctx context.Context) ([]backup.BlobEntry, error) {
	b, err := l.inner(ctx)
	if err != nil {
		return nil, err
	}
	return b.List(ctx)
}

func (l lazyBlobs) Get(ctx context.Context, e backup.BlobEntry) ([]byte, error) {
	b, err := l.inner(ctx)
	if err != nil {
		return nil, err
	}
	return b.Get(ctx, e)
}

func (l lazyBlobs) Put(ctx context.Context, e backup.BlobEntry, data []byte) error {
	b, err := l.inner(ctx)
	if err != nil {
		return err
	}
	return b.Put(ctx, e, data)
}

// ---------- hanko doctor ----------

type pgProbe struct{ pool *pgxpool.Pool }

func (p pgProbe) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

func (p pgProbe) ServerVersion(ctx context.Context) (string, error) {
	var v string
	err := p.pool.QueryRow(ctx, `SHOW server_version`).Scan(&v)
	return v, err
}

func (p pgProbe) AppliedMigrations(ctx context.Context) ([]int64, error) {
	return db.AppliedVersions(ctx, p.pool)
}

// Doctor roda as checagens da instalação com o ambiente do processo. Nunca derruba: uma configuração inválida vira a
// primeira falha do relatório.
func Doctor(ctx context.Context) (ops.Report, error) {
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		cfg = config.Defaults()
	}
	e := ops.DoctorEnv{Cfg: cfg, CfgErr: cfgErr, Getenv: os.Getenv, Cmd: syscmd.OS{}, HTTP: &http.Client{Timeout: 3 * time.Second},
		DiskFree: ops.DiskFree, Now: time.Now}
	if cfg.DatabaseURL != "" {
		pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		if pool, err := pgxpool.New(pctx, cfg.DatabaseURL); err == nil {
			defer pool.Close()
			e.DB = pgProbe{pool}
		} else {
			e.DB = failedProbe{err}
		}
	}
	if dir := cfg.Ops.BackupDir; dir != "" {
		m := &backup.Manager{Dir: dir}
		e.LastBackup = func() (time.Time, error) {
			l, err := m.List()
			if err != nil || len(l) == 0 {
				return time.Time{}, err
			}
			return l[0].Manifest.CreatedAt, nil
		}
	}
	return ops.Doctor(ctx, e), nil
}

// failedProbe é o DBProbe de uma URL que nem abre pool.
type failedProbe struct{ err error }

func (f failedProbe) Ping(context.Context) error { return f.err }
func (f failedProbe) ServerVersion(context.Context) (string, error) {
	return "", f.err
}
func (f failedProbe) AppliedMigrations(context.Context) ([]int64, error) { return nil, f.err }

// ---------- hanko serve --embedded-pg ----------

// ServeEmbedded sobe o Postgres local (initdb/pg_ctl de uma instalação do PostgreSQL) e o servidor em cima dele. É só
// para dev/trial (ADR-0063); o cluster é parado ao encerrar.
func ServeEmbedded(ctx context.Context, version string, o cli.EmbeddedOptions) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	dir := o.Dir
	if dir == "" {
		home := os.Getenv("HANKO_HOME")
		if home == "" {
			h, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("app: diretório do usuário: %w", err)
			}
			home = filepath.Join(h, ".hanko")
		}
		dir = filepath.Join(home, "pg")
	}
	inst, err := embeddedpg.Start(ctx, embeddedpg.Options{Dir: dir, BinDir: o.BinDir, Port: o.Port, Cmd: syscmd.OS{}, Log: logger})
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 70*time.Second)
		defer cancel()
		if err := inst.Stop(sctx); err != nil {
			logger.Warn("embedded-pg: não consegui parar o cluster", "err", err)
		}
	}()
	getenv := func(k string) string {
		if k == "HANKO_DATABASE_URL" {
			return inst.DSN
		}
		return os.Getenv(k)
	}
	cfg, err := config.LoadFrom(getenv, os.ReadFile)
	if err != nil {
		return err
	}
	return Serve(ctx, cfg, version)
}
