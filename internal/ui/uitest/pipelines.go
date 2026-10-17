package uitest

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Pipes estende o Fake com pipelines, cases, fila de revisão, aprendizados e exemplos (ADR-0034). As regras de
// verdade (aprovação, bloqueios, modo estrito, campos) ficam no service; aqui só o que as telas precisam: versões,
// movimentação, destinos da revisão, papéis (viewer não escreve, config é de admin) e os dados de exemplo.
type Pipes struct {
	*Fake
	pmu       sync.Mutex
	Pipelines []service.PipelineView
	Cases     []service.CaseView
	Examples  []domain.DecisionExample
	// PipesErr injeta uma falha por método (ex.: PipesErr["TransitionCase"] = domain.ErrCaseBlocked).
	PipesErr map[string]error
	nextCase int64
}

// WithPipelines devolve o Fake com um pipeline de conteúdo e alguns cases.
func WithPipelines(f *Fake) *Pipes {
	p := &Pipes{Fake: f, PipesErr: map[string]error{}}
	now := Now()
	co := f.Companies[0]
	var sato *domain.ID
	for _, ps := range f.Personas {
		if ps.Slug == "sato" {
			id := ps.ID
			sato = &id
		}
	}
	st := func(key, name string, pos int, kind domain.StageKind, cfg domain.StageConfig) domain.Stage {
		return domain.Stage{ID: ID("stage:" + key), PipelineID: ID("pipeline:conteudo"), Key: key, Name: name, Position: pos, Kind: kind, Config: cfg, CreatedAt: now, UpdatedAt: now}
	}
	pv := service.PipelineView{Pipeline: domain.Pipeline{ID: ID("pipeline:conteudo"), CompanyID: co.ID, Key: "conteudo", Name: "Conteúdo",
		Description: "Posts do LinkedIn, do rascunho à publicação.", CreatedBy: domain.HumanActor, CreatedAt: now, UpdatedAt: now},
		Stages: []domain.Stage{
			st("rascunho", "Rascunho", 0, domain.StageWorking, domain.StageConfig{WhatHappensHere: "A persona escreve o post.",
				Fields:     []domain.StageField{{Key: "tema", Label: "Tema", Type: domain.FieldText, Required: true, ShowInAddForm: true}, {Key: "formato", Label: "Formato", Type: domain.FieldSelect, Options: []string{"post", "carrossel"}, ShowInAddForm: true}},
				Automation: &domain.StageAutomation{PersonaID: sato, Instructions: "Escreva no tom da casa."}}),
			st("revisao", "Revisão", 1, domain.StageReview, domain.StageConfig{WhatHappensHere: "Um humano lê antes de publicar.",
				Review: &domain.ReviewRouting{ApproveTo: "publicado", RejectTo: "descartado", RequestChangesTo: "rascunho"}}),
			st("publicado", "Publicado", 2, domain.StageDone, domain.StageConfig{}),
			st("descartado", "Descartado", 3, domain.StageCancelled, domain.StageConfig{}),
		}}
	pv.Transitions = []domain.Transition{{ID: ID("tr:1"), From: pv.Stages[0].ID, To: pv.Stages[1].ID}}
	p.Pipelines = []service.PipelineView{pv}
	mk := func(n int64, title string, stage int, fields map[string]any) service.CaseView {
		c := service.CaseView{Case: domain.Case{ID: ID(fmt.Sprintf("case:%d", n)), CompanyID: co.ID, Number: n, Identifier: fmt.Sprintf("HNK-C%d", n), Type: "post",
			Title: title, Fields: fields, Version: 1, CreatedBy: domain.HumanActor, CreatedAt: now.Add(-time.Duration(n) * time.Hour), UpdatedAt: now.Add(-time.Duration(n) * time.Minute)}}
		if stage >= 0 {
			s := pv.Stages[stage]
			c.PipelineID, c.StageID, c.PipelineKey, c.PipelineName, c.StageKey, c.StageName, c.StageKind = &pv.ID, &s.ID, pv.Key, pv.Name, s.Key, s.Name, s.Kind
			c.Status = domain.StatusForStage(s.Kind)
		} else {
			c.Status = domain.CaseDraft
		}
		return c
	}
	c1 := mk(1, "Por que o heartbeat não é cron", 0, map[string]any{"tema": "heartbeat", "formato": "post"})
	c1.PendingSuggestion = &domain.CaseSuggestion{ID: ID("sg:1"), ToStage: "revisao", Rationale: "Rascunho pronto, 1.200 caracteres.", Confidence: 0.82, PersonaID: sato, At: now.Add(-20 * time.Minute)}
	c1.Summary = "Rascunho com **três** argumentos."
	c2 := mk(2, "Gates como contrato", 1, map[string]any{"tema": "gates"})
	c3 := mk(3, "Série sobre agentes", -1, map[string]any{})
	c3.ChildCount = 1
	c4 := mk(4, "Episódio 1: personas", -1, map[string]any{})
	c4.ParentCaseID, c4.ParentIdentifier, c4.ParentTitle = &c3.ID, c3.Identifier, c3.Title
	c2.Events = []domain.CaseEvent{{ID: ID("ev:2"), CaseID: c2.ID, Kind: domain.CaseEvTransitioned, Actor: domain.HumanActor, CreatedAt: now.Add(-time.Hour),
		Payload: map[string]any{"from_name": "Rascunho", "to_name": "Revisão"}}}
	p.Cases = []service.CaseView{c1, c2, c3, c4}
	p.nextCase = 4
	return p
}

