package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Cards de status (ADR-0032): um resumo vivo de um conjunto de tasks, mantido por uma persona resumidora.
// Este arquivo tem só o modelo e as regras puras (validação, política de atualização, comparação de retratos);
// a máquina de estados com banco e runs fica no service.

// StatusCardState é o estado gravado do card.
type StatusCardState string

const (
	CardCompiling    StatusCardState = "compiling"     // a resumidora ainda não gravou os filtros
	CardActive       StatusCardState = "active"        // filtros prontos; o resumo segue a política
	CardError        StatusCardState = "error"         // a última montagem ou atualização não terminou
	CardPausedBudget StatusCardState = "paused_budget" // o teto diário de tokens do card foi atingido
	CardPausedHours  StatusCardState = "paused_hours"  // fora do horário ativo
)

func (s StatusCardState) Valid() bool {
	switch s {
	case CardCompiling, CardActive, CardError, CardPausedBudget, CardPausedHours:
		return true
	}
	return false
}

// StatusCardView é o estado mostrado na UI, na ordem de precedência do ADR-0032.
type StatusCardView string

const (
	CardViewArchived     StatusCardView = "archived"
	CardViewCompiling    StatusCardView = "compiling"
	CardViewError        StatusCardView = "error"
	CardViewPausedBudget StatusCardView = "paused_budget"
	CardViewPausedHours  StatusCardView = "paused_hours"
	CardViewUpdating     StatusCardView = "updating"
	CardViewStale        StatusCardView = "stale"
	CardViewFresh        StatusCardView = "fresh"
)

// CardUpdateKind é o tipo de uma rodada.
type CardUpdateKind string

const (
	CardUpdateCompile     CardUpdateKind = "compile"
	CardUpdateFull        CardUpdateKind = "full"
	CardUpdateIncremental CardUpdateKind = "incremental"
)

// CardUpdateTrigger é o que motivou a rodada.
type CardUpdateTrigger string

const (
	CardTriggerManual   CardUpdateTrigger = "manual"   // pedido explícito (criar, editar, "Atualizar agora")
	CardTriggerInterval CardUpdateTrigger = "interval" // política em horário fixo
	CardTriggerReactive CardUpdateTrigger = "reactive" // política assim que algo mudar
	CardTriggerRestore  CardUpdateTrigger = "restore"  // card restaurado do arquivo
	CardTriggerQuery    CardUpdateTrigger = "query"    // primeira atualização depois de a resumidora gravar os filtros
	CardTriggerCron     CardUpdateTrigger = "cron"     // política de cron (ADR-0048)
)

// CardUpdateStatus é o desfecho de uma rodada.
type CardUpdateStatus string

const (
	CardUpdateRunning CardUpdateStatus = "running"
	CardUpdateOK      CardUpdateStatus = "ok"
	CardUpdateFailed  CardUpdateStatus = "failed"
)

// Modos da política de atualização.
const (
	CardModeManual   = "manual"
	CardModeInterval = "interval"
	CardModeReactive = "reactive"
	// CardModeCron atualiza num horário de cron (ADR-0048): "toda manhã às 7h", independente de mudanças (a menos que
	// CronSkipUnchanged).
	CardModeCron = "cron"
)

// CardEngine diz quem escreve o resumo do card.
type CardEngine string

const (
	// CardEnginePersona é a persona resumidora (ADR-0032): uma run completa, com tools.
	CardEnginePersona CardEngine = "persona"
	// CardEngineDecision é o decision model da empresa (ADR-0056): uma chamada só, barata, sobre o retrato das tasks.
	CardEngineDecision CardEngine = "decision"
)

// Valid diz se e é um motor conhecido.
func (e CardEngine) Valid() bool { return e == CardEnginePersona || e == CardEngineDecision }

