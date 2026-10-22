package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Uma geração pelo decision model que não terminou (o processo caiu no meio da chamada) vira erro visível em vez de
// segurar o card "atualizando" para sempre; o último resumo bom continua à vista.
func TestDecisionCardStaleGenerationBecomesAnError(t *testing.T) {
	d := newDecCardEnv(t)
	ctx := context.Background()
	v := d.create(t, nil)
	if _, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false); err != nil {
		t.Fatal(err)
	}
	// simula a geração interrompida: marca de geração antiga e uma rodada ainda "running"
	stale := d.now.Add(-time.Hour)
	if _, err := d.svc.Pool.Exec(ctx, `UPDATE status_cards SET generating_since = $2, next_eval_at = $3 WHERE id = $1`, v.ID, stale, d.now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.svc.Pool.Exec(ctx, `INSERT INTO status_card_updates (id, company_id, card_id, kind, trigger, started_at)
		VALUES ($1, $2, $3, 'full', 'manual', $4)`, domain.NewID(), d.co.ID, v.ID, stale); err != nil {
		t.Fatal(err)
	}
	if got := d.card(t, v.ID); got.ViewState != domain.CardViewUpdating {
		t.Fatalf("antes do tick o card parece atualizando: %s", got.ViewState)
	}
	// um pedido explícito durante a geração é recusado só enquanto ela é recente; esta já passou do prazo
	if n, err := d.svc.EvaluateDueStatusCards(ctx); err != nil || n != 1 {
		t.Fatalf("tick: n=%d err=%v", n, err)
	}
	got := d.card(t, v.ID)
	if got.State != domain.CardError || got.FailureReason == "" || got.SummaryRevision != 1 || got.Summary == "" {
		t.Fatalf("depois do tick: %+v", got.StatusCard)
	}
	ups, _ := d.svc.ListStatusCardUpdates(ctx, d.co.ID, v.ID, 10)
	failed := 0
	for _, u := range ups {
		if u.Status == domain.CardUpdateFailed && u.Error != "" {
			failed++
		}
		if u.Status == domain.CardUpdateRunning {
			t.Fatalf("nenhuma rodada devia seguir running: %+v", u)
		}
	}
	if failed != 1 {
		t.Fatalf("rodadas falhas = %d", failed)
	}
	// e o pedido explícito recupera
	d.now = d.now.Add(time.Hour)
	if got, err := d.svc.RefreshStatusCard(ctx, domain.HumanActor, d.co.ID, v.ID, false); err != nil || got.State != domain.CardActive || got.SummaryRevision != 2 {
		t.Fatalf("recuperação: %v %+v", err, got.StatusCard)
	}
}
