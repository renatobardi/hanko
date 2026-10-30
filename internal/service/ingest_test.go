package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// ingestEnv é um serviço sobre um banco limpo: uma empresa, um dono e uma chave de ingestão da empresa.
type ingestEnv struct {
	ctx   context.Context
	svc   *service.Service
	pool  *pgxpool.Pool
	owner domain.User
	co    domain.Company
	key   string
	keyID domain.ID
}

func newIngestEnv(t *testing.T) *ingestEnv {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	svc := service.New(pool, &fakeJobs{}, events.NewBroker())
	owner, err := svc.CreateOwner(ctx, "dono@exemplo.com", "Dona", goodPass)
	if err != nil {
		t.Fatalf("dono: %v", err)
	}
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Oute"})
	if err != nil {
		t.Fatalf("empresa: %v", err)
	}
	key, info, err := svc.CreateIngestAPIKey(ctx, owner.ID, co.ID, "oute-server", "oute-server")
	if err != nil {
		t.Fatalf("chave de ingestão: %v", err)
	}
	return &ingestEnv{ctx: ctx, svc: svc, pool: pool, owner: owner, co: co, key: key, keyID: info.ID}
}

// span é um span já decodificado: uma chamada da conversa `session`, do agente `agent`, no host `host`, na hora `at`.
func span(session, host, agent string, at int64, n int) ingest.Span {
	return ingest.Span{
		DedupeKey:          fmt.Sprintf("s:%s:%d", session, n),
		TimeUnixNano:       at,
		TraceID:            session,
		SpanID:             fmt.Sprint(n),
		HostName:           host,
		OuteAgent:          agent,
		SessionID:          session,
		OuteSubscription:   "claude",
		ResourceAttributes: []byte("{}"),
		Attributes:         []byte("{}"),
		Events:             []byte("[]"),
		Links:              []byte("[]"),
	}
}

