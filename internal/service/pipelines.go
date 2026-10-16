package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Pipelines e etapas (ADR-0034). Criar, configurar, arquivar e excluir é de admin; ler é de qualquer membro. A
// persona (run token) só lê pipelines: quem os desenha é o humano.

const (
	maxPipelines          = 100
	maxStagesPerPipeline  = 30
	maxTransitions        = 300
	maxPipelineNameRunes  = 120
	maxPipelineDescBytes  = 8 << 10
	maxTransitionLabelLen = 120
)

// StageInput descreve uma etapa nova.
type StageInput struct {
	Key    string
	Name   string
	Kind   domain.StageKind
	Config domain.StageConfig
}

// UpdateStageInput altera uma etapa; nil fica como está.
type UpdateStageInput struct {
	Key    *string
	Name   *string
	Kind   *domain.StageKind
	Config *domain.StageConfig
}

// CreatePipelineInput cria um pipeline, opcionalmente já com as etapas (na ordem dada).
type CreatePipelineInput struct {
	Key, Name, Description string
	ProjectID              *domain.ID
	EnforceTransitions     bool
	Stages                 []StageInput
}

// UpdatePipelineInput altera um pipeline; nil fica como está.
type UpdatePipelineInput struct {
	Key, Name, Description *string
	ProjectID              *domain.ID
	ClearProject           bool
	EnforceTransitions     *bool
	// Archived arquiva (true) ou reativa (false). Pipeline arquivado não aceita cases novos nem movimentação.
	Archived *bool
}

// TransitionSpec é uma aresta pelo key das etapas.
type TransitionSpec struct {
	From, To, Label string
}

// PipelineView é o pipeline com etapas, transições e contagens.
type PipelineView struct {
	domain.Pipeline
	ProjectSlug string              `json:"project_slug,omitempty"`
	ProjectName string              `json:"project_name,omitempty"`
	Stages      []domain.Stage      `json:"stages"`
	Transitions []domain.Transition `json:"transitions"`
	OpenCases   int                 `json:"open_cases"`
	InReview    int                 `json:"in_review"`
	TotalCases  int                 `json:"total_cases"`
}

// Stage devolve a etapa de key k.
func (v PipelineView) Stage(k string) (domain.Stage, bool) {
	for _, s := range v.Stages {
		if s.Key == k {
			return s, true
		}
	}
	return domain.Stage{}, false
}

// StageByID devolve a etapa de id.
func (v PipelineView) StageByID(id domain.ID) (domain.Stage, bool) {
	for _, s := range v.Stages {
		if s.ID == id {
			return s, true
		}
	}
	return domain.Stage{}, false
}

func pipelineFromRow(r store.Pipeline) domain.Pipeline {
	return domain.Pipeline{ID: r.ID, CompanyID: r.CompanyID, ProjectID: r.ProjectID, Key: r.Key, Name: r.Name, Description: r.Description,
		EnforceTransitions: r.EnforceTransitions, ArchivedAt: r.ArchivedAt, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func parseStageConfig(raw []byte) domain.StageConfig {
	var c domain.StageConfig
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &c) // validada na escrita; um jsonb estranho vira configuração vazia
	}
	return c
}

