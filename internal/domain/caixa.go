package domain

import (
	"strings"
	"time"
)

// Caixa por pessoa, notificações e visões "Minhas" (ADR-0042).

// InboxItemKind é o tipo de um item da Caixa de entrada. O item em si é derivado do estado da empresa; o que é da
// pessoa (lido, arquivado) fica em inbox_state, chaveado por (tipo, id da origem).
type InboxItemKind string

const (
	InboxGate         InboxItemKind = "gate"
	InboxTaskBlocked  InboxItemKind = "task_blocked"
	InboxTaskHeld     InboxItemKind = "task_held"
	InboxRunFailed    InboxItemKind = "run_failed"
	InboxPersonaError InboxItemKind = "persona_error"
	InboxAlert        InboxItemKind = "alert"
	InboxSubscription InboxItemKind = "subscription"
	InboxMention      InboxItemKind = "mention"
	InboxGoalAchieved InboxItemKind = "goal_achieved"
	// InboxRoundQuestion e InboxExternalApproval são os itens da rodada e da aprovação externas (ADR-0080 item 6; ADR-0081 item 6).
	InboxRoundQuestion    InboxItemKind = "round_question"
	InboxExternalApproval InboxItemKind = "external_approval"
	// InboxExternalAgent e InboxExternalHost são os agentes e hosts externos novos (ADR-0075 item 2; ADR-0077). Informativos.
	InboxExternalAgent InboxItemKind = "external_agent"
	InboxExternalHost  InboxItemKind = "external_host"
)

// InboxItemKinds lista os tipos na ordem em que a API os documenta.
var InboxItemKinds = []InboxItemKind{InboxGate, InboxTaskBlocked, InboxTaskHeld, InboxRunFailed, InboxPersonaError,
	InboxAlert, InboxSubscription, InboxMention, InboxGoalAchieved, InboxRoundQuestion, InboxExternalApproval,
	InboxExternalAgent, InboxExternalHost}

// Valid diz se k é um tipo conhecido.
func (k InboxItemKind) Valid() bool {
	for _, v := range InboxItemKinds {
		if v == k {
			return true
		}
	}
	return false
}

// InboxView escolhe quais itens a Caixa devolve.
type InboxView string

const (
	// InboxViewAll: tudo que não está arquivado (o padrão).
	InboxViewAll InboxView = "all"
	// InboxViewUnread: não arquivados e não lidos.
	InboxViewUnread InboxView = "unread"
	// InboxViewArchived: só os arquivados.
	InboxViewArchived InboxView = "archived"
)

// Valid diz se v é uma visão conhecida.
func (v InboxView) Valid() bool {
	return v == InboxViewAll || v == InboxViewUnread || v == InboxViewArchived
}

// InboxAction é uma mudança de estado feita pela pessoa.
type InboxAction string

const (
	InboxRead      InboxAction = "read"
	InboxUnread    InboxAction = "unread"
	InboxArchive   InboxAction = "archive"
	InboxUnarchive InboxAction = "unarchive"
	InboxReadAll   InboxAction = "read_all"
)

// Valid diz se a é uma ação conhecida.
func (a InboxAction) Valid() bool {
	switch a {
	case InboxRead, InboxUnread, InboxArchive, InboxUnarchive, InboxReadAll:
		return true
	}
	return false
}

// InboxItemState é o estado de um item para a pessoa, já comparado com a impressão atual do item.
type InboxItemState struct {
	Read     bool
	Archived bool
}

// EffectiveInboxState aplica a regra "reaparece se muda": se a impressão guardada difere da atual, o item volta como
// novo (não lido, não arquivado), seja qual for o estado guardado.
func EffectiveInboxState(storedFingerprint, currentFingerprint string, read, archived bool) InboxItemState {
	if storedFingerprint != currentFingerprint {
		return InboxItemState{}
	}
	return InboxItemState{Read: read || archived, Archived: archived}
}

// InboxVisible diz se um item com o estado st aparece na visão v.
func InboxVisible(v InboxView, st InboxItemState) bool {
	switch v {
	case InboxViewArchived:
		return st.Archived
	case InboxViewUnread:
		return !st.Archived && !st.Read
	default:
		return !st.Archived
	}
}

// NotifyKind é o tipo de aviso que a pessoa pode silenciar.
type NotifyKind string

const (
	NotifyMention   NotifyKind = "mention"
	NotifyGate      NotifyKind = "gate"
	NotifyRunFailed NotifyKind = "run_failed"
	NotifyAlert     NotifyKind = "alert"
)

// NotifyKinds lista os tipos de aviso na ordem da tela de preferências.
var NotifyKinds = []NotifyKind{NotifyMention, NotifyGate, NotifyRunFailed, NotifyAlert}

