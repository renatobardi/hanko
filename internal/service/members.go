package service

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Membros e convites (ADR-0021). Quem gerencia:
//   - owner: tudo (convida e muda qualquer papel, remove qualquer membro);
//   - admin: convida, revoga convites, muda e remove só membros member e viewer, e só para member/viewer;
//   - member e viewer: só enxergam a lista de membros.
// Nunca fica uma empresa sem owner.

func memberFromRow(companyID, userID domain.ID, role string, invitedBy *domain.ID, created time.Time, email, name, provider string) domain.Member {
	return domain.Member{CompanyID: companyID, UserID: userID, Email: email, Name: name, Provider: domain.AuthProvider(provider),
		Role: domain.MemberRole(role), InvitedBy: invitedBy, CreatedAt: created}
}

func inviteFromRow(r store.CompanyInvite) domain.Invite {
	return domain.Invite{ID: r.ID, CompanyID: r.CompanyID, Email: r.Email, Role: domain.MemberRole(r.Role), InvitedBy: r.InvitedBy,
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, AcceptedAt: r.AcceptedAt, RevokedAt: r.RevokedAt}
}

// ListMembers lista os membros da empresa (qualquer papel pode ver).
func (s *Service) ListMembers(ctx context.Context, companyID domain.ID) ([]domain.Member, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListMembers(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	out := make([]domain.Member, len(rows))
	for i, r := range rows {
		out[i] = memberFromRow(r.CompanyID, r.UserID, r.Role, r.InvitedBy, r.CreatedAt, r.Email, r.Name, r.Provider)
	}
	return out, nil
}

// MyRole devolve o papel do chamador na empresa. Sem usuário no contexto (trusted), owner.
func (s *Service) MyRole(ctx context.Context, companyID domain.ID) (domain.MemberRole, error) {
	role, _, err := s.callerRole(ctx, companyID)
	return role, err
}

// ListInvites lista os convites abertos (inclui os vencidos ainda não revogados). Só admin ou owner.
func (s *Service) ListInvites(ctx context.Context, companyID domain.ID) ([]domain.Invite, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	rows, err := s.read().ListOpenInvites(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	out := make([]domain.Invite, len(rows))
	for i, r := range rows {
		out[i] = inviteFromRow(r)
	}
	return out, nil
}

// InviteMember cria um convite e devolve o token em claro (só aqui; no banco fica o hash). Um convite
// aberto anterior para o mesmo e-mail é revogado. E-mail que já é membro = ErrConflict.
func (s *Service) InviteMember(ctx context.Context, actor domain.Actor, companyID domain.ID, email string, role domain.MemberRole) (domain.Invite, string, error) {
	by, _, err := s.callerRole(ctx, companyID)
	if err != nil {
		return domain.Invite{}, "", err
	}
	if !by.AtLeast(domain.MemberAdmin) {
		return domain.Invite{}, "", domain.ErrForbidden
	}
	email, err = NormalizeEmail(email)
	if err != nil {
		return domain.Invite{}, "", err
	}
	if !role.Valid() {
		return domain.Invite{}, "", domain.Invalid("role", "owner, admin, member ou viewer")
	}
	if !by.Manages(role) {
		return domain.Invite{}, "", domain.ErrForbidden
	}
	tok, hash, err := auth.NewInviteToken()
	if err != nil {
		return domain.Invite{}, "", err
	}
	var out domain.Invite
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockCompanyMembers(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		if u, err := t.q.GetUserByEmail(ctx, email); err == nil {
			if _, err := t.q.GetMemberRole(ctx, store.GetMemberRoleParams{CompanyID: companyID, UserID: u.ID}); err == nil {
				return fmt.Errorf("este e-mail já é membro da empresa: %w", domain.ErrConflict)
			} else if !isNoRows(err) {
				return fmt.Errorf("get member: %w", err)
			}
		} else if !isNoRows(err) {
			return fmt.Errorf("get user: %w", err)
		}
		if err := t.q.RevokeOpenInvitesForEmail(ctx, store.RevokeOpenInvitesForEmailParams{CompanyID: companyID, Email: email, Now: &t.now}); err != nil {
			return fmt.Errorf("revoke previous invites: %w", err)
		}
		row, err := t.q.CreateInvite(ctx, store.CreateInviteParams{ID: domain.NewID(), CompanyID: companyID, Email: email,
			Role: string(role), TokenHash: hash, InvitedBy: actor.ID, Now: t.now, ExpiresAt: t.now.Add(auth.InviteTTL)})
		if err != nil {
			return mapErr(err, "create invite")
		}
		out = inviteFromRow(row)
		return t.activity(actor, companyID, "member.invited", "invite", out.ID,
			map[string]any{"email": email, "role": role}, "")
	})
	if err != nil {
		return domain.Invite{}, "", err
	}
	return out, tok, nil
}

// RevokeInvite revoga um convite aberto. Admin só revoga convites member/viewer.
func (s *Service) RevokeInvite(ctx context.Context, actor domain.Actor, companyID, inviteID domain.ID) error {
	by, _, err := s.callerRole(ctx, companyID)
	if err != nil {
		return err
	}
	if !by.AtLeast(domain.MemberAdmin) {
		return domain.ErrForbidden
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetInviteByID(ctx, store.GetInviteByIDParams{CompanyID: companyID, ID: inviteID})
		if err != nil {
			return mapErr(err, "invite")
		}
		if cur.AcceptedAt != nil || cur.RevokedAt != nil {
			return notFound("invite")
		}
		if !by.Manages(domain.MemberRole(cur.Role)) {
			return domain.ErrForbidden
		}
		if _, err := t.q.RevokeInvite(ctx, store.RevokeInviteParams{CompanyID: companyID, ID: inviteID, Now: &t.now}); err != nil {
			return mapErr(err, "invite")
		}
		return t.activity(actor, companyID, "invite.revoked", "invite", inviteID, map[string]any{"email": cur.Email, "role": cur.Role}, "")
	})
}

