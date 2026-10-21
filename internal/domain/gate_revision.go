package domain

import (
	"encoding/json"
	"time"
)

// GateRevision é uma versão anterior de um gate reenviado (ADR-0041). Append-only: o que o humano já viu não muda.
type GateRevision struct {
	ID           ID              `json:"id"`
	GateID       ID              `json:"gate_id"`
	Revision     int             `json:"revision"`
	Title        string          `json:"title"`
	Body         string          `json:"body"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	DecisionNote string          `json:"decision_note,omitempty"` // o que o humano pediu quando devolveu esta versão
	SHA256       string          `json:"sha256"`
	CreatedAt    time.Time       `json:"created_at"`
}
