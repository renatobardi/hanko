package db

import (
	"context"
	"testing"
)

const (
	m209Company = "00000000-0000-0000-0000-000000000001"
	m209User    = "00000000-0000-0000-0000-0000000000d1"
	m209Sub     = "00000000-0000-0000-0000-0000000000a1"
	m209Run     = "00000000-0000-0000-0000-0000000000c1"
)

// A 00209 (ADR-0082 e 0083) é contrato, com uma exceção: cada assinatura que já existe ganha a linha inicial do
// histórico de mensalidade. As constraints recusam origem, chave, status e prazo fora do contrato, e o Down volta ao
// schema anterior sem perder a assinatura.
func TestMigration00209(t *testing.T) {
	ctx := context.Background()
	pool, p := openMigrations(t, 208)
	m := m208{t: t, pool: pool}
	m.exec(`INSERT INTO companies (id, slug, name) VALUES ($1, 'c', 'C')`, m209Company)
	m.exec(`INSERT INTO users (id, email, provider, password_hash) VALUES ($1, 'a@b.c', 'local', 'x')`, m209User)
	m.exec(`INSERT INTO subscriptions (id, company_id, name, agent, monthly_usd, created_at) VALUES ($1, $2, 'claude-max', 'claude', 200, '2026-03-05T10:00:00Z')`, m209Sub, m209Company)

	if _, err := p.UpTo(ctx, 209); err != nil {
		t.Fatal(err)
	}
	m.priceAndSubscription()
	m.history()
	m.priceChecks()
	m.alertsAndSettings()

	// Caixa: a marca migrada começa desligada.
	if n := m.count(`SELECT count(*) FROM inbox_state WHERE migrated`); n != 0 {
		t.Fatalf("marcas migradas = %d, quer 0", n)
	}

	if _, err := p.DownTo(ctx, 208); err != nil {
		t.Fatal(err)
	}
	if n := m.count(`SELECT count(*) FROM subscriptions`); n != 2 {
		t.Fatalf("assinaturas depois do Down = %d, quer 2", n)
	}
	if n := m.count(`SELECT count(*) FROM alerts`); n != 1 {
		t.Fatalf("alertas depois do Down = %d, quer 1 (o de cofre; os externos somem)", n)
	}
	if _, err := p.UpTo(ctx, 209); err != nil {
		t.Fatalf("Up de novo depois do Down: %v", err)
	}
}

// Preço: origem fechada e trava por linha. Assinatura: chave externa única por empresa.
func (m m208) priceAndSubscription() {
	const price = `INSERT INTO model_prices (id, model, input_per_mtok, effective_at, origin, locked) VALUES ($1, $2, 1, $3, $4, $5)`
	m.exec(price, "00000000-0000-0000-0000-0000000000b1", "gpt-6-luna", "2026-10-26T00:00:00Z", "sources", true)
	m.fail("origem de preço desconhecida", price, "00000000-0000-0000-0000-0000000000b2", "gpt-6-luna", "2026-10-27T00:00:00Z", "guess", false)
	if m.count(`SELECT count(*) FROM model_prices WHERE origin = 'config' AND NOT locked`) < 1 {
		m.t.Fatal("os preços semeados deveriam ficar com origem config e sem trava")
	}

	const key = `UPDATE subscriptions SET external_key = $1 WHERE id = $2`
	m.exec(key, "claude", m209Sub)
	m.fail("chave externa com maiúscula", key, "Claude", m209Sub)
	const other = `INSERT INTO subscriptions (id, company_id, name, agent, monthly_usd, external_key) VALUES ($1, $2, $3, 'claude', 20, $4)`
	m.fail("chave externa repetida na empresa", other, "00000000-0000-0000-0000-0000000000a2", m209Company, "claude-pro", "claude")
	m.exec(other, "00000000-0000-0000-0000-0000000000a2", m209Company, "claude-pro", nil)
}