func (e *ingestEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (e *ingestEnv) ingest(t *testing.T, spans ...ingest.Span) service.TraceIngestResult {
	t.Helper()
	res, err := e.svc.IngestTraces(e.ctx, e.co.ID, e.keyID, spans)
	if err != nil {
		t.Fatalf("IngestTraces: %v", err)
	}
	return res
}

// Critério de aceite do #125: fato OTLP duplicado não duplica a conversa.
func TestIngestTracesDuplicateDoesNotDuplicateTheConversation(t *testing.T) {
	e := newIngestEnv(t)
	batch := []ingest.Span{span("sess-1", "oute-server", "claude", 100, 1), span("sess-1", "oute-server", "claude", 200, 2)}

	first := e.ingest(t, batch...)
	if first.Accepted != 2 || first.Duplicates != 0 {
		t.Fatalf("primeiro lote = %+v, quero 2 novos", first)
	}
	again := e.ingest(t, batch...)
	if again.Accepted != 0 || again.Duplicates != 2 {
		t.Fatalf("reenvio = %+v, quero 2 repetidos", again)
	}

	if n := e.count(t, `SELECT count(*) FROM telemetry_spans WHERE company_id = $1`, e.co.ID); n != 2 {
		t.Errorf("spans gravados = %d, quero 2", n)
	}
	if n := e.count(t, `SELECT count(*) FROM external_conversation WHERE company_id = $1 AND session_id = 'sess-1'`, e.co.ID); n != 1 {
		t.Errorf("conversas = %d, quero 1", n)
	}
	var first0, last0 int64
	if err := e.pool.QueryRow(e.ctx, `SELECT first_at, last_at FROM external_conversation WHERE session_id = 'sess-1'`).Scan(&first0, &last0); err != nil {
		t.Fatal(err)
	}
	if first0 != 100 || last0 != 200 {
		t.Errorf("janela = %d..%d, quero 100..200", first0, last0)
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'external_conversation.created'`, e.co.ID); n != 1 {
		t.Errorf("eventos de conversa criada = %d, quero 1", n)
	}
}

// Critério de aceite do #125: chave sem o escopo ingest não é aceita no listener, e a chave ingest não serve na API.
func TestResolveIngestKeyRefusesEveryOtherScope(t *testing.T) {
	e := newIngestEnv(t)
	co, keyID, err := e.svc.ResolveIngestKey(e.ctx, e.key)
	if err != nil || co != e.co.ID || keyID != e.keyID {
		t.Fatalf("chave ingest = %v %v %v, quero a empresa e o id dela", co, keyID, err)
	}

	fullKey, _, err := e.svc.CreateAPIKey(e.ctx, e.owner.ID, "ci")
	if err != nil {
		t.Fatal(err)
	}
	readKey, _, err := e.svc.CreateAPIKeyScoped(e.ctx, e.owner.ID, "leitura", domain.KeyScopeRead, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]string{"full": fullKey, "read": readKey} {
		if _, _, err := e.svc.ResolveIngestKey(e.ctx, key); !errors.Is(err, domain.ErrNotAllowed) {
			t.Errorf("chave %s no listener de ingestão: erro = %v, quero ErrNotAllowed", name, err)
		}
	}
	if _, _, err := e.svc.ResolveIngestKey(e.ctx, "hk_inexistente000000000000000000000000000000"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Errorf("chave inexistente: erro = %v, quero ErrUnauthenticated", err)
	}
	if _, _, err := e.svc.CreateAPIKeyScoped(e.ctx, e.owner.ID, "x", domain.KeyScopeIngest, ""); err == nil {
		t.Error("a criação comum não pode emitir chave ingest: ela tem de nascer presa à empresa")
	}

	// a chave ingest não lê: a API pública a recusa pelo escopo, e a identidade que ela cria não tem a empresa do listener
	if _, info, err := e.svc.ResolveAPIKeyInfo(e.ctx, e.key); err != nil || info.Scope != domain.KeyScopeIngest {
		t.Fatalf("ResolveAPIKeyInfo da chave ingest = %+v, %v", info, err)
	}
}

// Critério de aceite do #125: nome gerado segue o padrão do ADR-0084 e não repete numa empresa.
func TestIngestTracesNamesEachConversationOnceInACompany(t *testing.T) {
	e := newIngestEnv(t)
	seedA, seedB := collidingSessions(t)

	batch := []ingest.Span{span(seedA, "oute-server", "claude", 100, 1), span(seedB, "oute-server", "claude", 101, 1)}
	for i := range 40 {
		batch = append(batch, span(fmt.Sprintf("sess-%d", i), "oute-server", "claude", int64(200+i), 1))
	}
	e.ingest(t, batch...)

	if n := e.count(t, `SELECT count(*) FROM external_conversation WHERE company_id = $1`, e.co.ID); n != 42 {
		t.Fatalf("conversas = %d, quero 42", n)
	}
	if n := e.count(t, `SELECT count(DISTINCT name) FROM external_conversation WHERE company_id = $1`, e.co.ID); n != 42 {
		t.Fatalf("nomes distintos = %d, quero 42: algum nome repetiu", n)
	}
	rows, err := e.pool.Query(e.ctx, `SELECT session_id, name FROM external_conversation WHERE company_id = $1`, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var session, name string
		if err := rows.Scan(&session, &name); err != nil {
			t.Fatal(err)
		}
		if !domain.ValidExternalConversationName(name) {
			t.Errorf("conversa %s: nome %q fora do padrão do ADR-0084", session, name)
		}
	}
	// o segundo dos dois que colidiram sai na tentativa seguinte
	var nameB string
	if err := e.pool.QueryRow(e.ctx, `SELECT name FROM external_conversation WHERE session_id = $1`, seedB).Scan(&nameB); err != nil {
		t.Fatal(err)
	}
	if nameB != domain.ExternalName(seedB, 1) {
		t.Errorf("colidido saiu como %q, quero a tentativa 1 %q", nameB, domain.ExternalName(seedB, 1))
	}
}

// collidingSessions acha dois session.id cuja tentativa 0 dá o mesmo nome: o caso que o gerador tem de resolver.
func collidingSessions(t *testing.T) (string, string) {
	t.Helper()
	seen := map[string]string{}
	for i := range 100000 {
		seed := fmt.Sprintf("colide-%d", i)
		name := domain.ExternalName(seed, 0)
		if prev, ok := seen[name]; ok {
			return prev, seed
		}
		seen[name] = seed
	}
	t.Fatal("nenhuma colisão achada")
	return "", ""
}

// O valor de um atributo da conversa é o da hora mais antiga que o trouxe, não o do último fato (ADR-0078 item 1).
func TestIngestTracesKeepsTheEarliestValueOfAnAttribute(t *testing.T) {
	e := newIngestEnv(t)
	e.ingest(t, span("sess-2", "oute-mac", "claude", 200, 1))
	e.ingest(t, span("sess-2", "oute-server", "claude", 100, 2))

	var host string
	if err := e.pool.QueryRow(e.ctx, `SELECT h.name FROM external_conversation c JOIN external_host h ON h.id = c.host_id
		WHERE c.session_id = 'sess-2'`).Scan(&host); err != nil {
		t.Fatal(err)
	}
	if host != "oute-server" {
		t.Errorf("host da conversa = %q, quero o do fato mais antigo (oute-server)", host)
	}
	var first, last int64
	if err := e.pool.QueryRow(e.ctx, `SELECT first_at, last_at FROM external_conversation WHERE session_id = 'sess-2'`).Scan(&first, &last); err != nil {
		t.Fatal(err)
	}
	if first != 100 || last != 200 {
		t.Errorf("janela = %d..%d, quero 100..200", first, last)
	}
}

// Agente com o mesmo nome em outra caixa é o mesmo agente; o texto que chegou vira apelido (ADR-0075 item 2).
func TestIngestTracesAgentNameIgnoresCase(t *testing.T) {
	e := newIngestEnv(t)
	e.ingest(t, span("sess-3", "oute-server", "claude", 100, 1), span("sess-4", "oute-server", "Claude", 101, 1))
	if n := e.count(t, `SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); n != 1 {
		t.Errorf("agentes = %d, quero 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM external_agent_alias WHERE company_id = $1`, e.co.ID); n != 2 {
		t.Errorf("apelidos = %d, quero 2 (claude e Claude)", n)
	}
}
