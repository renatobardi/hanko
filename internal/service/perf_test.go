package service_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/testdb"
)

// As listagens escolhem a página só pelas chaves de ordenação e depois juntam o resto (docs/PERF.md). Percorrer a
// lista em páginas pequenas tem de dar exatamente a lista inteira, na mesma ordem: pendentes primeiro (a mais antiga
// primeiro), depois as decididas (a decidida mais recentemente primeiro).
func TestListGatesPaginacaoEstavel(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	t0 := time.Now().UTC().Add(-time.Hour)
	tick := 0
	e.svc.Now = func() time.Time { tick++; return t0.Add(time.Duration(tick) * time.Minute) }

	var gates []service.GateView
	for i := 0; i < 7; i++ {
		tv := e.task(t, service.CreateTaskInput{Title: "t"})
		g, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "g", TaskID: &tv.ID})
		if err != nil {
			t.Fatal(err)
		}
		gates = append(gates, g)
	}
	// decide 1, 5 e 3 (nessa ordem); ficam pendentes 0, 2, 4 e 6
	for _, i := range []int{1, 5, 3} {
		if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gates[i].ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
			t.Fatal(err)
		}
	}
	want := []domain.ID{gates[0].ID, gates[2].ID, gates[4].ID, gates[6].ID, gates[3].ID, gates[5].ID, gates[1].ID}

	all, err := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(want, gateIDs(all)); d != "" {
		t.Fatalf("ordem (-want +got):\n%s", d)
	}
	var paged []domain.ID
	for off := 0; ; off += 3 {
		page, err := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Limit: 3, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		paged = append(paged, gateIDs(page)...)
	}
	if d := cmp.Diff(want, paged); d != "" {
		t.Fatalf("páginas de 3 (-want +got):\n%s", d)
	}
	// o filtro vale antes da página: só as decididas, com o offset aplicado sobre elas
	dec, err := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GateApproved}, Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff([]domain.ID{gates[5].ID, gates[1].ID}, gateIDs(dec)); d != "" {
		t.Fatalf("decididas (-want +got):\n%s", d)
	}
}

func gateIDs(gs []service.GateView) []domain.ID {
	out := make([]domain.ID, len(gs))
	for i, g := range gs {
		out[i] = g.ID
	}
	return out
}

// Tasks: updated_at DESC, number DESC; as páginas encadeiam sem repetir nem perder linhas, inclusive com
// updated_at igual (desempate por number) e com filtro.
func TestListTasksPaginacaoEstavel(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	t0 := time.Now().UTC().Add(-time.Hour)
	var keys []string
	// duas tasks por instante: o desempate é o number
	for i := 0; i < 8; i++ {
		at := t0.Add(time.Duration(i/2) * time.Minute)
		e.svc.Now = func() time.Time { return at }
		in := service.CreateTaskInput{Title: "t"}
		if i%2 == 0 {
			in.PersonaID = &p.ID
		}
		keys = append(keys, e.task(t, in).Key)
	}
	// mais recente primeiro; no mesmo instante, o de maior number primeiro
	want := []string{keys[7], keys[6], keys[5], keys[4], keys[3], keys[2], keys[1], keys[0]}
	var got []string
	for off := 0; ; off += 3 {
		page, err := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{Limit: 3, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, tv := range page {
			got = append(got, tv.Key)
		}
	}
	if d := cmp.Diff(want, got); d != "" {
		t.Fatalf("páginas de 3 (-want +got):\n%s", d)
	}
	byPersona, err := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{PersonaID: &p.ID, Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	var gotP []string
	for _, tv := range byPersona {
		gotP = append(gotP, tv.Key)
	}
	if d := cmp.Diff([]string{keys[4], keys[2]}, gotP); d != "" {
		t.Fatalf("filtro por persona (-want +got):\n%s", d)
	}
}

// planRecorder é um store.DBTX que, em vez de executar a consulta, pede o plano dela (EXPLAIN com os mesmos
// parâmetros) numa conexão com varredura sequencial desligada: mostra que a consulta REAL do app consegue usar o índice.
type planRecorder struct {
	conn  *pgx.Conn
	plans []string
	err   error
}

var errPlanned = errors.New("planned")

func (r *planRecorder) explain(ctx context.Context, sql string, args []any) {
	rows, err := r.conn.Query(ctx, "EXPLAIN "+sql, args...)
	if err != nil {
		r.err = err
		return
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			r.err = err
			return
		}
		b.WriteString(l + "\n")
	}
	r.err = rows.Err()
	r.plans = append(r.plans, b.String())
}

func (r *planRecorder) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.explain(ctx, sql, args)
	return pgconn.CommandTag{}, errPlanned
}

