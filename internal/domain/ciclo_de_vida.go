package domain

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Tipos e regras puras do ADR-0057: arquivamento de empresa, merge de import, login da CLI e pedidos de entrada.

// ErrCompanyArchived: a empresa está arquivada e só aceita leitura. Vale como ErrConflict (HTTP 409).
var ErrCompanyArchived = fmt.Errorf("empresa arquivada (somente leitura): %w", ErrConflict)

// ArchiveAllowsVerb informa se um evento de atividade pode ser gravado numa empresa arquivada. Toda escrita de domínio
// grava atividade na mesma transação, então recusar o evento desfaz a escrita. Passam os eventos da própria empresa
// (arquivar, restaurar, excluir) e o fechamento de execuções em voo (run.queued e run.started não passam: nada novo
// começa numa empresa arquivada).
func ArchiveAllowsVerb(verb string) bool {
	return strings.HasPrefix(verb, "company.") || verb == "run.cancelled" || verb == "run.finished"
}

// KeyScope é o escopo de uma API key.
type KeyScope string

const (
	// KeyScopeFull é o padrão: a chave faz o que o usuário faz.
	KeyScopeFull KeyScope = "full"
	// KeyScopeRead só lê: métodos seguros da API; nunca /mcp.
	KeyScopeRead KeyScope = "read"
)

// Valid informa se o escopo é conhecido.
func (k KeyScope) Valid() bool { return k == KeyScopeFull || k == KeyScopeRead }

// Allows informa se uma chave com este escopo pode fazer a requisição. Escopo vazio vale como full (chaves antigas).
func (k KeyScope) Allows(method, path string) bool {
	if k == "" || k == KeyScopeFull {
		return true
	}
	if k != KeyScopeRead {
		return false
	}
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// CLIAuthStatus é o estado de um pedido de login da CLI.
type CLIAuthStatus string

const (
	CLIAuthPending  CLIAuthStatus = "pending"
	CLIAuthApproved CLIAuthStatus = "approved"
	CLIAuthDenied   CLIAuthStatus = "denied"
	CLIAuthConsumed CLIAuthStatus = "consumed"
)

// CLIAuthRequest é um pedido de login pelo navegador (o código e o segredo de consulta só existem em claro na criação).
type CLIAuthRequest struct {
	ID        ID            `json:"id"`
	Device    string        `json:"device"`
	Scope     KeyScope      `json:"scope"`
	Status    CLIAuthStatus `json:"status"`
	UserID    *ID           `json:"user_id,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// Expired informa se o pedido passou da validade.
func (r CLIAuthRequest) Expired(now time.Time) bool { return !now.Before(r.ExpiresAt) }

// JoinStatus é o estado de um pedido de entrada.
type JoinStatus string

const (
	JoinPending  JoinStatus = "pending"
	JoinApproved JoinStatus = "approved"
	JoinDenied   JoinStatus = "denied"
)

// JoinRequest é um pedido de entrada na empresa.
type JoinRequest struct {
	ID        ID          `json:"id"`
	CompanyID ID          `json:"company_id"`
	UserID    ID          `json:"user_id"`
	Email     string      `json:"email"`
	Name      string      `json:"name"`
	Message   string      `json:"message,omitempty"`
	Status    JoinStatus  `json:"status"`
	Role      *MemberRole `json:"role,omitempty"`
	DecidedBy *ID         `json:"decided_by,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	DecidedAt *time.Time  `json:"decided_at,omitempty"`
}

// JoinLink é o estado do link público de entrada de uma empresa (o token só existe em claro na criação).
type JoinLink struct {
	CompanyID ID        `json:"company_id"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// InstanceUser é uma conta da instalação, para a tela de administração.
type InstanceUser struct {
	User       User  `json:"user"`
	Companies  int64 `json:"companies"`
	ActiveKeys int64 `json:"active_keys"`
}

// ConflictAction é o que fazer com um item do arquivo que já existe na empresa de destino (ADR-0057).
type ConflictAction string

const (
	ConflictSkip    ConflictAction = "skip"
	ConflictRename  ConflictAction = "rename"
	ConflictReplace ConflictAction = "replace"
)

// Valid informa se a ação é conhecida.
func (a ConflictAction) Valid() bool {
	return a == ConflictSkip || a == ConflictRename || a == ConflictReplace
}

// ItemAction é a ação aplicada a um item numa prévia ou num merge: create quando não há conflito.
type ItemAction string

const (
	ItemCreate  ItemAction = "create"
	ItemSkip    ItemAction = "skip"
	ItemRename  ItemAction = "rename"
	ItemReplace ItemAction = "replace"
)

// ItemOf converte a ação de conflito na ação do item.
func (a ConflictAction) ItemOf() ItemAction {
	switch a {
	case ConflictRename:
		return ItemRename
	case ConflictReplace:
		return ItemReplace
	}
	return ItemSkip
}

// RenameTo devolve o primeiro nome livre entre base-2, base-3... (slug) ou "base (2)"... (nome livre, spaced). taken diz
// se um nome já existe. Para slugs respeita o teto de 63 caracteres cortando a base.
func RenameTo(base string, spaced bool, taken func(string) bool) string {
	for n := 2; ; n++ {
		var cand string
		if spaced {
			cand = base + " (" + strconv.Itoa(n) + ")"
		} else {
			suffix := "-" + strconv.Itoa(n)
			b := base
			if len(b)+len(suffix) > 63 {
				b = strings.TrimRight(b[:63-len(suffix)], "-")
			}
			cand = b + suffix
		}
		if !taken(cand) {
			return cand
		}
	}
}

// ParseResolution lê "kind/chave=ação" (a chave pode conter "/" ou espaços; a ação é o que vem depois do último "=").
func ParseResolution(s string) (kind, key string, act ConflictAction, err error) {
	i := strings.LastIndex(s, "=")
	if i < 0 {
		return "", "", "", Invalid("resolve", fmt.Sprintf("%q: want kind/key=action", s))
	}
	left, a := s[:i], ConflictAction(strings.TrimSpace(s[i+1:]))
	k, rest, ok := strings.Cut(left, "/")
	if !ok || k == "" || rest == "" {
		return "", "", "", Invalid("resolve", fmt.Sprintf("%q: want kind/key=action", s))
	}
	if !a.Valid() {
		return "", "", "", Invalid("resolve", fmt.Sprintf("%q: unknown action %q (want skip, rename or replace)", s, a))
	}
	return k, rest, a, nil
}
