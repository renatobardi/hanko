package domain

import (
	"regexp"
	"strings"
	"time"
)

// Sala (ADR-0033): conversa de um ou mais humanos com várias personas ao mesmo tempo. É uma conversa
// (ADR-0027) com kind "room": sem persona própria, com participantes, e cada mensagem humana acorda as
// personas que devem responder (as mencionadas, a moderadora ou todas, conforme o modo).

// ConversationKind separa o chat direto (uma persona) da sala (várias).
type ConversationKind string

const (
	ConversationDirect ConversationKind = "direct"
	ConversationRoom   ConversationKind = "room"
)

func (k ConversationKind) Valid() bool { return k == ConversationDirect || k == ConversationRoom }

// RoomMode diz quem responde a uma mensagem humana sem menção.
type RoomMode string

const (
	// RoomMentions: só responde quem foi citado com @slug; sem menção, responde a moderadora.
	RoomMentions RoomMode = "mentions"
	// RoomAll: todas as personas da sala respondem a toda mensagem humana.
	RoomAll RoomMode = "all"
)

func (m RoomMode) Valid() bool { return m == RoomMentions || m == RoomAll }

// Limites da sala.
const (
	MinRoomParticipants = 2
	MaxRoomParticipants = 12
	// MaxRoomHops é o teto configurável de saltos (persona que acorda persona) a partir de uma mensagem humana.
	MaxRoomHops     = 5
	DefaultRoomHops = 2
	// MaxRoomRunsPerMessage é o teto configurável de runs abertas a partir de uma mensagem humana.
	MaxRoomRunsPerMessage = 24
)

// RoomSettings é a configuração de uma sala (conversations.room_settings).
type RoomSettings struct {
	Mode               RoomMode `json:"mode"`
	ModeratorPersonaID *ID      `json:"moderator_persona_id,omitempty"`
	// MaxHops: quantos saltos encadeados (uma resposta que menciona outra persona) a partir da mensagem humana.
	MaxHops int `json:"max_hops"`
	// MaxRunsPerMessage: teto de runs por mensagem humana, contando os saltos; 0 = número de participantes ativos.
	MaxRunsPerMessage int `json:"max_runs_per_message"`
}

// Normalized preenche os padrões de uma configuração lida do banco.
func (s RoomSettings) Normalized() RoomSettings {
	if !s.Mode.Valid() {
		s.Mode = RoomMentions
	}
	if s.MaxHops < 1 || s.MaxHops > MaxRoomHops {
		s.MaxHops = DefaultRoomHops
	}
	if s.MaxRunsPerMessage < 0 || s.MaxRunsPerMessage > MaxRoomRunsPerMessage {
		s.MaxRunsPerMessage = 0
	}
	return s
}

// EffectiveMaxRuns é o teto de runs por mensagem humana com active participantes ativos.
func (s RoomSettings) EffectiveMaxRuns(active int) int {
	if s.MaxRunsPerMessage > 0 {
		return s.MaxRunsPerMessage
	}
	return max(active, 1)
}

// RoomParticipant é uma persona numa sala; RemovedAt marca quem saiu (pode voltar).
type RoomParticipant struct {
	PersonaID ID         `json:"persona_id"`
	AddedAt   time.Time  `json:"added_at"`
	AddedBy   Actor      `json:"added_by"`
	RemovedAt *time.Time `json:"removed_at,omitempty"`
}

// Active diz se a persona está na sala.
func (p RoomParticipant) Active() bool { return p.RemovedAt == nil }

// MessageDelivery é a entrega de uma mensagem da sala a uma persona: RunID é a run que a recolheu (nil enquanto
// espera a persona terminar a run em andamento).
type MessageDelivery struct {
	PersonaID ID  `json:"persona_id"`
	RunID     *ID `json:"run_id,omitempty"`
}

// mentionRe acha @slug no começo, depois de espaço ou pontuação (não dentro de e-mail ou de outra palavra).
var mentionRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_.@/-])@([a-z0-9][a-z0-9-]{0,62})`)

// ParseMentions devolve os slugs citados com @ no texto, em minúsculas, na ordem e sem repetição. Trechos de
// código (entre crases) não contam.
func ParseMentions(body string) []string {
	body = stripCode(body)
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(body, -1) {
		slug := strings.TrimRight(strings.ToLower(m[1]), "-")
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	return out
}

// stripCode troca blocos e trechos de código por espaço, para uma menção num exemplo de código não acordar ninguém.
func stripCode(s string) string {
	var b strings.Builder
	parts := strings.Split(s, "```")
	for i, p := range parts {
		if i%2 == 1 {
			b.WriteByte(' ')
			continue
		}
		inline := strings.Split(p, "`")
		for j, q := range inline {
			if j%2 == 1 {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(q)
		}
	}
	return b.String()
}