// Limites (ADR-0032).
const (
	MaxCardTitleRunes          = 200
	MaxCardInterestRunes       = 20000
	MaxCardInterestPersonaRune = 4000 // interesse escrito por uma persona (MCP)
	MaxCardSummaryBytes        = 64 << 10
	MaxCardQueries             = 10
	MaxCardQueryTextRunes      = 200
	MaxCardQueryLabelRunes     = 200
	MaxCardChangeSummaryRunes  = 2000
	MaxCardRevisions           = 50
	MaxCardUpdates             = 200
	MaxCardsPerPersona         = 20
	MaxCardsPerCompany         = 100
	// MaxCardObservedTasks é quantas tasks um card observa no máximo (somando os filtros, sem repetir).
	MaxCardObservedTasks = 300
	// MaxCardMentions é quantas tasks citadas um resumo guarda.
	MaxCardMentions = 200
	// CardFullAfterChanges: mais mudanças que isto pedem um resumo refeito do zero.
	CardFullAfterChanges = 10
	// CardFullAfterIncrementals: depois de tantos incrementais seguidos, o próximo é full.
	CardFullAfterIncrementals = 9
	// CardGenerationTimeout é quanto uma montagem ou atualização pode ficar sem terminar antes de virar erro.
	CardGenerationTimeout = 2 * time.Hour
)

// StatusCardQuery é um filtro de task. Os campos são os da lista de tasks (status, prioridade, persona,
// projeto, meta, texto, só abertas); dentro de um filtro valem todos (E); entre filtros vale qualquer um (OU).
type StatusCardQuery struct {
	Label      string       `json:"label,omitempty"`
	Statuses   []TaskStatus `json:"status,omitempty"`
	Priorities []Priority   `json:"priority,omitempty"`
	PersonaID  *ID          `json:"persona_id,omitempty"`
	ProjectID  *ID          `json:"project_id,omitempty"`
	GoalID     *ID          `json:"goal_id,omitempty"`
	Text       string       `json:"text,omitempty"`
	OpenOnly   bool         `json:"open,omitempty"`
}

// Validate confere um filtro (as referências a persona, projeto e meta são conferidas pelo service).
func (q *StatusCardQuery) Validate(i int) error {
	field := fmt.Sprintf("queries[%d]", i)
	q.Label = strings.TrimSpace(q.Label)
	q.Text = strings.TrimSpace(q.Text)
	if utf8.RuneCountInString(q.Label) > MaxCardQueryLabelRunes {
		return Invalid(field+".label", fmt.Sprintf("até %d caracteres", MaxCardQueryLabelRunes))
	}
	if utf8.RuneCountInString(q.Text) > MaxCardQueryTextRunes {
		return Invalid(field+".text", fmt.Sprintf("até %d caracteres", MaxCardQueryTextRunes))
	}
	seenS := map[TaskStatus]bool{}
	var sts []TaskStatus
	for _, s := range q.Statuses {
		if !s.Valid() {
			return Invalid(field+".status", fmt.Sprintf("status desconhecido %q", s))
		}
		if !seenS[s] {
			seenS[s] = true
			sts = append(sts, s)
		}
	}
	q.Statuses = sts
	seenP := map[Priority]bool{}
	var ps []Priority
	for _, p := range q.Priorities {
		if !p.Valid() {
			return Invalid(field+".priority", fmt.Sprintf("prioridade desconhecida %q", p))
		}
		if !seenP[p] {
			seenP[p] = true
			ps = append(ps, p)
		}
	}
	q.Priorities = ps
	if len(q.Statuses) == 0 && len(q.Priorities) == 0 && q.PersonaID == nil && q.ProjectID == nil && q.GoalID == nil && q.Text == "" && !q.OpenOnly {
		return Invalid(field, "filtro vazio: informe ao menos um critério")
	}
	return nil
}

// ValidateCardQueries confere a lista de filtros (1 a 10).
func ValidateCardQueries(qs []StatusCardQuery) error {
	if len(qs) == 0 || len(qs) > MaxCardQueries {
		return Invalid("queries", fmt.Sprintf("de 1 a %d filtros", MaxCardQueries))
	}
	for i := range qs {
		if err := qs[i].Validate(i); err != nil {
			return err
		}
	}
	return nil
}

// CardTriggers diz o que conta como mudança.
type CardTriggers struct {
	Status        bool `json:"status"`         // uma task observada mudou de status
	Membership    bool `json:"membership"`     // uma task entrou ou saiu do conjunto
	HumanComments bool `json:"human_comments"` // um humano comentou numa task observada
	Assignee      bool `json:"assignee"`       // mudou a persona responsável
	AnyUpdate     bool `json:"any_update"`     // qualquer alteração (inclui título e prioridade)
}