// SetMemberRole muda o papel de um membro. Não deixa a empresa sem owner (ErrConflict).
func (s *Service) SetMemberRole(ctx context.Context, actor domain.Actor, companyID, userID domain.ID, role domain.MemberRole) (domain.Member, error) {
	by, _, err := s.callerRole(ctx, companyID)
	if err != nil {
		return domain.Member{}, err
	}
	if !by.AtLeast(domain.MemberAdmin) {
		return domain.Member{}, domain.ErrForbidden
	}
	if !role.Valid() {
		return domain.Member{}, domain.Invalid("role", "owner, admin, member ou viewer")
	}
	var out domain.Member
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockCompanyMembers(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		cur, err := t.q.GetMember(ctx, store.GetMemberParams{CompanyID: companyID, UserID: userID})
		if err != nil {
			return mapErr(err, "member")
		}
		old := domain.MemberRole(cur.Role)
		if !by.Manages(old) || !by.Manages(role) {
			return domain.ErrForbidden
		}
		if old == domain.MemberOwner && role != domain.MemberOwner {
			if err := lastOwnerGuard(ctx, t.q, companyID); err != nil {
				return err
			}
		}
		if old != role {
			if err := t.q.SetMemberRole(ctx, store.SetMemberRoleParams{CompanyID: companyID, UserID: userID, Role: string(role), Now: t.now}); err != nil {
				return fmt.Errorf("set role: %w", err)
			}
			if err := t.activity(actor, companyID, "member.role_changed", "user", userID,
				map[string]any{"email": cur.Email, "from": old, "to": role}, ""); err != nil {
				return err
			}
		}
		out = memberFromRow(cur.CompanyID, cur.UserID, string(role), cur.InvitedBy, cur.CreatedAt, cur.Email, cur.Name, cur.Provider)
		return nil
	})
	return out, err
}

// RemoveMember tira um membro da empresa. Qualquer membro pode sair (remover a si mesmo), exceto o
// último owner; remover outro exige admin (só member/viewer) ou owner.
func (s *Service) RemoveMember(ctx context.Context, actor domain.Actor, companyID, userID domain.ID) error {
	by, restricted, err := s.callerRole(ctx, companyID)
	if err != nil {
		return err
	}
	self := false
	if id, ok := auth.IdentityFrom(ctx); ok && id.User.ID == userID {
		self = true
	}
	if restricted && !self && !by.AtLeast(domain.MemberAdmin) {
		return domain.ErrForbidden
	}
	return s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockCompanyMembers(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		cur, err := t.q.GetMember(ctx, store.GetMemberParams{CompanyID: companyID, UserID: userID})
		if err != nil {
			return mapErr(err, "member")
		}
		old := domain.MemberRole(cur.Role)
		if !self && !by.Manages(old) {
			return domain.ErrForbidden
		}
		if old == domain.MemberOwner {
			if err := lastOwnerGuard(ctx, t.q, companyID); err != nil {
				return err
			}
		}
		if _, err := t.q.RemoveMember(ctx, store.RemoveMemberParams{CompanyID: companyID, UserID: userID}); err != nil {
			return fmt.Errorf("remove member: %w", err)
		}
		return t.activity(actor, companyID, "member.removed", "user", userID, map[string]any{"email": cur.Email, "role": old}, "")
	})
}