func (r *planRecorder) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	r.explain(ctx, sql, args)
	return nil, errPlanned
}

func (r *planRecorder) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	r.explain(ctx, sql, args)
	return errRow{}
}

type errRow struct{}

func (errRow) Scan(...any) error { return errPlanned }

// Cada consulta quente tem de poder usar o índice criado para ela na 00013. Pega o caso em que alguém muda a
// expressão da consulta (ou do índice) e os dois deixam de casar: a lentidão só apareceria em produção.
func TestConsultasQuentesUsamIndices(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	if _, err := c.Exec(ctx, "SET enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	rec := &planRecorder{conn: c.Conn()}
	q := store.New(rec)
	co, pe := domain.ID{1}, domain.ID{2}
	now := time.Now().UTC()
	var noIDs []string

	tests := []struct {
		name  string
		run   func()
		index string
	}{
		{"wake: última run da task", func() {
			_, _ = q.ListWakeCandidates(ctx, store.ListWakeCandidatesParams{BudgetCutoff: now, Now: now, StaleBefore: now, Lim: 50})
		}, "runs_task_created_idx"},
		{"wake: tasks trabalháveis", func() {
			_, _ = q.ListWakeCandidates(ctx, store.ListWakeCandidatesParams{BudgetCutoff: now, Now: now, StaleBefore: now, Lim: 50})
		}, "tasks_workable_idx"},
		{"silenciosas: atividade da persona", func() {
			_, _ = q.ListSilentPersonas(ctx, store.ListSilentPersonasParams{Now: now, Factor: 3})
		}, "runs_persona_activity_idx"},
		{"silenciosas: mudança humana", func() {
			_, _ = q.ListSilentPersonas(ctx, store.ListSilentPersonasParams{Now: now, Factor: 3})
		}, "tasks_persona_human_change_idx"},
		{"runs por task", func() {
			_, _ = q.ListRunViews(ctx, store.ListRunViewsParams{CompanyID: co, TaskID: &pe, Lim: 50})
		}, "runs_task_created_idx"},
		{"runs superadas da caixa de entrada", func() {
			_, _ = q.ListFailedRuns(ctx, store.ListFailedRunsParams{CompanyID: co, Since: now, Lim: 50})
		}, "runs_task_created_idx"},
		{"tasks por updated_at", func() {
			_, _ = q.ListTaskViews(ctx, store.ListTaskViewsParams{CompanyID: co, Statuses: noIDs, Lim: 100})
		}, "tasks_company_updated_idx"},
		{"runs sem nome", func() { _, _ = q.ListRunsWithoutName(ctx, 500) }, "runs_unnamed_idx"},
		{"runs travadas: último evento por seq", func() { _, _ = q.ListStuckRuns(ctx, now) }, "run_events_run_id_seq_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec.plans, rec.err = nil, nil
			tt.run()
			if rec.err != nil {
				t.Fatal(rec.err)
			}
			if len(rec.plans) != 1 || !strings.Contains(rec.plans[0], tt.index) {
				t.Fatalf("o plano não usa %s:\n%s", tt.index, strings.Join(rec.plans, "\n"))
			}
		})
	}
}

