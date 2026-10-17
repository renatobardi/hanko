package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cases e pipelines pelo MCP (ADR-0034). A persona cria e atualiza cases (o par tipo+key faz upsert), lê o case com
// o histórico e os aprendizados da etapa, move entre etapas de trabalho sem aprovação e sugere as demais
// movimentações; nunca decide revisão nem força transição. Toda escrita liga a task da execução ao case.

// caseBackend é o que as tools de case pedem ao service. Fica fora de Backend para que fakes que não usam cases
// continuem compilando; sem ele as tools respondem que o recurso está indisponível.
type caseBackend interface {
	CreateCase(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateCaseInput) (service.CaseView, bool, error)
	UpdateCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.UpdateCaseInput) (service.CaseView, error)
	GetCaseView(ctx context.Context, companyID domain.ID, ref string) (service.CaseView, error)
	ListCases(ctx context.Context, companyID domain.ID, f service.CaseFilter) ([]service.CaseView, error)
	TransitionCase(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.TransitionCaseInput) (service.CaseView, error)
	SuggestCaseTransition(ctx context.Context, actor domain.Actor, companyID, caseID domain.ID, in service.SuggestInput) (service.CaseView, error)
	CaseLearnings(ctx context.Context, companyID, caseID domain.ID) ([]service.StageLearning, error)
	GetPipeline(ctx context.Context, companyID domain.ID, ref string) (service.PipelineView, error)
	ListPipelines(ctx context.Context, companyID domain.ID, includeArchived bool) ([]service.PipelineView, error)
}

var _ caseBackend = (*service.Service)(nil)

const maxMCPCaseEvents = 30

type caseCreateIn struct {
	Company  string         `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Pipeline string         `json:"pipeline,omitempty" jsonschema:"key do pipeline (veja hanko_pipeline_list); vazio = case solto"`
	Stage    string         `json:"stage,omitempty" jsonschema:"key da etapa inicial (de trabalho); vazio = a primeira"`
	Title    string         `json:"title" jsonschema:"título curto do case (até 300 caracteres)"`
	Type     string         `json:"case_type,omitempty" jsonschema:"tipo livre, ex. lead, incidente, pedido"`
	Key      string         `json:"key,omitempty" jsonschema:"chave externa (e-mail, domínio, id do sistema de origem): o mesmo tipo+key atualiza o case em vez de duplicar"`
	Summary  string         `json:"summary,omitempty" jsonschema:"resumo em markdown (até 64 KiB)"`
	Fields   map[string]any `json:"fields,omitempty" jsonschema:"campos planos: texto, número ou true/false (os da etapa aparecem em hanko_pipeline_list)"`
	Parent   string         `json:"parent,omitempty" jsonschema:"identificador do case pai (ex. HNK-C3)"`
	Status   string         `json:"status,omitempty" jsonschema:"só para case solto: draft, in_progress, in_review, done ou cancelled"`
}

type caseRefIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Case    string `json:"case" jsonschema:"identificador do case (ex. HNK-C12), número ou id"`
}

type caseUpdateIn struct {
	Company         string         `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Case            string         `json:"case" jsonschema:"identificador do case (ex. HNK-C12)"`
	ExpectedVersion int            `json:"expected_version" jsonschema:"a versão do case que você leu (hanko_case_get); se mudou, releia e refaça"`
	Title           string         `json:"title,omitempty" jsonschema:"novo título"`
	Summary         *string        `json:"summary,omitempty" jsonschema:"novo resumo em markdown (substitui o atual)"`
	Fields          map[string]any `json:"fields,omitempty" jsonschema:"campos a gravar (merge; null apaga a chave)"`
}

type caseListIn struct {
	Company  string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Pipeline string `json:"pipeline,omitempty" jsonschema:"key do pipeline"`
	Stage    string `json:"stage,omitempty" jsonschema:"key da etapa (com pipeline)"`
	Type     string `json:"case_type,omitempty" jsonschema:"filtra pelo tipo"`
	Status   string `json:"status,omitempty" jsonschema:"status separados por vírgula"`
	Query    string `json:"query,omitempty" jsonschema:"busca no título, no resumo e na key"`
	Limit    int    `json:"limit,omitempty" jsonschema:"máximo de cases (padrão 50, máx 200)"`
}

type caseMoveIn struct {
	Company         string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Case            string `json:"case" jsonschema:"identificador do case (ex. HNK-C12)"`
	ToStage         string `json:"to_stage" jsonschema:"key da etapa de destino"`
	ExpectedVersion int    `json:"expected_version,omitempty" jsonschema:"a versão que você leu; vazio = a atual"`
}