// Valid diz se k é um tipo de aviso conhecido.
func (k NotifyKind) Valid() bool {
	for _, v := range NotifyKinds {
		if v == k {
			return true
		}
	}
	return false
}

// NotifyPref diz por quais canais a pessoa quer receber um tipo de aviso.
type NotifyPref struct {
	Kind  NotifyKind `json:"kind"`
	Toast bool       `json:"toast"`
	Push  bool       `json:"push"`
}

// DefaultNotifyPref é o padrão de um tipo quando a pessoa não mudou nada: toast em todos; push só no que pede ação
// de uma pessoa (menção e gate).
func DefaultNotifyPref(k NotifyKind) NotifyPref {
	return NotifyPref{Kind: k, Toast: true, Push: k == NotifyMention || k == NotifyGate}
}

// MaxMentionsPerComment é o teto de alvos distintos de menção num comentário (o excedente é ignorado).
const MaxMentionsPerComment = 10

// MentionHandle é como um membro é citado: a parte local do e-mail em minúsculas, com tudo fora de [a-z0-9]
// virando "-" (sem hífens nas pontas nem repetidos). "Renato.Bardi@outlook.com" → "renato-bardi". Vazio se não sobra nada.
func MentionHandle(email string) string {
	local, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	var b strings.Builder
	dash := false
	for _, r := range local {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	h := b.String()
	if len(h) > 63 {
		h = strings.TrimRight(h[:63], "-")
	}
	return h
}

// MentionTarget é o alvo resolvido de uma menção.
type MentionTarget struct {
	Handle string
	// PersonaID ou UserID, um dos dois.
	PersonaID *ID
	UserID    *ID
}

// MentionCandidate é uma pessoa ou persona que pode ser citada.
type MentionCandidate struct {
	ID     ID
	Handle string
}

// ResolveMentions casa os handles citados no texto com personas (por slug, têm prioridade) e membros (por
// MentionHandle). Um handle de membro que casa com mais de um membro é ambíguo: não menciona ninguém e volta em
// ambiguous. O autor (selfUser) nunca é alvo. No máximo MaxMentionsPerComment alvos.
func ResolveMentions(handles []string, personas, members []MentionCandidate, selfUser *ID) (targets []MentionTarget, ambiguous []string) {
	bySlug := map[string]ID{}
	for _, p := range personas {
		bySlug[strings.ToLower(p.Handle)] = p.ID
	}
	byHandle := map[string][]ID{}
	for _, m := range members {
		if m.Handle != "" {
			byHandle[m.Handle] = append(byHandle[m.Handle], m.ID)
		}
	}
	seen := map[ID]bool{}
	for _, h := range handles {
		if len(targets) >= MaxMentionsPerComment {
			break
		}
		h = strings.ToLower(h)
		if pid, ok := bySlug[h]; ok {
			if !seen[pid] {
				seen[pid] = true
				id := pid
				targets = append(targets, MentionTarget{Handle: h, PersonaID: &id})
			}
			continue
		}
		ids := byHandle[h]
		switch {
		case len(ids) == 1:
			uid := ids[0]
			if (selfUser != nil && uid == *selfUser) || seen[uid] {
				continue
			}
			seen[uid] = true
			targets = append(targets, MentionTarget{Handle: h, UserID: &uid})
		case len(ids) > 1:
			ambiguous = append(ambiguous, h)
		}
	}
	return targets, ambiguous
}

// Participation diz por que uma task é "minha" para uma pessoa (ADR-0042 §B). Mais de um motivo pode valer.
type Participation struct {
	Created   bool `json:"created"`
	Assigned  bool `json:"assigned"`
	Mentioned bool `json:"mentioned"`
	Commented bool `json:"commented"`
	Gate      bool `json:"gate"`
}

// Any diz se há algum motivo.
func (p Participation) Any() bool {
	return p.Created || p.Assigned || p.Mentioned || p.Commented || p.Gate
}

// Reasons devolve os motivos como chaves estáveis (created, assigned, mentioned, commented, gate).
func (p Participation) Reasons() []string {
	var out []string
	if p.Created {
		out = append(out, "created")
	}
	if p.Assigned {
		out = append(out, "assigned")
	}
	if p.Mentioned {
		out = append(out, "mentioned")
	}
	if p.Commented {
		out = append(out, "commented")
	}
	if p.Gate {
		out = append(out, "gate")
	}
	return out
}

// Mention é uma menção a uma pessoa num comentário.
type Mention struct {
	ID        ID        `json:"id"`
	CommentID ID        `json:"comment_id"`
	TaskID    ID        `json:"task_id"`
	UserID    *ID       `json:"user_id,omitempty"`
	PersonaID *ID       `json:"persona_id,omitempty"`
	Author    Actor     `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}
