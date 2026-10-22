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

// Auditoria das operações administrativas da instalação (hanko auth user ...): além do log estruturado, cada
// operação grava uma linha em admin_audit (quem operou, em quem, o quê e o resultado; nunca senha nem token).
// O resultado "ok" é gravado NA MESMA transação da operação (ou ambos ou nenhum); "denied" e "refused" não têm
// transação de sucesso, então a linha é gravada à parte (falha ao gravar é registrada no log, não mascara o erro
// da operação). Os eventos de atividade são por empresa; estas operações são da instalação.

// operatorOf devolve o operador da identidade no contexto (vazio se não houver).
func operatorOf(ctx context.Context) (id *domain.ID, email string) {
	if i, ok := auth.IdentityFrom(ctx); ok {
		uid := i.User.ID
		return &uid, i.User.Email
	}
	return nil, ""
}

// recordAdmin grava a linha de auditoria com q (uma transação ou o pool).
func (s *Service) recordAdmin(ctx context.Context, q *store.Queries, action, target string, targetID *domain.ID, result string) error {
	actorID, actorEmail := operatorOf(ctx)
	if err := q.InsertAdminAudit(ctx, store.InsertAdminAuditParams{ID: domain.NewID(), At: s.Now().UTC(), ActorID: actorID,
		ActorEmail: actorEmail, Action: action, TargetID: targetID, TargetEmail: strings.TrimSpace(target), Result: result}); err != nil {
		return fmt.Errorf("admin audit: %w", err)
	}
	return nil
}

// logAdmin registra a operação em log estruturado: quem operou, em quem e o quê.
func (s *Service) logAdmin(ctx context.Context, level slog.Level, action, target string, targetID *domain.ID, result string, attrs ...any) {
	log := s.Log
	if log == nil {
		return
	}
	_, actor := operatorOf(ctx)
	actorID := ""
	if id, ok := auth.IdentityFrom(ctx); ok {
		actorID = id.User.ID.String()
	}
	tid := ""
	if targetID != nil {
		tid = targetID.String()
	}
	log.Log(ctx, level, "admin: "+action, append([]any{"audit", true, "action", action, "actor_email", actor, "actor_id", actorID,
		"target_email", strings.TrimSpace(target), "target_id", tid, "result", result}, attrs...)...)
}

// auditAdmin registra uma operação sem transação própria (recusada ou negada): linha avulsa mais o log.
func (s *Service) auditAdmin(ctx context.Context, level slog.Level, action, target string, targetID *domain.ID, result string, attrs ...any) {
	if err := s.recordAdmin(ctx, s.read(), action, target, targetID, result); err != nil && s.Log != nil {
		s.Log.ErrorContext(ctx, "admin: falha ao gravar a auditoria", "action", action, "err", err)
	}
	s.logAdmin(ctx, level, action, target, targetID, result, attrs...)
}

// AdminListAudit lista a auditoria das operações administrativas, da mais recente para a mais antiga (limit 0 =
// 100; máximo 1000). Só o dono da instalação.
func (s *Service) AdminListAudit(ctx context.Context, limit int) ([]domain.AdminAuditEntry, error) {
	q := s.read()
	if err := s.requireInstallOwner(ctx, q); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 1000)
	rows, err := q.ListAdminAudit(ctx, i32(limit))
	if err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	out := make([]domain.AdminAuditEntry, len(rows))
	for i, r := range rows {
		out[i] = domain.AdminAuditEntry{ID: r.ID, At: r.At, ActorID: r.ActorID, ActorEmail: r.ActorEmail, Action: r.Action,
			TargetID: r.TargetID, TargetEmail: r.TargetEmail, Result: r.Result}
	}
	return out, nil
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
		s.auditAdmin(ctx, slog.LevelWarn, "user.reset_password", email, nil, "denied")
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
		return s.recordAdmin(ctx, t.q, "user.reset_password", row.Email, &row.ID, "ok")
	})
	if err != nil {
		return domain.User{}, "", err
	}
	s.logAdmin(ctx, slog.LevelInfo, "user.reset_password", row.Email, &row.ID, "ok", "temporary_password_generated", generated)
	if !generated {
		newPassword = ""
	}
	return userFromRow(row), newPassword, nil
}

// AdminRemoveUser apaga uma conta (e, em cascata, sessões, API keys e vínculos). Recusa o dono da
// instalação e quem é o único owner de alguma empresa (ErrConflict).
func (s *Service) AdminRemoveUser(ctx context.Context, email string) error {
	if err := s.requireInstallOwner(ctx, s.read()); err != nil {
		s.auditAdmin(ctx, slog.LevelWarn, "user.remove", email, nil, "denied")
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
		// favoritos, recentes e dispensas não têm FK para users (o modo trusted não tem usuário): limpa à mão (ADR-0061)
		if err := t.q.DeleteNavItemsForSubject(ctx, row.ID.String()); err != nil {
			return fmt.Errorf("delete nav items: %w", err)
		}
		if err := t.q.DeleteDismissalsForSubject(ctx, row.ID.String()); err != nil {
			return fmt.Errorf("delete dismissals: %w", err)
		}
		removed = row.ID
		return s.recordAdmin(ctx, t.q, "user.remove", row.Email, &row.ID, "ok")
	})
	if err != nil {
		s.auditAdmin(ctx, slog.LevelWarn, "user.remove", email, nil, "refused")
		return err
	}
	s.logAdmin(ctx, slog.LevelInfo, "user.remove", email, &removed, "ok")
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

// ChangeOwnPassword troca a senha da própria conta (provider local): confere a senha atual, valida a nova
// e encerra todas as sessões da conta (o navegador volta para o login). Fica na trilha de admin.
func (s *Service) ChangeOwnPassword(ctx context.Context, current, next string) error {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id.User.ID == (domain.ID{}) {
		return domain.ErrUnauthenticated
	}
	row, err := s.read().GetUserByID(ctx, id.User.ID)
	if err != nil {
		return mapErr(err, "user")
	}
	if row.Provider != string(domain.ProviderLocal) || row.PasswordHash == nil {
		return domain.Invalid("password", "a conta não usa senha local (provider "+row.Provider+")")
	}
	if err := auth.ValidatePassword(next); err != nil {
		return domain.Invalid("new_password", err.Error())
	}
	release, err := s.PasswordGate.Acquire(ctx)
	if err != nil {
		return err
	}
	match, err := auth.VerifyPassword(current, *row.PasswordHash)
	if err != nil || !match {
		release()
		s.auditAdmin(ctx, slog.LevelWarn, "user.change_password", row.Email, &row.ID, "denied")
		return domain.Invalid("current_password", "senha atual incorreta")
	}
	hash, err := auth.HashPassword(next)
	release()
	if err != nil {
		return err
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
		return s.recordAdmin(ctx, t.q, "user.change_password", row.Email, &row.ID, "ok")
	})
	if err != nil {
		return err
	}
	s.logAdmin(ctx, slog.LevelInfo, "user.change_password", row.Email, &row.ID, "ok")
	return nil
}
