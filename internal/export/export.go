// Package export define o arquivo portátil de uma empresa (ADR-0022): um JSON versionado com tudo
// o que descreve o time (workspaces, projetos, personas, organograma, tasks e comentários, fluxos,
// orçamentos, assinaturas) e, se pedido, o histórico de execuções. Referências entre entidades são
// por slug, nome ou número de task, nunca por id: o arquivo vale em qualquer instalação.
// Segredos, tokens e credenciais nunca entram.
package export

import (
	"encoding/json"
	"time"
)

// Format é o identificador e a versão do arquivo.
const Format = "hanko.company.v1"

// Document é o arquivo inteiro.
type Document struct {
	Format        string         `json:"format" enum:"hanko.company.v1" doc:"Identificador e versão do formato."`
	ExportedAt    time.Time      `json:"exported_at"`
	Company       Company        `json:"company"`
	Workspaces    []Workspace    `json:"workspaces,omitempty"`
	Projects      []Project      `json:"projects,omitempty"`
	Subscriptions []Subscription `json:"subscriptions,omitempty"`
	Personas      []Persona      `json:"personas,omitempty" doc:"Em ordem de contratação; o organograma é o campo reports_to."`
	Tasks         []Task         `json:"tasks,omitempty" doc:"Em ordem de número, com os comentários."`
	Flows         []Flow         `json:"flows,omitempty"`
	Skills        []Skill        `json:"skills,omitempty" doc:"Skills da empresa (SKILL.md); a ligação com as personas está em personas[].skills. Opcional (ADR-0029)."`
	Integrations  []Integration  `json:"integrations,omitempty" doc:"Integrações SEM segredos (headers, variáveis, segredo HMAC); entram desligadas na importação. Opcional (ADR-0029)."`
	Runs          []Run          `json:"runs,omitempty" doc:"Só com with_runs: execuções encerradas, sem logs a menos que with_logs."`
	// Artifacts só vem com with_artifacts (ADR-0028); arquivos antigos não têm o campo, e o formato continua v1.
	Artifacts []Artifact `json:"artifacts,omitempty" doc:"Só com with_artifacts: todas as versões dos artefatos, com o conteúdo em base64."`
}

// Company são os dados da empresa. Os preços de modelo são da instalação, não da empresa, e não viajam.
type Company struct {
	Name               string `json:"name"`
	Slug               string `json:"slug,omitempty" doc:"Sugestão de slug na importação; o parâmetro slug vence."`
	TaskPrefix         string `json:"task_prefix"`
	Timezone           string `json:"timezone"`
	BudgetMonthlyCents *int64 `json:"budget_monthly_cents,omitempty" doc:"Orçamento mensal da empresa em centavos de dólar."`
	NextTaskNumber     int64  `json:"next_task_number" doc:"Número da próxima task; preserva a numeração."`
}

type Workspace struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	RepoURL string `json:"repo_url,omitempty"`
	// DefaultBranch é metadado; opcional (arquivos antigos não têm).
	DefaultBranch string    `json:"default_branch,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type Project struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Workspace   string `json:"workspace,omitempty" doc:"Slug do workspace padrão."`
	// Status e BudgetMonthlyCents são opcionais: arquivos antigos não têm (status vazio = active, sem teto).
	Status             string    `json:"status,omitempty" enum:"active,archived"`
	BudgetMonthlyCents *int64    `json:"budget_monthly_cents,omitempty" doc:"Orçamento mensal do projeto em centavos de dólar."`
	CreatedAt          time.Time `json:"created_at"`
}

// Period é uma vigência da assinatura (liga/desliga).
type Period struct {
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

type Subscription struct {
	Name           string     `json:"name"`
	Agent          string     `json:"agent" enum:"claude,codex,acp,devin,pi,cursor,http,fake"`
	MonthlyUSD     float64    `json:"monthly_usd"`
	ModelPrefixes  []string   `json:"model_prefixes,omitempty"`
	WeeklyLimitUSD *float64   `json:"weekly_limit_usd,omitempty"`
	WeeklyGuardPct int        `json:"weekly_guard_pct"`
	Active         bool       `json:"active"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	Periods        []Period   `json:"periods,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type Persona struct {
	Slug                 string         `json:"slug"`
	Name                 string         `json:"name"`
	Title                string         `json:"title,omitempty"`
	Icon                 string         `json:"icon,omitempty" doc:"Glifo da persona; ausente = bot (arquivos anteriores ao campo)."`
	Instructions         string         `json:"instructions,omitempty"`
	Agent                string         `json:"agent" enum:"claude,codex,acp,devin,pi,cursor,http,fake"`
	Model                string         `json:"model,omitempty"`
	AdapterConfig        map[string]any `json:"adapter_config,omitempty" doc:"Sem chaves sensíveis (token, senha, credencial...)."`
	Status               string         `json:"status" enum:"active,idle,paused,off" doc:"Estados transitórios (running, error, awaiting_gate) viram idle."`
	ReportsTo            string         `json:"reports_to,omitempty" doc:"Slug da persona superior."`
	Workspace            string         `json:"workspace,omitempty"`
	HeartbeatIntervalSec int            `json:"heartbeat_interval_sec,omitempty"`
	BudgetMonthlyCents   *int64         `json:"budget_monthly_cents,omitempty"`
	Subscription         string         `json:"subscription,omitempty" doc:"Nome da assinatura."`
	RequireReview        bool           `json:"require_review,omitempty"`
	Reviewer             string         `json:"reviewer,omitempty" doc:"Slug da persona revisora (exclusivo com reviewer_agent)."`
	ReviewerAgent        string         `json:"reviewer_agent,omitempty" enum:"claude,codex,fake"`
	ReviewerModel        string         `json:"reviewer_model,omitempty"`
	Skills               []string       `json:"skills,omitempty" doc:"Slugs das skills habilitadas para a persona."`
	CreatedAt            time.Time      `json:"created_at"`
}

// Actor é quem fez algo. O id de um humano é da instalação de origem e não viaja.
type Actor struct {
	Type    string `json:"type" enum:"human,persona,system"`
	Persona string `json:"persona,omitempty" doc:"Slug da persona, quando type=persona."`
}

type Comment struct {
	Author    Actor     `json:"author"`
	Body      string    `json:"body"`
	Run       string    `json:"run,omitempty" doc:"Chave (key) da execução, quando o arquivo traz runs."`
	CreatedAt time.Time `json:"created_at"`
}

type Task struct {
	Number      int64      `json:"number"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status" enum:"backlog,todo,in_progress,in_review,blocked,done,cancelled"`
	Priority    string     `json:"priority" enum:"critical,high,medium,low"`
	Persona     string     `json:"persona,omitempty" doc:"Slug da persona responsável."`
	Project     string     `json:"project,omitempty"`
	Workspace   string     `json:"workspace,omitempty"`
	Parent      int64      `json:"parent,omitempty" doc:"Número da task pai."`
	Model       string     `json:"model,omitempty"`
	CreatedBy   Actor      `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	Comments    []Comment  `json:"comments,omitempty"`
}

type Flow struct {
	Slug            string         `json:"slug"`
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	Cron            string         `json:"cron"`
	Timezone        string         `json:"timezone"`
	Enabled         bool           `json:"enabled"`
	Persona         string         `json:"persona" doc:"Slug da persona que recebe as tasks."`
	Project         string         `json:"project,omitempty"`
	Priority        string         `json:"priority" enum:"critical,high,medium,low"`
	TaskTitle       string         `json:"task_title,omitempty"`
	TaskDescription string         `json:"task_description,omitempty"`
	Variables       []FlowVariable `json:"variables,omitempty" doc:"Variáveis declaradas ({{nome}} no título e na descrição). Gatilhos e segredos não viajam."`
	Archived        bool           `json:"archived,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

