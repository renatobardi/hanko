package db

import (
	"context"
	"strings"
	"testing"
)

const (
	m211Company = "00000000-0000-0000-0000-000000000001"
	m211Round   = "00000000-0000-0000-0000-0000000000a1"
)

// A 00211 ajusta o contrato da aprovação e da rodada externas: o pedido decidido pode faltar o sha256, a duração é em
// segundos, o nome de quem aprovou é guardado e a rodada guarda o último evento, a última pergunta e a última resposta.
func TestMigration00211(t *testing.T) {
	ctx := context.Background()
	pool, p := openMigrations(t, 210)
	m := m208{t: t, pool: pool}
	m.exec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, m211Company)

	if _, err := p.UpTo(ctx, 211); err != nil {
		t.Fatal(err)
	}
	const dec = `INSERT INTO external_approval (id, company_id, canal_id, state, decision, approver, decided_sha256, duration_s, approved_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	m.exec(dec, "00000000-0000-0000-0000-0000000000b1", m211Company, "20261027-080000-cafe", "decided", "rejected", "human", nil, nil, "bardi")
	m.exec(dec, "00000000-0000-0000-0000-0000000000b2", m211Company, "20261027-080001-cafe", "decided", "executed", "human_dark", strings.Repeat("ab", 32), 12, "")
	m.exec(`INSERT INTO external_approval (id, company_id, canal_id, state) VALUES ('00000000-0000-0000-0000-0000000000b3', $1, '20261027-080002-cafe', 'pending')`, m211Company)
	m.fail("decidido sem decisão", `INSERT INTO external_approval (id, company_id, canal_id, state) VALUES ('00000000-0000-0000-0000-0000000000b4', $1, '20261027-080003-cafe', 'decided')`, m211Company)
	m.fail("pendente com decisão", dec, "00000000-0000-0000-0000-0000000000b5", m211Company, "20261027-080004-cafe", "pending", "rejected", "human", nil, nil, "")
	m.fail("duração negativa", dec, "00000000-0000-0000-0000-0000000000b6", m211Company, "20261027-080005-cafe", "decided", "executed", "human", nil, -1, "")
	m.fail("sha256 fora do formato", dec, "00000000-0000-0000-0000-0000000000b7", m211Company, "20261027-080006-cafe", "decided", "executed", "human", "xyz", 1, "")
	if n := m.count(`SELECT count(*) FROM external_approval WHERE approved_by = ''`); n != 2 {
		t.Fatalf("pedidos sem nome de aprovador = %d, quer 2 (o padrão é vazio)", n)
	}

	m.exec(`INSERT INTO external_round (id, company_id, round_key, name, last_event_at, last_asked_at, last_answered_at) VALUES ($1, $2, 'swarm-1', 'Brave_Otter', 3, 2, 1)`, m211Round, m211Company)
	m.fail("hora negativa", `UPDATE external_round SET last_asked_at = -1 WHERE id = $1`, m211Round)

	// Down: com pedido decidido sem sha256, recusa; sem ele, volta ao schema anterior.
	if _, err := p.DownTo(ctx, 210); err == nil {
		t.Fatal("o Down deveria recusar com pedido decidido sem sha256")
	}
	m.exec(`DELETE FROM external_approval WHERE decided_sha256 IS NULL AND state = 'decided'`)
	if _, err := p.DownTo(ctx, 210); err != nil {
		t.Fatal(err)
	}
	if n := m.count(`SELECT count(*) FROM external_approval`); n != 2 {
		t.Fatalf("pedidos depois do Down = %d, quer 2", n)
	}
	if _, err := p.UpTo(ctx, 211); err != nil {
		t.Fatalf("Up de novo depois do Down: %v", err)
	}
}