type caseSuggestIn struct {
	Company         string  `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Case            string  `json:"case" jsonschema:"identificador do case (ex. HNK-C12)"`
	ToStage         string  `json:"to_stage" jsonschema:"key da etapa para onde o case deveria ir"`
	Rationale       string  `json:"rationale" jsonschema:"por que (evidências, o que foi feito); o humano lê isto na fila de revisão"`
	Confidence      float64 `json:"confidence" jsonschema:"confiança de 0 a 1"`
	ExpectedVersion int     `json:"expected_version,omitempty" jsonschema:"a versão que você leu; vazio = a atual"`
}

type pipelineListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
}

type caseFieldOut struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

type stageOut struct {
	Key             string         `json:"key"`
	Name            string         `json:"name"`
	Kind            string         `json:"kind" jsonschema:"working, review, done ou cancelled"`
	WhatHappensHere string         `json:"what_happens_here,omitempty"`
	Approval        bool           `json:"approval,omitempty" jsonschema:"sair exige aprovador: sugira em vez de mover"`
	Disabled        bool           `json:"disabled,omitempty"`
	Fields          []caseFieldOut `json:"fields,omitempty"`
}

type pipelineOut struct {
	Key    string     `json:"key"`
	Name   string     `json:"name"`
	Strict bool       `json:"strict,omitempty" jsonschema:"só as transições configuradas valem"`
	Stages []stageOut `json:"stages"`
}

type pipelineListOut struct {
	Pipelines []pipelineOut `json:"pipelines"`
}

type caseEventOut struct {
	Kind   string `json:"kind"`
	At     string `json:"at"`
	Actor  string `json:"actor"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type caseLearningOut struct {
	Case     string `json:"case"`
	Decision string `json:"decision"`
	ToStage  string `json:"to_stage,omitempty"`
	Reason   string `json:"reason"`
	At       string `json:"at"`
}

type caseOut struct {
	Identifier string            `json:"identifier"`
	Title      string            `json:"title"`
	Type       string            `json:"case_type,omitempty"`
	Key        string            `json:"key,omitempty"`
	Status     string            `json:"status"`
	Pipeline   string            `json:"pipeline,omitempty"`
	Stage      string            `json:"stage,omitempty"`
	StageKind  string            `json:"stage_kind,omitempty"`
	Version    int               `json:"version" jsonschema:"mande esta versão como expected_version na próxima escrita"`
	Summary    string            `json:"summary,omitempty"`
	Fields     map[string]any    `json:"fields"`
	Parent     string            `json:"parent,omitempty"`
	Suggestion string            `json:"pending_suggestion,omitempty"`
	Reapproval bool              `json:"reapproval_required,omitempty"`
	Tasks      []string          `json:"tasks,omitempty" jsonschema:"tasks ligadas (chave e papel)"`
	History    []caseEventOut    `json:"history,omitempty" jsonschema:"eventos mais recentes, com os motivos das revisões"`
	Learnings  []caseLearningOut `json:"learnings,omitempty" jsonschema:"o que as revisões desta etapa ensinaram: leve em conta antes de agir"`
}

type caseResult struct {
	Case    caseOut `json:"case"`
	Created *bool   `json:"created,omitempty"`
}

type caseListOut struct {
	Cases []caseOut `json:"cases"`
}

func registerCaseTools(s *sdk.Server, t *tools) {
	if _, ok := t.b.(caseBackend); !ok {
		return // backend sem cases (fakes de teste): as tools nem aparecem
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_case_create",
		Description: "Cria um case: um registro estruturado do trabalho (lead, incidente, pedido, candidato), solto ou num pipeline (na etapa inicial). " +
			"Com key, o mesmo tipo+key atualiza o case existente em vez de duplicar (use o id do sistema de origem, um e-mail, um domínio). " +
			"A task da sua execução fica ligada ao case.",
	}, t.caseCreate)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_case_get",
		Description: "Lê um case: campos, resumo, etapa, versão, tasks ligadas, o histórico com os motivos das revisões e os aprendizados da etapa " +
			"(o que os humanos aprovaram, recusaram ou pediram para mudar). Leia antes de escrever: a versão vai como expected_version.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.caseGet)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_case_update",
		Description: "Atualiza o resumo, o título ou os campos de um case (os campos fazem merge; null apaga). Exige expected_version: " +
			"se o case mudou desde a leitura, a escrita é recusada com a versão atual; releia e refaça.",
	}, t.caseUpdate)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_case_list",
		Description: "Lista cases (mais recentes primeiro), por pipeline, etapa, tipo, status ou busca.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.caseList)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_case_transition",
		Description: "Move um case entre etapas de trabalho (working) sem aprovação. Para ir à revisão, a uma etapa com aprovador " +
			"ou a qualquer decisão humana, use hanko_case_suggest. Campos obrigatórios da etapa precisam estar preenchidos.",
	}, t.caseTransition)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_case_suggest",
		Description: "Sugere mover o case para uma etapa, com a justificativa e a confiança (0 a 1). A sugestão vai para a fila de revisão " +
			"e um humano aceita ou dispensa. Use quando o próximo passo não é seu de decidir.",
	}, t.caseSuggest)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_pipeline_list",
		Description: "Lista os pipelines com as etapas (key, tipo, o que acontece em cada uma, se exige aprovação e os campos de entrada).",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.pipelineList)
}