func (p *Pipes) perr(name string) error { return p.PipesErr[name] }

func (p *Pipes) need(ctx context.Context, min domain.MemberRole) error {
	r, _ := p.MyRole(ctx, domain.ID{})
	if !r.AtLeast(min) {
		return domain.ErrForbidden
	}
	return nil
}

func (p *Pipes) pipeIdx(ref string) int {
	for i, v := range p.Pipelines {
		if v.Key == ref || v.ID.String() == ref {
			return i
		}
	}
	return -1
}

func (p *Pipes) caseIdx(ref string) int {
	for i, c := range p.Cases {
		if c.Identifier == ref || c.ID.String() == ref || fmt.Sprintf("C%d", c.Number) == ref {
			return i
		}
	}
	return -1
}

func (p *Pipes) recount() {
	for i := range p.Pipelines {
		open, rev, total := 0, 0, 0
		for _, c := range p.Cases {
			if c.PipelineID == nil || *c.PipelineID != p.Pipelines[i].ID {
				continue
			}
			total++
			if !c.Status.Terminal() {
				open++
				if c.StageKind == domain.StageReview {
					rev++
				}
			}
		}
		p.Pipelines[i].OpenCases, p.Pipelines[i].InReview, p.Pipelines[i].TotalCases = open, rev, total
	}
}

