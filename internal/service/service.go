// Package service holds Hanko's use cases. One transaction per use case; every
// domain write emits an activity_event in the same transaction (docs/STACK.md §6).
// Handlers (api, cli, mcp, ui) and the heartbeat call only this package.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/acp"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/readiness"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/vault"
)

// ErrNotImplemented marks contract stubs not yet filled.
var ErrNotImplemented = errors.New("not implemented")

// Wake asks the heartbeat to consider running a persona on a task.
type Wake struct {
	CompanyID domain.ID         `json:"company_id"`
	PersonaID domain.ID         `json:"persona_id"`
	TaskID    domain.ID         `json:"task_id"`
	Trigger   domain.RunTrigger `json:"trigger"`
}

// Jobs enqueues background work inside the caller's transaction (river InsertTx).
// Implemented by internal/heartbeat. Must coalesce duplicate wakes.
type Jobs interface {
	WakeTx(ctx context.Context, tx pgx.Tx, w Wake) error
	ExecuteRunTx(ctx context.Context, tx pgx.Tx, companyID, runID domain.ID) error
	DeliverMemoryTx(ctx context.Context, tx pgx.Tx, outboxID domain.ID) error
	// StartReviewTx enqueues the start of a pending review (revisão automática antes do gate).
	StartReviewTx(ctx context.Context, tx pgx.Tx, companyID, reviewID domain.ID) error
}

// Clock is injectable for tests.
type Clock func() time.Time

