package service_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/teams"
)

// Assistente de primeiros passos (ADR-0061, #67): retomável porque os passos são derivados do que a empresa tem.

func TestOnboardingStepsAreDerivedAndResumable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	st, err := e.svc.OnboardingStatus(ctx, e.co.ID)
	if err != nil || st.Current != domain.StepHarness || st.Done() != 1 || !st.Active() {
		t.Fatalf("começo: %+v %v", st, err)
	}

	// probe que falha: o passo continua aberto e o motivo fica guardado
	e.svc.ReadinessRunner = scriptRunner("bin=/usr/bin/claude\nversion=2.0\nauth=none\n", nil, nil)
	res, err := e.svc.OnboardingProbe(ctx, domain.HumanActor, e.co.ID, "claude", "")
	if err != nil || res.Status != domain.ReadinessNotReady || res.Reason == "" || len(res.Steps) != 4 {
		t.Fatalf("probe sem login: %+v %v", res, err)
	}
	if st, _ = e.svc.OnboardingStatus(ctx, e.co.ID); st.Current != domain.StepHarness || st.HarnessStatus != "not_ready" || st.HarnessAgent != "claude" {
		t.Fatalf("depois do probe ruim: %+v", st)
	}
	last, err := e.svc.OnboardingLastProbe(ctx, e.co.ID)
	if err != nil || last == nil || last.Status != domain.ReadinessNotReady || len(last.Steps) != 4 {
		t.Fatalf("último probe: %+v %v", last, err)
	}

	// conserta o login: o passo fecha e o assistente segue para a persona
	e.svc.ReadinessRunner = scriptRunner("bin=/usr/bin/claude\nversion=2.0\nauth=login\n", nil, nil)
	if res, err = e.svc.OnboardingProbe(ctx, domain.HumanActor, e.co.ID, "claude", "claude-sonnet-4-5"); err != nil || res.Status != domain.ReadinessReady {
		t.Fatalf("probe ok: %+v %v", res, err)
	}
	if st, _ = e.svc.OnboardingStatus(ctx, e.co.ID); st.Current != domain.StepPersona || st.Done() != 2 {
		t.Fatalf("depois do probe ok: %+v", st)
	}
	if countActivity(t, e, "onboarding.harness_probed") != 2 {
		t.Fatal("cada probe vira fato de atividade")
	}

	// agente fora da lista do assistente é recusado
	if _, err := e.svc.OnboardingProbe(ctx, domain.HumanActor, e.co.ID, "acp", ""); err == nil {
		t.Fatal("acp exige comando por persona")
	}

	// primeira persona e primeira task
	p := e.persona(t, "Ana", nil)
	if st, _ = e.svc.OnboardingStatus(ctx, e.co.ID); st.Current != domain.StepTask {
		t.Fatalf("com persona: %+v", st)
	}
	tv, err := e.svc.OnboardingStartTask(ctx, domain.HumanActor, e.co.ID, p.ID, "Resumir o README", "Leia e resuma.")
	if err != nil || tv.PersonaID == nil || *tv.PersonaID != p.ID {
		t.Fatalf("primeira task: %+v %v", tv, err)
	}
	if e.jobs.wakeCount() != 1 {
		t.Fatalf("a persona deve acordar: %d wakes", e.jobs.wakeCount())
	}
	if st, _ = e.svc.OnboardingStatus(ctx, e.co.ID); !st.Complete || st.Active() || st.Done() != 4 {
		t.Fatalf("completo: %+v", st)
	}
	if countActivity(t, e, "onboarding.first_task") != 1 {
		t.Fatal("fato da primeira task")
	}
}

func TestOnboardingSkipAndDismiss(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.OnboardingSkip(ctx, domain.HumanActor, e.co.ID, domain.StepPersona); err == nil {
		t.Fatal("só o harness pode ser pulado")
	}
	st, err := e.svc.OnboardingSkip(ctx, domain.HumanActor, e.co.ID, domain.StepHarness)
	if err != nil || st.Current != domain.StepPersona {
		t.Fatalf("pulou: %+v %v", st, err)
	}
	if _, err = e.svc.OnboardingSkip(ctx, domain.HumanActor, e.co.ID, domain.StepHarness); err != nil { // idempotente
		t.Fatal(err)
	}
	if countActivity(t, e, "onboarding.step_skipped") != 1 {
		t.Fatal("pular duas vezes grava um fato só")
	}
	if st, err = e.svc.OnboardingDismiss(ctx, domain.HumanActor, e.co.ID, true); err != nil || !st.Dismissed || st.Active() {
		t.Fatalf("dispensado: %+v %v", st, err)
	}
	if st, err = e.svc.OnboardingDismiss(ctx, domain.HumanActor, e.co.ID, false); err != nil || st.Dismissed || !st.Active() {
		t.Fatalf("retomado: %+v %v", st, err)
	}
}

func TestOnboardingInstallTeamActivatesTheLead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	list, err := teams.List()
	if err != nil || len(list) == 0 {
		t.Fatalf("catálogo: %v", err)
	}
	// o catálogo usa harness reais; o assistente só instala, não roda nada
	res, err := e.svc.OnboardingInstallTeam(ctx, domain.HumanActor, e.co.ID, list[0].Package.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if res.Lead == nil || res.Lead.Status != domain.PersonaActive {
		t.Fatalf("a persona de topo deve ficar ativa: %+v", res.Lead)
	}
	ps, _ := e.svc.ListPersonas(ctx, e.co.ID)
	active := 0
	for _, p := range ps {
		if p.Status == domain.PersonaActive {
			active++
		}
	}
	if len(ps) < 2 || active != 1 {
		t.Fatalf("as outras personas seguem pausadas: %d personas, %d ativas", len(ps), active)
	}
	if st, _ := e.svc.OnboardingStatus(ctx, e.co.ID); st.Current != domain.StepHarness && st.Current != domain.StepTask {
		t.Fatalf("depois do pacote: %+v", st)
	}
	if _, err := e.svc.OnboardingInstallTeam(ctx, domain.HumanActor, e.co.ID, "nao-existe"); err == nil {
		t.Fatal("pacote desconhecido")
	}
}

func TestOnboardingRequiresRoles(t *testing.T) {
	e, ana, _ := caixaEnv(t) // Ana é membro (não admin)
	if _, err := e.svc.OnboardingProbe(as(ana), ana.Actor(), e.co.ID, "fake", ""); err == nil {
		t.Fatal("probe é do admin")
	}
	if _, err := e.svc.OnboardingDismiss(as(ana), ana.Actor(), e.co.ID, true); err == nil {
		t.Fatal("dispensar é do admin")
	}
	if _, err := e.svc.OnboardingStatus(as(ana), e.co.ID); err != nil {
		t.Fatalf("ler o estado é de qualquer membro: %v", err)
	}
}
