package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log/slog"
	"strings"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Administração de usuários da instalação (ADR-0021, Consequências). No provider local o link de
// convite vale como prova de identidade; o dono da instalação (users.role = owner) precisa poder
// listar, redefinir a senha e remover contas sem mexer no banco. Todas as operações exigem uma
// identidade no contexto cujo papel NO BANCO seja owner (o papel do contexto não é confiado).

// requireInstallOwner confere, no banco, que quem chama é o dono da instalação.
func (s *Service) requireInstallOwner(ctx context.Context, q *store.Queries) error {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return domain.ErrForbidden
	}
	u, err := q.GetUserByID(ctx, id.User.ID)
	if err != nil {
		if isNoRows(err) {
			return domain.ErrForbidden
		}
		return fmt.Errorf("get user: %w", err)
	}
	if u.Role != string(domain.RoleOwner) {
		return domain.ErrForbidden
	}
	return nil
}

// auditAdmin registra uma operação administrativa da instalação em log estruturado: quem operou, em quem e o
// quê. Nunca recebe senha nem token. (Os eventos de atividade são por empresa; estas operações são da
// instalação, então o log é o registro.)
func (s *Service) auditAdmin(ctx context.Context, level slog.Level, action, target string, attrs ...any) {
	log := s.Log
	if log == nil {
		return
	}
	actor, actorID := "", ""
	if id, ok := auth.IdentityFrom(ctx); ok {
		actor, actorID = id.User.Email, id.User.ID.String()
	}
	log.Log(ctx, level, "admin: "+action, append([]any{"audit", true, "action", action, "actor_email", actor, "actor_id", actorID,
		"target_email", strings.TrimSpace(target)}, attrs...)...)
}

// AdminListUsers lista todos os usuários da instalação. Só o dono da instalação.
func (s *Service) AdminListUsers(ctx context.Context) ([]domain.User, error) {
	if err := s.requireInstallOwner(ctx, s.read()); err != nil {
		return nil, err
	}
	return s.ListUsers(ctx)
}

// AdminResetPassword redefine a senha de uma conta do provider local e encerra as sessões dela. Sem
// newPassword, gera uma senha temporária e a devolve (a única vez em que existe em claro).
func (s *Service) AdminResetPassword(ctx context.Context, email, newPassword string) (domain.User, string, error) {
	if err := s.requireInstallOwner(ctx, s.read()); err != nil {
		s.auditAdmin(ctx, slog.LevelWarn, "user.reset_password", email, "result", "denied")
		return domain.User{}, "", err
	}
	row, err := s.read().GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		return domain.User{}, "", mapErr(err, "user")
	}
	if row.Provider != string(domain.ProviderLocal) {
		return domain.User{}, "", domain.Invalid("email", "a conta não usa senha local (provider "+row.Provider+")")
	}
	generated := newPassword == ""
	if generated {
		newPassword, err = TempPassword()
		if err != nil {
			return domain.User{}, "", err
		}
	}
	if err := auth.ValidatePassword(newPassword); err != nil {
		return domain.User{}, "", domain.Invalid("password", err.Error())
	}
	release, err := s.PasswordGate.Acquire(ctx)
	if err != nil {
		return domain.User{}, "", err
	}
	hash, err := auth.HashPassword(newPassword)
	release()
	if err != nil {
		return domain.User{}, "", err
	}
	err = s.withTx(ctx, func(t *txc) error {
		if n, err := t.q.SetUserPassword(ctx, store.SetUserPasswordParams{ID: row.ID, PasswordHash: &hash}); err != nil {
			return fmt.Errorf("set password: %w", err)
		} else if n == 0 {
			return notFound("user")
		}
		if err := t.q.DeleteSessionsByUser(ctx, row.ID); err != nil {
			return fmt.Errorf("end sessions: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.User{}, "", err
	}
	s.auditAdmin(ctx, slog.LevelInfo, "user.reset_password", row.Email, "target_id", row.ID.String(), "result", "ok",
		"temporary_password_generated", generated)
	if !generated {
		newPassword = ""
	}
	return userFromRow(row), newPassword, nil
}

// AdminRemoveUser apaga uma conta (e, em cascata, sessões, API keys e vínculos). Recusa o dono da
// instalação e quem é o único owner de alguma empresa (ErrConflict).
func (s *Service) AdminRemoveUser(ctx context.Context, email string) error {
	if err := s.requireInstallOwner(ctx, s.read()); err != nil {
		s.auditAdmin(ctx, slog.LevelWarn, "user.remove", email, "result", "denied")
		return err
	}
	var removed domain.ID
	err := s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockUsers(ctx); err != nil {
			return fmt.Errorf("lock users: %w", err)
		}
		row, err := t.q.GetUserByEmail(ctx, strings.TrimSpace(email))
		if err != nil {
			return mapErr(err, "user")
		}
		if row.Role == string(domain.RoleOwner) {
			return fmt.Errorf("o dono da instalação não pode ser removido: %w", domain.ErrConflict)
		}
		n, err := t.q.CountSoleOwnedCompanies(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("count owned: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("a pessoa é a única owner de %d empresa(s): promova outro owner antes: %w", n, domain.ErrConflict)
		}
		if _, err := t.q.DeleteUser(ctx, row.ID); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		removed = row.ID
		return nil
	})
	if err != nil {
		s.auditAdmin(ctx, slog.LevelWarn, "user.remove", email, "result", "refused")
		return err
	}
	s.auditAdmin(ctx, slog.LevelInfo, "user.remove", email, "target_id", removed.String(), "result", "ok")
	return nil
}

// TempPassword gera uma senha temporária aleatória (20 caracteres base32, 100 bits).
func TempPassword() (string, error) {
	b := make([]byte, 13)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerar senha: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))[:20], nil
}
