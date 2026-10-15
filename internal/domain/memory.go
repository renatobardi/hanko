package domain

import "time"

// Consolidação de memória governada pelo Hanko (ADR-0030). A curadoria roda como uma run de sistema
// (gatilho memory) de uma persona da empresa; o que ela remove, funde ou reescreve vira proposta e só
// é aplicado no ai-memory depois da decisão de um humano.

// Limites do intervalo de consolidação, em horas.
const (
	MemoryMinIntervalHours     = 1
	MemoryMaxIntervalHours     = 24 * 30
	MemoryDefaultIntervalHours = 24
)

// MemorySettings é a configuração da consolidação de uma empresa. Desligada por padrão.
type MemorySettings struct {
	CompanyID        ID         `json:"company_id"`
	Enabled          bool       `json:"enabled"`
	IntervalHours    int        `json:"interval_hours"`
	CuratorPersonaID *ID        `json:"curator_persona_id,omitempty"`
	LastFiredAt      *time.Time `json:"last_fired_at,omitempty"`
	NextFireAt       *time.Time `json:"next_fire_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// MemoryOrigin diz quem disparou uma consolidação.
type MemoryOrigin string

const (
	MemoryScheduled MemoryOrigin = "scheduled"
	MemoryManual    MemoryOrigin = "manual"
)

// MemoryConsolidationStatus é o estado de uma consolidação (a run tem o dela).
type MemoryConsolidationStatus string

const (
	ConsolidationOpen      MemoryConsolidationStatus = "open"      // task criada; a run ainda não terminou
	ConsolidationDone      MemoryConsolidationStatus = "done"      // a curadoria terminou
	ConsolidationFailed    MemoryConsolidationStatus = "failed"    // a run falhou ou expirou
	ConsolidationCancelled MemoryConsolidationStatus = "cancelled" // a run foi cancelada ou interrompida
	ConsolidationExpired   MemoryConsolidationStatus = "expired"   // nunca chegou a rodar (orçamento, curador parado)
)

// MemoryConsolidation é uma rodada de curadoria de um workspace.
type MemoryConsolidation struct {
	ID            ID                        `json:"id"`
	CompanyID     ID                        `json:"company_id"`
	WorkspaceID   *ID                       `json:"workspace_id,omitempty"`
	WorkspaceSlug string                    `json:"workspace_slug"`
	TaskID        *ID                       `json:"task_id,omitempty"`
	RunID         *ID                       `json:"run_id,omitempty"`
	Origin        MemoryOrigin              `json:"origin"`
	ScheduledFor  time.Time                 `json:"scheduled_for"`
	Status        MemoryConsolidationStatus `json:"status"`
	Summary       string                    `json:"summary,omitempty"`
	CreatedAt     time.Time                 `json:"created_at"`
	FinishedAt    *time.Time                `json:"finished_at,omitempty"`
}

// MemoryProposalKind classifica o que a escrita proposta faz com o conteúdo existente.
type MemoryProposalKind string

const (
	ProposalMerge   MemoryProposalKind = "merge"
	ProposalDelete  MemoryProposalKind = "delete"
	ProposalRewrite MemoryProposalKind = "rewrite"
)

func (k MemoryProposalKind) Valid() bool {
	return k == ProposalMerge || k == ProposalDelete || k == ProposalRewrite
}

// MemoryProposalStatus é o ciclo da proposta.
type MemoryProposalStatus string

const (
	ProposalPending  MemoryProposalStatus = "pending"  // esperando a decisão humana (gate memory)
	ProposalApproved MemoryProposalStatus = "approved" // aprovada; a aplicação está na fila
	ProposalRejected MemoryProposalStatus = "rejected" // descartada
	ProposalApplied  MemoryProposalStatus = "applied"  // aplicada no ai-memory
	ProposalFailed   MemoryProposalStatus = "failed"   // a aplicação falhou; nova tentativa agendada (ou manual)
)

func (s MemoryProposalStatus) Valid() bool {
	switch s {
	case ProposalPending, ProposalApproved, ProposalRejected, ProposalApplied, ProposalFailed:
		return true
	}
	return false
}

// MemoryProposal é uma escrita da curadoria retida para decisão humana. Tool e Arguments são a chamada
// exata que o Hanko repete no ai-memory quando a proposta é aprovada.
type MemoryProposal struct {
	ID            ID                   `json:"id"`
	CompanyID     ID                   `json:"company_id"`
	WorkspaceID   *ID                  `json:"workspace_id,omitempty"`
	WorkspaceSlug string               `json:"workspace_slug"`
	RunID         *ID                  `json:"run_id,omitempty"`
	GateID        *ID                  `json:"gate_id,omitempty"`
	Kind          MemoryProposalKind   `json:"kind"`
	Status        MemoryProposalStatus `json:"status"`
	Tool          string               `json:"tool"`
	Targets       []string             `json:"targets"`
	Proposed      string               `json:"proposed,omitempty"`
	Current       string               `json:"current,omitempty"`
	Diff          string               `json:"diff,omitempty"`
	SHA256        string               `json:"sha256"`
	Attempts      int                  `json:"attempts"`
	LastError     string               `json:"last_error,omitempty"`
	NextAttemptAt *time.Time           `json:"next_attempt_at,omitempty"`
	DecidedBy     *Actor               `json:"decided_by,omitempty"`
	DecisionNote  string               `json:"decision_note,omitempty"`
	DecidedAt     *time.Time           `json:"decided_at,omitempty"`
	AppliedAt     *time.Time           `json:"applied_at,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
}

// MemoryAccess é o nível de acesso de uma persona à memória de um workspace além do que o próprio
// escopo da run já dá.
type MemoryAccess string

const (
	MemoryRead  MemoryAccess = "read"
	MemoryWrite MemoryAccess = "write"
)

func (a MemoryAccess) Valid() bool { return a == MemoryRead || a == MemoryWrite }

// Allows diz se o nível a cobre o que se quer fazer (write cobre read).
func (a MemoryAccess) Allows(want MemoryAccess) bool {
	return a == MemoryWrite || (a == MemoryRead && want == MemoryRead)
}

// MemoryGrant libera uma persona a ler (ou ler e escrever) a memória de outro workspace.
type MemoryGrant struct {
	CompanyID     ID           `json:"company_id"`
	PersonaID     ID           `json:"persona_id"`
	WorkspaceID   ID           `json:"workspace_id"`
	Access        MemoryAccess `json:"access"`
	PersonaName   string       `json:"persona_name,omitempty"`
	WorkspaceSlug string       `json:"workspace_slug,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
}
