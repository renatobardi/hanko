package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Cases (ADR-0034). Ler é de qualquer membro; criar, editar, mover e ligar é de member; a persona (run token)
// cria e atualiza cases, liga a própria task, sugere transições e move entre etapas working sem aprovação. Toda
// escrita confere a versão esperada (trava otimista), sobe a versão em 1 e grava um evento no histórico do case.

const (
	maxCaseTitleRunes   = 300
	maxCaseSummaryBytes = 64 << 10
	maxCaseFieldsBytes  = 64 << 10
	maxCaseFields       = 100
	maxCaseKeyRunes     = 200
	maxCaseTypeRunes    = 60
	maxCaseBlockers     = 50
	maxCaseList         = 500
	maxCaseEvents       = 500
	maxCaseDepth        = 64
	maxReasonRunes      = 4000
)

// CaseFilter filtra a lista de cases.
type CaseFilter struct {
	Type       string
	Statuses   []domain.CaseStatus
	PipelineID *domain.ID
	StageID    *domain.ID
	ProjectID  *domain.ID
	ParentID   *domain.ID
	RootsOnly  bool
	LooseOnly  bool
	Q          string
	Limit      int
	Offset     int
}

// CreateCaseInput cria um case solto (Status) ou num pipeline (PipelineID e, opcionalmente, StageKey; o padrão é a
// primeira etapa ativa). Com Key, o par (Type, Key) é único na empresa: se já existe, o case é atualizado (upsert:
// título, resumo e campos) e Created volta false.
type CreateCaseInput struct {
	Type, Key, Title, Summary string
	Fields                    map[string]any
	Status                    domain.CaseStatus
	PipelineID                *domain.ID
	StageKey                  string
	ParentID                  *domain.ID
	ProjectID                 *domain.ID
	// RunID é a execução da persona que escreve (obrigatória para ator persona); TaskID é a task dela, ligada ao
	// case (origin na criação, work depois).
	RunID  *domain.ID
	TaskID *domain.ID
}

// UpdateCaseInput altera um case. ExpectedVersion é obrigatória. Fields faz merge (valor nil apaga a chave);
// ReplaceFields troca o mapa inteiro.
type UpdateCaseInput struct {
	ExpectedVersion int
	Title, Summary  *string
	Fields          map[string]any
	ReplaceFields   bool
	// Status só vale para case solto; num pipeline, mova de etapa.
	Status         *domain.CaseStatus
	ProjectID      *domain.ID
	ClearProject   bool
	ParentID       *domain.ID
	ClearParent    bool
	ClearAttention bool
	RunID, TaskID  *domain.ID
}