// ActiveHours é a janela do dia em que o card se atualiza sozinho. Start > End atravessa a meia-noite.
type ActiveHours struct {
	Start string `json:"start"` // HH:MM
	End   string `json:"end"`   // HH:MM
	TZ    string `json:"tz"`    // fuso IANA
}

// CardPolicy é a política de atualização.
type CardPolicy struct {
	Mode              string       `json:"mode"`
	IntervalMinutes   int          `json:"interval_minutes"`
	DebounceSeconds   int          `json:"debounce_seconds"`
	MaxUpdatesPerHour int          `json:"max_updates_per_hour"`
	Triggers          CardTriggers `json:"triggers"`
	ActiveHours       *ActiveHours `json:"active_hours,omitempty"`
	DailyTokenCap     int64        `json:"daily_token_cap"`
	// Cron e CronTZ valem no modo cron (expressão de 5 campos e fuso IANA; padrão do fuso: UTC).
	Cron   string `json:"cron,omitempty"`
	CronTZ string `json:"cron_tz,omitempty"`
	// CronSkipUnchanged: no horário do cron, só atualiza se alguma task observada mudou.
	CronSkipUnchanged bool `json:"cron_skip_unchanged,omitempty"`
}

// DefaultCardPolicy é a política de um card novo: assim que algo mudar (com espera), status, entrada/saída e
// comentários humanos contam, até 6 atualizações por hora e 100 mil tokens por dia.
func DefaultCardPolicy() CardPolicy {
	return CardPolicy{Mode: CardModeReactive, IntervalMinutes: 60, DebounceSeconds: 300, MaxUpdatesPerHour: 6,
		Triggers: CardTriggers{Status: true, Membership: true, HumanComments: true}, DailyTokenCap: 100000}
}

// Normalize completa os campos zerados com o padrão e confere os limites.
func (p *CardPolicy) Normalize() error {
	d := DefaultCardPolicy()
	p.Mode = strings.TrimSpace(p.Mode)
	if p.Mode == "" {
		p.Mode = d.Mode
	}
	switch p.Mode {
	case CardModeManual, CardModeInterval, CardModeReactive:
	case CardModeCron:
		p.Cron = strings.TrimSpace(p.Cron)
		if p.Cron == "" {
			return Invalid("refresh_policy.cron", "informe a expressão cron (5 campos)")
		}
		p.CronTZ = strings.TrimSpace(p.CronTZ)
		if p.CronTZ == "" {
			p.CronTZ = "UTC"
		}
		if _, err := time.LoadLocation(p.CronTZ); err != nil {
			return Invalid("refresh_policy.cron_tz", "fuso desconhecido")
		}
	default:
		return Invalid("refresh_policy.mode", "use manual, interval, reactive ou cron")
	}
	if p.IntervalMinutes == 0 {
		p.IntervalMinutes = d.IntervalMinutes
	}
	if p.IntervalMinutes < 15 || p.IntervalMinutes > 1440 {
		return Invalid("refresh_policy.interval_minutes", "de 15 a 1440 minutos")
	}
	if p.DebounceSeconds == 0 {
		p.DebounceSeconds = d.DebounceSeconds
	}
	if p.DebounceSeconds < 30 || p.DebounceSeconds > 3600 {
		return Invalid("refresh_policy.debounce_seconds", "de 30 a 3600 segundos")
	}
	if p.MaxUpdatesPerHour == 0 {
		p.MaxUpdatesPerHour = d.MaxUpdatesPerHour
	}
	if p.MaxUpdatesPerHour < 1 || p.MaxUpdatesPerHour > 60 {
		return Invalid("refresh_policy.max_updates_per_hour", "de 1 a 60")
	}
	if p.DailyTokenCap == 0 {
		p.DailyTokenCap = d.DailyTokenCap
	}
	if p.DailyTokenCap < 1000 || p.DailyTokenCap > 100_000_000 {
		return Invalid("refresh_policy.daily_token_cap", "de 1.000 a 100.000.000 tokens")
	}
	if p.Mode == CardModeCron && p.Triggers == (CardTriggers{}) {
		p.Triggers = d.Triggers // no cron os gatilhos só valem para "atualizar só se mudou"
	}
	t := p.Triggers
	if !t.Status && !t.Membership && !t.HumanComments && !t.Assignee && !t.AnyUpdate {
		return Invalid("refresh_policy.triggers", "marque ao menos um tipo de mudança")
	}
	if p.ActiveHours != nil {
		ah := p.ActiveHours
		ah.Start, ah.End, ah.TZ = strings.TrimSpace(ah.Start), strings.TrimSpace(ah.End), strings.TrimSpace(ah.TZ)
		if ah.Start == "" && ah.End == "" {
			p.ActiveHours = nil
			return nil
		}
		if _, ok := clockMinutes(ah.Start); !ok {
			return Invalid("refresh_policy.active_hours.start", "use HH:MM")
		}
		if _, ok := clockMinutes(ah.End); !ok {
			return Invalid("refresh_policy.active_hours.end", "use HH:MM")
		}
		if ah.Start == ah.End {
			return Invalid("refresh_policy.active_hours", "o início e o fim não podem ser iguais")
		}
		if ah.TZ == "" {
			ah.TZ = "UTC"
		}
		if _, err := time.LoadLocation(ah.TZ); err != nil {
			return Invalid("refresh_policy.active_hours.tz", "fuso desconhecido")
		}
	}
	return nil
}

