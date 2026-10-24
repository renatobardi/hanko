package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

// A 00203 (ADR-0068/0069/0070) é só contrato: cria tabelas e colunas sem mexer nos segredos que já existem, as
// constraints recusam campo, concessão e envio inválidos, e o Down volta ao schema anterior sem perder segredo.
func TestMigration00203(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	sdb := stdlib.OpenDBFromPool(pool)
	defer sdb.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sdb, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 202); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustFail := func(why, q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err == nil {
			t.Fatalf("%s: a constraint deveria recusar: %s", why, q)
		}
	}
	const (
		co  = "00000000-0000-0000-0000-000000000001"
		uid = "00000000-0000-0000-0000-0000000000d1"
		sec = "00000000-0000-0000-0000-0000000000a1"
	)
	mustExec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, co)
	mustExec(`INSERT INTO users (id, email, provider, password_hash) VALUES ($1, 'a@b.c', 'local', 'x')`, uid)
	mustExec(`INSERT INTO sessions (id, user_id, token_hash, expires_at, absolute_expires_at) VALUES
		('00000000-0000-0000-0000-0000000000e1', $1, 'h', now() + interval '1 day', now() + interval '90 days')`, uid)
	mustExec(`INSERT INTO secrets (id, company_id, name, value_enc, created_by_type) VALUES ($1, $2, 'gh', 'enc', 'human')`, sec, co)

	if _, err := p.UpTo(ctx, 203); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM secret_fields`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("campos = %d (%v): o contrato não copia dado (ADR-0068 §10)", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM secrets WHERE value_enc = 'enc'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("segredo existente = %d (%v), quer intacto", n, err)
	}

	field := func(id, name, kind string, sensitive bool, provider, enc, ext, blob string) string {
		return `INSERT INTO secret_fields (id, company_id, secret_id, name, kind, sensitive, provider, value_enc, external_ref, blob_key)
			VALUES ('` + id + `', '` + co + `', '` + sec + `', '` + name + `', '` + kind + `', ` + map[bool]string{true: "true", false: "false"}[sensitive] +
			`, '` + provider + `', '` + enc + `', '` + ext + `', '` + blob + `')`
	}
	mustExec(field("00000000-0000-0000-0000-0000000000f1", "password", "text", true, "local", "x", "", ""))
	mustExec(field("00000000-0000-0000-0000-0000000000f2", "username", "text", false, "local", "x", "", ""))
	mustExec(field("00000000-0000-0000-0000-0000000000f3", "totp", "totp", true, "local", "x", "", ""))
	mustExec(field("00000000-0000-0000-0000-0000000000f4", "kubeconfig", "file", true, "local", "", "", "blob/1"))
	mustExec(field("00000000-0000-0000-0000-0000000000f5", "token", "text", true, "vault", "", "kv/gh#token", ""))
	mustFail("nome repetido no segredo", field("00000000-0000-0000-0000-0000000000f6", "password", "text", true, "local", "x", "", ""))
	mustFail("totp visível", field("00000000-0000-0000-0000-0000000000f7", "seed", "totp", false, "local", "x", "", ""))
	mustFail("arquivo sem blob", field("00000000-0000-0000-0000-0000000000f8", "pem", "file", true, "local", "x", "", ""))
	mustFail("arquivo no cofre externo", field("00000000-0000-0000-0000-0000000000f9", "pem", "file", true, "vault", "", "kv/x", ""))
	mustFail("local sem valor", field("00000000-0000-0000-0000-0000000000fa", "empty", "text", true, "local", "", "", ""))
	mustFail("nome inválido", field("00000000-0000-0000-0000-0000000000fb", "1bad", "text", true, "local", "x", "", ""))

	const col = "00000000-0000-0000-0000-0000000000c1"
	mustExec(`INSERT INTO secret_collections (id, company_id, name, is_default, created_by_type) VALUES ($1, $2, 'padrão', true, 'system')`, col, co)
	mustFail("duas coleções padrão", `INSERT INTO secret_collections (id, company_id, name, is_default, created_by_type)
		VALUES ('00000000-0000-0000-0000-0000000000c2', $1, 'outra', true, 'system')`, co)
	mustFail("nome de coleção com espaço na ponta", `INSERT INTO secret_collections (id, company_id, name, created_by_type)
		VALUES ('00000000-0000-0000-0000-0000000000c3', $1, ' infra', 'human')`, co)
	mustExec(`INSERT INTO secret_collection_items (company_id, collection_id, secret_id) VALUES ($1, $2, $3)`, co, col, sec)
	grant := func(id, subject string, subjectID any, perm string) (string, []any) {
		return `INSERT INTO secret_collection_grants (id, company_id, collection_id, subject_type, subject_id, permission, created_by_type)
			VALUES ($1, $2, $3, $4, $5, $6, 'human')`, []any{id, co, col, subject, subjectID, perm}
	}
	q, a := grant("00000000-0000-0000-0000-000000000201", "all_personas", nil, "use")
	mustExec(q, a...)
	q, a = grant("00000000-0000-0000-0000-000000000202", "all_personas", nil, "use")
	mustFail("concessão repetida a todas as personas", q, a...)
	q, a = grant("00000000-0000-0000-0000-000000000203", "user", uid, "use")
	mustFail("humano não recebe use", q, a...)
	q, a = grant("00000000-0000-0000-0000-000000000204", "persona", nil, "use")
	mustFail("persona sem id", q, a...)
	q, a = grant("00000000-0000-0000-0000-000000000205", "user", uid, "manage")
	mustExec(q, a...)

	mustExec(`INSERT INTO secret_sends (id, company_id, public_id, kind, ciphertext, expires_at, created_by_type)
		VALUES ('00000000-0000-0000-0000-0000000000b1', $1, 'AAAAAAAAAAAAAAAAAAAAAA', 'text', 'ct', now() + interval '7 days', 'human')`, co)
	mustFail("envio com mais de 30 dias", `INSERT INTO secret_sends (id, company_id, public_id, kind, ciphertext, expires_at, created_by_type)
		VALUES ('00000000-0000-0000-0000-0000000000b2', $1, 'BAAAAAAAAAAAAAAAAAAAAA', 'text', 'ct', now() + interval '31 days', 'human')`, co)
	mustFail("senha sem verificador", `INSERT INTO secret_sends (id, company_id, public_id, kind, ciphertext, kdf_salt, expires_at, created_by_type)
		VALUES ('00000000-0000-0000-0000-0000000000b3', $1, 'CAAAAAAAAAAAAAAAAAAAAA', 'text', 'ct', 'salt', now() + interval '1 day', 'human')`, co)

	mustExec(`UPDATE sessions SET reauth_at = now(), reauth_method = 'passkey'`)
	mustFail("método de reautenticação desconhecido", `UPDATE sessions SET reauth_method = 'sms'`)
	mustExec(`INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, opened_at, last_seen_at)
		VALUES ('00000000-0000-0000-0000-0000000000a9', $1, 'secret_rotation_due', 'secret', $2, 'warning', 'rotacionar', now(), now())`, co, sec)

	if _, err := p.DownTo(ctx, 202); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM secrets`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("segredos depois do Down = %d (%v), quer 1", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alerts`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("alertas de segredo depois do Down = %d (%v), quer 0", n, err)
	}
	if _, err := p.UpTo(ctx, 203); err != nil {
		t.Fatalf("Up de novo depois do Down: %v", err)
	}
}
