package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Pipelines, cases, fila de revisão, aprendizados e exemplos de decisão (ADR-0034). O service é a barreira de papéis
// e das regras; aqui só se resolvem referências e se converte. Run tokens (personas) leem pipelines e cases, criam e
// atualizam cases, sugerem e movem entre etapas de trabalho; desenhar pipelines, decidir revisões e exemplos é humano.

// pipelinesBackend é o subconjunto do service desta funcionalidade. Fica fora de backend para que os fakes dos
// testes unitários continuem compilando; sem ele, as rotas respondem 501.
type pipelinesBackend interface {
	CreatePipeline(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreatePipelineInput) (service.PipelineView, error)
	UpdatePipeline(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, in service.UpdatePipelineInput) (service.PipelineView, error)
	DeletePipeline(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID) error
	GetPipeline(ctx context.Context, companyID domain.ID, ref string) (service.PipelineView, error)
	ListPipelines(ctx context.Context, companyID domain.ID, includeArchived bool) ([]service.PipelineView, error)
	AddStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, in service.StageInput, position *int) (service.PipelineView, error)
	UpdateStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, stageRef string, in service.UpdateStageInput) (service.PipelineView, error)
	DeleteStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, stageRef string) (service.PipelineView, error)
	ReorderStages(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, keys []string) (service.PipelineView, error)
	SetTransitions(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, specs []service.TransitionSpec, enforce bool) (service.PipelineView, error)
	CreateCase(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateCaseInput) (service.CaseView, bool, error)
	UpdateCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.UpdateCaseInput) (service.CaseView, error)
	GetCaseView(ctx context.Context, companyID domain.ID, ref string) (service.CaseView, error)
	ResolveCase(ctx context.Context, companyID domain.ID, ref string) (domain.ID, error)
	ListCases(ctx context.Context, companyID domain.ID, f service.CaseFilter) ([]service.CaseView, error)
	ListChildCases(ctx context.Context, companyID, caseID domain.ID) ([]service.CaseView, error)
	ListCaseEvents(ctx context.Context, companyID, caseID domain.ID, limit int) ([]domain.CaseEvent, error)
	TransitionCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.TransitionCaseInput) (service.CaseView, error)
	SuggestCaseTransition(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.SuggestInput) (service.CaseView, error)
	ResolveSuggestion(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.ResolveSuggestionInput) (service.CaseView, error)
	ReviewCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.ReviewCaseInput) (service.CaseView, error)
	BulkApprove(ctx context.Context, actor domain.Actor, companyID domain.ID, items []service.BulkReviewItem) ([]service.BulkReviewResult, error)
	LinkCaseTask(ctx context.Context, actor domain.Actor, companyID, caseID, taskID domain.ID, role domain.CaseLinkRole, runID *domain.ID) (service.CaseView, error)
	UnlinkCaseTask(ctx context.Context, actor domain.Actor, companyID, caseID, taskID domain.ID) (service.CaseView, error)
	SetCaseBlockers(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, expectedVersion int, blockers []domain.ID, runID *domain.ID) (service.CaseView, error)
	GetCaseContext(ctx context.Context, companyID, caseID domain.ID) (string, error)
	OpenCaseConversation(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID) (service.TaskView, error)
	ReviewQueue(ctx context.Context, companyID domain.ID) ([]service.ReviewItem, error)
	ListLearnings(ctx context.Context, companyID domain.ID, f service.LearningFilter) ([]service.Learning, error)
	SaveDecisionExample(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.SaveDecisionExampleInput) (domain.DecisionExample, error)
	UpdateDecisionExampleNotes(ctx context.Context, actor domain.Actor, companyID, id domain.ID, notes string) (domain.DecisionExample, error)
	DeleteDecisionExample(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error
	GetDecisionExample(ctx context.Context, companyID, id domain.ID) (domain.DecisionExample, error)
	ListDecisionExamples(ctx context.Context, companyID domain.ID, f service.DecisionExampleFilter) ([]domain.DecisionExample, error)
	ExportDecisionExamples(ctx context.Context, companyID domain.ID, w io.Writer) (int, error)
}

var _ pipelinesBackend = (*service.Service)(nil)

func (s *server) pipes() (pipelinesBackend, error) {
	b, ok := s.svc.(pipelinesBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "pipelines indisponíveis neste servidor")
	}
	return b, nil
}

// pipeFail é fail com a versão atual no corpo do 409 de conflito de versão.
func (s *server) pipeFail(ctx context.Context, err error) error {
	if cur, ok := domain.CurrentVersion(err); ok {
		return problem(http.StatusConflict, fmt.Sprintf("o case mudou: a versão atual é %d; releia e tente de novo", cur),
			&huma.ErrorDetail{Location: "body.expected_version", Message: "versão atual", Value: cur})
	}
	return s.fail(ctx, err)
}

// ---------- DTOs ----------

// StageField é um campo de entrada de uma etapa.
type StageField struct {
	Key           string   `json:"key" doc:"Chave do campo em fields (minúsculas, dígitos, - e _)." example:"empresa"`
	Label         string   `json:"label" example:"Empresa"`
	Type          string   `json:"type" enum:"text,multiline,select,number,date,url"`
	Options       []string `json:"options,omitempty" doc:"Opções de um campo select."`
	Required      bool     `json:"required,omitempty" doc:"Precisa estar preenchido para o case sair da etapa."`
	ShowInAddForm bool     `json:"show_in_add_form,omitempty" doc:"Aparece no formulário Novo item (vale na primeira etapa)."`
}

// StageApproval diz que sair da etapa exige o aprovador.
type StageApproval struct {
	Required bool   `json:"required"`
	Approver string `json:"approver,omitempty" doc:"any_member (padrão), member:<user_id> ou persona:<persona_id> (persona só em etapa working)."`
}

// StageAutomation cria, ao entrar na etapa, uma task para a persona com o pacote de contexto do case.
type StageAutomation struct {
	PersonaID    string `json:"persona_id,omitempty" format:"uuid" doc:"Persona que trabalha o case nesta etapa (na escrita, aceita também o slug em persona)."`
	Persona      string `json:"persona,omitempty" doc:"Slug ou id da persona (só na escrita)."`
	Instructions string `json:"instructions,omitempty" maxLength:"8192" doc:"Instruções em markdown (até 8 KiB) que vão no pacote de contexto."`
	Priority     string `json:"priority,omitempty" enum:"critical,high,medium,low"`
}

// StageReview são os destinos de uma etapa de revisão.
type StageReview struct {
	ApproveTo            string `json:"approve_to,omitempty" doc:"Key da etapa para onde vai o case aprovado (obrigatório em etapa review)."`
	RejectTo             string `json:"reject_to,omitempty" doc:"Key da etapa para onde vai o case recusado (obrigatório em etapa review)."`
	RequestChangesTo     string `json:"request_changes_to,omitempty" doc:"Key da etapa de trabalho para onde volta o case com pedido de mudanças."`
	RequireRejectReason  *bool  `json:"require_reject_reason,omitempty" doc:"Recusar exige motivo (padrão true)."`
	RequireChangesReason *bool  `json:"require_changes_reason,omitempty" doc:"Pedir mudanças exige motivo (padrão true)."`
}

// StageGates são as condições extras para sair da etapa.
type StageGates struct {
	RequireChildrenTerminal bool `json:"require_children_terminal,omitempty" doc:"Só sai quando todos os cases filhos terminaram."`
}

// StageConfig é a configuração de uma etapa.
type StageConfig struct {
	Fields          []StageField     `json:"fields,omitempty" maxItems:"30"`
	WhatHappensHere string           `json:"what_happens_here,omitempty" maxLength:"4096" doc:"O que acontece nesta etapa (markdown)."`
	Disabled        bool             `json:"disabled,omitempty" doc:"Etapa desligada: nenhum case entra."`
	DisabledReason  string           `json:"disabled_reason,omitempty" maxLength:"200"`
	Approval        *StageApproval   `json:"approval,omitempty"`
	Automation      *StageAutomation `json:"automation,omitempty"`
	Review          *StageReview     `json:"review,omitempty"`
	Gates           *StageGates      `json:"gates,omitempty"`
}

// Stage é uma etapa de pipeline.
type Stage struct {
	ID       string      `json:"id" format:"uuid"`
	Key      string      `json:"key" example:"qualificacao"`
	Name     string      `json:"name" example:"Qualificação"`
	Position int         `json:"position"`
	Kind     string      `json:"kind" enum:"working,review,done,cancelled"`
	Config   StageConfig `json:"config"`
}

