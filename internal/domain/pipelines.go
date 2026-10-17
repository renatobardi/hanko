package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Pipelines, cases, fila de revisão, aprendizados e exemplos de decisão (ADR-0034).

// StageKind é o tipo de uma etapa de pipeline.
type StageKind string

const (
	StageWorking   StageKind = "working"
	StageReview    StageKind = "review"
	StageDone      StageKind = "done"
	StageCancelled StageKind = "cancelled"
)

// StageKinds lista os tipos na ordem dos seletores.
var StageKinds = []StageKind{StageWorking, StageReview, StageDone, StageCancelled}

// Valid diz se k é um tipo conhecido.
func (k StageKind) Valid() bool {
	return k == StageWorking || k == StageReview || k == StageDone || k == StageCancelled
}

// Terminal diz se a etapa encerra o case (done ou cancelled).
func (k StageKind) Terminal() bool { return k == StageDone || k == StageCancelled }

// CaseStatus é o status de um case. Solto, é livre; num pipeline, é derivado do tipo da etapa.
type CaseStatus string

const (
	CaseDraft      CaseStatus = "draft"
	CaseInProgress CaseStatus = "in_progress"
	CaseInReview   CaseStatus = "in_review"
	CaseApproved   CaseStatus = "approved"
	CaseDone       CaseStatus = "done"
	CaseCancelled  CaseStatus = "cancelled"
)

// CaseStatuses lista os status na ordem das listas.
var CaseStatuses = []CaseStatus{CaseDraft, CaseInProgress, CaseInReview, CaseApproved, CaseDone, CaseCancelled}

// Valid diz se s é um status conhecido.
func (s CaseStatus) Valid() bool {
	for _, v := range CaseStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// Terminal diz se o status encerra o case.
func (s CaseStatus) Terminal() bool { return s == CaseDone || s == CaseCancelled }

// StatusForStage é o status de um case que está numa etapa do tipo k.
func StatusForStage(k StageKind) CaseStatus {
	switch k {
	case StageReview:
		return CaseInReview
	case StageDone:
		return CaseDone
	case StageCancelled:
		return CaseCancelled
	}
	return CaseInProgress
}

// FieldType é o tipo de um campo de entrada de etapa.
type FieldType string

const (
	FieldText      FieldType = "text"
	FieldMultiline FieldType = "multiline"
	FieldSelect    FieldType = "select"
	FieldNumber    FieldType = "number"
	FieldDate      FieldType = "date"
	FieldURL       FieldType = "url"
)

// FieldTypes lista os tipos de campo.
var FieldTypes = []FieldType{FieldText, FieldMultiline, FieldSelect, FieldNumber, FieldDate, FieldURL}

// Valid diz se t é um tipo de campo conhecido.
func (t FieldType) Valid() bool {
	for _, v := range FieldTypes {
		if v == t {
			return true
		}
	}
	return false
}

// StageField é um campo de entrada de uma etapa (o formulário "Novo item" usa os com ShowInAddForm da primeira).
type StageField struct {
	Key           string    `json:"key"`
	Label         string    `json:"label"`
	Type          FieldType `json:"type"`
	Options       []string  `json:"options,omitempty"`
	Required      bool      `json:"required,omitempty"`
	ShowInAddForm bool      `json:"show_in_add_form,omitempty"`
}

// Aprovador de uma etapa: qualquer membro humano, um usuário ou uma persona.
const (
	ApproverAnyMember     = "any_member"
	approverMemberPrefix  = "member:"
	approverPersonaPrefix = "persona:"
)

// StageApproval diz que sair da etapa exige o aprovador.
type StageApproval struct {
	Required bool `json:"required"`
	// Approver é any_member, member:<user_id> ou persona:<persona_id>.
	Approver string `json:"approver,omitempty"`
}

// ApproverRef é o aprovador já decomposto.
type ApproverRef struct {
	AnyMember bool
	UserID    *ID
	PersonaID *ID
}

// ParseApprover decompõe o aprovador; vazio vale any_member.
func ParseApprover(s string) (ApproverRef, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == ApproverAnyMember:
		return ApproverRef{AnyMember: true}, nil
	case strings.HasPrefix(s, approverMemberPrefix):
		id, err := uuid.Parse(strings.TrimPrefix(s, approverMemberPrefix))
		if err != nil {
			return ApproverRef{}, Invalid("approval.approver", "member:<user_id> needs a user id")
		}
		return ApproverRef{UserID: &id}, nil
	case strings.HasPrefix(s, approverPersonaPrefix):
		id, err := uuid.Parse(strings.TrimPrefix(s, approverPersonaPrefix))
		if err != nil {
			return ApproverRef{}, Invalid("approval.approver", "persona:<persona_id> needs a persona id")
		}
		return ApproverRef{PersonaID: &id}, nil
	}
	return ApproverRef{}, Invalid("approval.approver", "use any_member, member:<user_id> or persona:<persona_id>")
}