func stageFromRow(r store.PipelineStage) domain.Stage {
	return domain.Stage{ID: r.ID, PipelineID: r.PipelineID, Key: r.Key, Name: r.Name, Position: int(r.Position), Kind: domain.StageKind(r.Kind),
		Config: parseStageConfig(r.Config), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// normalizeStageConfig apara os textos da configuração.
func normalizeStageConfig(c domain.StageConfig) domain.StageConfig {
	c.WhatHappensHere = strings.TrimSpace(c.WhatHappensHere)
	c.DisabledReason = strings.TrimSpace(c.DisabledReason)
	for i := range c.Fields {
		c.Fields[i].Key = strings.TrimSpace(c.Fields[i].Key)
		c.Fields[i].Label = strings.TrimSpace(c.Fields[i].Label)
		for j := range c.Fields[i].Options {
			c.Fields[i].Options[j] = strings.TrimSpace(c.Fields[i].Options[j])
		}
	}
	if a := c.Automation; a != nil {
		a.Instructions = strings.TrimSpace(a.Instructions)
		if a.PersonaID == nil && a.Instructions == "" && a.Priority == "" {
			c.Automation = nil
		}
	}
	if c.Approval != nil {
		c.Approval.Approver = strings.TrimSpace(c.Approval.Approver)
		if !c.Approval.Required && c.Approval.Approver == "" {
			c.Approval = nil
		} else if c.Approval.Approver == "" {
			c.Approval.Approver = domain.ApproverAnyMember
		}
	}
	if g := c.Gates; g != nil && !g.RequireChildrenTerminal {
		c.Gates = nil
	}
	return c
}

func checkPipelineText(name, desc string) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > maxPipelineNameRunes {
		return domain.Invalid("name", fmt.Sprintf("must have 1-%d characters", maxPipelineNameRunes))
	}
	if len(desc) > maxPipelineDescBytes {
		return domain.Invalid("description", fmt.Sprintf("too long (max %d bytes)", maxPipelineDescBytes))
	}
	return nil
}

// checkStageRefs confere as referências externas da configuração: persona da automação e aprovador.
func (t *txc) checkStageRefs(companyID domain.ID, c domain.StageConfig) error {
	if a := c.Automation; a.Enabled() {
		if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *a.PersonaID}); err != nil {
			return domain.Invalid("config.automation.persona_id", "persona not found in company")
		}
	}
	if c.Approval != nil {
		ref, err := domain.ParseApprover(c.Approval.Approver)
		if err != nil {
			return err
		}
		if ref.PersonaID != nil {
			if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *ref.PersonaID}); err != nil {
				return domain.Invalid("config.approval.approver", "persona not found in company")
			}
		}
		if ref.UserID != nil {
			if _, err := t.q.GetMemberRole(t.ctx, store.GetMemberRoleParams{CompanyID: companyID, UserID: *ref.UserID}); err != nil {
				return domain.Invalid("config.approval.approver", "user is not a member of the company")
			}
		}
	}
	return nil
}

// validateStages confere o conjunto de etapas de um pipeline: keys únicas, configurações válidas e referências
// de revisão para etapas que existem.
func (t *txc) validateStages(companyID domain.ID, stages []domain.Stage) error {
	if len(stages) > maxStagesPerPipeline {
		return domain.Invalid("stages", fmt.Sprintf("at most %d stages", maxStagesPerPipeline))
	}
	keys := map[string]bool{}
	for _, s := range stages {
		if keys[s.Key] {
			return domain.Invalid("stages", "duplicate stage key "+s.Key)
		}
		keys[s.Key] = true
	}
	for _, s := range stages {
		if err := domain.ValidateStageConfig(s.Kind, s.Config, keys); err != nil {
			return fmt.Errorf("stage %s: %w", s.Key, err)
		}
		if r := s.Config.Review; r != nil {
			for _, k := range []string{r.ApproveTo, r.RejectTo, r.RequestChangesTo} {
				if k == s.Key {
					return domain.Invalid("config.review", "a review destination must be another stage")
				}
			}
		}
		if err := t.checkStageRefs(companyID, s.Config); err != nil {
			return fmt.Errorf("stage %s: %w", s.Key, err)
		}
	}
	return nil
}

func normalizeStageInput(in StageInput) (StageInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > maxPipelineNameRunes {
		return in, domain.Invalid("stages.name", fmt.Sprintf("must have 1-%d characters", maxPipelineNameRunes))
	}
	key, err := resolveSlug(strings.TrimSpace(in.Key), in.Name)
	if err != nil {
		return in, domain.Invalid("stages.key", "must be lowercase letters, digits and dashes (1-63 chars)")
	}
	in.Key = key
	if in.Kind == "" {
		in.Kind = domain.StageWorking
	}
	if !in.Kind.Valid() {
		return in, domain.Invalid("stages.kind", "use working, review, done or cancelled")
	}
	in.Config = normalizeStageConfig(in.Config)
	return in, nil
}

