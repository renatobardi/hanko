package db

import (
	"context"
	"testing"
)

// A 00214 põe a janela da classificação na conversa externa: a hora de recebimento da sessão vista na última
// classificação, NULL até a primeira. O Down tira a coluna e as conversas ficam.
func TestMigration00214(t *testing.T) {
	ctx := context.Background()
	pool, p := openMigrations(t, 213)
	m := m208{t: t, pool: pool}
	const company = "00000000-0000-0000-0000-000000000002"
	m.exec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c214', 'C')`, company)

	if _, err := p.UpTo(ctx, 214); err != nil {
		t.Fatal(err)
	}
	m.exec(`INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ('00000000-0000-0000-0000-0000000000c1', $1, 'sessao-1', 'Alfa_Beta')`, company)
	if n := m.count(`SELECT count(*) FROM external_conversation WHERE session_id = 'sessao-1' AND classified_session_at IS NULL`); n != 1 {
		t.Fatalf("janela padrão não é NULL: %d linhas", n)
	}

	if _, err := p.DownTo(ctx, 213); err != nil {
		t.Fatalf("Down da 00214: %v", err)
	}
	if n := m.count(`SELECT count(*) FROM external_conversation WHERE session_id = 'sessao-1'`); n != 1 {
		t.Fatalf("conversas depois do Down = %d, quer 1", n)
	}
	if _, err := p.UpTo(ctx, 214); err != nil {
		t.Fatalf("Up de novo depois do Down: %v", err)
	}
}