// MemberApprover e PersonaApprover montam o texto do aprovador.
func MemberApprover(id ID) string  { return approverMemberPrefix + id.String() }
func PersonaApprover(id ID) string { return approverPersonaPrefix + id.String() }

// StageAutomation: ao entrar na etapa, o Hanko cria uma task para a persona com o pacote de contexto do case.
type StageAutomation struct {
	PersonaID    *ID      `json:"persona_id,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Priority     Priority `json:"priority,omitempty"`
}

// Enabled diz se a etapa tem automação.
func (a *StageAutomation) Enabled() bool { return a != nil && a.PersonaID != nil }

// ReviewRouting são os destinos de uma etapa de revisão.
type ReviewRouting struct {
	ApproveTo        string `json:"approve_to,omitempty"`
	RejectTo         string `json:"reject_to,omitempty"`
	RequestChangesTo string `json:"request_changes_to,omitempty"`
	// RequireRejectReason e RequireChangesReason valem true quando ausentes.
	RequireRejectReason  *bool `json:"require_reject_reason,omitempty"`
	RequireChangesReason *bool `json:"require_changes_reason,omitempty"`
}

// RejectReasonRequired e ChangesReasonRequired aplicam o padrão (true).
func (r *ReviewRouting) RejectReasonRequired() bool {
	return r == nil || r.RequireRejectReason == nil || *r.RequireRejectReason
}

func (r *ReviewRouting) ChangesReasonRequired() bool {
	return r == nil || r.RequireChangesReason == nil || *r.RequireChangesReason
}

// StageGates são as condições extras para sair da etapa.
type StageGates struct {
	RequireChildrenTerminal bool `json:"require_children_terminal,omitempty"`
}

// StageConfig é a configuração de uma etapa (jsonb em pipeline_stages.config).
type StageConfig struct {
	Fields          []StageField     `json:"fields,omitempty"`
	WhatHappensHere string           `json:"what_happens_here,omitempty"`
	Disabled        bool             `json:"disabled,omitempty"`
	DisabledReason  string           `json:"disabled_reason,omitempty"`
	Approval        *StageApproval   `json:"approval,omitempty"`
	Automation      *StageAutomation `json:"automation,omitempty"`
	Review          *ReviewRouting   `json:"review,omitempty"`
	Gates           *StageGates      `json:"gates,omitempty"`
}

// Limites da configuração de etapa.
const (
	MaxStageFields         = 30
	MaxFieldOptions        = 50
	MaxAutomationBytes     = 8 << 10
	MaxWhatHappensHereSize = 4 << 10
	maxFieldLabelRunes     = 80
	maxFieldOptionRunes    = 80
	maxDisabledReasonRunes = 200
)

// ApprovalRequired diz se sair da etapa exige aprovador.
func (c StageConfig) ApprovalRequired() bool { return c.Approval != nil && c.Approval.Required }

// RequireChildrenTerminal diz se sair da etapa exige todos os filhos terminados.
func (c StageConfig) RequireChildrenTerminal() bool {
	return c.Gates != nil && c.Gates.RequireChildrenTerminal
}

// ValidateStageConfig confere a configuração de uma etapa do tipo kind; stageKeys são as keys das etapas do
// pipeline (os destinos de revisão precisam existir). A existência de personas e usuários é do service.
func ValidateStageConfig(kind StageKind, c StageConfig, stageKeys map[string]bool) error {
	if len(c.Fields) > MaxStageFields {
		return Invalid("config.fields", fmt.Sprintf("at most %d fields", MaxStageFields))
	}
	seen := map[string]bool{}
	for i, f := range c.Fields {
		p := fmt.Sprintf("config.fields[%d]", i)
		if !ValidSlug(f.Key) && !validFieldKey(f.Key) {
			return Invalid(p+".key", "use lowercase letters, digits, dashes or underscores (1-63)")
		}
		if seen[f.Key] {
			return Invalid(p+".key", "duplicate field key "+f.Key)
		}
		seen[f.Key] = true
		if n := utf8.RuneCountInString(strings.TrimSpace(f.Label)); n < 1 || n > maxFieldLabelRunes {
			return Invalid(p+".label", fmt.Sprintf("must have 1-%d characters", maxFieldLabelRunes))
		}
		if !f.Type.Valid() {
			return Invalid(p+".type", "use text, multiline, select, number, date or url")
		}
		if f.Type == FieldSelect && len(f.Options) == 0 {
			return Invalid(p+".options", "a select field needs options")
		}
		if f.Type != FieldSelect && len(f.Options) > 0 {
			return Invalid(p+".options", "only select fields take options")
		}
		if len(f.Options) > MaxFieldOptions {
			return Invalid(p+".options", fmt.Sprintf("at most %d options", MaxFieldOptions))
		}
		for _, o := range f.Options {
			if n := utf8.RuneCountInString(strings.TrimSpace(o)); n < 1 || n > maxFieldOptionRunes {
				return Invalid(p+".options", fmt.Sprintf("each option must have 1-%d characters", maxFieldOptionRunes))
			}
		}
	}
	if len(c.WhatHappensHere) > MaxWhatHappensHereSize {
		return Invalid("config.what_happens_here", fmt.Sprintf("too long (max %d bytes)", MaxWhatHappensHereSize))
	}
	if utf8.RuneCountInString(c.DisabledReason) > maxDisabledReasonRunes {
		return Invalid("config.disabled_reason", fmt.Sprintf("too long (max %d)", maxDisabledReasonRunes))
	}
	if c.Approval != nil {
		ref, err := ParseApprover(c.Approval.Approver)
		if err != nil {
			return err
		}
		if kind == StageReview && ref.PersonaID != nil {
			return Invalid("config.approval.approver", "a review stage is decided by a human: use any_member or member:<user_id>")
		}
	}
	if a := c.Automation; a != nil {
		if len(a.Instructions) > MaxAutomationBytes {
			return Invalid("config.automation.instructions", fmt.Sprintf("too long (max %d bytes)", MaxAutomationBytes))
		}
		if a.Priority != "" && !a.Priority.Valid() {
			return Invalid("config.automation.priority", "use critical, high, medium or low")
		}
		if a.PersonaID == nil && (strings.TrimSpace(a.Instructions) != "" || a.Priority != "") {
			return Invalid("config.automation.persona_id", "automation needs a persona")
		}
		if a.PersonaID != nil && kind.Terminal() {
			return Invalid("config.automation", "done and cancelled stages run no automation")
		}
	}
	if kind == StageReview {
		r := c.Review
		if r == nil || r.ApproveTo == "" || r.RejectTo == "" {
			return Invalid("config.review", "a review stage needs approve_to and reject_to")
		}
	} else if c.Review != nil && (c.Review.ApproveTo != "" || c.Review.RejectTo != "" || c.Review.RequestChangesTo != "") {
		return Invalid("config.review", "only review stages have review destinations")
	}
	if r := c.Review; r != nil {
		for field, k := range map[string]string{"approve_to": r.ApproveTo, "reject_to": r.RejectTo, "request_changes_to": r.RequestChangesTo} {
			if k != "" && !stageKeys[k] {
				return Invalid("config.review."+field, "unknown stage "+k)
			}
		}
	}
	return nil
}

func validFieldKey(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || (i > 0 && (r == '-' || r == '_'))
		if !ok {
			return false
		}
	}
	return true
}

// Pipeline é um conjunto ordenado de etapas.
type Pipeline struct {
	ID                 ID         `json:"id"`
	CompanyID          ID         `json:"company_id"`
	ProjectID          *ID        `json:"project_id,omitempty"`
	Key                string     `json:"key"`
	Name               string     `json:"name"`
	Description        string     `json:"description,omitempty"`
	EnforceTransitions bool       `json:"enforce_transitions"`
	ArchivedAt         *time.Time `json:"archived_at,omitempty"`
	CreatedBy          Actor      `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// Stage é uma etapa de pipeline.
type Stage struct {
	ID         ID          `json:"id"`
	PipelineID ID          `json:"pipeline_id"`
	Key        string      `json:"key"`
	Name       string      `json:"name"`
	Position   int         `json:"position"`
	Kind       StageKind   `json:"kind"`
	Config     StageConfig `json:"config"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// Transition é uma aresta permitida entre duas etapas.
type Transition struct {
	ID    ID     `json:"id"`
	From  ID     `json:"from_stage_id"`
	To    ID     `json:"to_stage_id"`
	Label string `json:"label,omitempty"`
}

// CaseSuggestion é a sugestão pendente de uma persona: ir para uma etapa, com justificativa e confiança.
type CaseSuggestion struct {
	ID         ID        `json:"id"`
	ToStage    string    `json:"to_stage"`
	Rationale  string    `json:"rationale"`
	Confidence float64   `json:"confidence"`
	PersonaID  *ID       `json:"persona_id,omitempty"`
	RunID      *ID       `json:"run_id,omitempty"`
	At         time.Time `json:"at"`
}

// Case é um registro estruturado de trabalho.
type Case struct {
	ID                 ID              `json:"id"`
	CompanyID          ID              `json:"company_id"`
	Number             int64           `json:"number"`
	Identifier         string          `json:"identifier"` // <PREFIXO>-C<n>
	Type               string          `json:"case_type,omitempty"`
	Key                string          `json:"key,omitempty"`
	Title              string          `json:"title"`
	Summary            string          `json:"summary,omitempty"`
	Fields             map[string]any  `json:"fields"`
	Status             CaseStatus      `json:"status"`
	PipelineID         *ID             `json:"pipeline_id,omitempty"`
	StageID            *ID             `json:"stage_id,omitempty"`
	ParentCaseID       *ID             `json:"parent_case_id,omitempty"`
	ProjectID          *ID             `json:"project_id,omitempty"`
	Version            int             `json:"version"`
	PendingSuggestion  *CaseSuggestion `json:"pending_suggestion,omitempty"`
	ApprovedVersion    *int            `json:"approved_version,omitempty"`
	ReapprovalRequired bool            `json:"reapproval_required,omitempty"`
	Attention          string          `json:"attention,omitempty"`
	AttentionAt        *time.Time      `json:"attention_at,omitempty"`
	TerminalKind       string          `json:"terminal_kind,omitempty"`
	TerminalAt         *time.Time      `json:"terminal_at,omitempty"`
	ChildCount         int             `json:"child_count"`
	TerminalChildCount int             `json:"terminal_child_count"`
	OriginRunID        *ID             `json:"origin_run_id,omitempty"`
	CreatedBy          Actor           `json:"created_by"`
	CompletedAt        *time.Time      `json:"completed_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// CaseIdentifier é <PREFIXO>-C<n>.
func CaseIdentifier(prefix string, n int64) string { return fmt.Sprintf("%s-C%d", prefix, n) }

// ParseCaseIdentifier lê <PREFIXO>-C<n>.
func ParseCaseIdentifier(s string) (prefix string, n int64, ok bool) {
	i := strings.LastIndex(s, "-C")
	if i < 1 || i+2 >= len(s) {
		return "", 0, false
	}
	var v int64
	for _, r := range s[i+2:] {
		if r < '0' || r > '9' {
			return "", 0, false
		}
		v = v*10 + int64(r-'0')
		if v > 1<<53 {
			return "", 0, false
		}
	}
	return s[:i], v, v > 0
}

// CaseLinkRole é o papel de uma task ligada a um case.
type CaseLinkRole string

const (
	LinkOrigin       CaseLinkRole = "origin"
	LinkWork         CaseLinkRole = "work"
	LinkAutomation   CaseLinkRole = "automation"
	LinkConversation CaseLinkRole = "conversation"
)

// Valid diz se r é um papel conhecido.
func (r CaseLinkRole) Valid() bool {
	return r == LinkOrigin || r == LinkWork || r == LinkAutomation || r == LinkConversation
}

// CaseEventKind é o tipo de um evento do histórico do case.
type CaseEventKind string

const (
	CaseEvCreated             CaseEventKind = "created"
	CaseEvUpdated             CaseEventKind = "updated"
	CaseEvFieldsChanged       CaseEventKind = "fields_changed"
	CaseEvStatusChanged       CaseEventKind = "status_changed"
	CaseEvTransitioned        CaseEventKind = "transitioned"
	CaseEvTransitionForced    CaseEventKind = "transition_forced"
	CaseEvTransitionSuggested CaseEventKind = "transition_suggested"
	CaseEvSuggestionResolved  CaseEventKind = "suggestion_resolved"
	CaseEvReviewDecided       CaseEventKind = "review_decided"
	CaseEvTaskLinked          CaseEventKind = "task_linked"
	CaseEvTaskUnlinked        CaseEventKind = "task_unlinked"
	CaseEvAutomationStarted   CaseEventKind = "automation_started"
	CaseEvAutomationFailed    CaseEventKind = "automation_failed"
	CaseEvBlockersSet         CaseEventKind = "blockers_set"
	CaseEvBlockersResolved    CaseEventKind = "blockers_resolved"
	CaseEvChildrenTerminal    CaseEventKind = "children_terminal"
)

// CaseEvent é um fato do histórico do case (imutável).
type CaseEvent struct {
	ID          ID             `json:"id"`
	CaseID      ID             `json:"case_id"`
	Kind        CaseEventKind  `json:"kind"`
	FromStageID *ID            `json:"from_stage_id,omitempty"`
	ToStageID   *ID            `json:"to_stage_id,omitempty"`
	Actor       Actor          `json:"actor"`
	RunID       *ID            `json:"run_id,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// ReviewDecision é a decisão humana sobre um case numa etapa de revisão.
type ReviewDecision string

const (
	ReviewApprove        ReviewDecision = "approve"
	ReviewReject         ReviewDecision = "reject"
	ReviewRequestChanges ReviewDecision = "request_changes"
)

// Valid diz se d é uma decisão conhecida.
func (d ReviewDecision) Valid() bool {
	return d == ReviewApprove || d == ReviewReject || d == ReviewRequestChanges
}

// DecisionSource é de onde vem um exemplo de decisão.
type DecisionSource string

const (
	DecisionFromGate       DecisionSource = "gate"
	DecisionFromCaseReview DecisionSource = "case_review"
)

// Valid diz se s é uma origem conhecida.
func (s DecisionSource) Valid() bool { return s == DecisionFromGate || s == DecisionFromCaseReview }

// DecisionNote é uma versão anterior das notas de um exemplo.
type DecisionNote struct {
	Notes string    `json:"notes"`
	At    time.Time `json:"at"`
}

// DecisionExample é uma decisão humana guardada com notas e o contexto até a decisão.
type DecisionExample struct {
	ID              ID             `json:"id"`
	CompanyID       ID             `json:"company_id"`
	SourceKind      DecisionSource `json:"source_kind"`
	SourceID        ID             `json:"source_id"`
	TaskID          *ID            `json:"task_id,omitempty"`
	CaseID          *ID            `json:"case_id,omitempty"`
	CutoffAt        time.Time      `json:"cutoff_at"`
	DecisionOutcome string         `json:"decision_outcome"`
	Notes           string         `json:"notes"`
	NotesHistory    []DecisionNote `json:"notes_history,omitempty"`
	Snapshot        map[string]any `json:"snapshot"`
	CreatedByUserID *ID            `json:"created_by_user_id,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// Erros de transição. Os de conflito valem 409 (ErrConflict); os de regra, 422 (ErrInvalid) ou 403 (ErrNotAllowed).
var (
	// ErrVersionConflict: expected_version não confere com a versão atual (VersionConflictError leva a atual).
	ErrVersionConflict = fmt.Errorf("case version conflict: %w", ErrConflict)
	// ErrReviewOutdated: o case mudou depois de aprovado; a próxima saída precisa passar por revisão.
	ErrReviewOutdated = fmt.Errorf("review_outdated: the case changed after approval and needs a new review: %w", ErrConflict)
	// ErrCaseBlocked: há bloqueadores abertos ou filhos não terminados.
	ErrCaseBlocked = fmt.Errorf("case is blocked: %w", ErrConflict)
)

// VersionConflictError carrega a versão atual do case.
type VersionConflictError struct{ Current int }

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("case version conflict: current version is %d", e.Current)
}
func (e *VersionConflictError) Unwrap() error { return ErrVersionConflict }

// CurrentVersion devolve a versão atual de um erro de conflito de versão.
func CurrentVersion(err error) (int, bool) {
	var v *VersionConflictError
	if errors.As(err, &v) {
		return v.Current, true
	}
	return 0, false
}