// clockMinutes lê HH:MM como minutos desde a meia-noite.
func clockMinutes(s string) (int, bool) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

func (a ActiveHours) loc() *time.Location {
	if l, err := time.LoadLocation(a.TZ); err == nil {
		return l
	}
	return time.UTC
}

// Contains diz se t está dentro da janela.
func (a ActiveHours) Contains(t time.Time) bool {
	s, ok1 := clockMinutes(a.Start)
	e, ok2 := clockMinutes(a.End)
	if !ok1 || !ok2 {
		return true
	}
	lt := t.In(a.loc())
	m := lt.Hour()*60 + lt.Minute()
	if s < e {
		return m >= s && m < e
	}
	return m >= s || m < e
}

// NextStart é o próximo início da janela depois de t (t mesmo, se já está dentro).
func (a ActiveHours) NextStart(t time.Time) time.Time {
	if a.Contains(t) {
		return t
	}
	s, _ := clockMinutes(a.Start)
	lt := t.In(a.loc())
	y, mo, d := lt.Date()
	cand := time.Date(y, mo, d, s/60, s%60, 0, 0, a.loc())
	if !cand.After(t) {
		cand = time.Date(y, mo, d+1, s/60, s%60, 0, 0, a.loc())
	}
	return cand.UTC()
}

// DayStart é o início do dia de t no fuso da política (o do horário ativo; senão UTC): o teto diário de
// tokens conta a partir dele.
func (p CardPolicy) DayStart(t time.Time) time.Time {
	loc := time.UTC
	if p.ActiveHours != nil {
		loc = p.ActiveHours.loc()
	}
	lt := t.In(loc)
	y, m, d := lt.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc).UTC()
}

// NextDay é o início do dia seguinte ao de t, no fuso da política.
func (p CardPolicy) NextDay(t time.Time) time.Time {
	loc := time.UTC
	if p.ActiveHours != nil {
		loc = p.ActiveHours.loc()
	}
	lt := t.In(loc)
	y, m, d := lt.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, loc).UTC()
}

// CardTaskPrint é o retrato de uma task observada.
type CardTaskPrint struct {
	Key            string     `json:"key"`
	Title          string     `json:"title"`
	Status         TaskStatus `json:"status"`
	Priority       Priority   `json:"priority"`
	PersonaID      string     `json:"persona_id,omitempty"`
	PersonaName    string     `json:"persona,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
	HumanCommentID string     `json:"human_comment_id,omitempty"`
	HumanCommentAt *time.Time `json:"human_comment_at,omitempty"`
}

// CardFingerprint é o retrato do conjunto: id da task → retrato.
type CardFingerprint map[string]CardTaskPrint

// Tipos de mudança.
const (
	ChangeEntered      = "entered"
	ChangeLeft         = "left"
	ChangeStatus       = "status"
	ChangeAssignee     = "assignee"
	ChangeHumanComment = "human_comment"
	ChangeUpdate       = "update"
)

// CardChange é uma mudança numa task observada desde a última versão do resumo.
type CardChange struct {
	TaskID     string `json:"task_id"`
	Key        string `json:"key"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	ChangeKind string `json:"change_kind"`
}