func (t *tools) caseScope(ctx context.Context, company string) (caseBackend, identity, domain.ID, error) {
	cb, ok := t.b.(caseBackend)
	if !ok {
		return nil, identity{}, domain.ID{}, errors.New("cases indisponíveis neste servidor")
	}
	id, cid, err := t.scope(ctx, company)
	return cb, id, cid, err
}

func actorText(a domain.Actor) string {
	switch a.Type {
	case domain.ActorHuman:
		return "humano"
	case domain.ActorPersona:
		return "persona"
	}
	return "sistema"
}

func toCaseOut(v service.CaseView, learnings []service.StageLearning) caseOut {
	o := caseOut{Identifier: v.Identifier, Title: v.Title, Type: v.Type, Key: v.Key, Status: string(v.Status), Pipeline: v.PipelineKey, Stage: v.StageKey,
		StageKind: string(v.StageKind), Version: v.Version, Summary: v.Summary, Fields: v.Fields, Parent: v.ParentIdentifier, Reapproval: v.ReapprovalRequired}
	if o.Fields == nil {
		o.Fields = map[string]any{}
	}
	if sg := v.PendingSuggestion; sg != nil {
		o.Suggestion = fmt.Sprintf("%s (%.0f%%): %s", sg.ToStage, sg.Confidence*100, sg.Rationale)
	}
	for _, l := range v.Links {
		o.Tasks = append(o.Tasks, l.TaskKey+" ("+string(l.Role)+")")
	}
	start := max(len(v.Events)-maxMCPCaseEvents, 0)
	for _, e := range v.Events[start:] {
		ev := caseEventOut{Kind: string(e.Kind), At: e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), Actor: actorText(e.Actor)}
		ev.To, _ = e.Payload["to"].(string)
		if r, _ := e.Payload["reason"].(string); r != "" {
			ev.Reason = r
		} else if r, _ := e.Payload["rationale"].(string); r != "" {
			ev.Reason = r
		}
		if d, _ := e.Payload["decision"].(string); d != "" {
			ev.Kind += ":" + d
		}
		o.History = append(o.History, ev)
	}
	for _, l := range learnings {
		dec := l.Decision
		if dec == "" {
			dec = l.Kind
		}
		o.Learnings = append(o.Learnings, caseLearningOut{Case: l.CaseIdentifier, Decision: dec, ToStage: l.ToStage, Reason: l.Reason, At: l.At})
	}
	return o
}

func (t *tools) caseCreate(ctx context.Context, _ *sdk.CallToolRequest, in caseCreateIn) (*sdk.CallToolResult, caseResult, error) {
	cb, id, cid, err := t.caseScope(ctx, in.Company)
	if err != nil {
		return nil, caseResult{}, err
	}
	ci := service.CreateCaseInput{Title: in.Title, Type: in.Type, Key: in.Key, Summary: in.Summary, Fields: in.Fields, StageKey: in.Stage,
		Status: domain.CaseStatus(strings.TrimSpace(in.Status)), RunID: id.RunID, TaskID: id.TaskID}
	if p := strings.TrimSpace(in.Pipeline); p != "" {
		pv, err := cb.GetPipeline(ctx, cid, p)
		if err != nil {
			return nil, caseResult{}, toolErr("pipeline", p, err)
		}
		ci.PipelineID = &pv.ID
	}
	if p := strings.TrimSpace(in.Parent); p != "" {
		pc, err := cb.GetCaseView(ctx, cid, p)
		if err != nil {
			return nil, caseResult{}, toolErr("case pai", p, err)
		}
		ci.ParentID = &pc.ID
	}
	v, created, err := cb.CreateCase(ctx, id.Actor, cid, ci)
	if err != nil {
		return nil, caseResult{}, opErr("criar case", err)
	}
	verb := "criado"
	if !created {
		verb = "atualizado (mesmo tipo+key)"
	}
	return textResult("Case %s %s: %s (versão %d)", v.Identifier, verb, v.Title, v.Version), caseResult{Case: toCaseOut(v, nil), Created: &created}, nil
}

