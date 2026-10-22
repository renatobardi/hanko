package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

// A 00201 (ADR-0064) cria um documento por cadeia antiga (empresa, kind, task, conversa, nome), numera vN como N.0,
// herda projeto e workspace e volta no Down enquanto os dados ainda cabem no modelo antigo.
func TestMigration00201(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 200); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const co = "00000000-0000-0000-0000-000000000001"
	mustExec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, co)
	mustExec(`INSERT INTO workspaces (id, company_id, slug, name, path) VALUES ('00000000-0000-0000-0000-0000000000e1', $1, 'w', 'W', '/w')`, co)
	mustExec(`INSERT INTO projects (id, company_id, slug, name, workspace_id) VALUES
		('00000000-0000-0000-0000-0000000000f1', $1, 'p', 'P', '00000000-0000-0000-0000-0000000000e1')`, co)
	mustExec(`INSERT INTO tasks (id, company_id, number, title, created_by_type, project_id) VALUES
		('00000000-0000-0000-0000-0000000000b1', $1, 1, 'T', 'human', '00000000-0000-0000-0000-0000000000f1')`, co)
	art := func(id, task, name string, version int, kind string) {
		t.Helper()
		var tk any
		if task != "" {
			tk = task
		}
		mustExec(`INSERT INTO artifacts (id, company_id, task_id, project_id, author_type, name, mime, size_bytes, sha256, version, kind, created_at)
			VALUES ($1, $2, $3, CASE WHEN $3::uuid IS NULL THEN NULL ELSE '00000000-0000-0000-0000-0000000000f1'::uuid END, 'human', $4,
			        'text/markdown', 1, repeat('a', 64), $5::int, $6, now() + make_interval(secs => $5::int))`, id, co, tk, name, version, kind)
	}
	const task = "00000000-0000-0000-0000-0000000000b1"
	art("00000000-0000-0000-0000-0000000000a1", task, "plano.md", 1, "artifact")
	art("00000000-0000-0000-0000-0000000000a2", task, "plano.md", 2, "artifact")
	art("00000000-0000-0000-0000-0000000000a3", "", "plano.md", 1, "artifact")
	art("00000000-0000-0000-0000-0000000000a4", task, "foto.png", 1, "attachment")

	if _, err := p.UpTo(ctx, 201); err != nil {
		t.Fatal(err)
	}
	var docs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM artifact_documents`).Scan(&docs); err != nil || docs != 3 {
		t.Fatalf("documentos = %d (%v), quer 3 (uma por cadeia)", docs, err)
	}
	type ver struct {
		doc          string
		major, minor int
		change       string
	}
	got := map[string]ver{}
	rows, err := pool.Query(ctx, `SELECT id::text, document_id::text, version_major, version_minor, change_kind FROM artifacts`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var v ver
		if err := rows.Scan(&id, &v.doc, &v.major, &v.minor, &v.change); err != nil {
			t.Fatal(err)
		}
		got[id[len(id)-2:]] = v
	}
	rows.Close()
	if got["a1"].doc != got["a2"].doc || got["a1"].doc == got["a3"].doc || got["a4"].doc == got["a1"].doc {
		t.Fatalf("agrupamento por cadeia: %+v", got)
	}
	if got["a1"].major != 1 || got["a2"].major != 2 || got["a2"].minor != 0 || got["a2"].change != "major" {
		t.Fatalf("vN vira N.0: %+v", got)
	}
	var project, workspace *string
	if err := pool.QueryRow(ctx, `SELECT project_id::text, workspace_id::text FROM artifact_documents WHERE id = $1`, got["a1"].doc).Scan(&project, &workspace); err != nil ||
		project == nil || workspace == nil || *workspace != "00000000-0000-0000-0000-0000000000e1" {
		t.Fatalf("projeto/workspace herdados: %v %v %v", project, workspace, err)
	}
	// apagar a task preserva as versões do artefato e apaga o anexo (e o documento dele)
	mustExec(`DELETE FROM tasks WHERE id = $1`, task)
	var arts, attDocs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE kind = 'artifact'), (SELECT count(*) FROM artifact_documents WHERE kind = 'attachment') FROM artifacts`).Scan(&arts, &attDocs); err != nil ||
		arts != 3 || attDocs != 0 {
		t.Fatalf("depois de apagar a task: %d versões de artefato, %d documentos de anexo (%v)", arts, attDocs, err)
	}

	if _, err := p.DownTo(ctx, 200); err != nil {
		t.Fatalf("down: %v", err)
	}
}
