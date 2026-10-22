package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Cards de status (ADR-0032).
//
// Um card é um resumo vivo de um conjunto de tasks. O humano escreve o interesse; a persona resumidora o
// transforma em filtros de task (montagem: hanko_status_card_set_query) e mantém um resumo em markdown
// (atualização: hanko_status_card_set_summary). Montagem e atualização são tasks de sistema da resumidora,
// executadas pela máquina normal de runs (gatilho status_card): orçamento, ledger, alertas e log ao vivo valem
// como para qualquer run. Papéis: viewer lê; member cria, edita e atualiza qualquer card; arquivar é do autor
// ou de admin; apagar é de admin. Uma persona cria até 20 cards (MCP) e só mexe nos que criou.

// StatusCardView é o card com o nome da resumidora e o estado mostrado.
type StatusCardView struct {
	domain.StatusCard
	SummarizerName string                `json:"summarizer_name,omitempty"`
	SummarizerSlug string                `json:"summarizer_slug,omitempty"`
	ViewState      domain.StatusCardView `json:"view_state"`
	// Mentioned são as tasks citadas no resumo atual (só no detalhe).
	Mentioned []CardTaskRef `json:"mentioned,omitempty"`
}

// CardTaskRef é uma task citada ou observada, já com a chave.
type CardTaskRef struct {
	ID          domain.ID         `json:"id"`
	Key         string            `json:"key"`
	Title       string            `json:"title"`
	Status      domain.TaskStatus `json:"status"`
	Priority    domain.Priority   `json:"priority,omitempty"`
	PersonaName string            `json:"persona_name,omitempty"`
}

// StatusCardUpdateView é uma rodada com a chave da task e o nome da run.
type StatusCardUpdateView struct {
	domain.StatusCardUpdate
	TaskKey string `json:"task_key,omitempty"`
	RunName string `json:"run_name,omitempty"`
}