// counts diz se a mudança conta para os gatilhos escolhidos.
func (t CardTriggers) counts(kind string) bool {
	if t.AnyUpdate {
		return true
	}
	switch kind {
	case ChangeEntered, ChangeLeft:
		return t.Membership
	case ChangeStatus:
		return t.Status
	case ChangeAssignee:
		return t.Assignee
	case ChangeHumanComment:
		return t.HumanComments
	}
	return false
}

// DiffFingerprints compara o retrato guardado com o atual e devolve as mudanças que contam para os
// gatilhos, em ordem estável (chave da task, tipo).
func DiffFingerprints(old, cur CardFingerprint, tr CardTriggers) []CardChange {
	var out []CardChange
	add := func(c CardChange) {
		if tr.counts(c.ChangeKind) {
			out = append(out, c)
		}
	}
	for id, n := range cur {
		o, ok := old[id]
		if !ok {
			add(CardChange{TaskID: id, Key: n.Key, To: string(n.Status), ChangeKind: ChangeEntered})
			continue
		}
		other := true
		if o.Status != n.Status {
			add(CardChange{TaskID: id, Key: n.Key, From: string(o.Status), To: string(n.Status), ChangeKind: ChangeStatus})
			other = false
		}
		if o.PersonaID != n.PersonaID {
			add(CardChange{TaskID: id, Key: n.Key, From: o.PersonaName, To: n.PersonaName, ChangeKind: ChangeAssignee})
			other = false
		}
		if n.HumanCommentID != "" && o.HumanCommentID != n.HumanCommentID {
			add(CardChange{TaskID: id, Key: n.Key, To: n.HumanCommentID, ChangeKind: ChangeHumanComment})
			other = false
		}
		if other && (o.Title != n.Title || o.Priority != n.Priority || !o.UpdatedAt.Equal(n.UpdatedAt)) {
			add(CardChange{TaskID: id, Key: n.Key, From: o.UpdatedAt.UTC().Format(time.RFC3339), To: n.UpdatedAt.UTC().Format(time.RFC3339), ChangeKind: ChangeUpdate})
		}
	}
	for id, o := range old {
		if _, ok := cur[id]; !ok {
			add(CardChange{TaskID: id, Key: o.Key, From: string(o.Status), ChangeKind: ChangeLeft})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		if out[i].TaskID != out[j].TaskID {
			return out[i].TaskID < out[j].TaskID
		}
		return out[i].ChangeKind < out[j].ChangeKind
	})
	return out
}

