package db

import (
	"context"
	"testing"
)

const (
	m210Company = "00000000-0000-0000-0000-000000000001"
	m210User    = "00000000-0000-0000-0000-0000000000d1"
	m210Host    = "00000000-0000-0000-0000-000000000701"
	m210Sess    = "00000000-0000-0000-0000-000000000702"
	m210Conv    = "00000000-0000-0000-0000-000000000703"
	m210Cycle   = "00000000-0000-0000-0000-000000000101"
	m210Round   = "00000000-0000-0000-0000-000000000201"
	m210Triage  = "00000000-0000-0000-0000-000000000401"
	m210Merge   = "00000000-0000-0000-0000-000000000402"
	m210Summary = "00000000-0000-0000-0000-000000000403"
	m210Action  = "00000000-0000-0000-0000-000000000501"
	m210Appr    = "00000000-0000-0000-0000-000000000601"
	m210Appr2   = "00000000-0000-0000-0000-000000000602"
	m210Sha     = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	m210Sha2    = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

// A 00210 (ADR-0080, 0081 e 0078; alerta de disco) é contrato: tabelas e colunas novas, sem mover dado. As constraints
// recusam o que está fora do contrato (rodada, etapa, marca, aprovação), a busca encontra a conversa pelo nome, e o Down
// volta ao schema da 00209 sem perder a conversa da 00208.
func TestMigration00210(t *testing.T) {
	ctx := context.Background()
	pool, p := openMigrations(t, 209)
	m := m208{t: t, pool: pool}
	m.exec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, m210Company)
	m.exec(`INSERT INTO users (id, email, provider, password_hash) VALUES ($1, 'a@b.c', 'local', 'x')`, m210User)
	m.exec(`INSERT INTO external_host (id, company_id, name) VALUES ($1, $2, 'oute-server')`, m210Host, m210Company)
	m.exec(`INSERT INTO external_session (id, company_id, task_id) VALUES ($1, $2, 'task-1')`, m210Sess, m210Company)
	m.exec(`INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ($1, $2, 'sess-1', 'Brave_Otter')`, m210Conv, m210Company)

	if _, err := p.UpTo(ctx, 210); err != nil {
		t.Fatal(err)
	}
	m.rounds()
	m.approvals()
	m.diskAlert()
	m.search()

	if _, err := p.DownTo(ctx, 209); err != nil {
		t.Fatal(err)
	}
	if n := m.count(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('external_round', 'round_step', 'external_approval')`); n != 0 {
		t.Fatalf("tabelas da rodada e da aprovação depois do Down = %d, quer 0", n)
	}
	if n := m.count(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'external_conversation' AND column_name = 'search_tsv'`); n != 0 {
		t.Fatalf("search_tsv depois do Down = %d, quer 0", n)
	}
	if n := m.count(`SELECT count(*) FROM external_conversation WHERE id = $1`, m210Conv); n != 1 {
		t.Fatalf("conversa da 00208 depois do Down = %d, quer 1", n)
	}
	m.fail("alerta de disco depois do Down", `INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, opened_at, last_seen_at)
		VALUES ('00000000-0000-0000-0000-0000000007f1', $1, 'db_disk_low', 'company', $1, 'warning', 'x', now(), now())`, m210Company)
}

