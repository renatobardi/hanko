package domain

import "time"

// Versões, fontes e estúdio de skills (ADR-0035).

// Limites das fontes e do estúdio.
const (
	// MaxSkillsPerSource é quantas skills uma fonte importa no máximo; o resto é ignorado com aviso.
	MaxSkillsPerSource = 200
	// MaxSkillSourceBytes é o teto do clone em disco; acima dele a sincronização aborta.
	MaxSkillSourceBytes = 50 << 20
	// MaxSkillSourceExcluded é quantos caminhos excluídos uma fonte guarda.
	MaxSkillSourceExcluded = 200
	// MaxSkillTestInputBytes é o tamanho máximo de uma entrada do estúdio (markdown).
	MaxSkillTestInputBytes = 32 << 10
	// MaxSkillTestInputs é quantas entradas salvas uma skill tem no máximo.
	MaxSkillTestInputs = 50
	// MaxSkillTestOutputBytes é quanto da saída de um teste é guardado.
	MaxSkillTestOutputBytes = 64 << 10
)

// SkillVersion é uma versão imutável do conteúdo de uma skill. Revision é o valor de skills.version quando a
// versão foi gravada.
type SkillVersion struct {
	ID           ID        `json:"id"`
	CompanyID    ID        `json:"company_id"`
	SkillID      ID        `json:"skill_id"`
	Revision     int       `json:"revision"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Body         string    `json:"body,omitempty"`
	BodyBytes    int       `json:"body_bytes"`
	SHA256       string    `json:"sha256"`
	SourceCommit string    `json:"source_commit,omitempty"`
	Author       Actor     `json:"author"`
	CreatedAt    time.Time `json:"created_at"`
}

// SkillSourceStatus é o estado de uma fonte.
type SkillSourceStatus string

const (
	SourceIdle    SkillSourceStatus = "idle"    // criada, nunca sincronizada
	SourceSyncing SkillSourceStatus = "syncing" // sincronização em andamento
	SourceOK      SkillSourceStatus = "ok"      // última sincronização deu certo
	SourceError   SkillSourceStatus = "error"   // última sincronização falhou (há backoff)
	SourcePaused  SkillSourceStatus = "paused"  // atualização automática desligada
)

// EntrySelection é a escolha do admin para um diretório da fonte.
type EntrySelection string

const (
	EntrySelected EntrySelection = "selected" // importada e mantida em dia
	EntryExcluded EntrySelection = "excluded" // ignorada
	EntryNew      EntrySelection = "new"      // apareceu na fonte e espera revisão (import_new desligado)
)

// Valid diz se a seleção é conhecida.
func (e EntrySelection) Valid() bool {
	switch e {
	case EntrySelected, EntryExcluded, EntryNew:
		return true
	}
	return false
}

// SkillSource é um repositório de onde a empresa importa skills.
type SkillSource struct {
	ID            ID                `json:"id"`
	CompanyID     ID                `json:"company_id"`
	URL           string            `json:"url"`
	TrackingRef   string            `json:"tracking_ref"`
	PathPrefix    string            `json:"path_prefix"`
	ExcludedPaths []string          `json:"excluded_paths"`
	AutoUpdate    bool              `json:"auto_update"`
	ImportNew     bool              `json:"import_new"`
	Enabled       bool              `json:"enabled"`
	LastCommit    string            `json:"last_commit,omitempty"`
	LastTreeHash  string            `json:"last_tree_hash,omitempty"`
	Status        SkillSourceStatus `json:"status"`
	LastError     string            `json:"last_error,omitempty"`
	FailureCount  int               `json:"failure_count"`
	NextSyncAt    *time.Time        `json:"next_sync_at,omitempty"`
	LastAttemptAt *time.Time        `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time        `json:"last_success_at,omitempty"`
	CreatedBy     Actor             `json:"created_by"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// SkillSourceEntry é um diretório com SKILL.md encontrado na fonte.
type SkillSourceEntry struct {
	ID          ID             `json:"id"`
	Path        string         `json:"path"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	SkillID     *ID            `json:"skill_id,omitempty"`
	SkillSlug   string         `json:"skill_slug,omitempty"`
	Modified    bool           `json:"modified,omitempty"`
	Selection   EntrySelection `json:"selection"`
	Present     bool           `json:"present"`
	HasScripts  bool           `json:"has_scripts"`
	Error       string         `json:"error,omitempty"`
}

// SourceSyncInterval é o intervalo entre sincronizações automáticas bem-sucedidas.
const SourceSyncInterval = time.Hour

// SkillSourceBackoff é a espera depois da n-ésima falha seguida (n ≥ 1): min(60 min, 5 min·2^min(n-1,4)),
// isto é 5, 10, 20, 40 e 60 minutos.
func SkillSourceBackoff(failures int) time.Duration {
	n := failures - 1
	if n < 0 {
		n = 0
	}
	if n > 4 {
		n = 4
	}
	d := 5 * time.Minute << n
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// SkillTestStatus é o estado de uma execução de teste do estúdio.
type SkillTestStatus string

const (
	SkillTestQueued    SkillTestStatus = "queued"
	SkillTestRunning   SkillTestStatus = "running"
	SkillTestDone      SkillTestStatus = "done"
	SkillTestFailed    SkillTestStatus = "failed"
	SkillTestCancelled SkillTestStatus = "cancelled"
)

// Terminal diz se o teste acabou.
func (s SkillTestStatus) Terminal() bool {
	return s == SkillTestDone || s == SkillTestFailed || s == SkillTestCancelled
}

// SkillTestInput é uma entrada salva do estúdio.
type SkillTestInput struct {
	ID        ID        `json:"id"`
	SkillID   ID        `json:"skill_id"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	CreatedBy Actor     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SkillTestRun é uma execução de teste: a skill numa versão fixa, uma entrada e a persona que a roda.
type SkillTestRun struct {
	ID            ID              `json:"id"`
	CompanyID     ID              `json:"company_id"`
	SkillID       ID              `json:"skill_id"`
	SkillSlug     string          `json:"skill_slug"`
	SkillRevision int             `json:"skill_revision"`
	InputID       *ID             `json:"input_id,omitempty"`
	Input         string          `json:"input"`
	PersonaID     ID              `json:"persona_id"`
	PersonaSlug   string          `json:"persona_slug"`
	PersonaName   string          `json:"persona_name"`
	TaskID        *ID             `json:"task_id,omitempty"`
	RunID         *ID             `json:"run_id,omitempty"`
	RunName       string          `json:"run_name,omitempty"`
	CostUSD       *float64        `json:"cost_usd,omitempty"`
	Status        SkillTestStatus `json:"status"`
	Output        string          `json:"output"`
	Error         string          `json:"error,omitempty"`
	CreatedBy     Actor           `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}
