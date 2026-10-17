package domain

import (
	"encoding/json"
	"time"
)

// GateKind says what a gate asks the human to decide.
type GateKind string

const (
	GateApproval   GateKind = "approval"    // irreversible action, merge, deploy, spend
	GateTaskReview GateKind = "task_review" // a persona with require_review wants to close a task
	GateHire       GateKind = "hire"        // hiring a persona
	GateMemory     GateKind = "memory"      // proposta da consolidação de memória que remove, funde ou reescreve conteúdo (ADR-0030)
	GateTool       GateKind = "tool"        // chamada de tool em modo "perguntar antes" no gateway de tools (ADR-0036)
	// GateInstruction é a proposta da própria persona para mudar as instruções dela (ADR-0052); não bloqueia.
	GateInstruction GateKind = "instruction"
	// GateQuestion é a pergunta estruturada da persona ao humano (formulário; ADR-0041): a resposta volta no wake.
	GateQuestion GateKind = "question"
	// GatePlan é o plano que a persona submete antes de executar, quando a política de plan-review vale (ADR-0041).
	GatePlan GateKind = "plan"
)

func (k GateKind) Valid() bool {
	return k == GateApproval || k == GateTaskReview || k == GateHire || k == GateMemory || k == GateTool || k == GateInstruction ||
		k == GateQuestion || k == GatePlan
}

// Blocking reports whether a pending gate of this kind holds the requesting persona
// (awaiting_gate) and blocks its task. A task review only parks the task in in_review.
func (k GateKind) Blocking() bool {
	return k == GateApproval || k == GateHire || k == GateQuestion || k == GatePlan
}

// GateStatus is the lifecycle of a gate. Only pending gates can be decided.
type GateStatus string

const (
	GatePending          GateStatus = "pending"
	GateApproved         GateStatus = "approved"
	GateRejected         GateStatus = "rejected"
	GateChangesRequested GateStatus = "changes_requested"
	GateExpired          GateStatus = "expired"
	// GateDismissed é a pergunta que um humano dispensou sem responder; reabrir a devolve a pending (ADR-0041).
	GateDismissed GateStatus = "dismissed"
)

func (s GateStatus) Valid() bool {
	switch s {
	case GatePending, GateApproved, GateRejected, GateChangesRequested, GateExpired, GateDismissed:
		return true
	}
	return false
}

// Decision is what a human may answer to a pending gate.
func (s GateStatus) Decision() bool {
	return s == GateApproved || s == GateRejected || s == GateChangesRequested
}

// ReviewVerdict é a opinião do revisor automático sobre uma entrega.
type ReviewVerdict string

const (
	ReviewApproved ReviewVerdict = "approved"
	ReviewRejected ReviewVerdict = "rejected"
)

func (v ReviewVerdict) Valid() bool { return v == ReviewApproved || v == ReviewRejected }

// GateReview é a revisão automática que antecedeu um gate task_review. Verdict vazio = a revisão
// não concluiu (Justification diz por quê). A decisão do gate continua humana.
type GateReview struct {
	Reviewer       string        `json:"reviewer"` // "Kenji (codex · gpt-5)"
	Verdict        ReviewVerdict `json:"verdict,omitempty"`
	Justification  string        `json:"justification,omitempty"`
	ArtifactSHA256 string        `json:"artifact_sha256,omitempty"` // do pacote revisado (task + entrega)
	RunID          *ID           `json:"run_id,omitempty"`
}

// Gate is a point where a human decides. Every request and decision is an activity event.
type Gate struct {
	ID           ID         `json:"id"`
	CompanyID    ID         `json:"company_id"`
	Kind         GateKind   `json:"kind"`
	Status       GateStatus `json:"status"`
	Title        string     `json:"title"`
	Body         string     `json:"body"` // markdown
	TaskID       *ID        `json:"task_id,omitempty"`
	RunID        *ID        `json:"run_id,omitempty"`
	PersonaID    *ID        `json:"persona_id,omitempty"` // who asked (and waits)
	RequestedBy  Actor      `json:"requested_by"`
	DecidedBy    *Actor     `json:"decided_by,omitempty"`
	DecisionNote string     `json:"decision_note,omitempty"`
	// RequestedStatus is, in a task_review gate, the status the persona asked for (done or
	// cancelled); approving the gate applies it.
	RequestedStatus TaskStatus `json:"requested_status,omitempty"`
	// Review está presente quando uma revisão automática antecedeu o gate (Verdict reprovado = o gate nasceu
	// marcado como reprovado pelo revisor; o humano ainda decide).
	Review *GateReview `json:"review,omitempty"`
	// Payload é o conteúdo estruturado do tipo: formulário (question), spec (hire) ou artefato do plano (plan).
	Payload json.RawMessage `json:"payload,omitempty"`
	// Answer é a resposta estruturada de uma question respondida (ADR-0041).
	Answer json.RawMessage `json:"answer,omitempty"`
	// Audience restringe quem decide (e quem vê o gate na Caixa); vazia = qualquer decisor do papel exigido.
	Audience GateAudience `json:"audience"`
	// Revision é a versão vigente do pedido (1 na criação; cada reenvio soma 1).
	Revision  int        `json:"revision"`
	EventID   string     `json:"event_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// OrgParents maps a persona to the persona it reports to (nil = top of the chart).
type OrgParents map[ID]*ID

// maxOrgDepth bounds every walk up the chart, so corrupt data cannot loop forever.
const maxOrgDepth = 1024

// WouldCycle reports whether making id report to parent closes a loop in the chart
// (parent is id itself or one of its descendants).
func (o OrgParents) WouldCycle(id, parent ID) bool {
	cur := &parent
	for i := 0; cur != nil && i < maxOrgDepth; i++ {
		if *cur == id {
			return true
		}
		cur = o[*cur]
	}
	return false
}

// IsDescendant reports whether id is below ancestor in the chart (not equal to it).
func (o OrgParents) IsDescendant(ancestor, id ID) bool {
	cur := o[id]
	for i := 0; cur != nil && i < maxOrgDepth; i++ {
		if *cur == ancestor {
			return true
		}
		cur = o[*cur]
	}
	return false
}

// CanDelegateTo reports whether a persona may create or assign work to target: itself
// or anyone below it in the chart.
func (o OrgParents) CanDelegateTo(from, target ID) bool {
	return from == target || o.IsDescendant(from, target)
}