func (p *Pipes) CreatePipeline(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreatePipelineInput) (service.PipelineView, error) {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return service.PipelineView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	if err := p.perr("CreatePipeline"); err != nil {
		return service.PipelineView{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return service.PipelineView{}, domain.Invalid("name", "required")
	}
	key := in.Key
	if key == "" {
		key = domain.Slugify(in.Name)
	}
	if p.pipeIdx(key) >= 0 {
		return service.PipelineView{}, domain.ErrConflict
	}
	v := service.PipelineView{Pipeline: domain.Pipeline{ID: domain.NewID(), CompanyID: companyID, Key: key, Name: in.Name, Description: in.Description,
		CreatedBy: actor, CreatedAt: Now(), UpdatedAt: Now()}}
	for i, s := range in.Stages {
		k := s.Key
		if k == "" {
			k = domain.Slugify(s.Name)
		}
		v.Stages = append(v.Stages, domain.Stage{ID: domain.NewID(), PipelineID: v.ID, Key: k, Name: s.Name, Position: i, Kind: s.Kind, Config: s.Config})
	}
	p.Pipelines = append(p.Pipelines, v)
	p.note("CreatePipeline")
	return v, nil
}

func (p *Pipes) UpdatePipeline(ctx context.Context, _ domain.Actor, _, id domain.ID, in service.UpdatePipelineInput) (service.PipelineView, error) {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return service.PipelineView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.pipeIdx(id.String())
	if i < 0 {
		return service.PipelineView{}, domain.ErrNotFound
	}
	v := &p.Pipelines[i]
	if in.Name != nil {
		v.Name = *in.Name
	}
	if in.Key != nil {
		v.Key = *in.Key
	}
	if in.Description != nil {
		v.Description = *in.Description
	}
	if in.Archived != nil {
		if *in.Archived {
			t := Now()
			v.ArchivedAt = &t
		} else {
			v.ArchivedAt = nil
		}
	}
	p.note("UpdatePipeline")
	return *v, nil
}

func (p *Pipes) DeletePipeline(ctx context.Context, _ domain.Actor, _, id domain.ID) error {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.pipeIdx(id.String())
	if i < 0 {
		return domain.ErrNotFound
	}
	if p.Pipelines[i].TotalCases > 0 {
		return domain.ErrConflict
	}
	p.Pipelines = append(p.Pipelines[:i], p.Pipelines[i+1:]...)
	return nil
}

func (p *Pipes) GetPipeline(_ context.Context, _ domain.ID, ref string) (service.PipelineView, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	p.recount()
	i := p.pipeIdx(ref)
	if i < 0 {
		return service.PipelineView{}, domain.ErrNotFound
	}
	return p.Pipelines[i], nil
}

func (p *Pipes) ListPipelines(_ context.Context, _ domain.ID, arch bool) ([]service.PipelineView, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	p.recount()
	var out []service.PipelineView
	for _, v := range p.Pipelines {
		if v.ArchivedAt == nil || arch {
			out = append(out, v)
		}
	}
	return out, p.perr("ListPipelines")
}

func (p *Pipes) AddStage(ctx context.Context, _ domain.Actor, _, id domain.ID, in service.StageInput, _ *int) (service.PipelineView, error) {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return service.PipelineView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.pipeIdx(id.String())
	if i < 0 {
		return service.PipelineView{}, domain.ErrNotFound
	}
	k := in.Key
	if k == "" {
		k = domain.Slugify(in.Name)
	}
	v := &p.Pipelines[i]
	v.Stages = append(v.Stages, domain.Stage{ID: domain.NewID(), PipelineID: v.ID, Key: k, Name: in.Name, Position: len(v.Stages), Kind: in.Kind, Config: in.Config})
	p.note("AddStage")
	return *v, nil
}

func (p *Pipes) UpdateStage(ctx context.Context, _ domain.Actor, _, id domain.ID, ref string, in service.UpdateStageInput) (service.PipelineView, error) {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return service.PipelineView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.pipeIdx(id.String())
	if i < 0 {
		return service.PipelineView{}, domain.ErrNotFound
	}
	if err := p.perr("UpdateStage"); err != nil {
		return service.PipelineView{}, err
	}
	v := &p.Pipelines[i]
	for j := range v.Stages {
		if v.Stages[j].Key != ref {
			continue
		}
		if in.Name != nil {
			v.Stages[j].Name = *in.Name
		}
		if in.Config != nil {
			v.Stages[j].Config = *in.Config
		}
		if in.Kind != nil {
			v.Stages[j].Kind = *in.Kind
		}
		p.note("UpdateStage")
		return *v, nil
	}
	return service.PipelineView{}, domain.ErrNotFound
}

func (p *Pipes) DeleteStage(ctx context.Context, _ domain.Actor, _, id domain.ID, ref string) (service.PipelineView, error) {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return service.PipelineView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.pipeIdx(id.String())
	if i < 0 {
		return service.PipelineView{}, domain.ErrNotFound
	}
	v := &p.Pipelines[i]
	for j := range v.Stages {
		if v.Stages[j].Key == ref {
			v.Stages = append(v.Stages[:j], v.Stages[j+1:]...)
			return *v, nil
		}
	}
	return service.PipelineView{}, domain.ErrNotFound
}

func (p *Pipes) ReorderStages(ctx context.Context, _ domain.Actor, _, id domain.ID, keys []string) (service.PipelineView, error) {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return service.PipelineView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.pipeIdx(id.String())
	if i < 0 {
		return service.PipelineView{}, domain.ErrNotFound
	}
	v := &p.Pipelines[i]
	var out []domain.Stage
	for n, k := range keys {
		for _, s := range v.Stages {
			if s.Key == k {
				s.Position = n
				out = append(out, s)
			}
		}
	}
	v.Stages = out
	p.note("ReorderStages")
	return *v, nil
}

func (p *Pipes) SetTransitions(ctx context.Context, _ domain.Actor, _, id domain.ID, specs []service.TransitionSpec, enforce bool) (service.PipelineView, error) {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return service.PipelineView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.pipeIdx(id.String())
	if i < 0 {
		return service.PipelineView{}, domain.ErrNotFound
	}
	v := &p.Pipelines[i]
	v.Transitions = nil
	for _, sp := range specs {
		f, okF := v.Stage(sp.From)
		t, okT := v.Stage(sp.To)
		if okF && okT {
			v.Transitions = append(v.Transitions, domain.Transition{ID: domain.NewID(), From: f.ID, To: t.ID})
		}
	}
	v.EnforceTransitions = enforce
	p.note("SetTransitions")
	return *v, nil
}

func (p *Pipes) ListPipelineEvents(_ context.Context, _, id domain.ID, _ int) ([]service.PipelineEvent, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	var out []service.PipelineEvent
	for _, c := range p.Cases {
		if c.PipelineID != nil && *c.PipelineID == id {
			for _, e := range c.Events {
				out = append(out, service.PipelineEvent{CaseEvent: e, CaseIdentifier: c.Identifier, CaseTitle: c.Title})
			}
		}
	}
	return out, nil
}

func (p *Pipes) CreateCase(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateCaseInput) (service.CaseView, bool, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return service.CaseView{}, false, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	if err := p.perr("CreateCase"); err != nil {
		return service.CaseView{}, false, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return service.CaseView{}, false, domain.Invalid("title", "must have 1-300 characters")
	}
	c := service.CaseView{Case: domain.Case{ID: domain.NewID(), CompanyID: companyID, Number: p.nextCase + 1, Identifier: fmt.Sprintf("HNK-C%d", p.nextCase+1),
		Type: in.Type, Key: in.Key, Title: in.Title, Summary: in.Summary, Fields: in.Fields, Status: in.Status, Version: 1, CreatedBy: actor, CreatedAt: Now(), UpdatedAt: Now()}}
	if c.Fields == nil {
		c.Fields = map[string]any{}
	}
	if c.Status == "" {
		c.Status = domain.CaseDraft
	}
	if in.PipelineID != nil {
		i := p.pipeIdx(in.PipelineID.String())
		if i < 0 {
			return service.CaseView{}, false, domain.ErrNotFound
		}
		v := p.Pipelines[i]
		st := v.Stages[0]
		for _, f := range st.Config.Fields {
			if f.Required && f.ShowInAddForm && fmt.Sprint(c.Fields[f.Key]) == "" || f.Required && f.ShowInAddForm && c.Fields[f.Key] == nil {
				return service.CaseView{}, false, domain.Invalid("fields."+f.Key, fmt.Sprintf("%q is required in stage %s", f.Label, st.Name))
			}
		}
		c.PipelineID, c.StageID, c.PipelineKey, c.PipelineName, c.StageKey, c.StageName, c.StageKind = &v.ID, &st.ID, v.Key, v.Name, st.Key, st.Name, st.Kind
		c.Status = domain.StatusForStage(st.Kind)
	}
	c.Events = []domain.CaseEvent{{ID: domain.NewID(), CaseID: c.ID, Kind: domain.CaseEvCreated, Actor: actor, CreatedAt: Now()}}
	p.nextCase++
	p.Cases = append(p.Cases, c)
	p.note("CreateCase")
	return c, true, nil
}

func (p *Pipes) bump(i int, actor domain.Actor, kind domain.CaseEventKind, payload map[string]any) {
	c := &p.Cases[i]
	c.Version++
	c.UpdatedAt = Now()
	c.Events = append(c.Events, domain.CaseEvent{ID: domain.NewID(), CaseID: c.ID, Kind: kind, Actor: actor, Payload: payload, CreatedAt: Now()})
}

func (p *Pipes) checkVer(i, v int) error {
	if p.Cases[i].Version != v {
		return &domain.VersionConflictError{Current: p.Cases[i].Version}
	}
	return nil
}

func (p *Pipes) UpdateCase(ctx context.Context, actor domain.Actor, _, id domain.ID, in service.UpdateCaseInput) (service.CaseView, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return service.CaseView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.caseIdx(id.String())
	if i < 0 {
		return service.CaseView{}, domain.ErrNotFound
	}
	if err := p.checkVer(i, in.ExpectedVersion); err != nil {
		return service.CaseView{}, err
	}
	c := &p.Cases[i]
	if in.Title != nil {
		c.Title = *in.Title
	}
	if in.Summary != nil {
		c.Summary = *in.Summary
	}
	if in.ReplaceFields {
		c.Fields = map[string]any{}
	}
	for k, v := range in.Fields {
		c.Fields[k] = v
	}
	if in.Status != nil {
		c.Status = *in.Status
	}
	if in.ClearAttention {
		c.Attention = ""
	}
	p.bump(i, actor, domain.CaseEvUpdated, nil)
	p.note("UpdateCase")
	return *c, nil
}

func (p *Pipes) GetCaseView(_ context.Context, _ domain.ID, ref string) (service.CaseView, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.caseIdx(ref)
	if i < 0 {
		return service.CaseView{}, domain.ErrNotFound
	}
	return p.Cases[i], p.perr("GetCaseView")
}

func (p *Pipes) ListCases(_ context.Context, _ domain.ID, f service.CaseFilter) ([]service.CaseView, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	var out []service.CaseView
	for _, c := range p.Cases {
		switch {
		case f.PipelineID != nil && (c.PipelineID == nil || *c.PipelineID != *f.PipelineID):
		case f.LooseOnly && c.PipelineID != nil:
		case f.ParentID != nil && (c.ParentCaseID == nil || *c.ParentCaseID != *f.ParentID):
		case f.Type != "" && c.Type != f.Type:
		case f.Q != "" && !strings.Contains(strings.ToLower(c.Title), strings.ToLower(f.Q)):
		case len(f.Statuses) > 0 && c.Status != f.Statuses[0]:
		default:
			out = append(out, c)
		}
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, p.perr("ListCases")
}

func (p *Pipes) move(i int, actor domain.Actor, to domain.Stage, kind domain.CaseEventKind, payload map[string]any) {
	c := &p.Cases[i]
	if payload == nil {
		payload = map[string]any{}
	}
	payload["from_name"], payload["to_name"], payload["to"] = c.StageName, to.Name, to.Key
	c.StageID, c.StageKey, c.StageName, c.StageKind = &to.ID, to.Key, to.Name, to.Kind
	c.Status = domain.StatusForStage(to.Kind)
	c.PendingSuggestion, c.Attention = nil, ""
	p.bump(i, actor, kind, payload)
}

func (p *Pipes) TransitionCase(ctx context.Context, actor domain.Actor, _, id domain.ID, in service.TransitionCaseInput) (service.CaseView, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return service.CaseView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.caseIdx(id.String())
	if i < 0 {
		return service.CaseView{}, domain.ErrNotFound
	}
	if err := p.perr("TransitionCase"); err != nil {
		return service.CaseView{}, err
	}
	if err := p.checkVer(i, in.ExpectedVersion); err != nil {
		return service.CaseView{}, err
	}
	c := p.Cases[i]
	if c.PipelineID == nil {
		return service.CaseView{}, domain.Invalid("case", "a loose case changes status, not stage")
	}
	v := p.Pipelines[p.pipeIdx(c.PipelineID.String())]
	to, ok := v.Stage(in.ToStage)
	if !ok {
		return service.CaseView{}, domain.Invalid("to_stage", "unknown stage "+in.ToStage)
	}
	if c.StageKind == domain.StageReview {
		return service.CaseView{}, domain.Invalid("to_stage", "the case is in review: approve, request changes or reject it")
	}
	p.move(i, actor, to, domain.CaseEvTransitioned, map[string]any{"reason": in.Reason})
	p.note("TransitionCase")
	return p.Cases[i], nil
}

func (p *Pipes) ResolveSuggestion(ctx context.Context, actor domain.Actor, _, id domain.ID, in service.ResolveSuggestionInput) (service.CaseView, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return service.CaseView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.caseIdx(id.String())
	if i < 0 {
		return service.CaseView{}, domain.ErrNotFound
	}
	if err := p.checkVer(i, in.ExpectedVersion); err != nil {
		return service.CaseView{}, err
	}
	c := p.Cases[i]
	if c.PendingSuggestion == nil {
		return service.CaseView{}, domain.Invalid("suggestion", "the case has no pending suggestion")
	}
	if in.Accept {
		v := p.Pipelines[p.pipeIdx(c.PipelineID.String())]
		to, _ := v.Stage(c.PendingSuggestion.ToStage)
		p.move(i, actor, to, domain.CaseEvTransitioned, nil)
	} else {
		p.Cases[i].PendingSuggestion = nil
		p.bump(i, actor, domain.CaseEvSuggestionResolved, map[string]any{"resolution": "dismissed"})
	}
	p.note("ResolveSuggestion")
	return p.Cases[i], nil
}

func (p *Pipes) ReviewCase(ctx context.Context, actor domain.Actor, _, id domain.ID, in service.ReviewCaseInput) (service.CaseView, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return service.CaseView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	return p.reviewLocked(actor, id, in)
}

func (p *Pipes) reviewLocked(actor domain.Actor, id domain.ID, in service.ReviewCaseInput) (service.CaseView, error) {
	i := p.caseIdx(id.String())
	if i < 0 {
		return service.CaseView{}, domain.ErrNotFound
	}
	if err := p.checkVer(i, in.ExpectedVersion); err != nil {
		return service.CaseView{}, err
	}
	c := p.Cases[i]
	if c.StageKind != domain.StageReview {
		return service.CaseView{}, domain.Invalid("case", "the case is not in a review stage")
	}
	v := p.Pipelines[p.pipeIdx(c.PipelineID.String())]
	from, _ := v.Stage(c.StageKey)
	r := from.Config.Review
	dest := r.ApproveTo
	switch in.Decision {
	case domain.ReviewReject:
		if strings.TrimSpace(in.Reason) == "" {
			return service.CaseView{}, domain.Invalid("reason", "rejecting needs a reason")
		}
		dest = r.RejectTo
	case domain.ReviewRequestChanges:
		if strings.TrimSpace(in.Reason) == "" {
			return service.CaseView{}, domain.Invalid("reason", "asking for changes needs a reason")
		}
		dest = r.RequestChangesTo
	}
	to, _ := v.Stage(dest)
	p.move(i, actor, to, domain.CaseEvReviewDecided, map[string]any{"decision": string(in.Decision), "reason": in.Reason})
	p.note("ReviewCase")
	return p.Cases[i], nil
}

func (p *Pipes) BulkApprove(ctx context.Context, actor domain.Actor, _ domain.ID, items []service.BulkReviewItem) ([]service.BulkReviewResult, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return nil, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	var out []service.BulkReviewResult
	for _, it := range items {
		v, err := p.reviewLocked(actor, it.CaseID, service.ReviewCaseInput{ExpectedVersion: it.ExpectedVersion, Decision: domain.ReviewApprove, Reason: it.Reason})
		r := service.BulkReviewResult{CaseID: it.CaseID, OK: err == nil, Version: v.Version, Err: err}
		if err != nil {
			r.Error = err.Error()
			if cur, ok := domain.CurrentVersion(err); ok {
				r.CurrentVersion = cur
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (p *Pipes) LinkCaseTask(ctx context.Context, actor domain.Actor, _, caseID, taskID domain.ID, role domain.CaseLinkRole, _ *domain.ID) (service.CaseView, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return service.CaseView{}, err
	}
	tv, err := p.GetTask(ctx, domain.ID{}, taskID.String())
	if err != nil {
		return service.CaseView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.caseIdx(caseID.String())
	if i < 0 {
		return service.CaseView{}, domain.ErrNotFound
	}
	p.Cases[i].Links = append(p.Cases[i].Links, service.CaseLink{TaskID: tv.ID, TaskKey: tv.Key, TaskTitle: tv.Title, TaskStatus: tv.Status, Role: role, CreatedAt: Now()})
	p.note("LinkCaseTask")
	return p.Cases[i], nil
}

func (p *Pipes) UnlinkCaseTask(ctx context.Context, _ domain.Actor, _, caseID, taskID domain.ID) (service.CaseView, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return service.CaseView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.caseIdx(caseID.String())
	if i < 0 {
		return service.CaseView{}, domain.ErrNotFound
	}
	var keep []service.CaseLink
	for _, l := range p.Cases[i].Links {
		if l.TaskID != taskID {
			keep = append(keep, l)
		}
	}
	p.Cases[i].Links = keep
	return p.Cases[i], nil
}

func (p *Pipes) SetCaseBlockers(ctx context.Context, actor domain.Actor, _, caseID domain.ID, ver int, blockers []domain.ID, _ *domain.ID) (service.CaseView, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return service.CaseView{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	i := p.caseIdx(caseID.String())
	if i < 0 {
		return service.CaseView{}, domain.ErrNotFound
	}
	if err := p.checkVer(i, ver); err != nil {
		return service.CaseView{}, err
	}
	p.Cases[i].Blockers = nil
	for _, b := range blockers {
		if j := p.caseIdx(b.String()); j >= 0 {
			p.Cases[i].Blockers = append(p.Cases[i].Blockers, service.CaseRef{ID: b, Identifier: p.Cases[j].Identifier, Title: p.Cases[j].Title})
		}
	}
	p.bump(i, actor, domain.CaseEvBlockersSet, nil)
	return p.Cases[i], nil
}

func (p *Pipes) OpenCaseConversation(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID) (service.TaskView, error) {
	c, err := p.GetCaseView(ctx, companyID, caseID.String())
	if err != nil {
		return service.TaskView{}, err
	}
	tv, err := p.CreateTask(ctx, actor, companyID, service.CreateTaskInput{Title: "Discutir: " + c.Title})
	if err != nil {
		return service.TaskView{}, err
	}
	_, err = p.LinkCaseTask(ctx, actor, companyID, caseID, tv.ID, domain.LinkConversation, nil)
	return tv, err
}

func (p *Pipes) CasesForTask(_ context.Context, _, taskID domain.ID) ([]service.CaseForTask, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	var out []service.CaseForTask
	for _, c := range p.Cases {
		for _, l := range c.Links {
			if l.TaskID == taskID {
				out = append(out, service.CaseForTask{CaseRef: service.CaseRef{ID: c.ID, Identifier: c.Identifier, Title: c.Title, Status: c.Status}, Role: l.Role,
					PipelineName: c.PipelineName, StageName: c.StageName})
			}
		}
	}
	return out, nil
}

func (p *Pipes) ReviewQueue(_ context.Context, _ domain.ID) ([]service.ReviewItem, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	var out []service.ReviewItem
	for _, c := range p.Cases {
		if c.Status.Terminal() {
			continue
		}
		if c.StageKind == domain.StageReview {
			out = append(out, service.ReviewItem{Kind: "review", Case: c, Since: c.UpdatedAt, ApproveTo: "publicado", ApproveToName: "Publicado", RejectTo: "descartado",
				RejectToName: "Descartado", RequestChangesTo: "rascunho", RequestChangesToName: "Rascunho", RequireRejectReason: true, RequireChangesReason: true})
		}
		if sg := c.PendingSuggestion; sg != nil {
			out = append(out, service.ReviewItem{Kind: "suggestion", Case: c, Since: sg.At, Reason: sg.Rationale, Suggestion: sg, SuggestedStageName: "Revisão"})
		}
		if c.Attention != "" {
			out = append(out, service.ReviewItem{Kind: "attention", Case: c, Since: c.UpdatedAt, Reason: c.Attention})
		}
	}
	return out, nil
}

func (p *Pipes) ReviewQueueCount(ctx context.Context, companyID domain.ID) (int, error) {
	items, err := p.ReviewQueue(ctx, companyID)
	return len(items), err
}

func (p *Pipes) ListLearnings(_ context.Context, _ domain.ID, f service.LearningFilter) ([]service.Learning, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	var out []service.Learning
	for _, c := range p.Cases {
		for _, e := range c.Events {
			if e.Kind != domain.CaseEvReviewDecided && e.Kind != domain.CaseEvTransitionForced {
				continue
			}
			d, _ := e.Payload["decision"].(string)
			if f.Decision != "" && f.Decision != d {
				continue
			}
			reason, _ := e.Payload["reason"].(string)
			to, _ := e.Payload["to_name"].(string)
			from, _ := e.Payload["from_name"].(string)
			out = append(out, service.Learning{EventID: e.ID, Kind: string(e.Kind), Decision: d, Reason: reason, Actor: e.Actor, CaseID: c.ID,
				CaseIdentifier: c.Identifier, CaseTitle: c.Title, PipelineKey: c.PipelineKey, PipelineName: c.PipelineName, FromName: from, ToName: to, At: e.CreatedAt})
		}
	}
	return out, nil
}

func (p *Pipes) SaveDecisionExample(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.SaveDecisionExampleInput) (domain.DecisionExample, error) {
	if err := p.need(ctx, domain.MemberMember); err != nil {
		return domain.DecisionExample{}, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	for _, x := range p.Examples {
		if x.SourceID == in.SourceID && sameIDPtr(x.CreatedByUserID, actor.ID) {
			return domain.DecisionExample{}, domain.ErrConflict
		}
	}
	x := domain.DecisionExample{ID: domain.NewID(), CompanyID: companyID, SourceKind: in.SourceKind, SourceID: in.SourceID, CutoffAt: Now(), DecisionOutcome: "approve",
		Notes: in.Notes, Snapshot: map[string]any{}, CreatedByUserID: actor.ID, CreatedAt: Now(), UpdatedAt: Now()}
	p.Examples = append(p.Examples, x)
	p.note("SaveDecisionExample")
	return x, nil
}

func (p *Pipes) UpdateDecisionExampleNotes(_ context.Context, actor domain.Actor, _, id domain.ID, notes string) (domain.DecisionExample, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	for i, x := range p.Examples {
		if x.ID == id {
			if !sameIDPtr(x.CreatedByUserID, actor.ID) {
				return domain.DecisionExample{}, domain.ErrNotAllowed
			}
			p.Examples[i].Notes = notes
			return p.Examples[i], nil
		}
	}
	return domain.DecisionExample{}, domain.ErrNotFound
}

func (p *Pipes) DeleteDecisionExample(_ context.Context, actor domain.Actor, _, id domain.ID) error {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	for i, x := range p.Examples {
		if x.ID == id {
			if !sameIDPtr(x.CreatedByUserID, actor.ID) {
				return domain.ErrNotAllowed
			}
			p.Examples = append(p.Examples[:i], p.Examples[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (p *Pipes) ListDecisionExamples(_ context.Context, _ domain.ID, f service.DecisionExampleFilter) ([]domain.DecisionExample, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	var out []domain.DecisionExample
	for _, x := range p.Examples {
		if (f.SourceKind == "" || x.SourceKind == f.SourceKind) && (f.SourceID == nil || x.SourceID == *f.SourceID) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (p *Pipes) ExportDecisionExamples(ctx context.Context, _ domain.ID, w io.Writer) (int, error) {
	if err := p.need(ctx, domain.MemberAdmin); err != nil {
		return 0, err
	}
	p.pmu.Lock()
	defer p.pmu.Unlock()
	for _, x := range p.Examples {
		if _, err := fmt.Fprintf(w, "{\"id\":%q,\"notes\":%q}\n", x.ID, x.Notes); err != nil {
			return 0, err
		}
	}
	return len(p.Examples), nil
}