// A busca de run por nome compara lower(name); precisa de dados para o planner preferir o índice de nome ao que já
// vem ordenado por data.
func TestBuscaDeRunPorNomeUsaIndice(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO companies (id, slug, name) VALUES ('11111111-1111-4111-8111-111111111111', 'a', 'A')`,
		`INSERT INTO personas (id, company_id, slug, name, agent) VALUES ('22222222-2222-4222-8222-222222222222', '11111111-1111-4111-8111-111111111111', 'p', 'P', 'fake')`,
		`INSERT INTO tasks (id, company_id, number, title, created_by_type)
		   SELECT gen_random_uuid(), '11111111-1111-4111-8111-111111111111', n, 't', 'human' FROM generate_series(1, 3000) n`,
		`INSERT INTO runs (id, company_id, task_id, persona_id, status, trigger, agent, name, created_at)
		   SELECT gen_random_uuid(), t.company_id, t.id, '22222222-2222-4222-8222-222222222222', 'done', 'assign', 'fake', 'Run_' || t.number, now() - t.number * interval '1 minute' FROM tasks t`,
		`ANALYZE`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	c, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	rec := &planRecorder{conn: c.Conn()}
	name := "RUN_42" // a consulta compara lower(name) = lower(?)
	_, _ = store.New(rec).ListRunViews(ctx, store.ListRunViewsParams{CompanyID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Name: &name, Lim: 50})
	if rec.err != nil {
		t.Fatal(rec.err)
	}
	if len(rec.plans) != 1 || !strings.Contains(rec.plans[0], "runs_company_lower_name_idx") {
		t.Fatalf("o plano não usa runs_company_lower_name_idx:\n%s", strings.Join(rec.plans, "\n"))
	}
}

// 00013: sobe com os índices e o fillfactor, e o Down desfaz tudo (inclusive recriando o índice que a Up removeu).
func TestMigration00013Indices(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	indexes := func() map[string]bool {
		rows, err := pool.Query(ctx, `SELECT indexname FROM pg_indexes WHERE schemaname = 'public'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		m := map[string]bool{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			m[n] = true
		}
		return m
	}
	fillfactor := func() string {
		var opts []string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(reloptions, '{}') FROM pg_class WHERE oid = 'public.runs'::regclass`).Scan(&opts); err != nil {
			t.Fatal(err)
		}
		return strings.Join(opts, ",")
	}
	novos := []string{"runs_task_created_idx", "runs_persona_activity_idx", "runs_company_lower_name_idx", "runs_unnamed_idx",
		"tasks_company_updated_idx", "tasks_persona_human_change_idx", "tasks_workable_idx"}

	before := indexes()
	if !before["tasks_persona_idx"] || fillfactor() != "" {
		t.Fatalf("estado antes da 00013 inesperado: tasks_persona_idx=%v fillfactor=%q", before["tasks_persona_idx"], fillfactor())
	}
	if _, err := p.UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	up := indexes()
	for _, n := range novos {
		if !up[n] {
			t.Errorf("Up: falta o índice %s", n)
		}
	}
	if up["tasks_persona_idx"] {
		t.Error("Up: tasks_persona_idx (redundante) deveria ter sido removido")
	}
	if fillfactor() != "fillfactor=80" {
		t.Errorf("Up: fillfactor = %q", fillfactor())
	}
	if _, err := p.DownTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	down := indexes()
	for _, n := range novos {
		if down[n] {
			t.Errorf("Down: o índice %s ficou", n)
		}
	}
	if !down["tasks_persona_idx"] || fillfactor() != "" {
		t.Errorf("Down: tasks_persona_idx=%v fillfactor=%q", down["tasks_persona_idx"], fillfactor())
	}
	if d := cmp.Diff(before, down); d != "" {
		t.Errorf("os índices depois do Down diferem dos de antes (-antes +depois):\n%s", d)
	}
}
