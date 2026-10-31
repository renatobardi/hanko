package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/service"
)

// A leitura da fonte externa: sessões, conversas, troca manual de classe e uso (com e sem o filtro de rodada).
func TestExternalReads(t *testing.T) {
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
	cost := `,{"key":"oute.cost_usd","value":{"doubleValue":0.25}}`
	rows, err := ingest.SpanRows(spanPayload(t, oneSpan("s1", "claude_code.llm_request", 0, cost), oneSpan("s2", "claude_code.llm_request", 1, "")), time.Now().UnixNano()+10)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Spans: rows}); err != nil {
		t.Fatal(err)
	}

	convs, err := e.svc.ListExternalConversations(ctx, e.co.ID, service.ExternalConversationFilter{Limit: 50})
	if err != nil || len(convs) != 1 || convs[0].Calls != 2 || convs[0].SessionID != "conv-1" {
		t.Fatalf("ListExternalConversations = %+v (%v)", convs, err)
	}
	conv := convs[0]
	if got, err := e.svc.ListExternalConversations(ctx, e.co.ID, service.ExternalConversationFilter{Class: "nenhuma", Limit: 50}); err != nil || len(got) != 0 {
		t.Errorf("filtro por classe sem resultado = %d (%v)", len(got), err)
	}
	if got, err := e.svc.ListExternalConversations(ctx, e.co.ID, service.ExternalConversationFilter{Host: conv.HostID, ByCost: true, Limit: 50}); err != nil || len(got) != 1 {
		t.Errorf("filtro por host = %d (%v)", len(got), err)
	}
	if g, err := e.svc.GetExternalConversation(ctx, e.co.ID, conv.ID); err != nil || g.Name != conv.Name {
		t.Errorf("GetExternalConversation = %+v (%v)", g, err)
	}
	if _, err := e.svc.GetExternalConversation(ctx, e.co.ID, domain.NewID()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("conversa inexistente: %v, quer ErrNotFound", err)
	}

	sessions, err := e.svc.ListExternalSessions(ctx, e.co.ID, nil, nil, 50, 0)
	if err != nil || len(sessions) != 1 || sessions[0].Conversations != 1 || sessions[0].TaskID != "task-1" {
		t.Fatalf("ListExternalSessions = %+v (%v)", sessions, err)
	}
	if _, err := e.svc.GetExternalSession(ctx, e.co.ID, sessions[0].ID); err != nil {
		t.Errorf("GetExternalSession: %v", err)
	}

	updated, err := e.svc.ClassifyExternalConversation(ctx, actor, e.co.ID, conv.ID, " qa ")
	if err != nil || updated.Class != "qa" || updated.ClassOrigin != "manual" || updated.ClassConfidence != "high" {
		t.Fatalf("ClassifyExternalConversation = %+v (%v)", updated, err)
	}
	if _, err := e.svc.ClassifyExternalConversation(ctx, actor, e.co.ID, conv.ID, ""); err == nil {
		t.Error("classe vazia deveria ser recusada")
	}
	inputs, err := e.svc.ListConversationClassInputs(ctx, e.co.ID, conv.ID)
	if err != nil || len(inputs) != 1 || inputs[0].Kind != "manual" || inputs[0].Class != "qa" || inputs[0].ActorID == nil {
		t.Errorf("entradas da classificação = %+v (%v)", inputs, err)
	}

	usage, err := e.svc.ExternalUsage(ctx, e.co.ID, time.Time{}, time.Time{}, []string{"model"}, nil)
	if err != nil || len(usage) != 1 || usage[0].Calls != 2 || usage[0].Model != "claude-opus-5-5" || usage[0].CostRealUSD != 0.25 || usage[0].UnpricedCalls != 1 {
		t.Fatalf("ExternalUsage por modelo = %+v (%v)", usage, err)
	}
	if total, err := e.svc.ExternalUsage(ctx, e.co.ID, time.Time{}, time.Time{}, nil, nil); err != nil || len(total) != 1 || total[0].Calls != 2 {
		t.Errorf("ExternalUsage total = %+v (%v)", total, err)
	}
	if _, err := e.svc.ExternalUsage(ctx, e.co.ID, time.Time{}, time.Time{}, []string{"cor"}, nil); err == nil {
		t.Error("group_by desconhecido deveria ser recusado")
	}

	// o filtro de rodada soma só as chamadas das conversas das sessões da rodada
	roundID := domain.NewID()
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO external_round (id, company_id, round_key, name) VALUES ($1, $2, 'swarm-1', 'Brave_Otter')`, roundID, e.co.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.ExternalUsage(ctx, e.co.ID, time.Time{}, time.Time{}, nil, &roundID); err != nil || len(got) != 1 || got[0].Calls != 0 {
		t.Errorf("rodada sem sessão ligada = %+v (%v), quer 0 chamadas", got, err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE external_session SET round = 'swarm-1' WHERE company_id = $1`, e.co.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.ExternalUsage(ctx, e.co.ID, time.Time{}, time.Time{}, []string{"agent"}, &roundID); err != nil || len(got) != 1 || got[0].Calls != 2 || got[0].UnpricedCalls != 1 {
		t.Errorf("rodada com a sessão ligada = %+v (%v), quer 2 chamadas", got, err)
	}
}
