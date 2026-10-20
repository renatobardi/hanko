package db

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

// semVersoes esconde do goose os arquivos de migration cuja versão está em skip, simulando um banco que recebeu
// só parte delas.
type semVersoes struct {
	fs.FS
	skip map[string]bool
}

func (f semVersoes) ReadDir(name string) ([]fs.DirEntry, error) {
	es, err := fs.ReadDir(f.FS, name)
	if err != nil {
		return nil, err
	}
	out := es[:0:0]
	for _, e := range es {
		if f.skip[strings.SplitN(e.Name(), "_", 2)[0]] {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (f semVersoes) Open(name string) (fs.File, error) {
	if f.skip[strings.SplitN(name, "_", 2)[0]] {
		return nil, fs.ErrNotExist
	}
	return f.FS.Open(name)
}

// O prd já estava na 00100 quando o main passou a trazer as 00050, 00080 e 00090 (faixas de outros marcos).
// Migrate precisa aplicar as que faltam em vez de recusar com "missing (out-of-order) migrations".
func TestMigrateAplicaMigrationsForaDeOrdem(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, testURL(t), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}

	// estado antigo: tudo menos as três intermediárias e as posteriores à 00100.
	skip := map[string]bool{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		v := strings.SplitN(e.Name(), "_", 2)[0]
		if v == "00050" || v == "00080" || v == "00090" || v > "00100" {
			skip[v] = true
		}
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close() //nolint:errcheck // wrapper; o pool segue aberto
	old, err := goose.NewProvider(goose.DialectPostgres, sqlDB, semVersoes{FS: migrations.FS, skip: skip})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Up(ctx); err != nil {
		t.Fatalf("estado antigo: %v", err)
	}
	var top int
	if err := pool.QueryRow(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&top); err != nil || top != 100 {
		t.Fatalf("banco devia estar na 100, está na %d (err=%v)", top, err)
	}

	if err := Migrate(ctx, pool, testLogger()); err != nil {
		t.Fatalf("Migrate sobre banco na 00100: %v", err)
	}
	for _, v := range []int{50, 80, 90} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id = $1 AND is_applied", v).Scan(&n); err != nil || n != 1 {
			t.Fatalf("migration %d aplicada %d vezes (err=%v)", v, n, err)
		}
	}
}