// PipelineTransition é uma aresta permitida (pelas keys das etapas).
type PipelineTransition struct {
	From  string `json:"from" doc:"Key da etapa de origem."`
	To    string `json:"to" doc:"Key da etapa de destino."`
	Label string `json:"label,omitempty" maxLength:"120"`
}

// Pipeline são etapas ordenadas com transições, aprovação e automação.
type Pipeline struct {
	ID                 string               `json:"id" format:"uuid"`
	CompanyID          string               `json:"company_id" format:"uuid"`
	Key                string               `json:"key" example:"vendas"`
	Name               string               `json:"name" example:"Vendas"`
	Description        string               `json:"description,omitempty"`
	ProjectID          string               `json:"project_id,omitempty" format:"uuid"`
	ProjectSlug        string               `json:"project_slug,omitempty"`
	ProjectName        string               `json:"project_name,omitempty"`
	EnforceTransitions bool                 `json:"enforce_transitions" doc:"Modo estrito: fora das transições só com force + motivo."`
	Archived           bool                 `json:"archived"`
	ArchivedAt         *time.Time           `json:"archived_at,omitempty"`
	Stages             []Stage              `json:"stages"`
	Transitions        []PipelineTransition `json:"transitions"`
	OpenCases          int                  `json:"open_cases"`
	InReview           int                  `json:"in_review"`
	TotalCases         int                  `json:"total_cases"`
	CreatedBy          ActorRef             `json:"created_by"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
}

// CaseSuggestion é a sugestão pendente de mover o case.
type CaseSuggestion struct {
	ID         string    `json:"id" format:"uuid"`
	ToStage    string    `json:"to_stage"`
	Rationale  string    `json:"rationale"`
	Confidence float64   `json:"confidence" minimum:"0" maximum:"1"`
	PersonaID  string    `json:"persona_id,omitempty" format:"uuid"`
	RunID      string    `json:"run_id,omitempty" format:"uuid"`
	At         time.Time `json:"at"`
}

// CaseTaskLink é uma task ligada ao case.
type CaseTaskLink struct {
	TaskID      string    `json:"task_id" format:"uuid"`
	TaskKey     string    `json:"task_key"`
	TaskTitle   string    `json:"task_title"`
	TaskStatus  string    `json:"task_status"`
	PersonaName string    `json:"persona_name,omitempty"`
	Role        string    `json:"role" enum:"origin,work,automation,conversation"`
	RunID       string    `json:"created_by_run_id,omitempty" format:"uuid"`
	CreatedAt   time.Time `json:"created_at"`
}

// CaseRef é um case resumido.
type CaseRef struct {
	ID         string `json:"id" format:"uuid"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Status     string `json:"status,omitempty"`
	Resolved   bool   `json:"resolved,omitempty" doc:"Num bloqueador: já chegou a done."`
}

// CaseEvent é um fato imutável do histórico do case.
type CaseEvent struct {
	ID          string         `json:"id" format:"uuid"`
	Kind        string         `json:"kind"`
	FromStageID string         `json:"from_stage_id,omitempty" format:"uuid"`
	ToStageID   string         `json:"to_stage_id,omitempty" format:"uuid"`
	Actor       ActorRef       `json:"actor"`
	RunID       string         `json:"run_id,omitempty" format:"uuid"`
	Payload     map[string]any `json:"payload,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Case é um registro estruturado de trabalho, solto ou numa etapa de pipeline.
type Case struct {
	ID                 string          `json:"id" format:"uuid"`
	CompanyID          string          `json:"company_id" format:"uuid"`
	Number             int64           `json:"number"`
	Identifier         string          `json:"identifier" example:"HNK-C12"`
	CaseType           string          `json:"case_type,omitempty" example:"lead"`
	Key                string          `json:"key,omitempty" doc:"Chave externa: única por empresa e tipo (upsert)."`
	Title              string          `json:"title"`
	Summary            string          `json:"summary,omitempty" doc:"Markdown."`
	Fields             map[string]any  `json:"fields" doc:"Campos planos: texto, número, booleano."`
	Status             string          `json:"status" enum:"draft,in_progress,in_review,approved,done,cancelled"`
	PipelineID         string          `json:"pipeline_id,omitempty" format:"uuid"`
	PipelineKey        string          `json:"pipeline_key,omitempty"`
	PipelineName       string          `json:"pipeline_name,omitempty"`
	StageID            string          `json:"stage_id,omitempty" format:"uuid"`
	StageKey           string          `json:"stage_key,omitempty"`
	StageName          string          `json:"stage_name,omitempty"`
	StageKind          string          `json:"stage_kind,omitempty"`
	ParentID           string          `json:"parent_id,omitempty" format:"uuid"`
	ParentIdentifier   string          `json:"parent_identifier,omitempty"`
	ParentTitle        string          `json:"parent_title,omitempty"`
	ProjectID          string          `json:"project_id,omitempty" format:"uuid"`
	ProjectSlug        string          `json:"project_slug,omitempty"`
	ProjectName        string          `json:"project_name,omitempty"`
	Version            int             `json:"version" doc:"Trava otimista: toda escrita manda expected_version e sobe a versão em 1."`
	PendingSuggestion  *CaseSuggestion `json:"pending_suggestion,omitempty"`
	ApprovedVersion    *int            `json:"approved_version,omitempty"`
	ReapprovalRequired bool            `json:"reapproval_required,omitempty" doc:"Mudou depois de aprovado: a próxima saída passa por revisão."`
	Attention          string          `json:"attention,omitempty" enum:"blockers_resolved,children_terminal"`
	TerminalKind       string          `json:"terminal_kind,omitempty" enum:"done,cancelled"`
	TerminalAt         *time.Time      `json:"terminal_at,omitempty"`
	ChildCount         int             `json:"child_count"`
	TerminalChildCount int             `json:"terminal_child_count"`
	OpenBlockers       int             `json:"open_blockers"`
	OriginRunID        string          `json:"origin_run_id,omitempty" format:"uuid"`
	CreatedBy          ActorRef        `json:"created_by"`
	CompletedAt        *time.Time      `json:"completed_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	// Só no detalhe.
	Links    []CaseTaskLink `json:"links,omitempty"`
	Blockers []CaseRef      `json:"blockers,omitempty"`
	Blocking []CaseRef      `json:"blocking,omitempty"`
	Events   []CaseEvent    `json:"events,omitempty"`
	// Created vem na criação: false quando o par (tipo, key) já existia e o case foi atualizado.
	Created *bool `json:"created,omitempty"`
}

// ReviewQueueItem é uma linha da fila de revisão.
type ReviewQueueItem struct {
	Kind                 string          `json:"kind" enum:"review,suggestion,attention"`
	Case                 Case            `json:"case"`
	Since                time.Time       `json:"since"`
	Reason               string          `json:"reason,omitempty"`
	Suggestion           *CaseSuggestion `json:"suggestion,omitempty"`
	SuggestedStageName   string          `json:"suggested_stage_name,omitempty"`
	ApproveTo            string          `json:"approve_to,omitempty"`
	ApproveToName        string          `json:"approve_to_name,omitempty"`
	RejectTo             string          `json:"reject_to,omitempty"`
	RejectToName         string          `json:"reject_to_name,omitempty"`
	RequestChangesTo     string          `json:"request_changes_to,omitempty"`
	RequestChangesToName string          `json:"request_changes_to_name,omitempty"`
	RequireRejectReason  bool            `json:"require_reject_reason"`
	RequireChangesReason bool            `json:"require_changes_reason"`
	Approver             string          `json:"approver,omitempty"`
}

// BulkResult é o resultado de um item da aprovação em lote.
type BulkResult struct {
	Case           string `json:"case" format:"uuid"`
	OK             bool   `json:"ok"`
	Version        int    `json:"version,omitempty"`
	Error          string `json:"error,omitempty"`
	CurrentVersion int    `json:"current_version,omitempty"`
}