func (t *tools) caseGet(ctx context.Context, _ *sdk.CallToolRequest, in caseRefIn) (*sdk.CallToolResult, caseResult, error) {
	cb, _, cid, err := t.caseScope(ctx, in.Company)
	if err != nil {
		return nil, caseResult{}, err
	}
	ref := strings.TrimSpace(in.Case)
	if ref == "" {
		return nil, caseResult{}, errors.New("case (identificador, ex. HNK-C12) é obrigatório")
	}
	v, err := cb.GetCaseView(ctx, cid, ref)
	if err != nil {
		return nil, caseResult{}, toolErr("case", ref, err)
	}
	ls, err := cb.CaseLearnings(ctx, cid, v.ID)
	if err != nil {
		return nil, caseResult{}, opErr("ler aprendizados", err)
	}
	where := "solto"
	if v.PipelineKey != "" {
		where = v.PipelineName + " › " + v.StageName
	}
	return textResult("Case %s — %s (%s, %s, versão %d; %d evento(s), %d aprendizado(s))", v.Identifier, v.Title, where, v.Status, v.Version,
		len(v.Events), len(ls)), caseResult{Case: toCaseOut(v, ls)}, nil
}

func (t *tools) caseUpdate(ctx context.Context, _ *sdk.CallToolRequest, in caseUpdateIn) (*sdk.CallToolResult, caseResult, error) {
	cb, id, cid, err := t.caseScope(ctx, in.Company)
	if err != nil {
		return nil, caseResult{}, err
	}
	if in.ExpectedVersion <= 0 {
		return nil, caseResult{}, errors.New("expected_version é obrigatório: leia o case com hanko_case_get e mande a versão")
	}
	cur, err := cb.GetCaseView(ctx, cid, strings.TrimSpace(in.Case))
	if err != nil {
		return nil, caseResult{}, toolErr("case", in.Case, err)
	}
	up := service.UpdateCaseInput{ExpectedVersion: in.ExpectedVersion, Summary: in.Summary, Fields: in.Fields, RunID: id.RunID, TaskID: id.TaskID}
	if strings.TrimSpace(in.Title) != "" {
		up.Title = &in.Title
	}
	v, err := cb.UpdateCase(ctx, id.Actor, cid, cur.ID, up)
	if err != nil {
		if now, ok := domain.CurrentVersion(err); ok {
			return nil, caseResult{}, fmt.Errorf("o case mudou desde a sua leitura (versão atual %d): releia com hanko_case_get e refaça", now)
		}
		return nil, caseResult{}, opErr("atualizar case", err)
	}
	return textResult("Case %s atualizado (versão %d)", v.Identifier, v.Version), caseResult{Case: toCaseOut(v, nil)}, nil
}

func (t *tools) caseList(ctx context.Context, _ *sdk.CallToolRequest, in caseListIn) (*sdk.CallToolResult, caseListOut, error) {
	cb, _, cid, err := t.caseScope(ctx, in.Company)
	if err != nil {
		return nil, caseListOut{}, err
	}
	f := service.CaseFilter{Type: in.Type, Q: in.Query, Limit: min(max(in.Limit, 0), 200)}
	if f.Limit == 0 {
		f.Limit = 50
	}
	for _, st := range strings.Split(in.Status, ",") {
		if st = strings.TrimSpace(st); st != "" {
			f.Statuses = append(f.Statuses, domain.CaseStatus(st))
		}
	}
	if p := strings.TrimSpace(in.Pipeline); p != "" {
		pv, err := cb.GetPipeline(ctx, cid, p)
		if err != nil {
			return nil, caseListOut{}, toolErr("pipeline", p, err)
		}
		f.PipelineID = &pv.ID
		if s := strings.TrimSpace(in.Stage); s != "" {
			st, ok := pv.Stage(s)
			if !ok {
				return nil, caseListOut{}, fmt.Errorf("etapa %q não existe no pipeline %s", s, pv.Key)
			}
			f.StageID = &st.ID
		}
	}
	list, err := cb.ListCases(ctx, cid, f)
	if err != nil {
		return nil, caseListOut{}, opErr("listar cases", err)
	}
	out := caseListOut{Cases: make([]caseOut, 0, len(list))}
	var sb strings.Builder
	for _, v := range list {
		o := toCaseOut(v, nil)
		o.Summary = ""
		out.Cases = append(out.Cases, o)
		fmt.Fprintf(&sb, "\n- %s — %s [%s %s] v%d", v.Identifier, v.Title, v.Status, v.StageKey, v.Version)
	}
	return textResult("%d case(s)%s", len(out.Cases), sb.String()), out, nil
}

