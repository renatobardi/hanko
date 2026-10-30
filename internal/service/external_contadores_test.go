package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// dailyKey é a chave de uma linha do agregado diário (calls_daily). Modelo e assinatura são fixos nos testes.
type dailyKey struct {
	day             string
	host, agent, ws domain.ID
}

// dailyCounts são os nove contadores de uma linha do agregado diário (calls_daily).
type dailyCounts struct {
	calls, errs, input, output, cacheRead, cacheCreate, unpriced int64
	costReal, costList                                           string
}

// dailyCols são as nove colunas contadoras, na ordem de dailyCounts.
const dailyCols = `calls, errors, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, unpriced_calls, cost_real_usd, cost_list_usd`

// insertDaily põe uma linha do agregado diário (calls_daily) na chave dada, com os nove contadores.
func insertDaily(t *testing.T, e *env, k dailyKey, c dailyCounts) {
	t.Helper()
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key, `+dailyCols+`)
		VALUES ($1, $2, $3, $4, $5, 'm', 's', $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		e.co.ID, k.day, k.host, k.agent, k.ws, c.calls, c.errs, c.input, c.output, c.cacheRead, c.cacheCreate, c.unpriced, c.costReal, c.costList); err != nil {
		t.Fatal(err)
	}
}

// dailyRow lê os nove contadores da linha do agregado na chave dada.
func dailyRow(t *testing.T, e *env, k dailyKey) dailyCounts {
	t.Helper()
	var c dailyCounts
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT calls, errors, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, unpriced_calls,
		cost_real_usd::text, cost_list_usd::text FROM calls_daily
		WHERE company_id = $1 AND day = $2 AND host_id = $3 AND agent_id = $4 AND workspace_id = $5 AND model = 'm' AND subscription_key = 's'`,
		e.co.ID, k.day, k.host, k.agent, k.ws).Scan(&c.calls, &c.errs, &c.input, &c.output, &c.cacheRead, &c.cacheCreate, &c.unpriced, &c.costReal, &c.costList); err != nil {
		t.Fatalf("linha do agregado %s: %v", k.day, err)
	}
	return c
}

func newWorkspace(t *testing.T, e *env, slug string) domain.ID {
	t.Helper()
	var id domain.ID
	if err := e.svc.Pool.QueryRow(context.Background(), `INSERT INTO workspaces (id, company_id, slug, name, path) VALUES ($1, $2, $3, 'WS', '') RETURNING id`,
		domain.NewID(), e.co.ID, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// mergeEnv prepara a empresa com um admin e grava um lote de fatos: as entidades que a junção vai usar.
func mergeEnv(t *testing.T, email string, recs ...derivRec) (*env, domain.User) {
	t.Helper()
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, email, domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	writeBatch(t, e, key, derivBatch(t, recs...))
	return e, admin
}

// Juntar agente soma os nove contadores quando o que fica já tem a mesma chave, e move a linha sem colisão (#153).
func TestMergeAgentSumsCallsDailyOnKeyCollision(t *testing.T) {
	at := time.Now().UnixNano()
	e, admin := mergeEnv(t, "admin.soma.agente@x.com",
		derivRec{id: "s1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "s2", agent: "claude-velho", host: "oute-server", at: at + 1})
	host := entityID(t, e, "external_host", "oute-server")
	from := entityID(t, e, "external_agent", "claude-velho")
	into := entityID(t, e, "external_agent", "claude")
	ws := newWorkspace(t, e, "ws-soma")
	insertDaily(t, e, dailyKey{"2026-10-29", host, from, ws}, dailyCounts{calls: 3, errs: 1, input: 10, output: 20, cacheRead: 30, cacheCreate: 40, unpriced: 1, costReal: "1.500000", costList: "2.000000"}) // colide
	insertDaily(t, e, dailyKey{"2026-10-29", host, into, ws}, dailyCounts{calls: 2, input: 5, output: 6, cacheRead: 7, cacheCreate: 8, costReal: "0.500000", costList: "0.750000"})
	insertDaily(t, e, dailyKey{"2026-10-30", host, from, ws}, dailyCounts{calls: 4, input: 7, costReal: "0.250000", costList: "0.250000"}) // sem colisão: só muda de agente

	if _, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into); err != nil {
		t.Fatal(err)
	}
	want := dailyCounts{calls: 5, errs: 1, input: 15, output: 26, cacheRead: 37, cacheCreate: 48, unpriced: 1, costReal: "2.000000", costList: "2.750000"}
	if got := dailyRow(t, e, dailyKey{"2026-10-29", host, into, ws}); got != want {
		t.Errorf("soma na colisão = %+v; quero %+v", got, want)
	}
	if got := dailyRow(t, e, dailyKey{"2026-10-30", host, into, ws}); got.calls != 4 || got.input != 7 {
		t.Errorf("linha sem colisão depois da junção = %d chamadas, %d entrada; quero 4 e 7", got.calls, got.input)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM calls_daily WHERE company_id = $1 AND agent_id = $2`, e.co.ID, from); n != 0 {
		t.Errorf("linhas do agregado que ficaram no juntado = %d, quero 0", n)
	}
}

