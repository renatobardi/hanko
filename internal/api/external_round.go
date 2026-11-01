package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Contrato da rodada e da aprovação externas (ADR-0080 e 0081), grupo 3 da v0.8 (#124). Cada operação já tem entrada e
// saída no OpenAPI e responde 501 até a fatia que a implementa; a descrição diz qual. Os limites de alerta (incluindo o
// de disco) já têm operação no grupo 2 (`alert-settings`, ADR-0083). O Hanko só observa: nenhuma operação marca, aprova
// nem roda algo no host. O texto de uma etapa e o script de um pedido saem do fato da ingestão, com permissão e
// `activity_event` (ADR-0080 item 3; ADR-0081 item 3).

// pendingRoundOp registra uma operação do contrato da rodada e da aprovação que ainda responde 501.
func pendingRoundOp[I, O any](api huma.API, o huma.Operation, slice string) {
	o.Description = "Contrato do grupo 3 da v0.8 (#124): rodada e aprovação externas (ADR-0080 e 0081). Responde 501 até a " + slice + "."
	o.Errors = append(o.Errors, http.StatusNotImplemented)
	huma.Register(api, o, func(context.Context, *I) (*O, error) {
		return nil, problem(http.StatusNotImplemented, "operação ainda não implementada")
	})
}

const sliceRoundApproval = "fatia 3 (#127)"

// ---------- recursos ----------

// ExternalRound é uma rodada do swarm observada pela ingestão (ADR-0080 item 2).
type ExternalRound struct {
	ID             string     `json:"id" format:"uuid"`
	RoundKey       string     `json:"round_key" example:"swarm-1027-0802" doc:"Id da rodada no agent-studio."`
	Name           string     `json:"name" example:"Brave_Otter" doc:"Nome amigável do gerador único (ADR-0084)."`
	SourceKind     string     `json:"source_kind" enum:"external"`
	CycleID        *string    `json:"cycle_id,omitempty" format:"uuid" doc:"O ciclo da issue, quando a triagem já trouxe o ciclo."`
	Repo           string     `json:"repo" doc:"Repositório da rodada; liga ao workspace pelo emparelhamento (ADR-0075)."`
	WorkspaceID    *string    `json:"workspace_id,omitempty" format:"uuid"`
	MaxSessions    *int       `json:"max_sessions,omitempty" doc:"Número de sessões que a rodada permite."`
	OpenedAt       *time.Time `json:"opened_at,omitempty"`
	ClosedAt       *time.Time `json:"closed_at,omitempty" doc:"Vazio enquanto a rodada está aberta."`
	LastEventAt    *time.Time `json:"last_event_at,omitempty" doc:"Último evento oute.swarm.* da rodada."`
	LastAskedAt    *time.Time `json:"last_asked_at,omitempty" doc:"Última pergunta ao Bardi."`
	LastAnsweredAt *time.Time `json:"last_answered_at,omitempty" doc:"Última resposta. Com a pergunta igual ou mais nova que ela, a pergunta está pendente (ADR-0080 item 6)."`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// RoundParticipant é um worker da rodada, ligado à sessão externa quando ela já chegou (ADR-0080 item 2).
type RoundParticipant struct {
	ID                string     `json:"id" format:"uuid"`
	Slug              string     `json:"slug"`
	IssueRef          string     `json:"issue_ref,omitempty" doc:"Issue <dono>/<repo>#<n> do worker."`
	ExternalSessionID *string    `json:"external_session_id,omitempty" format:"uuid"`
	SpawnedAt         *time.Time `json:"spawned_at,omitempty"`
	ClosedAt          *time.Time `json:"closed_at,omitempty"`
}

// ActionMark é a marca mais nova de uma ação. As marcas só se acrescentam (ADR-0080 item 4).
type ActionMark struct {
	State        string    `json:"state" enum:"done,pending"`
	MarkedByType string    `json:"marked_by_type" enum:"human,system"`
	MarkedByID   *string   `json:"marked_by_id,omitempty" format:"uuid"`
	MarkedAt     time.Time `json:"marked_at"`
	Migrated     bool      `json:"migrated" doc:"Marca trazida do agent-studio, atribuída ao Bardi pela migração."`
}

// StepAction é uma ação da etapa vigente, com a marca mais nova (ADR-0080 item 4). Ação com `approval_key` tem o estado
// do pedido do canal, não o da marca.
type StepAction struct {
	ActionID    string      `json:"action_id" example:"ab1"`
	ApprovalKey string      `json:"approval_key,omitempty" doc:"Pedido do canal que a ação cita (pedido:<id>)."`
	Mark        *ActionMark `json:"mark,omitempty"`
}

// RoundStep é a revisão vigente de uma etapa. O texto não vem aqui: fica no fato e sai pela operação de conteúdo (ADR-0080 item 3).
type RoundStep struct {
	ID             string       `json:"id" format:"uuid"`
	Kind           string       `json:"kind" enum:"triage,merge,kaizen,closing,cycle_summary"`
	StepKey        string       `json:"step_key,omitempty" doc:"Número do PR, na etapa merge."`
	Revision       int64        `json:"revision" doc:"A revisão mais alta vence, em qualquer ordem de chegada."`
	SHA256         string       `json:"sha256"`
	Review         string       `json:"review" enum:"approved,rejected,no_reviewer"`
	ReferenceCheck string       `json:"reference_check" enum:"ok,failed,missing"`
	PublishedAt    time.Time    `json:"published_at"`
	Actions        []StepAction `json:"actions"`
}

// ExternalRoundDetail é a rodada com participantes e etapas (ADR-0080 itens 1 a 4).
type ExternalRoundDetail struct {
	ExternalRound
	Participants []RoundParticipant `json:"participants"`
	Steps        []RoundStep        `json:"steps"`
}

// ExternalCycle é o ciclo de uma issue: as rodadas e o resumo no topo (ADR-0080 item 7).
type ExternalCycle struct {
	ID        string          `json:"id" format:"uuid"`
	IssueRef  string          `json:"issue_ref" example:"renatobardi/oute-agent#12"`
	Summary   *RoundStep      `json:"summary,omitempty" doc:"A etapa cycle_summary, quando já chegou."`
	Rounds    []ExternalRound `json:"rounds"`
	CreatedAt time.Time       `json:"created_at"`
}

// ExternalApproval é um pedido do canal de aprovação observado no host (ADR-0081 itens 1 a 5). O script não vem aqui.
type ExternalApproval struct {
	ID            string     `json:"id" format:"uuid"`
	CanalID       string     `json:"canal_id" example:"20261027-080000-cafe"`
	Title         string     `json:"title"`
	RequestedAs   string     `json:"requested_as,omitempty" doc:"Usuário que pediu (campo as)."`
	HostName      string     `json:"host_name,omitempty"`
	HostID        *string    `json:"host_id,omitempty" format:"uuid"`
	AgentID       *string    `json:"agent_id,omitempty" format:"uuid"`
	State         string     `json:"state" enum:"pending,decided"`
	Decision      *string    `json:"decision,omitempty" enum:"rejected,executed"`
	Approver      *string    `json:"approver,omitempty" enum:"human,human_dark"`
	ApprovedBy    string     `json:"approved_by,omitempty" doc:"Nome de quem aprovou ou recusou, como o host o declarou."`
	RC            *int       `json:"rc,omitempty"`
	DurationS     *int       `json:"duration_s,omitempty" doc:"Duração da execução no host, em segundos."`
	OutputBytes   *int       `json:"output_bytes,omitempty" doc:"Tamanho da saída; a saída nunca sai do host."`
	ScriptSHA256  string     `json:"script_sha256" doc:"Resumo do script que a página mostra, no formato do oute approve."`
	DecidedSHA256 *string    `json:"decided_sha256,omitempty" doc:"Resumo do que rodou no host. Pode faltar, em especial numa recusa."`
	Versions      int        `json:"versions" doc:"Quantas versões diferentes do pedido chegaram. Com mais de uma, a página avisa."`
	RequestedAt   *time.Time `json:"requested_at,omitempty"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
	ListedAt      *time.Time `json:"listed_at,omitempty" doc:"Última vez que esteve na lista da outbox do host."`
}

// ApprovalVersion é uma versão do script que chegou para um pedido.
type ApprovalVersion struct {
	SHA256 string    `json:"sha256"`
	SeenAt time.Time `json:"seen_at"`
}

// ApprovalScript é o corpo do script de um pedido. Só owner e admin leem; cada leitura gera activity_event (ADR-0081 item 3).
type ApprovalScript struct {
	ApprovalID string            `json:"approval_id" format:"uuid"`
	SHA256     string            `json:"sha256"`
	Script     string            `json:"script"`
	Versions   []ApprovalVersion `json:"versions"`
}

// ---------- entradas ----------

type ExternalRoundPath struct {
	CompanyPath
	Round string `path:"round" format:"uuid" doc:"Id da rodada externa."`
}

type RoundStepPath struct {
	ExternalRoundPath
	Step string `path:"step" format:"uuid" doc:"Id da etapa da rodada."`
}

type RoundActionPath struct {
	RoundStepPath
	Action string `path:"action" pattern:"^[a-z][a-z0-9]{0,15}$" doc:"Id da ação na etapa (o id que o dispatcher escreveu)."`
}

type ExternalCyclePath struct {
	CompanyPath
	Cycle string `path:"cycle" format:"uuid" doc:"Id do ciclo."`
}

type ExternalApprovalPath struct {
	CompanyPath
	Approval string `path:"approval" format:"uuid" doc:"Id da aprovação externa."`
}

type listExternalRoundsIn struct {
	CompanyPath
	Page
	Cycle string `query:"cycle" format:"uuid" doc:"Só as rodadas deste ciclo."`
}

type listExternalApprovalsIn struct {
	CompanyPath
	Page
	State string `query:"state" enum:"pending,decided" doc:"Só os pedidos neste estado."`
}

type markRoundActionIn struct {
	RoundActionPath
	Body struct {
		State string `json:"state" enum:"done,pending" doc:"A marca nova. Só se acrescenta: a mais nova vence (owner ou admin, com reautenticação recente)."`
	}
}

// ---------- registro ----------

func (s *server) registerExternalRounds(api huma.API) {
	const tag = "external-rounds"
	b := "/companies/{company}"
	s.registerRoundReads(api)

	pendingRoundOp[listExternalApprovalsIn, ListOut[ExternalApproval]](api, op("list-external-approvals", http.MethodGet, b+"/external-approvals", tag,
		"Lista os pedidos do canal de aprovação observados nos hosts, sem o script"), sliceRoundApproval)
	pendingRoundOp[ExternalApprovalPath, ItemOut[ExternalApproval]](api, op("get-external-approval", http.MethodGet, b+"/external-approvals/{approval}", tag,
		"Mostra um pedido do canal, com o sha256 e as versões; o comando oute approve <id> vai para copiar"), sliceRoundApproval)
	pendingRoundOp[ExternalApprovalPath, ItemOut[ApprovalScript]](api, op("get-approval-script", http.MethodGet, b+"/external-approvals/{approval}/script", tag,
		"Mostra o corpo do script de um pedido (owner ou admin; cada leitura gera activity_event)"), sliceRoundApproval)
}

// RoundStepContent é o texto de uma etapa, lido do fato (ADR-0080 item 3). Só owner e admin leem.
type RoundStepContent struct {
	StepID string `json:"step_id" format:"uuid"`
	Text   string `json:"text" doc:"Texto da revisão vigente, dado não confiável: o Markdown restrito se interpreta só em dados."`
}