// Service is the use-case layer.
type Service struct {
	Pool   *pgxpool.Pool
	Jobs   Jobs
	Broker *events.Broker
	Now    Clock
	// SearchStatementTimeout é o teto de cada consulta do banco da busca (ADR-0024); 0 usa DefaultSearchStatementTimeout.
	SearchStatementTimeout time.Duration
	// WorkspacesRoot restringe o caminho de novos workspaces a esta raiz (HANKO_WORKSPACES_ROOT).
	// Vazio = sem restrição de raiz (só caminho absoluto e limpo); o app sempre define.
	WorkspacesRoot string
	// RunReserveUSD é o piso da reserva de orçamento por run aberta (HANKO_RUN_RESERVE_USD);
	// 0 usa DefaultRunReserveUSD.
	RunReserveUSD float64
	// PasswordGate limita os argon2id simultâneos (hash e verificação de senha); New cria o padrão.
	PasswordGate *auth.PasswordGate
	// Admission é a política de admissão do provider firebase (HANKO_AUTH_ALLOWED_EMAILS/DOMAINS):
	// com listas, uma sessão (ou API key) de usuário firebase cujo e-mail saiu delas deixa de valer.
	Admission auth.Admission
	// Alerts são os limites dos alertas operacionais (HANKO_ALERT_*); zero usa os padrões.
	Alerts AlertConfig
	// OwnerEmail é HANKO_AUTH_OWNER_EMAIL (minúsculas): o dono nomeado passa mesmo fora das listas.
	OwnerEmail string
	// ExternalIdentity: o provider (firebase) prova a identidade por fora, então um convite não cria
	// conta com senha. Falso (provider local), o link do convite é a prova de identidade e convidar
	// e-mail sem conta exige owner da empresa (ADR-0021).
	ExternalIdentity bool
	// ExportPageSize é quantos eventos de uma execução o export lê por página; 0 usa o padrão (1000).
	ExportPageSize int
	// ExportIdleTxTimeout e ExportStatementTimeout são os tetos SET LOCAL da transação de snapshot do export;
	// 0 usa DefaultExportIdleTxTimeout/DefaultExportStatementTimeout.
	ExportIdleTxTimeout, ExportStatementTimeout time.Duration
	// ExportPageHook (só testes) roda antes de cada página de eventos do export; um erro interrompe a escrita.
	ExportPageHook func(page int) error
	// ArtifactMaxBytes é o tamanho máximo de um artefato (HANKO_ARTIFACT_MAX_BYTES) e ArtifactQuotaBytes a cota
	// por empresa, somando todas as versões (HANKO_ARTIFACT_QUOTA_BYTES); zero usa os padrões (5 MiB e 512 MiB).
	ArtifactMaxBytes, ArtifactQuotaBytes int64
	// PublicURL é a URL pública da instalação (HANKO_PUBLIC_URL): o OAuth das conexões volta por PublicURL/oauth/callback
	// (ADR-0043). Vazio = conexões OAuth não conseguem autorizar.
	PublicURL string
	// Integrations é a política e o cofre das integrações (ADR-0029). O zero value é o seguro: stdio desligado,
	// destinos internos bloqueados e sem chave de cifragem.
	Integrations IntegrationsConfig
	// ACP é a política do operador para o harness acp (HANKO_ACP_ALLOW / HANKO_ACP_COMMANDS, ADR-0023). O zero
	// value recusa: persona acp não pode ser contratada nem importada.
	ACP acp.Policy
	// Probers são os probes de readiness por harness (ADR-0056); o app os registra junto dos adapters.
	Probers map[string]adapter.Prober
	// Vault é o cofre externo de segredos (ADR-0050); nil = nenhum configurado (segredos provider=vault recusados).
	Vault vault.Provider
	// Blobs guarda o conteúdo dos artefatos (ADR-0051); nil usa o driver postgres sobre o pool.
	Blobs *storage.Registry
	// Settings são os toggles não sensíveis da instalação (ADR-0050); nil usa os campos fixos acima (testes).
	Settings *settings.Manager
	// Environments é a política do operador para os ambientes de execução remotos (ADR-0037): SSH e sandbox
	// vêm desligados no zero value.
	Environments execenv.Config
	// Memory é o provider de memória (ADR-0015), usado só pela busca (ADR-0024); nil ou Noop = sem memória na busca.
	Memory memory.Provider

	// MemoryProvider é o provedor de memória em uso ("ai-memory" ou "none"; vazio = none). Sem provedor a
	// consolidação de memória fica desligada (ADR-0030).
	MemoryProvider string
	// SkillSources é o interruptor do operador e os tetos das fontes de skills (HANKO_SKILL_SOURCES, ADR-0035).
	// O zero value é o seguro: fontes desligadas.
	SkillSources SkillSourcesConfig
	// Log recebe o registro de auditoria das operações administrativas da instalação (hanko auth user ...);
	// nil descarta.
	Log *slog.Logger
	// Plugins é a política do operador para plugins de terceiros (HANKO_PLUGINS_DIR, ADR-0038); zero = desligados.
	Plugins PluginsConfig
	// GitWorkspaces é o interruptor do operador dos workspaces de execução com Git (HANKO_GIT_WORKSPACES, ADR-0054).
	// O zero value é o seguro: desligados, o servidor não clona nada.
	GitWorkspaces GitWorkspacesConfig

	gitSt *gitState // executor do git e locks de espelho; ver gitState

	// Readiness são os binários dos harnesses locais e ReadinessEvery o intervalo da checagem periódica da persona
	// (HANKO_READINESS_INTERVAL, ADR-0055); zero desliga a checagem periódica (o app define DefaultReadinessInterval).
	Readiness      readiness.Config
	ReadinessEvery time.Duration
	// ReadinessRunner (só testes) troca a execução do script do probe; nil usa execenv.RunScript no ambiente da persona.
	ReadinessRunner func(ctx context.Context, target execenv.Target, script string, env map[string]string) ([]byte, error)

	// Push é a configuração do web push (ADR-0042); o zero value serve (precisa de HANKO_SECRET_KEY para a chave VAPID).
	Push PushConfig

	// Channels é a configuração do operador para os canais externos (ADR-0046).
	Channels ChannelsConfig

	// notifier guarda o estado dos avisos (entregas em andamento, cache da chave VAPID). É ponteiro para o Service
	// poder ser copiado; New o cria, e sem ele vale o estado global do processo (ver ns).
	notifier *notifyState
}

func New(pool *pgxpool.Pool, jobs Jobs, broker *events.Broker) *Service {
	return &Service{Pool: pool, Jobs: jobs, Broker: broker, Now: func() time.Time { return time.Now().UTC() },
		PasswordGate: auth.NewPasswordGate(0, 0), gitSt: &gitState{}, notifier: &notifyState{}}
}

// ---------- inputs ----------