// CaseLink é uma task ligada a um case.
type CaseLink struct {
	TaskID      domain.ID           `json:"task_id"`
	TaskKey     string              `json:"task_key"`
	TaskTitle   string              `json:"task_title"`
	TaskStatus  domain.TaskStatus   `json:"task_status"`
	PersonaName string              `json:"persona_name,omitempty"`
	Role        domain.CaseLinkRole `json:"role"`
	RunID       *domain.ID          `json:"created_by_run_id,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
}

// CaseRef é um case resumido (bloqueadores, filhos, ligações).
type CaseRef struct {
	ID         domain.ID         `json:"id"`
	Identifier string            `json:"identifier"`
	Title      string            `json:"title"`
	Status     domain.CaseStatus `json:"status,omitempty"`
	Resolved   bool              `json:"resolved,omitempty"`
}

// CaseView é o case com os nomes das ligações e, no detalhe, tasks, bloqueios e o histórico.
type CaseView struct {
	domain.Case
	PipelineKey      string           `json:"pipeline_key,omitempty"`
	PipelineName     string           `json:"pipeline_name,omitempty"`
	StageKey         string           `json:"stage_key,omitempty"`
	StageName        string           `json:"stage_name,omitempty"`
	StageKind        domain.StageKind `json:"stage_kind,omitempty"`
	ProjectSlug      string           `json:"project_slug,omitempty"`
	ProjectName      string           `json:"project_name,omitempty"`
	ParentIdentifier string           `json:"parent_identifier,omitempty"`
	ParentTitle      string           `json:"parent_title,omitempty"`
	// Detalhe (GetCaseView).
	Links    []CaseLink         `json:"links,omitempty"`
	Blockers []CaseRef          `json:"blockers,omitempty"`
	Blocking []CaseRef          `json:"blocking,omitempty"`
	Events   []domain.CaseEvent `json:"events,omitempty"`
	// OpenBlockers conta os bloqueadores ainda não resolvidos (no detalhe).
	OpenBlockers int `json:"open_blockers"`
}

// ---------- conversões ----------

func caseFromRow(r store.Case, prefix string) domain.Case {
	c := domain.Case{ID: r.ID, CompanyID: r.CompanyID, Number: r.Number, Identifier: domain.CaseIdentifier(prefix, r.Number), Type: r.CaseType,
		Title: r.Title, Summary: r.Summary, Fields: map[string]any{}, Status: domain.CaseStatus(r.Status), PipelineID: r.PipelineID,
		StageID: r.StageID, ParentCaseID: r.ParentCaseID, ProjectID: r.ProjectID, Version: int(r.Version),
		ReapprovalRequired: r.ReapprovalRequired, AttentionAt: r.AttentionAt, TerminalAt: r.TerminalAt, ChildCount: int(r.ChildCount),
		TerminalChildCount: int(r.TerminalChildCount), OriginRunID: r.OriginRunID, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CompletedAt: r.CompletedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.Key != nil {
		c.Key = *r.Key
	}
	if len(r.Fields) > 0 {
		_ = json.Unmarshal(r.Fields, &c.Fields)
	}
	if len(r.PendingSuggestion) > 0 {
		var sg domain.CaseSuggestion
		if json.Unmarshal(r.PendingSuggestion, &sg) == nil {
			c.PendingSuggestion = &sg
		}
	}
	if r.ApprovedVersion != nil {
		v := int(*r.ApprovedVersion)
		c.ApprovedVersion = &v
	}
	if r.Attention != nil {
		c.Attention = *r.Attention
	}
	if r.TerminalKind != nil {
		c.TerminalKind = *r.TerminalKind
	}
	return c
}

type caseJoin struct {
	prefix, pipelineKey, pipelineName, stageKey, stageName, stageKind, projectSlug, projectName, parentTitle string
	parentNumber                                                                                             int64
}

func caseViewOf(r store.Case, j caseJoin) CaseView {
	v := CaseView{Case: caseFromRow(r, j.prefix), PipelineKey: j.pipelineKey, PipelineName: j.pipelineName, StageKey: j.stageKey,
		StageName: j.stageName, StageKind: domain.StageKind(j.stageKind), ProjectSlug: j.projectSlug, ProjectName: j.projectName,
		ParentTitle: j.parentTitle}
	if j.parentNumber > 0 {
		v.ParentIdentifier = domain.CaseIdentifier(j.prefix, j.parentNumber)
	}
	return v
}

func caseEventFromRow(r store.CaseEvent) domain.CaseEvent {
	e := domain.CaseEvent{ID: r.ID, CaseID: r.CaseID, Kind: domain.CaseEventKind(r.Kind), FromStageID: r.FromStageID, ToStageID: r.ToStageID,
		Actor: actorOf(r.ActorType, r.ActorID), RunID: r.RunID, CreatedAt: r.CreatedAt}
	if len(r.Payload) > 0 {
		_ = json.Unmarshal(r.Payload, &e.Payload)
	}
	return e
}

// ---------- validação ----------

func checkCaseText(title, summary string) error {
	if n := utf8.RuneCountInString(title); n < 1 || n > maxCaseTitleRunes {
		return domain.Invalid("title", fmt.Sprintf("must have 1-%d characters", maxCaseTitleRunes))
	}
	if len(summary) > maxCaseSummaryBytes {
		return domain.Invalid("summary", fmt.Sprintf("too long (max %d bytes)", maxCaseSummaryBytes))
	}
	return nil
}

// cleanFieldValue aceita texto, número, booleano e nulo (os campos são planos).
func cleanFieldValue(k string, v any) (any, error) {
	switch x := v.(type) {
	case nil, bool:
		return x, nil
	case string:
		return strings.TrimSpace(x), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, domain.Invalid("fields."+k, "must be a finite number")
		}
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return nil, domain.Invalid("fields."+k, "not a number")
		}
		return f, nil
	}
	return nil, domain.Invalid("fields."+k, "use text, number, true/false or null (no nested objects)")
}

// mergeFields aplica patch sobre base (valor nil apaga) ou troca tudo.
func mergeFields(base, patch map[string]any, replace bool) (map[string]any, bool, error) {
	out := map[string]any{}
	if !replace {
		for k, v := range base {
			out[k] = v
		}
	}
	changed := replace && len(base) != len(patch)
	for k, v := range patch {
		k = strings.TrimSpace(k)
		if k == "" || utf8.RuneCountInString(k) > 63 {
			return nil, false, domain.Invalid("fields", "field keys have 1-63 characters")
		}
		cv, err := cleanFieldValue(k, v)
		if err != nil {
			return nil, false, err
		}
		old, had := base[k]
		if cv == nil {
			if had {
				changed = true
			}
			delete(out, k)
			continue
		}
		if !had || fmt.Sprint(old) != fmt.Sprint(cv) {
			changed = true
		}
		out[k] = cv
	}
	if len(out) > maxCaseFields {
		return nil, false, domain.Invalid("fields", fmt.Sprintf("at most %d fields", maxCaseFields))
	}
	return out, changed, nil
}

func marshalFields(f map[string]any) ([]byte, error) {
	if f == nil {
		f = map[string]any{}
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("marshal fields: %w", err)
	}
	if len(b) > maxCaseFieldsBytes {
		return nil, domain.Invalid("fields", fmt.Sprintf("too large (max %d bytes)", maxCaseFieldsBytes))
	}
	return b, nil
}

// checkStageFields confere os valores dos campos declarados na etapa (tipo, opções, URL, data). requireAll exige
// os obrigatórios; addForm restringe a exigência aos que aparecem no formulário de criação.
func checkStageFields(st domain.Stage, fields map[string]any, requireAll, addForm bool) error {
	for _, f := range st.Config.Fields {
		v, ok := fields[f.Key]
		s := ""
		if ok && v != nil {
			s = strings.TrimSpace(fmt.Sprint(v))
		}
		if s == "" {
			if f.Required && (requireAll || (addForm && f.ShowInAddForm)) {
				return domain.Invalid("fields."+f.Key, fmt.Sprintf("%q is required in stage %s", f.Label, st.Name))
			}
			continue
		}
		switch f.Type {
		case domain.FieldNumber:
			if _, isNum := v.(float64); !isNum {
				if _, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64); err != nil {
					return domain.Invalid("fields."+f.Key, "must be a number")
				}
			}
		case domain.FieldDate:
			if _, err := time.Parse("2006-01-02", s); err != nil {
				return domain.Invalid("fields."+f.Key, "use AAAA-MM-DD")
			}
		case domain.FieldURL:
			u, err := url.Parse(s)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return domain.Invalid("fields."+f.Key, "must be an http(s) URL")
			}
		case domain.FieldSelect:
			found := false
			for _, o := range f.Options {
				if o == s {
					found = true
				}
			}
			if !found {
				return domain.Invalid("fields."+f.Key, "not one of the options")
			}
		}
	}
	return nil
}

func normalizeCaseType(t string) (string, error) {
	t = strings.ToLower(strings.TrimSpace(t))
	if utf8.RuneCountInString(t) > maxCaseTypeRunes {
		return "", domain.Invalid("case_type", fmt.Sprintf("too long (max %d)", maxCaseTypeRunes))
	}
	return t, nil
}

// personaWrite confere que a escrita de uma persona traz a execução (o histórico guarda de qual run veio).
func personaWrite(actor domain.Actor, runID *domain.ID) error {
	if actor.Type == domain.ActorPersona && runID == nil {
		return domain.Invalid("run_id", "a persona writes to a case from a run (use the run token)")
	}
	return nil
}

// ---------- leitura ----------

// resolveCase acha um case por UUID, identificador (<PREFIXO>-C<n>), C<n> ou o número.
func resolveCase(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (store.Case, error) {
	ref = strings.TrimSpace(ref)
	if id, err := uuid.Parse(ref); err == nil {
		r, err := q.GetCaseByID(ctx, store.GetCaseByIDParams{CompanyID: companyID, ID: id})
		return r, mapErr(err, "get case")
	}
	n := int64(0)
	if _, num, ok := domain.ParseCaseIdentifier(ref); ok {
		n = num
	} else if v, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(ref, "C"), "c"), 10, 64); err == nil && v > 0 {
		n = v
	}
	if n == 0 {
		return store.Case{}, notFound("case")
	}
	r, err := q.GetCaseByNumber(ctx, store.GetCaseByNumberParams{CompanyID: companyID, Number: n})
	return r, mapErr(err, "get case")
}

// ResolveCase devolve o id de um case pela referência (id, identificador ou número).
func (s *Service) ResolveCase(ctx context.Context, companyID domain.ID, ref string) (domain.ID, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.ID{}, err
	}
	r, err := resolveCase(ctx, s.read(), companyID, ref)
	return r.ID, err
}

func loadCaseView(ctx context.Context, q *store.Queries, companyID, id domain.ID) (CaseView, error) {
	r, err := q.GetCaseView(ctx, store.GetCaseViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return CaseView{}, mapErr(err, "get case")
	}
	return caseViewOf(r.Case, caseJoin{r.TaskPrefix, r.PipelineKey, r.PipelineName, r.StageKey, r.StageName, r.StageKind, r.ProjectSlug,
		r.ProjectName, r.ParentTitle, r.ParentNumber}), nil
}

// GetCaseView é o detalhe de um case: tasks ligadas, bloqueios e o histórico (os 200 eventos mais recentes, do
// mais antigo ao mais novo).
func (s *Service) GetCaseView(ctx context.Context, companyID domain.ID, ref string) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return CaseView{}, err
	}
	q := s.read()
	r, err := resolveCase(ctx, q, companyID, ref)
	if err != nil {
		return CaseView{}, err
	}
	v, err := loadCaseView(ctx, q, companyID, r.ID)
	if err != nil {
		return CaseView{}, err
	}
	links, err := q.ListCaseLinks(ctx, store.ListCaseLinksParams{CompanyID: companyID, CaseID: r.ID})
	if err != nil {
		return CaseView{}, fmt.Errorf("list case links: %w", err)
	}
	for _, l := range links {
		v.Links = append(v.Links, CaseLink{TaskID: l.TaskID, TaskKey: l.TaskKey, TaskTitle: l.TaskTitle, TaskStatus: domain.TaskStatus(l.TaskStatus),
			PersonaName: l.PersonaName, Role: domain.CaseLinkRole(l.Role), RunID: l.CreatedByRunID, CreatedAt: l.CreatedAt})
	}
	bl, err := q.ListCaseBlockers(ctx, store.ListCaseBlockersParams{CompanyID: companyID, CaseID: r.ID})
	if err != nil {
		return CaseView{}, fmt.Errorf("list blockers: %w", err)
	}
	for _, b := range bl {
		v.Blockers = append(v.Blockers, CaseRef{ID: b.BlockerCaseID, Identifier: domain.CaseIdentifier(b.TaskPrefix, b.Number), Title: b.Title,
			Status: domain.CaseStatus(b.Status), Resolved: b.ResolvedAt != nil})
		if b.ResolvedAt == nil {
			v.OpenBlockers++
		}
	}
	by, err := q.ListCasesBlockedBy(ctx, store.ListCasesBlockedByParams{CompanyID: companyID, BlockerCaseID: r.ID})
	if err != nil {
		return CaseView{}, fmt.Errorf("list blocked: %w", err)
	}
	for _, b := range by {
		v.Blocking = append(v.Blocking, CaseRef{ID: b.CaseID, Identifier: domain.CaseIdentifier(b.TaskPrefix, b.Number), Title: b.Title})
	}
	evs, err := q.ListCaseEvents(ctx, store.ListCaseEventsParams{CompanyID: companyID, CaseID: r.ID, Lim: 200})
	if err != nil {
		return CaseView{}, fmt.Errorf("list case events: %w", err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		v.Events = append(v.Events, caseEventFromRow(evs[i]))
	}
	return v, nil
}

// ListCases lista cases com filtros (mais recentes primeiro).
func (s *Service) ListCases(ctx context.Context, companyID domain.ID, f CaseFilter) ([]CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListCaseViewsParams{CompanyID: companyID, PipelineID: f.PipelineID, StageID: f.StageID, ProjectID: f.ProjectID, ParentID: f.ParentID,
		RootsOnly: f.RootsOnly, LooseOnly: f.LooseOnly, Lim: i32(clamp(f.Limit, 100, maxCaseList)), Off: i32(max(f.Offset, 0))}
	if t := strings.ToLower(strings.TrimSpace(f.Type)); t != "" {
		p.CaseType = &t
	}
	for _, st := range f.Statuses {
		if !st.Valid() {
			return nil, domain.Invalid("status", fmt.Sprintf("unknown status %q", st))
		}
		p.Statuses = append(p.Statuses, string(st))
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		if utf8.RuneCountInString(q) > 200 {
			return nil, domain.Invalid("q", "too long")
		}
		e := escapeLike(q)
		p.Q = &e
	}
	rows, err := s.read().ListCaseViews(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}
	out := make([]CaseView, len(rows))
	for i, r := range rows {
		out[i] = caseViewOf(r.Case, caseJoin{r.TaskPrefix, r.PipelineKey, r.PipelineName, r.StageKey, r.StageName, r.StageKind, r.ProjectSlug,
			r.ProjectName, r.ParentTitle, r.ParentNumber})
	}
	return out, nil
}

// ListCaseEvents devolve o histórico do case, do mais novo para o mais antigo.
func (s *Service) ListCaseEvents(ctx context.Context, companyID, caseID domain.ID, limit int) ([]domain.CaseEvent, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListCaseEvents(ctx, store.ListCaseEventsParams{CompanyID: companyID, CaseID: caseID, Lim: i32(clamp(limit, 100, maxCaseEvents))})
	if err != nil {
		return nil, fmt.Errorf("list case events: %w", err)
	}
	out := make([]domain.CaseEvent, len(rows))
	for i, r := range rows {
		out[i] = caseEventFromRow(r)
	}
	return out, nil
}

// CaseForTask é um case ligado a uma task (o painel "Cases" da task).
type CaseForTask struct {
	CaseRef
	Role         domain.CaseLinkRole `json:"role"`
	PipelineName string              `json:"pipeline_name,omitempty"`
	StageName    string              `json:"stage_name,omitempty"`
}

// CasesForTask lista os cases ligados a uma task.
func (s *Service) CasesForTask(ctx context.Context, companyID, taskID domain.ID) ([]CaseForTask, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListCasesForTask(ctx, store.ListCasesForTaskParams{CompanyID: companyID, TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("cases for task: %w", err)
	}
	out := make([]CaseForTask, len(rows))
	for i, r := range rows {
		out[i] = CaseForTask{CaseRef: CaseRef{ID: r.Case.ID, Identifier: domain.CaseIdentifier(r.TaskPrefix, r.Case.Number), Title: r.Case.Title,
			Status: domain.CaseStatus(r.Case.Status)}, Role: domain.CaseLinkRole(r.Role), PipelineName: r.PipelineName, StageName: r.StageName}
	}
	return out, nil
}

// ---------- escrita: helpers de transação ----------

// caseEvent grava um evento imutável do case.
func (t *txc) caseEvent(companyID, caseID domain.ID, kind domain.CaseEventKind, actor domain.Actor, runID, from, to *domain.ID, payload map[string]any) (store.CaseEvent, error) {
	raw := []byte(`{}`)
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return store.CaseEvent{}, fmt.Errorf("marshal case event: %w", err)
		}
		raw = b
	}
	if actor.Type != domain.ActorPersona && runID != nil && actor.Type == domain.ActorHuman {
		runID = nil // humano não escreve "de dentro" de uma run
	}
	ev, err := t.q.CreateCaseEvent(t.ctx, store.CreateCaseEventParams{ID: domain.NewID(), CompanyID: companyID, CaseID: caseID, Kind: string(kind),
		FromStageID: from, ToStageID: to, ActorType: string(actor.Type), ActorID: actor.ID, RunID: runID, Payload: raw, Now: t.now})
	if err != nil {
		return store.CaseEvent{}, fmt.Errorf("record case event %s: %w", kind, err)
	}
	return ev, nil
}

func (t *txc) lockCase(companyID, caseID domain.ID) (store.Case, error) {
	r, err := t.q.GetCaseByIDForUpdate(t.ctx, store.GetCaseByIDForUpdateParams{CompanyID: companyID, ID: caseID})
	if err != nil {
		return store.Case{}, mapErr(err, "get case")
	}
	return r, nil
}

func checkVersion(cur store.Case, expected int) error {
	if expected <= 0 {
		return domain.Invalid("expected_version", "required (the version you read)")
	}
	if int(cur.Version) != expected {
		return &domain.VersionConflictError{Current: int(cur.Version)}
	}
	return nil
}

// saveCase grava o estado do case com a versão seguinte.
func (t *txc) saveCase(c store.Case) (store.Case, error) {
	row, err := t.q.UpdateCase(t.ctx, store.UpdateCaseParams{CompanyID: c.CompanyID, ID: c.ID, Title: c.Title, Summary: c.Summary, Fields: c.Fields,
		Status: c.Status, PipelineID: c.PipelineID, StageID: c.StageID, ParentCaseID: c.ParentCaseID, ProjectID: c.ProjectID, Version: c.Version + 1,
		PendingSuggestion: c.PendingSuggestion, ApprovedVersion: c.ApprovedVersion, ReapprovalRequired: c.ReapprovalRequired,
		Attention: c.Attention, AttentionAt: c.AttentionAt, TerminalKind: c.TerminalKind, TerminalAt: c.TerminalAt, CompletedAt: c.CompletedAt, Now: t.now})
	if err != nil {
		return store.Case{}, mapErr(err, "update case")
	}
	return row, nil
}

func (t *txc) companyPrefix(companyID domain.ID) (string, error) {
	co, err := t.q.GetCompanyByID(t.ctx, companyID)
	if err != nil {
		return "", mapErr(err, "company")
	}
	return co.TaskPrefix, nil
}

// linkPersonaTask liga a task da run da persona ao case (uma vez; o papel é o da primeira ligação).
func (t *txc) linkPersonaTask(companyID, caseID domain.ID, actor domain.Actor, taskID, runID *domain.ID, role domain.CaseLinkRole) error {
	if actor.Type != domain.ActorPersona || taskID == nil {
		return nil
	}
	n, err := t.q.LinkCaseTask(t.ctx, store.LinkCaseTaskParams{CaseID: caseID, TaskID: *taskID, CompanyID: companyID, Role: string(role),
		CreatedByRunID: runID, Now: t.now})
	if err != nil {
		return mapErr(err, "link task")
	}
	if n == 0 {
		return nil
	}
	_, err = t.caseEvent(companyID, caseID, domain.CaseEvTaskLinked, actor, runID, nil, nil, map[string]any{"task_id": taskID.String(), "role": role})
	return err
}

// adjustParent atualiza os contadores do pai quando um filho entra (dChildren) ou vira terminal (dTerminal). Se
// todos os filhos terminaram, o pai vai para a fila de revisão como "atenção".
func (t *txc) adjustParent(companyID domain.ID, parentID *domain.ID, dChildren, dTerminal int, actor domain.Actor, runID *domain.ID) error {
	if parentID == nil || (dChildren == 0 && dTerminal == 0) {
		return nil
	}
	p, err := t.q.AdjustCaseChildCounts(t.ctx, store.AdjustCaseChildCountsParams{CompanyID: companyID, ID: *parentID, DChildren: i32(dChildren), DTerminal: i32(dTerminal)})
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("adjust parent counts: %w", err)
	}
	if dTerminal > 0 && p.ChildCount > 0 && p.ChildCount == p.TerminalChildCount && p.TerminalKind == nil {
		if err := t.q.SetCaseAttention(t.ctx, store.SetCaseAttentionParams{CompanyID: companyID, ID: p.ID, Attention: ptrTo("children_terminal"), Now: &t.now}); err != nil {
			return fmt.Errorf("set attention: %w", err)
		}
		if _, err := t.caseEvent(companyID, p.ID, domain.CaseEvChildrenTerminal, domain.SystemActor, nil, nil, nil,
			map[string]any{"children": p.ChildCount}); err != nil {
			return err
		}
	}
	return nil
}

func ptrTo[T any](v T) *T { return &v }

// applyTerminal ajusta terminal_kind/at e completed_at conforme o novo status e devolve a variação de terminal
// (+1 virou terminal, -1 reabriu, 0 nada).
func applyTerminal(c *store.Case, st domain.CaseStatus, now time.Time) int {
	was := c.TerminalKind != nil
	if st.Terminal() {
		k := string(st)
		if !was || *c.TerminalKind != k {
			c.TerminalKind, c.TerminalAt = &k, &now
		}
		if st == domain.CaseDone {
			c.CompletedAt = &now
		} else {
			c.CompletedAt = nil
		}
	} else {
		c.TerminalKind, c.TerminalAt, c.CompletedAt = nil, nil, nil
	}
	switch {
	case !was && st.Terminal():
		return 1
	case was && !st.Terminal():
		return -1
	}
	return 0
}

// afterStatus roda os efeitos de um case que mudou de terminal: contadores do pai, bloqueios resolvidos (done).
func (t *txc) afterStatus(before, after store.Case, dTerminal int, actor domain.Actor, runID *domain.ID) error {
	if err := t.adjustParent(after.CompanyID, after.ParentCaseID, 0, dTerminal, actor, runID); err != nil {
		return err
	}
	if after.Status != string(domain.CaseDone) || before.Status == string(domain.CaseDone) {
		return nil
	}
	freed, err := t.q.ResolveBlockersOf(t.ctx, store.ResolveBlockersOfParams{CompanyID: after.CompanyID, BlockerCaseID: after.ID, Now: &t.now})
	if err != nil {
		return fmt.Errorf("resolve blockers: %w", err)
	}
	for _, id := range freed {
		n, err := t.q.CountOpenBlockers(t.ctx, store.CountOpenBlockersParams{CompanyID: after.CompanyID, CaseID: id})
		if err != nil {
			return fmt.Errorf("count blockers: %w", err)
		}
		if n > 0 {
			continue
		}
		if err := t.q.SetCaseAttention(t.ctx, store.SetCaseAttentionParams{CompanyID: after.CompanyID, ID: id, Attention: ptrTo("blockers_resolved"), Now: &t.now}); err != nil {
			return fmt.Errorf("set attention: %w", err)
		}
		if _, err := t.caseEvent(after.CompanyID, id, domain.CaseEvBlockersResolved, domain.SystemActor, nil, nil, nil,
			map[string]any{"by_case_id": after.ID.String(), "by_title": after.Title}); err != nil {
			return err
		}
	}
	return nil
}

// checkParent confere o pai: existe na empresa e não fecha um ciclo.
func (t *txc) checkParent(companyID, caseID domain.ID, parentID domain.ID) error {
	cur := parentID
	for i := 0; i < maxCaseDepth; i++ {
		if cur == caseID {
			return domain.Invalid("parent", "a case cannot be its own ancestor")
		}
		p, err := t.q.GetCaseByID(t.ctx, store.GetCaseByIDParams{CompanyID: companyID, ID: cur})
		if err != nil {
			if i == 0 {
				return domain.Invalid("parent", "parent case not found in company")
			}
			return nil
		}
		if p.ParentCaseID == nil {
			return nil
		}
		cur = *p.ParentCaseID
	}
	return domain.Invalid("parent", fmt.Sprintf("the case tree is limited to %d levels", maxCaseDepth))
}

// firstStage devolve a etapa inicial: a de key dada ou a primeira ativa (não desligada, não terminal).
func firstStage(v PipelineView, key string) (domain.Stage, error) {
	if key = strings.TrimSpace(key); key != "" {
		st, ok := v.Stage(key)
		if !ok {
			return domain.Stage{}, domain.Invalid("stage", "unknown stage "+key)
		}
		if st.Config.Disabled {
			return domain.Stage{}, domain.Invalid("stage", "stage "+st.Name+" is disabled")
		}
		return st, nil
	}
	for _, st := range v.Stages {
		if !st.Config.Disabled && !st.Kind.Terminal() {
			return st, nil
		}
	}
	return domain.Stage{}, domain.Invalid("pipeline", "the pipeline has no active stage")
}

// ---------- criação ----------

// CreateCase cria um case (ou atualiza o de mesmo tipo+key). Devolve o case e se ele foi criado.
func (s *Service) CreateCase(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateCaseInput) (CaseView, bool, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, false, err
	}
	if err := personaWrite(actor, in.RunID); err != nil {
		return CaseView{}, false, err
	}
	title, summary := strings.TrimSpace(in.Title), strings.TrimSpace(in.Summary)
	if err := checkCaseText(title, summary); err != nil {
		return CaseView{}, false, err
	}
	ctype, err := normalizeCaseType(in.Type)
	if err != nil {
		return CaseView{}, false, err
	}
	key := strings.TrimSpace(in.Key)
	if utf8.RuneCountInString(key) > maxCaseKeyRunes {
		return CaseView{}, false, domain.Invalid("key", fmt.Sprintf("too long (max %d)", maxCaseKeyRunes))
	}
	fields, _, err := mergeFields(nil, in.Fields, false)
	if err != nil {
		return CaseView{}, false, err
	}
	var pv PipelineView
	var stage domain.Stage
	if in.PipelineID != nil {
		if pv, err = s.GetPipeline(ctx, companyID, in.PipelineID.String()); err != nil {
			return CaseView{}, false, err
		}
		if pv.ArchivedAt != nil {
			return CaseView{}, false, domain.Invalid("pipeline", "the pipeline is archived")
		}
		if stage, err = firstStage(pv, in.StageKey); err != nil {
			return CaseView{}, false, err
		}
		if actor.Type == domain.ActorPersona && stage.Kind != domain.StageWorking {
			return CaseView{}, false, fmt.Errorf("a persona creates cases in a working stage: %w", domain.ErrNotAllowed)
		}
	} else if in.StageKey != "" {
		return CaseView{}, false, domain.Invalid("stage", "a stage needs a pipeline")
	}
	status := in.Status
	if in.PipelineID != nil {
		if status != "" && status != domain.StatusForStage(stage.Kind) {
			return CaseView{}, false, domain.Invalid("status", "a case in a pipeline takes the status of its stage")
		}
		status = domain.StatusForStage(stage.Kind)
	}
	if status == "" {
		status = domain.CaseDraft
	}
	if !status.Valid() {
		return CaseView{}, false, domain.Invalid("status", fmt.Sprintf("unknown status %q", status))
	}
	var id domain.ID
	created := false
	err = s.withTx(ctx, func(t *txc) error {
		prefix, err := t.companyPrefix(companyID)
		if err != nil {
			return err
		}
		if key != "" {
			ex, err := t.q.GetCaseByTypeKeyForUpdate(ctx, store.GetCaseByTypeKeyForUpdateParams{CompanyID: companyID, CaseType: ctype, Key: &key})
			if err == nil {
				id = ex.ID
				return t.upsertExisting(ex, prefix, actor, title, summary, in)
			}
			if !isNoRows(err) {
				return fmt.Errorf("lookup case key: %w", err)
			}
		}
		if in.PipelineID != nil {
			if err := checkStageFields(stage, fields, false, true); err != nil {
				return err
			}
		}
		if in.ProjectID != nil {
			if _, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *in.ProjectID}); err != nil {
				return domain.Invalid("project_id", "project not found in company")
			}
		}
		projectID := in.ProjectID
		if projectID == nil && in.PipelineID != nil {
			projectID = pv.ProjectID
		}
		if in.ParentID != nil {
			if _, err := t.q.GetCaseByID(ctx, store.GetCaseByIDParams{CompanyID: companyID, ID: *in.ParentID}); err != nil {
				return domain.Invalid("parent", "parent case not found in company")
			}
		}
		num, err := t.q.NextCaseNumber(ctx, companyID)
		if err != nil {
			return fmt.Errorf("next case number: %w", err)
		}
		raw, err := marshalFields(fields)
		if err != nil {
			return err
		}
		p := store.CreateCaseParams{ID: domain.NewID(), CompanyID: companyID, Number: num, CaseType: ctype, Title: title, Summary: summary,
			Fields: raw, Status: string(status), PipelineID: in.PipelineID, ParentCaseID: in.ParentID, ProjectID: projectID,
			CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now}
		if key != "" {
			p.Key = &key
		}
		if in.PipelineID != nil {
			p.StageID = &stage.ID
		}
		if actor.Type == domain.ActorPersona {
			p.OriginRunID = in.RunID
		}
		if status.Terminal() {
			k := string(status)
			p.TerminalKind, p.TerminalAt = &k, &t.now
			if status == domain.CaseDone {
				p.CompletedAt = &t.now
			}
		}
		row, err := t.q.CreateCase(ctx, p)
		if err != nil {
			if isUniqueOn(err, "cases_type_key_uq") {
				return fmt.Errorf("a case with this type and key was just created: %w", domain.ErrConflict)
			}
			return mapErr(err, "create case")
		}
		id, created = row.ID, true
		payload := map[string]any{"identifier": domain.CaseIdentifier(prefix, num), "title": title, "status": status}
		if in.PipelineID != nil {
			payload["pipeline"], payload["stage"] = pv.Key, stage.Key
		}
		ev, err := t.caseEvent(companyID, row.ID, domain.CaseEvCreated, actor, in.RunID, nil, p.StageID, payload)
		if err != nil {
			return err
		}
		if err := t.adjustParent(companyID, in.ParentID, 1, b2i(status.Terminal()), actor, in.RunID); err != nil {
			return err
		}
		if err := t.linkPersonaTask(companyID, row.ID, actor, in.TaskID, in.RunID, domain.LinkOrigin); err != nil {
			return err
		}
		if in.PipelineID != nil && stage.Config.Automation.Enabled() {
			if err := t.startAutomation(row, prefix, pv, stage, ev.ID); err != nil {
				return err
			}
		}
		return t.activity(actor, companyID, "case.created", "case", row.ID, payload, "")
	})
	if err != nil {
		return CaseView{}, false, err
	}
	v, err := loadCaseView(ctx, s.read(), companyID, id)
	return v, created, err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// upsertExisting aplica o upsert por tipo+key: título, resumo e campos (merge), sem mudar de etapa.
func (t *txc) upsertExisting(ex store.Case, prefix string, actor domain.Actor, title, summary string, in CreateCaseInput) error {
	cur := caseFromRow(ex, prefix)
	fields, fchanged, err := mergeFields(cur.Fields, in.Fields, false)
	if err != nil {
		return err
	}
	changed := map[string]any{}
	if title != ex.Title {
		ex.Title, changed["title"] = title, title
	}
	if summary != "" && summary != ex.Summary {
		ex.Summary, changed["summary"] = summary, true
	}
	if fchanged {
		raw, err := marshalFields(fields)
		if err != nil {
			return err
		}
		ex.Fields = raw
	}
	if len(changed) == 0 && !fchanged {
		return t.linkPersonaTask(ex.CompanyID, ex.ID, actor, in.TaskID, in.RunID, domain.LinkWork)
	}
	if ex.ApprovedVersion != nil {
		ex.ReapprovalRequired = true
	}
	if _, err := t.saveCase(ex); err != nil {
		return err
	}
	if len(changed) > 0 {
		changed["upsert"] = true
		if _, err := t.caseEvent(ex.CompanyID, ex.ID, domain.CaseEvUpdated, actor, in.RunID, nil, nil, changed); err != nil {
			return err
		}
	}
	if fchanged {
		if _, err := t.caseEvent(ex.CompanyID, ex.ID, domain.CaseEvFieldsChanged, actor, in.RunID, nil, nil, map[string]any{"keys": fieldKeys(in.Fields), "upsert": true}); err != nil {
			return err
		}
	}
	if err := t.linkPersonaTask(ex.CompanyID, ex.ID, actor, in.TaskID, in.RunID, domain.LinkWork); err != nil {
		return err
	}
	return t.activity(actor, ex.CompanyID, "case.updated", "case", ex.ID, map[string]any{"identifier": domain.CaseIdentifier(prefix, ex.Number), "upsert": true}, "")
}

func fieldKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------- atualização ----------

// UpdateCase altera título, resumo, campos, status (case solto), projeto, pai ou dispensa a atenção.
func (s *Service) UpdateCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in UpdateCaseInput) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, err
	}
	if err := personaWrite(actor, in.RunID); err != nil {
		return CaseView{}, err
	}
	if in.ProjectID != nil && in.ClearProject {
		return CaseView{}, domain.Invalid("project_id", "cannot set and clear the project at once")
	}
	if in.ParentID != nil && in.ClearParent {
		return CaseView{}, domain.Invalid("parent", "cannot set and clear the parent at once")
	}
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.lockCase(companyID, caseID)
		if err != nil {
			return err
		}
		if err := checkVersion(cur, in.ExpectedVersion); err != nil {
			return err
		}
		prefix, err := t.companyPrefix(companyID)
		if err != nil {
			return err
		}
		before := cur
		next, changed, fieldsChanged, err := t.applyCaseEdits(cur, prefix, in.Title, in.Summary, in.Fields, in.ReplaceFields)
		if err != nil {
			return err
		}
		dTerminal := 0
		if in.Status != nil && string(*in.Status) != next.Status {
			if next.PipelineID != nil {
				return domain.Invalid("status", "a case in a pipeline changes status by moving between stages")
			}
			if !in.Status.Valid() {
				return domain.Invalid("status", fmt.Sprintf("unknown status %q", *in.Status))
			}
			if actor.Type == domain.ActorPersona && *in.Status == domain.CaseApproved {
				return fmt.Errorf("a persona does not approve cases: %w", domain.ErrNotAllowed)
			}
			next.Status = string(*in.Status)
			dTerminal = applyTerminal(&next, *in.Status, t.now)
			next.Attention, next.AttentionAt = nil, nil
		}
		switch {
		case in.ClearProject && next.ProjectID != nil:
			next.ProjectID, changed["project_id"] = nil, nil
		case in.ProjectID != nil && !sameID(next.ProjectID, in.ProjectID):
			if _, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *in.ProjectID}); err != nil {
				return domain.Invalid("project_id", "project not found in company")
			}
			next.ProjectID, changed["project_id"] = in.ProjectID, in.ProjectID.String()
		}
		parentChanged := false
		switch {
		case in.ClearParent && next.ParentCaseID != nil:
			next.ParentCaseID, parentChanged = nil, true
		case in.ParentID != nil && !sameID(next.ParentCaseID, in.ParentID):
			if err := t.checkParent(companyID, cur.ID, *in.ParentID); err != nil {
				return err
			}
			next.ParentCaseID, parentChanged = in.ParentID, true
		}
		if parentChanged {
			changed["parent"] = idPtrStr(next.ParentCaseID)
		}
		if in.ClearAttention && next.Attention != nil {
			next.Attention, next.AttentionAt, changed["attention"] = nil, nil, "dismissed"
		}
		if len(changed) == 0 && !fieldsChanged && next.Status == before.Status {
			return t.linkPersonaTask(companyID, cur.ID, actor, in.TaskID, in.RunID, domain.LinkWork)
		}
		if (len(changed) > 0 || fieldsChanged) && next.ApprovedVersion != nil && (in.Title != nil || in.Summary != nil || fieldsChanged) {
			next.ReapprovalRequired = true
		}
		saved, err := t.saveCase(next)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			if _, err := t.caseEvent(companyID, cur.ID, domain.CaseEvUpdated, actor, in.RunID, nil, nil, changed); err != nil {
				return err
			}
		}
		if fieldsChanged {
			if _, err := t.caseEvent(companyID, cur.ID, domain.CaseEvFieldsChanged, actor, in.RunID, nil, nil, map[string]any{"keys": fieldKeys(in.Fields)}); err != nil {
				return err
			}
		}
		if saved.Status != before.Status {
			if _, err := t.caseEvent(companyID, cur.ID, domain.CaseEvStatusChanged, actor, in.RunID, nil, nil,
				map[string]any{"from": before.Status, "to": saved.Status}); err != nil {
				return err
			}
		}
		if parentChanged {
			if err := t.adjustParent(companyID, before.ParentCaseID, -1, -b2i(before.TerminalKind != nil), actor, in.RunID); err != nil {
				return err
			}
			if err := t.adjustParent(companyID, saved.ParentCaseID, 1, b2i(saved.TerminalKind != nil), actor, in.RunID); err != nil {
				return err
			}
		} else if err := t.afterStatus(before, saved, dTerminal, actor, in.RunID); err != nil {
			return err
		}
		if parentChanged && saved.Status == string(domain.CaseDone) && before.Status != string(domain.CaseDone) {
			if err := t.afterStatus(before, saved, 0, actor, in.RunID); err != nil {
				return err
			}
		}
		if err := t.linkPersonaTask(companyID, cur.ID, actor, in.TaskID, in.RunID, domain.LinkWork); err != nil {
			return err
		}
		return t.activity(actor, companyID, "case.updated", "case", cur.ID, map[string]any{"identifier": domain.CaseIdentifier(prefix, cur.Number),
			"version": saved.Version}, "")
	})
	if err != nil {
		return CaseView{}, err
	}
	return loadCaseView(ctx, s.read(), companyID, caseID)
}

func idPtrStr(id *domain.ID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

// applyCaseEdits aplica título, resumo e campos sobre o case (em memória) e diz o que mudou.
func (t *txc) applyCaseEdits(cur store.Case, prefix string, title, summary *string, fields map[string]any, replace bool) (store.Case, map[string]any, bool, error) {
	next := cur
	changed := map[string]any{}
	if title != nil {
		if v := strings.TrimSpace(*title); v != cur.Title {
			next.Title, changed["title"] = v, v
		}
	}
	if summary != nil {
		if v := strings.TrimSpace(*summary); v != cur.Summary {
			next.Summary, changed["summary"] = v, true
		}
	}
	if err := checkCaseText(next.Title, next.Summary); err != nil {
		return cur, nil, false, err
	}
	fchanged := false
	if fields != nil || replace {
		base := caseFromRow(cur, prefix).Fields
		merged, ch, err := mergeFields(base, fields, replace)
		if err != nil {
			return cur, nil, false, err
		}
		if ch {
			if next.StageID != nil {
				st, err := t.q.GetStageByID(t.ctx, store.GetStageByIDParams{CompanyID: cur.CompanyID, ID: *next.StageID})
				if err == nil {
					if err := checkStageFields(stageFromRow(st), merged, false, false); err != nil {
						return cur, nil, false, err
					}
				}
			}
			raw, err := marshalFields(merged)
			if err != nil {
				return cur, nil, false, err
			}
			next.Fields, fchanged = raw, true
		}
	}
	return next, changed, fchanged, nil
}

// ---------- tasks ligadas ----------

// LinkCaseTask liga uma task ao case. A persona só liga a task da própria execução (papel work).
func (s *Service) LinkCaseTask(ctx context.Context, actor domain.Actor, companyID, caseID, taskID domain.ID, role domain.CaseLinkRole, runID *domain.ID) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, err
	}
	if err := personaWrite(actor, runID); err != nil {
		return CaseView{}, err
	}
	if role == "" {
		role = domain.LinkWork
	}
	if !role.Valid() {
		return CaseView{}, domain.Invalid("role", "use origin, work, automation or conversation")
	}
	if actor.Type == domain.ActorPersona && role != domain.LinkWork && role != domain.LinkOrigin {
		return CaseView{}, fmt.Errorf("a persona links its task as work or origin: %w", domain.ErrNotAllowed)
	}
	err := s.withTx(ctx, func(t *txc) error {
		c, err := t.lockCase(companyID, caseID)
		if err != nil {
			return err
		}
		task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return domain.Invalid("task", "task not found in company")
		}
		if actor.Type == domain.ActorPersona {
			run, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: *runID})
			if err != nil || run.TaskID == nil || *run.TaskID != task.ID {
				return fmt.Errorf("a persona links only the task of its own run: %w", domain.ErrNotAllowed)
			}
		}
		n, err := t.q.LinkCaseTask(ctx, store.LinkCaseTaskParams{CaseID: c.ID, TaskID: task.ID, CompanyID: companyID, Role: string(role), CreatedByRunID: runID, Now: t.now})
		if err != nil {
			return mapErr(err, "link task")
		}
		if n == 0 {
			return nil
		}
		if _, err := t.caseEvent(companyID, c.ID, domain.CaseEvTaskLinked, actor, runID, nil, nil, map[string]any{"task_id": task.ID.String(), "role": role}); err != nil {
			return err
		}
		return t.activity(actor, companyID, "case.task_linked", "case", c.ID, map[string]any{"task_id": task.ID, "role": role}, "")
	})
	if err != nil {
		return CaseView{}, err
	}
	return s.GetCaseView(ctx, companyID, caseID.String())
}

// UnlinkCaseTask desfaz a ligação de uma task com o case (só humano).
func (s *Service) UnlinkCaseTask(ctx context.Context, actor domain.Actor, companyID, caseID, taskID domain.ID) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return CaseView{}, fmt.Errorf("a persona does not unlink tasks: %w", domain.ErrNotAllowed)
	}
	err := s.withTx(ctx, func(t *txc) error {
		c, err := t.lockCase(companyID, caseID)
		if err != nil {
			return err
		}
		n, err := t.q.UnlinkCaseTask(ctx, store.UnlinkCaseTaskParams{CompanyID: companyID, CaseID: c.ID, TaskID: taskID})
		if err != nil {
			return fmt.Errorf("unlink task: %w", err)
		}
		if n == 0 {
			return notFound("case link")
		}
		if _, err := t.caseEvent(companyID, c.ID, domain.CaseEvTaskUnlinked, actor, nil, nil, nil, map[string]any{"task_id": taskID.String()}); err != nil {
			return err
		}
		return t.activity(actor, companyID, "case.task_unlinked", "case", c.ID, map[string]any{"task_id": taskID}, "")
	})
	if err != nil {
		return CaseView{}, err
	}
	return s.GetCaseView(ctx, companyID, caseID.String())
}

// OpenCaseConversation cria a task "Discutir: <título>" ligada ao case (papel conversation), com o pacote de
// contexto na descrição e, se a etapa tem automação, atribuída à persona dela.
func (s *Service) OpenCaseConversation(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID) (TaskView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return TaskView{}, fmt.Errorf("a conversation is opened by a human: %w", domain.ErrNotAllowed)
	}
	var out TaskView
	err := s.withTx(ctx, func(t *txc) error {
		c, err := t.lockCase(companyID, caseID)
		if err != nil {
			return err
		}
		prefix, err := t.companyPrefix(companyID)
		if err != nil {
			return err
		}
		var persona *domain.ID
		ctxOpts := contextOpts{}
		if c.StageID != nil && c.PipelineID != nil {
			pv, err := t.s.pipelineView(ctx, t.q, mustPipeline(ctx, t.q, companyID, *c.PipelineID))
			if err == nil {
				if st, ok := pv.StageByID(*c.StageID); ok {
					if st.Config.Automation.Enabled() {
						if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *st.Config.Automation.PersonaID}); err == nil {
							persona = st.Config.Automation.PersonaID
						}
					}
					ctxOpts.pipeline, ctxOpts.stage = &pv, &st
				}
			}
		}
		body, err := t.s.buildCaseContext(ctx, t.q, companyID, c, prefix, ctxOpts)
		if err != nil {
			return err
		}
		title := "Discutir: " + c.Title
		if r := []rune(title); len(r) > 500 {
			title = string(r[:499]) + "…"
		}
		tv, err := t.createTask(actor, companyID, CreateTaskInput{Title: title, Description: body, PersonaID: persona, ProjectID: activeProject(ctx, t.q, companyID, c.ProjectID)})
		if err != nil {
			return err
		}
		out = tv
		if _, err := t.q.LinkCaseTask(ctx, store.LinkCaseTaskParams{CaseID: c.ID, TaskID: tv.ID, CompanyID: companyID, Role: string(domain.LinkConversation), Now: t.now}); err != nil {
			return mapErr(err, "link task")
		}
		_, err = t.caseEvent(companyID, c.ID, domain.CaseEvTaskLinked, actor, nil, nil, nil, map[string]any{"task_id": tv.ID.String(), "task_key": tv.Key, "role": domain.LinkConversation})
		return err
	})
	return out, err
}

func mustPipeline(ctx context.Context, q *store.Queries, companyID, id domain.ID) store.Pipeline {
	p, _ := q.GetPipelineByID(ctx, store.GetPipelineByIDParams{CompanyID: companyID, ID: id})
	return p
}

// activeProject devolve o projeto se ele existe e está ativo (task nova não nasce em projeto arquivado).
func activeProject(ctx context.Context, q *store.Queries, companyID domain.ID, id *domain.ID) *domain.ID {
	if id == nil {
		return nil
	}
	p, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *id})
	if err != nil || domain.ProjectStatus(p.Status) == domain.ProjectArchived {
		return nil
	}
	return id
}

// ---------- bloqueios ----------

// SetCaseBlockers troca os bloqueadores do case (lista vazia limpa). Bloqueador já em done nasce resolvido.
func (s *Service) SetCaseBlockers(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, expectedVersion int, blockers []domain.ID, runID *domain.ID) (CaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CaseView{}, err
	}
	if err := personaWrite(actor, runID); err != nil {
		return CaseView{}, err
	}
	if len(blockers) > maxCaseBlockers {
		return CaseView{}, domain.Invalid("blockers", fmt.Sprintf("at most %d blockers", maxCaseBlockers))
	}
	err := s.withTx(ctx, func(t *txc) error {
		c, err := t.lockCase(companyID, caseID)
		if err != nil {
			return err
		}
		if err := checkVersion(c, expectedVersion); err != nil {
			return err
		}
		if err := t.q.DeleteCaseBlockers(ctx, store.DeleteCaseBlockersParams{CompanyID: companyID, CaseID: c.ID}); err != nil {
			return fmt.Errorf("clear blockers: %w", err)
		}
		seen := map[domain.ID]bool{}
		var ids []string
		for _, b := range blockers {
			if seen[b] {
				continue
			}
			seen[b] = true
			if b == c.ID {
				return domain.Invalid("blockers", "a case cannot block itself")
			}
			bc, err := t.q.GetCaseByID(ctx, store.GetCaseByIDParams{CompanyID: companyID, ID: b})
			if err != nil {
				return domain.Invalid("blockers", "blocker case not found in company")
			}
			if cyc, err := t.blockerCycle(companyID, c.ID, b); err != nil {
				return err
			} else if cyc {
				return domain.Invalid("blockers", "that would make the cases wait on each other")
			}
			var resolved *time.Time
			if bc.Status == string(domain.CaseDone) {
				resolved = &t.now
			}
			if err := t.q.AddCaseBlocker(ctx, store.AddCaseBlockerParams{CaseID: c.ID, BlockerCaseID: b, CompanyID: companyID, Now: t.now, ResolvedAt: resolved}); err != nil {
				return mapErr(err, "add blocker")
			}
			ids = append(ids, b.String())
		}
		if _, err := t.saveCase(c); err != nil {
			return err
		}
		if _, err := t.caseEvent(companyID, c.ID, domain.CaseEvBlockersSet, actor, runID, nil, nil, map[string]any{"blockers": ids}); err != nil {
			return err
		}
		return t.activity(actor, companyID, "case.blockers_set", "case", c.ID, map[string]any{"blockers": len(ids)}, "")
	})
	if err != nil {
		return CaseView{}, err
	}
	return s.GetCaseView(ctx, companyID, caseID.String())
}

// blockerCycle diz se blocker já espera (direta ou indiretamente) por caseID.
func (t *txc) blockerCycle(companyID, caseID, blocker domain.ID) (bool, error) {
	seen := map[domain.ID]bool{}
	frontier := []domain.ID{blocker}
	for depth := 0; len(frontier) > 0 && depth < maxCaseDepth; depth++ {
		var next []domain.ID
		for _, id := range frontier {
			deps, err := t.q.ListOpenBlockerIDs(t.ctx, store.ListOpenBlockerIDsParams{CompanyID: companyID, CaseID: id})
			if err != nil {
				return false, fmt.Errorf("list blockers: %w", err)
			}
			for _, d := range deps {
				if d == caseID {
					return true, nil
				}
				if !seen[d] {
					seen[d] = true
					next = append(next, d)
				}
			}
		}
		frontier = next
	}
	return false, nil
}

// ListChildCases lista os filhos de um case.
func (s *Service) ListChildCases(ctx context.Context, companyID, caseID domain.ID) ([]CaseView, error) {
	return s.ListCases(ctx, companyID, CaseFilter{ParentID: &caseID, Limit: maxCaseList})
}
