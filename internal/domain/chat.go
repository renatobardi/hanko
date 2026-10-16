package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Chat (ADR-0027): conversa direta de um humano com uma persona, fora de uma task. Cada mensagem humana
// dispara uma run da persona com o gatilho "chat"; o resumo final da run vira a resposta na conversa.

// ConversationStatus é o ciclo de vida de uma conversa.
type ConversationStatus string

const (
	ConversationOpen     ConversationStatus = "open"
	ConversationArchived ConversationStatus = "archived"
)

func (s ConversationStatus) Valid() bool { return s == ConversationOpen || s == ConversationArchived }

// Conversation é uma conversa de um humano com uma persona.
type Conversation struct {
	ID            ID                 `json:"id"`
	CompanyID     ID                 `json:"company_id"`
	PersonaID     ID                 `json:"persona_id"` // zero numa sala (ADR-0033): as personas são os participantes
	Kind          ConversationKind   `json:"kind"`
	Title         string             `json:"title"`
	Status        ConversationStatus `json:"status"`
	CreatedBy     Actor              `json:"created_by"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	LastMessageAt time.Time          `json:"last_message_at"`
}

// MessageAuthor é quem escreveu uma mensagem: o humano, a persona (resposta de uma run) ou o sistema (aviso).
type MessageAuthor string

const (
	MessageHuman   MessageAuthor = "human"
	MessagePersona MessageAuthor = "persona"
	MessageSystem  MessageAuthor = "system"
)

func (a MessageAuthor) Valid() bool {
	return a == MessageHuman || a == MessagePersona || a == MessageSystem
}

// ConversationMessage é uma mensagem de uma conversa.
type ConversationMessage struct {
	ID             ID            `json:"id"`
	CompanyID      ID            `json:"company_id"`
	ConversationID ID            `json:"conversation_id"`
	Author         Actor         `json:"author"`
	Kind           MessageAuthor `json:"kind"`
	Body           string        `json:"body"`
	// RunID: na resposta da persona (ou no aviso), a run que a produziu; na mensagem humana, a run que a
	// recolheu (nil enquanto está na fila).
	RunID     *ID       `json:"run_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Sala (ADR-0033): a persona que falou (ou de quem o aviso trata), a mensagem respondida, as personas
	// mencionadas e, na mensagem que acorda personas, a entrega a cada uma (com a run que a recolheu).
	PersonaID   *ID               `json:"persona_id,omitempty"`
	PersonaName string            `json:"persona_name,omitempty"`
	ReplyToID   *ID               `json:"reply_to_id,omitempty"`
	Mentions    []ID              `json:"mentions,omitempty"`
	Deliveries  []MessageDelivery `json:"deliveries,omitempty"`
}

// MaxMessageBytes é o maior corpo de uma mensagem humana.
const MaxMessageBytes = 20000

// MaxConversationTitle é o maior título (em caracteres).
const MaxConversationTitle = 200

// IsChat diz se a run pertence a uma conversa (e não a uma task).
func (r Run) IsChat() bool { return r.Trigger == TriggerChat }

// TitleFromMessage deriva o título de uma conversa da primeira mensagem: a primeira linha não vazia, sem
// marcação de cabeçalho, cortada em ~60 caracteres.
func TitleFromMessage(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(ln), "#>*- "))
		if ln == "" {
			continue
		}
		const max = 60
		if utf8.RuneCountInString(ln) > max {
			r := []rune(ln)
			ln = strings.TrimSpace(string(r[:max-1])) + "…"
		}
		return ln
	}
	return ""
}