func marshalConfig(c domain.StageConfig) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal stage config: %w", err)
	}
	return b, nil
}

// CreatePipeline cria um pipeline (admin), com as etapas informadas.
func (s *Service) CreatePipeline(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreatePipelineInput) (PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return PipelineView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return PipelineView{}, fmt.Errorf("pipelines are designed by humans: %w", domain.ErrNotAllowed)
	}
	name, desc := strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if err := checkPipelineText(name, desc); err != nil {
		return PipelineView{}, err
	}
	key, err := resolveSlug(strings.TrimSpace(in.Key), name)
	if err != nil {
		return PipelineView{}, domain.Invalid("key", "must be lowercase letters, digits and dashes (1-63 chars)")
	}
	stages := make([]StageInput, len(in.Stages))
	for i, st := range in.Stages {
		if stages[i], err = normalizeStageInput(st); err != nil {
			return PipelineView{}, err
		}
	}
	var id domain.ID
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		existing, err := t.q.ListPipelines(ctx, companyID)
		if err != nil {
			return fmt.Errorf("list pipelines: %w", err)
		}
		if len(existing) >= maxPipelines {
			return domain.Invalid("pipeline", fmt.Sprintf("at most %d pipelines per company", maxPipelines))
		}
		if in.ProjectID != nil {
			if _, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *in.ProjectID}); err != nil {
				return domain.Invalid("project_id", "project not found in company")
			}
		}
		row, err := t.q.CreatePipeline(ctx, store.CreatePipelineParams{ID: domain.NewID(), CompanyID: companyID, ProjectID: in.ProjectID,
			Key: key, Name: name, Description: desc, EnforceTransitions: in.EnforceTransitions, CreatedByType: string(actor.Type),
			CreatedByID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create pipeline")
		}
		id = row.ID
		ds := make([]domain.Stage, len(stages))
		for i, st := range stages {
			ds[i] = domain.Stage{Key: st.Key, Name: st.Name, Kind: st.Kind, Config: st.Config, Position: i}
		}
		if err := t.validateStages(companyID, ds); err != nil {
			return err
		}
		for _, st := range ds {
			cfg, err := marshalConfig(st.Config)
			if err != nil {
				return err
			}
			if _, err := t.q.CreateStage(ctx, store.CreateStageParams{ID: domain.NewID(), CompanyID: companyID, PipelineID: row.ID, Key: st.Key,
				Name: st.Name, Position: i32(st.Position), Kind: string(st.Kind), Config: cfg, Now: t.now}); err != nil {
				return mapErr(err, "create stage")
			}
		}
		return t.activity(actor, companyID, "pipeline.created", "pipeline", row.ID, map[string]any{"key": key, "name": name, "stages": len(ds)}, "")
	})
	if err != nil {
		return PipelineView{}, err
	}
	return s.GetPipeline(ctx, companyID, id.String())
}

// lockPipeline trava a linha do pipeline (as mudanças de etapas e transições serializam por ela).
func (t *txc) lockPipeline(companyID, pipelineID domain.ID) (store.Pipeline, error) {
	p, err := t.q.GetPipelineForUpdate(t.ctx, store.GetPipelineForUpdateParams{CompanyID: companyID, ID: pipelineID})
	if err != nil {
		return store.Pipeline{}, mapErr(err, "get pipeline")
	}
	return p, nil
}

func (t *txc) stagesOf(companyID, pipelineID domain.ID) ([]domain.Stage, error) {
	rows, err := t.q.ListStages(t.ctx, store.ListStagesParams{CompanyID: companyID, PipelineID: pipelineID})
	if err != nil {
		return nil, fmt.Errorf("list stages: %w", err)
	}
	out := make([]domain.Stage, len(rows))
	for i, r := range rows {
		out[i] = stageFromRow(r)
	}
	return out, nil
}

