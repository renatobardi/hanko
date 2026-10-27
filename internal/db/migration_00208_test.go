package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

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
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustFail := func(why, q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err == nil {
			t.Fatalf("%s: a constraint deveria recusar: %s", why, q)
		}
	}
	const (
		co  = "00000000-0000-0000-0000-000000000001"
		uid = "00000000-0000-0000-0000-0000000000d1"
		ws  = "00000000-0000-0000-0000-0000000000b1"
	)
	mustExec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, co)
	mustExec(`INSERT INTO users (id, email, provider, password_hash) VALUES ($1, 'a@b.c', 'local', 'x')`, uid)
	mustExec(`INSERT INTO workspaces (id, company_id, slug, name, path) VALUES ($1, $2, 'oute-agent', 'oute-agent', '')`, ws, co)
	mustExec(`INSERT INTO api_keys (id, user_id, name, prefix, hash) VALUES ('00000000-0000-0000-0000-0000000000e1', $1, 'cli', 'p', 'h1')`, uid)

	if _, err := p.UpTo(ctx, 208); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE scope = 'full' AND company_id IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("chave existente = %d (%v), quer intacta", n, err)
	}

	// Chave de ingestão: sempre presa a uma empresa; as outras nunca.
	mustExec(`INSERT INTO api_keys (id, user_id, name, prefix, hash, scope, company_id) VALUES ('00000000-0000-0000-0000-0000000000e2', $1, 'oute-server', 'p', 'h2', 'ingest', $2)`, uid, co)
	mustFail("ingest sem empresa", `INSERT INTO api_keys (id, user_id, name, prefix, hash, scope) VALUES ('00000000-0000-0000-0000-0000000000e3', $1, 'x', 'p', 'h3', 'ingest')`, uid)
	mustFail("full com empresa", `INSERT INTO api_keys (id, user_id, name, prefix, hash, scope, company_id) VALUES ('00000000-0000-0000-0000-0000000000e4', $1, 'x', 'p', 'h4', 'full', $2)`, uid, co)

	// Workspace de sistema: um por empresa.
	mustExec(`INSERT INTO workspaces (id, company_id, slug, name, path, is_system) VALUES ('00000000-0000-0000-0000-0000000000b2', $1, 'no-repo', 'no-repo', '', true)`, co)
	mustFail("dois workspaces de sistema", `INSERT INTO workspaces (id, company_id, slug, name, path, is_system) VALUES ('00000000-0000-0000-0000-0000000000b3', $1, 'outro', 'outro', '', true)`, co)

	// Agente e host: nome único sem distinguir caixa, sem espaço na ponta; apelido único por empresa.
	const ag, host = "00000000-0000-0000-0000-0000000000a1", "00000000-0000-0000-0000-0000000000c1"
	mustExec(`INSERT INTO external_agent (id, company_id, name) VALUES ($1, $2, 'claude')`, ag, co)
	mustFail("agente repetido em outra caixa", `INSERT INTO external_agent (id, company_id, name) VALUES ('00000000-0000-0000-0000-0000000000a2', $1, 'Claude')`, co)
	mustFail("agente com espaço na ponta", `INSERT INTO external_agent (id, company_id, name) VALUES ('00000000-0000-0000-0000-0000000000a3', $1, ' codex')`, co)
	mustFail("origem desconhecida", `INSERT INTO external_agent (id, company_id, name, source_kind) VALUES ('00000000-0000-0000-0000-0000000000a4', $1, 'codex', 'internal')`, co)
	mustExec(`INSERT INTO external_agent_alias (company_id, alias, agent_id) VALUES ($1, 'claude-code', $2)`, co, ag)
	mustFail("apelido repetido", `INSERT INTO external_agent_alias (company_id, alias, agent_id) VALUES ($1, 'claude-code', $2)`, co, ag)
	mustExec(`INSERT INTO external_host (id, company_id, name, always_on) VALUES ($1, $2, 'oute-server', true)`, host, co)
	mustExec(`INSERT INTO external_host_alias (company_id, alias, host_id) VALUES ($1, 'oute-server', $2)`, co, host)

	// Emparelhamento: uma chave de repositório por empresa.
	mustExec(`INSERT INTO repo_pairing (id, company_id, repo_key, key_kind, workspace_id) VALUES ('00000000-0000-0000-0000-0000000000f1', $1, 'github.com/renatobardi/oute-agent', 'url', $2)`, co, ws)
	mustFail("chave de repositório repetida", `INSERT INTO repo_pairing (id, company_id, repo_key, key_kind, workspace_id) VALUES ('00000000-0000-0000-0000-0000000000f2', $1, 'github.com/renatobardi/oute-agent', 'url', $2)`, co, ws)
	mustFail("tipo de chave desconhecido", `INSERT INTO repo_pairing (id, company_id, repo_key, key_kind, workspace_id) VALUES ('00000000-0000-0000-0000-0000000000f3', $1, 'x', 'path', $2)`, co, ws)

	// Conversa: nome no padrão do gerador (com ou sem o sufixo de 3 dígitos), único por empresa; classificação coerente.
	const conv = "00000000-0000-0000-0000-000000000d01"
	mustExec(`INSERT INTO external_conversation (id, company_id, session_id, name, host_id, agent_id) VALUES ($1, $2, 's-1', 'Veloz_Raposa', $3, $4)`, conv, co, host, ag)
	mustExec(`INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ('00000000-0000-0000-0000-000000000d02', $1, 's-2', 'Veloz_Raposa_417')`, co)
	mustFail("nome repetido", `INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ('00000000-0000-0000-0000-000000000d03', $1, 's-3', 'Veloz_Raposa')`, co)
	mustFail("nome em português com acento", `INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ('00000000-0000-0000-0000-000000000d04', $1, 's-4', 'Ágil_Raposa')`, co)
	mustFail("sufixo começando em zero", `INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ('00000000-0000-0000-0000-000000000d05', $1, 's-5', 'Veloz_Raposa_017')`, co)
	mustFail("sessão repetida", `INSERT INTO external_conversation (id, company_id, session_id, name) VALUES ('00000000-0000-0000-0000-000000000d06', $1, 's-1', 'Calmo_Tigre')`, co)
	mustFail("origem de classe desconhecida", `UPDATE external_conversation SET class_origin = 'guess' WHERE id = $1`, conv)
	mustExec(`INSERT INTO conversation_class_input (id, company_id, conversation_id, kind, class, confidence, actor_type) VALUES ('00000000-0000-0000-0000-000000000e01', $1, $2, 'decision_model', 'build', 'high', 'system')`, co, conv)
	mustExec(`INSERT INTO conversation_class_input (id, company_id, conversation_id, kind, class, confidence, actor_type, actor_id) VALUES ('00000000-0000-0000-0000-000000000e02', $1, $2, 'manual', 'qa', 'high', 'human', $3)`, co, conv, uid)
	mustFail("troca manual feita pelo sistema", `INSERT INTO conversation_class_input (id, company_id, conversation_id, kind, class, confidence, actor_type) VALUES ('00000000-0000-0000-0000-000000000e03', $1, $2, 'manual', 'qa', 'high', 'system')`, co, conv)
	mustExec(`INSERT INTO conversation_class_input (id, company_id, conversation_id, kind, class, confidence, actor_type, migrated) VALUES ('00000000-0000-0000-0000-000000000e04', $1, $2, 'manual', 'plan', 'high', 'system', true)`, co, conv)

	// Telemetria bruta: o fato cai na partição do mês dele; fora das faixas criadas, cai na DEFAULT; repetido é recusado.
	const (
		now  = `(extract(epoch FROM now())::bigint * 1000000000)`
		old  = `(extract(epoch FROM timestamp '2020-01-15')::bigint * 1000000000)`
		span = `INSERT INTO telemetry_spans (company_id, dedupe_key, time_unix_nano, trace_id, span_id, received_unix_nano) VALUES ($1, $2, `
		tail = `, 't', 's', 1)`
	)
	mustExec(span+now+tail, co, "s:t:1")
	mustExec(span+old+tail, co, "s:t:2")
	mustFail("span repetido", span+now+tail, co, "s:t:1")
	var inDefault, inMonth int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_spans_default`).Scan(&inDefault); err != nil || inDefault != 1 {
		t.Fatalf("spans na partição DEFAULT = %d (%v), quer 1 (o de 2020)", inDefault, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_spans WHERE tableoid::regclass::text <> 'telemetry_spans_default'`).Scan(&inMonth); err != nil || inMonth != 1 {
		t.Fatalf("spans nas partições mensais = %d (%v), quer 1", inMonth, err)
	}
	mustExec(`INSERT INTO telemetry_logs (company_id, dedupe_key, time_unix_nano, received_unix_nano) VALUES ($1, 'l:1', `+now+`, 1)`, co)
	mustExec(`INSERT INTO telemetry_metrics (company_id, dedupe_key, time_unix_nano, metric_name, metric_type, received_unix_nano) VALUES ($1, 'h:1', `+now+`, 'm', 'gauge', 1)`, co)
	mustFail("tipo de métrica desconhecido", `INSERT INTO telemetry_metrics (company_id, dedupe_key, time_unix_nano, metric_name, metric_type, received_unix_nano) VALUES ($1, 'h:2', `+now+`, 'm', 'rate', 1)`, co)

	// Chamada ao modelo: unpriced nunca vira zero; papel e classe de custo fechados.
	call := func(key, role, class, real, list string) string {
		return `INSERT INTO model_calls (company_id, dedupe_key, time_unix_nano, conversation_id, host_id, agent_id, workspace_id, role, cost_class, cost_real_usd, cost_list_usd)
			VALUES ('` + co + `', '` + key + `', 1, '` + conv + `', '` + host + `', '` + ag + `', '` + ws + `', '` + role + `', '` + class + `', ` + real + `, ` + list + `)`
	}
	mustExec(call("c1", "worker", "real", "0.5", "0.4"))
	mustExec(call("c2", "subagent", "listed", "NULL", "0.4"))
	mustExec(call("c3", "standalone", "unpriced", "NULL", "NULL"))
	mustFail("unpriced com custo", call("c4", "worker", "unpriced", "NULL", "0"))
	mustFail("papel em português", call("c5", "subagente", "real", "0.5", "0.4"))
	mustFail("classe desconhecida", call("c6", "worker", "paid", "0.5", "0.4"))
	mustFail("chamada repetida", call("c1", "worker", "real", "0.5", "0.4"))

	// Agregado diário.
	mustExec(`INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key, calls) VALUES ($1, '2026-10-26', $2, $3, $4, 'm', 'claude', 1)`, co, host, ag, ws)
	mustFail("dimensão repetida no dia", `INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key, calls) VALUES ($1, '2026-10-26', $2, $3, $4, 'm', 'claude', 1)`, co, host, ag, ws)

	// Caixa: os três tipos novos.
	mustExec(`INSERT INTO inbox_state (company_id, user_id, item_kind, item_id, updated_at) VALUES ($1, $2, 'external_agent', $3, now())`, co, uid, ag)
	mustFail("tipo de item desconhecido", `INSERT INTO inbox_state (company_id, user_id, item_kind, item_id, updated_at) VALUES ($1, $2, 'external_round', $3, now())`, co, uid, ag)

	// Down: com chave de ingestão ainda viva, recusa; sem ela, volta ao schema anterior sem perder a chave comum.
	if _, err := p.DownTo(ctx, 207); err == nil {
		t.Fatal("o Down deveria recusar com chave de escopo ingest")
	}
	mustExec(`DELETE FROM api_keys WHERE scope = 'ingest'`)
	if _, err := p.DownTo(ctx, 207); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE scope = 'full'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("chaves comuns depois do Down = %d (%v), quer 1", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspaces`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("workspaces depois do Down = %d (%v), quer 2", n, err)
	}
	if _, err := p.UpTo(ctx, 208); err != nil {
		t.Fatalf("Up de novo depois do Down: %v", err)
	}
}