// FlowVariable é uma variável declarada de um fluxo (ADR-0047).
type FlowVariable struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`
}

type Usage struct {
	InputTokens         int64 `json:"input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
}

type RunEvent struct {
	Seq        int64           `json:"seq"`
	Kind       string          `json:"kind" enum:"system,assistant,thinking,tool_use,tool_result,stdout,stderr,result,status"`
	Text       string          `json:"text,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty" type:"object"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type Run struct {
	Key          string     `json:"key" doc:"Identificador local ao arquivo (r1, r2...), usado pelos comentários."`
	Name         string     `json:"name,omitempty" doc:"Nome amigável Adjetivo_Substantivo."`
	Task         int64      `json:"task" doc:"Número da task."`
	Persona      string     `json:"persona"`
	Status       string     `json:"status" enum:"done,interrupted,failed,cancelled,expired"`
	Trigger      string     `json:"trigger" enum:"assign,comment,manual,heartbeat,gate,flow,review"`
	Agent        string     `json:"agent"`
	Model        string     `json:"model,omitempty"`
	ModelSource  string     `json:"model_source,omitempty" enum:"task,persona,harness_default"`
	Subscription string     `json:"subscription,omitempty"`
	ExitCode     *int       `json:"exit_code,omitempty"`
	Error        string     `json:"error,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	CostUSD      *float64   `json:"cost_usd,omitempty"`
	CostSource   string     `json:"cost_source,omitempty" enum:"reported,estimated,none"`
	ListCostUSD  *float64   `json:"list_cost_usd,omitempty"`
	Usage        Usage      `json:"usage"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Events       []RunEvent `json:"events,omitempty" doc:"Só com with_logs."`
}

// Artifact é uma versão de artefato (ADR-0028). Referências por número de task, slug e chave de run, como o resto
// do arquivo; as versões de um mesmo artefato (task, nome) vêm em ordem crescente.
type Artifact struct {
	Name    string `json:"name"`
	Mime    string `json:"mime"`
	Version int    `json:"version" doc:"Ordinal da publicação dentro do documento (1, 2, 3...)."`
	// Campos do ADR-0064, opcionais: um arquivo antigo agrupa as versões por (task, nome) e numera vN como N.0.
	Document        string    `json:"document,omitempty" doc:"Chave do documento no arquivo; versões com a mesma chave são do mesmo documento."`
	Major           int       `json:"major,omitempty"`
	Minor           int       `json:"minor,omitempty"`
	Change          string    `json:"change,omitempty" doc:"major ou minor."`
	ChangeReason    string    `json:"change_reason,omitempty"`
	Tags            []string  `json:"tags,omitempty" doc:"Tags do documento."`
	DocumentProject string    `json:"document_project,omitempty" doc:"Slug do projeto do documento (pode diferir do desta versão)."`
	Task            int64     `json:"task,omitempty" doc:"Número da task; ausente = artefato da empresa."`
	Project         string    `json:"project,omitempty"`
	Persona         string    `json:"persona,omitempty" doc:"Slug da persona autora."`
	Run             string    `json:"run,omitempty" doc:"Chave (key) da execução, quando o arquivo traz runs."`
	Author          Actor     `json:"author"`
	SHA256          string    `json:"sha256"`
	SizeBytes       int64     `json:"size_bytes"`
	Content         []byte    `json:"content" doc:"Conteúdo em base64."`
	CreatedAt       time.Time `json:"created_at"`
}
