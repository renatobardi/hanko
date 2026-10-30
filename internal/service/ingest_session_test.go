package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Sessão: uma por `oute.task.id` na empresa, criada uma vez, e cada atributo fica com o valor do fato mais antigo que o
// trouxe (ADR-0078 item 2 e 1). A hora da sessão vai do fato mais antigo ao mais novo.
func TestDeriveSessionOncePerTaskAndKeepsTheOldestValue(t *testing.T) {
	e, key, count := derivEnv(t)
	base := time.Now().Truncate(time.Second).Add(time.Hour).UnixNano()
	// lote 1: fato mais novo traz repositório, agente e rodada
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "s1", task: "task-7", repo: "oute-agent", agent: "claude", round: "r2", host: "oute-server", at: base + 200}))
	// lote 2: fato mais antigo traz outros valores; eles trocam os do lote 1
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "s2", task: "task-7", repo: "oute-web", agent: "codex", round: "r1", host: "oute-server", at: base + 100}))
	// lote 3: fato mais novo não troca nada
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "s3", task: "task-7", repo: "oute-api", agent: "gemini", round: "r9", host: "oute-server", at: base + 300}))

	if n := count(`SELECT count(*) FROM external_session WHERE company_id = $1 AND task_id = 'task-7'`, e.co.ID); n != 1 {
		t.Fatalf("sessões = %d, quero 1", n)
	}
	var repo, round string
	var first, last int64
	var agentName string
	if err := e.svc.Pool.QueryRow(context.Background(),
		`SELECT s.oute_repo, s.round, a.name, s.first_at, s.last_at
		   FROM external_session s LEFT JOIN external_agent a ON a.id = s.agent_id
		  WHERE s.company_id = $1 AND s.task_id = 'task-7'`, e.co.ID).Scan(&repo, &round, &agentName, &first, &last); err != nil {
		t.Fatal(err)
	}
	if repo != "oute-web" || round != "r1" || agentName != "codex" {
		t.Errorf("atributos = repo %q, rodada %q, agente %q; quero os do fato mais antigo (oute-web, r1, codex)", repo, round, agentName)
	}
	if first != base+100 || last != base+300 {
		t.Errorf("janela = %d..%d, quero %d..%d (do fato mais antigo ao mais novo, em ns)", first, last, base+100, base+300)
	}
	if n := count(`SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'external_session.created'`, e.co.ID); n != 1 {
		t.Errorf("eventos de sessão criada = %d, quero 1", n)
	}
}

// Sessão sem `oute.task.id` não nasce: a conversa sem sessão fica fora dela (ADR-0078 item 2).
func TestDeriveSessionNeedsATaskId(t *testing.T) {
	e, key, count := derivEnv(t)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "n1", agent: "claude", host: "oute-server", repo: "oute-agent", at: time.Now().UnixNano()}))
	if n := count(`SELECT count(*) FROM external_session WHERE company_id = $1`, e.co.ID); n != 0 {
		t.Errorf("sessões sem task_id = %d, quero 0", n)
	}
}

// A sessão segue o workspace do repositório que ela adotou: o emparelhamento do repositório mais antigo.
func TestDeriveSessionFollowsTheRepoWorkspace(t *testing.T) {
	e, key, _ := derivEnv(t)
	ctx := context.Background()
	base := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "w1", task: "task-8", repo: "oute-agent", host: "oute-server", at: base + 10}))
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "w2", task: "task-8", repo: "hanko-site", host: "oute-server", at: base}))

	var sessionWS, pairedWS domain.ID
	if err := e.svc.Pool.QueryRow(ctx, `SELECT workspace_id FROM external_session WHERE company_id = $1 AND task_id = 'task-8'`, e.co.ID).Scan(&sessionWS); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Pool.QueryRow(ctx, `SELECT workspace_id FROM repo_pairing WHERE company_id = $1 AND repo_key = 'hanko-site'`, e.co.ID).Scan(&pairedWS); err != nil {
		t.Fatal(err)
	}
	if sessionWS != pairedWS {
		t.Errorf("workspace da sessão = %v, quero o do repositório mais antigo %v", sessionWS, pairedWS)
	}
}
