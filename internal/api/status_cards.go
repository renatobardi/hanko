package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cards de status (ADR-0032): ler é de qualquer membro (e de run tokens da empresa); criar, editar e atualizar
// é de member; arquivar é de quem criou ou de admin; apagar é de admin (o service confere). As escritas da
// persona resumidora (filtros e resumo) só existem no MCP.

// statusCardsBackend é o subconjunto do service para cards de status; fica fora de backend para que os fakes
// dos testes não precisem implementá-lo.
type statusCardsBackend interface {
	ListStatusCards(ctx context.Context, companyID domain.ID, archived *bool) ([]service.StatusCardView, error)
	GetStatusCard(ctx context.Context, companyID, cardID domain.ID) (service.StatusCardView, error)
	CreateStatusCard(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateStatusCardInput) (service.StatusCardView, error)
	UpdateStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID, in service.UpdateStatusCardInput) (service.StatusCardView, error)
	DeleteStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID) error
	RecompileStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID) (service.StatusCardView, error)
	RefreshStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID, full bool) (service.StatusCardView, error)
	ListStatusCardUpdates(ctx context.Context, companyID, cardID domain.ID, limit int) ([]service.StatusCardUpdateView, error)
	ListStatusCardRevisions(ctx context.Context, companyID, cardID domain.ID, limit int) ([]domain.StatusCardRevision, error)
	PreviewStatusCard(ctx context.Context, companyID, cardID domain.ID) (service.StatusCardPreview, error)
}

func (s *server) statusCards() (statusCardsBackend, error) {
	b, ok := s.svc.(statusCardsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "cards de status indisponíveis neste servidor")
	}
	return b, nil
}

// StatusCardTriggers diz o que conta como mudança numa task observada.
type StatusCardTriggers struct {
	Status        bool `json:"status,omitempty" doc:"Mudou o status de uma task observada."`
	Membership    bool `json:"membership,omitempty" doc:"Uma task entrou ou saiu do conjunto."`
	HumanComments bool `json:"human_comments,omitempty" doc:"Um humano comentou numa task observada."`
	Assignee      bool `json:"assignee,omitempty" doc:"Mudou a persona responsável."`
	AnyUpdate     bool `json:"any_update,omitempty" doc:"Qualquer alteração (inclui título e prioridade)."`
}

// StatusCardActiveHours é a janela do dia em que o card se atualiza sozinho.
type StatusCardActiveHours struct {
	Start string `json:"start" example:"09:00" doc:"Início, HH:MM."`
	End   string `json:"end" example:"18:00" doc:"Fim, HH:MM; antes do início atravessa a meia-noite."`
	TZ    string `json:"tz,omitempty" example:"America/Sao_Paulo" doc:"Fuso IANA; padrão UTC."`
}

// StatusCardPolicy é a política de atualização do resumo.
type StatusCardPolicy struct {
	Mode              string                 `json:"mode,omitempty" enum:"manual,interval,reactive" doc:"Padrão reactive. manual = só sob pedido; interval = em horário fixo, se algo mudou; reactive = assim que algo mudar, com espera."`
	IntervalMinutes   int                    `json:"interval_minutes,omitempty" minimum:"0" maximum:"1440" doc:"Modo interval: de 15 a 1440 minutos (0 = padrão 60)."`
	DebounceSeconds   int                    `json:"debounce_seconds,omitempty" minimum:"0" maximum:"3600" doc:"Modo reactive: espera sem mudanças novas antes de atualizar, de 30 a 3600 s (0 = padrão 300)."`
	MaxUpdatesPerHour int                    `json:"max_updates_per_hour,omitempty" minimum:"0" maximum:"60" doc:"Teto de atualizações automáticas por hora, de 1 a 60 (0 = padrão 6)."`
	Triggers          StatusCardTriggers     `json:"triggers,omitempty" doc:"O que conta como mudança; vazio = status, entrada/saída e comentários humanos."`
	ActiveHours       *StatusCardActiveHours `json:"active_hours,omitempty" doc:"Horário em que o card se atualiza sozinho; fora dele fica pausado."`
	DailyTokenCap     int64                  `json:"daily_token_cap,omitempty" minimum:"0" doc:"Teto de tokens por dia somando as rodadas do card (0 = padrão 100000); atingido, o card pausa."`
}

