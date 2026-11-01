package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
)

// Leitura da rodada e do ciclo, texto da etapa e marca da ação (ADR-0080 itens 3, 4 e 7).
func TestExternalRoundReadsAndMarks(t *testing.T) {
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
	base := time.Now().UnixNano()
	step := func(id string, at int64, kind, rev, review, body string) string {
		return swarmLogBody(id, "oute.swarm.step.published", body, at, "oute.swarm.step.kind", kind, "oute.swarm.step.rev", rev,
			"oute.swarm.step.sha256", stepSHA, "oute.swarm.step.review", review, "oute.swarm.step.refcheck", "ok", "oute.swarm.cycle", "renatobardi/hanko#126")
	}
	recs := []string{
		swarmLog("r-opened", "oute.swarm.round.opened", base+1, "oute.swarm.repo", "renatobardi/hanko"),
		swarmLog("r-asked", "oute.swarm.round.asked", base+3),
		step("s-triage", base+10, "triagem", "1", "aprovado", "## Ações\n- a1: rodar o deploy\n- a2: ver `pedido:20261031-101500-deploy`\n"),
		step("s-kaizen", base+20, "kaizen", "1", "reprovado", "## Ações\n- k1: não deve aparecer\n"),
		step("s-cycle", base+30, "ciclo", "1", "aprovado", "resumo"),
	}
	doc, err := ingest.DecodeJSON([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + strings.Join(recs, ",") + `]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ingest.LogRows(doc, time.Now().UnixNano()+1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: rows}); err != nil {
		t.Fatal(err)
	}

	list, err := e.svc.ListExternalRounds(ctx, e.co.ID, nil, 50, 0)
	if err != nil || len(list) != 1 || list[0].LastEventAt == nil || list[0].LastAskedAt == nil {
		t.Fatalf("ListExternalRounds = %+v (%v), quer 1 rodada com último evento e última pergunta", list, err)
	}
	d, err := e.svc.GetExternalRound(ctx, e.co.ID, list[0].ID)
	if err != nil || len(d.Steps) != 2 {
		t.Fatalf("GetExternalRound = %d etapas (%v), quer 2 (triagem e kaizen)", len(d.Steps), err)
	}
	triage, kaizen := d.Steps[0], d.Steps[1]
	if triage.Kind != "triage" || len(triage.Actions) != 2 || triage.Actions[1].ApprovalKey != "20261031-101500-deploy" {
		t.Errorf("triagem = %+v, quer 2 ações e a segunda com o pedido", triage)
	}
	if kaizen.Kind != "kaizen" || len(kaizen.Actions) != 0 {
		t.Errorf("a etapa reprovada sai sem ações: %+v", kaizen)
	}

	// marca: a mais nova vence; a ação com pedido e a que não existe são recusadas
	if _, err := e.svc.MarkRoundAction(ctx, actor, e.co.ID, list[0].ID, triage.ID, "a1", "done"); err != nil {
		t.Fatal(err)
	}
	if d, _ = e.svc.GetExternalRound(ctx, e.co.ID, list[0].ID); d.Steps[0].Actions[0].Mark == nil || d.Steps[0].Actions[0].Mark.State != "done" {
		t.Errorf("a marca feita não apareceu: %+v", d.Steps[0].Actions[0])
	}
	if _, err := e.svc.MarkRoundAction(ctx, actor, e.co.ID, list[0].ID, triage.ID, "a1", "pending"); err != nil {
		t.Fatal(err)
	}
	if d, _ = e.svc.GetExternalRound(ctx, e.co.ID, list[0].ID); d.Steps[0].Actions[0].Mark.State != "pending" {
		t.Errorf("a marca mais nova devia vencer: %+v", d.Steps[0].Actions[0].Mark)
	}
	if _, err := e.svc.MarkRoundAction(ctx, actor, e.co.ID, list[0].ID, triage.ID, "a2", "done"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("ação com pedido do canal: %v, quer ErrInvalid", err)
	}
	if _, err := e.svc.MarkRoundAction(ctx, actor, e.co.ID, list[0].ID, triage.ID, "zz", "done"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ação que não existe: %v, quer ErrNotFound", err)
	}
	if _, err := e.svc.MarkRoundAction(ctx, actor, e.co.ID, list[0].ID, triage.ID, "a1", "talvez"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("estado inválido: %v, quer ErrInvalid", err)
	}

	// texto: sai do fato, só da etapa aprovada, e cada leitura gera activity_event
	text, err := e.svc.RoundStepContent(ctx, actor, e.co.ID, list[0].ID, triage.ID)
	if err != nil || !strings.Contains(text, "a1: rodar o deploy") {
		t.Errorf("texto da etapa = %q (%v)", text, err)
	}
	if _, err := e.svc.RoundStepContent(ctx, actor, e.co.ID, list[0].ID, kaizen.ID); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("texto da etapa reprovada: %v, quer ErrNotAllowed", err)
	}
	var reads int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'round_step.content_read'`, e.co.ID).Scan(&reads); err != nil || reads != 1 {
		t.Errorf("leituras do texto no activity_event = %d (%v), quer 1", reads, err)
	}

	// ciclo: o resumo no topo e a rodada
	if list[0].CycleID == nil {
		t.Fatal("a rodada devia ter o ciclo da triagem")
	}
	c, err := e.svc.GetExternalCycle(ctx, e.co.ID, *list[0].CycleID)
	if err != nil || c.Summary == nil || c.Summary.Kind != "cycle_summary" || len(c.Rounds) != 1 || c.IssueRef != "renatobardi/hanko#126" {
		t.Errorf("GetExternalCycle = %+v (%v)", c, err)
	}
	if _, err := e.svc.GetExternalRound(ctx, e.co.ID, domain.NewID()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("rodada inexistente: %v, quer ErrNotFound", err)
	}
}