// Learning é uma decisão de revisão (ou uma transição forçada) e o que ela ensinou.
type Learning struct {
	EventID        string         `json:"event_id" format:"uuid"`
	Kind           string         `json:"kind" enum:"review_decided,transition_forced"`
	Decision       string         `json:"decision" enum:"approve,reject,request_changes,forced"`
	Reason         string         `json:"reason,omitempty"`
	Actor          ActorRef       `json:"actor"`
	ActorName      string         `json:"actor_name,omitempty"`
	CaseID         string         `json:"case_id" format:"uuid"`
	CaseIdentifier string         `json:"case_identifier"`
	CaseTitle      string         `json:"case_title"`
	PipelineKey    string         `json:"pipeline_key,omitempty"`
	PipelineName   string         `json:"pipeline_name,omitempty"`
	FromStage      string         `json:"from_stage,omitempty"`
	FromName       string         `json:"from_name,omitempty"`
	ToStage        string         `json:"to_stage,omitempty"`
	ToName         string         `json:"to_name,omitempty"`
	At             time.Time      `json:"at"`
	Snapshot       map[string]any `json:"snapshot,omitempty" doc:"O case como estava na decisão (título, resumo, campos)."`
}

// DecisionExample é uma decisão humana guardada com notas e o contexto até o corte.
type DecisionExample struct {
	ID              string         `json:"id" format:"uuid"`
	SourceKind      string         `json:"source_kind" enum:"gate,case_review"`
	SourceID        string         `json:"source_id" format:"uuid" doc:"Id do gate ou do evento review_decided do case."`
	TaskID          string         `json:"task_id,omitempty" format:"uuid"`
	CaseID          string         `json:"case_id,omitempty" format:"uuid"`
	CutoffAt        time.Time      `json:"cutoff_at" doc:"Momento da decisão: o snapshot vai até aqui."`
	DecisionOutcome string         `json:"decision_outcome"`
	Notes           string         `json:"notes"`
	NotesHistory    []NoteVersion  `json:"notes_history,omitempty"`
	Snapshot        map[string]any `json:"snapshot,omitempty" doc:"Só no detalhe."`
	CreatedByUserID string         `json:"created_by_user_id,omitempty" format:"uuid"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// NoteVersion é uma versão anterior das notas.
type NoteVersion struct {
	Notes string    `json:"notes"`
	At    time.Time `json:"at"`
}

// CaseContext é o pacote de contexto do case em markdown.
type CaseContext struct {
	Markdown string `json:"markdown"`
}

// ---------- conversões ----------

func stageConfigOut(c domain.StageConfig) StageConfig {
	o := StageConfig{WhatHappensHere: c.WhatHappensHere, Disabled: c.Disabled, DisabledReason: c.DisabledReason}
	for _, f := range c.Fields {
		o.Fields = append(o.Fields, StageField{Key: f.Key, Label: f.Label, Type: string(f.Type), Options: f.Options, Required: f.Required, ShowInAddForm: f.ShowInAddForm})
	}
	if a := c.Approval; a != nil {
		o.Approval = &StageApproval{Required: a.Required, Approver: a.Approver}
	}
	if a := c.Automation; a != nil {
		o.Automation = &StageAutomation{PersonaID: idPtr(a.PersonaID), Instructions: a.Instructions, Priority: string(a.Priority)}
	}
	if r := c.Review; r != nil {
		o.Review = &StageReview{ApproveTo: r.ApproveTo, RejectTo: r.RejectTo, RequestChangesTo: r.RequestChangesTo,
			RequireRejectReason: r.RequireRejectReason, RequireChangesReason: r.RequireChangesReason}
	}
	if g := c.Gates; g != nil {
		o.Gates = &StageGates{RequireChildrenTerminal: g.RequireChildrenTerminal}
	}
	return o
}

// stageConfigIn converte a configuração recebida, resolvendo a persona da automação por id ou slug.
func (s *server) stageConfigIn(ctx context.Context, cid domain.ID, c StageConfig) (domain.StageConfig, error) {
	o := domain.StageConfig{WhatHappensHere: c.WhatHappensHere, Disabled: c.Disabled, DisabledReason: c.DisabledReason}
	for _, f := range c.Fields {
		o.Fields = append(o.Fields, domain.StageField{Key: f.Key, Label: f.Label, Type: domain.FieldType(f.Type), Options: f.Options, Required: f.Required, ShowInAddForm: f.ShowInAddForm})
	}
	if a := c.Approval; a != nil {
		o.Approval = &domain.StageApproval{Required: a.Required, Approver: a.Approver}
	}
	if a := c.Automation; a != nil {
		ref := strings.TrimSpace(a.Persona)
		if ref == "" {
			ref = strings.TrimSpace(a.PersonaID)
		}
		pid, err := s.personaRef(ctx, cid, ref)
		if err != nil {
			return o, domain.Invalid("config.automation.persona", "persona not found in company")
		}
		o.Automation = &domain.StageAutomation{PersonaID: pid, Instructions: a.Instructions, Priority: domain.Priority(a.Priority)}
	}
	if r := c.Review; r != nil {
		o.Review = &domain.ReviewRouting{ApproveTo: r.ApproveTo, RejectTo: r.RejectTo, RequestChangesTo: r.RequestChangesTo,
			RequireRejectReason: r.RequireRejectReason, RequireChangesReason: r.RequireChangesReason}
	}
	if g := c.Gates; g != nil {
		o.Gates = &domain.StageGates{RequireChildrenTerminal: g.RequireChildrenTerminal}
	}
	return o, nil
}

func pipelineOut(v service.PipelineView) Pipeline {
	p := Pipeline{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Key: v.Key, Name: v.Name, Description: v.Description, ProjectID: idPtr(v.ProjectID),
		ProjectSlug: v.ProjectSlug, ProjectName: v.ProjectName, EnforceTransitions: v.EnforceTransitions, Archived: v.ArchivedAt != nil,
		ArchivedAt: v.ArchivedAt, Stages: []Stage{}, Transitions: []PipelineTransition{}, OpenCases: v.OpenCases, InReview: v.InReview,
		TotalCases: v.TotalCases, CreatedBy: actorRef(v.CreatedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	keys := map[domain.ID]string{}
	for _, st := range v.Stages {
		keys[st.ID] = st.Key
		p.Stages = append(p.Stages, Stage{ID: idStr(st.ID), Key: st.Key, Name: st.Name, Position: st.Position, Kind: string(st.Kind), Config: stageConfigOut(st.Config)})
	}
	for _, t := range v.Transitions {
		p.Transitions = append(p.Transitions, PipelineTransition{From: keys[t.From], To: keys[t.To], Label: t.Label})
	}
	return p
}

func suggestionOut(sg *domain.CaseSuggestion) *CaseSuggestion {
	if sg == nil {
		return nil
	}
	return &CaseSuggestion{ID: idStr(sg.ID), ToStage: sg.ToStage, Rationale: sg.Rationale, Confidence: sg.Confidence, PersonaID: idPtr(sg.PersonaID),
		RunID: idPtr(sg.RunID), At: sg.At}
}

func caseEventOut(e domain.CaseEvent) CaseEvent {
	return CaseEvent{ID: idStr(e.ID), Kind: string(e.Kind), FromStageID: idPtr(e.FromStageID), ToStageID: idPtr(e.ToStageID), Actor: actorRef(e.Actor),
		RunID: idPtr(e.RunID), Payload: e.Payload, CreatedAt: e.CreatedAt}
}

func caseOut(v service.CaseView) Case {
	c := Case{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Number: v.Number, Identifier: v.Identifier, CaseType: v.Type, Key: v.Key, Title: v.Title,
		Summary: v.Summary, Fields: v.Fields, Status: string(v.Status), PipelineID: idPtr(v.PipelineID), PipelineKey: v.PipelineKey,
		PipelineName: v.PipelineName, StageID: idPtr(v.StageID), StageKey: v.StageKey, StageName: v.StageName, StageKind: string(v.StageKind),
		ParentID: idPtr(v.ParentCaseID), ParentIdentifier: v.ParentIdentifier, ParentTitle: v.ParentTitle, ProjectID: idPtr(v.ProjectID),
		ProjectSlug: v.ProjectSlug, ProjectName: v.ProjectName, Version: v.Version, PendingSuggestion: suggestionOut(v.PendingSuggestion),
		ApprovedVersion: v.ApprovedVersion, ReapprovalRequired: v.ReapprovalRequired, Attention: v.Attention, TerminalKind: v.TerminalKind,
		TerminalAt: v.TerminalAt, ChildCount: v.ChildCount, TerminalChildCount: v.TerminalChildCount, OpenBlockers: v.OpenBlockers,
		OriginRunID: idPtr(v.OriginRunID), CreatedBy: actorRef(v.CreatedBy), CompletedAt: v.CompletedAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if c.Fields == nil {
		c.Fields = map[string]any{}
	}
	for _, l := range v.Links {
		c.Links = append(c.Links, CaseTaskLink{TaskID: idStr(l.TaskID), TaskKey: l.TaskKey, TaskTitle: l.TaskTitle, TaskStatus: string(l.TaskStatus),
			PersonaName: l.PersonaName, Role: string(l.Role), RunID: idPtr(l.RunID), CreatedAt: l.CreatedAt})
	}
	for _, b := range v.Blockers {
		c.Blockers = append(c.Blockers, CaseRef{ID: idStr(b.ID), Identifier: b.Identifier, Title: b.Title, Status: string(b.Status), Resolved: b.Resolved})
	}
	for _, b := range v.Blocking {
		c.Blocking = append(c.Blocking, CaseRef{ID: idStr(b.ID), Identifier: b.Identifier, Title: b.Title})
	}
	for _, e := range v.Events {
		c.Events = append(c.Events, caseEventOut(e))
	}
	return c
}

func reviewItemOut(it service.ReviewItem) ReviewQueueItem {
	return ReviewQueueItem{Kind: it.Kind, Case: caseOut(it.Case), Since: it.Since, Reason: it.Reason, Suggestion: suggestionOut(it.Suggestion),
		SuggestedStageName: it.SuggestedStageName, ApproveTo: it.ApproveTo, ApproveToName: it.ApproveToName, RejectTo: it.RejectTo,
		RejectToName: it.RejectToName, RequestChangesTo: it.RequestChangesTo, RequestChangesToName: it.RequestChangesToName,
		RequireRejectReason: it.RequireRejectReason, RequireChangesReason: it.RequireChangesReason, Approver: it.Approver}
}

func learningOut(l service.Learning) Learning {
	return Learning{EventID: idStr(l.EventID), Kind: l.Kind, Decision: l.Decision, Reason: l.Reason, Actor: actorRef(l.Actor), ActorName: l.ActorName,
		CaseID: idStr(l.CaseID), CaseIdentifier: l.CaseIdentifier, CaseTitle: l.CaseTitle, PipelineKey: l.PipelineKey, PipelineName: l.PipelineName,
		FromStage: l.FromStage, FromName: l.FromName, ToStage: l.ToStage, ToName: l.ToName, At: l.At, Snapshot: l.Snapshot}
}

func decisionExampleOut(e domain.DecisionExample, withSnapshot bool) DecisionExample {
	o := DecisionExample{ID: idStr(e.ID), SourceKind: string(e.SourceKind), SourceID: idStr(e.SourceID), TaskID: idPtr(e.TaskID), CaseID: idPtr(e.CaseID),
		CutoffAt: e.CutoffAt, DecisionOutcome: e.DecisionOutcome, Notes: e.Notes, CreatedByUserID: idPtr(e.CreatedByUserID), CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt}
	for _, n := range e.NotesHistory {
		o.NotesHistory = append(o.NotesHistory, NoteVersion{Notes: n.Notes, At: n.At})
	}
	if withSnapshot {
		o.Snapshot = e.Snapshot
	}
	return o
}

// runRefs devolve a run e a task do run token (nil para humano).
func runRefs(sc scope) (runID, taskID *domain.ID) {
	if sc.Claims == nil {
		return nil, nil
	}
	if id, err := domain.ParseID(sc.Claims.RunID); err == nil {
		runID = &id
	}
	if id, err := domain.ParseID(sc.Claims.TaskID); err == nil && id != (domain.ID{}) {
		taskID = &id
	}
	return runID, taskID
}

// ---------- entradas ----------

// PipelinePath identifica um pipeline.
type PipelinePath struct {
	CompanyPath
	Pipeline string `path:"pipeline" doc:"Id ou key do pipeline." example:"vendas"`
}

// StagePath identifica uma etapa.
type StagePath struct {
	PipelinePath
	Stage string `path:"stage" doc:"Id ou key da etapa." example:"qualificacao"`
}

// CasePath identifica um case.
type CasePath struct {
	CompanyPath
	Case string `path:"case" doc:"Id, identificador (HNK-C12) ou número do case." example:"HNK-C12"`
}

// StageBody é uma etapa na escrita.
type StageBody struct {
	Key    string      `json:"key,omitempty" doc:"Derivada do nome quando vazia."`
	Name   string      `json:"name" minLength:"1" maxLength:"120"`
	Kind   string      `json:"kind,omitempty" enum:"working,review,done,cancelled" doc:"Padrão working."`
	Config StageConfig `json:"config,omitempty"`
}

type createPipelineIn struct {
	CompanyPath
	Body struct {
		Key                string      `json:"key,omitempty" doc:"Derivada do nome quando vazia."`
		Name               string      `json:"name" minLength:"1" maxLength:"120"`
		Description        string      `json:"description,omitempty" maxLength:"8192"`
		Project            string      `json:"project,omitempty" doc:"Id ou slug do projeto."`
		EnforceTransitions bool        `json:"enforce_transitions,omitempty"`
		Stages             []StageBody `json:"stages,omitempty" maxItems:"30" doc:"Etapas na ordem."`
	}
}

type listPipelinesIn struct {
	CompanyPath
	Page
	Archived bool `query:"archived" doc:"Inclui os arquivados."`
}

type updatePipelineIn struct {
	PipelinePath
	Body struct {
		Key                *string `json:"key,omitempty"`
		Name               *string `json:"name,omitempty" minLength:"1" maxLength:"120"`
		Description        *string `json:"description,omitempty" maxLength:"8192"`
		Project            string  `json:"project,omitempty"`
		ClearProject       bool    `json:"clear_project,omitempty"`
		EnforceTransitions *bool   `json:"enforce_transitions,omitempty"`
		Archived           *bool   `json:"archived,omitempty" doc:"true arquiva; false reativa."`
	}
}

type addStageIn struct {
	PipelinePath
	Body struct {
		StageBody
		Position *int `json:"position,omitempty" minimum:"0" doc:"Posição (0 = primeira); vazio = no fim."`
	}
}

type updateStageIn struct {
	StagePath
	Body struct {
		Key    *string      `json:"key,omitempty"`
		Name   *string      `json:"name,omitempty" minLength:"1" maxLength:"120"`
		Kind   *string      `json:"kind,omitempty" enum:"working,review,done,cancelled"`
		Config *StageConfig `json:"config,omitempty" doc:"Troca a configuração inteira."`
	}
}

type reorderStagesIn struct {
	PipelinePath
	Body struct {
		Stages []string `json:"stages" minItems:"1" maxItems:"30" doc:"Todas as keys, na ordem nova."`
	}
}

type setTransitionsIn struct {
	PipelinePath
	Body struct {
		Transitions []PipelineTransition `json:"transitions" maxItems:"300"`
		Enforce     bool                 `json:"enforce" doc:"Liga o modo estrito."`
	}
}

// CaseBody são os campos de criação de um case.
type CaseBody struct {
	Title    string         `json:"title" minLength:"1" maxLength:"300"`
	CaseType string         `json:"case_type,omitempty" maxLength:"60" example:"lead"`
	Key      string         `json:"key,omitempty" maxLength:"200" doc:"Com key, (tipo, key) é único: repetir atualiza o case (upsert)."`
	Summary  string         `json:"summary,omitempty" doc:"Markdown (até 64 KiB)."`
	Fields   map[string]any `json:"fields,omitempty" doc:"Campos planos (até 64 KiB)."`
	Status   string         `json:"status,omitempty" enum:"draft,in_progress,in_review,approved,done,cancelled" doc:"Só para case solto; padrão draft."`
	Stage    string         `json:"stage,omitempty" doc:"Etapa inicial (key); padrão a primeira ativa."`
	Parent   string         `json:"parent,omitempty" doc:"Case pai (id ou identificador)."`
	Project  string         `json:"project,omitempty" doc:"Id ou slug do projeto."`
}

type createPipelineCaseIn struct {
	PipelinePath
	Body CaseBody
}

type createCaseIn struct {
	CompanyPath
	Body struct {
		CaseBody
		Pipeline string `json:"pipeline,omitempty" doc:"Id ou key do pipeline; vazio = case solto."`
	}
}

type listCasesIn struct {
	CompanyPath
	Page
	CaseType string `query:"type" doc:"Filtra pelo tipo."`
	Status   string `query:"status" doc:"Status separados por vírgula."`
	Pipeline string `query:"pipeline" doc:"Id ou key do pipeline."`
	Stage    string `query:"stage" doc:"Key da etapa (com pipeline)."`
	Project  string `query:"project" doc:"Id ou slug do projeto."`
	Parent   string `query:"parent" doc:"Só os filhos deste case."`
	Roots    bool   `query:"roots" doc:"Só os cases sem pai."`
	Loose    bool   `query:"loose" doc:"Só os cases soltos (fora de pipeline)."`
	Q        string `query:"q" maxLength:"200" doc:"Busca no título, no resumo e na key."`
}

type listPipelineCasesIn struct {
	PipelinePath
	Page
	Stage string `query:"stage" doc:"Key da etapa."`
}

type updateCaseIn struct {
	CasePath
	Body struct {
		ExpectedVersion int            `json:"expected_version" minimum:"1" doc:"A versão que você leu."`
		Title           *string        `json:"title,omitempty" minLength:"1" maxLength:"300"`
		Summary         *string        `json:"summary,omitempty"`
		Fields          map[string]any `json:"fields,omitempty" doc:"Merge: valor null apaga a chave."`
		ReplaceFields   bool           `json:"replace_fields,omitempty" doc:"Troca o mapa inteiro em vez do merge."`
		Status          string         `json:"status,omitempty" enum:"draft,in_progress,in_review,approved,done,cancelled" doc:"Só para case solto."`
		Project         string         `json:"project,omitempty"`
		ClearProject    bool           `json:"clear_project,omitempty"`
		Parent          string         `json:"parent,omitempty"`
		ClearParent     bool           `json:"clear_parent,omitempty"`
		ClearAttention  bool           `json:"clear_attention,omitempty" doc:"Dispensa o item de atenção da fila de revisão."`
	}
}

type transitionCaseIn struct {
	CasePath
	Body struct {
		ExpectedVersion int    `json:"expected_version" minimum:"1"`
		ToStage         string `json:"to_stage" minLength:"1" doc:"Key da etapa de destino."`
		Force           bool   `json:"force,omitempty" doc:"Modo estrito: passa fora das transições (com reason; só member humano)."`
		Reason          string `json:"reason,omitempty" maxLength:"4000"`
	}
}

type suggestCaseIn struct {
	CasePath
	Body struct {
		ExpectedVersion int     `json:"expected_version" minimum:"1"`
		ToStage         string  `json:"to_stage" minLength:"1"`
		Rationale       string  `json:"rationale" minLength:"1" maxLength:"4000"`
		Confidence      float64 `json:"confidence" minimum:"0" maximum:"1"`
	}
}

type resolveSuggestionIn struct {
	CasePath
	Body struct {
		ExpectedVersion int    `json:"expected_version" minimum:"1"`
		Accept          bool   `json:"accept" doc:"true move para a etapa sugerida; false dispensa."`
		Reason          string `json:"reason,omitempty" maxLength:"4000"`
	}
}

type reviewCaseIn struct {
	CasePath
	Body struct {
		ExpectedVersion int            `json:"expected_version" minimum:"1"`
		Decision        string         `json:"decision" enum:"approve,reject,request_changes"`
		Reason          string         `json:"reason,omitempty" maxLength:"4000" doc:"Obrigatório para recusar e pedir mudanças (conforme a etapa)."`
		Title           *string        `json:"title,omitempty" minLength:"1" maxLength:"300" doc:"Edições aplicadas antes de mover."`
		Summary         *string        `json:"summary,omitempty"`
		Fields          map[string]any `json:"fields,omitempty"`
	}
}

type linkCaseIn struct {
	CasePath
	Body struct {
		Task string `json:"task" minLength:"1" doc:"Chave ou id da task. Com run token, só a task da execução."`
		Role string `json:"role,omitempty" enum:"origin,work,automation,conversation" doc:"Padrão work."`
	}
}

type unlinkCaseIn struct {
	CasePath
	Task string `path:"task" doc:"Chave ou id da task."`
}

type setBlockersIn struct {
	CasePath
	Body struct {
		ExpectedVersion int      `json:"expected_version" minimum:"1"`
		Blockers        []string `json:"blockers" maxItems:"50" doc:"Cases (id ou identificador) que bloqueiam este; vazio limpa."`
	}
}

type caseEventsIn struct {
	CasePath
	Limit int `query:"limit" minimum:"1" maximum:"500" default:"100"`
}

type bulkReviewIn struct {
	CompanyPath
	Body struct {
		Items []struct {
			Case            string `json:"case" minLength:"1" doc:"Id ou identificador do case."`
			ExpectedVersion int    `json:"expected_version" minimum:"1"`
			Reason          string `json:"reason,omitempty" maxLength:"4000"`
		} `json:"items" minItems:"1" maxItems:"100"`
	}
}

type bulkReviewOut struct {
	Body struct {
		Results []BulkResult `json:"results"`
		Failed  int          `json:"failed"`
	}
}

type learningsIn struct {
	CompanyPath
	Page
	Pipeline string `query:"pipeline" doc:"Id ou key do pipeline."`
	Stage    string `query:"stage" doc:"Key da etapa de onde o case saiu (com pipeline)."`
	Decision string `query:"decision" enum:"approve,reject,request_changes,forced"`
}

// DecisionExamplePath identifica um exemplo.
type DecisionExamplePath struct {
	CompanyPath
	Example string `path:"example" format:"uuid" doc:"Id do exemplo."`
}

type listExamplesIn struct {
	CompanyPath
	Page
	SourceKind string `query:"source_kind" enum:"gate,case_review"`
	SourceID   string `query:"source_id" doc:"Id do gate ou do evento de revisão."`
}

type createExampleIn struct {
	CompanyPath
	Body struct {
		SourceKind string `json:"source_kind" enum:"gate,case_review"`
		SourceID   string `json:"source_id" format:"uuid" doc:"Id do gate decidido ou do evento review_decided."`
		Notes      string `json:"notes,omitempty" maxLength:"16384" doc:"O que este exemplo ensina (markdown)."`
	}
}

type updateExampleIn struct {
	DecisionExamplePath
	Body struct {
		Notes string `json:"notes" maxLength:"16384"`
	}
}

// ---------- resolução ----------

func (s *server) pipelineID(ctx context.Context, b pipelinesBackend, cid domain.ID, ref string) (service.PipelineView, error) {
	v, err := b.GetPipeline(ctx, cid, ref)
	if err != nil {
		return service.PipelineView{}, s.fail(ctx, err)
	}
	return v, nil
}

func (s *server) caseID(ctx context.Context, b pipelinesBackend, cid domain.ID, ref string) (domain.ID, error) {
	id, err := b.ResolveCase(ctx, cid, ref)
	if err != nil {
		return domain.ID{}, s.fail(ctx, err)
	}
	return id, nil
}

func (s *server) caseRef(ctx context.Context, b pipelinesBackend, cid domain.ID, ref string) (*domain.ID, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, nil
	}
	id, err := b.ResolveCase(ctx, cid, ref)
	if err != nil {
		return nil, s.fail(ctx, fmt.Errorf("resolver %q: %w", ref, err))
	}
	return &id, nil
}

func (s *server) stageIn(ctx context.Context, cid domain.ID, b StageBody) (service.StageInput, error) {
	cfg, err := s.stageConfigIn(ctx, cid, b.Config)
	if err != nil {
		return service.StageInput{}, err
	}
	return service.StageInput{Key: b.Key, Name: b.Name, Kind: domain.StageKind(b.Kind), Config: cfg}, nil
}

func (s *server) caseInput(ctx context.Context, b pipelinesBackend, sc scope, body CaseBody) (service.CreateCaseInput, error) {
	cid := sc.Company.ID
	in := service.CreateCaseInput{Type: body.CaseType, Key: body.Key, Title: body.Title, Summary: body.Summary, Fields: body.Fields,
		Status: domain.CaseStatus(body.Status), StageKey: body.Stage}
	var err error
	if in.ParentID, err = s.caseRef(ctx, b, cid, body.Parent); err != nil {
		return in, err
	}
	if in.ProjectID, err = s.projectRef(ctx, cid, body.Project); err != nil {
		return in, s.fail(ctx, err)
	}
	in.RunID, in.TaskID = runRefs(sc)
	return in, nil
}

func splitStatuses(raw string) []domain.CaseStatus {
	var out []domain.CaseStatus
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, domain.CaseStatus(p))
		}
	}
	return out
}

// ---------- rotas ----------

func (s *server) registerPipelines(api huma.API) {
	s.registerPipelineRoutes(api)
	s.registerCaseRoutes(api)
	s.registerReviewRoutes(api)
}

func (s *server) registerPipelineRoutes(api huma.API) {
	huma.Register(api, created(op("create-pipeline", http.MethodPost, "/companies/{company}/pipelines", "pipelines", "Cria um pipeline com etapas (admin)")),
		func(ctx context.Context, in *createPipelineIn) (*ItemOut[Pipeline], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			pin := service.CreatePipelineInput{Key: in.Body.Key, Name: in.Body.Name, Description: in.Body.Description, EnforceTransitions: in.Body.EnforceTransitions}
			if pin.ProjectID, err = s.projectRef(ctx, cid, in.Body.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			for _, st := range in.Body.Stages {
				si, err := s.stageIn(ctx, cid, st)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				pin.Stages = append(pin.Stages, si)
			}
			v, err := b.CreatePipeline(ctx, sc.Actor, cid, pin)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Pipeline]{Body: pipelineOut(v)}, nil
		})

	huma.Register(api, op("list-pipelines", http.MethodGet, "/companies/{company}/pipelines", "pipelines", "Lista os pipelines com etapas e contagens"),
		func(ctx context.Context, in *listPipelinesIn) (*ListOut[Pipeline], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			vs, err := b.ListPipelines(ctx, sc.Company.ID, in.Archived)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(vs, in.Page, pipelineOut), nil
		})

	huma.Register(api, op("get-pipeline", http.MethodGet, "/companies/{company}/pipelines/{pipeline}", "pipelines", "Obtém um pipeline"),
		func(ctx context.Context, in *PipelinePath) (*ItemOut[Pipeline], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			return &ItemOut[Pipeline]{Body: pipelineOut(v)}, nil
		})

	huma.Register(api, op("update-pipeline", http.MethodPatch, "/companies/{company}/pipelines/{pipeline}", "pipelines", "Altera, arquiva ou reativa um pipeline (admin)"),
		func(ctx context.Context, in *updatePipelineIn) (*ItemOut[Pipeline], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			up := service.UpdatePipelineInput{Key: in.Body.Key, Name: in.Body.Name, Description: in.Body.Description, ClearProject: in.Body.ClearProject,
				EnforceTransitions: in.Body.EnforceTransitions, Archived: in.Body.Archived}
			if up.ProjectID, err = s.projectRef(ctx, sc.Company.ID, in.Body.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := b.UpdatePipeline(ctx, sc.Actor, sc.Company.ID, cur.ID, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Pipeline]{Body: pipelineOut(v)}, nil
		})

	del := op("delete-pipeline", http.MethodDelete, "/companies/{company}/pipelines/{pipeline}", "pipelines", "Exclui um pipeline sem cases (admin); com cases, arquive")
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *PipelinePath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		b, err := s.pipes()
		if err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		cur, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
		if err != nil {
			return nil, err
		}
		if err := b.DeletePipeline(ctx, sc.Actor, sc.Company.ID, cur.ID); err != nil {
			return nil, s.fail(ctx, err)
		}
		return nil, nil
	})

	huma.Register(api, created(op("add-pipeline-stage", http.MethodPost, "/companies/{company}/pipelines/{pipeline}/stages", "pipelines", "Acrescenta uma etapa (admin)")),
		func(ctx context.Context, in *addStageIn) (*ItemOut[Pipeline], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			si, err := s.stageIn(ctx, sc.Company.ID, in.Body.StageBody)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := b.AddStage(ctx, sc.Actor, sc.Company.ID, cur.ID, si, in.Body.Position)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Pipeline]{Body: pipelineOut(v)}, nil
		})

	huma.Register(api, op("reorder-pipeline-stages", http.MethodPut, "/companies/{company}/pipelines/{pipeline}/stages/order", "pipelines", "Reordena as etapas (admin)"),
		func(ctx context.Context, in *reorderStagesIn) (*ItemOut[Pipeline], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			v, err := b.ReorderStages(ctx, sc.Actor, sc.Company.ID, cur.ID, in.Body.Stages)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Pipeline]{Body: pipelineOut(v)}, nil
		})

	huma.Register(api, op("update-pipeline-stage", http.MethodPatch, "/companies/{company}/pipelines/{pipeline}/stages/{stage}", "pipelines", "Altera uma etapa (admin)"),
		func(ctx context.Context, in *updateStageIn) (*ItemOut[Pipeline], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			up := service.UpdateStageInput{Key: in.Body.Key, Name: in.Body.Name}
			if in.Body.Kind != nil {
				k := domain.StageKind(*in.Body.Kind)
				up.Kind = &k
			}
			if in.Body.Config != nil {
				cfg, err := s.stageConfigIn(ctx, sc.Company.ID, *in.Body.Config)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				up.Config = &cfg
			}
			v, err := b.UpdateStage(ctx, sc.Actor, sc.Company.ID, cur.ID, in.Stage, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Pipeline]{Body: pipelineOut(v)}, nil
		})

	huma.Register(api, op("delete-pipeline-stage", http.MethodDelete, "/companies/{company}/pipelines/{pipeline}/stages/{stage}", "pipelines", "Remove uma etapa sem cases (admin)"),
		func(ctx context.Context, in *StagePath) (*ItemOut[Pipeline], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			v, err := b.DeleteStage(ctx, sc.Actor, sc.Company.ID, cur.ID, in.Stage)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Pipeline]{Body: pipelineOut(v)}, nil
		})

	huma.Register(api, op("set-pipeline-transitions", http.MethodPut, "/companies/{company}/pipelines/{pipeline}/transitions", "pipelines",
		"Troca as transições permitidas e o modo estrito (admin)"),
		func(ctx context.Context, in *setTransitionsIn) (*ItemOut[Pipeline], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			specs := make([]service.TransitionSpec, 0, len(in.Body.Transitions))
			for _, t := range in.Body.Transitions {
				specs = append(specs, service.TransitionSpec{From: t.From, To: t.To, Label: t.Label})
			}
			v, err := b.SetTransitions(ctx, sc.Actor, sc.Company.ID, cur.ID, specs, in.Body.Enforce)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Pipeline]{Body: pipelineOut(v)}, nil
		})

	huma.Register(api, created(op("create-pipeline-case", http.MethodPost, "/companies/{company}/pipelines/{pipeline}/cases", "cases",
		"Cria um case no pipeline (na etapa inicial; upsert por tipo+key)")),
		func(ctx context.Context, in *createPipelineCaseIn) (*ItemOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pv, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			ci, err := s.caseInput(ctx, b, sc, in.Body)
			if err != nil {
				return nil, err
			}
			ci.PipelineID = &pv.ID
			v, isNew, err := b.CreateCase(ctx, sc.Actor, sc.Company.ID, ci)
			if err != nil {
				return nil, s.pipeFail(ctx, err)
			}
			out := caseOut(v)
			out.Created = &isNew
			return &ItemOut[Case]{Body: out}, nil
		})

	huma.Register(api, op("list-pipeline-cases", http.MethodGet, "/companies/{company}/pipelines/{pipeline}/cases", "cases", "Lista os cases do pipeline"),
		func(ctx context.Context, in *listPipelineCasesIn) (*ListOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pv, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
			if err != nil {
				return nil, err
			}
			f := service.CaseFilter{PipelineID: &pv.ID, Limit: 500}
			if in.Stage != "" {
				st, ok := pv.Stage(in.Stage)
				if !ok {
					return nil, s.fail(ctx, domain.Invalid("stage", "etapa desconhecida"))
				}
				f.StageID = &st.ID
			}
			vs, err := b.ListCases(ctx, sc.Company.ID, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(vs, in.Page, caseOut), nil
		})
}

func (s *server) registerCaseRoutes(api huma.API) {
	huma.Register(api, op("list-cases", http.MethodGet, "/companies/{company}/cases", "cases", "Lista cases com filtros"),
		func(ctx context.Context, in *listCasesIn) (*ListOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			f := service.CaseFilter{Type: in.CaseType, Statuses: splitStatuses(in.Status), RootsOnly: in.Roots, LooseOnly: in.Loose, Q: in.Q,
				Limit: in.Limit, Offset: in.Offset}
			if in.Pipeline != "" {
				pv, err := s.pipelineID(ctx, b, cid, in.Pipeline)
				if err != nil {
					return nil, err
				}
				f.PipelineID = &pv.ID
				if in.Stage != "" {
					st, ok := pv.Stage(in.Stage)
					if !ok {
						return nil, s.fail(ctx, domain.Invalid("stage", "etapa desconhecida"))
					}
					f.StageID = &st.ID
				}
			} else if in.Stage != "" {
				return nil, s.fail(ctx, domain.Invalid("stage", "filtrar por etapa exige pipeline"))
			}
			if f.ProjectID, err = s.projectRef(ctx, cid, in.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			if f.ParentID, err = s.caseRef(ctx, b, cid, in.Parent); err != nil {
				return nil, err
			}
			vs, err := b.ListCases(ctx, cid, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(vs, in.Page, caseOut), nil
		})

	huma.Register(api, created(op("create-case", http.MethodPost, "/companies/{company}/cases", "cases", "Cria um case solto ou num pipeline (upsert por tipo+key)")),
		func(ctx context.Context, in *createCaseIn) (*ItemOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ci, err := s.caseInput(ctx, b, sc, in.Body.CaseBody)
			if err != nil {
				return nil, err
			}
			if in.Body.Pipeline != "" {
				pv, err := s.pipelineID(ctx, b, sc.Company.ID, in.Body.Pipeline)
				if err != nil {
					return nil, err
				}
				ci.PipelineID = &pv.ID
			}
			v, isNew, err := b.CreateCase(ctx, sc.Actor, sc.Company.ID, ci)
			if err != nil {
				return nil, s.pipeFail(ctx, err)
			}
			out := caseOut(v)
			out.Created = &isNew
			return &ItemOut[Case]{Body: out}, nil
		})

	huma.Register(api, op("get-case", http.MethodGet, "/companies/{company}/cases/{case}", "cases", "Obtém um case com tasks, bloqueios e histórico"),
		func(ctx context.Context, in *CasePath) (*ItemOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := b.GetCaseView(ctx, sc.Company.ID, in.Case)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, op("update-case", http.MethodPatch, "/companies/{company}/cases/{case}", "cases", "Atualiza um case (expected_version obrigatória)"),
		func(ctx context.Context, in *updateCaseIn) (*ItemOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			id, err := s.caseID(ctx, b, cid, in.Case)
			if err != nil {
				return nil, err
			}
			bd := in.Body
			up := service.UpdateCaseInput{ExpectedVersion: bd.ExpectedVersion, Title: bd.Title, Summary: bd.Summary, Fields: bd.Fields, ReplaceFields: bd.ReplaceFields,
				ClearProject: bd.ClearProject, ClearParent: bd.ClearParent, ClearAttention: bd.ClearAttention}
			if bd.Status != "" {
				st := domain.CaseStatus(bd.Status)
				up.Status = &st
			}
			if up.ProjectID, err = s.projectRef(ctx, cid, bd.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			if up.ParentID, err = s.caseRef(ctx, b, cid, bd.Parent); err != nil {
				return nil, err
			}
			up.RunID, up.TaskID = runRefs(sc)
			v, err := b.UpdateCase(ctx, sc.Actor, cid, id, up)
			if err != nil {
				return nil, s.pipeFail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, op("transition-case", http.MethodPost, "/companies/{company}/cases/{case}/transition", "cases",
		"Move o case para outra etapa (regras de aprovação, bloqueios, filhos e modo estrito)"),
		func(ctx context.Context, in *transitionCaseIn) (*ItemOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			ti := service.TransitionCaseInput{ExpectedVersion: in.Body.ExpectedVersion, ToStage: in.Body.ToStage, Force: in.Body.Force, Reason: in.Body.Reason}
			ti.RunID, ti.TaskID = runRefs(sc)
			v, err := b.TransitionCase(ctx, sc.Actor, sc.Company.ID, id, ti)
			if err != nil {
				return nil, s.pipeFail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, op("suggest-case-transition", http.MethodPost, "/companies/{company}/cases/{case}/suggest", "cases",
		"Sugere mover o case para uma etapa, com justificativa e confiança (a decisão é humana)"),
		func(ctx context.Context, in *suggestCaseIn) (*ItemOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			si := service.SuggestInput{ExpectedVersion: in.Body.ExpectedVersion, ToStage: in.Body.ToStage, Rationale: in.Body.Rationale, Confidence: in.Body.Confidence}
			si.RunID, si.TaskID = runRefs(sc)
			v, err := b.SuggestCaseTransition(ctx, sc.Actor, sc.Company.ID, id, si)
			if err != nil {
				return nil, s.pipeFail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, op("resolve-case-suggestion", http.MethodPost, "/companies/{company}/cases/{case}/resolve-suggestion", "cases",
		"Aceita ou dispensa a sugestão pendente (humano)"),
		func(ctx context.Context, in *resolveSuggestionIn) (*ItemOut[Case], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			v, err := b.ResolveSuggestion(ctx, sc.Actor, sc.Company.ID, id, service.ResolveSuggestionInput{ExpectedVersion: in.Body.ExpectedVersion,
				Accept: in.Body.Accept, Reason: in.Body.Reason})
			if err != nil {
				return nil, s.pipeFail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, op("review-case", http.MethodPost, "/companies/{company}/cases/{case}/review", "cases",
		"Decide a revisão do case (aprovador humano da etapa)"),
		func(ctx context.Context, in *reviewCaseIn) (*ItemOut[Case], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			v, err := b.ReviewCase(ctx, sc.Actor, sc.Company.ID, id, service.ReviewCaseInput{ExpectedVersion: in.Body.ExpectedVersion,
				Decision: domain.ReviewDecision(in.Body.Decision), Reason: in.Body.Reason, Title: in.Body.Title, Summary: in.Body.Summary, Fields: in.Body.Fields})
			if err != nil {
				return nil, s.pipeFail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, created(op("link-case-task", http.MethodPost, "/companies/{company}/cases/{case}/links", "cases", "Liga uma task ao case")),
		func(ctx context.Context, in *linkCaseIn) (*ItemOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			tid, err := s.taskRef(ctx, sc.Company.ID, in.Body.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			runID, _ := runRefs(sc)
			v, err := b.LinkCaseTask(ctx, sc.Actor, sc.Company.ID, id, *tid, domain.CaseLinkRole(in.Body.Role), runID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, op("unlink-case-task", http.MethodDelete, "/companies/{company}/cases/{case}/links/{task}", "cases", "Desfaz a ligação de uma task (humano)"),
		func(ctx context.Context, in *unlinkCaseIn) (*ItemOut[Case], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			tid, err := s.taskRef(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := b.UnlinkCaseTask(ctx, sc.Actor, sc.Company.ID, id, *tid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, op("set-case-blockers", http.MethodPut, "/companies/{company}/cases/{case}/blockers", "cases",
		"Troca os cases que bloqueiam este (resolvem quando chegam a done)"),
		func(ctx context.Context, in *setBlockersIn) (*ItemOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			ids := make([]domain.ID, 0, len(in.Body.Blockers))
			for _, r := range in.Body.Blockers {
				bid, err := s.caseID(ctx, b, sc.Company.ID, r)
				if err != nil {
					return nil, err
				}
				ids = append(ids, bid)
			}
			runID, _ := runRefs(sc)
			v, err := b.SetCaseBlockers(ctx, sc.Actor, sc.Company.ID, id, in.Body.ExpectedVersion, ids, runID)
			if err != nil {
				return nil, s.pipeFail(ctx, err)
			}
			return &ItemOut[Case]{Body: caseOut(v)}, nil
		})

	huma.Register(api, op("list-case-events", http.MethodGet, "/companies/{company}/cases/{case}/events", "cases", "Histórico do case (mais recentes primeiro)"),
		func(ctx context.Context, in *caseEventsIn) (*ListOut[CaseEvent], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			evs, err := b.ListCaseEvents(ctx, sc.Company.ID, id, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(evs, Page{Limit: 500}, caseEventOut), nil
		})

	huma.Register(api, op("list-case-children", http.MethodGet, "/companies/{company}/cases/{case}/children", "cases", "Lista os filhos do case"),
		func(ctx context.Context, in *CasePath) (*ListOut[Case], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			vs, err := b.ListChildCases(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(vs, Page{Limit: 500}, caseOut), nil
		})

	huma.Register(api, op("get-case-context", http.MethodGet, "/companies/{company}/cases/{case}/context", "cases",
		"O pacote de contexto do case (o que vai na task de automação), com os aprendizados da etapa"),
		func(ctx context.Context, in *CasePath) (*ItemOut[CaseContext], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			md, err := b.GetCaseContext(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[CaseContext]{Body: CaseContext{Markdown: md}}, nil
		})

	huma.Register(api, created(op("open-case-conversation", http.MethodPost, "/companies/{company}/cases/{case}/conversation", "cases",
		"Abre uma task \"Discutir: <título>\" ligada ao case (humano)")),
		func(ctx context.Context, in *CasePath) (*ItemOut[Task], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.caseID(ctx, b, sc.Company.ID, in.Case)
			if err != nil {
				return nil, err
			}
			tv, err := b.OpenCaseConversation(ctx, sc.Actor, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(tv)}, nil
		})
}

func (s *server) registerReviewRoutes(api huma.API) {
	huma.Register(api, op("review-queue", http.MethodGet, "/companies/{company}/review-queue", "review", "Fila de revisão: o que espera decisão humana"),
		func(ctx context.Context, in *struct {
			CompanyPath
			Page
		},
		) (*ListOut[ReviewQueueItem], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			items, err := b.ReviewQueue(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(items, in.Page, reviewItemOut), nil
		})

	huma.Register(api, op("review-queue-bulk-approve", http.MethodPost, "/companies/{company}/review-queue/bulk", "review",
		"Aprova vários cases (cada um com a versão vista); o resultado é por item"),
		func(ctx context.Context, in *bulkReviewIn) (*bulkReviewOut, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			items := make([]service.BulkReviewItem, 0, len(in.Body.Items))
			for _, it := range in.Body.Items {
				id, err := s.caseID(ctx, b, sc.Company.ID, it.Case)
				if err != nil {
					return nil, err
				}
				items = append(items, service.BulkReviewItem{CaseID: id, ExpectedVersion: it.ExpectedVersion, Reason: it.Reason})
			}
			res, err := b.BulkApprove(ctx, sc.Actor, sc.Company.ID, items)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := &bulkReviewOut{}
			out.Body.Results = make([]BulkResult, 0, len(res))
			for _, r := range res {
				msg := r.Error
				if r.Err != nil {
					if p, _ := toProblem(r.Err); p != nil {
						msg = p.Detail
					}
				}
				out.Body.Results = append(out.Body.Results, BulkResult{Case: idStr(r.CaseID), OK: r.OK, Version: r.Version, Error: msg, CurrentVersion: r.CurrentVersion})
				if !r.OK {
					out.Body.Failed++
				}
			}
			return out, nil
		})

	huma.Register(api, op("list-learnings", http.MethodGet, "/companies/{company}/learnings", "review",
		"Aprendizados: decisões de revisão e transições forçadas, mais recentes primeiro"),
		func(ctx context.Context, in *learningsIn) (*ListOut[Learning], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			f := service.LearningFilter{Decision: in.Decision, Limit: in.Limit, Offset: in.Offset}
			if in.Pipeline != "" {
				pv, err := s.pipelineID(ctx, b, sc.Company.ID, in.Pipeline)
				if err != nil {
					return nil, err
				}
				f.PipelineID = &pv.ID
				if in.Stage != "" {
					st, ok := pv.Stage(in.Stage)
					if !ok {
						return nil, s.fail(ctx, domain.Invalid("stage", "etapa desconhecida"))
					}
					f.StageID = &st.ID
				}
			}
			ls, err := b.ListLearnings(ctx, sc.Company.ID, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(ls, Page{Limit: 500}, learningOut), nil
		})

	huma.Register(api, op("list-decision-examples", http.MethodGet, "/companies/{company}/decision-examples", "review", "Lista os exemplos de decisão"),
		func(ctx context.Context, in *listExamplesIn) (*ListOut[DecisionExample], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			f := service.DecisionExampleFilter{SourceKind: domain.DecisionSource(in.SourceKind), Limit: in.Limit, Offset: in.Offset}
			if in.SourceID != "" {
				id, err := domain.ParseID(in.SourceID)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("source_id", "não é um uuid"))
				}
				f.SourceID = &id
			}
			xs, err := b.ListDecisionExamples(ctx, sc.Company.ID, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(xs, Page{Limit: 500}, func(e domain.DecisionExample) DecisionExample { return decisionExampleOut(e, false) }), nil
		})

	huma.Register(api, created(op("create-decision-example", http.MethodPost, "/companies/{company}/decision-examples", "review",
		"Salva uma decisão (gate decidido ou revisão de case) como exemplo, com notas (humano)")),
		func(ctx context.Context, in *createExampleIn) (*ItemOut[DecisionExample], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			src, err := domain.ParseID(in.Body.SourceID)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("source_id", "não é um uuid"))
			}
			e, err := b.SaveDecisionExample(ctx, sc.Actor, sc.Company.ID, service.SaveDecisionExampleInput{SourceKind: domain.DecisionSource(in.Body.SourceKind),
				SourceID: src, Notes: in.Body.Notes})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[DecisionExample]{Body: decisionExampleOut(e, true)}, nil
		})

	exp := op("export-decision-examples", http.MethodGet, "/companies/{company}/decision-examples/export.jsonl", "review",
		"Exporta os exemplos de decisão em JSONL, um por linha (admin)")
	exp.Responses = map[string]*huma.Response{"200": {Description: "Um objeto JSON por linha (application/x-ndjson).",
		Content: map[string]*huma.MediaType{"application/x-ndjson": {Schema: &huma.Schema{Type: "string"}}}}}
	huma.Register(api, exp, func(ctx context.Context, in *CompanyPath) (*huma.StreamResponse, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		b, err := s.pipes()
		if err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		// lê tudo antes de responder: um erro de papel ou de banco vira status, não um corpo pela metade
		var buf strings.Builder
		if _, err := b.ExportDecisionExamples(ctx, sc.Company.ID, &buf); err != nil {
			return nil, s.fail(ctx, err)
		}
		body := buf.String()
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", "application/x-ndjson; charset=utf-8")
			hctx.SetHeader("Content-Disposition", `attachment; filename="decision-examples.jsonl"`)
			hctx.SetHeader("X-Content-Type-Options", "nosniff")
			hctx.SetStatus(http.StatusOK)
			_, _ = io.WriteString(hctx.BodyWriter(), body)
		}}, nil
	})

	huma.Register(api, op("get-decision-example", http.MethodGet, "/companies/{company}/decision-examples/{example}", "review", "Obtém um exemplo com o snapshot"),
		func(ctx context.Context, in *DecisionExamplePath) (*ItemOut[DecisionExample], error) {
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Example)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("example", "não é um uuid"))
			}
			e, err := b.GetDecisionExample(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[DecisionExample]{Body: decisionExampleOut(e, true)}, nil
		})

	huma.Register(api, op("update-decision-example", http.MethodPatch, "/companies/{company}/decision-examples/{example}", "review", "Edita as notas (só o autor)"),
		func(ctx context.Context, in *updateExampleIn) (*ItemOut[DecisionExample], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.pipes()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Example)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("example", "não é um uuid"))
			}
			e, err := b.UpdateDecisionExampleNotes(ctx, sc.Actor, sc.Company.ID, id, in.Body.Notes)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[DecisionExample]{Body: decisionExampleOut(e, true)}, nil
		})

	delEx := op("delete-decision-example", http.MethodDelete, "/companies/{company}/decision-examples/{example}", "review", "Apaga um exemplo (só o autor)")
	delEx.DefaultStatus = http.StatusNoContent
	huma.Register(api, delEx, func(ctx context.Context, in *DecisionExamplePath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		b, err := s.pipes()
		if err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		id, err := domain.ParseID(in.Example)
		if err != nil {
			return nil, s.fail(ctx, domain.Invalid("example", "não é um uuid"))
		}
		if err := b.DeleteDecisionExample(ctx, sc.Actor, sc.Company.ID, id); err != nil {
			return nil, s.fail(ctx, err)
		}
		return nil, nil
	})
}
