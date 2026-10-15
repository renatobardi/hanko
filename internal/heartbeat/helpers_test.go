package heartbeat

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func statDir(p string) (os.FileInfo, error) { return os.Stat(p) }

// lazyPool returns a pool that never connects unless used (river only needs it non-nil).
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}
