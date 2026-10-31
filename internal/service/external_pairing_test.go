package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/service"
)

const pairingRepo = "renatobardi/oute-agent"

// Corrigir o emparelhamento leva o passado junto: a conversa, a sessão, as chamadas e o calls_daily do repositório passam
// para o workspace novo; o que sobra no velho zera e some (ADR-0075 item 5).
func TestRepoPairingMovesThePast(t *testing.T) {
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
	repo := fmt.Sprintf(`,{"key":"oute.task.repo","value":{"stringValue":%q}}`, pairingRepo)
	rows, err := ingest.SpanRows(spanPayload(t, oneSpan("s1", "claude_code.llm_request", 0, repo), oneSpan("s2", "claude_code.llm_request", 1, repo)), time.Now().UnixNano()+10)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Spans: rows}); err != nil {
		t.Fatal(err)
	}

	pairings, err := e.svc.ListRepoPairings(ctx, e.co.ID, 50, 0)
	if err != nil || len(pairings) != 1 || pairings[0].RepoKey != pairingRepo {
		t.Fatalf("ListRepoPairings = %+v (%v)", pairings, err)
	}
	p := pairings[0]
	newWS, systemWS := domain.NewID(), domain.NewID()
	for id, sys := range map[domain.ID]bool{newWS: false, systemWS: true} {
		slug := "novo"
		if sys {
			slug = "sistema"
		}
		if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO workspaces (id, company_id, slug, name, path, is_system) VALUES ($1, $2, $3, $3, '', $4)`,
			id, e.co.ID, slug, sys); err != nil && !sys { // já há um no-repo de sistema por empresa: o segundo é recusado pelo índice único
			t.Fatal(err)
		}
	}

	if _, err := e.svc.UpdateRepoPairing(ctx, actor, e.co.ID, p.ID, domain.NewID()); err == nil {
		t.Error("workspace inexistente deveria ser recusado")
	}
	var sysID domain.ID
	if err := e.svc.Pool.QueryRow(ctx, `SELECT id FROM workspaces WHERE company_id = $1 AND is_system`, e.co.ID).Scan(&sysID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateRepoPairing(ctx, actor, e.co.ID, p.ID, sysID); err == nil {
		t.Error("o workspace de sistema não deveria receber o emparelhamento")
	}
	if got, err := e.svc.UpdateRepoPairing(ctx, actor, e.co.ID, p.ID, p.WorkspaceID); err != nil || got.WorkspaceID != p.WorkspaceID {
		t.Errorf("trocar para o mesmo workspace deveria ser um no-op: %+v %v", got, err)
	}

	got, err := e.svc.UpdateRepoPairing(ctx, actor, e.co.ID, p.ID, newWS)
	if err != nil || got.WorkspaceID != newWS || got.WorkspaceSlug != "novo" {
		t.Fatalf("UpdateRepoPairing = %+v (%v)", got, err)
	}
	count := func(q string, args ...any) (n int64) {
		if err := e.svc.Pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM external_conversation WHERE company_id = $1 AND workspace_id = $2`, e.co.ID, newWS); n != 1 {
		t.Errorf("conversas no workspace novo = %d, quer 1", n)
	}
	if n := count(`SELECT count(*) FROM external_session WHERE company_id = $1 AND workspace_id = $2`, e.co.ID, newWS); n != 1 {
		t.Errorf("sessões no workspace novo = %d, quer 1", n)
	}
	if n := count(`SELECT count(*) FROM model_calls WHERE company_id = $1 AND workspace_id = $2`, e.co.ID, newWS); n != 2 {
		t.Errorf("chamadas no workspace novo = %d, quer 2", n)
	}
	if n := count(`SELECT COALESCE(sum(calls), 0) FROM calls_daily WHERE company_id = $1 AND workspace_id = $2`, e.co.ID, newWS); n != 2 {
		t.Errorf("calls_daily no workspace novo = %d, quer 2", n)
	}
	if n := count(`SELECT count(*) FROM calls_daily WHERE company_id = $1 AND workspace_id = $2`, e.co.ID, p.WorkspaceID); n != 0 {
		t.Errorf("linhas do calls_daily que sobraram no workspace velho = %d, quer 0", n)
	}
	// uma chamada nova do mesmo repositório já cai no workspace novo
	more, err := ingest.SpanRows(spanPayload(t, oneSpan("s3", "claude_code.llm_request", 2, repo)), time.Now().UnixNano()+20)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Spans: more}); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT COALESCE(sum(calls), 0) FROM calls_daily WHERE company_id = $1 AND workspace_id = $2`, e.co.ID, newWS); n != 3 {
		t.Errorf("calls_daily no workspace novo depois de mais uma chamada = %d, quer 3", n)
	}
}

// O conteúdo completo da conversa é de owner ou admin, e cada leitura deixa um activity_event.
func TestConversationContentAudited(t *testing.T) {
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
	now := time.Now().UnixNano()
	doc, err := ingest.DecodeJSON([]byte(fmt.Sprintf(`{"resourceLogs":[{"resource":{"attributes":[{"key":"host.name","value":{"stringValue":"oute-server"}}]},
		"scopeLogs":[{"logRecords":[
		{"timeUnixNano":"%d","eventName":"user_prompt","attributes":[{"key":"session.id","value":{"stringValue":"conv-9"}},{"key":"prompt","value":{"stringValue":"faça X"}}]},
		{"timeUnixNano":"%d","eventName":"claude_code.user_prompt","attributes":[{"key":"session.id","value":{"stringValue":"conv-9"}},{"key":"prompt","value":{"stringValue":"agora Y"}}]},
		{"timeUnixNano":"%d","eventName":"api_request","attributes":[{"key":"session.id","value":{"stringValue":"conv-9"}}]}]}]}]}`, now, now+1, now+2)))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ingest.LogRows(doc, now+100)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: rows}); err != nil {
		t.Fatal(err)
	}
	convs, err := e.svc.ListExternalConversations(ctx, e.co.ID, service.ExternalConversationFilter{Limit: 50})
	if err != nil || len(convs) != 1 {
		t.Fatalf("conversas = %d (%v)", len(convs), err)
	}
	items, err := e.svc.GetExternalConversationContent(ctx, actor, e.co.ID, convs[0].ID)
	if err != nil || len(items) != 2 || items[0].Text != "faça X" || items[1].Text != "agora Y" || items[0].Role != "prompt" {
		t.Fatalf("conteúdo = %+v (%v)", items, err)
	}
	if _, err := e.svc.GetExternalConversationContent(ctx, actor, e.co.ID, convs[0].ID); err != nil {
		t.Fatal(err)
	}
	var reads int64
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'external_conversation.content_read'`, e.co.ID).Scan(&reads); err != nil || reads != 2 {
		t.Errorf("leituras registradas = %d (%v), quer 2 (uma por leitura)", reads, err)
	}
	if _, err := e.svc.GetExternalConversationContent(ctx, actor, e.co.ID, domain.NewID()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("conversa inexistente: %v, quer ErrNotFound", err)
	}
}