// Juntar host faz o mesmo: a chave inclui o host, então a soma acontece no host que fica.
func TestMergeHostSumsCallsDailyOnKeyCollision(t *testing.T) {
	at := time.Now().UnixNano()
	e, admin := mergeEnv(t, "admin.soma.host@x.com",
		derivRec{id: "h1", agent: "claude", host: "oute-a", at: at},
		derivRec{id: "h2", agent: "claude", host: "oute-b", at: at + 1})
	agent := entityID(t, e, "external_agent", "claude")
	from := entityID(t, e, "external_host", "oute-b")
	into := entityID(t, e, "external_host", "oute-a")
	ws := newWorkspace(t, e, "ws-soma-host")
	insertDaily(t, e, dailyKey{"2026-10-29", from, agent, ws}, dailyCounts{calls: 6, errs: 2, input: 20, output: 30, cacheRead: 40, cacheCreate: 50, unpriced: 3, costReal: "3.000000", costList: "4.000000"})
	insertDaily(t, e, dailyKey{"2026-10-29", into, agent, ws}, dailyCounts{calls: 1, input: 1, output: 2, cacheRead: 3, cacheCreate: 4, costReal: "0.100000", costList: "0.200000"})

	if _, err := e.svc.MergeExternalHost(as(admin), domain.HumanActor, e.co.ID, from, into); err != nil {
		t.Fatal(err)
	}
	want := dailyCounts{calls: 7, errs: 2, input: 21, output: 32, cacheRead: 43, cacheCreate: 54, unpriced: 3, costReal: "3.100000", costList: "4.200000"}
	if got := dailyRow(t, e, dailyKey{"2026-10-29", into, agent, ws}); got != want {
		t.Errorf("soma do host na colisão = %+v; quero %+v", got, want)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM calls_daily WHERE company_id = $1 AND host_id = $2`, e.co.ID, from); n != 0 {
		t.Errorf("linhas do agregado que ficaram no host juntado = %d, quero 0", n)
	}
}

// Repetir a junção não soma de novo: o juntado já não tem linhas e o que fica mantém os contadores.
func TestMergeCallsDailyIsIdempotent(t *testing.T) {
	at := time.Now().UnixNano()
	e, admin := mergeEnv(t, "admin.soma.idem@x.com",
		derivRec{id: "i1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "i2", agent: "codex", host: "oute-server", at: at + 1})
	host := entityID(t, e, "external_host", "oute-server")
	from := entityID(t, e, "external_agent", "codex")
	into := entityID(t, e, "external_agent", "claude")
	ws := newWorkspace(t, e, "ws-idem")
	insertDaily(t, e, dailyKey{"2026-10-29", host, from, ws}, dailyCounts{calls: 3, input: 9, costReal: "0.300000", costList: "0.300000"})
	insertDaily(t, e, dailyKey{"2026-10-29", host, into, ws}, dailyCounts{calls: 2, input: 1, costReal: "0.100000", costList: "0.100000"})
	for i := 0; i < 2; i++ {
		if _, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into); err != nil {
			t.Fatalf("junção %d: %v", i+1, err)
		}
	}
	if got := dailyRow(t, e, dailyKey{"2026-10-29", host, into, ws}); got.calls != 5 || got.input != 10 {
		t.Errorf("depois de duas junções = %d chamadas, %d entrada; quero 5 e 10 (a segunda não soma de novo)", got.calls, got.input)
	}
}

// Falha no meio da junção de host desfaz tudo: a soma e a lista já feitas voltam, e os dois hosts continuam como estavam.
func TestMergeHostRollsBackWhenALaterStepFails(t *testing.T) {
	at := time.Now().UnixNano()
	e, admin := mergeEnv(t, "admin.reverte@x.com",
		derivRec{id: "r1", agent: "claude", host: "oute-a", at: at},
		derivRec{id: "r2", agent: "claude", host: "oute-b", at: at + 1})
	agent := entityID(t, e, "external_agent", "claude")
	from := entityID(t, e, "external_host", "oute-b")
	into := entityID(t, e, "external_host", "oute-a")
	ws := newWorkspace(t, e, "ws-reverte")
	insertDaily(t, e, dailyKey{"2026-10-29", from, agent, ws}, dailyCounts{calls: 6, costReal: "1.000000", costList: "1.000000"})
	insertDaily(t, e, dailyKey{"2026-10-29", into, agent, ws}, dailyCounts{calls: 1, costReal: "0.100000", costList: "0.100000"})
	putSnapshot(t, e, from, 100, "c-from")
	// A soma e o apagar do juntado rodam antes do snapshot; este gatilho faz o snapshot falhar no meio da junção.
	for _, q := range []string{
		`CREATE FUNCTION falha_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'falha de teste'; END $$`,
		`CREATE TRIGGER falha_snapshot BEFORE INSERT ON external_outbox_snapshot FOR EACH ROW EXECUTE FUNCTION falha_snapshot()`,
	} {
		if _, err := e.svc.Pool.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}

	_, err := e.svc.MergeExternalHost(as(admin), domain.HumanActor, e.co.ID, from, into)
	if err == nil || !strings.Contains(err.Error(), "falha de teste") {
		t.Fatalf("junção com falha no meio = %v; quero a falha de teste", err)
	}
	if got := dailyRow(t, e, dailyKey{"2026-10-29", from, agent, ws}); got.calls != 6 {
		t.Errorf("agregado do juntado depois da falha = %d chamadas, quero 6 (a soma não pode ter ficado)", got.calls)
	}
	if got := dailyRow(t, e, dailyKey{"2026-10-29", into, agent, ws}); got.calls != 1 {
		t.Errorf("agregado do host que fica depois da falha = %d chamadas, quero 1", got.calls)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_host WHERE id = $1 OR id = $2`, from, into); n != 2 {
		t.Errorf("hosts depois da falha = %d, quero 2", n)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_outbox_snapshot WHERE host_id = $1`, from); n != 1 {
		t.Errorf("lista do juntado depois da falha = %d, quero 1", n)
	}
}

// A junção de um host não toca dados de outra empresa: linhas do agregado, lista da outbox e entidades com os mesmos nomes.
func TestMergeLeavesOtherCompanyAlone(t *testing.T) {
	at := time.Now().UnixNano()
	e, admin := mergeEnv(t, "admin.outra@x.com",
		derivRec{id: "o1", agent: "claude", host: "oute-a", at: at},
		derivRec{id: "o2", agent: "claude", host: "oute-b", at: at + 1})
	other, err := e.svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var agentB, hostB, wsB domain.ID
	for _, q := range []struct {
		dst *domain.ID
		sql string
		arg []any
	}{
		{&agentB, `INSERT INTO external_agent (id, company_id, name, created_at, updated_at) VALUES ($1, $2, 'claude', now(), now()) RETURNING id`, []any{domain.NewID(), other.ID}},
		{&hostB, `INSERT INTO external_host (id, company_id, name, created_at, updated_at) VALUES ($1, $2, 'oute-b', now(), now()) RETURNING id`, []any{domain.NewID(), other.ID}},
		{&wsB, `INSERT INTO workspaces (id, company_id, slug, name, path) VALUES ($1, $2, 'ws-outra', 'WS', '') RETURNING id`, []any{domain.NewID(), other.ID}},
	} {
		if err := e.svc.Pool.QueryRow(ctx, q.sql, q.arg...).Scan(q.dst); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, calls) VALUES ($1, '2026-10-29', $2, $3, $4, 7)`,
		other.ID, hostB, agentB, wsB); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO external_outbox_snapshot (host_id, company_id, snapshot_at, canal_ids, received_at) VALUES ($1, $2, 50, ARRAY['c-outra']::text[], 1)`,
		hostB, other.ID); err != nil {
		t.Fatal(err)
	}
	from := entityID(t, e, "external_host", "oute-b")
	into := entityID(t, e, "external_host", "oute-a")
	if _, err := e.svc.MergeExternalHost(as(admin), domain.HumanActor, e.co.ID, from, into); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM calls_daily WHERE company_id = $1 AND host_id = $2 AND agent_id = $3 AND calls = 7`, other.ID, hostB, agentB); n != 1 {
		t.Errorf("agregado da outra empresa mudou: %d linhas com 7 chamadas, quero 1", n)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_outbox_snapshot WHERE host_id = $1 AND canal_ids[1] = 'c-outra'`, hostB); n != 1 {
		t.Errorf("lista da outbox da outra empresa mudou: %d, quero 1", n)
	}
}

// snapshotCase é uma junção de host com as listas da outbox: intoAt e fromAt são as horas (nil = sem lista).
type snapshotCase struct {
	name           string
	intoAt, fromAt *int64
	wantCanal      string // canal da lista que fica depois da junção; "" = o que fica não tem lista
}

// Snapshot da outbox: a lista de hora mais nova vence (ADR-0081 item 5). Juntar copia a do juntado se ela for mais nova,
// e em empate ou quando é mais velha, fica a do que fica. Sem lista nos dois lados, o que fica continua sem lista.
func TestMergeOutboxSnapshotKeepsTheNewestList(t *testing.T) {
	cases := []snapshotCase{
		{"só o juntado tem lista", nil, ptrInt(100), "c-from"},
		{"o que fica tem lista mais velha", ptrInt(50), ptrInt(100), "c-from"},
		{"o que fica tem lista mais nova", ptrInt(200), ptrInt(100), "c-into"},
		{"empate fica o que fica", ptrInt(100), ptrInt(100), "c-into"},
		{"nenhum dos lados tem lista", nil, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { mergeSnapshotCase(t, c) })
	}
}

// mergeSnapshotCase junta o host do caso e confere qual lista da outbox ficou no host que fica.
func mergeSnapshotCase(t *testing.T, c snapshotCase) {
	at := time.Now().UnixNano()
	e, admin := mergeEnv(t, "admin.snapshot@x.com",
		derivRec{id: "p1", agent: "claude", host: "oute-a", at: at},
		derivRec{id: "p2", agent: "claude", host: "oute-b", at: at + 1})
	from := entityID(t, e, "external_host", "oute-b")
	into := entityID(t, e, "external_host", "oute-a")
	if c.fromAt != nil {
		putSnapshot(t, e, from, *c.fromAt, "c-from")
	}
	if c.intoAt != nil {
		putSnapshot(t, e, into, *c.intoAt, "c-into")
	}
	if _, err := e.svc.MergeExternalHost(as(admin), domain.HumanActor, e.co.ID, from, into); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_outbox_snapshot WHERE host_id = $1`, from); n != 0 {
		t.Errorf("lista do juntado que ficou = %d, quero 0", n)
	}
	if c.wantCanal == "" {
		if n := rowCount(t, e, `SELECT count(*) FROM external_outbox_snapshot WHERE host_id = $1`, into); n != 0 {
			t.Errorf("lista do que fica = %d linhas, quero nenhuma", n)
		}
		return
	}
	var canal string
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT canal_ids[1] FROM external_outbox_snapshot WHERE host_id = $1`, into).Scan(&canal); err != nil {
		t.Fatalf("lista do que fica: %v", err)
	}
	if canal != c.wantCanal {
		t.Errorf("lista do que fica = %q, quero %q", canal, c.wantCanal)
	}
}

// putSnapshot grava a lista de um host com a hora e um canal de referência.
func putSnapshot(t *testing.T, e *env, host domain.ID, at int64, canal string) {
	t.Helper()
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO external_outbox_snapshot (host_id, company_id, snapshot_at, canal_ids, received_at)
		VALUES ($1, $2, $3, ARRAY[$4]::text[], 1)`, host, e.co.ID, at, canal); err != nil {
		t.Fatal(err)
	}
}

func ptrInt(v int64) *int64 { return &v }
