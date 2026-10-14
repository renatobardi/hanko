package domain

import "time"

// GateKind says what a gate asks the human to decide.
type GateKind string

const (
	GateApproval   GateKind = "approval"    // irreversible action, merge, deploy, spend
	GateTaskReview GateKind = "task_review" // a persona with require_review wants to close a task
	GateHire       GateKind = "hire"        // hiring a persona
)

func (k GateKind) Valid() bool { return k == GateApproval || k == GateTaskReview || k == GateHire }

// Blocking reports whether a pending gate of this kind holds the requesting persona
// (awaiting_gate) and blocks its task. A task review only parks the task in in_review.
func (k GateKind) Blocking() bool { return k == GateApproval || k == GateHire }

// GateStatus is the lifecycle of a gate. Only pending gates can be decided.
type GateStatus string

const (
	GatePending          GateStatus = "pending"
	GateApproved         GateStatus = "approved"
	GateRejected         GateStatus = "rejected"
	GateChangesRequested GateStatus = "changes_requested"
	GateExpired          GateStatus = "expired"
)

func (s GateStatus) Valid() bool {
	switch s {
	case GatePending, GateApproved, GateRejected, GateChangesRequested, GateExpired:
		return true
	}
	return false
}

// Decision is what a human may answer to a pending gate.
func (s GateStatus) Decision() bool {
	return s == GateApproved || s == GateRejected || s == GateChangesRequested
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
	EventID         string     `json:"event_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
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