// Histórico de mensalidade: a assinatura que já existia ganhou a linha inicial; a ordem do desempate é por registro.
func (m m208) history() {
	if n := m.count(`SELECT count(*) FROM subscription_monthly_history WHERE subscription_id = $1 AND origin = 'created'
		AND monthly_usd = 200 AND starts_on = '2026-03-05'`, m209Sub); n != 1 {
		m.t.Fatalf("linha inicial = %d, quer 1", n)
	}
	const add = `INSERT INTO subscription_monthly_history (company_id, subscription_id, monthly_usd, starts_on, origin, actor_type, actor_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	m.exec(add, m209Company, m209Sub, 250, "2026-06-01", "manual", "human", m209User)
	m.exec(add, m209Company, m209Sub, 100, "2026-01-01", "migrated", "system", nil)
	m.fail("mensalidade negativa", add, m209Company, m209Sub, -1, "2026-06-01", "manual", "human", m209User)
	m.fail("origem do histórico desconhecida", add, m209Company, m209Sub, 10, "2026-06-01", "auto", "system", nil)
	var v string
	if err := m.pool.QueryRow(context.Background(), `SELECT monthly_usd::text FROM subscription_monthly_history
		WHERE subscription_id = $1 AND starts_on <= '2026-07-01' ORDER BY starts_on DESC, recorded_at DESC LIMIT 1`, m209Sub).Scan(&v); err != nil || v != "250.00" {
		m.t.Fatalf("valor vigente em julho = %q (%v), quer 250.00", v, err)
	}
}

// Conferência de preços: status fechados; a falha de fonte guarda só o código.
func (m m208) priceChecks() {
	m.exec(`INSERT INTO price_check_run (id, reason, started_at, finished_at) VALUES ($1, 'daily', now(), now())`, m209Run)
	m.fail("motivo desconhecido", `INSERT INTO price_check_run (id, reason, started_at) VALUES ('00000000-0000-0000-0000-0000000000c2', 'cron', now())`)
	m.fail("fim antes do início", `INSERT INTO price_check_run (id, reason, started_at, finished_at) VALUES ('00000000-0000-0000-0000-0000000000c3', 'boot', now(), now() - interval '1 hour')`)
	const src = `INSERT INTO price_check_source (run_id, source, status, error_code) VALUES ($1, $2, $3, $4)`
	m.exec(src, m209Run, "a", "ok", "")
	m.exec(src, m209Run, "b", "down", "timeout")
	m.fail("erro numa fonte que respondeu", src, m209Run, "c", "ok", "timeout")
	const res = `INSERT INTO price_check_result (run_id, model, source, status, input_per_mtok) VALUES ($1, $2, $3, $4, $5)`
	for i, st := range []string{"equal", "changed", "new", "diverges", "locked_differs", "no_source", "source_down"} {
		m.exec(res, m209Run, "m"+string(rune('a'+i)), "a", st, nil)
	}
	m.fail("status desconhecido", res, m209Run, "mx", "a", "same", nil)
	m.fail("preço negativo", res, m209Run, "my", "a", "changed", -1)
}

// Alertas: os 17 tipos externos e os sujeitos novos; ajustes da empresa só com chave e prazo válidos.
func (m m208) alertsAndSettings() {
	const alert = `INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, opened_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, 'warning', 'x', now(), now())`
	m.exec(alert, "00000000-0000-0000-0000-0000000000f1", m209Company, "secret_rotation_due", "secret", m209Sub)
	for i, k := range []string{"queue", "destination_refusing", "host_no_data", "spool", "quota", "round_stalled", "round_old", "llm_proxy_down",
		"memory_capture", "memory_hooks_untrusted", "price_changed", "price_sources_diverge", "price_source_down", "price_model_unpriced",
		"price_locked_differs", "cost_claude_diff", "cost_subscription_unpriced"} {
		id := "00000000-0000-0000-0000-00000000f1" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		m.exec(alert, id, m209Company, k, "external_host", id)
	}
	m.fail("tipo antigo do agent-studio", alert, "00000000-0000-0000-0000-0000000000f2", m209Company, "price_fixed_differs", "model", m209Sub)
	m.fail("sujeito desconhecido", alert, "00000000-0000-0000-0000-0000000000f3", m209Company, "queue", "host", m209Sub)
	m.exec(alert, "00000000-0000-0000-0000-0000000000f4", m209Company, "price_changed", "model", "00000000-0000-0000-0000-0000000000e1")
	m.exec(alert, "00000000-0000-0000-0000-0000000000f5", m209Company, "price_source_down", "price_source", "00000000-0000-0000-0000-0000000000e2")

	const set = `INSERT INTO company_alert_settings (company_id, key, value, updated_by_type, updated_by_id) VALUES ($1, $2, $3, 'human', $4)`
	m.exec(set, m209Company, "limit.host_no_data_minutes", 45, m209User)
	m.exec(set, m209Company, "due.queue", 0, m209User)
	m.exec(set, m209Company, "due.round_stalled", 720, m209User)
	m.fail("prazo acima de 30 dias", set, m209Company, "due.price_changed", 721, m209User)
	m.fail("chave fora do formato", set, m209Company, "other.queue", 1, m209User)
	m.fail("valor negativo", set, m209Company, "limit.queue_ratio", -1, m209User)
	m.fail("chave repetida", set, m209Company, "due.queue", 1, m209User)
}