type CreateCompanyInput struct {
	Name       string
	Slug       string // derived from Name when empty
	TaskPrefix string // default "HNK"
	Timezone   string // default America/Sao_Paulo
}

type CreateWorkspaceInput struct {
	Name, Slug, Path, RepoURL, DefaultBranch string
}

type CreateProjectInput struct {
	Name, Slug, Description string
	WorkspaceID             *domain.ID
	// BudgetMonthlyCents is the project's monthly ceiling (USD cents); nil = none.
	BudgetMonthlyCents *int64
}

type HirePersonaInput struct {
	Name                 string
	Slug                 string
	Title                string
	Icon                 string // um de domain.PersonaIcons; vazio = domain.DefaultPersonaIcon
	Instructions         string
	Agent                string // claude | codex | acp | fake
	Model                string
	AdapterConfig        json.RawMessage
	ReportsTo            *domain.ID
	WorkspaceID          *domain.ID
	HeartbeatIntervalSec int
	BudgetMonthlyCents   *int64
	// SubscriptionID links the persona to a plan (assinatura) of the company.
	SubscriptionID *domain.ID
	// RequireReview sends this persona's attempts to close a task through a task_review gate.
	RequireReview bool
	// Reviewer (slug or id of another persona) or ReviewerAgent/ReviewerModel (same role on another
	// harness/model) put an automatic review before that gate. Mutually exclusive.
	ReviewerPersona *domain.ID
	ReviewerAgent   string
	ReviewerModel   string
}

type UpdatePersonaInput struct {
	Name, Title, Instructions, Model *string
	// InstructionsReason é o motivo da mudança de instruções (ADR-0052); vai na revisão gravada.
	InstructionsReason   string
	Icon                 *string // um de domain.PersonaIcons
	Status               *domain.PersonaStatus
	HeartbeatIntervalSec *int
	WorkspaceID          *domain.ID
	ClearWorkspace       bool
	// ReportsTo moves the persona under another one; ClearReportsTo makes it top-level.
	// A move that would close a loop in the chart is rejected (ErrInvalid).
	ReportsTo      *domain.ID
	ClearReportsTo bool
	RequireReview  *bool
	// ReviewerPersona sets the reviewer persona; ReviewerAgent/ReviewerModel set an alternate harness for
	// the same role; ClearReviewer removes any reviewer. Setting one replaces the other.
	ReviewerPersona *domain.ID
	ReviewerAgent   *string
	ReviewerModel   *string
	ClearReviewer   bool
	// BudgetMonthlyCents sets the persona's monthly budget (USD cents); ClearBudget removes it.
	BudgetMonthlyCents *int64
	ClearBudget        bool
	// SubscriptionID links the persona to a subscription; ClearSubscription removes the link.
	SubscriptionID    *domain.ID
	ClearSubscription bool
}

type CreateTaskInput struct {
	Title       string
	Description string
	Status      domain.TaskStatus // default todo
	Priority    domain.Priority   // default medium
	PersonaID   *domain.ID
	ProjectID   *domain.ID
	WorkspaceID *domain.ID // explicit choice; wins over the project's and the persona's workspace at run time (ADR-0025)
	GoalID      *domain.ID // meta a que a task serve (ADR-0028); só meta ativa
	Model       string     // manual model choice (wins over the persona's); empty = none
	ParentID    *domain.ID
	// DependsOn são as tasks de que a nova depende (bloqueada até todas fecharem; ADR-0040).
	DependsOn []domain.ID
	EventID   string // idempotency key (agents/MCP)
	// Unassigned recusa o responsável padrão: uma task criada por uma pessoa sem PersonaID nasce com a persona principal
	// dela (ADR-0041), a menos que isto esteja ligado.
	Unassigned bool

	trigger domain.RunTrigger // wake trigger; default assign (flows use flow)
	// noMemoryFact: a task de sistema da consolidação de memória não vira página na própria memória.
	noMemoryFact bool
}

