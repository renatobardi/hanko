package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
)

func ingestActor(t *testing.T, e *env) domain.Actor {
	t.Helper()
	uid := domain.NewID()
	if _, err := e.svc.Pool.Exec(context.Background(),
		`INSERT INTO users (id, email, provider, password_hash) VALUES ($1, $2, 'local', 'x')`, uid, uid.String()+"@exemplo.org"); err != nil {
		t.Fatal(err)
	}
	return domain.Actor{Type: domain.ActorHuman, ID: &uid}
}

// A chave de ingestão: criada por owner ou admin, presa à empresa, só vale na ingestão, e revogada deixa de valer.
func TestIngestKeyLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	actor := ingestActor(t, e)

	token, key, err := e.svc.CreateIngestKey(ctx, actor, e.co.ID, "oute-server")
	if err != nil || token == "" || key.CompanyID != e.co.ID || key.Name != "oute-server" {
		t.Fatalf("CreateIngestKey = %q %+v %v", token, key, err)
	}
	if _, _, err := e.svc.CreateIngestKey(ctx, domain.HumanActor, e.co.ID, "x"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ator sem usuário: %v, quer ErrForbidden", err)
	}
	if _, _, err := e.svc.CreateIngestKey(ctx, actor, e.co.ID, " com espaço "); err != nil {
		t.Errorf("o nome é aparado: %v", err)
	}
	if _, _, err := e.svc.CreateIngestKey(ctx, actor, e.co.ID, ""); err == nil {
		t.Error("nome vazio deveria ser recusado")
	}

	k, err := e.svc.ResolveIngestKey(ctx, token)
	if err != nil || k.ID != key.ID || k.CompanyID != e.co.ID {
		t.Fatalf("ResolveIngestKey = %+v %v", k, err)
	}
	if _, err := e.svc.ResolveIngestKey(ctx, "hk_inexistente"); !errors.Is(err, ingest.ErrUnauthenticated) {
		t.Errorf("chave inexistente: %v, quer ErrUnauthenticated", err)
	}
	if _, err := e.svc.ResolveIngestKey(ctx, "sem-prefixo"); !errors.Is(err, ingest.ErrUnauthenticated) {
		t.Errorf("token sem o prefixo de API key: %v, quer ErrUnauthenticated", err)
	}
	full, _, err := e.svc.CreateAPIKey(ctx, *actor.ID, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveIngestKey(ctx, full); !errors.Is(err, ingest.ErrWrongScope) {
		t.Errorf("chave full na ingestão: %v, quer ErrWrongScope", err)
	}

	keys, err := e.svc.ListIngestKeys(ctx, e.co.ID, false, 50, 0)
	if err != nil || len(keys) != 2 {
		t.Fatalf("ListIngestKeys = %d chaves (%v), quer 2 (a chave full não entra)", len(keys), err)
	}
	if _, err := e.svc.RevokeIngestKey(ctx, actor, e.co.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RevokeIngestKey(ctx, actor, e.co.ID, key.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revogar de novo: %v, quer ErrNotFound", err)
	}
	if _, err := e.svc.ResolveIngestKey(ctx, token); !errors.Is(err, ingest.ErrUnauthenticated) {
		t.Errorf("chave revogada: %v, quer ErrUnauthenticated", err)
	}
	if keys, _ := e.svc.ListIngestKeys(ctx, e.co.ID, false, 50, 0); len(keys) != 1 {
		t.Errorf("sem revogadas = %d, quer 1", len(keys))
	}
	if keys, _ := e.svc.ListIngestKeys(ctx, e.co.ID, true, 50, 0); len(keys) != 2 {
		t.Errorf("com revogadas = %d, quer 2", len(keys))
	}
}

// logPayload monta um lote de logs; cada id tem a hora do fato fixa, então o mesmo id repetido é o mesmo fato.
func logPayload(t *testing.T, eventIDs ...string) any {
	t.Helper()
	base := time.Now().UnixNano()
	recs := ""
	for i, id := range eventIDs {
		if i > 0 {
			recs += ","
		}
		at := base + int64(id[len(id)-1]-'0')
		recs += fmt.Sprintf(`{"timeUnixNano":"%d","body":{"stringValue":"x"},"attributes":[{"key":"oute.event.id","value":{"stringValue":%q}},{"key":"host.name","value":{"stringValue":"oute-server"}}]}`, at, id)
	}
	doc, err := ingest.DecodeJSON([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + recs + `]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// Gravar o mesmo fato de novo, no mesmo lote ou em outro, não duplica nada; cada lote é uma transação só.
func TestWriteTelemetryDedupe(t *testing.T) {
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
	rows, err := ingest.LogRows(logPayload(t, "e-1", "e-2", "e-1"), time.Now().UnixNano()+1_000_000)
	if err != nil || len(rows) != 3 {
		t.Fatalf("LogRows = %d (%v)", len(rows), err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: rows}); err != nil {
		t.Fatal(err)
	}
	count := func() (n int) {
		if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_logs WHERE company_id = $1`, e.co.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 2 {
		t.Fatalf("linhas depois do 1º lote = %d, quer 2 (e-1 veio duas vezes)", n)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: rows}); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("linhas depois do reenvio = %d, quer 2", n)
	}
	var by string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT ingested_by_key_id::text FROM telemetry_logs WHERE company_id = $1 LIMIT 1`, e.co.ID).Scan(&by); err != nil || by != key.ID.String() {
		t.Errorf("entregue por = %q (%v), quer %s", by, err, key.ID)
	}
	if err := e.svc.IngestReady(ctx); err != nil {
		t.Errorf("IngestReady: %v", err)
	}

	// Fato sem hora: a hora vira a da chegada, que muda a cada reenvio; a chave tem de bastar.
	noTime, err := ingest.DecodeJSON([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"sem hora"}}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, received := range []int64{time.Now().UnixNano(), time.Now().UnixNano() + 5_000_000_000} {
		r, err := ingest.LogRows(noTime, received)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: r}); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(); n != 3 {
		t.Fatalf("linhas depois do fato sem hora enviado duas vezes = %d, quer 3 (2 + 1)", n)
	}
}

// As partições dos meses à frente são criadas e o fato do mês cai na partição dele; criar de novo não falha.
func TestEnsureTelemetryPartitions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := time.Date(2031, time.January, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if err := e.svc.EnsureTelemetryPartitions(ctx, base); err != nil {
			t.Fatalf("EnsureTelemetryPartitions #%d: %v", i, err)
		}
	}
	for _, table := range []string{"telemetry_spans", "telemetry_logs", "telemetry_metrics"} {
		for _, m := range []string{"2031_01", "2031_02", "2031_03"} {
			var ok bool
			if err := e.svc.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table+"_"+m).Scan(&ok); err != nil || !ok {
				t.Errorf("falta a partição %s_%s", table, m)
			}
		}
	}
	at := time.Date(2031, time.February, 3, 0, 0, 0, 0, time.UTC).UnixNano()
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO telemetry_logs (company_id, dedupe_key, time_unix_nano, received_unix_nano) VALUES ($1, 'k', $2, 1)`, e.co.ID, at); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_logs_2031_02`).Scan(&n); err != nil || n != 1 {
		t.Errorf("linhas na partição de fevereiro = %d (%v), quer 1", n, err)
	}
}