func (t *txc) touchPipeline(p store.Pipeline) error {
	_, err := t.q.UpdatePipeline(t.ctx, store.UpdatePipelineParams{CompanyID: p.CompanyID, ID: p.ID, ProjectID: p.ProjectID, Key: p.Key, Name: p.Name,
		Description: p.Description, EnforceTransitions: p.EnforceTransitions, ArchivedAt: p.ArchivedAt, Now: t.now})
	return mapErr(err, "update pipeline")
}

// UpdatePipeline altera nome, key, descrição, projeto, modo estrito ou arquiva/reativa (admin).
func (s *Service) UpdatePipeline(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, in UpdatePipelineInput) (PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return PipelineView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return PipelineView{}, fmt.Errorf("pipelines are designed by humans: %w", domain.ErrNotAllowed)
	}
	if in.ProjectID != nil && in.ClearProject {
		return PipelineView{}, domain.Invalid("project_id", "cannot set and clear the project at once")
	}
	err := s.withTx(ctx, func(t *txc) error {
		p, err := t.lockPipeline(companyID, pipelineID)
		if err != nil {
			return err
		}
		changed := map[string]any{}
		if in.Name != nil {
			if v := strings.TrimSpace(*in.Name); v != p.Name {
				p.Name, changed["name"] = v, v
			}
		}
		if in.Description != nil {
			if v := strings.TrimSpace(*in.Description); v != p.Description {
				p.Description, changed["description"] = v, true
			}
		}
		if err := checkPipelineText(p.Name, p.Description); err != nil {
			return err
		}
		if in.Key != nil {
			v := strings.TrimSpace(*in.Key)
			if !domain.ValidSlug(v) {
				return domain.Invalid("key", "must be lowercase letters, digits and dashes (1-63 chars)")
			}
			if v != p.Key {
				p.Key, changed["key"] = v, v
			}
		}
		switch {
		case in.ClearProject && p.ProjectID != nil:
			p.ProjectID, changed["project_id"] = nil, nil
		case in.ProjectID != nil && !sameID(p.ProjectID, in.ProjectID):
			if _, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *in.ProjectID}); err != nil {
				return domain.Invalid("project_id", "project not found in company")
			}
			p.ProjectID, changed["project_id"] = in.ProjectID, in.ProjectID
		}
		if in.EnforceTransitions != nil && *in.EnforceTransitions != p.EnforceTransitions {
			p.EnforceTransitions, changed["enforce_transitions"] = *in.EnforceTransitions, *in.EnforceTransitions
		}
		verb := "pipeline.updated"
		if in.Archived != nil && *in.Archived != (p.ArchivedAt != nil) {
			if *in.Archived {
				p.ArchivedAt, verb = &t.now, "pipeline.archived"
			} else {
				p.ArchivedAt, verb = nil, "pipeline.restored"
			}
			changed["archived"] = *in.Archived
		}
		if len(changed) == 0 {
			return nil
		}
		if err := t.touchPipeline(p); err != nil {
			return err
		}
		return t.activity(actor, companyID, verb, "pipeline", p.ID, changed, "")
	})
	if err != nil {
		return PipelineView{}, err
	}
	return s.GetPipeline(ctx, companyID, pipelineID.String())
}

// DeletePipeline exclui um pipeline sem cases (admin). Com cases, arquive.
func (s *Service) DeletePipeline(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	if actor.Type == domain.ActorPersona {
		return fmt.Errorf("pipelines are designed by humans: %w", domain.ErrNotAllowed)
	}
	return s.withTx(ctx, func(t *txc) error {
		p, err := t.lockPipeline(companyID, pipelineID)
		if err != nil {
			return err
		}
		n, err := t.q.CountCasesInPipeline(ctx, store.CountCasesInPipelineParams{CompanyID: companyID, PipelineID: &p.ID})
		if err != nil {
			return fmt.Errorf("count cases: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("the pipeline has %d case(s); archive it instead: %w", n, domain.ErrConflict)
		}
		if _, err := t.q.DeletePipeline(ctx, store.DeletePipelineParams{CompanyID: companyID, ID: p.ID}); err != nil {
			return fmt.Errorf("delete pipeline: %w", err)
		}
		return t.activity(actor, companyID, "pipeline.deleted", "pipeline", p.ID, map[string]any{"key": p.Key, "name": p.Name}, "")
	})
}

// GetPipeline resolve ref como UUID ou key e devolve etapas, transições e contagens.
func (s *Service) GetPipeline(ctx context.Context, companyID domain.ID, ref string) (PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return PipelineView{}, err
	}
	q := s.read()
	p, err := getPipelineRow(ctx, q, companyID, ref)
	if err != nil {
		return PipelineView{}, err
	}
	return s.pipelineView(ctx, q, p)
}

