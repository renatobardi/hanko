package sandbox_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/sandbox"
)

func TestSlugAndNames(t *testing.T) {
	cases := map[string]string{
		"feature/Nova Tela":            "feature-nova-tela",
		"  ":                           "dev",
		"":                             "dev",
		"évolução/trilha-w3":           "volu-o-trilha-w3",
		"A--B__C":                      "a-b-c",
		"x/" + strings.Repeat("a", 80): "x-" + strings.Repeat("a", 38),
	}
	for in, want := range cases {
		if got := sandbox.Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, quer %q", in, got, want)
		}
	}
	if got := sandbox.DBName("feature/nova-tela"); got != "hanko_sbx_feature_nova_tela" {
		t.Errorf("DBName = %q", got)
	}
	if got := sandbox.NameOfDB("hanko_sbx_feature_nova_tela"); got != "feature-nova-tela" {
		t.Errorf("NameOfDB = %q", got)
	}
	for _, db := range []string{"hanko", "postgres", "hanko_sbx_", "outro_hanko_sbx_x"} {
		if sandbox.NameOfDB(db) != "" {
			t.Errorf("NameOfDB(%q) deveria ser vazio", db)
		}
	}
	// o nome de um banco nunca passa de 63 bytes
	if n := len(sandbox.DBName(strings.Repeat("z", 200))); n > 63 {
		t.Errorf("nome de banco com %d bytes", n)
	}
}

func TestPortIsDeterministicAndInRange(t *testing.T) {
	for _, n := range []string{"main", "feature-x", "evolucao/trilha-w3", ""} {
		p := sandbox.Port(n)
		if p < sandbox.PortBase || p >= sandbox.PortBase+sandbox.PortSpan || p != sandbox.Port(n) {
			t.Errorf("Port(%q) = %d", n, p)
		}
	}
	if sandbox.Port("Feature X") != sandbox.Port("feature-x") {
		t.Error("o mesmo slug cai na mesma porta")
	}
}

func TestWithDatabase(t *testing.T) {
	got, err := sandbox.WithDatabase("postgres://u:p@localhost:5432/hanko?sslmode=disable", "hanko_sbx_x")
	if err != nil || got != "postgres://u:p@localhost:5432/hanko_sbx_x?sslmode=disable" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "mysql://x/y", "postgres:///db", "not a url"} {
		if _, err := sandbox.WithDatabase(bad, "x"); err == nil {
			t.Errorf("WithDatabase(%q) deveria falhar", bad)
		}
	}
}

func TestBranchFallsBackToDirName(t *testing.T) {
	dir := t.TempDir() // fora de qualquer repositório git
	if got := sandbox.Branch(context.Background(), dir); got == "" {
		t.Fatal("Branch deve devolver algo")
	}
}

// Ciclo completo contra o Postgres de teste: cria, lista, é idempotente, apaga e nunca toca fora do prefixo.
func TestCreateListDrop(t *testing.T) {
	admin := os.Getenv("HANKO_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definida")
	}
	ctx := context.Background()
	name := "teste-ciclo-w3"
	_ = sandbox.Drop(ctx, admin, name)
	t.Cleanup(func() { _ = sandbox.Drop(ctx, admin, name) })

	in, err := sandbox.Create(ctx, admin, name)
	if err != nil {
		t.Fatal(err)
	}
	if in.Database != "hanko_sbx_teste_ciclo_w3" || in.Port != sandbox.Port(name) || !strings.Contains(in.DatabaseURL, "/hanko_sbx_teste_ciclo_w3") {
		t.Fatalf("instância: %+v", in)
	}
	if _, err := sandbox.Create(ctx, admin, name); err != nil { // idempotente
		t.Fatalf("segunda criação: %v", err)
	}
	// o banco criado aceita conexões
	c, err := pgx.Connect(ctx, in.DatabaseURL)
	if err != nil {
		t.Fatalf("conectar na sandbox: %v", err)
	}
	c.Close(ctx)

	all, err := sandbox.List(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range all {
		if x.Name == "teste-ciclo-w3" {
			found = true
		}
		if !strings.HasPrefix(x.Database, sandbox.DBPrefix) {
			t.Errorf("List devolveu um banco fora do prefixo: %s", x.Database)
		}
	}
	if !found {
		t.Fatalf("a sandbox não aparece em List: %+v", all)
	}

	if err := sandbox.Drop(ctx, admin, name); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.Drop(ctx, admin, name); err != nil { // apagar o que não existe não falha
		t.Fatalf("segunda remoção: %v", err)
	}
	all, _ = sandbox.List(ctx, admin)
	for _, x := range all {
		if x.Name == "teste-ciclo-w3" {
			t.Fatal("a sandbox deveria ter sumido")
		}
	}
}
