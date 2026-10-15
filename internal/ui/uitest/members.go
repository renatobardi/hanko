package uitest

import (
	"context"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Membros e convites em memória. O Fake não aplica papéis (isso é do service); MyRoleIs escolhe o
// papel que a UI enxerga para esconder ou mostrar os controles.

// SetMembers define os membros e MyRoleIs o papel do usuário logado.
func (f *Fake) SetMembers(ms []domain.Member, invs []domain.Invite) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Members, f.Invites = ms, invs
}

func (f *Fake) MyRole(context.Context, domain.ID) (domain.MemberRole, error) {
	if f.MyRoleIs == "" {
		return domain.MemberOwner, nil
	}
	return f.MyRoleIs, nil
}

func (f *Fake) ListMembers(context.Context, domain.ID) ([]domain.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Member(nil), f.Members...), nil
}

func (f *Fake) ListInvites(context.Context, domain.ID) ([]domain.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Invite(nil), f.Invites...), nil
}

func (f *Fake) InviteMember(_ context.Context, _ domain.Actor, companyID domain.ID, email string, role domain.MemberRole) (domain.Invite, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("InviteMember")
	if err := f.fail("InviteMember"); err != nil {
		return domain.Invite{}, "", err
	}
	if !strings.Contains(email, "@") {
		return domain.Invite{}, "", domain.Invalid("email", "endereço inválido")
	}
	now := time.Now().UTC()
	inv := domain.Invite{ID: domain.NewID(), CompanyID: companyID, Email: strings.ToLower(email), Role: role, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	f.Invites = append(f.Invites, inv)
	return inv, "tok-" + inv.ID.String(), nil
}

func (f *Fake) RevokeInvite(_ context.Context, _ domain.Actor, _, inviteID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RevokeInvite")
	if err := f.fail("RevokeInvite"); err != nil {
		return err
	}
	for i, in := range f.Invites {
		if in.ID == inviteID {
			f.Invites = append(f.Invites[:i], f.Invites[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Fake) SetMemberRole(_ context.Context, _ domain.Actor, _, userID domain.ID, role domain.MemberRole) (domain.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetMemberRole")
	if err := f.fail("SetMemberRole"); err != nil {
		return domain.Member{}, err
	}
	for i := range f.Members {
		if f.Members[i].UserID == userID {
			f.Members[i].Role = role
			return f.Members[i], nil
		}
	}
	return domain.Member{}, domain.ErrNotFound
}

func (f *Fake) RemoveMember(_ context.Context, _ domain.Actor, _, userID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RemoveMember")
	if err := f.fail("RemoveMember"); err != nil {
		return err
	}
	for i, m := range f.Members {
		if m.UserID == userID {
			f.Members = append(f.Members[:i], f.Members[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}