// rounds cobre o ciclo, a rodada, a etapa (uma por tipo e chave; o resumo pertence ao ciclo) e a ação com a marca.
func (m m208) rounds() {
	m.exec(`INSERT INTO round_cycle (id, company_id, issue_ref) VALUES ($1, $2, 'renatobardi/oute-agent#12')`, m210Cycle, m210Company)
	m.fail("issue sem dono", `INSERT INTO round_cycle (id, company_id, issue_ref) VALUES ('00000000-0000-0000-0000-000000000199', $1, 'oute-agent#12')`, m210Company)

	const round = `INSERT INTO external_round (id, company_id, round_key, name, cycle_id, repo, max_sessions, opened_at)
		VALUES ($1, $2, 'swarm-1027-0802', 'Brave_Otter', $3, 'github.com/renatobardi/oute-agent', 3, 1)`
	m.exec(round, m210Round, m210Company, m210Cycle)
	m.fail("nome fora do gerador", `INSERT INTO external_round (id, company_id, round_key, name) VALUES ('00000000-0000-0000-0000-000000000299', $1, 'swarm-x', 'brave')`, m210Company)
	m.fail("sessões negativas", `INSERT INTO external_round (id, company_id, round_key, name, max_sessions) VALUES ('00000000-0000-0000-0000-000000000298', $1, 'swarm-y', 'Calm_Fox', -1)`, m210Company)

	m.exec(`INSERT INTO round_participant (id, company_id, round_id, slug, external_session_id) VALUES ('00000000-0000-0000-0000-000000000301', $1, $2, 'w1', $3)`, m210Company, m210Round, m210Sess)
	m.fail("participante repetido na rodada", `INSERT INTO round_participant (id, company_id, round_id, slug) VALUES ('00000000-0000-0000-0000-000000000302', $1, $2, 'w1')`, m210Company, m210Round)

	m.step(m210Triage, m210Round, nil, "triage", "", 1)
	m.step(m210Merge, m210Round, nil, "merge", "42", 1)
	m.fail("merge sem número do PR", stepInsert, "00000000-0000-0000-0000-000000000404", m210Company, m210Round, nil, "merge", "", 1, m210Sha, "approved", "ok", "k", 1, 1)
	m.fail("etapa de ciclo com rodada", stepInsert, "00000000-0000-0000-0000-000000000405", m210Company, m210Round, m210Cycle, "cycle_summary", "", 1, m210Sha, "approved", "ok", "k", 1, 1)
	m.step(m210Summary, "", m210Cycle, "cycle_summary", "", 1)
	m.fail("etapa lógica repetida na rodada", stepInsert, "00000000-0000-0000-0000-000000000406", m210Company, m210Round, nil, "triage", "", 2, m210Sha, "approved", "ok", "k", 1, 1)
	m.fail("revisão de etapa sem sha256", stepInsert, "00000000-0000-0000-0000-000000000407", m210Company, m210Round, nil, "kaizen", "", 1, "curto", "approved", "ok", "k", 1, 1)
	m.fail("revisão fora do contrato", stepInsert, "00000000-0000-0000-0000-000000000408", m210Company, m210Round, nil, "kaizen", "", 1, m210Sha, "aprovado", "ok", "k", 1, 1)

	m.exec(`INSERT INTO step_action (id, company_id, step_id, action_id, approval_key) VALUES ($1, $2, $3, 'ab1', '20261027-080000-cafe')`, m210Action, m210Company, m210Merge)
	m.fail("ação com id fora do formato", `INSERT INTO step_action (id, company_id, step_id, action_id) VALUES ('00000000-0000-0000-0000-000000000502', $1, $2, 'AB1')`, m210Company, m210Merge)
	m.fail("ação com chave de pedido inválida", `INSERT INTO step_action (id, company_id, step_id, action_id, approval_key) VALUES ('00000000-0000-0000-0000-000000000503', $1, $2, 'ab2', 'pedido')`, m210Company, m210Merge)

	const mark = `INSERT INTO action_mark (id, company_id, step_id, action_id, state, marked_by_type, marked_by_id, marked_unix_nano)
		VALUES ($1, $2, $3, 'ab1', $4, $5, $6, $7)`
	m.exec(mark, "00000000-0000-0000-0000-000000000601", m210Company, m210Merge, "pending", "human", m210User, 100)
	m.exec(mark, "00000000-0000-0000-0000-000000000602", m210Company, m210Merge, "done", "human", m210User, 200)
	m.fail("marca sem autor humano", mark, "00000000-0000-0000-0000-000000000603", m210Company, m210Merge, "done", "human", nil, 300)
	m.fail("marca com estado fora do contrato", mark, "00000000-0000-0000-0000-000000000604", m210Company, m210Merge, "feita", "human", m210User, 300)
	if n := m.count(`SELECT count(*) FROM action_mark WHERE step_id = $1 AND action_id = 'ab1'`, m210Merge); n != 2 {
		m.t.Fatalf("marcas gravadas = %d, quer 2", n)
	}
}

