package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
)

// InvitePathPrefix é o caminho público de um convite; o token vem depois.
const InvitePathPrefix = auth.InvitePath

// Member é um usuário numa empresa, com o papel dele (ADR-0021).
type Member struct {
	UserID    string    `json:"user_id" format:"uuid"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role" enum:"owner,admin,member,viewer" doc:"owner gerencia membros; admin gerencia personas, orçamentos, assinaturas e fluxos; member opera tasks, comentários e gates; viewer só lê."`
	CreatedAt time.Time `json:"created_at"`
}

// Invite é um convite de empresa. O token só aparece na resposta da criação.
type Invite struct {
	ID        string    `json:"id" format:"uuid"`
	Email     string    `json:"email"`
	Role      string    `json:"role" enum:"owner,admin,member,viewer"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Expired   bool      `json:"expired"`
	// Token e Path só vêm na criação: o servidor guarda apenas o hash, então o link não pode ser recuperado depois.
	Token string `json:"token,omitempty" doc:"Token do convite (só na criação)."`
	Path  string `json:"path,omitempty" doc:"Caminho do link de aceite, /convite/<token> (só na criação)."`
}

func memberOut(m domain.Member) Member {
	return Member{UserID: idStr(m.UserID), Email: m.Email, Name: m.Name, Role: string(m.Role), CreatedAt: m.CreatedAt}
}

func (s *server) inviteOut(i domain.Invite) Invite {
	return Invite{ID: idStr(i.ID), Email: i.Email, Role: string(i.Role), CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt,
		Expired: !time.Now().Before(i.ExpiresAt)}
}

type InvitePath struct {
	CompanyPath
	Invite string `path:"invite" format:"uuid" doc:"Id do convite."`
}

type MemberPath struct {
	CompanyPath
	Member string `path:"member" doc:"Id ou e-mail do membro." example:"ana@exemplo.com"`
}

type inviteIn struct {
	CompanyPath
	Body struct {
		Email string `json:"email" minLength:"3" maxLength:"254" example:"ana@exemplo.com"`
		Role  string `json:"role" enum:"owner,admin,member,viewer" example:"member"`
	}
}

type setRoleIn struct {
	MemberPath
	Body struct {
		Role string `json:"role" enum:"owner,admin,member,viewer"`
	}
}

func (s *server) registerMembers(api huma.API) {
	huma.Register(api, op("list-members", http.MethodGet, "/companies/{company}/members", "members", "Lista os membros da empresa"),
		func(ctx context.Context, in *listCompanyIn) (*ListOut[Member], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ms, err := s.svc.ListMembers(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(ms, in.Page, memberOut), nil
		})

	huma.Register(api, op("list-invites", http.MethodGet, "/companies/{company}/invites", "members", "Lista os convites abertos (admin ou owner)"),
		func(ctx context.Context, in *listCompanyIn) (*ListOut[Invite], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			is, err := s.svc.ListInvites(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(is, in.Page, s.inviteOut), nil
		})

	huma.Register(api, created(op("invite-member", http.MethodPost, "/companies/{company}/invites", "members",
		"Convida alguém por e-mail; devolve o link uma única vez (não há envio de e-mail)")),
		func(ctx context.Context, in *inviteIn) (*ItemOut[Invite], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			inv, tok, err := s.svc.InviteMember(ctx, sc.Actor, sc.Company.ID, in.Body.Email, domain.MemberRole(in.Body.Role))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := s.inviteOut(inv)
			out.Token, out.Path = tok, InvitePathPrefix+tok
			return &ItemOut[Invite]{Body: out}, nil
		})

	huma.Register(api, func() huma.Operation {
		o := op("revoke-invite", http.MethodDelete, "/companies/{company}/invites/{invite}", "members", "Revoga um convite aberto")
		o.DefaultStatus = http.StatusNoContent
		return o
	}(), func(ctx context.Context, in *InvitePath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		id, err := domain.ParseID(in.Invite)
		if err != nil {
			return nil, s.fail(ctx, domain.ErrNotFound)
		}
		if err := s.svc.RevokeInvite(ctx, sc.Actor, sc.Company.ID, id); err != nil {
			return nil, s.fail(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(api, op("set-member-role", http.MethodPatch, "/companies/{company}/members/{member}", "members", "Troca o papel de um membro"),
		func(ctx context.Context, in *setRoleIn) (*ItemOut[Member], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			target, err := s.memberRef(ctx, sc.Company.ID, in.Member)
			if err != nil {
				return nil, err
			}
			m, err := s.svc.SetMemberRole(ctx, sc.Actor, sc.Company.ID, target, domain.MemberRole(in.Body.Role))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Member]{Body: memberOut(m)}, nil
		})

	huma.Register(api, func() huma.Operation {
		o := op("remove-member", http.MethodDelete, "/companies/{company}/members/{member}", "members", "Remove um membro (ou sai da empresa)")
		o.DefaultStatus = http.StatusNoContent
		return o
	}(), func(ctx context.Context, in *MemberPath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		target, err := s.memberRef(ctx, sc.Company.ID, in.Member)
		if err != nil {
			return nil, err
		}
		if err := s.svc.RemoveMember(ctx, sc.Actor, sc.Company.ID, target); err != nil {
			return nil, s.fail(ctx, err)
		}
		return &struct{}{}, nil
	})
}

// memberRef resolve id ou e-mail de um membro da empresa.
func (s *server) memberRef(ctx context.Context, cid domain.ID, ref string) (domain.ID, error) {
	ms, err := s.svc.ListMembers(ctx, cid)
	if err != nil {
		return domain.ID{}, s.fail(ctx, err)
	}
	for _, m := range ms {
		if m.UserID.String() == ref || strings.EqualFold(m.Email, ref) {
			return m.UserID, nil
		}
	}
	return domain.ID{}, s.fail(ctx, domain.ErrNotFound)
}
