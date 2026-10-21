package service_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/testdb"
)

// A migração 00050 é aditiva: a auditoria anterior ganha origem e ator no próprio Up, os gates e as aprovações seguem
// intactos, o Down desfaz tudo o que ela criou e o Up volta a aplicar. A constraint gates_kind_check não muda.
func TestMigration00050IsAdditiveAndReversible(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 49); err != nil {
		t.Fatal(err)
	}
	const co, pe, in, pr, gw, gt = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555",
		"66666666-6666-4666-8666-666666666666"
	for _, q := range []string{
		`INSERT INTO companies (id, slug, name, task_prefix) VALUES ('` + co + `','a','A','AAA')`,
		`INSERT INTO personas (id, company_id, slug, name, agent) VALUES ('` + pe + `','` + co + `','p','P','fake')`,
		`INSERT INTO integrations (id, company_id, kind, slug, name, config, created_by_type) VALUES ('` + in + `','` + co + `','mcp','docs','Docs','{"url":"http://x"}','human')`,
		`INSERT INTO tool_profiles (id, company_id, slug, name, created_by_type) VALUES ('` + pr + `','` + co + `','base','Base','human')`,
		`INSERT INTO tool_gateways (id, company_id, slug, name, profile_id, created_by_type) VALUES ('` + gw + `','` + co + `','ide','IDE','` + pr + `','human')`,
		`INSERT INTO gates (id, company_id, kind, status, title, persona_id, requested_by_type) VALUES ('` + gt + `','` + co + `','tool','pending','t','` + pe + `','persona')`,
		`INSERT INTO tool_call_approvals (id, company_id, gate_id, integration_id, tool, args_sha256, persona_id)
		 VALUES ('77777777-7777-4777-8777-777777777777','` + co + `','` + gt + `','` + in + `','apagar','abc','` + pe + `')`,
		`INSERT INTO tool_invocations (id, company_id, persona_id, integration_id, integration_slug, tool, decision, status)
		 VALUES ('88888888-8888-4888-8888-888888888888','` + co + `','` + pe + `','` + in + `','docs','ler','allow','ok')`,
		`INSERT INTO tool_invocations (id, company_id, gateway_id, integration_id, integration_slug, tool, decision, status)
		 VALUES ('99999999-9999-4999-8999-999999999999','` + co + `','` + gw + `','` + in + `','docs','ler','allow','ok')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var kindCheck string
	if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'gates_kind_check'`).Scan(&kindCheck); err != nil {
		t.Fatal(err)
	}

	if _, err := p.UpTo(ctx, 50); err != nil {
		t.Fatal(err)
	}
	// a auditoria anterior ganhou origem e ator
	rows, err := pool.Query(ctx, `SELECT id::text, source, coalesce(actor_type,''), coalesce(actor_id::text,''), result_preview FROM tool_invocations ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type inv struct{ id, source, actorType, actorID, preview string }
	var got []inv
	for rows.Next() {
		var i inv
		if err := rows.Scan(&i.id, &i.source, &i.actorType, &i.actorID, &i.preview); err != nil {
			t.Fatal(err)
		}
		got = append(got, i)
	}
	rows.Close()
	if len(got) != 2 || got[0].source != "run" || got[0].actorType != "persona" || got[0].actorID != pe ||
		got[1].source != "gateway" || got[1].actorType != "system" || got[1].actorID != "" || got[0].preview != "" {
		t.Fatalf("auditoria migrada: %+v", got)
	}
	// o gate e a aprovação seguem; a constraint de kind é a mesma
	var native string
	var integ *string
	if err := pool.QueryRow(ctx, `SELECT native_tool, integration_id::text FROM tool_call_approvals`).Scan(&native, &integ); err != nil || native != "" || integ == nil {
		t.Fatalf("aprovação anterior: %q %v %v", native, integ, err)
	}
	var kindCheckAfter string
	_ = pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'gates_kind_check'`).Scan(&kindCheckAfter)
	if kindCheckAfter != kindCheck {
		t.Fatalf("gates_kind_check mudou:\n%s\n%s", kindCheck, kindCheckAfter)
	}
	// o alvo da aprovação é exatamente um: integração ou tool nativa
	if _, err := pool.Exec(ctx, `UPDATE tool_call_approvals SET native_tool = 'Bash'`); err == nil {
		t.Fatal("integração e tool nativa ao mesmo tempo deveria ser recusado")
	}
	if _, err := pool.Exec(ctx, `UPDATE tool_call_approvals SET integration_id = NULL`); err == nil {
		t.Fatal("sem integração e sem tool nativa deveria ser recusado")
	}
	// formas inválidas de política são recusadas pelo banco
	for _, q := range []string{
		`INSERT INTO tool_policies (id, company_id, scope, target, mode, created_by_type) VALUES (gen_random_uuid(),'` + co + `','persona','all','off','human')`,
		`INSERT INTO tool_policies (id, company_id, scope, target, tool_name, mode, created_by_type) VALUES (gen_random_uuid(),'` + co + `','company','all','x','off','human')`,
		`INSERT INTO tool_policies (id, company_id, scope, target, mode, created_by_type) VALUES (gen_random_uuid(),'` + co + `','company','tool','off','human')`,
		`INSERT INTO tool_policies (id, company_id, scope, target, mode, created_by_type) VALUES (gen_random_uuid(),'` + co + `','company','all','talvez','human')`,
	} {
		if _, err := pool.Exec(ctx, q); err == nil {
			t.Fatalf("o banco deveria recusar: %s", q)
		}
	}
	ok := `INSERT INTO tool_policies (id, company_id, scope, target, mode, created_by_type) VALUES (gen_random_uuid(),'` + co + `','company','all','ask','human')`
	if _, err := pool.Exec(ctx, ok); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, ok); err == nil {
		t.Fatal("a chave (escopo, alvo...) é única")
	}

	if _, err := p.DownTo(ctx, 49); err != nil {
		t.Fatalf("down: %v", err)
	}
	var reg *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.tool_policies')::text`).Scan(&reg); err != nil || reg != nil {
		t.Fatalf("o down deveria remover tool_policies (%v %v)", reg, err)
	}
	var cols int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND
		((table_name='gates' AND column_name='expires_at') OR (table_name='tool_invocations' AND column_name IN ('source','actor_type','actor_id','result_preview','policy_source'))
		 OR (table_name='tool_call_approvals' AND column_name='native_tool'))`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("colunas restantes: %d %v", cols, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tool_invocations`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("o down não mexe nos dados anteriores: %d %v", n, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
}
