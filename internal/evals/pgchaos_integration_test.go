//go:build integration

package evals

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/testdb"
)

// Chaos de infraestrutura: com o banco fora, o pool falha; ao voltar, o MESMO pool se recupera sem reinício.
func TestChaosBancoForaERecuperacao(t *testing.T) {
	pool := testdb.New(t) // também pula sem HANKO_TEST_DATABASE_URL
	url := os.Getenv(testdb.EnvURL)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// pool próprio e pequeno: o do testdb é compartilhado com o Cleanup
	victim, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	if err := Ping(ctx, victim); err != nil {
		t.Fatalf("antes da falha: %v", err)
	}
	_ = pool

	inj := PGInjector{DatabaseURL: url}
	err = RunWithInjector(ctx, inj, func(ctx context.Context) error {
		// conexões abertas foram encerradas e as novas são recusadas
		for i := 0; i < 3; i++ {
			if err := Ping(ctx, victim); err == nil {
				t.Errorf("tentativa %d: o banco deveria estar fora", i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// restaurado: o pool volta sozinho (pode haver uma conexão morta no pool; uma tentativa extra a descarta)
	var last error
	for i := 0; i < 5; i++ {
		if last = Ping(ctx, victim); last == nil {
			break
		}
	}
	if last != nil {
		t.Fatalf("o pool não se recuperou: %v", last)
	}
}

func TestPGInjectorRecusaBancoQueNaoEDeTeste(t *testing.T) {
	err := PGInjector{DatabaseURL: "postgres://u@h:5432/hanko_prod"}.Inject(context.Background())
	if err == nil {
		t.Fatal("derrubaria um banco que não é de teste")
	}
}
