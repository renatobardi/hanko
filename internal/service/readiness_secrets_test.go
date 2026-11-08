package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O readiness confere os segredos do adapter_config (passo "secrets"): ${secret:} resolve e ${secretfile:} existe e está ao
// alcance da persona; a falha nomeia a referência, nunca o valor, e barra o despacho antes da run.
func TestReadinessChecksAdapterSecrets(t *testing.T) {
	e := newSecEnv(t)
	ctx := context.Background()
	e.svc.ReadinessRunner = scriptRunner("bin=/usr/bin/claude\nversion=2.0\nauth=login\n", nil, nil)
	e.create(t, "kube", sentinelV1)
	if _, err := e.svc.CreateSecretField(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "kube",
		service.CreateSecretFieldInput{Name: "arquivo", Kind: domain.SecretFieldFile, File: []byte(sentinelV1), FileName: "config"}); err != nil {
		t.Fatal(err)
	}
	e.create(t, "token", sentinelV1)
	cfg := json.RawMessage(`{"env":{"KUBECONFIG":"${secretfile:kube#arquivo}","API_TOKEN":"${secret:token}"}}`)
	p := e.persona(t, "Ana", func(in *service.HirePersonaInput) { in.Agent = "claude"; in.Model = "sonnet"; in.AdapterConfig = cfg })

	rd, err := e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, p.ID)
	if err != nil || rd.Status != domain.ReadinessReady {
		t.Fatalf("com os segredos no lugar a persona está pronta: %+v %v", rd, err)
	}
	if s := rd.Steps[0]; s.Name != domain.StepSecrets || s.Status != domain.StepOK ||
		!strings.Contains(s.Detail, "${secretfile:kube#arquivo}") || !strings.Contains(s.Detail, "${secret:token}") || strings.Contains(s.Detail, sentinelV1) {
		t.Fatalf("passo secrets lista as referências, nunca o valor: %+v", s)
	}

	// o item do arquivo some (apagado à força): o passo secrets falha e o despacho é barrado com o motivo
	if err := e.svc.DeleteSecret(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "kube", true); err != nil {
		t.Fatal(err)
	}
	rd, err = e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, p.ID)
	if err != nil || rd.Status != domain.ReadinessNotReady || !strings.HasPrefix(rd.Reason, "secrets: ") ||
		!strings.Contains(rd.Reason, "${secretfile:kube#arquivo}") || !strings.Contains(rd.Reason, "não existe") {
		t.Fatalf("secretfile inexistente deve reprovar o passo secrets: %+v %v", rd, err)
	}
	for _, s := range rd.Steps[1:] {
		if s.Status != domain.StepSkip {
			t.Errorf("sem os segredos os demais passos ficam skip: %+v", s)
		}
	}
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	if _, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign}); err == nil || !strings.Contains(err.Error(), "secrets:") {
		t.Fatalf("despacho bloqueado pelo segredo: %v", err)
	}

	// ${secret:} de um item fora das coleções da persona também reprova, nomeando a persona e o item
	personaSemColecao(t, e, "restrito", "infra")
	beto := e.persona(t, "Beto", func(in *service.HirePersonaInput) {
		in.Agent = "claude"
		in.Model = "sonnet"
		in.AdapterConfig = json.RawMessage(`{"api_key_ref":"${secret:restrito}"}`)
	})
	rd, err = e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, beto.ID)
	if err != nil || rd.Status != domain.ReadinessNotReady || !strings.Contains(rd.Reason, beto.Slug) || !strings.Contains(rd.Reason, "restrito") || strings.Contains(rd.Reason, sentinelV1) {
		t.Fatalf("item fora do alcance: %+v %v", rd, err)
	}
	grantPersona(t, e, "infra", domain.CollectionSubjectPersona, beto.Slug)
	if rd, err := e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, beto.ID); err != nil || rd.Status != domain.ReadinessReady {
		t.Fatalf("com a concessão a persona fica pronta: %+v %v", rd, err)
	}
}

// Um modelo que é só a família de preço (claude-haiku) reprova o passo model antes de qualquer run, e não sai no catálogo.
func TestReadinessRejectsFamilyModel(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	e.svc.ReadinessRunner = scriptRunner("bin=/usr/bin/claude\nversion=2.0\nauth=login\n", nil, nil)
	p := e.persona(t, "Ana", func(in *service.HirePersonaInput) { in.Agent = "claude"; in.Model = "claude-haiku" })
	rd, err := e.svc.TestPersonaReadiness(ctx, domain.HumanActor, e.co.ID, p.ID)
	if err != nil || rd.Status != domain.ReadinessNotReady || !strings.HasPrefix(rd.Reason, "model: claude-haiku é a família") || !strings.Contains(rd.Reason, `"haiku"`) {
		t.Fatalf("família de preço não é modelo: %+v %v", rd, err)
	}
	cat, err := e.svc.ModelCatalog(ctx, e.co.ID, "claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range cat {
		if m.Model == "claude-haiku" || m.Model == "claude-sonnet" || m.Model == "claude-opus" {
			t.Fatalf("o catálogo não oferece a família como modelo: %+v", m)
		}
	}
}
