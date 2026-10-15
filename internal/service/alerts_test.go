package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func (e *env) openAlerts(t *testing.T, kind domain.AlertKind) []domain.Alert {
	t.Helper()
	all, err := e.svc.ListOpenAlerts(context.Background(), e.co.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []domain.Alert
	for _, a := range all {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func (e *env) evalAt(t *testing.T, at time.Time) service.AlertEval {
	t.Helper()
	e.svc.Now = func() time.Time { return at }
	ev, err := e.svc.EvaluateAlerts(context.Background())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return ev
}

func (e *env) alertActivity(t *testing.T, verb string) int {
	t.Helper()
	acts, err := e.svc.ListActivity(context.Background(), e.co.ID, service.ActivityFilter{SubjectType: "alert", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range acts {
		if a.Verb == verb {
			n++
		}
	}
	return n
}

// Persona com trabalho esperando e sem ser acordada além de N× o intervalo: abre um alerta (um só,
// mesmo avaliando de novo) e fecha sozinho quando a condição some.
func TestAlertPersonaSilent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.Alerts = service.AlertConfig{SilentFactor: 3}
	t0 := time.Now().UTC()
	e.svc.Now = func() time.Time { return t0 }
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.HeartbeatIntervalSec = 60 })
	tv := e.task(t, service.CreateTaskInput{PersonaID: &p.ID})
	// outra persona, sem intervalo de heartbeat: nunca é silenciosa
	q := e.persona(t, "Manual", nil)
	e.task(t, service.CreateTaskInput{PersonaID: &q.ID})
	// persona com intervalo e nenhuma task: não há o que acordar
	e.persona(t, "Ociosa", func(in *service.HirePersonaInput) { in.HeartbeatIntervalSec = 60 })

	if ev := e.evalAt(t, t0.Add(2*time.Minute+30*time.Second)); ev.Opened != 0 || ev.Open != 0 {
		t.Fatalf("dentro do limite (3× 60 s): %+v", ev)
	}
	ev := e.evalAt(t, t0.Add(4*time.Minute))
	if ev.Opened != 1 || ev.Open != 1 {
		t.Fatalf("além do limite: %+v", ev)
	}
	got := e.openAlerts(t, domain.AlertPersonaSilent)
	if len(got) != 1 || got[0].SubjectID != p.ID || got[0].SubjectType != "persona" || got[0].Severity != domain.AlertWarning {
		t.Fatalf("alertas: %+v", got)
	}
	// idempotente: avaliar de novo não duplica nem gera novo fato
	if ev := e.evalAt(t, t0.Add(5*time.Minute)); ev.Opened != 0 || ev.Closed != 0 || ev.Open != 1 {
		t.Fatalf("reavaliação: %+v", ev)
	}
	if n := e.alertActivity(t, "alert.opened"); n != 1 {
		t.Fatalf("alert.opened = %d", n)
	}
	// aparece na Caixa de entrada
	in, err := e.svc.Inbox(ctx, e.co.ID)
	if err != nil || len(in.Alerts) != 1 || in.Count() != 1 {
		t.Fatalf("inbox: err=%v alerts=%d count=%d", err, len(in.Alerts), in.Count())
	}
	// a persona voltou a ser acordada (run aberta na task): a condição some e o alerta fecha
	e.svc.Now = func() time.Time { return t0.Add(6 * time.Minute) }
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat)); err != nil {
		t.Fatal(err)
	}
	if ev := e.evalAt(t, t0.Add(6*time.Minute)); ev.Closed != 1 || ev.Open != 0 {
		t.Fatalf("fechamento: %+v", ev)
	}
	if len(e.openAlerts(t, domain.AlertPersonaSilent)) != 0 || e.alertActivity(t, "alert.closed") != 1 {
		t.Fatal("alerta deveria estar fechado, com um alert.closed")
	}
	if in, _ := e.svc.Inbox(ctx, e.co.ID); len(in.Alerts) != 0 {
		t.Fatalf("inbox ainda mostra alerta: %+v", in.Alerts)
	}
}

// A persona pausada ou desligada não deveria ser acordada: sem alerta.
func TestAlertPersonaSilentIgnoraPausada(t *testing.T) {
	e := newEnv(t)
	t0 := time.Now().UTC()
	e.svc.Now = func() time.Time { return t0 }
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.HeartbeatIntervalSec = 60 })
	e.task(t, service.CreateTaskInput{PersonaID: &p.ID})
	paused := domain.PersonaPaused
	if _, err := e.svc.UpdatePersona(context.Background(), domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: &paused}); err != nil {
		t.Fatal(err)
	}
	if ev := e.evalAt(t, t0.Add(time.Hour)); ev.Open != 0 {
		t.Fatalf("persona pausada: %+v", ev)
	}
}

