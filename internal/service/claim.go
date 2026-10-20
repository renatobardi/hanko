package service

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Claim do primeiro dono (ADR-0058 §A). O claim é da instalação, anterior a qualquer empresa: não gera
// activity_event (que é por empresa); a trilha é o admin_audit, na mesma transação.

// OwnerClaim é o link recém-emitido. Token só existe aqui: no banco fica o hash.
type OwnerClaim struct {
	Token     string
	ExpiresAt time.Time
}

// IssueOwnerClaim emite um claim novo e revoga os abertos. ErrConflict se já há usuário (não há o que reivindicar).
func (s *Service) IssueOwnerClaim(ctx context.Context) (OwnerClaim, error) {
	ctx, span := otel.Tracer("hanko/service").Start(ctx, "service.IssueOwnerClaim")
	defer span.End()
	tok, hash, err := auth.NewClaimToken()
	if err != nil {
		return OwnerClaim{}, err
	}
	var out OwnerClaim
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockUsers(ctx); err != nil {
			return fmt.Errorf("lock users: %w", err)
		}
		n, err := t.q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("já existe um usuário: %w", domain.ErrConflict)
		}
		if _, err := t.q.RevokeOpenOwnerClaims(ctx, t.now); err != nil {
			return fmt.Errorf("revoke claims: %w", err)
		}
		exp := t.now.Add(auth.ClaimTTL)
		if err := t.q.InsertOwnerClaim(ctx, store.InsertOwnerClaimParams{ID: domain.NewID(), TokenHash: hash, Now: t.now, ExpiresAt: exp}); err != nil {
			return mapErr(err, "insert owner claim")
		}
		if err := s.recordAdmin(ctx, t.q, "claim_issue", "", nil, "ok"); err != nil {
			return err
		}
		out = OwnerClaim{Token: tok, ExpiresAt: exp}
		return nil
	})
	if err != nil {
		span.RecordError(err)
		return OwnerClaim{}, err
	}
	return out, nil
}

// ownerClaimOpen confere, dentro da transação, que o token é de um claim aberto e que ainda não há usuário. Qualquer
// falha vira o mesmo ErrNotFound: o chamador não distingue "inexistente" de "usado" ou "expirado".
func ownerClaimOpen(t *txc, token string) (store.OwnerClaim, error) {
	if err := t.q.LockUsers(t.ctx); err != nil {
		return store.OwnerClaim{}, fmt.Errorf("lock users: %w", err)
	}
	n, err := t.q.CountUsers(t.ctx)
	if err != nil {
		return store.OwnerClaim{}, fmt.Errorf("count users: %w", err)
	}
	if n > 0 || token == "" || len(token) > 200 {
		return store.OwnerClaim{}, domain.ErrNotFound
	}
	c, err := t.q.GetOwnerClaimByHashForUpdate(t.ctx, auth.HashToken(token))
	if err != nil {
		if isNoRows(err) {
			return store.OwnerClaim{}, domain.ErrNotFound
		}
		return store.OwnerClaim{}, fmt.Errorf("get owner claim: %w", err)
	}
	if c.UsedAt != nil || !t.now.Before(c.ExpiresAt) {
		return store.OwnerClaim{}, domain.ErrNotFound
	}
	return c, nil
}

// CheckOwnerClaim diz se o token ainda vale (a página de claim só mostra o formulário se sim), sem travar nada.
// ErrNotFound se não vale.
func (s *Service) CheckOwnerClaim(ctx context.Context, token string) error {
	q := s.read()
	n, err := q.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if n > 0 || token == "" || len(token) > 200 {
		return domain.ErrNotFound
	}
	c, err := q.GetOwnerClaimByHash(ctx, auth.HashToken(token))
	if err != nil {
		if isNoRows(err) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("get owner claim: %w", err)
	}
	if c.UsedAt != nil || !s.Now().UTC().Before(c.ExpiresAt) {
		return domain.ErrNotFound
	}
	return nil
}

// ClaimOwner cria o primeiro dono com o token do claim e o invalida, numa transação só: duas disputas pelo mesmo link
// produzem no máximo um dono. Os dados são validados antes de gastar o token (senha fraca não queima o link).
// ErrNotFound: token inválido, usado, expirado ou já existe usuário.
func (s *Service) ClaimOwner(ctx context.Context, token, email, name, password string) (domain.User, error) {
	ctx, span := otel.Tracer("hanko/service").Start(ctx, "service.ClaimOwner")
	defer span.End()
	email, name, hash, err := s.prepareOwner(ctx, email, name, password)
	if err != nil {
		return domain.User{}, err
	}
	var out domain.User
	err = s.withTx(ctx, func(t *txc) error {
		c, err := ownerClaimOpen(t, token)
		if err != nil {
			return err
		}
		u, err := createFirstOwner(t, email, name, hash)
		if err != nil {
			return err
		}
		if err := t.q.UseOwnerClaim(ctx, store.UseOwnerClaimParams{ID: c.ID, Now: t.now, UsedBy: &u.ID}); err != nil {
			return mapErr(err, "use owner claim")
		}
		if _, err := t.q.RevokeOpenOwnerClaims(ctx, t.now); err != nil {
			return fmt.Errorf("revoke claims: %w", err)
		}
		if err := t.q.InsertAdminAudit(ctx, store.InsertAdminAuditParams{ID: domain.NewID(), At: t.now, ActorID: &u.ID,
			ActorEmail: u.Email, Action: "claim_owner", TargetID: &u.ID, TargetEmail: u.Email, Result: "ok"}); err != nil {
			return fmt.Errorf("admin audit: %w", err)
		}
		out = u
		return nil
	})
	if err != nil {
		span.RecordError(err)
		return domain.User{}, err
	}
	span.SetAttributes(attribute.String("user.role", string(out.Role)))
	return out, nil
}
