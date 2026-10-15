package service

import (
	"context"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Autorização por empresa (ADR-0021). O service é a única barreira: API, UI, CLI e MCP passam por aqui.
//
// Quem chama:
//   - com auth.Identity no contexto (sessão ou API key de um usuário): vale o papel dele na empresa;
//   - sem identidade (modo trusted, heartbeat, persona com run token): sem restrição de papel — a
//     persona já é limitada à empresa do token pela API e pelo MCP.
//
// O dono da instalação (users.role = owner) é owner de todas as empresas. Quem não é membro recebe
// ErrNotFound (a empresa "não existe" para ele); quem é membro com papel insuficiente, ErrForbidden.

// callerRole devolve o papel do chamador na empresa. restricted é false quando não há usuário no
// contexto. Não-membro = ErrNotFound.
func (s *Service) callerRole(ctx context.Context, companyID domain.ID) (role domain.MemberRole, restricted bool, err error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return domain.MemberOwner, false, nil
	}
	role, err = roleOf(ctx, s.read(), id.User, companyID)
	return role, true, err
}

func roleOf(ctx context.Context, q *store.Queries, u domain.User, companyID domain.ID) (domain.MemberRole, error) {
	if u.Role == domain.RoleOwner {
		return domain.MemberOwner, nil
	}
	r, err := q.GetMemberRole(ctx, store.GetMemberRoleParams{CompanyID: companyID, UserID: u.ID})
	if err != nil {
		if isNoRows(err) {
			return "", notFound("company")
		}
		return "", err
	}
	return domain.MemberRole(r), nil
}

// require exige que o chamador tenha pelo menos o papel min na empresa.
func (s *Service) require(ctx context.Context, companyID domain.ID, min domain.MemberRole) error {
	role, restricted, err := s.callerRole(ctx, companyID)
	if err != nil {
		return err
	}
	if restricted && !role.AtLeast(min) {
		return domain.ErrForbidden
	}
	return nil
}
