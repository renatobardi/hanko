package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
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

// rawRecord monta um registro de log com o atributo `oute.task.id` exatamente como veio (inclusive vazio ou com NUL).
func rawRecord(id, task string, at int64) string {
	return fmt.Sprintf(`{"timeUnixNano":"%d","body":{"stringValue":"x"},"attributes":[%s,%s]}`,
		at, attr("oute.event.id", id), attr("oute.task.id", task))
}

// Task id inválido (vazio, só espaços, acima de 200 runas, com NUL) não derruba o lote nem o bruto: nenhuma sessão nasce.
func TestDeriveSessionInvalidTaskIdKeepsRawAndBatch(t *testing.T) {
	e, key, count := derivEnv(t)
	at := time.Now().UnixNano()
	invalid := []string{"", "   ", strings.Repeat("t", 201), "tarefa\x00nula"}
	recs := make([]string, 0, len(invalid))
	for i, task := range invalid {
		recs = append(recs, rawRecord(fmt.Sprintf("bad-%d", i), task, at+int64(i)))
	}
	doc, err := ingest.DecodeJSON([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + strings.Join(recs, ",") + `]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ingest.LogRows(doc, time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	writeBatch(t, e, key, ingest.Batch{Logs: rows})

	if n := count(`SELECT count(*) FROM telemetry_logs WHERE company_id = $1`, e.co.ID); n != len(invalid) {
		t.Errorf("bruto gravado = %d, quero %d", n, len(invalid))
	}
	if n := count(`SELECT count(*) FROM external_session WHERE company_id = $1`, e.co.ID); n != 0 {
		t.Errorf("sessões com task id inválido = %d, quero 0", n)
	}
}

// Corrida: lotes concorrentes com a mesma task_id e horas diferentes deixam a sessão igual à de um lote só com todos os
// fatos. A regra do UPDATE tem de valer sob read committed: o valor é o do fato mais antigo, a janela cobre tudo.
func TestDeriveSessionConcurrentBatchesMatchSerial(t *testing.T) {
	e, key, count := derivEnv(t)
	base := time.Now().Truncate(time.Second).Add(time.Hour).UnixNano()
	facts := []derivRec{
		{id: "c1", task: "task-race", repo: "oute-api", agent: "gemini", round: "r3", host: "oute-server", at: base + 300},
		{id: "c2", task: "task-race", repo: "oute-web", agent: "codex", round: "r2", host: "oute-server", at: base + 200},
		{id: "c3", task: "task-race", repo: "oute-agent", agent: "claude", round: "r1", host: "oute-server", at: base + 100},
		{id: "c4", task: "task-race", repo: "oute-site", agent: "claude", round: "r4", host: "oute-server", at: base + 400},
	}
	errs := make(chan error, len(facts))
	for _, f := range facts {
		b := derivBatch(t, f)
		go func() { errs <- e.svc.WriteTelemetry(context.Background(), key, b) }()
	}
	for range facts {
		if err := <-errs; err != nil {
			t.Fatalf("lote concorrente falhou: %v", err)
		}
	}
	if n := count(`SELECT count(*) FROM external_session WHERE company_id = $1 AND task_id = 'task-race'`, e.co.ID); n != 1 {
		t.Fatalf("sessões = %d, quero 1", n)
	}
	var repo, round, agentName string
	var first, last int64
	if err := e.svc.Pool.QueryRow(context.Background(),
		`SELECT s.oute_repo, s.round, a.name, s.first_at, s.last_at
		   FROM external_session s LEFT JOIN external_agent a ON a.id = s.agent_id
		  WHERE s.company_id = $1 AND s.task_id = 'task-race'`, e.co.ID).Scan(&repo, &round, &agentName, &first, &last); err != nil {
		t.Fatal(err)
	}
	// o fato mais antigo é c3 (base+100): repositório oute-agent, agente claude, rodada r1; janela base+100..base+400
	if repo != "oute-agent" || round != "r1" || agentName != "claude" {
		t.Errorf("atributos = repo %q, rodada %q, agente %q; quero os do fato mais antigo (oute-agent, r1, claude)", repo, round, agentName)
	}
	if first != base+100 || last != base+400 {
		t.Errorf("janela = %d..%d, quero %d..%d", first, last, base+100, base+400)
	}
}

// NUL em agente e host (colunas fixas) não derruba o lote: o valor vira ausente, e o fato segue no bruto.
func TestDeriveNulInFixedAttributesKeepsRawAndBatch(t *testing.T) {
	e, key, count := derivEnv(t)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "nul-1", agent: "cl\x00aude", host: "ho\x00st", task: "task-nul", at: at},
		derivRec{id: "nul-2", agent: "codex", host: "oute-server", at: at + 1},
	))
	if n := count(`SELECT count(*) FROM telemetry_logs WHERE company_id = $1`, e.co.ID); n != 2 {
		t.Errorf("bruto gravado = %d, quero 2", n)
	}
	// só o agente válido do lote existe: o "cl\x00aude" não vira agente
	if n := count(`SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); n != 1 {
		t.Errorf("agentes = %d, quero 1 (só codex)", n)
	}
	if n := count(`SELECT count(*) FROM external_agent WHERE company_id = $1 AND name = 'codex'`, e.co.ID); n != 1 {
		t.Errorf("agente válido do mesmo lote = %d, quero 1", n)
	}
}