// HashChanges é a impressão digital de um conjunto de mudanças (vazio = sem mudança): o debounce só reinicia
// quando ela muda.
func HashChanges(cs []CardChange) string {
	if len(cs) == 0 {
		return ""
	}
	b, _ := json.Marshal(cs)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// CardKindInput são os fatos que decidem entre resumo do zero (full) e incremental.
type CardKindInput struct {
	ExplicitFull          bool // pedido explícito de refazer
	HasSummary            bool
	Changes               int
	QueryChanged          bool // os filtros mudaram desde o resumo atual
	IncrementalsSinceFull int
	Restore               bool
}

// ChooseCardUpdateKind aplica a regra do ADR-0032.
func ChooseCardUpdateKind(in CardKindInput) CardUpdateKind {
	if in.ExplicitFull || !in.HasSummary || in.Changes > CardFullAfterChanges || in.QueryChanged ||
		in.IncrementalsSinceFull >= CardFullAfterIncrementals || in.Restore {
		return CardUpdateFull
	}
	return CardUpdateIncremental
}

// StatusCard é o card de status.
type StatusCard struct {
	ID                  ID                `json:"id"`
	CompanyID           ID                `json:"company_id"`
	Title               string            `json:"title"`
	TitlePinned         bool              `json:"title_pinned"`
	Interest            string            `json:"interest"`
	SummarizerPersonaID *ID               `json:"summarizer_persona_id,omitempty"`
	Queries             []StatusCardQuery `json:"queries"`
	QueryVersion        int               `json:"query_version"`
	QueryCompiledAt     *time.Time        `json:"query_compiled_at,omitempty"`
	Policy              CardPolicy        `json:"refresh_policy"`
	State               StatusCardState   `json:"state"`
	PendingChangeCount  int               `json:"pending_change_count"`
	LastChangeAt        *time.Time        `json:"last_change_at,omitempty"`
	FingerprintAt       *time.Time        `json:"fingerprint_at,omitempty"`
	MentionedTaskIDs    []ID              `json:"mentioned_task_ids"`
	Summary             string            `json:"summary"`
	SummaryRevision     int               `json:"summary_revision"`
	LastUpdateKind      CardUpdateKind    `json:"last_update_kind,omitempty"`
	LastGeneratedAt     *time.Time        `json:"last_generated_at,omitempty"`
	LastModel           string            `json:"last_model,omitempty"`
	GeneratingTaskID    *ID               `json:"generating_task_id,omitempty"`
	GeneratingSince     *time.Time        `json:"generating_since,omitempty"`
	FailureReason       string            `json:"failure_reason,omitempty"`
	NextEvalAt          *time.Time        `json:"next_eval_at,omitempty"`
	ArchivedAt          *time.Time        `json:"archived_at,omitempty"`
	// Engine, ArtifactName, ArtifactID e OnDashboard são do ADR-0048: quem escreve o resumo, o artefato versionado que
	// guarda o resultado (a versão mais nova) e a presença no Painel.
	Engine       CardEngine `json:"engine"`
	ArtifactName string     `json:"artifact_name,omitempty"`
	ArtifactID   *ID        `json:"artifact_id,omitempty"`
	OnDashboard  bool       `json:"on_dashboard"`
	CreatedBy    Actor      `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// View é o estado que a UI mostra (ordem do ADR-0032): arquivado, montando, erro, pausado por orçamento,
// pausado por horário, atualizando, desatualizado, em dia.
func (c StatusCard) View() StatusCardView {
	switch {
	case c.ArchivedAt != nil:
		return CardViewArchived
	case c.State == CardCompiling:
		return CardViewCompiling
	case c.State == CardError:
		return CardViewError
	case c.State == CardPausedBudget:
		return CardViewPausedBudget
	case c.State == CardPausedHours:
		return CardViewPausedHours
	case c.GeneratingTaskID != nil || (c.Engine == CardEngineDecision && c.GeneratingSince != nil):
		return CardViewUpdating
	case c.PendingChangeCount > 0:
		return CardViewStale
	}
	return CardViewFresh
}

// StatusCardRevision é uma versão imutável do resumo.
type StatusCardRevision struct {
	ID               ID             `json:"id"`
	CardID           ID             `json:"card_id"`
	Revision         int            `json:"revision"`
	Markdown         string         `json:"markdown"`
	ChangeSummary    string         `json:"change_summary,omitempty"`
	Kind             CardUpdateKind `json:"kind"`
	RunID            *ID            `json:"run_id,omitempty"`
	Model            string         `json:"model,omitempty"`
	MentionedTaskIDs []ID           `json:"mentioned_task_ids"`
	CreatedAt        time.Time      `json:"created_at"`
}

// StatusCardUpdate é uma rodada (montagem ou geração).
type StatusCardUpdate struct {
	ID            ID                `json:"id"`
	CardID        ID                `json:"card_id"`
	Kind          CardUpdateKind    `json:"kind"`
	Trigger       CardUpdateTrigger `json:"trigger"`
	TaskID        *ID               `json:"task_id,omitempty"`
	RunID         *ID               `json:"run_id,omitempty"`
	Changes       []CardChange      `json:"changes"`
	InputTokens   int64             `json:"input_tokens"`
	OutputTokens  int64             `json:"output_tokens"`
	CostUSD       *float64          `json:"cost_usd,omitempty"`
	Model         string            `json:"model,omitempty"`
	QueryVersion  int               `json:"query_version"`
	ChangeSummary string            `json:"change_summary,omitempty"`
	StartedAt     time.Time         `json:"started_at"`
	FinishedAt    *time.Time        `json:"finished_at,omitempty"`
	Status        CardUpdateStatus  `json:"status"`
	Error         string            `json:"error,omitempty"`
}
