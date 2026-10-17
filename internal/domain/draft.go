package domain

import "time"

// Rascunhos no servidor (ADR-0052): o texto começado de um comentário de task ou de uma mensagem de conversa.

// MaxDraftRunes é o maior rascunho (o mesmo teto de um comentário ou de uma mensagem).
const MaxDraftRunes = 100000

// DraftTarget diz onde o rascunho será enviado.
type DraftTarget string

const (
	DraftTask         DraftTarget = "task"         // comentário de uma task
	DraftConversation DraftTarget = "conversation" // mensagem de uma conversa (chat ou sala)
)

// Valid diz se t é um alvo conhecido.
func (t DraftTarget) Valid() bool { return t == DraftTask || t == DraftConversation }

// Draft é o rascunho de um usuário para um alvo. Body vazio e UpdatedAt zero = não há rascunho.
type Draft struct {
	CompanyID  ID          `json:"company_id"`
	TargetType DraftTarget `json:"target_type"`
	TargetID   ID          `json:"target_id"`
	Body       string      `json:"body"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// Exists diz se há um rascunho gravado.
func (d Draft) Exists() bool { return !d.UpdatedAt.IsZero() }
