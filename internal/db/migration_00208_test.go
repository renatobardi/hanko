package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

const (
	m208Company = "00000000-0000-0000-0000-000000000001"
	m208User    = "00000000-0000-0000-0000-0000000000d1"
	m208WS      = "00000000-0000-0000-0000-0000000000b1"
	m208Agent   = "00000000-0000-0000-0000-0000000000a1"
	m208Host    = "00000000-0000-0000-0000-0000000000c1"
	m208Conv    = "00000000-0000-0000-0000-000000000d01"
)

// m208 junta o pool e os dois atalhos dos testes: um comando que deve passar e outro que a constraint deve recusar.
type m208 struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func (m m208) exec(q string, args ...any) {
	m.t.Helper()
	if _, err := m.pool.Exec(context.Background(), q, args...); err != nil {
		m.t.Fatalf("%s: %v", q, err)
	}
}

func (m m208) fail(why, q string, args ...any) {
	m.t.Helper()
	if _, err := m.pool.Exec(context.Background(), q, args...); err == nil {
		m.t.Fatalf("%s: a constraint deveria recusar: %s", why, q)
	}
}

func (m m208) count(q string, args ...any) int {
	m.t.Helper()
	var n int
	if err := m.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		m.t.Fatalf("%s: %v", q, err)
	}
	return n
}

// A 00208 (ADR-0075 a 0079, 0084 e 0086) é só contrato: cria tabelas e colunas sem mexer no que existe, as
// constraints recusam chave de ingestão sem empresa, agente repetido, nome fora do padrão e custo incoerente, as
// partições mensais recebem o fato certo e o Down volta ao schema anterior.
func TestMigration00208(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 207); err != nil {
		t.Fatal(err)
	}
	m := m208{t: t, pool: pool}
	m.exec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, m208Company)
	m.exec(`INSERT INTO users (id, email, provider, password_hash) VALUES ($1, 'a@b.c', 'local', 'x')`, m208User)
	m.exec(`INSERT INTO workspaces (id, company_id, slug, name, path) VALUES ($1, $2, 'oute-agent', 'oute-agent', '')`, m208WS, m208Company)
	m.exec(`INSERT INTO api_keys (id, user_id, name, prefix, hash) VALUES ('00000000-0000-0000-0000-0000000000e1', $1, 'cli', 'p', 'h1')`, m208User)

	if _, err := p.UpTo(ctx, 208); err != nil {
		t.Fatal(err)
	}
	if n := m.count(`SELECT count(*) FROM api_keys WHERE scope = 'full' AND company_id IS NULL`); n != 1 {
		t.Fatalf("chave existente = %d, quer intacta", n)
	}

	m.keysAndWorkspaces()
	m.entities()
	m.conversations()
	m.telemetry()
	m.calls()

	// Caixa: os três tipos novos.
	m.exec(`INSERT INTO inbox_state (company_id, user_id, item_kind, item_id, updated_at) VALUES ($1, $2, 'external_agent', $3, now())`, m208Company, m208User, m208Agent)
	m.fail("tipo de item desconhecido", `INSERT INTO inbox_state (company_id, user_id, item_kind, item_id, updated_at) VALUES ($1, $2, 'external_round', $3, now())`, m208Company, m208User, m208Agent)

	// Down: com chave de ingestão ainda viva, recusa; sem ela, volta ao schema anterior sem perder a chave comum.
	if _, err := p.DownTo(ctx, 207); err == nil {
		t.Fatal("o Down deveria recusar com chave de escopo ingest")
	}
	m.exec(`DELETE FROM api_keys WHERE scope = 'ingest'`)
	if _, err := p.DownTo(ctx, 207); err != nil {
		t.Fatal(err)
	}
	if n := m.count(`SELECT count(*) FROM api_keys WHERE scope = 'full'`); n != 1 {
		t.Fatalf("chaves comuns depois do Down = %d, quer 1", n)
	}
	if n := m.count(`SELECT count(*) FROM workspaces`); n != 2 {
		t.Fatalf("workspaces depois do Down = %d, quer 2", n)
	}
	if _, err := p.UpTo(ctx, 208); err != nil {
		t.Fatalf("Up de novo depois do Down: %v", err)
	}
}