// Run em running sem evento além do limite configurável vira alerta crítico; um evento novo fecha.
func TestAlertRunStuck(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.Alerts = service.AlertConfig{RunStuckAfter: 10 * time.Minute}
	t0 := time.Now().UTC()
	e.svc.Now = func() time.Time { return t0 }
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{PersonaID: &p.ID})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	// queued não conta: ainda não começou
	if ev := e.evalAt(t, t0.Add(time.Hour)); ev.Open != 0 {
		t.Fatalf("run queued: %+v", ev)
	}
	e.svc.Now = func() time.Time { return t0 }
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	if ev := e.evalAt(t, t0.Add(9*time.Minute)); ev.Open != 0 {
		t.Fatalf("dentro do limite: %+v", ev)
	}
	if ev := e.evalAt(t, t0.Add(11*time.Minute)); ev.Opened != 1 {
		t.Fatalf("além do limite: %+v", ev)
	}
	got := e.openAlerts(t, domain.AlertRunStuck)
	if len(got) != 1 || got[0].SubjectID != run.ID || got[0].SubjectType != "run" || got[0].Severity != domain.AlertCritical {
		t.Fatalf("alertas: %+v", got)
	}
	// um evento novo mostra que a run está viva: fecha
	e.svc.Now = func() time.Time { return t0.Add(12 * time.Minute) }
	if _, err := e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: domain.EventAssistant, Text: "ainda aqui"}); err != nil {
		t.Fatal(err)
	}
	if ev := e.evalAt(t, t0.Add(13*time.Minute)); ev.Closed != 1 || ev.Open != 0 {
		t.Fatalf("após evento: %+v", ev)
	}
	// parou de novo: reabre como alerta novo
	if ev := e.evalAt(t, t0.Add(30*time.Minute)); ev.Opened != 1 {
		t.Fatalf("reabertura: %+v", ev)
	}
	// run terminada: fecha
	e.svc.Now = func() time.Time { return t0.Add(31 * time.Minute) }
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone}); err != nil {
		t.Fatal(err)
	}
	if ev := e.evalAt(t, t0.Add(32*time.Minute)); ev.Open != 0 || ev.Closed != 1 {
		t.Fatalf("run terminada: %+v", ev)
	}
	if n := e.alertActivity(t, "alert.opened"); n != 2 {
		t.Fatalf("alert.opened = %d", n)
	}
}

// Orçamento em 80% abre alerta (warning); em 100% sobe para critical sem duplicar; subir o teto fecha.
func TestAlertBudget(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return at }
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(100000))); err != nil {
		t.Fatal(err)
	}
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(3000)) })
	setPersonaBudget := func(cents int64) {
		t.Helper()
		if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{BudgetMonthlyCents: &cents}); err != nil {
			t.Fatal(err)
		}
	}

	e.spend(t, p, at, nil, usd(7))
	if ev := e.evalAt(t, at); ev.Open != 0 {
		t.Fatalf("abaixo de 80%% (persona 23%%): %+v", ev)
	}
	e.spend(t, p, at, nil, usd(17.5)) // persona 81,7%
	if ev := e.evalAt(t, at); ev.Opened != 1 {
		t.Fatalf("persona em 82%%: %+v", ev)
	}
	got := e.openAlerts(t, domain.AlertBudgetNearLimit)
	if len(got) != 1 || got[0].SubjectID != p.ID || got[0].SubjectType != "persona" || got[0].Severity != domain.AlertWarning {
		t.Fatalf("alertas: %+v", got)
	}
	if ev := e.evalAt(t, at); ev.Opened != 0 || ev.Closed != 0 || ev.Open != 1 {
		t.Fatalf("idempotência: %+v", ev)
	}
	setPersonaBudget(2000) // 122%
	if ev := e.evalAt(t, at); ev.Opened != 0 || ev.Open != 1 {
		t.Fatalf("subiu de nível: %+v", ev)
	}
	if got = e.openAlerts(t, domain.AlertBudgetNearLimit); len(got) != 1 || got[0].Severity != domain.AlertCritical {
		t.Fatalf("deveria virar critical: %+v", got)
	}
	// o teto da empresa também alerta
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(3000))); err != nil {
		t.Fatal(err)
	}
	if ev := e.evalAt(t, at); ev.Opened != 1 || ev.Open != 2 {
		t.Fatalf("empresa em 82%%: %+v", ev)
	}
	// subir os tetos fecha os dois sozinho
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(100000))); err != nil {
		t.Fatal(err)
	}
	setPersonaBudget(100000)
	if ev := e.evalAt(t, at); ev.Closed != 2 || ev.Open != 0 {
		t.Fatalf("teto novo: %+v", ev)
	}
	// volta ao teto baixo e vira o mês: o gasto zera e o alerta fecha
	setPersonaBudget(3000)
	if ev := e.evalAt(t, at); ev.Opened != 1 {
		t.Fatalf("reabertura: %+v", ev)
	}
	if ev := e.evalAt(t, at.AddDate(0, 1, 0)); ev.Open != 0 || ev.Closed != 1 {
		t.Fatalf("mês novo: %+v", ev)
	}
}

func TestAlertCounts(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	counts, err := e.svc.AlertCounts(ctx)
	if err != nil || len(counts) != len(domain.AlertKinds) {
		t.Fatalf("sem alertas todos os tipos aparecem com zero: %v %v", counts, err)
	}
	e.svc.Alerts = service.AlertConfig{RunStuckAfter: time.Minute}
	t0 := time.Now().UTC()
	e.svc.Now = func() time.Time { return t0 }
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{PersonaID: &p.ID})
	run, _ := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	e.evalAt(t, t0.Add(5*time.Minute))
	counts, err = e.svc.AlertCounts(ctx)
	if err != nil || counts[domain.AlertRunStuck] != 1 || counts[domain.AlertPersonaSilent] != 0 {
		t.Fatalf("contagem: %v %v", counts, err)
	}
}