// step grava uma revisão válida de etapa. Uma etapa de rodada leva `round`; o resumo do ciclo leva `cycle`.
func (m m208) step(id, round string, cycle any, kind, key string, rev int) {
	var r any
	if round != "" {
		r = round
	}
	m.exec(stepInsert, id, m210Company, r, cycle, kind, key, rev, m210Sha, "approved", "ok", "fact-"+id, 1, 1)
}

const stepInsert = `INSERT INTO round_step (id, company_id, round_id, cycle_id, kind, step_key, revision, sha256, review,
	reference_check, fact_dedupe_key, fact_time_unix_nano, published_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`

// approvals cobre o pedido pendente, o decidido, as versões do script e a lista da outbox do host.
func (m m208) approvals() {
	const pending = `INSERT INTO external_approval (id, company_id, canal_id, title, host_id, state, script_sha256, requested_at)
		VALUES ($1, $2, '20261027-080000-cafe', 'instalar', $3, 'pending', $4, 1)`
	m.exec(pending, m210Appr, m210Company, m210Host, m210Sha)
	m.fail("pedido repetido no mesmo canal", pending, m210Appr2, m210Company, m210Host, m210Sha)

	const decided = `INSERT INTO external_approval (id, company_id, canal_id, state, decision, approver, decided_sha256, requested_at, decided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 2)`
	m.exec(decided, m210Appr2, m210Company, "20261027-090000-beef", "decided", "executed", "human", m210Sha2)
	m.fail("decidido sem aprovador", decided, "00000000-0000-0000-0000-000000000603", m210Company, "20261027-100000-beef", "decided", "executed", nil, m210Sha2)
	m.fail("decisão fora do contrato", decided, "00000000-0000-0000-0000-000000000604", m210Company, "20261027-110000-beef", "decided", "recusado", "human", m210Sha2)
	m.fail("pendente com decisão", decided, "00000000-0000-0000-0000-000000000605", m210Company, "20261027-120000-beef", "pending", "executed", "human", m210Sha2)

	const version = `INSERT INTO external_approval_version (approval_id, sha256, fact_dedupe_key, fact_time_unix_nano, seen_at) VALUES ($1, $2, 'fact-v', 1, 1)`
	m.exec(version, m210Appr, m210Sha)
	m.exec(version, m210Appr, m210Sha2)
	m.fail("mesma versão duas vezes", version, m210Appr, m210Sha)

	m.exec(`INSERT INTO external_outbox_snapshot (host_id, company_id, snapshot_at, canal_ids, received_at) VALUES ($1, $2, 1, $3, 1)`,
		m210Host, m210Company, []string{"20261027-080000-cafe"})
}

// diskAlert cobre o tipo de alerta de disco e os itens novos da Caixa.
func (m m208) diskAlert() {
	const alert = `INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, opened_at, last_seen_at)
		VALUES ($1, $2, $3, 'company', $2, 'warning', 'x', now(), now())`
	m.exec(alert, "00000000-0000-0000-0000-0000000007d1", m210Company, "db_disk_low")
	m.fail("tipo de disco fora do contrato", alert, "00000000-0000-0000-0000-0000000007d2", m210Company, "db_disk_full")
	if n := m.count(`SELECT count(*) FROM pg_constraint WHERE conname = 'inbox_state_item_kind_check' AND pg_get_constraintdef(oid) LIKE '%round_question%external_approval%'`); n != 1 {
		m.t.Fatalf("Caixa sem os tipos novos: %d", n)
	}
}

// search cobre a busca da conversa e da sessão pelo nome, sem caixa (hanko_fold).
func (m m208) search() {
	if n := m.count(`SELECT count(*) FROM external_conversation WHERE search_tsv @@ plainto_tsquery('simple', hanko_fold('OTTER'))`); n != 1 {
		m.t.Fatalf("busca da conversa = %d, quer 1", n)
	}
	m.exec(`INSERT INTO external_session (id, company_id, task_id, slug) VALUES ('00000000-0000-0000-0000-000000000704', $1, 'task-2', 'Reviewer')`, m210Company)
	if n := m.count(`SELECT count(*) FROM external_session WHERE search_tsv @@ plainto_tsquery('simple', hanko_fold('reviewer'))`); n != 1 {
		m.t.Fatalf("busca da sessão = %d, quer 1", n)
	}
}
