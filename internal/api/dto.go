package api

import (
	"encoding/json"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// Tipos de entrada e saída da API. Códigos e campos em inglês (ADR-0016);
// documentação (tags doc) em pt-BR.

// Page são os parâmetros de paginação das listagens.
type Page struct {
	Limit  int `query:"limit" minimum:"1" maximum:"500" default:"50" doc:"Máximo de itens retornados."`
	Offset int `query:"offset" minimum:"0" default:"0" doc:"Quantos itens pular."`
}

// ListBody envelopa listagens paginadas.
type ListBody[T any] struct {
	Items  []T `json:"items" doc:"Itens da página."`
	Limit  int `json:"limit" doc:"Limite aplicado."`
	Offset int `json:"offset" doc:"Deslocamento aplicado."`
}

// ListOut é a resposta de uma listagem.
type ListOut[T any] struct {
	Body ListBody[T]
}

// ItemOut é a resposta de um recurso único.
type ItemOut[T any] struct {
	Body T
}

// ActorRef identifica quem causou um fato.
type ActorRef struct {
	Type string `json:"type" enum:"human,persona,system" doc:"Tipo do autor."`
	ID   string `json:"id,omitempty" format:"uuid" doc:"Id da persona, quando o autor é persona."`
}

// Company é a unidade de isolamento multi-org.
type Company struct {
	ID         string `json:"id" format:"uuid" doc:"Identificador (UUIDv7)."`
	Slug       string `json:"slug" example:"acme" doc:"Identificador legível, único."`
	Name       string `json:"name" example:"Acme Ltda" doc:"Nome de exibição."`
	TaskPrefix string `json:"task_prefix" example:"HNK" doc:"Prefixo das chaves de task (HNK-12)."`
	Timezone   string `json:"timezone" example:"America/Sao_Paulo" doc:"Fuso IANA."`
	// BudgetMonthlyCents é o teto mensal de gasto em centavos de dólar; ausente = sem teto.
	BudgetMonthlyCents *int64    `json:"budget_monthly_cents,omitempty" doc:"Orçamento mensal da empresa em centavos de dólar."`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Workspace é um diretório/repositório onde personas trabalham.
type Workspace struct {
	ID        string `json:"id" format:"uuid"`
	CompanyID string `json:"company_id" format:"uuid"`
	Slug      string `json:"slug" example:"api"`
	Name      string `json:"name" example:"API"`
	Path      string `json:"path" example:"/srv/work/api" doc:"Diretório de trabalho."`
	RepoURL   string `json:"repo_url,omitempty" example:"https://example.com/acme/api.git" doc:"Metadado: https://, ssh:// ou formato scp (usuario@host:dono/repo). O Hanko não clona."`
	// DefaultBranch é metadado (o Hanko não troca de branch).
	DefaultBranch string `json:"default_branch,omitempty" example:"main"`
	// Uso do workspace: vem na lista e no detalhe. Tasks abertas contam as que escolheram o workspace de propósito.
	Usage     *WorkspaceUsage `json:"usage,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// UsageRef identifica uma persona ou projeto que usa um workspace.
type UsageRef struct {
	ID   string `json:"id" format:"uuid"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// WorkspaceUsage diz quem usa o workspace; um workspace em uso não é excluído sem desvincular.
type WorkspaceUsage struct {
	Personas     int        `json:"personas" doc:"Personas que o têm como padrão."`
	Projects     int        `json:"projects"`
	OpenTasks    int        `json:"open_tasks" doc:"Tasks abertas que escolheram este workspace."`
	PersonaItems []UsageRef `json:"persona_items,omitempty" doc:"Só no detalhe."`
	ProjectItems []UsageRef `json:"project_items,omitempty" doc:"Só no detalhe."`
}

// Project agrupa tasks; pode ter workspace e orçamento mensal próprios.
type Project struct {
	ID          string `json:"id" format:"uuid"`
	CompanyID   string `json:"company_id" format:"uuid"`
	Slug        string `json:"slug" example:"lancamento"`
	Name        string `json:"name" example:"Lançamento"`
	Description string `json:"description,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty" format:"uuid"`
	Status      string `json:"status" enum:"active,archived" doc:"Projeto arquivado não recebe tasks novas."`
	// BudgetMonthlyCents é o teto mensal do projeto em centavos de dólar; ausente = sem teto. Estourado, nenhuma
	// execução de task do projeto abre até o teto subir ou o mês virar.
	BudgetMonthlyCents *int64        `json:"budget_monthly_cents,omitempty"`
	Stats              *ProjectStats `json:"stats,omitempty" doc:"Progresso e custo do mês; vem na lista e no detalhe."`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

// ProjectPersona é uma persona com tasks no projeto.
type ProjectPersona struct {
	ID        string `json:"id" format:"uuid"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Tasks     int    `json:"tasks"`
	OpenTasks int    `json:"open_tasks"`
}

// ProjectStats é o painel do projeto: tasks por status, progresso, custo do mês contra o teto e personas.
type ProjectStats struct {
	WorkspaceSlug   string           `json:"workspace_slug,omitempty"`
	WorkspaceName   string           `json:"workspace_name,omitempty"`
	Tasks           map[string]int   `json:"tasks" doc:"Tasks por status (todos os status presentes)."`
	TasksTotal      int              `json:"tasks_total"`
	ProgressPercent float64          `json:"progress_percent" doc:"done / (total - canceladas), de 0 a 100."`
	Budget          Budget           `json:"budget" doc:"Gasto do mês do projeto contra o teto dele."`
	Personas        []ProjectPersona `json:"personas,omitempty" doc:"Só no detalhe."`
}

// Persona é um papel contratado pela empresa.
type Persona struct {
	ID                   string         `json:"id" format:"uuid"`
	CompanyID            string         `json:"company_id" format:"uuid"`
	Slug                 string         `json:"slug" example:"cto"`
	Name                 string         `json:"name" example:"Marina"`
	Title                string         `json:"title" example:"CTO"`
	Icon                 string         `json:"icon" example:"code" doc:"Glifo da persona (catálogo fechado)."`
	Instructions         string         `json:"instructions" doc:"Instruções permanentes da persona."`
	InstructionRevision  int            `json:"instruction_revision" doc:"Revisão vigente das instruções (histórico em /instructions/revisions, ADR-0052)."`
	Agent                string         `json:"agent" enum:"claude,codex,acp,fake" doc:"Harness que executa a persona."`
	Model                string         `json:"model,omitempty" example:"claude-sonnet-4-5"`
	AdapterConfig        map[string]any `json:"adapter_config,omitempty" doc:"Configuração específica do adapter. Valores de env e segredos voltam mascarados (***); os nomes ficam."`
	Status               string         `json:"status" enum:"active,idle,running,paused,error,awaiting_gate,off"`
	ReportsTo            string         `json:"reports_to,omitempty" format:"uuid" doc:"Persona à qual esta reporta."`
	WorkspaceID          string         `json:"workspace_id,omitempty" format:"uuid"`
	HeartbeatIntervalSec int            `json:"heartbeat_interval_sec" doc:"Intervalo do heartbeat em segundos; 0 desliga."`
	BudgetMonthlyCents   *int64         `json:"budget_monthly_cents,omitempty" doc:"Orçamento mensal em centavos de dólar."`
	RequireReview        bool           `json:"require_review" doc:"Quando verdadeiro, concluir uma task passa por um gate de revisão (task_review)."`
	PauseReason          string         `json:"pause_reason,omitempty" doc:"Motivo de uma pausa automática (orçamento estourado)."`
	ReviewerPersonaID    string         `json:"reviewer_persona_id,omitempty" format:"uuid" doc:"Persona que revisa, em run headless, as entregas desta antes do gate."`
	ReviewerAgent        string         `json:"reviewer_agent,omitempty" enum:"claude,codex,fake" doc:"Harness alternativo que revisa no papel desta persona."`
	ReviewerModel        string         `json:"reviewer_model,omitempty" doc:"Modelo do harness revisor."`
	SubscriptionID       string         `json:"subscription_id,omitempty" format:"uuid" doc:"Assinatura que cobre as execuções da persona."`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`

	EnvironmentID string `json:"environment_id,omitempty" format:"uuid" doc:"Ambiente de execução da persona; ausente = o padrão da empresa (ADR-0037)."`
}

// Task é uma unidade de trabalho atribuída a uma persona.
type Task struct {
	ID          string     `json:"id" format:"uuid"`
	CompanyID   string     `json:"company_id" format:"uuid"`
	Number      int64      `json:"number" example:"12"`
	Key         string     `json:"key" example:"HNK-12" doc:"Chave legível <prefixo>-<número>."`
	Title       string     `json:"title" example:"Implementar login"`
	Description string     `json:"description"`
	Status      string     `json:"status" enum:"backlog,todo,in_progress,in_review,blocked,done,cancelled"`
	Priority    string     `json:"priority" enum:"critical,high,medium,low"`
	PersonaID   string     `json:"persona_id,omitempty" format:"uuid"`
	PersonaName string     `json:"persona_name,omitempty"`
	ProjectID   string     `json:"project_id,omitempty" format:"uuid"`
	ProjectName string     `json:"project_name,omitempty"`
	WorkspaceID string     `json:"workspace_id,omitempty" format:"uuid"`
	GoalID      string     `json:"goal_id,omitempty" format:"uuid" doc:"Meta a que a task serve."`
	ParentID    string     `json:"parent_id,omitempty" format:"uuid"`
	Model       string     `json:"model,omitempty" doc:"Modelo escolhido à mão para esta task; vence o da persona."`
	ActiveRunID string     `json:"active_run_id,omitempty" format:"uuid" doc:"Execução em andamento, se houver."`
	CreatedBy   ActorRef   `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	// Coordenação (ADR-0040): derivados, nunca gravados.
	OpenDependencies  int       `json:"open_dependencies" doc:"Dependências ainda abertas; a task está bloqueada enquanto for maior que zero."`
	DependenciesTotal int       `json:"dependencies_total"`
	Blocked           bool      `json:"blocked" doc:"Derivado: tem ao menos uma dependência aberta. Nenhuma execução abre enquanto durar."`
	ChildrenTotal     int       `json:"children_total"`
	ChildrenOpen      int       `json:"children_open"`
	Participation     []string  `json:"participation,omitempty" doc:"Só com mine=true: por que a task é da pessoa (created, assigned, mentioned, commented, gate)."`
	Hold              *TaskHold `json:"hold,omitempty" doc:"Pausa efetiva: a da própria task ou herdada do ancestral mais próximo. Nenhuma execução abre enquanto durar."`
}

// TaskHold é a pausa de uma task (gravada nela ou herdada de um ancestral).
type TaskHold struct {
	Reason    string    `json:"reason"`
	By        ActorRef  `json:"by"`
	At        time.Time `json:"at"`
	OriginID  string    `json:"origin_id" format:"uuid" doc:"Task onde a pausa foi gravada."`
	OriginKey string    `json:"origin_key" example:"HNK-3"`
	Inherited bool      `json:"inherited" doc:"Verdadeiro quando a pausa vem de um ancestral."`
}

// TaskRef é uma task numa lista curta (ancestral, filha, dependência).
type TaskRef struct {
	ID          string `json:"id" format:"uuid"`
	Key         string `json:"key" example:"HNK-12"`
	Title       string `json:"title"`
	Status      string `json:"status" enum:"backlog,todo,in_progress,in_review,blocked,done,cancelled"`
	PersonaID   string `json:"persona_id,omitempty" format:"uuid"`
	PersonaName string `json:"persona_name,omitempty"`
	Closed      bool   `json:"closed" doc:"done ou cancelled: já não bloqueia nem conta como aberta."`
}

// TaskRelations é a árvore e as dependências de uma task.
type TaskRelations struct {
	Ancestors  []TaskRef `json:"ancestors" doc:"Da raiz ao pai imediato."`
	Children   []TaskRef `json:"children"`
	DependsOn  []TaskRef `json:"depends_on" doc:"Tasks de que esta depende."`
	Dependents []TaskRef `json:"dependents" doc:"Tasks que dependem desta."`
}

// ReviewOverride é a sobreposição da política de revisão de uma camada (task ou projeto).
type ReviewOverride struct {
	RequireReview     *bool  `json:"require_review,omitempty" doc:"Ausente = herda da camada de baixo; true/false força."`
	ReviewerMode      string `json:"reviewer_mode" enum:"inherit,none,custom" doc:"inherit herda; none = sem revisor automático (vai direto ao gate humano); custom = persona revisora ou harness+modelo."`
	ReviewerPersonaID string `json:"reviewer_persona_id,omitempty" format:"uuid"`
	ReviewerAgent     string `json:"reviewer_agent,omitempty" enum:"claude,codex,fake"`
	ReviewerModel     string `json:"reviewer_model,omitempty"`
}

// ReviewPolicy é o resultado da precedência task > projeto > persona e a origem de cada decisão.
type ReviewPolicy struct {
	Required          bool   `json:"required"`
	RequiredSource    string `json:"required_source" enum:"task,project,persona"`
	HasReviewer       bool   `json:"has_reviewer"`
	ReviewerSource    string `json:"reviewer_source" enum:"task,project,persona"`
	ReviewerPersonaID string `json:"reviewer_persona_id,omitempty" format:"uuid"`
	ReviewerAgent     string `json:"reviewer_agent,omitempty"`
	ReviewerModel     string `json:"reviewer_model,omitempty"`
}

// TaskReviewPolicy mostra as camadas da política de revisão de uma task e o efeito.
type TaskReviewPolicy struct {
	Task      ReviewOverride `json:"task"`
	Project   ReviewOverride `json:"project"`
	Persona   ReviewOverride `json:"persona" doc:"A política da persona responsável, na mesma forma."`
	Effective ReviewPolicy   `json:"effective"`
}

// RunPolicyOrigin é a origem da política de revisão que valeu numa execução.
type RunPolicyOrigin struct {
	ReviewRequired       bool   `json:"review_required"`
	ReviewRequiredSource string `json:"review_required_source" enum:"task,project,persona"`
	ReviewerSource       string `json:"reviewer_source" enum:"task,project,persona"`
}

// Comment é uma mensagem na thread de uma task.
type Comment struct {
	ID          string       `json:"id" format:"uuid"`
	CompanyID   string       `json:"company_id" format:"uuid"`
	TaskID      string       `json:"task_id" format:"uuid"`
	Author      ActorRef     `json:"author"`
	RunID       string       `json:"run_id,omitempty" format:"uuid"`
	Body        string       `json:"body"`
	EventID     string       `json:"event_id,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	Anchor      *Anchor      `json:"anchor,omitempty" doc:"Trecho do documento a que o comentário se refere (comentário ancorado)."`
	AnchorState *AnchorState `json:"anchor_state,omitempty" doc:"Onde o trecho está na versão atual do documento; 'lost' = âncora perdida."`
}

// Anchor liga um comentário a um trecho da descrição da task ou do resumo (entrega) de uma execução.
// A citação é a fonte da verdade: se o documento mudar, o trecho é procurado de novo pelo texto.
type Anchor struct {
	Target  string `json:"target" enum:"task_description,run_summary" doc:"Documento-alvo."`
	RunID   string `json:"run_id,omitempty" format:"uuid" doc:"Execução cujo resumo é o alvo (obrigatório com run_summary)."`
	Quote   string `json:"quote" minLength:"1" maxLength:"2000" doc:"Texto citado, exatamente como está no documento."`
	Start   int    `json:"start" minimum:"0" doc:"Posição inicial da citação, em code points do documento (dica; o servidor recalcula)."`
	End     int    `json:"end" minimum:"0" doc:"Posição final (exclusiva), em code points."`
	DocHash string `json:"doc_hash,omitempty" doc:"Hash do documento no momento do comentário; definido pelo servidor."`
}

// AnchorState é a posição atual de uma âncora.
type AnchorState struct {
	Status string `json:"status" enum:"exact,moved,lost" doc:"exact = documento igual; moved = documento mudou e o trecho foi reencontrado; lost = trecho não existe mais."`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

// Usage são contagens de tokens reportadas pelo agente.
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
}

// Run é uma execução de uma persona sobre uma task.
type Run struct {
	ID                string   `json:"id" format:"uuid"`
	Name              string   `json:"name,omitempty" example:"Veloz_Raposa" doc:"Nome amigável Adjetivo_Substantivo, único na empresa; vazio em execuções anteriores a ele."`
	CompanyID         string   `json:"company_id" format:"uuid"`
	TaskID            string   `json:"task_id,omitempty" format:"uuid" doc:"Task da execução; ausente numa execução de chat."`
	TaskKey           string   `json:"task_key,omitempty" example:"HNK-12"`
	TaskTitle         string   `json:"task_title,omitempty"`
	ConversationID    string   `json:"conversation_id,omitempty" format:"uuid" doc:"Conversa de uma execução de chat (gatilho chat); ausente nas execuções de task."`
	ConversationTitle string   `json:"conversation_title,omitempty"`
	PersonaID         string   `json:"persona_id" format:"uuid"`
	PersonaName       string   `json:"persona_name,omitempty"`
	Status            string   `json:"status" enum:"queued,running,done,interrupted,failed,cancelled,expired"`
	Trigger           string   `json:"trigger" enum:"assign,comment,manual,heartbeat,gate,flow,review,memory,chat"`
	Agent             string   `json:"agent" enum:"claude,codex,acp,fake"`
	Model             string   `json:"model,omitempty"`
	SessionRef        string   `json:"session_ref,omitempty"`
	TraceID           string   `json:"trace_id,omitempty"`
	ExitCode          *int     `json:"exit_code,omitempty"`
	Error             string   `json:"error,omitempty"`
	Summary           string   `json:"summary,omitempty"`
	CostUSD           *float64 `json:"cost_usd,omitempty"`
	CostSource        string   `json:"cost_source,omitempty" enum:"reported,estimated,none" doc:"Origem do custo: informado pelo harness, estimado pela tabela de preços ou desconhecido."`
	ListCostUSD       *float64 `json:"list_cost_usd,omitempty" doc:"Custo de lista: tokens × preço de tabela vigente quando a execução terminou."`
	ModelSource       string   `json:"model_source,omitempty" enum:"task,persona,harness_default" doc:"Quem escolheu o modelo: a task (manual), a persona ou o padrão do harness."`
	SubscriptionID    string   `json:"subscription_id,omitempty" format:"uuid" doc:"Assinatura que cobre a execução."`
	Usage             Usage    `json:"usage"`
	// InstructionRevision/InstructionSHA256: com que revisão das instruções da persona a execução rodou (ADR-0052).
	InstructionRevision *int       `json:"instruction_revision,omitempty"`
	InstructionSHA256   string     `json:"instruction_sha256,omitempty" doc:"sha256 (hex) do texto das instruções com que a execução rodou."`
	CreatedAt           time.Time  `json:"created_at"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	// Resiliência (ADR-0039)
	SteerMode           string        `json:"steer_mode,omitempty" enum:"none,inject,restart" doc:"Como o harness da execução aceita uma mensagem durante a execução: inject (entra na execução viva) ou restart (interrompe e continua)."`
	ContinuationOfRunID string        `json:"continuation_of_run_id,omitempty" format:"uuid" doc:"Execução que esta continua (queda do processo, steer, Tentar de novo ou Continuar)."`
	ContinuationAttempt int           `json:"continuation_attempt,omitempty" doc:"Quantas continuações automáticas a cadeia já usou (0 = a original; o máximo é 2)."`
	ContinuationReason  string        `json:"continuation_reason,omitempty" enum:"recovery,steer,retry,continue"`
	ContinuedByRunID    string        `json:"continued_by_run_id,omitempty" format:"uuid" doc:"Execução que continuou esta (só no GET de uma execução)."`
	Diagnosis           *RunDiagnosis `json:"diagnosis,omitempty" doc:"Card de diagnóstico de uma execução que parou: causa, tentativa, próxima ação e dono."`
	DecisionPending     bool          `json:"decision_pending,omitempty" doc:"A execução parou e espera Tentar de novo, Continuar ou Encerrar."`
	Decision            string        `json:"decision,omitempty" enum:"retry,continue,close" doc:"O que o operador decidiu."`
	DecisionNote        string        `json:"decision_note,omitempty"`
	DecidedAt           *time.Time    `json:"decided_at,omitempty"`
	// PolicyOrigin: de que camada veio a política de revisão desta execução (só no detalhe).
	PolicyOrigin *RunPolicyOrigin `json:"policy_origin,omitempty"`
}

// RunDiagnosis é o card de diagnóstico de uma execução que parou.
type RunDiagnosis struct {
	Cause       string         `json:"cause" enum:"process_restart,server_shutdown,agent_error,timeout,cancelled"`
	CauseDetail string         `json:"cause_detail,omitempty"`
	Attempt     int            `json:"attempt" doc:"Continuação em que a execução parou (0 = original)."`
	MaxAttempts int            `json:"max_attempts" doc:"Máximo de continuações automáticas."`
	NextAction  string         `json:"next_action" enum:"auto_continue,human_decision,none,continued"`
	Owner       string         `json:"owner" enum:"system,human" doc:"Quem tem a próxima ação."`
	Exhausted   bool           `json:"exhausted,omitempty" doc:"As continuações automáticas acabaram."`
	Uncertain   *UncertainTool `json:"uncertain,omitempty" doc:"Desfecho incerto: tool de efeito externo em andamento quando a execução parou."`
}

// UncertainTool é a tool de efeito externo que estava em andamento quando a execução parou.
type UncertainTool struct {
	Name  string `json:"name"`
	Input string `json:"input,omitempty"`
	Seq   int64  `json:"seq" doc:"Seq do evento tool_use no log."`
}

// RunEvent é uma linha do log de uma execução.
type RunEvent struct {
	ID         string          `json:"id" format:"uuid"`
	RunID      string          `json:"run_id" format:"uuid"`
	Seq        int64           `json:"seq" doc:"Ordem dentro da execução."`
	Kind       string          `json:"kind" enum:"system,assistant,thinking,tool_use,tool_result,stdout,stderr,result,status"`
	Text       string          `json:"text"`
	Payload    json.RawMessage `json:"payload,omitempty" doc:"Dados estruturados do evento." type:"object"`
	OccurredAt time.Time       `json:"occurred_at"`
	ReceivedAt time.Time       `json:"received_at"`
}

// Activity é um fato de domínio.
type Activity struct {
	ID          string          `json:"id" format:"uuid"`
	CompanyID   string          `json:"company_id" format:"uuid"`
	Actor       ActorRef        `json:"actor"`
	Verb        string          `json:"verb" example:"task.created"`
	SubjectType string          `json:"subject_type" example:"task"`
	SubjectID   string          `json:"subject_id" format:"uuid"`
	Data        json.RawMessage `json:"data,omitempty" type:"object"`
	EventID     string          `json:"event_id,omitempty"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ReceivedAt  time.Time       `json:"received_at"`
}

// Dashboard são os números do Painel.
type Dashboard struct {
	ActivePersonas  int     `json:"active_personas"`
	TasksInProgress int     `json:"tasks_in_progress"`
	OpenTasks       int     `json:"open_tasks"`
	MonthCostUSD    float64 `json:"month_cost_usd"`
	PendingGates    int     `json:"pending_gates"`
	RunningRuns     int     `json:"running_runs"`
	// Teto mensal da empresa (centavos de dólar), uso e estado (none, ok, warning, exceeded).
	MonthBudgetCents *int64  `json:"month_budget_cents,omitempty"`
	BudgetPercent    float64 `json:"budget_percent"`
	BudgetState      string  `json:"budget_state" enum:"none,ok,warning,exceeded"`
}

// Budget é o gasto do mês contra o teto.
type Budget struct {
	BudgetMonthlyCents *int64  `json:"budget_monthly_cents,omitempty" doc:"Teto mensal em centavos de dólar; ausente = sem teto."`
	SpentUSD           float64 `json:"spent_usd"`
	Percent            float64 `json:"percent" doc:"Uso do teto em %; 0 sem teto."`
	State              string  `json:"state" enum:"none,ok,warning,exceeded" doc:"warning a partir de 80%, exceeded a partir de 100% (trava dura)."`
}

// CostRow é um grupo do resumo de custos.
type CostRow struct {
	Key          string  `json:"key" doc:"Chave do grupo (data, id da persona, id do projeto, modelo ou harness)."`
	Label        string  `json:"label"`
	CostUSD      float64 `json:"cost_usd"`
	ReportedUSD  float64 `json:"reported_usd" doc:"Custo real: informado pelo harness."`
	EstimatedUSD float64 `json:"estimated_usd" doc:"Parte do custo estimada pela tabela de preços."`
	ListUSD      float64 `json:"list_usd" doc:"Custo de lista: tokens × preço de tabela vigente quando cada execução terminou."`
	PaidUSD      float64 `json:"paid_usd" doc:"Custo pago: rateio da mensalidade das assinaturas (mensalidade ÷ dias do mês, por dia, proporcional ao custo de lista de cada execução do dia)."`
	IdleUSD      float64 `json:"idle_usd" doc:"Mensalidade de um dia sem uso (só no agrupamento por dia)."`
	Unused       bool    `json:"unused" doc:"Dia em que a assinatura não foi usada (a tela mostra \"sem uso\")."`
	Runs         int     `json:"runs"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
}

// CostSummary agrega o custo de um período por uma dimensão.
type CostSummary struct {
	From          time.Time          `json:"from"`
	To            time.Time          `json:"to" doc:"Exclusivo."`
	Timezone      string             `json:"timezone"`
	GroupBy       string             `json:"group_by" enum:"day,persona,project,model,harness"`
	TotalUSD      float64            `json:"total_usd"`
	ReportedUSD   float64            `json:"reported_usd"`
	EstimatedUSD  float64            `json:"estimated_usd"`
	ListUSD       float64            `json:"list_usd"`
	PaidUSD       float64            `json:"paid_usd" doc:"Mensalidades rateadas nas execuções, até hoje."`
	IdleUSD       float64            `json:"idle_usd" doc:"Mensalidades de dias sem uso. paid_usd + idle_usd fecha com a mensalidade num mês completo."`
	Subscriptions []SubscriptionCost `json:"subscriptions" doc:"Custo pago por assinatura no período."`
	Runs          int                `json:"runs"`
	InputTokens   int64              `json:"input_tokens"`
	OutputTokens  int64              `json:"output_tokens"`
	CacheTokens   int64              `json:"cache_tokens"`
	Rows          []CostRow          `json:"rows"`
	Budget        Budget             `json:"budget" doc:"Orçamento da empresa no mês corrente."`
}

// Flow é uma definição recorrente que cria tasks por cron.
type Flow struct {
	ID              string     `json:"id" format:"uuid"`
	CompanyID       string     `json:"company_id" format:"uuid"`
	Slug            string     `json:"slug" example:"resumo-diario"`
	Name            string     `json:"name" example:"Resumo diário"`
	Description     string     `json:"description,omitempty"`
	Cron            string     `json:"cron" example:"0 9 * * 1-5"`
	Timezone        string     `json:"timezone" example:"America/Sao_Paulo"`
	Enabled         bool       `json:"enabled"`
	PersonaID       string     `json:"persona_id" format:"uuid"`
	PersonaName     string     `json:"persona_name"`
	ProjectID       string     `json:"project_id,omitempty" format:"uuid"`
	ProjectName     string     `json:"project_name,omitempty"`
	Priority        string     `json:"priority" enum:"critical,high,medium,low"`
	TaskTitle       string     `json:"task_title" doc:"Modelo do título: {{data}}, {{n}}."`
	TaskDescription string     `json:"task_description"`
	LastFiredAt     *time.Time `json:"last_fired_at,omitempty"`
	NextFireAt      *time.Time `json:"next_fire_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// FlowFire é um disparo de um fluxo.
type FlowFire struct {
	ID           string    `json:"id" format:"uuid"`
	FlowID       string    `json:"flow_id" format:"uuid"`
	ScheduledFor time.Time `json:"scheduled_for"`
	TaskID       string    `json:"task_id,omitempty" format:"uuid"`
	TaskKey      string    `json:"task_key,omitempty" example:"HNK-12"`
	TaskTitle    string    `json:"task_title,omitempty"`
	TaskStatus   string    `json:"task_status,omitempty"`
	Manual       bool      `json:"manual,omitempty" doc:"true no disparo de \"Rodar agora\" (não é uma ocorrência do cron)."`
	CreatedAt    time.Time `json:"created_at"`
}

// FlowRun é o resultado de "rodar agora".
type FlowRun struct {
	Fire      FlowFire `json:"fire"`
	Task      *Task    `json:"task,omitempty"`
	Duplicate bool     `json:"duplicate,omitempty" doc:"true quando já havia um disparo neste minuto: nada novo foi criado."`
}

// WakeResult confirma o enfileiramento de um wake.
type WakeResult struct {
	Queued bool `json:"queued" doc:"Sempre true quando aceito; execuções simultâneas são coalescidas."`
}

// ---------- conversões ----------

func idStr(id domain.ID) string { return id.String() }

func idPtr(id *domain.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func actorRef(a domain.Actor) ActorRef {
	return ActorRef{Type: string(a.Type), ID: idPtr(a.ID)}
}

func companyOut(c domain.Company) Company {
	return Company{ID: idStr(c.ID), Slug: c.Slug, Name: c.Name, TaskPrefix: c.TaskPrefix,
		Timezone: c.Timezone, BudgetMonthlyCents: c.BudgetMonthlyCents, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func workspaceOut(w domain.Workspace) Workspace {
	return Workspace{ID: idStr(w.ID), CompanyID: idStr(w.CompanyID), Slug: w.Slug, Name: w.Name,
		Path: w.Path, RepoURL: w.RepoURL, DefaultBranch: w.DefaultBranch, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
}

func usageRefs(rs []service.UsageRef) []UsageRef {
	out := make([]UsageRef, len(rs))
	for i, r := range rs {
		out[i] = UsageRef{ID: idStr(r.ID), Slug: r.Slug, Name: r.Name}
	}
	return out
}

func workspaceViewOut(v service.WorkspaceView) Workspace {
	w := workspaceOut(v.Workspace)
	w.Usage = &WorkspaceUsage{Personas: v.PersonaCount, Projects: v.ProjectCount, OpenTasks: v.OpenTasks,
		PersonaItems: usageRefs(v.Personas), ProjectItems: usageRefs(v.Projects)}
	return w
}

func projectOut(p domain.Project) Project {
	return Project{ID: idStr(p.ID), CompanyID: idStr(p.CompanyID), Slug: p.Slug, Name: p.Name,
		Description: p.Description, WorkspaceID: idPtr(p.WorkspaceID), Status: string(p.Status),
		BudgetMonthlyCents: p.BudgetMonthlyCents, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func projectViewOut(v service.ProjectView) Project {
	p := projectOut(v.Project)
	tasks := make(map[string]int, len(v.Tasks))
	for st, n := range v.Tasks {
		tasks[string(st)] = n
	}
	st := &ProjectStats{WorkspaceSlug: v.WorkspaceSlug, WorkspaceName: v.WorkspaceName, Tasks: tasks, TasksTotal: v.TasksTotal,
		ProgressPercent: v.Progress, Budget: budgetOut(v.Budget)}
	for _, pe := range v.Personas {
		st.Personas = append(st.Personas, ProjectPersona{ID: idStr(pe.ID), Slug: pe.Slug, Name: pe.Name, Tasks: pe.Tasks, OpenTasks: pe.OpenTasks})
	}
	p.Stats = st
	return p
}

func personaOut(p domain.Persona) Persona {
	var cfg map[string]any
	if len(p.AdapterConfig) > 0 {
		_ = json.Unmarshal(p.AdapterConfig, &cfg)
	}
	return Persona{ID: idStr(p.ID), CompanyID: idStr(p.CompanyID), Slug: p.Slug, Name: p.Name, Title: p.Title, Icon: p.Icon,
		Instructions: p.Instructions, InstructionRevision: p.InstructionRevision, Agent: p.Agent, Model: p.Model, AdapterConfig: export.MaskConfig(cfg), Status: string(p.Status),
		ReportsTo: idPtr(p.ReportsTo), WorkspaceID: idPtr(p.WorkspaceID), HeartbeatIntervalSec: p.HeartbeatIntervalSec,
		BudgetMonthlyCents: p.BudgetMonthlyCents, PauseReason: p.PauseReason, SubscriptionID: idPtr(p.SubscriptionID), RequireReview: p.RequireReview, ReviewerPersonaID: idPtr(p.ReviewerPersonaID), ReviewerAgent: p.ReviewerAgent, ReviewerModel: p.ReviewerModel, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		EnvironmentID: idPtr(p.EnvironmentID)}
}

func taskOut(t service.TaskView) Task {
	return Task{ID: idStr(t.ID), CompanyID: idStr(t.CompanyID), Number: t.Number, Key: t.Key, Title: t.Title,
		Description: t.Description, Status: string(t.Status), Priority: string(t.Priority),
		PersonaID: idPtr(t.PersonaID), PersonaName: t.PersonaName, ProjectID: idPtr(t.ProjectID), ProjectName: t.ProjectName,
		WorkspaceID: idPtr(t.WorkspaceID), GoalID: idPtr(t.GoalID), ParentID: idPtr(t.ParentID), Model: t.Model, ActiveRunID: idPtr(t.ActiveRunID),
		CreatedBy: actorRef(t.CreatedBy), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, ClosedAt: t.ClosedAt,
		OpenDependencies: t.OpenDependencies, DependenciesTotal: t.DependenciesTotal, Blocked: t.Blocked,
		ChildrenTotal: t.ChildrenTotal, ChildrenOpen: t.ChildrenOpen, Hold: holdOut(t.Hold), Participation: t.Participation}
}

func holdOut(h *domain.TaskHold) *TaskHold {
	if h == nil {
		return nil
	}
	return &TaskHold{Reason: h.Reason, By: actorRef(h.By), At: h.At, OriginID: idStr(h.OriginID), OriginKey: h.OriginKey, Inherited: h.Inherited}
}

func taskRefOut(d service.DependencyView) TaskRef {
	return TaskRef{ID: idStr(d.ID), Key: d.Key, Title: d.Title, Status: string(d.Status), PersonaID: idPtr(d.PersonaID),
		PersonaName: d.PersonaName, Closed: d.Closed}
}

func taskRelationsOut(r service.TaskRelations) TaskRelations {
	out := TaskRelations{Ancestors: []TaskRef{}, Children: []TaskRef{}, DependsOn: []TaskRef{}, Dependents: []TaskRef{}}
	for _, a := range r.Ancestors {
		out.Ancestors = append(out.Ancestors, taskRefOut(service.DependencyView{TaskRef: a, Closed: !a.Status.Open()}))
	}
	for _, d := range r.Children {
		out.Children = append(out.Children, taskRefOut(d))
	}
	for _, d := range r.DependsOn {
		out.DependsOn = append(out.DependsOn, taskRefOut(d))
	}
	for _, d := range r.Dependents {
		out.Dependents = append(out.Dependents, taskRefOut(d))
	}
	return out
}

func reviewOverrideOut(o domain.ReviewOverride) ReviewOverride {
	mode := string(o.ReviewerMode)
	if mode == "" {
		mode = string(domain.ReviewerInherit)
	}
	return ReviewOverride{RequireReview: o.RequireReview, ReviewerMode: mode, ReviewerPersonaID: idPtr(o.ReviewerPersonaID),
		ReviewerAgent: o.ReviewerAgent, ReviewerModel: o.ReviewerModel}
}

func reviewPolicyOut(p domain.ReviewPolicy) ReviewPolicy {
	return ReviewPolicy{Required: p.Required, RequiredSource: string(p.RequiredSource), HasReviewer: p.HasReviewer,
		ReviewerSource: string(p.ReviewerSource), ReviewerPersonaID: idPtr(p.ReviewerPersonaID), ReviewerAgent: p.ReviewerAgent,
		ReviewerModel: p.ReviewerModel}
}

func taskReviewPolicyOut(v service.ReviewPolicyView) TaskReviewPolicy {
	pr := v.Persona
	persona := ReviewOverride{ReviewerMode: string(domain.ReviewerInherit), ReviewerPersonaID: idPtr(pr.ReviewerPersonaID),
		ReviewerAgent: pr.ReviewerAgent, ReviewerModel: pr.ReviewerModel}
	req := pr.RequireReview
	persona.RequireReview = &req
	if domain.HasReviewer(pr.ReviewerPersonaID, pr.ReviewerAgent) {
		persona.ReviewerMode = string(domain.ReviewerCustom)
	}
	return TaskReviewPolicy{Task: reviewOverrideOut(v.Task), Project: reviewOverrideOut(v.Project), Persona: persona,
		Effective: reviewPolicyOut(v.Effective)}
}

func commentOut(c domain.Comment) Comment {
	out := Comment{ID: idStr(c.ID), CompanyID: idStr(c.CompanyID), TaskID: idStr(c.TaskID), Author: actorRef(c.Author),
		RunID: idPtr(c.RunID), Body: c.Body, EventID: c.EventID, CreatedAt: c.CreatedAt}
	if a := c.Anchor; a != nil {
		out.Anchor = &Anchor{Target: string(a.Target), RunID: idPtr(a.RunID), Quote: a.Quote, Start: a.Start, End: a.End, DocHash: a.DocHash}
	}
	if st := c.AnchorState; st != nil {
		out.AnchorState = &AnchorState{Status: st.Status, Start: st.Start, End: st.End}
	}
	return out
}

func runOut(r service.RunView) Run {
	taskID := ""
	if r.TaskID != (domain.ID{}) {
		taskID = idStr(r.TaskID)
	}
	return Run{ID: idStr(r.ID), Name: r.Name, CompanyID: idStr(r.CompanyID), TaskID: taskID, TaskKey: r.TaskKey,
		ConversationID: idPtr(r.ConversationID), ConversationTitle: r.ConversationTitle, TaskTitle: r.TaskTitle, PersonaID: idStr(r.PersonaID), PersonaName: r.PersonaName, Status: string(r.Status),
		CostSource: string(r.CostSource), ListCostUSD: r.ListCostUSD, ModelSource: string(r.ModelSource), SubscriptionID: idPtr(r.SubscriptionID),
		Trigger: string(r.Trigger), Agent: r.Agent, Model: r.Model, SessionRef: r.SessionRef, TraceID: r.TraceID,
		ExitCode: r.ExitCode, Error: r.Error, Summary: r.Summary, CostUSD: r.CostUSD, Usage: Usage(r.Usage),
		InstructionRevision: r.InstructionRevision, InstructionSHA256: r.InstructionSHA256, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		SteerMode: string(r.SteerMode), ContinuationOfRunID: idPtr(r.ContinuationOfRunID), ContinuationAttempt: r.ContinuationAttempt,
		ContinuationReason: string(r.ContinuationReason), ContinuedByRunID: idPtr(r.ContinuedByRunID), Diagnosis: diagnosisOut(r.Diagnosis),
		DecisionPending: r.DecisionPending, Decision: string(r.Decision), DecisionNote: r.DecisionNote, DecidedAt: r.DecidedAt, PolicyOrigin: policyOriginOut(r.PolicyOrigin)}
}

func diagnosisOut(d *domain.RunDiagnosis) *RunDiagnosis {
	if d == nil {
		return nil
	}
	out := &RunDiagnosis{Cause: string(d.Cause), CauseDetail: d.CauseDetail, Attempt: d.Attempt, MaxAttempts: d.MaxAttempts,
		NextAction: string(d.NextAction), Owner: string(d.Owner), Exhausted: d.Exhausted}
	if d.Uncertain != nil {
		out.Uncertain = &UncertainTool{Name: d.Uncertain.Name, Input: d.Uncertain.Input, Seq: d.Uncertain.Seq}
	}
	return out
}

func policyOriginOut(o *domain.RunPolicyOrigin) *RunPolicyOrigin {
	if o == nil {
		return nil
	}
	return &RunPolicyOrigin{ReviewRequired: o.ReviewRequired, ReviewRequiredSource: string(o.ReviewRequiredSource), ReviewerSource: string(o.ReviewerSource)}
}

func runEventOut(e domain.RunEvent) RunEvent {
	return RunEvent{ID: idStr(e.ID), RunID: idStr(e.RunID), Seq: e.Seq, Kind: string(e.Kind), Text: e.Text,
		Payload: e.Payload, OccurredAt: e.OccurredAt, ReceivedAt: e.ReceivedAt}
}

func activityOut(a domain.ActivityEvent) Activity {
	return Activity{ID: idStr(a.ID), CompanyID: idStr(a.CompanyID), Actor: actorRef(a.Actor), Verb: a.Verb,
		SubjectType: a.SubjectType, SubjectID: idStr(a.SubjectID), Data: a.Data, EventID: a.EventID,
		OccurredAt: a.OccurredAt, ReceivedAt: a.ReceivedAt}
}

func dashboardOut(d service.DashboardStats) Dashboard {
	return Dashboard{ActivePersonas: d.ActivePersonas, TasksInProgress: d.TasksInProgress, OpenTasks: d.OpenTasks,
		MonthCostUSD: d.MonthCostUSD, PendingGates: d.PendingGates, RunningRuns: d.RunningRuns,
		MonthBudgetCents: d.MonthBudgetCents, BudgetPercent: d.BudgetPercent, BudgetState: d.BudgetState}
}

func budgetOut(b service.BudgetStatus) Budget {
	return Budget{BudgetMonthlyCents: b.BudgetCents, SpentUSD: b.SpentUSD, Percent: b.Percent, State: b.State}
}

func costSummaryOut(c service.CostSummary, b service.BudgetStatus) CostSummary {
	rows := make([]CostRow, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = CostRow(r)
	}
	subs := make([]SubscriptionCost, len(c.Subscriptions))
	for i, sc := range c.Subscriptions {
		subs[i] = SubscriptionCost{ID: idStr(sc.ID), Name: sc.Name, Agent: sc.Agent, MonthlyUSD: sc.MonthlyUSD, PaidUSD: sc.PaidUSD,
			IdleUSD: sc.IdleUSD, ListUSD: sc.ListUSD, Runs: sc.Runs}
	}
	return CostSummary{From: c.From, To: c.To, Timezone: c.Timezone, GroupBy: c.GroupBy, TotalUSD: c.TotalUSD,
		ReportedUSD: c.ReportedUSD, EstimatedUSD: c.EstimatedUSD, ListUSD: c.ListUSD, PaidUSD: c.PaidUSD, IdleUSD: c.IdleUSD,
		Subscriptions: subs, Runs: c.Runs, InputTokens: c.InputTokens,
		OutputTokens: c.OutputTokens, CacheTokens: c.CacheTokens, Rows: rows, Budget: budgetOut(b)}
}

func flowOut(f service.FlowView) Flow {
	return Flow{ID: idStr(f.ID), CompanyID: idStr(f.CompanyID), Slug: f.Slug, Name: f.Name, Description: f.Description,
		Cron: f.Cron, Timezone: f.Timezone, Enabled: f.Enabled, PersonaID: idStr(f.PersonaID), PersonaName: f.PersonaName,
		ProjectID: idPtr(f.ProjectID), ProjectName: f.ProjectName, Priority: string(f.Priority), TaskTitle: f.TaskTitle,
		TaskDescription: f.TaskDescription, LastFiredAt: f.LastFiredAt, NextFireAt: f.NextFireAt,
		CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt}
}

func flowFireOut(f service.FlowFireView) FlowFire {
	return FlowFire{ID: idStr(f.ID), FlowID: idStr(f.FlowID), ScheduledFor: f.ScheduledFor, TaskID: idPtr(f.TaskID),
		TaskKey: f.TaskKey, TaskTitle: f.TaskTitle, TaskStatus: f.TaskStatus, Manual: f.Manual, CreatedAt: f.CreatedAt}
}

func flowRunOut(r service.FlowFireResult) FlowRun {
	out := FlowRun{Fire: FlowFire{ID: idStr(r.Fire.ID), FlowID: idStr(r.Fire.FlowID), ScheduledFor: r.Fire.ScheduledFor,
		TaskID: idPtr(r.Fire.TaskID), Manual: r.Fire.Manual, CreatedAt: r.Fire.CreatedAt}, Duplicate: r.Duplicate}
	if r.Task != nil {
		t := taskOut(*r.Task)
		out.Task = &t
		out.Fire.TaskKey, out.Fire.TaskTitle, out.Fire.TaskStatus = t.Key, t.Title, t.Status
	}
	return out
}

// listOf converte, pagina e envelopa. A paginação por fatiamento vale para
// listagens que o service devolve completas.
func listOf[S, T any](items []S, p Page, conv func(S) T) *ListOut[T] {
	start := min(p.Offset, len(items))
	end := min(start+p.Limit, len(items))
	out := make([]T, 0, end-start)
	for _, it := range items[start:end] {
		out = append(out, conv(it))
	}
	return &ListOut[T]{Body: ListBody[T]{Items: out, Limit: p.Limit, Offset: p.Offset}}
}

// listAll envelopa itens já paginados na origem.
func listAll[S, T any](items []S, p Page, conv func(S) T) *ListOut[T] {
	out := make([]T, 0, len(items))
	for _, it := range items {
		out = append(out, conv(it))
	}
	return &ListOut[T]{Body: ListBody[T]{Items: out, Limit: p.Limit, Offset: p.Offset}}
}

// Subscription é o plano de um harness: mensalidade, modelos cobertos e trava semanal.
type Subscription struct {
	ID             string    `json:"id" format:"uuid"`
	CompanyID      string    `json:"company_id" format:"uuid"`
	Name           string    `json:"name" example:"Claude Max"`
	Agent          string    `json:"agent" enum:"claude,codex,acp,fake"`
	MonthlyUSD     float64   `json:"monthly_usd"`
	ModelPrefixes  []string  `json:"model_prefixes" doc:"Prefixos de modelo cobertos; vazio = todos os do harness."`
	WeeklyLimitUSD *float64  `json:"weekly_limit_usd,omitempty" doc:"Teto semanal de uso estimado em dólares."`
	WeeklyGuardPct int       `json:"weekly_guard_pct" doc:"Percentual do teto semanal que trava novas execuções."`
	Active         bool      `json:"active"`
	WeekStart      time.Time `json:"week_start" doc:"Início (segunda-feira, fuso da empresa) da semana corrente."`
	WeekEnd        time.Time `json:"week_end" doc:"Exclusivo."`
	WeekUsageUSD   float64   `json:"week_usage_usd" doc:"Uso estimado da semana corrente (custo de lista das execuções cobertas)."`
	WeekPercent    float64   `json:"week_percent" doc:"Uso da semana em % do teto semanal; 0 sem teto."`
	Blocked        bool      `json:"blocked" doc:"A trava semanal disparou: novas execuções cobertas ficam bloqueadas até a semana virar."`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SubscriptionCost é o custo de uma assinatura num período.
type SubscriptionCost struct {
	ID         string  `json:"id" format:"uuid"`
	Name       string  `json:"name"`
	Agent      string  `json:"agent"`
	MonthlyUSD float64 `json:"monthly_usd"`
	PaidUSD    float64 `json:"paid_usd" doc:"Parte da mensalidade rateada nas execuções."`
	IdleUSD    float64 `json:"idle_usd" doc:"Parte da mensalidade de dias sem uso."`
	ListUSD    float64 `json:"list_usd" doc:"Custo de lista das execuções cobertas."`
	Runs       int     `json:"runs"`
}

func subscriptionOut(s service.SubscriptionStatus) Subscription {
	prefixes := s.ModelPrefixes
	if prefixes == nil {
		prefixes = []string{}
	}
	return Subscription{ID: idStr(s.ID), CompanyID: idStr(s.CompanyID), Name: s.Name, Agent: s.Agent, MonthlyUSD: s.MonthlyUSD,
		ModelPrefixes: prefixes, WeeklyLimitUSD: s.WeeklyLimitUSD, WeeklyGuardPct: s.WeeklyGuardPct, Active: s.Active,
		WeekStart: s.WeekStart, WeekEnd: s.WeekEnd, WeekUsageUSD: s.WeekUsageUSD, WeekPercent: s.WeekPercent, Blocked: s.Blocked,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}