// Chave de ingestão: sempre presa a uma empresa, as outras nunca. Workspace de sistema: um por empresa.
func (m m208) keysAndWorkspaces() {
	m.exec(`INSERT INTO api_keys (id, user_id, name, prefix, hash, scope, company_id) VALUES ('00000000-0000-0000-0000-0000000000e2', $1, 'oute-server', 'p', 'h2', 'ingest', $2)`, m208User, m208Company)
	m.fail("ingest sem empresa", `INSERT INTO api_keys (id, user_id, name, prefix, hash, scope) VALUES ('00000000-0000-0000-0000-0000000000e3', $1, 'x', 'p', 'h3', 'ingest')`, m208User)
	m.fail("full com empresa", `INSERT INTO api_keys (id, user_id, name, prefix, hash, scope, company_id) VALUES ('00000000-0000-0000-0000-0000000000e4', $1, 'x', 'p', 'h4', 'full', $2)`, m208User, m208Company)
	m.exec(`INSERT INTO workspaces (id, company_id, slug, name, path, is_system) VALUES ('00000000-0000-0000-0000-0000000000b2', $1, 'no-repo', 'no-repo', '', true)`, m208Company)
	m.fail("dois workspaces de sistema", `INSERT INTO workspaces (id, company_id, slug, name, path, is_system) VALUES ('00000000-0000-0000-0000-0000000000b3', $1, 'outro', 'outro', '', true)`, m208Company)
}

// Agente, host e emparelhamento: nome único sem distinguir caixa e sem espaço na ponta; apelido e chave únicos.
func (m m208) entities() {
	m.exec(`INSERT INTO external_agent (id, company_id, name) VALUES ($1, $2, 'claude')`, m208Agent, m208Company)
	m.fail("agente repetido em outra caixa", `INSERT INTO external_agent (id, company_id, name) VALUES ('00000000-0000-0000-0000-0000000000a2', $1, 'Claude')`, m208Company)
	m.fail("agente com espaço na ponta", `INSERT INTO external_agent (id, company_id, name) VALUES ('00000000-0000-0000-0000-0000000000a3', $1, ' codex')`, m208Company)
	m.fail("origem desconhecida", `INSERT INTO external_agent (id, company_id, name, source_kind) VALUES ('00000000-0000-0000-0000-0000000000a4', $1, 'codex', 'internal')`, m208Company)
	m.exec(`INSERT INTO external_agent_alias (company_id, alias, agent_id) VALUES ($1, 'claude-code', $2)`, m208Company, m208Agent)
	m.fail("apelido repetido", `INSERT INTO external_agent_alias (company_id, alias, agent_id) VALUES ($1, 'claude-code', $2)`, m208Company, m208Agent)
	m.exec(`INSERT INTO external_host (id, company_id, name, always_on) VALUES ($1, $2, 'oute-server', true)`, m208Host, m208Company)
	m.exec(`INSERT INTO external_host_alias (company_id, alias, host_id) VALUES ($1, 'oute-server', $2)`, m208Company, m208Host)

	const pair = `INSERT INTO repo_pairing (id, company_id, repo_key, key_kind, workspace_id) VALUES ($1, $2, $3, $4, $5)`
	m.exec(pair, "00000000-0000-0000-0000-0000000000f1", m208Company, "github.com/renatobardi/oute-agent", "url", m208WS)
	m.fail("chave de repositório repetida", pair, "00000000-0000-0000-0000-0000000000f2", m208Company, "github.com/renatobardi/oute-agent", "url", m208WS)
	m.fail("tipo de chave desconhecido", pair, "00000000-0000-0000-0000-0000000000f3", m208Company, "x", "path", m208WS)
}

// Conversa: nome no padrão do gerador (com ou sem o sufixo de 3 dígitos), único por empresa; classificação coerente.
func (m m208) conversations() {
	const conv = `INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ($1, $2, $3, $4)`
	m.exec(`INSERT INTO external_conversation (id, company_id, session_id, name, host_id, agent_id) VALUES ($1, $2, 's-1', 'Veloz_Raposa', $3, $4)`, m208Conv, m208Company, m208Host, m208Agent)
	m.exec(conv, "00000000-0000-0000-0000-000000000d02", m208Company, "s-2", "Veloz_Raposa_417")
	m.fail("nome repetido", conv, "00000000-0000-0000-0000-000000000d03", m208Company, "s-3", "Veloz_Raposa")
	m.fail("nome em português com acento", conv, "00000000-0000-0000-0000-000000000d04", m208Company, "s-4", "Ágil_Raposa")
	m.fail("sufixo começando em zero", conv, "00000000-0000-0000-0000-000000000d05", m208Company, "s-5", "Veloz_Raposa_017")
	m.fail("sessão repetida", conv, "00000000-0000-0000-0000-000000000d06", m208Company, "s-1", "Calmo_Tigre")
	m.fail("origem de classe desconhecida", `UPDATE external_conversation SET class_origin = 'guess' WHERE id = $1`, m208Conv)

	const in = `INSERT INTO conversation_class_input (id, company_id, conversation_id, kind, class, confidence, actor_type, actor_id, migrated) VALUES ($1, $2, $3, $4, $5, 'high', $6, $7, $8)`
	m.exec(in, "00000000-0000-0000-0000-000000000e01", m208Company, m208Conv, "decision_model", "build", "system", nil, false)
	m.exec(in, "00000000-0000-0000-0000-000000000e02", m208Company, m208Conv, "manual", "qa", "human", m208User, false)
	m.fail("troca manual feita pelo sistema", in, "00000000-0000-0000-0000-000000000e03", m208Company, m208Conv, "manual", "qa", "system", nil, false)
	m.exec(in, "00000000-0000-0000-0000-000000000e04", m208Company, m208Conv, "manual", "plan", "system", nil, true)
}