func getPipelineRow(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (store.Pipeline, error) {
	ref = strings.TrimSpace(ref)
	var p store.Pipeline
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		p, err = q.GetPipelineByID(ctx, store.GetPipelineByIDParams{CompanyID: companyID, ID: id})
	} else {
		p, err = q.GetPipelineByKey(ctx, store.GetPipelineByKeyParams{CompanyID: companyID, Key: ref})
	}
	if err != nil {
		return store.Pipeline{}, mapErr(err, "get pipeline")
	}
	return p, nil
}

func (s *Service) pipelineView(ctx context.Context, q *store.Queries, p store.Pipeline) (PipelineView, error) {
	v := PipelineView{Pipeline: pipelineFromRow(p)}
	rows, err := q.ListStages(ctx, store.ListStagesParams{CompanyID: p.CompanyID, PipelineID: p.ID})
	if err != nil {
		return PipelineView{}, fmt.Errorf("list stages: %w", err)
	}
	for _, r := range rows {
		v.Stages = append(v.Stages, stageFromRow(r))
	}
	trs, err := q.ListTransitions(ctx, store.ListTransitionsParams{CompanyID: p.CompanyID, PipelineID: p.ID})
	if err != nil {
		return PipelineView{}, fmt.Errorf("list transitions: %w", err)
	}
	for _, r := range trs {
		v.Transitions = append(v.Transitions, domain.Transition{ID: r.ID, From: r.FromStageID, To: r.ToStageID, Label: r.Label})
	}
	if p.ProjectID != nil {
		if pr, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: p.CompanyID, ID: *p.ProjectID}); err == nil {
			v.ProjectSlug, v.ProjectName = pr.Slug, pr.Name
		}
	}
	counts, err := q.PipelineCaseCounts(ctx, p.CompanyID)
	if err != nil {
		return PipelineView{}, fmt.Errorf("pipeline counts: %w", err)
	}
	for _, c := range counts {
		if c.PipelineID == p.ID {
			v.OpenCases, v.InReview, v.TotalCases = int(c.OpenCases), int(c.InReview), int(c.Total)
		}
	}
	return v, nil
}

