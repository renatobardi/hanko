package domain

import (
	"strings"
	"time"
)

// AuthProvider identifica de onde vem a identidade de um usuário (ADR-0007, ADR-0014).
type AuthProvider string

const (
	ProviderLocal    AuthProvider = "local"
	ProviderFirebase AuthProvider = "firebase"
)

// UserRole é o papel do usuário. No v0.1 todos podem tudo; o papel só registra o primeiro dono.
type UserRole string

const (
	RoleOwner  UserRole = "owner"
	RoleAdmin  UserRole = "admin"
	RoleMember UserRole = "member"
)

func (r UserRole) Valid() bool { return r == RoleOwner || r == RoleAdmin || r == RoleMember }

// User é um humano autenticado. PasswordHash nunca sai do servidor.
type User struct {
	ID          ID           `json:"id"`
	Email       string       `json:"email"`
	Name        string       `json:"name"`
	Provider    AuthProvider `json:"provider"`
	ExternalID  string       `json:"external_id,omitempty"`
	Role        UserRole     `json:"role"`
	CreatedAt   time.Time    `json:"created_at"`
	LastLoginAt *time.Time   `json:"last_login_at,omitempty"`

	PasswordHash string `json:"-"`
}

// DisplayName é o nome para a UI: o nome, ou a parte local do e-mail.
func (u User) DisplayName() string {
	if n := strings.TrimSpace(u.Name); n != "" {
		return n
	}
	local, _, _ := strings.Cut(u.Email, "@")
	return local
}

// Actor é o ator humano que carrega o ID do usuário.
func (u User) Actor() Actor {
	id := u.ID
	return Actor{Type: ActorHuman, ID: &id}
}

// Session é uma sessão de navegador. O token em si nunca é guardado, só o hash.
type Session struct {
	ID         ID        `json:"id"`
	UserID     ID        `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	UserAgent  string    `json:"user_agent,omitempty"`
	IP         string    `json:"ip,omitempty"`
}

// APIKey é uma credencial de longa duração de um usuário (Bearer hk_…). Só o hash é guardado.
type APIKey struct {
	ID         ID         `json:"id"`
	UserID     ID         `json:"user_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Active informa se a chave ainda vale.
func (k APIKey) Active() bool { return k.RevokedAt == nil }
