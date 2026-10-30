package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
)

// derivRec é um registro de log com os atributos fixos que a derivação lê; campo vazio = atributo ausente.
type derivRec struct {
	id, agent, host, repo, task, round string
	at                                 int64
}

// derivBatch monta o lote como o handler monta: DecodeJSON, depois LogRows.
func derivBatch(t *testing.T, recs ...derivRec) ingest.Batch {
	t.Helper()
	parts := make([]string, 0, len(recs))
	for _, r := range recs {
		attrs := []string{attr("oute.event.id", r.id)}
		if r.agent != "" {
			attrs = append(attrs, attr("oute.agent", r.agent))
		}
		if r.host != "" {
			attrs = append(attrs, attr("host.name", r.host))
		}
		if r.repo != "" {
			attrs = append(attrs, attr("oute.task.repo", r.repo))
		}
		if r.task != "" {
			attrs = append(attrs, attr("oute.task.id", r.task))
		}
		if r.round != "" {
			attrs = append(attrs, attr("oute.swarm.round", r.round))
		}
		parts = append(parts, fmt.Sprintf(`{"timeUnixNano":"%d","body":{"stringValue":"x"},"attributes":[%s]}`, r.at, strings.Join(attrs, ",")))
	}
	doc, err := ingest.DecodeJSON([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + strings.Join(parts, ",") + `]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ingest.LogRows(doc, time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	return ingest.Batch{Logs: rows}
}

func attr(key, value string) string {
	v, _ := json.Marshal(value)
	return fmt.Sprintf(`{"key":%q,"value":{"stringValue":%s}}`, key, v)
}

// derivEnv devolve uma empresa com uma chave de ingestão já resolvida e a função que conta linhas.
func derivEnv(t *testing.T) (*env, ingest.Key, func(query string, args ...any) int) {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	actor := ingestActor(t, e)
	token, _, err := e.svc.CreateIngestKey(ctx, actor, e.co.ID, "oute-server")
	if err != nil {
		t.Fatal(err)
	}
	key, err := e.svc.ResolveIngestKey(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := e.svc.Pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
		return n
	}
	return e, key, count
}

func writeBatch(t *testing.T, e *env, key ingest.Key, b ingest.Batch) {
	t.Helper()
	if err := e.svc.WriteTelemetry(context.Background(), key, b); err != nil {
		t.Fatalf("WriteTelemetry: %v", err)
	}
}

// Agente: o mesmo nome em outra caixa é o mesmo agente; cada texto novo vira apelido; a criação gera um evento só.
func TestDeriveAgentOncePerNameAndKeepsAliases(t *testing.T) {
	e, key, count := derivEnv(t)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "a1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "a2", agent: "Claude", host: "oute-server", at: at + 1},
		derivRec{id: "a3", agent: "codex", host: "oute-server", at: at + 2},
	))
	if n := count(`SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); n != 2 {
		t.Errorf("agentes = %d, quero 2 (claude e codex)", n)
	}
	if n := count(`SELECT count(*) FROM external_agent_alias WHERE company_id = $1`, e.co.ID); n != 3 {
		t.Errorf("apelidos = %d, quero 3 (claude, Claude, codex)", n)
	}
	if n := count(`SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'external_agent.created'`, e.co.ID); n != 2 {
		t.Errorf("eventos de agente criado = %d, quero 2", n)
	}
	// outro lote com o mesmo texto não cria nada
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "a4", agent: "claude", host: "oute-server", at: at + 3}))
	if n := count(`SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); n != 2 {
		t.Errorf("depois de novo lote: agentes = %d, quero 2", n)
	}
}

// Host: a hora do último fato sobe, e nunca desce com um lote antigo.
func TestDeriveHostKeepsTheNewestFactTime(t *testing.T) {
	e, key, _ := derivEnv(t)
	base := time.Now().Truncate(time.Second).Add(time.Hour)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "h1", host: "oute-mac", at: base.UnixNano()}))
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "h2", host: "oute-mac", at: base.Add(-time.Minute).UnixNano()}))

	var seen time.Time
	if err := e.svc.Pool.QueryRow(context.Background(),
		`SELECT last_seen_at FROM external_host WHERE company_id = $1 AND name = 'oute-mac'`, e.co.ID).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen.Unix() != base.Unix() {
		t.Errorf("last_seen_at = %v, quero %v (o fato mais novo, não o lote antigo)", seen, base)
	}
}

// Emparelhamento: o repositório acha o workspace de mesmo slug; sem ele, cria um só de metadado, sem caminho, com
// evento. Um repositório já emparelhado não gera outro vínculo.
func TestDerivePairsRepoWithWorkspaceOnce(t *testing.T) {
	e, key, count := derivEnv(t)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "r1", repo: "oute-agent", host: "oute-server", at: at}))
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "r2", repo: "oute-agent", host: "oute-server", at: at + 1}))

	if n := count(`SELECT count(*) FROM repo_pairing WHERE company_id = $1 AND repo_key = 'oute-agent'`, e.co.ID); n != 1 {
		t.Errorf("emparelhamentos = %d, quero 1", n)
	}
	if n := count(`SELECT count(*) FROM workspaces WHERE company_id = $1 AND slug = 'oute-agent' AND path = '' AND NOT is_system`, e.co.ID); n != 1 {
		t.Errorf("workspaces criados = %d, quero 1, sem caminho", n)
	}
	if n := count(`SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'repo_pairing.created'`, e.co.ID); n != 1 {
		t.Errorf("eventos de emparelhamento = %d, quero 1", n)
	}
}

// Repositório com workspace de mesmo slug já existente: o emparelhamento aponta para ele, sem criar outro.
func TestDerivePairsWithExistingWorkspaceBySlug(t *testing.T) {
	e, key, count := derivEnv(t)
	ctx := context.Background()
	var existing domain.ID
	if err := e.svc.Pool.QueryRow(ctx,
		`INSERT INTO workspaces (id, company_id, slug, name, path) VALUES ($1, $2, 'hanko-site', 'Site do Hanko', '') RETURNING id`,
		domain.NewID(), e.co.ID).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "s1", repo: "hanko-site", host: "oute-server", at: time.Now().UnixNano()}))

	var got domain.ID
	if err := e.svc.Pool.QueryRow(ctx, `SELECT workspace_id FROM repo_pairing WHERE company_id = $1 AND repo_key = 'hanko-site'`, e.co.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != existing {
		t.Errorf("emparelhamento aponta para %v, quero o workspace existente %v", got, existing)
	}
	if n := count(`SELECT count(*) FROM workspaces WHERE company_id = $1 AND slug = 'hanko-site'`, e.co.ID); n != 1 {
		t.Errorf("workspaces com o slug = %d, quero 1", n)
	}
}

// Fato sem repositório vai para o `no-repo`: um workspace de sistema, sem caminho, criado uma vez por empresa.
func TestDeriveFactWithoutRepoGoesToNoRepo(t *testing.T) {
	e, key, count := derivEnv(t)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "n1", host: "oute-server", at: at}))
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "n2", host: "oute-server", at: at + 1}))
	if n := count(`SELECT count(*) FROM workspaces WHERE company_id = $1 AND is_system AND slug = 'no-repo' AND path = ''`, e.co.ID); n != 1 {
		t.Errorf("workspaces no-repo = %d, quero 1", n)
	}
}

// Empresa arquivada: o bruto segue gravando, e nenhuma entidade é criada (escrita de domínio só com a empresa ativa).
func TestDeriveSkipsArchivedCompany(t *testing.T) {
	e, key, count := derivEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE companies SET archived_at = now() WHERE id = $1`, e.co.ID); err != nil {
		t.Fatal(err)
	}
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "x1", agent: "claude", host: "oute-server", repo: "oute-agent", at: time.Now().UnixNano()}))
	if n := count(`SELECT count(*) FROM telemetry_logs WHERE company_id = $1`, e.co.ID); n != 1 {
		t.Errorf("bruto gravado = %d, quero 1", n)
	}
	if n := count(`SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); n != 0 {
		t.Errorf("agentes numa empresa arquivada = %d, quero 0", n)
	}
}

// Entrada inválida: texto fora do CHECK do banco não derruba o lote nem o bruto. Texto aparado vira entidade; o que não
// serve fica sem derivação, e o fato segue no bruto.
func TestDeriveInvalidTextKeepsRawAndTheBatch(t *testing.T) {
	e, key, count := derivEnv(t)
	at := time.Now().UnixNano()
	long := strings.Repeat("a", 101)
	longRepo := strings.Repeat("r", 501)
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "i1", agent: " claude ", host: "oute-server", repo: "oute-agent", at: at},
		derivRec{id: "i2", agent: long, host: "   ", repo: longRepo, at: at + 1},
		derivRec{id: "i3", agent: "   ", host: "oute-server", at: at + 2},
	))
	if n := count(`SELECT count(*) FROM telemetry_logs WHERE company_id = $1`, e.co.ID); n != 3 {
		t.Errorf("bruto gravado = %d, quero 3 (o lote inteiro)", n)
	}
	if n := count(`SELECT count(*) FROM external_agent WHERE company_id = $1 AND name = 'claude'`, e.co.ID); n != 1 {
		t.Errorf("agente aparado = %d, quero 1", n)
	}
	if n := count(`SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); n != 1 {
		t.Errorf("agentes = %d, quero 1: o de 101 caracteres e o vazio não entram", n)
	}
	if n := count(`SELECT count(*) FROM external_host WHERE company_id = $1`, e.co.ID); n != 1 {
		t.Errorf("hosts = %d, quero 1 (oute-server)", n)
	}
	if n := count(`SELECT count(*) FROM repo_pairing WHERE company_id = $1 AND repo_key = 'oute-agent'`, e.co.ID); n != 1 {
		t.Errorf("emparelhamento do repositório válido = %d, quero 1", n)
	}
	if n := count(`SELECT count(*) FROM workspaces WHERE company_id = $1 AND is_system`, e.co.ID); n != 1 {
		t.Errorf("no-repo = %d, quero 1: o repositório de 501 caracteres vai para ele", n)
	}
}

