package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Texto com segredo colado não sai do Hanko, em nenhuma feature (ADR-0079 item 6): o provedor não é chamado, o ledger
// e o evento guardam só o tipo do padrão, e o valor não aparece em lugar nenhum.
func TestDecisionRefusesSecretBeforeSendingAnything(t *testing.T) {
	e, f := decisionEnv(t)
	e.configureDecision(t, f, "vendor/small")
	bg := context.Background()
	const valor = "abc12345xyz"

	_, err := e.svc.NameSummary(bg, domain.HumanActor, e.co.ID, "usa api_key="+valor+" para o deploy")
	if err == nil || !strings.Contains(err.Error(), "secret_detected: assignment") {
		t.Fatalf("resumo com segredo = %v; quero secret_detected: assignment", err)
	}
	if n := len(f.models); n != 0 {
		t.Fatalf("o provedor recebeu %d chamadas com segredo no texto; quero nenhuma", n)
	}

	var status, errText string
	if err := e.svc.Pool.QueryRow(bg, `SELECT status, error FROM decision_invocations WHERE company_id = $1 AND feature = 'name_summary'`, e.co.ID).Scan(&status, &errText); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if status != "error" || errText != "secret_detected: assignment" {
		t.Errorf("ledger = %q, %q; quero error e só o tipo do padrão", status, errText)
	}

	var data string
	if err := e.svc.Pool.QueryRow(bg, `SELECT data::text FROM activity_events WHERE company_id = $1 AND verb = 'decision.invoked'`, e.co.ID).Scan(&data); err != nil {
		t.Fatalf("evento: %v", err)
	}
	if !strings.Contains(data, `"secret_kind": "assignment"`) {
		t.Errorf("evento sem o tipo do padrão: %s", data)
	}

	if strings.Contains(errText+data, valor) {
		t.Fatal("o valor do segredo apareceu no ledger ou no evento")
	}
}

// A triagem é a mesma regra: uma descrição com segredo para antes do provedor.
func TestTriageRefusesSecretInTaskDescription(t *testing.T) {
	e, f := decisionEnv(t)
	e.configureDecision(t, f, "vendor/small")
	bg := context.Background()
	e.persona(t, "Ana", func(in *service.HirePersonaInput) { in.Title = "Backend" })
	tv := e.task(t, service.CreateTaskInput{Title: "Deploy falha", Description: "o Bearer abcdefghijklmnopqrstuv está no log"})

	_, err := e.svc.Triage(bg, domain.HumanActor, e.co.ID, tv.Key)
	if err == nil || !strings.Contains(err.Error(), "secret_detected: bearer") {
		t.Fatalf("triagem com segredo = %v; quero secret_detected: bearer", err)
	}
	if n := len(f.models); n != 0 {
		t.Fatalf("o provedor recebeu %d chamadas com segredo na descrição; quero nenhuma", n)
	}
}
