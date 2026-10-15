package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// A poda do histórico só apaga entregas encerradas: pending/retrying ainda têm job na fila.
func TestPruneKeepsPendingDeliveries(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	h := e.webhook(t, service.CreateIntegrationInput{Name: "Tasks", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated}})

	// 120 pendentes/em retry e 30 encerradas (as mais antigas, fora da janela das 100 últimas)
	for _, c := range []struct {
		status string
		n      int
		age    int
	}{{"pending", 60, 1000}, {"retrying", 60, 2000}, {"success", 15, 5000}, {"failed", 15, 6000}} {
		if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO webhook_deliveries (id, company_id, integration_id, event, payload, status, created_at)
			SELECT gen_random_uuid(), $1, $2, 'task.created', '{}', $3, now() - make_interval(secs => $5 + g) FROM generate_series(1, $4) g`,
			e.co.ID, h.ID, c.status, c.n, c.age); err != nil {
			t.Fatal(err)
		}
	}
	e.task(t, service.CreateTaskInput{Title: "Dispara a poda"})
	ids := e.hooks.queued()
	if _, err := e.svc.DeliverWebhook(ctx, ids[len(ids)-1], 1, service.WebhookMaxAttempts); err != nil {
		t.Fatal(err)
	}

	count := func(status string) int {
		var n int
		if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_deliveries WHERE integration_id=$1 AND status=$2`, h.ID, status).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if p, r := count("pending"), count("retrying"); p != 60 || r != 60 {
		t.Fatalf("a poda apagou entregas ainda na fila: pending=%d retrying=%d", p, r)
	}
	if s, f := count("success"), count("failed"); s > 1 || f != 0 {
		t.Fatalf("encerradas fora da janela deviam ser podadas: success=%d failed=%d", s, f)
	}
}

// Se a linha da entrega sumiu, o job não pode fingir que entregou.
func TestDeliverWebhookMissingRowIsAnError(t *testing.T) {
	e, _ := webhookEnv(t)
	out, err := e.svc.DeliverWebhook(context.Background(), uuid.New(), 1, service.WebhookMaxAttempts)
	if err == nil || out != service.WebhookGiveUp {
		t.Fatalf("out=%v err=%v", out, err)
	}
}
