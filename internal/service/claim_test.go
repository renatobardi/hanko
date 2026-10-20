package service_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
)

// Claim do primeiro dono (ADR-0058 §A).

func TestClaimFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c, err := e.svc.IssueOwnerClaim(ctx)
	if err != nil || c.Token == "" || !c.ExpiresAt.After(time.Now()) {
		t.Fatalf("emitir: %+v %v", c, err)
	}
	// só o hash vai ao banco
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM owner_claims WHERE token_hash = $1`, auth.HashToken(c.Token)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hash não encontrado: %d %v", n, err)
	}
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM owner_claims WHERE token_hash = $1`, c.Token).Scan(&n); err != nil || n != 0 {
		t.Fatalf("o token em claro não pode estar no banco: %d %v", n, err)
	}
	if err := e.svc.CheckOwnerClaim(ctx, c.Token); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := e.svc.CheckOwnerClaim(ctx, "outro-token"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("token desconhecido: %v", err)
	}

	// dados inválidos não gastam o token
	if _, err := e.svc.ClaimOwner(ctx, c.Token, "dona@x.com", "Dona", "curta"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("senha curta: %v", err)
	}
	if _, err := e.svc.ClaimOwner(ctx, c.Token, "nao-e-email", "Dona", goodPass); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("e-mail inválido: %v", err)
	}
	if err := e.svc.CheckOwnerClaim(ctx, c.Token); err != nil {
		t.Fatalf("token deveria seguir válido: %v", err)
	}

	u, err := e.svc.ClaimOwner(ctx, c.Token, "Dona@X.com", "Dona", goodPass)
	if err != nil || u.Role != domain.RoleOwner || u.Email != "dona@x.com" {
		t.Fatalf("claim: %+v %v", u, err)
	}
	if _, err := e.svc.AuthenticateLocal(ctx, "dona@x.com", goodPass); err != nil {
		t.Fatalf("o dono criado deve entrar: %v", err)
	}
	// uso único: o mesmo erro genérico para "usado"
	if _, err := e.svc.ClaimOwner(ctx, c.Token, "outra@x.com", "Outra", goodPass); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reuso: %v", err)
	}
	if err := e.svc.CheckOwnerClaim(ctx, c.Token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("check depois de usado: %v", err)
	}
	// com usuário no banco não há mais o que reivindicar
	if _, err := e.svc.IssueOwnerClaim(ctx); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("emitir com usuário: %v", err)
	}
	// auditoria na mesma transação, sem token nem senha
	var action, actor string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT action, actor_email FROM admin_audit WHERE action = 'claim_owner'`).Scan(&action, &actor); err != nil || actor != "dona@x.com" {
		t.Fatalf("audit claim_owner: %q %q %v", action, actor, err)
	}
	var used *time.Time
	var by *domain.ID
	if err := e.svc.Pool.QueryRow(ctx, `SELECT used_at, used_by FROM owner_claims WHERE token_hash = $1`, auth.HashToken(c.Token)).Scan(&used, &by); err != nil || used == nil || by == nil || *by != u.ID {
		t.Fatalf("claim usado: %v %v %v", used, by, err)
	}
}

func TestClaimReissueRevokesPreviousAndExpires(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first, err := e.svc.IssueOwnerClaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.IssueOwnerClaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CheckOwnerClaim(ctx, first.Token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("o link antigo deveria ter sido revogado: %v", err)
	}
	if err := e.svc.CheckOwnerClaim(ctx, second.Token); err != nil {
		t.Fatalf("o novo vale: %v", err)
	}
	// expirado: avança o relógio além do TTL
	base := time.Now()
	e.svc.Now = func() time.Time { return base.Add(auth.ClaimTTL + time.Second) }
	if err := e.svc.CheckOwnerClaim(ctx, second.Token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expirado (check): %v", err)
	}
	if _, err := e.svc.ClaimOwner(ctx, second.Token, "dona@x.com", "Dona", goodPass); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expirado (claim): %v", err)
	}
	n, _ := e.svc.CountUsers(ctx)
	if n != 0 {
		t.Fatalf("claim expirado criou usuário: %d", n)
	}
}

// Duas disputas pelo mesmo link: no máximo um dono.
func TestClaimRaceCreatesOneOwner(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c, err := e.svc.IssueOwnerClaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var ok, notFound atomic.Int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.svc.ClaimOwner(ctx, c.Token, "dono"+string(rune('a'+i))+"@x.com", "Dono", goodPass)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, domain.ErrNotFound):
				notFound.Add(1)
			default:
				t.Errorf("erro inesperado: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 1 || notFound.Load() != 5 {
		t.Fatalf("ok=%d notFound=%d, quer 1 e 5", ok.Load(), notFound.Load())
	}
	if n, _ := e.svc.CountUsers(ctx); n != 1 {
		t.Fatalf("usuários = %d", n)
	}
}

// create-owner continua funcionando como fallback e invalida o claim aberto (existe usuário).
func TestCreateOwnerFallbackInvalidatesClaim(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c, err := e.svc.IssueOwnerClaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateOwner(ctx, "dona@x.com", "Dona", goodPass); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CheckOwnerClaim(ctx, c.Token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("com usuário o claim não vale: %v", err)
	}
	if _, err := e.svc.ClaimOwner(ctx, c.Token, "outra@x.com", "Outra", goodPass); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("claim com usuário: %v", err)
	}
}