// ListPipelines lista os pipelines da empresa (ativos primeiro), com etapas e contagens.
func (s *Service) ListPipelines(ctx context.Context, companyID domain.ID, includeArchived bool) ([]PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListPipelines(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list pipelines: %w", err)
	}
	stages, err := q.ListCompanyStages(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list stages: %w", err)
	}
	counts, err := q.PipelineCaseCounts(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("pipeline counts: %w", err)
	}
	projects, err := q.ListProjects(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]PipelineView, 0, len(rows))
	for _, p := range rows {
		if p.ArchivedAt != nil && !includeArchived {
			continue
		}
		v := PipelineView{Pipeline: pipelineFromRow(p)}
		for _, st := range stages {
			if st.PipelineID == p.ID {
				v.Stages = append(v.Stages, stageFromRow(st))
			}
		}
		for _, c := range counts {
			if c.PipelineID == p.ID {
				v.OpenCases, v.InReview, v.TotalCases = int(c.OpenCases), int(c.InReview), int(c.Total)
			}
		}
		for _, pr := range projects {
			if p.ProjectID != nil && pr.ID == *p.ProjectID {
				v.ProjectSlug, v.ProjectName = pr.Slug, pr.Name
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// stageRefIndex acha uma etapa pelo key ou id.
func stageRefIndex(stages []domain.Stage, ref string) int {
	for i, st := range stages {
		if st.Key == ref || st.ID.String() == ref {
			return i
		}
	}
	return -1
}

// AddStage acrescenta uma etapa (admin). position nil = no fim; senão a etapa entra nessa posição (0 = primeira).
func (s *Service) AddStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, in StageInput, position *int) (PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return PipelineView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return PipelineView{}, fmt.Errorf("pipelines are designed by humans: %w", domain.ErrNotAllowed)
	}
	in, err := normalizeStageInput(in)
	if err != nil {
		return PipelineView{}, err
	}
	err = s.withTx(ctx, func(t *txc) error {
		p, err := t.lockPipeline(companyID, pipelineID)
		if err != nil {
			return err
		}
		stages, err := t.stagesOf(companyID, p.ID)
		if err != nil {
			return err
		}
		pos := len(stages)
		if position != nil && *position >= 0 && *position < pos {
			pos = *position
		}
		ns := domain.Stage{Key: in.Key, Name: in.Name, Kind: in.Kind, Config: in.Config}
		all := slices.Insert(slices.Clone(stages), pos, ns)
		if err := t.validateStages(companyID, all); err != nil {
			return err
		}
		cfg, err := marshalConfig(in.Config)
		if err != nil {
			return err
		}
		row, err := t.q.CreateStage(ctx, store.CreateStageParams{ID: domain.NewID(), CompanyID: companyID, PipelineID: p.ID, Key: in.Key,
			Name: in.Name, Position: i32(pos), Kind: string(in.Kind), Config: cfg, Now: t.now})
		if err != nil {
			return mapErr(err, "create stage")
		}
		all[pos].ID = row.ID
		if err := t.renumberStages(companyID, all); err != nil {
			return err
		}
		if err := t.touchPipeline(p); err != nil {
			return err
		}
		return t.activity(actor, companyID, "pipeline.stage_added", "pipeline", p.ID, map[string]any{"stage": in.Key, "kind": in.Kind}, "")
	})
	if err != nil {
		return PipelineView{}, err
	}
	return s.GetPipeline(ctx, companyID, pipelineID.String())
}

func (t *txc) renumberStages(companyID domain.ID, stages []domain.Stage) error {
	for i, st := range stages {
		if st.Position == i && st.ID != (domain.ID{}) {
			continue
		}
		if err := t.q.SetStagePosition(t.ctx, store.SetStagePositionParams{CompanyID: companyID, ID: st.ID, Position: i32(i), Now: t.now}); err != nil {
			return fmt.Errorf("set stage position: %w", err)
		}
	}
	return nil
}

// UpdateStage altera uma etapa (admin). Trocar a key de uma etapa que outra usa como destino de revisão é recusado;
// mudar o tipo de uma etapa com cases também (o status deles depende do tipo).
func (s *Service) UpdateStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, stageRef string, in UpdateStageInput) (PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return PipelineView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return PipelineView{}, fmt.Errorf("pipelines are designed by humans: %w", domain.ErrNotAllowed)
	}
	err := s.withTx(ctx, func(t *txc) error {
		p, err := t.lockPipeline(companyID, pipelineID)
		if err != nil {
			return err
		}
		stages, err := t.stagesOf(companyID, p.ID)
		if err != nil {
			return err
		}
		i := stageRefIndex(stages, stageRef)
		if i < 0 {
			return notFound("stage")
		}
		cur := stages[i]
		next := cur
		if in.Name != nil {
			next.Name = *in.Name
		}
		if in.Key != nil {
			next.Key = *in.Key
		}
		if in.Kind != nil {
			next.Kind = *in.Kind
		}
		if in.Config != nil {
			next.Config = *in.Config
		}
		norm, err := normalizeStageInput(StageInput{Key: next.Key, Name: next.Name, Kind: next.Kind, Config: next.Config})
		if err != nil {
			return err
		}
		next.Key, next.Name, next.Kind, next.Config = norm.Key, norm.Name, norm.Kind, norm.Config
		if next.Key != cur.Key {
			for _, o := range stages {
				if r := o.Config.Review; r != nil && (r.ApproveTo == cur.Key || r.RejectTo == cur.Key || r.RequestChangesTo == cur.Key) {
					return domain.Invalid("key", fmt.Sprintf("stage %s uses %s as a review destination; change it first", o.Key, cur.Key))
				}
			}
		}
		if next.Kind != cur.Kind {
			n, err := t.q.CountCasesInStage(ctx, store.CountCasesInStageParams{CompanyID: companyID, StageID: &cur.ID})
			if err != nil {
				return fmt.Errorf("count cases: %w", err)
			}
			if n > 0 {
				return fmt.Errorf("the stage has %d case(s); move them before changing its kind: %w", n, domain.ErrConflict)
			}
		}
		stages[i] = next
		if err := t.validateStages(companyID, stages); err != nil {
			return err
		}
		cfg, err := marshalConfig(next.Config)
		if err != nil {
			return err
		}
		if _, err := t.q.UpdateStage(ctx, store.UpdateStageParams{CompanyID: companyID, ID: cur.ID, Key: next.Key, Name: next.Name, Kind: string(next.Kind),
			Config: cfg, Position: i32(cur.Position), Now: t.now}); err != nil {
			return mapErr(err, "update stage")
		}
		if err := t.touchPipeline(p); err != nil {
			return err
		}
		return t.activity(actor, companyID, "pipeline.stage_updated", "pipeline", p.ID, map[string]any{"stage": next.Key}, "")
	})
	if err != nil {
		return PipelineView{}, err
	}
	return s.GetPipeline(ctx, companyID, pipelineID.String())
}

