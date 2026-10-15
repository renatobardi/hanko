package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

// A 00010 corrige o model_source das runs anteriores à 00008 cuja persona não tem modelo, não toca as
// posteriores nem as de persona com modelo, e abre o histórico de períodos das assinaturas.
func TestMigration00010(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO companies (id, slug, name) VALUES ('00000000-0000-0000-0000-000000000001', 'c', 'C')`)
	mustExec(`INSERT INTO personas (id, company_id, slug, name, agent, model) VALUES
		('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-000000000001', 'sem', 'Sem', 'claude', ''),
		('00000000-0000-0000-0000-0000000000a2', '00000000-0000-0000-0000-000000000001', 'com', 'Com', 'claude', 'm1')`)
	mustExec(`INSERT INTO tasks (id, company_id, number, title, created_by_type) VALUES
		('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-000000000001', 1, 'T', 'human')`)
	run := func(id, persona, source, when string) {
		mustExec(`INSERT INTO runs (id, company_id, task_id, persona_id, trigger, agent, model, model_source, status, created_at)
			VALUES ($1, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-0000000000b1', $2, 'manual', 'claude', 'reportado', $3, 'done', `+when+`)`,
			id, persona, source)
	}
	run("00000000-0000-0000-0000-0000000000c1", "00000000-0000-0000-0000-0000000000a1", "persona", "now() - interval '1 day'")  // fixed
	run("00000000-0000-0000-0000-0000000000c2", "00000000-0000-0000-0000-0000000000a2", "persona", "now() - interval '1 day'")  // persona has a model
	run("00000000-0000-0000-0000-0000000000c3", "00000000-0000-0000-0000-0000000000a1", "persona", "now() + interval '1 hour'") // after 00008
	mustExec(`INSERT INTO subscriptions (id, company_id, name, agent, monthly_usd, active, ended_at, created_at) VALUES
		('00000000-0000-0000-0000-0000000000d1', '00000000-0000-0000-0000-000000000001', 'on', 'claude', 10, true, NULL, '2026-01-05'),
		('00000000-0000-0000-0000-0000000000d2', '00000000-0000-0000-0000-000000000001', 'off', 'claude', 10, false, '2026-02-10', '2026-01-05'),
		('00000000-0000-0000-0000-0000000000d3', '00000000-0000-0000-0000-000000000001', 'never', 'claude', 10, false, NULL, '2026-01-05')`)

	if _, err := p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"00000000-0000-0000-0000-0000000000c1": "harness_default",
		"00000000-0000-0000-0000-0000000000c2": "persona",
		"00000000-0000-0000-0000-0000000000c3": "persona",
	} {
		var got string
		if err := pool.QueryRow(ctx, `SELECT model_source FROM runs WHERE id = $1`, id).Scan(&got); err != nil || got != want {
			t.Errorf("run %s: model_source = %q (%v), want %q", id[len(id)-2:], got, err, want)
		}
	}
	for sub, want := range map[string][2]int{ // [periods, open periods]
		"00000000-0000-0000-0000-0000000000d1": {1, 1},
		"00000000-0000-0000-0000-0000000000d2": {1, 0},
		"00000000-0000-0000-0000-0000000000d3": {0, 0},
	} {
		var n, open int
		if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE ended_at IS NULL) FROM subscription_periods WHERE subscription_id = $1`, sub).Scan(&n, &open); err != nil ||
			n != want[0] || open != want[1] {
			t.Errorf("sub %s: periods=%d open=%d (%v), want %v", sub[len(sub)-2:], n, open, err, want)
		}
	}
}