// Colisão: o slug `no-repo` é do workspace de sistema. Um repositório com esse nome vai para o de sistema, e nunca a um
// workspace comum de mesmo slug.
func TestDeriveRepoNamedNoRepoGoesToTheSystemWorkspace(t *testing.T) {
	e, key, count := derivEnv(t)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "c1", repo: "no-repo", host: "oute-server", at: at}))
	if n := count(`SELECT count(*) FROM repo_pairing rp JOIN workspaces w ON w.id = rp.workspace_id
		WHERE rp.company_id = $1 AND rp.repo_key = 'no-repo' AND w.is_system`, e.co.ID); n != 1 {
		t.Errorf("emparelhamento de 'no-repo' com o workspace de sistema = %d, quero 1", n)
	}
	if n := count(`SELECT count(*) FROM workspaces WHERE company_id = $1 AND slug = 'no-repo' AND NOT is_system`, e.co.ID); n != 0 {
		t.Errorf("workspace comum com slug no-repo = %d, quero 0", n)
	}
}

// Colisão: a empresa já tem um workspace comum com slug `no-repo`. O fato sem repositório não derruba o lote: o bruto
// grava e o workspace de sistema não nasce.
func TestDeriveNoRepoSlugTakenByAUserWorkspace(t *testing.T) {
	e, key, count := derivEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO workspaces (id, company_id, slug, name, path) VALUES ($1, $2, 'no-repo', 'Meu no-repo', '')`,
		domain.NewID(), e.co.ID); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "k1", host: "oute-server", at: at}))
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "k2", repo: "no-repo", host: "oute-server", at: at + 1}))
	if n := count(`SELECT count(*) FROM telemetry_logs WHERE company_id = $1`, e.co.ID); n != 2 {
		t.Errorf("bruto gravado = %d, quero 2", n)
	}
	if n := count(`SELECT count(*) FROM workspaces WHERE company_id = $1 AND is_system`, e.co.ID); n != 0 {
		t.Errorf("workspace de sistema = %d, quero 0 (o slug está tomado)", n)
	}
	if n := count(`SELECT count(*) FROM workspaces WHERE company_id = $1 AND slug = 'no-repo' AND NOT is_system`, e.co.ID); n != 1 {
		t.Errorf("workspace comum no-repo = %d, quero 1, intacto", n)
	}
}

// Corrida: lotes concorrentes com o mesmo repositório e o mesmo agente novos criam uma vez só e nenhum lote falha.
func TestDeriveConcurrentBatchesCreateOnce(t *testing.T) {
	e, key, count := derivEnv(t)
	at := time.Now().UnixNano()
	const n = 4
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		b := derivBatch(t, derivRec{id: fmt.Sprintf("p%d", i), agent: "corrida", repo: "corrida-repo", host: "oute-server", at: at + int64(i)})
		go func() { errs <- e.svc.WriteTelemetry(context.Background(), key, b) }()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("lote concorrente falhou: %v", err)
		}
	}
	if got := count(`SELECT count(*) FROM workspaces WHERE company_id = $1 AND slug = 'corrida-repo'`, e.co.ID); got != 1 {
		t.Errorf("workspaces criados = %d, quero 1", got)
	}
	if got := count(`SELECT count(*) FROM repo_pairing WHERE company_id = $1 AND repo_key = 'corrida-repo'`, e.co.ID); got != 1 {
		t.Errorf("emparelhamentos = %d, quero 1", got)
	}
	if got := count(`SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); got != 1 {
		t.Errorf("agentes = %d, quero 1", got)
	}
	if got := count(`SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'repo_pairing.created'`, e.co.ID); got != 1 {
		t.Errorf("eventos de emparelhamento = %d, quero 1", got)
	}
}