// DeleteStage remove uma etapa sem cases (admin). As transições dela somem junto.
func (s *Service) DeleteStage(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, stageRef string) (PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return PipelineView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return PipelineView{}, fmt.Errorf("pipelines are designed by humans: %w", domain.ErrNotAllowed)
	}
	err := s.withTx(ctx, func(t *txc) error {
		p, err := t.lockPipeline(companyID, pipelineID)
		if err != nil {
			return err
		}
		stages, err := t.stagesOf(companyID, p.ID)
		if err != nil {
			return err
		}
		i := stageRefIndex(stages, stageRef)
		if i < 0 {
			return notFound("stage")
		}
		cur := stages[i]
		for _, o := range stages {
			if r := o.Config.Review; r != nil && o.ID != cur.ID && (r.ApproveTo == cur.Key || r.RejectTo == cur.Key || r.RequestChangesTo == cur.Key) {
				return domain.Invalid("stage", fmt.Sprintf("stage %s uses %s as a review destination; change it first", o.Key, cur.Key))
			}
		}
		n, err := t.q.CountCasesInStage(ctx, store.CountCasesInStageParams{CompanyID: companyID, StageID: &cur.ID})
		if err != nil {
			return fmt.Errorf("count cases: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("the stage has %d case(s); move them first: %w", n, domain.ErrConflict)
		}
		if _, err := t.q.DeleteStage(ctx, store.DeleteStageParams{CompanyID: companyID, ID: cur.ID}); err != nil {
			return fmt.Errorf("delete stage: %w", err)
		}
		if err := t.renumberStages(companyID, slices.Delete(stages, i, i+1)); err != nil {
			return err
		}
		if err := t.touchPipeline(p); err != nil {
			return err
		}
		return t.activity(actor, companyID, "pipeline.stage_deleted", "pipeline", p.ID, map[string]any{"stage": cur.Key}, "")
	})
	if err != nil {
		return PipelineView{}, err
	}
	return s.GetPipeline(ctx, companyID, pipelineID.String())
}

