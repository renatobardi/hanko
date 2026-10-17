package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
	"unicode/utf8"
)

// Revisões append-only (ADR-0052). Este é o formato comum: um assunto (instruções de uma persona, uma skill, um fluxo,
// um gate...) ganha uma revisão a cada mudança e ela nunca é alterada. Quem escreveu o texto (Author) e quem o aplicou
// (Actor) podem ser diferentes: uma persona propõe, um humano aprova.

// MaxRevisionReason é o tamanho máximo (em caracteres) do motivo de uma revisão.
const MaxRevisionReason = 500

// RevisionSHA é o sha256 (hex, minúsculo) do texto em UTF-8; o mesmo cálculo da migration de backfill.
func RevisionSHA(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

// ValidRevisionReason diz se o motivo cabe (até MaxRevisionReason caracteres, UTF-8 válido).
func ValidRevisionReason(reason string) bool {
	return utf8.ValidString(reason) && utf8.RuneCountInString(reason) <= MaxRevisionReason
}

// RevisionOrigin diz de onde veio uma revisão das instruções.
type RevisionOrigin string

const (
	RevisionBackfill RevisionOrigin = "backfill" // o texto que já existia quando as revisões nasceram
	RevisionHire     RevisionOrigin = "hire"     // a contratação (revisão 1)
	RevisionEdit     RevisionOrigin = "edit"     // um humano editou
	RevisionRestore  RevisionOrigin = "restore"  // um humano voltou a uma revisão antiga (cria uma nova)
	RevisionProposal RevisionOrigin = "proposal" // a persona propôs e um humano aprovou o gate
	RevisionImport   RevisionOrigin = "import"   // importação de empresa
)

// Valid diz se é uma origem conhecida.
func (o RevisionOrigin) Valid() bool {
	switch o {
	case RevisionBackfill, RevisionHire, RevisionEdit, RevisionRestore, RevisionProposal, RevisionImport:
		return true
	}
	return false
}

// RevisionMeta são os metadados comuns de qualquer revisão append-only.
type RevisionMeta struct {
	Revision  int       `json:"revision"`
	SHA256    string    `json:"sha256"`
	Author    Actor     `json:"author"`
	Actor     Actor     `json:"actor"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// InstructionRevision é uma revisão das instruções de uma persona. Body vem vazio nas listagens (BodyBytes diz o tamanho).
type InstructionRevision struct {
	ID        ID `json:"id"`
	CompanyID ID `json:"company_id"`
	PersonaID ID `json:"persona_id"`
	RevisionMeta
	Origin       RevisionOrigin `json:"origin"`
	RestoredFrom *int           `json:"restored_from,omitempty"`
	GateID       *ID            `json:"gate_id,omitempty"`
	BodyBytes    int            `json:"body_bytes"`
	Body         string         `json:"body,omitempty"`
}