// CardMatchedTask é uma task que casa hoje com os filtros do card.
type CardMatchedTask struct {
	CardTaskRef
	PersonaID      *domain.ID `json:"persona_id,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
	HumanCommentID string     `json:"human_comment_id,omitempty"`
	HumanCommentAt *time.Time `json:"human_comment_at,omitempty"`
	HumanComment   string     `json:"human_comment,omitempty"`
	// Queries são os índices (base 0) dos filtros que a trazem.
	Queries []int `json:"queries"`
}

// CardQueryPreview é um filtro com a descrição legível e quantas tasks casam.
type CardQueryPreview struct {
	Query       domain.StatusCardQuery `json:"query"`
	Description string                 `json:"description"`
	Matches     int                    `json:"matches"`
}

// StatusCardPreview é a depuração dos filtros: o que casa hoje.
type StatusCardPreview struct {
	QueryVersion int                `json:"query_version"`
	Queries      []CardQueryPreview `json:"queries"`
	Tasks        []CardMatchedTask  `json:"tasks"`
	// Truncated diz que havia mais tasks do que o card observa (domain.MaxCardObservedTasks).
	Truncated bool `json:"truncated"`
}

// CreateStatusCardInput cria um card. Sem resumidora o card fica em erro até alguém escolher uma.
type CreateStatusCardInput struct {
	Interest            string
	Title               string
	TitlePinned         bool
	SummarizerPersonaID *domain.ID
	Policy              *domain.CardPolicy // nil = padrão
	// Engine escolhe quem escreve o resumo: a persona resumidora (padrão) ou o decision model (ADR-0048). No motor
	// decision os filtros vêm daqui (vazio = as tasks abertas) e não há montagem nem resumidora.
	Engine      domain.CardEngine
	Queries     []CardQueryInput
	OnDashboard bool // mostra o card no Painel
}

// UpdateStatusCardInput altera um card; nil fica como está. Mudar o interesse (ou a resumidora de um card sem
// filtros) dispara uma montagem nova.
type UpdateStatusCardInput struct {
	Interest            *string
	Title               *string
	TitlePinned         *bool
	SummarizerPersonaID *domain.ID
	Policy              *domain.CardPolicy
	Archived            *bool
	// Queries troca os filtros de um card cujo motor é o decision model (no motor persona quem grava os filtros é a
	// resumidora); OnDashboard liga ou desliga o card no Painel.
	Queries     *[]CardQueryInput
	OnDashboard *bool
}

// CardQueryInput é um filtro como a resumidora o informa: referências por slug ou id.
type CardQueryInput struct {
	Label      string   `json:"label,omitempty"`
	Statuses   []string `json:"status,omitempty"`
	Priorities []string `json:"priority,omitempty"`
	Persona    string   `json:"persona,omitempty"`
	Project    string   `json:"project,omitempty"`
	Goal       string   `json:"goal,omitempty"`
	Text       string   `json:"text,omitempty"`
	OpenOnly   bool     `json:"open,omitempty"`
}

// SetCardQueryInput é o que a resumidora grava na montagem.
type SetCardQueryInput struct {
	Queries       []CardQueryInput
	Title         string
	ChangeSummary string
}

// SetCardSummaryInput é o que a resumidora grava na atualização.
type SetCardSummaryInput struct {
	Markdown      string
	Title         string
	ChangeSummary string
	Mentioned     []string // chaves das tasks citadas (HNK-12)
}

// CardRunRef identifica a run da resumidora (do token MCP): só ela escreve, e só na task de geração ativa.
type CardRunRef struct {
	PersonaID domain.ID
	TaskID    domain.ID
	RunID     *domain.ID
}

// ---------- conversões ----------

func cardFromRow(r store.StatusCard) domain.StatusCard {
	c := domain.StatusCard{ID: r.ID, CompanyID: r.CompanyID, Title: r.Title, TitlePinned: r.TitlePinned, Interest: r.Interest,
		SummarizerPersonaID: r.SummarizerPersonaID, QueryVersion: int(r.QueryVersion), QueryCompiledAt: r.QueryCompiledAt,
		State: domain.StatusCardState(r.State), PendingChangeCount: int(r.PendingChangeCount), LastChangeAt: r.LastChangeAt,
		FingerprintAt: r.FingerprintAt, MentionedTaskIDs: r.MentionedTaskIds, Summary: r.Summary, SummaryRevision: int(r.SummaryRevision),
		LastGeneratedAt: r.LastGeneratedAt, LastModel: r.LastModel, GeneratingTaskID: r.GeneratingTaskID, GeneratingSince: r.GeneratingSince,
		FailureReason: r.FailureReason, NextEvalAt: r.NextEvalAt, ArchivedAt: r.ArchivedAt, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		Engine: domain.CardEngine(r.Engine), ArtifactName: r.ArtifactName, ArtifactID: r.ArtifactID, OnDashboard: r.OnDashboard,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.LastUpdateKind != nil {
		c.LastUpdateKind = domain.CardUpdateKind(*r.LastUpdateKind)
	}
	if len(r.Queries) > 0 {
		_ = json.Unmarshal(r.Queries, &c.Queries) // gravado pelo próprio service
	}
	if c.Queries == nil {
		c.Queries = []domain.StatusCardQuery{}
	}
	c.Policy = policyFromRaw(r.RefreshPolicy)
	if c.MentionedTaskIDs == nil {
		c.MentionedTaskIDs = []domain.ID{}
	}
	return c
}

func policyFromRaw(raw []byte) domain.CardPolicy {
	p := domain.DefaultCardPolicy()
	if len(raw) > 0 && string(raw) != "{}" {
		var got domain.CardPolicy
		if err := json.Unmarshal(raw, &got); err == nil && got.Normalize() == nil {
			p = got
		}
	}
	return p
}

func fingerprintFromRaw(raw []byte) domain.CardFingerprint {
	fp := domain.CardFingerprint{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &fp)
	}
	return fp
}

func changesFromRaw(raw []byte) []domain.CardChange {
	var cs []domain.CardChange
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cs)
	}
	if cs == nil {
		cs = []domain.CardChange{}
	}
	return cs
}

func cardUpdateFromRow(u store.StatusCardUpdate) domain.StatusCardUpdate {
	return domain.StatusCardUpdate{ID: u.ID, CardID: u.CardID, Kind: domain.CardUpdateKind(u.Kind), Trigger: domain.CardUpdateTrigger(u.Trigger),
		TaskID: u.TaskID, RunID: u.RunID, Changes: changesFromRaw(u.Changes), InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CostUSD: u.CostUsd, Model: u.Model, QueryVersion: int(u.QueryVersion), ChangeSummary: u.ChangeSummary, StartedAt: u.StartedAt,
		FinishedAt: u.FinishedAt, Status: domain.CardUpdateStatus(u.Status), Error: u.Error}
}

func cardRevisionFromRow(r store.StatusCardRevision) domain.StatusCardRevision {
	ids := r.MentionedTaskIds
	if ids == nil {
		ids = []domain.ID{}
	}
	return domain.StatusCardRevision{ID: r.ID, CardID: r.CardID, Revision: int(r.Revision), Markdown: r.Markdown, ChangeSummary: r.ChangeSummary,
		Kind: domain.CardUpdateKind(r.Kind), RunID: r.RunID, Model: r.Model, MentionedTaskIDs: ids, CreatedAt: r.CreatedAt}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

// saveCard grava o card inteiro (os campos mutáveis) e devolve a linha nova.
func (t *txc) saveCard(c store.StatusCard) (store.StatusCard, error) {
	row, err := t.q.UpdateStatusCard(t.ctx, store.UpdateStatusCardParams{CompanyID: c.CompanyID, ID: c.ID, Title: c.Title, TitlePinned: c.TitlePinned,
		Interest: c.Interest, InterestHash: c.InterestHash, SummarizerPersonaID: c.SummarizerPersonaID, Queries: c.Queries,
		QueryVersion: c.QueryVersion, QueryCompiledAt: c.QueryCompiledAt, RefreshPolicy: c.RefreshPolicy, State: c.State,
		PendingChangeCount: c.PendingChangeCount, PendingChangeHash: c.PendingChangeHash, LastChangeAt: c.LastChangeAt,
		Fingerprint: c.Fingerprint, FingerprintAt: c.FingerprintAt, MentionedTaskIds: c.MentionedTaskIds, Summary: c.Summary,
		SummaryRevision: c.SummaryRevision, SummaryQueryVersion: c.SummaryQueryVersion, IncrementalsSinceFull: c.IncrementalsSinceFull,
		LastUpdateKind: c.LastUpdateKind, LastGeneratedAt: c.LastGeneratedAt, LastModel: c.LastModel, GeneratingTaskID: c.GeneratingTaskID,
		GeneratingSince: c.GeneratingSince, FailureReason: c.FailureReason, NextEvalAt: c.NextEvalAt, ArchivedAt: c.ArchivedAt, Now: t.now})
	if err != nil {
		return store.StatusCard{}, mapErr(err, "save status card")
	}
	return row, nil
}

func interestHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

func cardTitleOrDefault(title string) string {
	if strings.TrimSpace(title) == "" {
		return "Card sem título"
	}
	return title
}

// ---------- validação ----------

func checkCardInterest(s string, persona bool) (string, error) {
	s = strings.TrimSpace(s)
	limit := domain.MaxCardInterestRunes
	if persona {
		limit = domain.MaxCardInterestPersonaRune
	}
	if n := utf8.RuneCountInString(s); n < 1 || n > limit {
		return "", domain.Invalid("interest", fmt.Sprintf("descreva o interesse em 1 a %d caracteres", limit))
	}
	return s, nil
}

func checkCardTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > domain.MaxCardTitleRunes {
		return "", domain.Invalid("title", fmt.Sprintf("até %d caracteres", domain.MaxCardTitleRunes))
	}
	return s, nil
}

func (t *txc) checkSummarizer(companyID domain.ID, id *domain.ID) error {
	if id == nil {
		return nil
	}
	if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *id}); err != nil {
		if isNoRows(err) {
			return domain.Invalid("summarizer", "persona não encontrada nesta empresa")
		}
		return fmt.Errorf("get summarizer: %w", err)
	}
	return nil
}

// cardWriteAllowed aplica a regra da persona: ela só mexe nos cards que criou (humanos e sistema mexem em todos).
func cardWriteAllowed(actor domain.Actor, c store.StatusCard) error {
	if actor.Type != domain.ActorPersona {
		return nil
	}
	if actor.ID == nil || c.CreatedByType != string(domain.ActorPersona) || c.CreatedByID == nil || *c.CreatedByID != *actor.ID {
		return fmt.Errorf("status card: uma persona só altera os cards que ela criou: %w", domain.ErrNotAllowed)
	}
	return nil
}

// ---------- leitura ----------

func (s *Service) cardView(ctx context.Context, q *store.Queries, r store.StatusCard, withMentions bool) (StatusCardView, error) {
	v := StatusCardView{StatusCard: cardFromRow(r)}
	v.ViewState = v.View()
	if r.SummarizerPersonaID != nil {
		p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: r.CompanyID, ID: *r.SummarizerPersonaID})
		if err == nil {
			v.SummarizerName, v.SummarizerSlug = p.Name, p.Slug
		} else if !isNoRows(err) {
			return StatusCardView{}, fmt.Errorf("get summarizer: %w", err)
		}
	}
	if withMentions && len(r.MentionedTaskIds) > 0 {
		refs, err := cardTaskRefs(ctx, q, r.CompanyID, r.MentionedTaskIds)
		if err != nil {
			return StatusCardView{}, err
		}
		v.Mentioned = refs
	}
	return v, nil
}

func cardTaskRefs(ctx context.Context, q *store.Queries, companyID domain.ID, ids []domain.ID) ([]CardTaskRef, error) {
	rows, err := q.StatusCardTasksByID(ctx, store.StatusCardTasksByIDParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("card tasks: %w", err)
	}
	out := make([]CardTaskRef, len(rows))
	for i, t := range rows {
		out[i] = CardTaskRef{ID: t.ID, Key: domain.TaskKey(t.TaskPrefix, t.Number), Title: t.Title, Status: domain.TaskStatus(t.Status),
			Priority: domain.Priority(t.Priority), PersonaName: t.PersonaName}
	}
	return out, nil
}

// ListStatusCards lista os cards da empresa (archived nil = todos; true = só arquivados; false = só ativos).
func (s *Service) ListStatusCards(ctx context.Context, companyID domain.ID, archived *bool) ([]StatusCardView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	return s.listCards(ctx, companyID, archived, nil)
}

func (s *Service) listCards(ctx context.Context, companyID domain.ID, archived *bool, byPersona *domain.ID) ([]StatusCardView, error) {
	p := store.ListStatusCardsParams{CompanyID: companyID, CreatedByPersona: byPersona, Lim: domain.MaxCardsPerCompany * 2}
	if archived != nil {
		p.Archived = pgtype.Bool{Bool: *archived, Valid: true}
	}
	rows, err := s.read().ListStatusCards(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list status cards: %w", err)
	}
	out := make([]StatusCardView, len(rows))
	for i, r := range rows {
		out[i] = StatusCardView{StatusCard: cardFromRow(r.StatusCard), SummarizerName: r.SummarizerName, SummarizerSlug: r.SummarizerSlug}
		out[i].ViewState = out[i].View()
	}
	return out, nil
}

// GetStatusCard devolve um card com as tasks citadas no resumo.
func (s *Service) GetStatusCard(ctx context.Context, companyID, cardID domain.ID) (StatusCardView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return StatusCardView{}, err
	}
	q := s.read()
	r, err := q.GetStatusCard(ctx, store.GetStatusCardParams{CompanyID: companyID, ID: cardID})
	if err != nil {
		return StatusCardView{}, mapErr(err, "status card")
	}
	return s.cardView(ctx, q, r, true)
}

// ListStatusCardUpdates devolve as rodadas do card, mais recentes primeiro.
func (s *Service) ListStatusCardUpdates(ctx context.Context, companyID, cardID domain.ID, limit int) ([]StatusCardUpdateView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetStatusCard(ctx, store.GetStatusCardParams{CompanyID: companyID, ID: cardID}); err != nil {
		return nil, mapErr(err, "status card")
	}
	rows, err := q.ListStatusCardUpdates(ctx, store.ListStatusCardUpdatesParams{CompanyID: companyID, CardID: cardID,
		Lim: i32(clamp(limit, 50, domain.MaxCardUpdates))})
	if err != nil {
		return nil, fmt.Errorf("list status card updates: %w", err)
	}
	out := make([]StatusCardUpdateView, len(rows))
	for i, r := range rows {
		out[i] = StatusCardUpdateView{StatusCardUpdate: cardUpdateFromRow(r.StatusCardUpdate), TaskKey: r.TaskKey, RunName: r.RunName}
	}
	return out, nil
}

// ListStatusCardRevisions devolve o histórico do resumo, da revisão mais nova para a mais antiga.
func (s *Service) ListStatusCardRevisions(ctx context.Context, companyID, cardID domain.ID, limit int) ([]domain.StatusCardRevision, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetStatusCard(ctx, store.GetStatusCardParams{CompanyID: companyID, ID: cardID}); err != nil {
		return nil, mapErr(err, "status card")
	}
	rows, err := q.ListStatusCardRevisions(ctx, store.ListStatusCardRevisionsParams{CompanyID: companyID, CardID: cardID,
		Lim: i32(clamp(limit, domain.MaxCardRevisions, domain.MaxCardRevisions))})
	if err != nil {
		return nil, fmt.Errorf("list status card revisions: %w", err)
	}
	out := make([]domain.StatusCardRevision, len(rows))
	for i, r := range rows {
		out[i] = cardRevisionFromRow(r)
	}
	return out, nil
}

// PreviewStatusCard mostra quais tasks casam hoje com os filtros (depuração).
func (s *Service) PreviewStatusCard(ctx context.Context, companyID, cardID domain.ID) (StatusCardPreview, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return StatusCardPreview{}, err
	}
	q := s.read()
	r, err := q.GetStatusCard(ctx, store.GetStatusCardParams{CompanyID: companyID, ID: cardID})
	if err != nil {
		return StatusCardPreview{}, mapErr(err, "status card")
	}
	c := cardFromRow(r)
	m, err := matchCard(ctx, q, companyID, c.Queries)
	if err != nil {
		return StatusCardPreview{}, err
	}
	names, err := loadCardNames(ctx, q, companyID)
	if err != nil {
		return StatusCardPreview{}, err
	}
	out := StatusCardPreview{QueryVersion: c.QueryVersion, Tasks: m.tasks, Truncated: m.truncated, Queries: make([]CardQueryPreview, len(c.Queries))}
	for i, cq := range c.Queries {
		out.Queries[i] = CardQueryPreview{Query: cq, Description: names.describe(cq), Matches: m.perQuery[i]}
	}
	return out, nil
}

// ---------- criação e edição ----------

// CreateStatusCard cria um card e pede a montagem dos filtros à resumidora.
func (s *Service) CreateStatusCard(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateStatusCardInput) (StatusCardView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return StatusCardView{}, err
	}
	isPersona := actor.Type == domain.ActorPersona
	interest, err := checkCardInterest(in.Interest, isPersona)
	if err != nil {
		return StatusCardView{}, err
	}
	title, err := checkCardTitle(in.Title)
	if err != nil {
		return StatusCardView{}, err
	}
	pol := domain.DefaultCardPolicy()
	if in.Policy != nil {
		pol = *in.Policy
	}
	if err := normalizeCardPolicy(&pol); err != nil {
		return StatusCardView{}, err
	}
	engine := in.Engine
	if engine == "" {
		engine = domain.CardEnginePersona
	}
	if !engine.Valid() {
		return StatusCardView{}, domain.Invalid("engine", "use persona ou decision")
	}
	if engine == domain.CardEngineDecision && isPersona {
		return StatusCardView{}, domain.Invalid("engine", "só uma pessoa escolhe o decision model como motor de um card")
	}
	if engine == domain.CardEnginePersona && len(in.Queries) > 0 {
		return StatusCardView{}, domain.Invalid("queries", "no motor persona a resumidora grava os filtros (montagem); informe o interesse")
	}
	pinned := in.TitlePinned && title != ""
	var out StatusCardView
	var job *cardDecisionJob
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		if err := t.q.LockStatusCardsOfCompany(ctx, companyID); err != nil {
			return fmt.Errorf("lock status cards: %w", err)
		}
		n, err := t.q.CountStatusCards(ctx, companyID)
		if err != nil {
			return fmt.Errorf("count status cards: %w", err)
		}
		if n >= domain.MaxCardsPerCompany {
			return fmt.Errorf("a empresa já tem %d cards de status (o limite); apague um antes de criar outro: %w", n, domain.ErrConflict)
		}
		summarizer := in.SummarizerPersonaID
		if isPersona {
			pn, err := t.q.CountStatusCardsByPersona(ctx, store.CountStatusCardsByPersonaParams{CompanyID: companyID, PersonaID: *actor.ID})
			if err != nil {
				return fmt.Errorf("count persona status cards: %w", err)
			}
			if pn >= domain.MaxCardsPerPersona {
				return fmt.Errorf("uma persona cria no máximo %d cards de status: %w", domain.MaxCardsPerPersona, domain.ErrConflict)
			}
			if summarizer == nil {
				summarizer = actor.ID
			}
		}
		if engine == domain.CardEngineDecision {
			summarizer = nil // o decision model não precisa de resumidora
		} else if err := t.checkSummarizer(companyID, summarizer); err != nil {
			return err
		}
		state := domain.CardCompiling
		if engine == domain.CardEngineDecision {
			state = domain.CardActive
		}
		row, err := t.q.InsertStatusCard(ctx, store.InsertStatusCardParams{ID: domain.NewID(), CompanyID: companyID, Title: title, TitlePinned: pinned,
			Interest: interest, SummarizerPersonaID: summarizer, RefreshPolicy: mustJSON(pol), State: string(state),
			CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create status card")
		}
		if err := t.q.SetStatusCardDelivery(ctx, store.SetStatusCardDeliveryParams{CompanyID: companyID, ID: row.ID, Engine: string(engine),
			OnDashboard: in.OnDashboard, ArtifactName: "", Now: t.now}); err != nil {
			return mapErr(err, "set status card delivery")
		}
		row.Engine, row.OnDashboard = string(engine), in.OnDashboard
		if err := t.activity(actor, companyID, "status_card.created", "status_card", row.ID, map[string]any{
			"title": title, "summarizer_persona_id": summarizer, "mode": pol.Mode, "engine": engine, "on_dashboard": in.OnDashboard}, ""); err != nil {
			return err
		}
		if engine == domain.CardEngineDecision {
			qs, err := t.decisionCardQueries(companyID, in.Queries)
			if err != nil {
				return err
			}
			row.Queries, row.QueryVersion, row.QueryCompiledAt = mustJSON(qs), 1, &t.now
			row.NextEvalAt = &t.now // a primeira atualização sai no próximo tick (ou em "Atualizar agora")
			if row, err = t.saveCard(row); err != nil {
				return err
			}
		} else if row, err = t.startCardCompile(row, domain.CardTriggerManual); err != nil {
			return err
		}
		job = t.cardJob
		out, err = s.cardView(ctx, t.q, row, false)
		return err
	})
	if err == nil && job != nil {
		s.logCardJob(ctx, job)
	}
	return out, err
}

// decisionCardQueries resolve os filtros de um card do motor decision; vazio = as tasks abertas da empresa.
func (t *txc) decisionCardQueries(companyID domain.ID, in []CardQueryInput) ([]domain.StatusCardQuery, error) {
	if len(in) == 0 {
		return []domain.StatusCardQuery{{Label: "Tasks abertas", OpenOnly: true}}, nil
	}
	return t.resolveCardQueries(companyID, in)
}

// logCardJob executa a geração aberta por uma escrita que não devolve o erro dela (criar, editar, restaurar): a falha
// já ficou no card e na rodada, aqui só vai para o log.
func (s *Service) logCardJob(ctx context.Context, job *cardDecisionJob) {
	if err := s.runCardDecision(ctx, job); err != nil && s.Log != nil {
		s.Log.Warn("card de status: a atualização pelo decision model falhou", "card_id", job.cardID, "err", err.Error())
	}
}

// UpdateStatusCard altera o card. Mudar o interesse dispara a montagem de novo (o mesmo interesse não);
// arquivar e restaurar são de quem criou ou de admin; restaurar pede um resumo do zero.
func (s *Service) UpdateStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID, in UpdateStatusCardInput) (StatusCardView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return StatusCardView{}, err
	}
	var out StatusCardView
	var job *cardDecisionJob
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetStatusCardForUpdate(ctx, store.GetStatusCardForUpdateParams{CompanyID: companyID, ID: cardID})
		if err != nil {
			return mapErr(err, "status card")
		}
		if err := cardWriteAllowed(actor, cur); err != nil {
			return err
		}
		next := cur
		var changed []string
		recompile := false
		decisionCard := domain.CardEngine(cur.Engine) == domain.CardEngineDecision
		stale := false // card do decision model com interesse ou filtros novos: o resumo atual está desatualizado
		if in.Archived != nil && *in.Archived != (cur.ArchivedAt != nil) {
			if err := s.requireArchive(ctx, actor, cur); err != nil {
				return err
			}
		}
		if cur.ArchivedAt != nil && (in.Archived == nil || *in.Archived) &&
			(in.Interest != nil || in.Title != nil || in.TitlePinned != nil || in.SummarizerPersonaID != nil || in.Policy != nil ||
				in.Queries != nil || in.OnDashboard != nil) {
			return domain.Invalid("archived", "restaure o card antes de editá-lo")
		}
		if in.Interest != nil {
			v, err := checkCardInterest(*in.Interest, actor.Type == domain.ActorPersona)
			if err != nil {
				return err
			}
			if v != cur.Interest {
				next.Interest = v
				changed = append(changed, "interest")
				if decisionCard {
					stale = true
				} else {
					recompile = true
				}
			}
		}
		if in.Title != nil {
			v, err := checkCardTitle(*in.Title)
			if err != nil {
				return err
			}
			if v != cur.Title {
				next.Title = v
				changed = append(changed, "title")
			}
		}
		if in.TitlePinned != nil && *in.TitlePinned != cur.TitlePinned {
			next.TitlePinned = *in.TitlePinned
			changed = append(changed, "title_pinned")
		}
		if next.TitlePinned && next.Title == "" {
			next.TitlePinned = false
		}
		if in.SummarizerPersonaID != nil && decisionCard {
			return domain.Invalid("summarizer", "o card usa o decision model como motor: não há persona resumidora")
		}
		if in.Queries != nil {
			if !decisionCard {
				return domain.Invalid("queries", "no motor persona quem grava os filtros é a resumidora (Montar de novo)")
			}
			qs, err := t.decisionCardQueries(companyID, *in.Queries)
			if err != nil {
				return err
			}
			if raw := mustJSON(qs); string(raw) != string(cur.Queries) {
				next.Queries = raw
				changed = append(changed, "queries")
				stale = true
			}
		}
		if in.OnDashboard != nil && *in.OnDashboard != cur.OnDashboard {
			next.OnDashboard = *in.OnDashboard
			changed = append(changed, "on_dashboard")
		}
		if in.SummarizerPersonaID != nil && (cur.SummarizerPersonaID == nil || *cur.SummarizerPersonaID != *in.SummarizerPersonaID) {
			if err := t.checkSummarizer(companyID, in.SummarizerPersonaID); err != nil {
				return err
			}
			id := *in.SummarizerPersonaID
			next.SummarizerPersonaID = &id
			changed = append(changed, "summarizer")
			if cur.QueryVersion == 0 {
				recompile = true // ainda não tem filtros: a resumidora nova faz a montagem
			}
		}
		if in.Policy != nil {
			p := *in.Policy
			if err := normalizeCardPolicy(&p); err != nil {
				return err
			}
			raw := mustJSON(p)
			if string(raw) != string(cur.RefreshPolicy) {
				next.RefreshPolicy = raw
				changed = append(changed, "refresh_policy")
				if cur.State == string(domain.CardPausedBudget) || cur.State == string(domain.CardPausedHours) {
					next.State = string(domain.CardActive)
				}
				if next.ArchivedAt == nil && next.GeneratingTaskID == nil && next.GeneratingSince == nil && next.State != string(domain.CardError) && next.QueryVersion > 0 {
					next.NextEvalAt = &t.now // a política nova vale já no próximo tick
				}
			}
		}
		restore := false
		if in.Archived != nil && *in.Archived != (cur.ArchivedAt != nil) {
			if *in.Archived {
				next.ArchivedAt, next.NextEvalAt = &t.now, nil
				changed = append(changed, "archived")
			} else {
				next.ArchivedAt = nil
				changed = append(changed, "restored")
				restore = true
			}
		}
		if len(changed) == 0 {
			out, err = s.cardView(ctx, t.q, cur, false)
			return err
		}
		if next.ArchivedAt != nil && next.GeneratingTaskID != nil {
			// arquivar encerra a geração em andamento
			if next, err = t.abortCardGeneration(next, "card arquivado"); err != nil {
				return err
			}
		}
		if stale { // o interesse ou os filtros mudaram: o resumo atual vale menos que uma geração nova
			next.QueryVersion++
			if next.ArchivedAt == nil && next.GeneratingSince == nil && next.State != string(domain.CardError) {
				next.NextEvalAt = &t.now
			}
		}
		row, err := t.saveCard(next)
		if err != nil {
			return err
		}
		if next.OnDashboard != cur.OnDashboard {
			if err := t.q.SetStatusCardDelivery(t.ctx, store.SetStatusCardDeliveryParams{CompanyID: companyID, ID: row.ID, Engine: row.Engine,
				OnDashboard: next.OnDashboard, ArtifactName: row.ArtifactName, Now: t.now}); err != nil {
				return mapErr(err, "set status card delivery")
			}
			row.OnDashboard = next.OnDashboard
		}
		verb := "status_card.updated"
		switch {
		case in.Archived != nil && *in.Archived && len(changed) == 1:
			verb = "status_card.archived"
		case restore:
			verb = "status_card.restored"
		}
		if err := t.activity(actor, companyID, verb, "status_card", row.ID, map[string]any{"fields": changed}, ""); err != nil {
			return err
		}
		switch {
		case recompile && row.ArchivedAt == nil:
			if row, err = t.startCardCompile(row, domain.CardTriggerManual); err != nil {
				return err
			}
		case restore:
			if row.QueryVersion == 0 {
				if row, err = t.startCardCompile(row, domain.CardTriggerRestore); err != nil {
					return err
				}
			} else if row, err = t.evaluateCard(row, evalRestore); err != nil {
				return err
			}
		}
		job = t.cardJob
		out, err = s.cardView(ctx, t.q, row, false)
		return err
	})
	if err == nil && job != nil {
		s.logCardJob(ctx, job)
	}
	return out, err
}

// requireArchive: arquivar ou restaurar é de quem criou o card (humano member, ou a persona autora) ou de admin.
func (s *Service) requireArchive(ctx context.Context, actor domain.Actor, c store.StatusCard) error {
	own := actor.ID != nil && c.CreatedByID != nil && *actor.ID == *c.CreatedByID && c.CreatedByType == string(actor.Type)
	if own {
		return nil
	}
	return s.require(ctx, c.CompanyID, domain.MemberAdmin)
}

// DeleteStatusCard apaga o card com o histórico (admin). Uma geração em andamento é encerrada.
func (s *Service) DeleteStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetStatusCardForUpdate(ctx, store.GetStatusCardForUpdateParams{CompanyID: companyID, ID: cardID})
		if err != nil {
			return mapErr(err, "status card")
		}
		if err := cardWriteAllowed(actor, cur); err != nil {
			return err
		}
		if cur.GeneratingTaskID != nil {
			if _, err := t.abortCardGeneration(cur, "card apagado"); err != nil {
				return err
			}
		}
		if _, err := t.q.DeleteStatusCard(ctx, store.DeleteStatusCardParams{CompanyID: companyID, ID: cardID}); err != nil {
			return mapErr(err, "delete status card")
		}
		return t.activity(actor, companyID, "status_card.deleted", "status_card", cardID, map[string]any{"title": cur.Title}, "")
	})
}

// RecompileStatusCard pede a montagem dos filtros de novo (a montagem anterior falhou ou o time quer refazê-la).
func (s *Service) RecompileStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID) (StatusCardView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return StatusCardView{}, err
	}
	var out StatusCardView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetStatusCardForUpdate(ctx, store.GetStatusCardForUpdateParams{CompanyID: companyID, ID: cardID})
		if err != nil {
			return mapErr(err, "status card")
		}
		if err := cardWriteAllowed(actor, cur); err != nil {
			return err
		}
		if cur.ArchivedAt != nil {
			return domain.Invalid("archived", "restaure o card antes de montá-lo de novo")
		}
		if domain.CardEngine(cur.Engine) == domain.CardEngineDecision {
			return domain.Invalid("engine", "o card usa o decision model como motor: não há montagem; use Atualizar agora")
		}
		if cur.SummarizerPersonaID == nil {
			return domain.Invalid("summarizer", "escolha a persona resumidora antes de montar o card")
		}
		cur.InterestHash = "" // força a montagem mesmo com o mesmo interesse
		row, err := t.startCardCompile(cur, domain.CardTriggerManual)
		if err != nil {
			return err
		}
		if err := t.activity(actor, companyID, "status_card.recompile_requested", "status_card", row.ID, nil, ""); err != nil {
			return err
		}
		out, err = s.cardView(ctx, t.q, row, false)
		return err
	})
	return out, err
}

// RefreshStatusCard é o "Atualizar agora": gera o resumo sem esperar a política (full = refazer do zero).
// Respeita o teto diário de tokens; não respeita o horário ativo nem o teto por hora (o pedido é explícito).
func (s *Service) RefreshStatusCard(ctx context.Context, actor domain.Actor, companyID, cardID domain.ID, full bool) (StatusCardView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return StatusCardView{}, err
	}
	var out StatusCardView
	var refused error
	var job *cardDecisionJob
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetStatusCardForUpdate(ctx, store.GetStatusCardForUpdateParams{CompanyID: companyID, ID: cardID})
		if err != nil {
			return mapErr(err, "status card")
		}
		if err := cardWriteAllowed(actor, cur); err != nil {
			return err
		}
		switch {
		case cur.ArchivedAt != nil:
			return domain.Invalid("archived", "restaure o card antes de atualizá-lo")
		case cur.GeneratingTaskID != nil:
			return fmt.Errorf("o card já está sendo atualizado: %w", domain.ErrConflict)
		case cur.QueryVersion == 0:
			return domain.Invalid("queries", "o card ainda não tem filtros: rode a montagem de novo")
		case cur.SummarizerPersonaID == nil && domain.CardEngine(cur.Engine) != domain.CardEngineDecision:
			return domain.Invalid("summarizer", "escolha a persona resumidora antes de atualizar o card")
		}
		reason := evalManual
		if full {
			reason = evalManualFull
		}
		row, err := t.evaluateCard(cur, reason)
		if err != nil {
			return err
		}
		job = t.cardJob
		if row.GeneratingTaskID == nil && job == nil && row.State == string(domain.CardPausedBudget) {
			refused = domain.Invalid("daily_token_cap", "o teto diário de tokens deste card foi atingido; aumente o teto ou espere o dia seguinte")
		}
		if err := t.activity(actor, companyID, "status_card.refresh_requested", "status_card", row.ID, map[string]any{"full": full}, ""); err != nil {
			return err
		}
		out, err = s.cardView(ctx, t.q, row, false)
		return err
	})
	if err == nil && refused != nil {
		return out, refused
	}
	if err == nil && job != nil {
		// o decision model responde na hora: a atualização manual devolve o card já com o resumo (ou o erro dela)
		if jerr := s.runCardDecision(ctx, job); jerr != nil {
			return out, jerr
		}
		return s.GetStatusCard(ctx, companyID, cardID)
	}
	return out, err
}

// ListPersonaStatusCards é o hanko_status_card_list: os cards que a persona criou e o card da task de geração
// da run (se houver).
func (s *Service) ListPersonaStatusCards(ctx context.Context, companyID, personaID domain.ID, taskID *domain.ID) ([]StatusCardView, error) {
	no := false
	out, err := s.listCards(ctx, companyID, &no, &personaID)
	if err != nil {
		return nil, err
	}
	if taskID == nil {
		return out, nil
	}
	q := s.read()
	u, err := q.GetStatusCardUpdateByTask(ctx, *taskID)
	if isNoRows(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get status card update: %w", err)
	}
	for _, c := range out {
		if c.ID == u.CardID {
			return out, nil
		}
	}
	r, err := q.GetStatusCard(ctx, store.GetStatusCardParams{CompanyID: companyID, ID: u.CardID})
	if isNoRows(err) {
		return out, nil
	}
	if err != nil {
		return nil, mapErr(err, "status card")
	}
	v, err := s.cardView(ctx, q, r, false)
	if err != nil {
		return nil, err
	}
	return append([]StatusCardView{v}, out...), nil
}

// ---------- nomes para descrever filtros ----------

type cardNames struct {
	personas, projects, goals map[domain.ID]string
}

func loadCardNames(ctx context.Context, q *store.Queries, companyID domain.ID) (cardNames, error) {
	n := cardNames{personas: map[domain.ID]string{}, projects: map[domain.ID]string{}, goals: map[domain.ID]string{}}
	ps, err := q.ListPersonas(ctx, companyID)
	if err != nil {
		return n, fmt.Errorf("list personas: %w", err)
	}
	for _, p := range ps {
		n.personas[p.ID] = p.Name + " (" + p.Slug + ")"
	}
	prs, err := q.ListProjects(ctx, companyID)
	if err != nil {
		return n, fmt.Errorf("list projects: %w", err)
	}
	for _, p := range prs {
		n.projects[p.ID] = p.Name + " (" + p.Slug + ")"
	}
	gs, err := q.ListGoals(ctx, store.ListGoalsParams{CompanyID: companyID})
	if err != nil {
		return n, fmt.Errorf("list goals: %w", err)
	}
	for _, g := range gs {
		n.goals[g.ID] = g.Title + " (" + g.Slug + ")"
	}
	return n, nil
}

func nameOr(m map[domain.ID]string, id domain.ID) string {
	if v, ok := m[id]; ok {
		return v
	}
	return id.String() + " (removido)"
}

// describe é o filtro em uma linha legível (pt-BR; vai para o prompt e para a tela de depuração).
func (n cardNames) describe(q domain.StatusCardQuery) string {
	var parts []string
	if len(q.Statuses) > 0 {
		ss := make([]string, len(q.Statuses))
		for i, s := range q.Statuses {
			ss[i] = string(s)
		}
		parts = append(parts, "status: "+strings.Join(ss, ", "))
	}
	if len(q.Priorities) > 0 {
		ps := make([]string, len(q.Priorities))
		for i, p := range q.Priorities {
			ps[i] = string(p)
		}
		parts = append(parts, "prioridade: "+strings.Join(ps, ", "))
	}
	if q.PersonaID != nil {
		parts = append(parts, "persona: "+nameOr(n.personas, *q.PersonaID))
	}
	if q.ProjectID != nil {
		parts = append(parts, "projeto: "+nameOr(n.projects, *q.ProjectID))
	}
	if q.GoalID != nil {
		parts = append(parts, "meta: "+nameOr(n.goals, *q.GoalID))
	}
	if q.Text != "" {
		parts = append(parts, fmt.Sprintf("texto: %q", q.Text))
	}
	if q.OpenOnly {
		parts = append(parts, "só abertas")
	}
	d := strings.Join(parts, " · ")
	if q.Label != "" {
		d = q.Label + " — " + d
	}
	return d
}

// resolveCardQueries converte os filtros da resumidora (referências por slug ou id) no formato gravado.
func (t *txc) resolveCardQueries(companyID domain.ID, in []CardQueryInput) ([]domain.StatusCardQuery, error) {
	if len(in) == 0 || len(in) > domain.MaxCardQueries {
		return nil, domain.Invalid("queries", fmt.Sprintf("de 1 a %d filtros", domain.MaxCardQueries))
	}
	out := make([]domain.StatusCardQuery, len(in))
	for i, qi := range in {
		field := fmt.Sprintf("queries[%d]", i)
		q := domain.StatusCardQuery{Label: qi.Label, Text: qi.Text, OpenOnly: qi.OpenOnly}
		for _, s := range qi.Statuses {
			q.Statuses = append(q.Statuses, domain.TaskStatus(strings.TrimSpace(s)))
		}
		for _, p := range qi.Priorities {
			q.Priorities = append(q.Priorities, domain.Priority(strings.TrimSpace(p)))
		}
		if ref := strings.TrimSpace(qi.Persona); ref != "" {
			p, err := t.personaByRef(companyID, ref)
			if err != nil {
				return nil, domain.Invalid(field+".persona", fmt.Sprintf("persona %q não encontrada", ref))
			}
			q.PersonaID = &p
		}
		if ref := strings.TrimSpace(qi.Project); ref != "" {
			id, err := t.projectByRef(companyID, ref)
			if err != nil {
				return nil, domain.Invalid(field+".project", fmt.Sprintf("projeto %q não encontrado", ref))
			}
			q.ProjectID = &id
		}
		if ref := strings.TrimSpace(qi.Goal); ref != "" {
			id, err := t.goalByRef(companyID, ref)
			if err != nil {
				return nil, domain.Invalid(field+".goal", fmt.Sprintf("meta %q não encontrada", ref))
			}
			q.GoalID = &id
		}
		out[i] = q
	}
	if err := domain.ValidateCardQueries(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (t *txc) personaByRef(companyID domain.ID, ref string) (domain.ID, error) {
	if id, err := uuid.Parse(ref); err == nil {
		p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: id})
		return p.ID, err
	}
	p, err := t.q.GetPersonaBySlug(t.ctx, store.GetPersonaBySlugParams{CompanyID: companyID, Slug: ref})
	return p.ID, err
}

func (t *txc) projectByRef(companyID domain.ID, ref string) (domain.ID, error) {
	if id, err := uuid.Parse(ref); err == nil {
		p, err := t.q.GetProjectByID(t.ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: id})
		return p.ID, err
	}
	p, err := t.q.GetProjectBySlug(t.ctx, store.GetProjectBySlugParams{CompanyID: companyID, Slug: ref})
	return p.ID, err
}

func (t *txc) goalByRef(companyID domain.ID, ref string) (domain.ID, error) {
	if id, err := uuid.Parse(ref); err == nil {
		g, err := t.q.GetGoalByID(t.ctx, store.GetGoalByIDParams{CompanyID: companyID, ID: id})
		return g.ID, err
	}
	g, err := t.q.GetGoalBySlug(t.ctx, store.GetGoalBySlugParams{CompanyID: companyID, Slug: ref})
	return g.ID, err
}