// StatusCardQuery é um filtro de task do card.
type StatusCardQuery struct {
	Label      string   `json:"label,omitempty"`
	Statuses   []string `json:"status,omitempty" doc:"Status aceitos."`
	Priorities []string `json:"priority,omitempty" doc:"Prioridades aceitas."`
	PersonaID  string   `json:"persona_id,omitempty" format:"uuid"`
	ProjectID  string   `json:"project_id,omitempty" format:"uuid"`
	GoalID     string   `json:"goal_id,omitempty" format:"uuid"`
	Text       string   `json:"text,omitempty" doc:"Texto no título ou na descrição."`
	OpenOnly   bool     `json:"open,omitempty" doc:"Só tasks abertas."`
}

// StatusCardTask é uma task citada no resumo.
type StatusCardTask struct {
	ID          string `json:"id" format:"uuid"`
	Key         string `json:"key" example:"HNK-12"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Priority    string `json:"priority,omitempty"`
	PersonaName string `json:"persona_name,omitempty"`
}

// StatusCard é um resumo vivo de um conjunto de tasks, mantido por uma persona resumidora.
type StatusCard struct {
	ID                  string            `json:"id" format:"uuid"`
	CompanyID           string            `json:"company_id" format:"uuid"`
	Title               string            `json:"title" doc:"Vazio até a resumidora propor um (ou o humano escrever)."`
	TitlePinned         bool              `json:"title_pinned" doc:"Título fixado pelo humano: a resumidora não o troca."`
	Interest            string            `json:"interest" doc:"O que acompanhar, em linguagem natural."`
	SummarizerPersonaID string            `json:"summarizer_persona_id,omitempty" format:"uuid"`
	SummarizerSlug      string            `json:"summarizer_slug,omitempty"`
	SummarizerName      string            `json:"summarizer_name,omitempty"`
	Queries             []StatusCardQuery `json:"queries" doc:"Filtros gravados pela resumidora na montagem (vazio antes dela)."`
	QueryVersion        int               `json:"query_version" doc:"Sobe a cada montagem gravada."`
	QueryCompiledAt     *time.Time        `json:"query_compiled_at,omitempty"`
	Policy              StatusCardPolicy  `json:"refresh_policy"`
	State               string            `json:"state" enum:"compiling,active,error,paused_budget,paused_hours"`
	ViewState           string            `json:"view_state" enum:"archived,compiling,error,paused_budget,paused_hours,updating,stale,fresh" doc:"O estado mostrado: arquivado, montando, erro, pausado (orçamento ou horário), atualizando, desatualizado ou em dia."`
	PendingChangeCount  int               `json:"pending_change_count" doc:"Mudanças nas tasks observadas desde a última versão do resumo."`
	LastChangeAt        *time.Time        `json:"last_change_at,omitempty"`
	Summary             string            `json:"summary" doc:"O resumo atual em markdown (até 64 KiB)."`
	SummaryRevision     int               `json:"summary_revision"`
	LastUpdateKind      string            `json:"last_update_kind,omitempty" enum:"full,incremental"`
	LastGeneratedAt     *time.Time        `json:"last_generated_at,omitempty"`
	LastModel           string            `json:"last_model,omitempty"`
	GeneratingTaskID    string            `json:"generating_task_id,omitempty" format:"uuid" doc:"Task de sistema da montagem ou atualização em andamento."`
	FailureReason       string            `json:"failure_reason,omitempty" doc:"Por que a última montagem ou atualização não terminou."`
	NextEvalAt          *time.Time        `json:"next_eval_at,omitempty"`
	ArchivedAt          *time.Time        `json:"archived_at,omitempty"`
	Mentioned           []StatusCardTask  `json:"mentioned,omitempty" doc:"Tasks citadas no resumo atual (só no detalhe)."`
	CreatedBy           ActorRef          `json:"created_by"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

// StatusCardChange é uma mudança numa task observada.
type StatusCardChange struct {
	TaskID     string `json:"task_id" format:"uuid"`
	Key        string `json:"key"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	ChangeKind string `json:"change_kind" enum:"entered,left,status,assignee,human_comment,update"`
}

// StatusCardUpdate é uma rodada do card: montagem ou geração do resumo, com o custo da run.
type StatusCardUpdate struct {
	ID            string             `json:"id" format:"uuid"`
	Kind          string             `json:"kind" enum:"compile,full,incremental"`
	Trigger       string             `json:"trigger" enum:"manual,interval,reactive,restore,query"`
	TaskID        string             `json:"task_id,omitempty" format:"uuid"`
	TaskKey       string             `json:"task_key,omitempty"`
	RunID         string             `json:"run_id,omitempty" format:"uuid"`
	RunName       string             `json:"run_name,omitempty"`
	Changes       []StatusCardChange `json:"changes"`
	InputTokens   int64              `json:"input_tokens"`
	OutputTokens  int64              `json:"output_tokens"`
	CostUSD       *float64           `json:"cost_usd,omitempty" doc:"Custo da run (do ledger), quando já terminou."`
	Model         string             `json:"model,omitempty"`
	QueryVersion  int                `json:"query_version"`
	ChangeSummary string             `json:"change_summary,omitempty"`
	Status        string             `json:"status" enum:"running,ok,failed"`
	Error         string             `json:"error,omitempty"`
	StartedAt     time.Time          `json:"started_at"`
	FinishedAt    *time.Time         `json:"finished_at,omitempty"`
}

// StatusCardRevision é uma versão imutável do resumo.
type StatusCardRevision struct {
	ID               string    `json:"id" format:"uuid"`
	Revision         int       `json:"revision"`
	Markdown         string    `json:"markdown"`
	ChangeSummary    string    `json:"change_summary,omitempty"`
	Kind             string    `json:"kind" enum:"full,incremental"`
	RunID            string    `json:"run_id,omitempty" format:"uuid"`
	Model            string    `json:"model,omitempty"`
	MentionedTaskIDs []string  `json:"mentioned_task_ids"`
	CreatedAt        time.Time `json:"created_at"`
}

// StatusCardMatch é uma task que casa hoje com os filtros.
type StatusCardMatch struct {
	StatusCardTask
	UpdatedAt      time.Time  `json:"updated_at"`
	HumanCommentAt *time.Time `json:"human_comment_at,omitempty" doc:"Último comentário humano."`
	Queries        []int      `json:"queries" doc:"Índices (base 0) dos filtros que trazem a task."`
}

// StatusCardQueryMatch é um filtro com a descrição e quantas tasks casam.
type StatusCardQueryMatch struct {
	Query       StatusCardQuery `json:"query"`
	Description string          `json:"description"`
	Matches     int             `json:"matches"`
}

// StatusCardPreview é a depuração dos filtros: o que casa hoje.
type StatusCardPreview struct {
	QueryVersion int                    `json:"query_version"`
	Queries      []StatusCardQueryMatch `json:"queries"`
	Tasks        []StatusCardMatch      `json:"tasks"`
	Truncated    bool                   `json:"truncated" doc:"Há mais tasks do que o card observa (300)."`
}

type StatusCardPath struct {
	CompanyPath
	Card string `path:"card" format:"uuid" doc:"Id do card."`
}

type listStatusCardsIn struct {
	CompanyPath
	Page
	Archived string `query:"archived" enum:"false,true,all" default:"false" doc:"false = só os ativos; true = só os arquivados; all = todos."`
}

type createStatusCardIn struct {
	CompanyPath
	Body struct {
		Interest    string            `json:"interest" minLength:"1" maxLength:"20000" doc:"O que acompanhar, em linguagem natural."`
		Title       string            `json:"title,omitempty" maxLength:"200"`
		TitlePinned bool              `json:"title_pinned,omitempty" doc:"Fixa o título (a resumidora não o troca)."`
		Summarizer  string            `json:"summarizer,omitempty" doc:"Id ou slug da persona resumidora; sem ela o card fica em erro até alguém escolher."`
		Policy      *StatusCardPolicy `json:"refresh_policy,omitempty" doc:"Padrão: reactive, 5 min de espera, 6 por hora, 100 mil tokens por dia."`
	}
}

type updateStatusCardIn struct {
	StatusCardPath
	Body struct {
		Interest    *string           `json:"interest,omitempty" minLength:"1" maxLength:"20000" doc:"Mudar o interesse dispara a montagem de novo."`
		Title       *string           `json:"title,omitempty" maxLength:"200"`
		TitlePinned *bool             `json:"title_pinned,omitempty"`
		Summarizer  string            `json:"summarizer,omitempty" doc:"Id ou slug da nova resumidora."`
		Policy      *StatusCardPolicy `json:"refresh_policy,omitempty"`
		Archived    *bool             `json:"archived,omitempty" doc:"true arquiva; false restaura (e pede um resumo do zero)."`
	}
}

type refreshStatusCardIn struct {
	StatusCardPath
	Body struct {
		Full bool `json:"full,omitempty" doc:"Refaz o resumo do zero em vez de incorporar só as mudanças."`
	}
}

type scHistoryIn struct {
	StatusCardPath
	Page
}

func scPolicyIn(p *StatusCardPolicy) *domain.CardPolicy {
	if p == nil {
		return nil
	}
	out := domain.CardPolicy{Mode: p.Mode, IntervalMinutes: p.IntervalMinutes, DebounceSeconds: p.DebounceSeconds, MaxUpdatesPerHour: p.MaxUpdatesPerHour,
		Triggers: domain.CardTriggers(p.Triggers), DailyTokenCap: p.DailyTokenCap}
	if out.Triggers == (domain.CardTriggers{}) {
		out.Triggers = domain.DefaultCardPolicy().Triggers
	}
	if p.ActiveHours != nil {
		out.ActiveHours = &domain.ActiveHours{Start: p.ActiveHours.Start, End: p.ActiveHours.End, TZ: p.ActiveHours.TZ}
	}
	return &out
}

func scPolicyOut(p domain.CardPolicy) StatusCardPolicy {
	out := StatusCardPolicy{Mode: p.Mode, IntervalMinutes: p.IntervalMinutes, DebounceSeconds: p.DebounceSeconds, MaxUpdatesPerHour: p.MaxUpdatesPerHour,
		Triggers: StatusCardTriggers(p.Triggers), DailyTokenCap: p.DailyTokenCap}
	if p.ActiveHours != nil {
		out.ActiveHours = &StatusCardActiveHours{Start: p.ActiveHours.Start, End: p.ActiveHours.End, TZ: p.ActiveHours.TZ}
	}
	return out
}

func scQueryOut(q domain.StatusCardQuery) StatusCardQuery {
	o := StatusCardQuery{Label: q.Label, PersonaID: idPtr(q.PersonaID), ProjectID: idPtr(q.ProjectID), GoalID: idPtr(q.GoalID), Text: q.Text, OpenOnly: q.OpenOnly,
		Statuses: []string{}, Priorities: []string{}}
	for _, s := range q.Statuses {
		o.Statuses = append(o.Statuses, string(s))
	}
	for _, p := range q.Priorities {
		o.Priorities = append(o.Priorities, string(p))
	}
	return o
}

func statusCardOut(v service.StatusCardView) StatusCard {
	o := StatusCard{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Title: v.Title, TitlePinned: v.TitlePinned, Interest: v.Interest,
		SummarizerPersonaID: idPtr(v.SummarizerPersonaID), SummarizerSlug: v.SummarizerSlug, SummarizerName: v.SummarizerName,
		Queries: []StatusCardQuery{}, QueryVersion: v.QueryVersion, QueryCompiledAt: v.QueryCompiledAt, Policy: scPolicyOut(v.Policy),
		State: string(v.State), ViewState: string(v.ViewState), PendingChangeCount: v.PendingChangeCount, LastChangeAt: v.LastChangeAt,
		Summary: v.Summary, SummaryRevision: v.SummaryRevision, LastUpdateKind: string(v.LastUpdateKind), LastGeneratedAt: v.LastGeneratedAt,
		LastModel: v.LastModel, GeneratingTaskID: idPtr(v.GeneratingTaskID), FailureReason: v.FailureReason, NextEvalAt: v.NextEvalAt,
		ArchivedAt: v.ArchivedAt, CreatedBy: actorRef(v.CreatedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	for _, q := range v.Queries {
		o.Queries = append(o.Queries, scQueryOut(q))
	}
	for _, m := range v.Mentioned {
		o.Mentioned = append(o.Mentioned, scTaskOut(m))
	}
	return o
}

func scTaskOut(m service.CardTaskRef) StatusCardTask {
	return StatusCardTask{ID: idStr(m.ID), Key: m.Key, Title: m.Title, Status: string(m.Status), Priority: string(m.Priority), PersonaName: m.PersonaName}
}

func scUpdateOut(u service.StatusCardUpdateView) StatusCardUpdate {
	o := StatusCardUpdate{ID: idStr(u.ID), Kind: string(u.Kind), Trigger: string(u.Trigger), TaskID: idPtr(u.TaskID), TaskKey: u.TaskKey,
		RunID: idPtr(u.RunID), RunName: u.RunName, Changes: []StatusCardChange{}, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CostUSD: u.CostUSD, Model: u.Model, QueryVersion: u.QueryVersion, ChangeSummary: u.ChangeSummary, Status: string(u.Status), Error: u.Error,
		StartedAt: u.StartedAt, FinishedAt: u.FinishedAt}
	for _, c := range u.Changes {
		o.Changes = append(o.Changes, StatusCardChange{TaskID: c.TaskID, Key: c.Key, From: c.From, To: c.To, ChangeKind: c.ChangeKind})
	}
	return o
}

func scRevisionOut(r domain.StatusCardRevision) StatusCardRevision {
	o := StatusCardRevision{ID: idStr(r.ID), Revision: r.Revision, Markdown: r.Markdown, ChangeSummary: r.ChangeSummary, Kind: string(r.Kind),
		RunID: idPtr(r.RunID), Model: r.Model, MentionedTaskIDs: []string{}, CreatedAt: r.CreatedAt}
	for _, id := range r.MentionedTaskIDs {
		o.MentionedTaskIDs = append(o.MentionedTaskIDs, id.String())
	}
	return o
}

func scPreviewOut(p service.StatusCardPreview) StatusCardPreview {
	o := StatusCardPreview{QueryVersion: p.QueryVersion, Truncated: p.Truncated, Queries: []StatusCardQueryMatch{}, Tasks: []StatusCardMatch{}}
	for _, q := range p.Queries {
		o.Queries = append(o.Queries, StatusCardQueryMatch{Query: scQueryOut(q.Query), Description: q.Description, Matches: q.Matches})
	}
	for _, t := range p.Tasks {
		o.Tasks = append(o.Tasks, StatusCardMatch{StatusCardTask: scTaskOut(t.CardTaskRef), UpdatedAt: t.UpdatedAt, HumanCommentAt: t.HumanCommentAt, Queries: t.Queries})
	}
	return o
}

// scCall resolve a empresa e o card do caminho.
func (s *server) scCall(ctx context.Context, in StatusCardPath) (statusCardsBackend, scope, domain.ID, error) {
	b, err := s.statusCards()
	if err != nil {
		return nil, scope{}, domain.ID{}, err
	}
	sc, err := s.scope(ctx, in.Company)
	if err != nil {
		return nil, scope{}, domain.ID{}, err
	}
	id, err := domain.ParseID(in.Card)
	if err != nil {
		return nil, scope{}, domain.ID{}, s.fail(ctx, domain.Invalid("card", "use o id (UUID) do card"))
	}
	return b, sc, id, nil
}

func (s *server) registerStatusCards(api huma.API) {
	const tag = "status-cards"
	base := "/companies/{company}/status-cards"

	huma.Register(api, op("list-status-cards", http.MethodGet, base, tag, "Lista os cards de status"),
		func(ctx context.Context, in *listStatusCardsIn) (*ListOut[StatusCard], error) {
			b, err := s.statusCards()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			var archived *bool
			switch in.Archived {
			case "true":
				archived = scBoolPtr(true)
			case "all":
			default:
				archived = scBoolPtr(false)
			}
			vs, err := b.ListStatusCards(ctx, sc.Company.ID, archived)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(vs, in.Page, statusCardOut), nil
		})

	huma.Register(api, created(op("create-status-card", http.MethodPost, base, tag, "Cria um card de status e pede a montagem dos filtros à resumidora (member)")),
		func(ctx context.Context, in *createStatusCardIn) (*ItemOut[StatusCard], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.statusCards()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			summarizer, err := s.personaRef(ctx, sc.Company.ID, in.Body.Summarizer)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := b.CreateStatusCard(ctx, sc.Actor, sc.Company.ID, service.CreateStatusCardInput{Interest: in.Body.Interest, Title: in.Body.Title,
				TitlePinned: in.Body.TitlePinned, SummarizerPersonaID: summarizer, Policy: scPolicyIn(in.Body.Policy)})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[StatusCard]{Body: statusCardOut(v)}, nil
		})

	huma.Register(api, op("get-status-card", http.MethodGet, base+"/{card}", tag, "Obtém um card de status com o resumo e as tasks citadas"),
		func(ctx context.Context, in *StatusCardPath) (*ItemOut[StatusCard], error) {
			b, sc, id, err := s.scCall(ctx, *in)
			if err != nil {
				return nil, err
			}
			v, err := b.GetStatusCard(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[StatusCard]{Body: statusCardOut(v)}, nil
		})

	huma.Register(api, op("update-status-card", http.MethodPatch, base+"/{card}", tag,
		"Altera um card de status; mudar o interesse remonta os filtros; archived arquiva ou restaura"),
		func(ctx context.Context, in *updateStatusCardIn) (*ItemOut[StatusCard], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, sc, id, err := s.scCall(ctx, in.StatusCardPath)
			if err != nil {
				return nil, err
			}
			up := service.UpdateStatusCardInput{Interest: in.Body.Interest, Title: in.Body.Title, TitlePinned: in.Body.TitlePinned,
				Policy: scPolicyIn(in.Body.Policy), Archived: in.Body.Archived}
			if up.SummarizerPersonaID, err = s.personaRef(ctx, sc.Company.ID, in.Body.Summarizer); err != nil {
				return nil, s.fail(ctx, err)
			}
			if _, err := b.UpdateStatusCard(ctx, sc.Actor, sc.Company.ID, id, up); err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := b.GetStatusCard(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[StatusCard]{Body: statusCardOut(v)}, nil
		})

	huma.Register(api, noContent(op("delete-status-card", http.MethodDelete, base+"/{card}", tag, "Apaga um card de status com o histórico (admin)")),
		func(ctx context.Context, in *StatusCardPath) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, sc, id, err := s.scCall(ctx, *in)
			if err != nil {
				return nil, err
			}
			if err := b.DeleteStatusCard(ctx, sc.Actor, sc.Company.ID, id); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("list-status-card-updates", http.MethodGet, base+"/{card}/updates", tag, "Lista as rodadas do card (montagens e atualizações, com tokens e custo)"),
		func(ctx context.Context, in *scHistoryIn) (*ListOut[StatusCardUpdate], error) {
			b, sc, id, err := s.scCall(ctx, in.StatusCardPath)
			if err != nil {
				return nil, err
			}
			us, err := b.ListStatusCardUpdates(ctx, sc.Company.ID, id, domain.MaxCardUpdates)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(us, in.Page, scUpdateOut), nil
		})

	huma.Register(api, op("list-status-card-revisions", http.MethodGet, base+"/{card}/revisions", tag, "Lista as versões do resumo, da mais nova para a mais antiga (até 50)"),
		func(ctx context.Context, in *scHistoryIn) (*ListOut[StatusCardRevision], error) {
			b, sc, id, err := s.scCall(ctx, in.StatusCardPath)
			if err != nil {
				return nil, err
			}
			rs, err := b.ListStatusCardRevisions(ctx, sc.Company.ID, id, domain.MaxCardRevisions)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(rs, in.Page, scRevisionOut), nil
		})

	huma.Register(api, op("recompile-status-card", http.MethodPost, base+"/{card}/recompile", tag, "Pede a montagem dos filtros de novo (member)"),
		func(ctx context.Context, in *StatusCardPath) (*ItemOut[StatusCard], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, sc, id, err := s.scCall(ctx, *in)
			if err != nil {
				return nil, err
			}
			v, err := b.RecompileStatusCard(ctx, sc.Actor, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[StatusCard]{Body: statusCardOut(v)}, nil
		})

	huma.Register(api, op("refresh-status-card", http.MethodPost, base+"/{card}/refresh", tag,
		"Atualiza o resumo agora, sem esperar a política (member); respeita o teto diário de tokens"),
		func(ctx context.Context, in *refreshStatusCardIn) (*ItemOut[StatusCard], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, sc, id, err := s.scCall(ctx, in.StatusCardPath)
			if err != nil {
				return nil, err
			}
			v, err := b.RefreshStatusCard(ctx, sc.Actor, sc.Company.ID, id, in.Body.Full)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[StatusCard]{Body: statusCardOut(v)}, nil
		})

	huma.Register(api, op("preview-status-card", http.MethodGet, base+"/{card}/preview", tag, "Mostra quais tasks casam hoje com os filtros do card (depuração)"),
		func(ctx context.Context, in *StatusCardPath) (*ItemOut[StatusCardPreview], error) {
			b, sc, id, err := s.scCall(ctx, *in)
			if err != nil {
				return nil, err
			}
			p, err := b.PreviewStatusCard(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[StatusCardPreview]{Body: scPreviewOut(p)}, nil
		})
}

func scBoolPtr[T any](v T) *T { return &v }
