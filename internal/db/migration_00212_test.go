package db

import (
	"context"
	"testing"
)

const (
	m212Company = "00000000-0000-0000-0000-000000000001"
	m212Other   = "00000000-0000-0000-0000-000000000002"
	m212Ana     = "00000000-0000-0000-0000-0000000000a1"
	m212Beto    = "00000000-0000-0000-0000-0000000000a2"
	m212Fora    = "00000000-0000-0000-0000-0000000000a3"
	m212Tag     = "00000000-0000-0000-0000-0000000000b1"
	m212TagFora = "00000000-0000-0000-0000-0000000000b2"
)

// A 00212 (ADR-0087) é contrato: cadastro de tags de persona, ligação e coluna de nomes para a busca, sem mover dado. As
// constraints recusam nome e cor fora do contrato e ligação entre empresas, deletar a tag apaga as ligações, a busca acha
// a persona pelo nome da tag e o Down volta ao schema da 00211 sem perder a persona.
func TestMigration00212(t *testing.T) {
	ctx := context.Background()
	pool, p := openMigrations(t, 211)
	m := m208{t: t, pool: pool}
	m.exec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C'), ($2, 'd', 'D')`, m212Company, m212Other)
	const persona = `INSERT INTO personas (id, company_id, slug, name, agent) VALUES ($1, $2, $3, $3, 'fake')`
	m.exec(persona, m212Ana, m212Company, "ana")
	m.exec(persona, m212Beto, m212Company, "beto")
	m.exec(persona, m212Fora, m212Other, "fora")

	if _, err := p.UpTo(ctx, 212); err != nil {
		t.Fatal(err)
	}
	m.tags()
	m.links()
	m.tagSearch()

	if _, err := p.DownTo(ctx, 211); err != nil {
		t.Fatal(err)
	}
	if n := m.count(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('persona_tags', 'persona_tag_links')`); n != 0 {
		t.Fatalf("tabelas de tag depois do Down = %d, quer 0", n)
	}
	if n := m.count(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'personas' AND column_name = 'tag_names'`); n != 0 {
		t.Fatalf("tag_names depois do Down = %d, quer 0", n)
	}
	if n := m.count(`SELECT count(*) FROM personas WHERE search_tsv @@ plainto_tsquery('simple', 'ana')`); n != 1 {
		t.Fatalf("busca da persona depois do Down = %d, quer 1", n)
	}
}

const tagInsert = `INSERT INTO persona_tags (id, company_id, name, color) VALUES ($1, $2, $3, $4)`

// tags cobre o nome (1 a 40, sem espaço nas pontas, único sem caixa), a cor da paleta e a descrição.
func (m m208) tags() {
	m.exec(tagInsert, m212Tag, m212Company, "Revisão de código", "blue")
	m.exec(tagInsert, m212TagFora, m212Other, "revisão de código", "gray")
	m.fail("nome repetido em outra caixa", tagInsert, "00000000-0000-0000-0000-0000000000b3", m212Company, "REVISÃO DE CÓDIGO", "red")
	m.fail("nome vazio", tagInsert, "00000000-0000-0000-0000-0000000000b4", m212Company, "", "red")
	m.fail("espaço nas pontas", tagInsert, "00000000-0000-0000-0000-0000000000b5", m212Company, " infra", "red")
	m.fail("nome com 41 caracteres", tagInsert, "00000000-0000-0000-0000-0000000000b6", m212Company, "ççççççççççççççççççççççççççççççççççççççççç", "red")
	m.exec(tagInsert, "00000000-0000-0000-0000-0000000000b7", m212Company, "çççççççççççççççççççççççççççççççççççççççç", "pink")
	m.fail("cor fora da paleta", tagInsert, "00000000-0000-0000-0000-0000000000b8", m212Company, "infra", "#ff0000")
	m.fail("descrição longa", `INSERT INTO persona_tags (id, company_id, name, description) VALUES ('00000000-0000-0000-0000-0000000000b9', $1, 'dados', repeat('x', 201))`, m212Company)
	if n := m.count(`SELECT count(*) FROM persona_tags WHERE id = $1 AND color = 'gray' AND archived_at IS NULL`, m212TagFora); n != 1 {
		m.t.Fatalf("tag nova = %d ativa com a cor, quer 1", n)
	}
}

const linkInsert = `INSERT INTO persona_tag_links (company_id, persona_id, tag_id) VALUES ($1, $2, $3)`

// links cobre a ligação: uma por par, nunca entre empresas, e apagada junto com a tag.
func (m m208) links() {
	m.exec(linkInsert, m212Company, m212Ana, m212Tag)
	m.exec(linkInsert, m212Company, m212Beto, m212Tag)
	m.fail("ligação repetida", linkInsert, m212Company, m212Ana, m212Tag)
	m.fail("tag de outra empresa", linkInsert, m212Company, m212Ana, m212TagFora)
	m.fail("persona de outra empresa", linkInsert, m212Company, m212Fora, m212Tag)
	m.exec(`DELETE FROM persona_tags WHERE id = $1`, m212Tag)
	if n := m.count(`SELECT count(*) FROM persona_tag_links`); n != 0 {
		m.t.Fatalf("ligações depois de deletar a tag = %d, quer 0", n)
	}
	if n := m.count(`SELECT count(*) FROM personas WHERE company_id = $1`, m212Company); n != 2 {
		m.t.Fatalf("personas depois de deletar a tag = %d, quer 2", n)
	}
}

// tagSearch cobre a coluna de nomes: a busca acha a persona pelo nome da tag, sem acento nem caixa, e recusa o excesso.
func (m m208) tagSearch() {
	m.exec(`UPDATE personas SET tag_names = 'Revisão de código' || chr(10) || 'Infra' WHERE id = $1`, m212Ana)
	if n := m.count(`SELECT count(*) FROM personas WHERE search_tsv @@ plainto_tsquery('simple', hanko_fold('REVISAO'))`); n != 1 {
		m.t.Fatalf("busca pelo nome da tag = %d, quer 1", n)
	}
	if n := m.count(`SELECT count(*) FROM personas WHERE search_tsv @@ plainto_tsquery('simple', 'beto')`); n != 1 {
		m.t.Fatalf("busca pelo nome da persona = %d, quer 1", n)
	}
	m.fail("nomes de tag além do teto", `UPDATE personas SET tag_names = repeat('x', 1001) WHERE id = $1`, m212Ana)
}
