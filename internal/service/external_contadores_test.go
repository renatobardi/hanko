package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// insertDaily põe uma linha do agregado diário (calls_daily) com os contadores dados.
func insertDaily(t *testing.T, e *env, day string, host, agent, ws domain.ID, calls, errs, input int64, costReal string) {
	t.Helper()
	if _, err := e.svc.Pool.Exec(context.Background(), `INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key,
		calls, errors, input_tokens, cost_real_usd) VALUES ($1, $2, $3, $4, $5, 'm', 's', $6, $7, $8, $9)`,
		e.co.ID, day, host, agent, ws, calls, errs, input, costReal); err != nil {
		t.Fatal(err)
	}
}

// dailyRow lê os contadores da linha do agregado na chave dada.
func dailyRow(t *testing.T, e *env, day string, host, agent, ws domain.ID) (calls, errs, input int64, cost string) {
	t.Helper()
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT calls, errors, input_tokens, cost_real_usd::text FROM calls_daily
		WHERE company_id = $1 AND day = $2 AND host_id = $3 AND agent_id = $4 AND workspace_id = $5 AND model = 'm' AND subscription_key = 's'`,
		e.co.ID, day, host, agent, ws).Scan(&calls, &errs, &input, &cost); err != nil {
		t.Fatalf("linha do agregado %s: %v", day, err)
	}
	return calls, errs, input, cost
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

// Juntar agente soma o agregado diário quando o que fica já tem a mesma chave, e move a linha sem colisão (#153).
func TestMergeAgentSumsCallsDailyOnKeyCollision(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.soma.agente@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "s1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "s2", agent: "claude-velho", host: "oute-server", at: at + 1}))
	host := entityID(t, e, "external_host", "oute-server")
	from := entityID(t, e, "external_agent", "claude-velho")
	into := entityID(t, e, "external_agent", "claude")
	ws := newWorkspace(t, e, "ws-soma")
	insertDaily(t, e, "2026-10-29", host, from, ws, 3, 1, 10, "1.500000") // colide com a linha do que fica
	insertDaily(t, e, "2026-10-29", host, into, ws, 2, 0, 5, "0.500000")
	insertDaily(t, e, "2026-10-30", host, from, ws, 4, 0, 7, "0.250000") // sem colisão: só muda de agente

	if _, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into); err != nil {
		t.Fatal(err)
	}
	calls, errs, input, cost := dailyRow(t, e, "2026-10-29", host, into, ws)
	if calls != 5 || errs != 1 || input != 15 || cost != "2.000000" {
		t.Errorf("soma na colisão = calls %d, erros %d, entrada %d, custo %s; quero 5, 1, 15, 2.000000", calls, errs, input, cost)
	}
	if calls, _, _, _ := dailyRow(t, e, "2026-10-30", host, into, ws); calls != 4 {
		t.Errorf("linha sem colisão depois da junção = %d chamadas, quero 4", calls)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM calls_daily WHERE company_id = $1 AND agent_id = $2`, e.co.ID, from); n != 0 {
		t.Errorf("linhas do agregado que ficaram no juntado = %d, quero 0", n)
	}
}

// Juntar host faz o mesmo: a chave inclui o host, então a soma acontece no host que fica.
func TestMergeHostSumsCallsDailyOnKeyCollision(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.soma.host@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "h1", agent: "claude", host: "oute-a", at: at},
		derivRec{id: "h2", agent: "claude", host: "oute-b", at: at + 1}))
	agent := entityID(t, e, "external_agent", "claude")
	from := entityID(t, e, "external_host", "oute-b")
	into := entityID(t, e, "external_host", "oute-a")
	ws := newWorkspace(t, e, "ws-soma-host")
	insertDaily(t, e, "2026-10-29", from, agent, ws, 6, 2, 20, "3.000000")
	insertDaily(t, e, "2026-10-29", into, agent, ws, 1, 0, 1, "0.100000")

	if _, err := e.svc.MergeExternalHost(as(admin), domain.HumanActor, e.co.ID, from, into); err != nil {
		t.Fatal(err)
	}
	calls, errs, input, cost := dailyRow(t, e, "2026-10-29", into, agent, ws)
	if calls != 7 || errs != 2 || input != 21 || cost != "3.100000" {
		t.Errorf("soma do host na colisão = %d, %d, %d, %s; quero 7, 2, 21, 3.100000", calls, errs, input, cost)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM calls_daily WHERE company_id = $1 AND host_id = $2`, e.co.ID, from); n != 0 {
		t.Errorf("linhas do agregado que ficaram no host juntado = %d, quero 0", n)
	}
}

// Repetir a junção não soma de novo: o juntado já não tem linhas e o que fica mantém os contadores.
func TestMergeCallsDailyIsIdempotent(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.soma.idem@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "i1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "i2", agent: "codex", host: "oute-server", at: at + 1}))
	host := entityID(t, e, "external_host", "oute-server")
	from := entityID(t, e, "external_agent", "codex")
	into := entityID(t, e, "external_agent", "claude")
	ws := newWorkspace(t, e, "ws-idem")
	insertDaily(t, e, "2026-10-29", host, from, ws, 3, 0, 9, "0.300000")
	insertDaily(t, e, "2026-10-29", host, into, ws, 2, 0, 1, "0.100000")
	for i := 0; i < 2; i++ {
		if _, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into); err != nil {
			t.Fatalf("junção %d: %v", i+1, err)
		}
	}
	if calls, _, _, _ := dailyRow(t, e, "2026-10-29", host, into, ws); calls != 5 {
		t.Errorf("chamadas depois de duas junções = %d, quero 5 (a segunda não soma de novo)", calls)
	}
}

// Snapshot da outbox: a lista de hora mais nova vence (ADR-0081 item 5). Juntar copia a do juntado se ela for mais nova,
// e em empate ou quando é mais velha, fica a do que fica.
func TestMergeOutboxSnapshotKeepsTheNewestList(t *testing.T) {
	cases := []struct {
		name           string
		intoAt, fromAt *int64 // nil = sem lista
		wantFromWins   bool
		wantIntoCanal  string
	}{
		{"só o juntado tem lista", nil, ptrInt(100), true, "c-from"},
		{"o que fica tem lista mais velha", ptrInt(50), ptrInt(100), true, "c-from"},
		{"o que fica tem lista mais nova", ptrInt(200), ptrInt(100), false, "c-into"},
		{"empate fica o que fica", ptrInt(100), ptrInt(100), false, "c-into"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, key, _ := derivEnv(t)
			admin := mkUser(t, e, "admin.snapshot@x.com", domain.RoleMember)
			join(t, e, admin, domain.MemberAdmin)
			writeBatch(t, e, key, derivBatch(t,
				derivRec{id: "p1", agent: "claude", host: "oute-a", at: time.Now().UnixNano()},
				derivRec{id: "p2", agent: "claude", host: "oute-b", at: time.Now().UnixNano() + 1}))
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
			var canal string
			if err := e.svc.Pool.QueryRow(context.Background(), `SELECT canal_ids[1] FROM external_outbox_snapshot WHERE host_id = $1`, into).Scan(&canal); err != nil {
				t.Fatalf("lista do que fica: %v", err)
			}
			if canal != c.wantIntoCanal {
				t.Errorf("lista do que fica = %q, quero %q", canal, c.wantIntoCanal)
			}
		})
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