// lastOwnerGuard recusa a mudança se o membro é o único owner (chamar com a empresa travada).
func lastOwnerGuard(ctx context.Context, q *store.Queries, companyID domain.ID) error {
	n, err := q.CountOwners(ctx, companyID)
	if err != nil {
		return fmt.Errorf("count owners: %w", err)
	}
	if n <= 1 {
		return fmt.Errorf("a empresa precisa de pelo menos um owner: %w", domain.ErrConflict)
	}
	return nil
}

// ---------- aceite ----------

// InvitePreview é o que a página do convite mostra a quem ainda não aceitou.
type InvitePreview struct {
	CompanyName string
	CompanySlug string
	Email       string
	Role        domain.MemberRole
	// HasAccount: já existe usuário com esse e-mail (entrar em vez de criar a conta).
	HasAccount bool
}

// openInvite acha o convite pelo token e confere validade. Qualquer problema = ErrInviteInvalid.
func openInvite(ctx context.Context, q *store.Queries, token string, now time.Time) (store.CompanyInvite, error) {
	if token == "" || len(token) > 200 {
		return store.CompanyInvite{}, domain.ErrInviteInvalid
	}
	hash := auth.HashToken(token)
	inv, err := q.GetInviteByHash(ctx, hash)
	if err != nil {
		if isNoRows(err) {
			return store.CompanyInvite{}, domain.ErrInviteInvalid
		}
		return store.CompanyInvite{}, fmt.Errorf("get invite: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(inv.TokenHash), []byte(hash)) != 1 ||
		inv.AcceptedAt != nil || inv.RevokedAt != nil || !now.Before(inv.ExpiresAt) {
		return store.CompanyInvite{}, domain.ErrInviteInvalid
	}
	return inv, nil
}

// PreviewInvite descreve um convite válido para a página pública. Inválido = ErrInviteInvalid.
func (s *Service) PreviewInvite(ctx context.Context, token string) (InvitePreview, error) {
	q := s.read()
	inv, err := openInvite(ctx, q, token, s.Now().UTC())
	if err != nil {
		return InvitePreview{}, err
	}
	co, err := q.GetCompanyByID(ctx, inv.CompanyID)
	if err != nil {
		return InvitePreview{}, mapErr(err, "company")
	}
	_, uerr := q.GetUserByEmail(ctx, inv.Email)
	if uerr != nil && !isNoRows(uerr) {
		return InvitePreview{}, fmt.Errorf("get user: %w", uerr)
	}
	return InvitePreview{CompanyName: co.Name, CompanySlug: co.Slug, Email: inv.Email, Role: domain.MemberRole(inv.Role), HasAccount: uerr == nil}, nil
}

// InviteValidFor informa se o token é um convite aberto para o e-mail (admissão no login).
func (s *Service) InviteValidFor(ctx context.Context, token, email string) bool {
	inv, err := openInvite(ctx, s.read(), token, s.Now().UTC())
	return err == nil && strings.EqualFold(inv.Email, strings.TrimSpace(email))
}

// acceptInTx consome o convite para o usuário (e-mail precisa bater). Chamar dentro da transação.
func (t *txc) acceptInvite(token string, u domain.User) (domain.Member, error) {
	inv, err := openInvite(t.ctx, t.q, token, t.now)
	if err != nil {
		return domain.Member{}, err
	}
	if !strings.EqualFold(inv.Email, u.Email) {
		return domain.Member{}, domain.ErrInviteInvalid
	}
	if err := t.q.LockCompanyMembers(t.ctx, inv.CompanyID); err != nil {
		return domain.Member{}, mapErr(err, "company")
	}
	if _, err := t.q.AcceptInvite(t.ctx, store.AcceptInviteParams{ID: inv.ID, UserID: &u.ID, Now: &t.now}); err != nil {
		if isNoRows(err) {
			return domain.Member{}, domain.ErrInviteInvalid // consumido por outra requisição
		}
		return domain.Member{}, fmt.Errorf("accept invite: %w", err)
	}
	if err := t.q.MarkUserInvited(t.ctx, u.ID); err != nil {
		return domain.Member{}, fmt.Errorf("mark invited: %w", err)
	}
	role := domain.MemberRole(inv.Role)
	if cur, err := t.q.GetMemberRole(t.ctx, store.GetMemberRoleParams{CompanyID: inv.CompanyID, UserID: u.ID}); err == nil {
		// já era membro (convite antigo): mantém o maior dos dois papéis
		if domain.MemberRole(cur).AtLeast(role) {
			role = domain.MemberRole(cur)
		} else if err := t.q.SetMemberRole(t.ctx, store.SetMemberRoleParams{CompanyID: inv.CompanyID, UserID: u.ID, Role: string(role), Now: t.now}); err != nil {
			return domain.Member{}, fmt.Errorf("set role: %w", err)
		}
	} else if !isNoRows(err) {
		return domain.Member{}, fmt.Errorf("get member: %w", err)
	} else if _, err := t.q.AddMember(t.ctx, store.AddMemberParams{CompanyID: inv.CompanyID, UserID: u.ID, Role: string(role),
		InvitedBy: inv.InvitedBy, Now: t.now}); err != nil {
		return domain.Member{}, mapErr(err, "add member")
	}
	if err := t.activity(u.Actor(), inv.CompanyID, "member.joined", "user", u.ID,
		map[string]any{"email": u.Email, "role": role, "invite_id": inv.ID}, ""); err != nil {
		return domain.Member{}, err
	}
	return domain.Member{CompanyID: inv.CompanyID, UserID: u.ID, Email: u.Email, Name: u.Name, Provider: u.Provider, Role: role, InvitedBy: inv.InvitedBy, CreatedAt: t.now}, nil
}

// AcceptInvite aceita o convite como o usuário já autenticado. O e-mail do usuário precisa ser o
// do convite (comparado sem caixa); senão, ErrInviteInvalid. No provider firebase o e-mail vem do ID
// token já verificado (e-mail confirmado).
func (s *Service) AcceptInvite(ctx context.Context, token string, u domain.User) (domain.Member, error) {
	var out domain.Member
	err := s.withTx(ctx, func(t *txc) error {
		m, err := t.acceptInvite(token, u)
		out = m
		return err
	})
	return out, err
}

// AcceptInviteLocal cria a conta local do convidado (e-mail do convite, nome e senha) e aceita o
// convite na mesma transação. Se já existe usuário com o e-mail, ErrConflict (a pessoa deve entrar).
func (s *Service) AcceptInviteLocal(ctx context.Context, token, name, password string) (domain.User, domain.Member, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.User{}, domain.Member{}, domain.Invalid("name", "required")
	}
	if err := auth.ValidatePassword(password); err != nil {
		return domain.User{}, domain.Member{}, domain.Invalid("password", err.Error())
	}
	if _, err := openInvite(ctx, s.read(), token, s.Now().UTC()); err != nil { // antes de gastar argon2id
		return domain.User{}, domain.Member{}, err
	}
	release, err := s.PasswordGate.Acquire(ctx)
	if err != nil {
		return domain.User{}, domain.Member{}, err
	}
	hash, err := auth.HashPassword(password)
	release()
	if err != nil {
		return domain.User{}, domain.Member{}, err
	}
	var user domain.User
	var mem domain.Member
	err = s.withTx(ctx, func(t *txc) error {
		inv, err := openInvite(ctx, t.q, token, t.now)
		if err != nil {
			return err
		}
		if err := t.q.LockUsers(ctx); err != nil {
			return fmt.Errorf("lock users: %w", err)
		}
		if _, err := t.q.GetUserByEmail(ctx, inv.Email); err == nil {
			return fmt.Errorf("já existe uma conta com este e-mail: %w", domain.ErrConflict)
		} else if !isNoRows(err) {
			return fmt.Errorf("get user: %w", err)
		}
		row, err := t.q.CreateUser(ctx, store.CreateUserParams{ID: domain.NewID(), Email: inv.Email, Name: name,
			Provider: string(domain.ProviderLocal), PasswordHash: &hash, Role: string(domain.RoleMember), Now: t.now})
		if err != nil {
			return mapErr(err, "create user")
		}
		user = userFromRow(row)
		mem, err = t.acceptInvite(token, user)
		return err
	})
	return user, mem, err
}
