package service_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/testdb"
)

// A migration da fatia 1 do cofre (00204, ADR-0068 §1.7 e §2.6) copia cada segredo para o campo `value` (AAD legado),
// cria a coleção `padrão` com todos os segredos da empresa, concede a all_personas e grava os fatos; é idempotente e
// reversível. Segredo de usuário ganha campo, mas não entra em coleção.
func TestMigrationCofreCampos(t *testing.T) {
	url := os.Getenv(testdb.EnvURL)
	if url == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definida")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	// Estado "atual": tudo até a fatia 0 aplicado, segredos e personas vivendo no modelo antigo.
	if _, err := p.UpTo(ctx, 203); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	one := func(q string, args ...any) string {
		t.Helper()
		var v string
		if err := pool.QueryRow(ctx, q, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}
	exec(`INSERT INTO companies (id, slug, name) VALUES ('11111111-1111-4111-8111-111111111111','acme','Acme')`)
	exec(`INSERT INTO users (id, email, provider, password_hash) VALUES ('22222222-2222-4222-8222-222222222222','u@acme.io','local','x')`)
	exec(`INSERT INTO personas (id, company_id, slug, name, agent) VALUES
	      ('33333333-3333-4333-8333-333333333333','11111111-1111-4111-8111-111111111111','eng','Eng','fake')`)
	exec(`INSERT INTO secrets (id, company_id, name, description, provider, value_enc, external_ref, version,
	                          created_by_type) VALUES
	      ('44444444-4444-4444-8444-444444444441','11111111-1111-4111-8111-111111111111','gh','GitHub','local','ENC-gh','',3,'human'),
	      ('44444444-4444-4444-8444-444444444442','11111111-1111-4111-8111-111111111111','infra','Infra','vault','','kv/infra#pw',1,'human')`)
	exec(`INSERT INTO secrets (id, company_id, scope, owner_user_id, name, provider, value_enc, created_by_type) VALUES
	      ('44444444-4444-4444-8444-444444444443','11111111-1111-4111-8111-111111111111','user',
	       '22222222-2222-4222-8222-222222222222','meu','local','ENC-meu','human')`)

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up com 00204: %v", err)
	}

	// Campo `value` de cada segredo: mesmo material, AAD legado (aad_version = 0), mesma versão.
	for id, wantEnc := range map[string]string{
		"44444444-4444-4444-8444-444444444441": "ENC-gh",
		"44444444-4444-4444-8444-444444444443": "ENC-meu",
	} {
		var enc, ext string
		var aad, ver int
		if err := pool.QueryRow(ctx,
			`SELECT value_enc, aad_version, version, external_ref FROM secret_fields WHERE secret_id = $1 AND name = 'value'`, id).
			Scan(&enc, &aad, &ver, &ext); err != nil {
			t.Fatalf("campo value de %s: %v", id, err)
		}
		if enc != wantEnc || aad != 0 || ext != "" {
			t.Fatalf("campo value de %s = enc %q aad %d ext %q", id, enc, aad, ext)
		}
		if id == "44444444-4444-4444-8444-444444444441" && ver != 3 {
			t.Fatalf("versão do campo = %d, want 3", ver)
		}
	}
	var ext string
	if err := pool.QueryRow(ctx,
		`SELECT external_ref FROM secret_fields WHERE secret_id = '44444444-4444-4444-8444-444444444442' AND name = 'value'`).
		Scan(&ext); err != nil || ext != "kv/infra#pw" {
		t.Fatalf("campo value do vault = %q, %v", ext, err)
	}

	// Coleção `padrão` com os segredos da empresa (o de usuário fica fora), concedida a all_personas.
	def := one(`SELECT id FROM secret_collections WHERE company_id = '11111111-1111-4111-8111-111111111111' AND is_default`)
	if n := one(`SELECT count(*)::text FROM secret_collection_items WHERE collection_id = $1`, def); n != "2" {
		t.Fatalf("itens na padrão = %s, want 2", n)
	}
	if n := one(`SELECT count(*)::text FROM secret_collection_grants WHERE collection_id = $1 AND subject_type = 'all_personas'
	             AND permission = 'use' AND subject_id IS NULL`, def); n != "1" {
		t.Fatalf("concessão all_personas = %s, want 1", n)
	}

	// Fatos da migração, sem valor.
	for verb, n := range map[string]string{
		"secret.fields_migrated": "1",
		"collection.created":     "1",
		"collection.granted":     "1",
	} {
		if got := one(`SELECT count(*)::text FROM activity_events WHERE company_id =
		               '11111111-1111-4111-8111-111111111111' AND verb = $1`, verb); got != n {
			t.Fatalf("eventos %s = %s, want %s", verb, got, n)
		}
	}
	if v := one(`SELECT data->>'fields' FROM activity_events WHERE verb = 'secret.fields_migrated'`); v != "3" {
		t.Fatalf("fato secret.fields_migrated diz %s campos, want 3 (todos os escopos)", v)
	}

	// Idempotente: o bloco Up de novo não move nada nem grava fato novo.
	up, err := migrations.FS.ReadFile("00204_cofre_campos.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	sql = sql[strings.Index(sql, "-- +goose Up")+len("-- +goose Up"):]
	sql = sql[:strings.Index(sql, "-- +goose Down")]
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatalf("reexecutar o bloco Up: %v", err)
	}
	if n := one(`SELECT count(*)::text FROM secret_fields`); n != "3" {
		t.Fatalf("campos após reexecução = %s, want 3", n)
	}
	if n := one(`SELECT count(*)::text FROM secret_collection_items`); n != "2" {
		t.Fatalf("itens após reexecução = %s, want 2", n)
	}
	if n := one(`SELECT count(*)::text FROM activity_events WHERE verb IN
	             ('secret.fields_migrated','collection.created','collection.granted')`); n != "3" {
		t.Fatalf("eventos após reexecução = %s, want 3", n)
	}

	// Reversível: Down remove a cópia e a padrão; segredos e colunas antigas ficam.
	if _, err := p.DownTo(ctx, 203); err != nil {
		t.Fatalf("down para 203: %v", err)
	}
	for q, want := range map[string]string{
		`SELECT count(*)::text FROM secret_fields`:            "0",
		`SELECT count(*)::text FROM secret_collections`:       "0",
		`SELECT count(*)::text FROM secret_collection_items`:  "0",
		`SELECT count(*)::text FROM secret_collection_grants`: "0",
		`SELECT count(*)::text FROM secrets`:                  "3",
		`SELECT value_enc FROM secrets WHERE name = 'gh'`:     "ENC-gh",
	} {
		if got := one(q); got != want {
			t.Fatalf("%s = %s, want %s", q, got, want)
		}
	}
}
