package domain

import (
	"errors"
	"fmt"
	"time"
)

// MemberRole é o papel de um usuário numa empresa (ADR-0021).
type MemberRole string

const (
	MemberOwner  MemberRole = "owner"
	MemberAdmin  MemberRole = "admin"
	MemberMember MemberRole = "member"
	MemberViewer MemberRole = "viewer"
)

// Valid informa se o papel é um dos quatro.
func (r MemberRole) Valid() bool { return r.Rank() > 0 }

// Rank ordena os papéis: viewer 1 < member 2 < admin 3 < owner 4; inválido 0.
func (r MemberRole) Rank() int {
	switch r {
	case MemberViewer:
		return 1
	case MemberMember:
		return 2
	case MemberAdmin:
		return 3
	case MemberOwner:
		return 4
	}
	return 0
}

// AtLeast informa se r tem pelo menos o poder de min.
func (r MemberRole) AtLeast(min MemberRole) bool { return r.Rank() >= min.Rank() && r.Rank() > 0 }

// Manages informa se quem tem o papel r pode conceder, mudar ou tirar o papel target: o owner mexe em
// qualquer um; o admin só em member e viewer; os demais em ninguém.
func (r MemberRole) Manages(target MemberRole) bool {
	switch r {
	case MemberOwner:
		return target.Valid()
	case MemberAdmin:
		return target == MemberMember || target == MemberViewer
	}
	return false
}

// ErrForbidden: o usuário é membro da empresa mas o papel não permite a ação. Vale como
// ErrNotAllowed (HTTP 403). Quem não é membro recebe ErrNotFound, nunca este erro.
var ErrForbidden = fmt.Errorf("papel insuficiente nesta empresa: %w", ErrNotAllowed)

// ErrInviteInvalid: convite desconhecido, expirado, revogado, já aceito ou para outro e-mail.
// Uma só resposta para todos os casos, para não revelar qual deles.
var ErrInviteInvalid = errors.New("convite inválido ou expirado")

// Member é um usuário numa empresa.
type Member struct {
	CompanyID ID           `json:"company_id"`
	UserID    ID           `json:"user_id"`
	Email     string       `json:"email"`
	Name      string       `json:"name"`
	Provider  AuthProvider `json:"provider"`
	Role      MemberRole   `json:"role"`
	InvitedBy *ID          `json:"invited_by,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

// Invite é um convite aberto ou encerrado. O token em claro só existe na criação.
type Invite struct {
	ID         ID         `json:"id"`
	CompanyID  ID         `json:"company_id"`
	Email      string     `json:"email"`
	Role       MemberRole `json:"role"`
	InvitedBy  *ID        `json:"invited_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Expired informa se o prazo já passou em now.
func (i Invite) Expired(now time.Time) bool { return !now.Before(i.ExpiresAt) }