func (t *tools) currentCase(ctx context.Context, cb caseBackend, cid domain.ID, ref string) (service.CaseView, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return service.CaseView{}, errors.New("case (identificador, ex. HNK-C12) é obrigatório")
	}
	v, err := cb.GetCaseView(ctx, cid, ref)
	if err != nil {
		return service.CaseView{}, toolErr("case", ref, err)
	}
	return v, nil
}

func (t *tools) caseTransition(ctx context.Context, _ *sdk.CallToolRequest, in caseMoveIn) (*sdk.CallToolResult, caseResult, error) {
	cb, id, cid, err := t.caseScope(ctx, in.Company)
	if err != nil {
		return nil, caseResult{}, err
	}
	cur, err := t.currentCase(ctx, cb, cid, in.Case)
	if err != nil {
		return nil, caseResult{}, err
	}
	ver := in.ExpectedVersion
	if ver <= 0 {
		ver = cur.Version
	}
	v, err := cb.TransitionCase(ctx, id.Actor, cid, cur.ID, service.TransitionCaseInput{ExpectedVersion: ver, ToStage: in.ToStage, RunID: id.RunID, TaskID: id.TaskID})
	if err != nil {
		if errors.Is(err, domain.ErrNotAllowed) {
			return nil, caseResult{}, fmt.Errorf("mover para %s não é seu de decidir (%s): use hanko_case_suggest com a justificativa", in.ToStage, err.Error())
		}
		return nil, caseResult{}, opErr("mover case", err)
	}
	return textResult("Case %s em %s (versão %d)", v.Identifier, v.StageName, v.Version), caseResult{Case: toCaseOut(v, nil)}, nil
}

func (t *tools) caseSuggest(ctx context.Context, _ *sdk.CallToolRequest, in caseSuggestIn) (*sdk.CallToolResult, caseResult, error) {
	cb, id, cid, err := t.caseScope(ctx, in.Company)
	if err != nil {
		return nil, caseResult{}, err
	}
	cur, err := t.currentCase(ctx, cb, cid, in.Case)
	if err != nil {
		return nil, caseResult{}, err
	}
	ver := in.ExpectedVersion
	if ver <= 0 {
		ver = cur.Version
	}
	v, err := cb.SuggestCaseTransition(ctx, id.Actor, cid, cur.ID, service.SuggestInput{ExpectedVersion: ver, ToStage: in.ToStage, Rationale: in.Rationale,
		Confidence: in.Confidence, RunID: id.RunID, TaskID: id.TaskID})
	if err != nil {
		return nil, caseResult{}, opErr("sugerir movimentação", err)
	}
	return textResult("Sugestão registrada: %s → %s; um humano decide na fila de revisão", v.Identifier, in.ToStage), caseResult{Case: toCaseOut(v, nil)}, nil
}

func (t *tools) pipelineList(ctx context.Context, _ *sdk.CallToolRequest, in pipelineListIn) (*sdk.CallToolResult, pipelineListOut, error) {
	cb, _, cid, err := t.caseScope(ctx, in.Company)
	if err != nil {
		return nil, pipelineListOut{}, err
	}
	ps, err := cb.ListPipelines(ctx, cid, false)
	if err != nil {
		return nil, pipelineListOut{}, opErr("listar pipelines", err)
	}
	out := pipelineListOut{Pipelines: make([]pipelineOut, 0, len(ps))}
	var sb strings.Builder
	for _, p := range ps {
		po := pipelineOut{Key: p.Key, Name: p.Name, Strict: p.EnforceTransitions}
		var keys []string
		for _, st := range p.Stages {
			so := stageOut{Key: st.Key, Name: st.Name, Kind: string(st.Kind), WhatHappensHere: st.Config.WhatHappensHere, Approval: st.Config.ApprovalRequired(),
				Disabled: st.Config.Disabled}
			for _, f := range st.Config.Fields {
				so.Fields = append(so.Fields, caseFieldOut{Key: f.Key, Label: f.Label, Type: string(f.Type), Options: f.Options, Required: f.Required})
			}
			po.Stages = append(po.Stages, so)
			keys = append(keys, st.Key)
		}
		out.Pipelines = append(out.Pipelines, po)
		fmt.Fprintf(&sb, "\n- %s — %s: %s", p.Key, p.Name, strings.Join(keys, " → "))
	}
	return textResult("%d pipeline(s)%s", len(out.Pipelines), sb.String()), out, nil
}
