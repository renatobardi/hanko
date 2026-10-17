package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// ${secret:X} também vale no env dos ambientes de execução (ADR-0037) e é resolvido só no uso.
func TestEnvironmentEnvAcceptsSecretRefs(t *testing.T) {
	e := newEnvEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	if _, err := e.svc.CreateSecret(ctx, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "build-token", Value: sentinel}); err != nil {
		t.Fatal(err)
	}

	in := sshInput("Build")
	in.Env = map[string]string{"API_TOKEN": "${secret:nao-existe}"}
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("referência a segredo inexistente: %v", err)
	}
	in.Env = map[string]string{"API_TOKEN": "${secret:build-token}"}
	build, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var enc string
	_ = e.svc.Pool.QueryRow(ctx, `SELECT secret_enc FROM environments WHERE id=$1`, build.ID).Scan(&enc)
	if strings.Contains(enc, sentinel) {
		t.Fatal("o valor não pode estar no ambiente")
	}
	if err := e.svc.SetDefaultEnvironment(ctx, domain.HumanActor, e.co.ID, &build.ID); err != nil {
		t.Fatal(err)
	}
	re, err := e.svc.RunEnvironment(ctx, e.co.ID, ana.ID)
	if err != nil || re.Target.Env["API_TOKEN"] != sentinel {
		t.Fatalf("o executor deve receber o valor resolvido: %+v %v", re.Target.Env, err)
	}
	// o segredo aparece como usado pelo ambiente? (uso por persona/integração; o ambiente falha cedo se sumir)
	if err := e.svc.DeleteSecret(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "build-token", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RunEnvironment(ctx, e.co.ID, ana.ID); err == nil || strings.Contains(err.Error(), sentinel) || !strings.Contains(err.Error(), "build-token") {
		t.Fatalf("segredo apagado: %v", err)
	}
}