type UpdateTaskInput struct {
	Title       *string
	Description *string
	Status      *domain.TaskStatus
	Priority    *domain.Priority
	PersonaID   *domain.ID
	Unassign    bool
	ProjectID   *domain.ID
	// ClearProject takes the task out of its project.
	ClearProject bool
	// GoalID liga a task a uma meta ativa; ClearGoal desliga (ADR-0028).
	GoalID    *domain.ID
	ClearGoal bool
	Model     *string // manual model choice; empty string clears it
	EventID   string
	// ParentID muda o pai da task (mesma empresa, sem ciclo, até 10 níveis); ClearParent a torna raiz (ADR-0040).
	ParentID    *domain.ID
	ClearParent bool
	// ForceClose + OverrideReason: um humano conclui o pai mesmo com subtask aberta, registrando o motivo.
	ForceClose     bool
	OverrideReason string
}

type TaskFilter struct {
	Statuses  []domain.TaskStatus
	PersonaID *domain.ID
	ProjectID *domain.ID
	GoalID    *domain.ID
	Query     string
	// Mine limita às tasks em que a pessoa autenticada participa (criou, atribuiu, foi mencionada, comentou ou é da
	// audiência de um gate, ADR-0042); sem pessoa autenticada devolve vazio. Cada task traz Participation.
	Mine   bool
	Limit  int // default 100, max 500
	Offset int
}

type AddCommentInput struct {
	Body    string
	RunID   *domain.ID
	EventID string
	// AttachmentIDs são anexos da task já enviados (sem comentário ainda) que passam a pertencer a este comentário,
	// na mesma transação: a run acordada pelo comentário já os vê no briefing (ADR-0052).
	AttachmentIDs []domain.ID
	// Anchor, when set, ties the comment to a passage of the task description or of a run summary
	// (ADR-0026). The service checks that the quote exists in the target document and fills offsets and hash.
	Anchor *domain.Anchor
}

type RunFilter struct {
	TaskID         *domain.ID
	ConversationID *domain.ID
	PersonaID      *domain.ID
	Statuses       []domain.RunStatus
	// Name busca pelo nome amigável (sem diferenciar caixa nem acento: agil_falcao acha Ágil_Falcão).
	Name  string
	Limit int // default 50, max 500
}

type ActivityFilter struct {
	SubjectType string
	SubjectID   *domain.ID
	// ActorType (human|persona|system) e ActorID filtram por quem causou o fato (ADR-0042); ActorID vazio com
	// ActorType human/persona pega todos do tipo. ResolveActor converte "human:ana@x.com" / "persona:ana" / "system".
	ActorType string
	ActorID   *domain.ID
	Before    *time.Time
	Limit     int // default 50
}

type FinishRunInput struct {
	Status domain.RunStatus // done | failed | interrupted | cancelled | expired
	Result adapter.Result
}

// TaskView is a task with display-ready joins.
type TaskView struct {
	domain.Task
	PersonaName string     `json:"persona_name,omitempty"`
	ProjectName string     `json:"project_name,omitempty"`
	ActiveRunID *domain.ID `json:"active_run_id,omitempty"`
	// System marca a task de sistema de uma rodada de curadoria de memória (ADR-0030): o Hanko a cria, a
	// acorda e a encerra; ninguém a acorda à mão nem por comentário.
	System bool `json:"system,omitempty"`
	// Participation são os motivos pelos quais a task é "minha" (só nas listagens com Mine, ADR-0042).
	Participation []string `json:"participation,omitempty"`
	// TaskCoord traz o que a coordenação deriva: dependências abertas (bloqueada), filhos e pausa (ADR-0040).
	TaskCoord
}

// RunView is a run with display-ready joins.
type RunView struct {
	domain.Run
	TaskKey     string `json:"task_key"`
	TaskTitle   string `json:"task_title"`
	PersonaName string `json:"persona_name"`
	// ConversationTitle is the title of the conversation of a chat run (empty for task runs).
	ConversationTitle string `json:"conversation_title,omitempty"`
	// SteerMode is how the run's harness takes an operator message mid-run (ADR-0039).
	SteerMode domain.SteerMode `json:"steer_mode,omitempty"`
	// ContinuedByRunID is the run that continued this one, when there is one (filled by GetRun).
	ContinuedByRunID *domain.ID `json:"continued_by_run_id,omitempty"`
	// PolicyOrigin diz de que camada (task, projeto ou persona) veio a política de revisão que valeu na execução
	// (ADR-0040); só o detalhe da execução preenche.
	PolicyOrigin *domain.RunPolicyOrigin `json:"policy_origin,omitempty"`
}