// ReorderStages põe as etapas na ordem das keys dadas (todas, uma vez cada) (admin).
func (s *Service) ReorderStages(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, keys []string) (PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return PipelineView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return PipelineView{}, fmt.Errorf("pipelines are designed by humans: %w", domain.ErrNotAllowed)
	}
	err := s.withTx(ctx, func(t *txc) error {
		p, err := t.lockPipeline(companyID, pipelineID)
		if err != nil {
			return err
		}
		stages, err := t.stagesOf(companyID, p.ID)
		if err != nil {
			return err
		}
		if len(keys) != len(stages) {
			return domain.Invalid("stages", "list every stage exactly once")
		}
		ordered := make([]domain.Stage, 0, len(stages))
		seen := map[string]bool{}
		for _, k := range keys {
			i := stageRefIndex(stages, strings.TrimSpace(k))
			if i < 0 || seen[stages[i].Key] {
				return domain.Invalid("stages", "list every stage exactly once")
			}
			seen[stages[i].Key] = true
			ordered = append(ordered, stages[i])
		}
		if err := t.renumberStages(companyID, ordered); err != nil {
			return err
		}
		if err := t.touchPipeline(p); err != nil {
			return err
		}
		return t.activity(actor, companyID, "pipeline.stages_reordered", "pipeline", p.ID, map[string]any{"stages": keys}, "")
	})
	if err != nil {
		return PipelineView{}, err
	}
	return s.GetPipeline(ctx, companyID, pipelineID.String())
}

// SetTransitions troca as arestas permitidas do pipeline e o modo estrito (admin).
func (s *Service) SetTransitions(ctx context.Context, actor domain.Actor, companyID, pipelineID domain.ID, specs []TransitionSpec, enforce bool) (PipelineView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return PipelineView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return PipelineView{}, fmt.Errorf("pipelines are designed by humans: %w", domain.ErrNotAllowed)
	}
	if len(specs) > maxTransitions {
		return PipelineView{}, domain.Invalid("transitions", fmt.Sprintf("at most %d transitions", maxTransitions))
	}
	err := s.withTx(ctx, func(t *txc) error {
		p, err := t.lockPipeline(companyID, pipelineID)
		if err != nil {
			return err
		}
		stages, err := t.stagesOf(companyID, p.ID)
		if err != nil {
			return err
		}
		type edge struct{ from, to domain.ID }
		seen := map[edge]bool{}
		if err := t.q.DeleteTransitions(ctx, store.DeleteTransitionsParams{CompanyID: companyID, PipelineID: p.ID}); err != nil {
			return fmt.Errorf("delete transitions: %w", err)
		}
		for _, sp := range specs {
			fi, ti := stageRefIndex(stages, strings.TrimSpace(sp.From)), stageRefIndex(stages, strings.TrimSpace(sp.To))
			if fi < 0 || ti < 0 {
				return domain.Invalid("transitions", fmt.Sprintf("unknown stage in %s → %s", sp.From, sp.To))
			}
			if fi == ti {
				return domain.Invalid("transitions", "a transition goes to another stage")
			}
			label := strings.TrimSpace(sp.Label)
			if utf8.RuneCountInString(label) > maxTransitionLabelLen {
				return domain.Invalid("transitions.label", fmt.Sprintf("too long (max %d)", maxTransitionLabelLen))
			}
			e := edge{stages[fi].ID, stages[ti].ID}
			if seen[e] {
				continue
			}
			seen[e] = true
			if err := t.q.CreateTransition(ctx, store.CreateTransitionParams{ID: domain.NewID(), CompanyID: companyID, PipelineID: p.ID,
				FromStageID: e.from, ToStageID: e.to, Label: label}); err != nil {
				return mapErr(err, "create transition")
			}
		}
		p.EnforceTransitions = enforce
		if err := t.touchPipeline(p); err != nil {
			return err
		}
		return t.activity(actor, companyID, "pipeline.transitions_set", "pipeline", p.ID, map[string]any{"transitions": len(seen), "enforce": enforce}, "")
	})
	if err != nil {
		return PipelineView{}, err
	}
	return s.GetPipeline(ctx, companyID, pipelineID.String())
}
