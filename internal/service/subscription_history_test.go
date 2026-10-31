package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cada assinatura nasce com a linha do histórico de mensalidade; trocar o valor acrescenta uma linha e o valor vigente
// segue a regra do maior início menor ou igual a hoje, com a registrada por último desempatando (ADR-0082 item 6).
func TestSubscriptionMonthlyHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub := e.sub(t, func(in *service.CreateSubscriptionInput) { in.Name = "Claude Max"; in.MonthlyUSD = 200 })

	hist, err := e.svc.ListSubscriptionMonthlyHistory(ctx, e.co.ID, sub.ID.String())
	if err != nil || len(hist) != 1 || hist[0].MonthlyUSD != 200 || hist[0].Origin != "created" {
		t.Fatalf("histórico ao nascer = %+v (%v)", hist, err)
	}
	current := func() float64 {
		var v float64
		if err := e.svc.Pool.QueryRow(ctx, `SELECT monthly_usd::float8 FROM subscriptions WHERE id = $1`, sub.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	today := time.Now().UTC()
	// um início no passado, antes do nascimento: não muda o valor de hoje
	if _, err := e.svc.AddSubscriptionMonthlyValue(ctx, domain.HumanActor, e.co.ID, "Claude Max", 150, today.AddDate(-1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if current() != 200 {
		t.Errorf("valor vigente = %v, quer 200 (a linha nova é anterior à de nascimento)", current())
	}
	// um início hoje: passa a valer
	v, err := e.svc.AddSubscriptionMonthlyValue(ctx, domain.HumanActor, e.co.ID, sub.ID.String(), 250, today)
	if err != nil || v.MonthlyUSD != 250 || v.Origin != "manual" {
		t.Fatalf("AddSubscriptionMonthlyValue = %+v (%v)", v, err)
	}
	if current() != 250 {
		t.Errorf("valor vigente = %v, quer 250", current())
	}
	// no mesmo dia, a registrada por último desempata
	if _, err := e.svc.AddSubscriptionMonthlyValue(ctx, domain.HumanActor, e.co.ID, sub.ID.String(), 260, today); err != nil {
		t.Fatal(err)
	}
	if current() != 260 {
		t.Errorf("valor vigente = %v, quer 260 (a última do dia)", current())
	}
	if _, err := e.svc.AddSubscriptionMonthlyValue(ctx, domain.HumanActor, e.co.ID, sub.ID.String(), 300, today.AddDate(0, 0, 2)); err == nil {
		t.Error("início no futuro deveria ser recusado")
	}
	if _, err := e.svc.AddSubscriptionMonthlyValue(ctx, domain.HumanActor, e.co.ID, sub.ID.String(), -1, today); err == nil {
		t.Error("mensalidade negativa deveria ser recusada")
	}
	if _, err := e.svc.AddSubscriptionMonthlyValue(ctx, domain.HumanActor, e.co.ID, "não existe", 10, today); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("assinatura inexistente: %v, quer ErrNotFound", err)
	}

	// trocar a mensalidade pelo cadastro também acrescenta uma linha
	nv := 310.0
	if _, err := e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, sub.ID, service.UpdateSubscriptionInput{MonthlyUSD: &nv}); err != nil {
		t.Fatal(err)
	}
	if hist, _ := e.svc.ListSubscriptionMonthlyHistory(ctx, e.co.ID, sub.ID.String()); len(hist) != 5 || hist[0].MonthlyUSD != 310 {
		t.Errorf("histórico depois da troca pelo cadastro = %d linhas (a mais nova %+v), quer 5 e a mais nova com 310", len(hist), hist)
	}
	// o evento da troca fica registrado
	var events int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'subscription.monthly_value_added'`, e.co.ID).Scan(&events); err != nil || events != 4 {
		t.Errorf("eventos da troca = %d (%v), quer 4", events, err)
	}
}

// A chave externa da assinatura é opcional, de um conjunto fechado, e única por empresa (ADR-0082 item 4).
func TestSubscriptionExternalKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.sub(t, func(in *service.CreateSubscriptionInput) { in.Name = "Claude"; in.ExternalKey = "claude" })
	if a.ExternalKey == nil || *a.ExternalKey != "claude" {
		t.Fatalf("chave externa ao criar = %v", a.ExternalKey)
	}
	if _, err := e.svc.CreateSubscription(ctx, domain.HumanActor, e.co.ID, service.CreateSubscriptionInput{Name: "Outro", Agent: "fake", MonthlyUSD: 1, ExternalKey: "claude"}); err == nil {
		t.Error("a mesma chave externa em outra assinatura da empresa deveria ser recusada")
	}
	if _, err := e.svc.CreateSubscription(ctx, domain.HumanActor, e.co.ID, service.CreateSubscriptionInput{Name: "Ruim", Agent: "fake", MonthlyUSD: 1, ExternalKey: "gpt"}); err == nil {
		t.Error("chave externa fora do conjunto deveria ser recusada")
	}
	b := e.sub(t, func(in *service.CreateSubscriptionInput) { in.Name = "Zai" })
	zai := "zai"
	got, err := e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdateSubscriptionInput{ExternalKey: &zai})
	if err != nil || got.ExternalKey == nil || *got.ExternalKey != "zai" {
		t.Fatalf("trocar a chave externa = %+v (%v)", got.ExternalKey, err)
	}
	clash := "claude"
	if _, err := e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdateSubscriptionInput{ExternalKey: &clash}); err == nil {
		t.Error("trocar para uma chave que outra assinatura usa deveria ser recusado")
	}
	cleared, err := e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdateSubscriptionInput{ClearExternalKey: true})
	if err != nil || cleared.ExternalKey != nil {
		t.Errorf("tirar a chave externa = %+v (%v)", cleared.ExternalKey, err)
	}
	if _, err := e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdateSubscriptionInput{ExternalKey: &zai, ClearExternalKey: true}); err == nil {
		t.Error("pôr e tirar a chave de uma vez deveria ser recusado")
	}
}