// DashboardStats feeds the Painel.
type DashboardStats struct {
	ActivePersonas  int     `json:"active_personas"`
	TasksInProgress int     `json:"tasks_in_progress"`
	OpenTasks       int     `json:"open_tasks"`
	MonthCostUSD    float64 `json:"month_cost_usd"`
	// Company monthly ceiling (USD cents), its use and state (none|ok|warning|exceeded).
	MonthBudgetCents *int64  `json:"month_budget_cents,omitempty"`
	BudgetPercent    float64 `json:"budget_percent"`
	BudgetState      string  `json:"budget_state"`
	PendingGates     int     `json:"pending_gates"`
	RunningRuns      int     `json:"running_runs"`
}

// RunContext is everything the executor needs to build the agent prompt.
type RunContext struct {
	Run       domain.Run
	Company   domain.Company
	Persona   domain.Persona
	Task      TaskView
	Comments  []domain.Comment
	Workspace *domain.Workspace
	// PendingResult is set when the harness already ran and only finalization is missing.
	PendingResult *FinishRunInput
	// Review is set for runs with trigger review.
	Review *ReviewContext
	// Goals são as metas ativas do projeto da task (e a da própria task), em formato curto (ADR-0028).
	Goals []GoalBrief
	// Chat is set for runs with trigger chat (no task: Task and Comments are empty).
	Chat *ChatContext
	// Continuation is set when the run continues another one (ADR-0039); ResumeSession is the harness session a task
	// run resumes (chat runs use Chat.ResumeSession).
	Continuation  *ContinuationContext
	ResumeSession string
	// Relations são a árvore (ancestrais e subtasks) e as dependências da task (ADR-0040); nil sem task.
	Relations *TaskRelations
	// ReviewPolicy é a política de revisão que vale para esta task e esta persona (task > projeto > persona).
	ReviewPolicy *domain.ReviewPolicy
	// Plan é a fase de plan-review da task (ADR-0041); nil quando a política de plano não vale.
	Plan *PlanContext
	// ProjectInstructions é o briefing do projeto da task (ADR-0054, #34); entra no system prompt depois das
	// instruções da persona. Vazio sem projeto ou sem briefing.
	ProjectInstructions string
	// SessionReset é o pedido de "Nova sessão" que este run aplicou (ADR-0054, #16); nil na maioria dos runs.
	SessionReset *domain.TaskSessionReset
	// Worktree é o worktree Git da task quando o executor o preparou (ADR-0054); o prompt diz a branch.
	Worktree *WorktreePrep
	// Attachments são os anexos da task (ou da conversa, numa run de chat), do mais antigo ao mais novo, até
	// MaxBriefingAttachments (ADR-0052).
	Attachments []AttachmentBrief
}

// MaxBriefingAttachments é quantos anexos entram no briefing da run.
const MaxBriefingAttachments = 30

// AttachmentBrief é um anexo em formato curto para o briefing da run.
type AttachmentBrief struct {
	ID        domain.ID
	Name      string
	Mime      string
	SizeBytes int64
	Author    domain.Actor
	CreatedAt time.Time
	CommentID *domain.ID
	MessageID *domain.ID
	// New: o anexo veio numa mensagem que esta run de chat recolheu.
	New bool
}

// WakeCandidate is an (assigned, workable, idle) task found by the periodic heartbeat.
type WakeCandidate struct {
	CompanyID domain.ID
	PersonaID domain.ID
	TaskID    domain.ID
}

// MemoryItem is a pending memory_outbox row.
type MemoryItem struct {
	ID          domain.ID
	CompanyID   domain.ID
	CompanySlug string
	Workspace   *domain.Workspace
	Kind        string
	Payload     json.RawMessage
	EventID     string
	Attempts    int
	Status      string // pending | sent | failed
	NextAttempt *time.Time
}

// PersonaHealth is the reliability state of a persona: its last failed run and how many
// failures in a row it has (the persona goes to error at personaMaxFailures).
type PersonaHealth struct {
	LastError           string
	LastErrorAt         *time.Time
	ConsecutiveFailures int
}