// Telemetria bruta: o fato cai na partição do mês dele; fora das faixas criadas, cai na DEFAULT; repetido é recusado.
func (m m208) telemetry() {
	const (
		current = `(extract(epoch FROM now())::bigint * 1000000000)`
		ancient = `(extract(epoch FROM timestamp '2020-01-15')::bigint * 1000000000)`
		span    = `INSERT INTO telemetry_spans (company_id, dedupe_key, time_unix_nano, trace_id, span_id, received_unix_nano) VALUES ($1, $2, `
		tail    = `, 't', 's', 1)`
	)
	m.exec(span+current+tail, m208Company, "s:t:1")
	m.exec(span+ancient+tail, m208Company, "s:t:2")
	m.fail("span repetido", span+current+tail, m208Company, "s:t:1")
	if n := m.count(`SELECT count(*) FROM telemetry_spans_default`); n != 1 {
		m.t.Fatalf("spans na partição DEFAULT = %d, quer 1 (o de 2020)", n)
	}
	if n := m.count(`SELECT count(*) FROM telemetry_spans WHERE tableoid::regclass::text <> 'telemetry_spans_default'`); n != 1 {
		m.t.Fatalf("spans nas partições mensais = %d, quer 1", n)
	}
	m.exec(`INSERT INTO telemetry_logs (company_id, dedupe_key, time_unix_nano, received_unix_nano) VALUES ($1, 'l:1', `+current+`, 1)`, m208Company)
	const metric = `INSERT INTO telemetry_metrics (company_id, dedupe_key, time_unix_nano, metric_name, metric_type, received_unix_nano) VALUES ($1, $2, ` + current + `, 'm', $3, 1)`
	m.exec(metric, m208Company, "h:1", "gauge")
	m.fail("tipo de métrica desconhecido", metric, m208Company, "h:2", "rate")
}

// Chamada ao modelo: unpriced nunca vira zero; papel e classe de custo fechados. Agregado diário: uma linha por dimensão.
func (m m208) calls() {
	const call = `INSERT INTO model_calls (company_id, dedupe_key, time_unix_nano, conversation_id, host_id, agent_id, workspace_id, role, cost_class, cost_real_usd, cost_list_usd)
		VALUES ($1, $2, 1, $3, $4, $5, $6, $7, $8, $9, $10)`
	do := func(key, role, class string, realUSD, listUSD any) (string, []any) {
		return call, []any{m208Company, key, m208Conv, m208Host, m208Agent, m208WS, role, class, realUSD, listUSD}
	}
	q, a := do("c1", "worker", "real", 0.5, 0.4)
	m.exec(q, a...)
	q, a = do("c2", "subagent", "listed", nil, 0.4)
	m.exec(q, a...)
	q, a = do("c3", "standalone", "unpriced", nil, nil)
	m.exec(q, a...)
	q, a = do("c4", "worker", "unpriced", nil, 0.0)
	m.fail("unpriced com custo", q, a...)
	q, a = do("c5", "subagente", "real", 0.5, 0.4)
	m.fail("papel em português", q, a...)
	q, a = do("c6", "worker", "paid", 0.5, 0.4)
	m.fail("classe desconhecida", q, a...)
	q, a = do("c1", "worker", "real", 0.5, 0.4)
	m.fail("chamada repetida", q, a...)

	const daily = `INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key, calls) VALUES ($1, '2026-10-26', $2, $3, $4, 'm', 'claude', 1)`
	m.exec(daily, m208Company, m208Host, m208Agent, m208WS)
	m.fail("dimensão repetida no dia", daily, m208Company, m208Host, m208Agent, m208WS)
}
