package db

import (
	"context"
	"testing"
)

const m213Company = "00000000-0000-0000-0000-000000000001"

// A 00213 põe a hora de cada atributo da sessão externa (ADR-0078 item 2): repositório, agente e rodada. Hora negativa
// é recusada, e o Down volta ao schema anterior sem perder as sessões.
func TestMigration00213(t *testing.T) {
	ctx := context.Background()
	pool, p := openMigrations(t, 212)
	m := m208{t: t, pool: pool}
	m.exec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, m213Company)

	if _, err := p.UpTo(ctx, 213); err != nil {
		t.Fatal(err)
	}
	m.exec(`INSERT INTO external_session (id, company_id, task_id, oute_repo, oute_repo_at, round, round_at) VALUES ('00000000-0000-0000-0000-0000000000e1', $1, 'task-1', 'oute-agent', 10, 'r1', 9)`, m213Company)
	m.fail("hora de repositório negativa", `UPDATE external_session SET oute_repo_at = -1 WHERE task_id = 'task-1'`)
	m.fail("hora de agente negativa", `UPDATE external_session SET agent_at = -1 WHERE task_id = 'task-1'`)
	m.fail("hora de rodada negativa", `UPDATE external_session SET round_at = -1 WHERE task_id = 'task-1'`)

	if _, err := p.DownTo(ctx, 212); err != nil {
		t.Fatalf("Down da 00213: %v", err)
	}
	if n := m.count(`SELECT count(*) FROM external_session WHERE task_id = 'task-1'`); n != 1 {
		t.Fatalf("sessões depois do Down = %d, quer 1", n)
	}
	if _, err := p.UpTo(ctx, 213); err != nil {
		t.Fatalf("Up de novo depois do Down: %v", err)
	}
}
