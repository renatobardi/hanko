package domain

import "time"

// Segredos como recurso (ADR-0050). O valor nunca faz parte destes tipos: ele só existe cifrado no banco e, durante
// uma run, em memória.

// SecretScope é o dono do segredo: a empresa (admins gerenciam) ou um usuário (só ele vê e usa).
type SecretScope string

const (
	SecretScopeCompany SecretScope = "company"
	SecretScopeUser    SecretScope = "user"
)

// Valid informa se o escopo é conhecido.
func (s SecretScope) Valid() bool { return s == SecretScopeCompany || s == SecretScopeUser }

// SecretProvider diz onde o valor mora: cifrado no Hanko (local) ou num cofre externo (vault), lido no despacho.
type SecretProvider string

const (
	SecretProviderLocal SecretProvider = "local"
	SecretProviderVault SecretProvider = "vault"
)

// Valid informa se o provider é conhecido.
func (p SecretProvider) Valid() bool { return p == SecretProviderLocal || p == SecretProviderVault }

// Secret são os metadados de um segredo.
type Secret struct {
	ID          ID             `json:"id"`
	CompanyID   ID             `json:"company_id"`
	Scope       SecretScope    `json:"scope"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Provider    SecretProvider `json:"provider"`
	// ExternalRef é o caminho no cofre (provider vault); não é o valor.
	ExternalRef string `json:"external_ref,omitempty"`
	// Version sobe a cada rotação.
	Version int `json:"version"`
	// RevealedAt: quando o valor desta versão foi mostrado (criação com valor gerado, ou "revelar"); nulo = ainda não.
	RevealedAt *time.Time `json:"revealed_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedBy  Actor      `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// SecretUse é um lugar que referencia o segredo.
type SecretUse struct {
	Kind string `json:"kind"` // persona | integration | decision | flow_trigger | channel
	Slug string `json:"slug"`
	Name string `json:"name"`
}
