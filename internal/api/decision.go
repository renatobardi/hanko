package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/provider"
	"github.com/renatobardi/hanko/internal/service"
)

// Decision model (ADR-0056). A chave é uma referência ${secret:NOME}; nenhuma resposta traz valor de segredo.

// decisionBackend é o subconjunto do service para o decision model; opcional como secretsBackend.
type decisionBackend interface {
	GetDecisionModel(ctx context.Context, companyID domain.ID) (service.DecisionView, error)
	SetDecisionModel(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.SetDecisionModelInput) (service.DecisionView, error)
	DeleteDecisionModel(ctx context.Context, actor domain.Actor, companyID domain.ID) error
	SetDecisionFeature(ctx context.Context, actor domain.Actor, companyID domain.ID, feature domain.DecisionFeature, enabled bool, model string) (service.DecisionView, error)
	DecisionCatalog(ctx context.Context, companyID domain.ID, baseURL, apiKeyRef string) ([]provider.CatalogModel, error)
	ListDecisionInvocations(ctx context.Context, companyID domain.ID, limit int) ([]domain.DecisionInvocation, error)
	Triage(ctx context.Context, actor domain.Actor, companyID domain.ID, taskRef string) (service.TriageOutcome, error)
	ClassifyPhase(ctx context.Context, actor domain.Actor, companyID domain.ID, pipelineRef, text string) (service.PhaseOutcome, error)
	NameSummary(ctx context.Context, actor domain.Actor, companyID domain.ID, text string) (service.NameSummaryOutcome, error)
}

func (s *server) decisionSvc() (decisionBackend, error) {
	b, ok := s.svc.(decisionBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "decision model indisponível neste servidor")
	}
	return b, nil
}

// DecisionModelConfig é a configuração do decision model da empresa.
type DecisionModelConfig struct {
	Provider           string    `json:"provider" enum:"openrouter"`
	BaseURL            string    `json:"base_url" example:"https://openrouter.ai/api/v1"`
	APIKeyRef          string    `json:"api_key_ref,omitempty" example:"${secret:openrouter}" doc:"Referência ao segredo da empresa com a chave; o valor nunca é devolvido."`
	Model              string    `json:"model" example:"anthropic/claude-haiku-4.5"`
	PromptUSDToken     float64   `json:"prompt_usd_token" doc:"Preço do catálogo (USD por token de entrada) quando o modelo foi escolhido."`
	CompletionUSDToken float64   `json:"completion_usd_token"`
	TimeoutSec         int       `json:"timeout_sec"`
	Enabled            bool      `json:"enabled"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// DecisionFeature é uma feature registrada e o estado dela na empresa.
type DecisionFeature struct {
	Feature     string `json:"feature" enum:"triage,phase_classifier,name_summary,fast_response,card_summary"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Model       string `json:"model,omitempty" doc:"Substitui o modelo da empresa nesta feature."`
}

// Decision é o decision model da empresa com as features.
type Decision struct {
	Configured bool                 `json:"configured"`
	Config     *DecisionModelConfig `json:"config,omitempty"`
	Features   []DecisionFeature    `json:"features"`
	Warning    string               `json:"warning,omitempty"`
}

func decisionOut(v service.DecisionView) Decision {
	o := Decision{Configured: v.Config != nil, Features: make([]DecisionFeature, len(v.Features)), Warning: v.Warning}
	if c := v.Config; c != nil {
		o.Config = &DecisionModelConfig{Provider: c.Provider, BaseURL: c.BaseURL, APIKeyRef: c.APIKeyRef, Model: c.Model,
			PromptUSDToken: c.PromptUSDToken, CompletionUSDToken: c.CompletionUSDToken, TimeoutSec: c.TimeoutSec, Enabled: c.Enabled, UpdatedAt: c.UpdatedAt}
	}
	for i, f := range v.Features {
		o.Features[i] = DecisionFeature{Feature: string(f.Feature), Title: f.Title, Description: f.Description, Enabled: f.Enabled, Model: f.Model}
	}
	return o
}

// DecisionCatalogModel é um modelo do catálogo do provedor.
type DecisionCatalogModel struct {
	ID            string  `json:"id" example:"anthropic/claude-haiku-4.5"`
	Name          string  `json:"name"`
	ContextLength int64   `json:"context_length,omitempty"`
	PromptUSD     float64 `json:"prompt_usd_token"`
	CompletionUSD float64 `json:"completion_usd_token"`
}

// DecisionInvocation é uma linha do livro de invocações (ator sempre system).
type DecisionInvocation struct {
	ID           string    `json:"id" format:"uuid"`
	Feature      string    `json:"feature" enum:"triage,phase_classifier,name_summary,fast_response,card_summary"`
	Actor        string    `json:"actor" enum:"system"`
	TaskID       string    `json:"task_id,omitempty" format:"uuid"`
	Model        string    `json:"model"`
	Status       string    `json:"status" enum:"ok,error"`
	Error        string    `json:"error,omitempty"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	CostSource   string    `json:"cost_source" enum:"reported,estimated"`
	LatencyMs    int       `json:"latency_ms"`
	CreatedAt    time.Time `json:"created_at"`
}

func decisionInvocationOut(i domain.DecisionInvocation) DecisionInvocation {
	return DecisionInvocation{ID: idStr(i.ID), Feature: string(i.Feature), Actor: string(i.Actor), TaskID: idPtr(i.TaskID), Model: i.Model,
		Status: i.Status, Error: i.Error, InputTokens: i.InputTokens, OutputTokens: i.OutputTokens, CostUSD: i.CostUSD,
		CostSource: i.CostSource, LatencyMs: i.LatencyMs, CreatedAt: i.CreatedAt}
}

// DecisionCostRow é o custo de uma feature e modelo no período.
type DecisionCostRow struct {
	Feature      string  `json:"feature"`
	Model        string  `json:"model"`
	Calls        int     `json:"calls"`
	Errors       int     `json:"errors"`
	CostUSD      float64 `json:"cost_usd"`
	EstimatedUSD float64 `json:"estimated_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

// DecisionCost é o bloco do decision model no Custos: invocações com ator system, somadas ao gasto da empresa.
type DecisionCost struct {
	Actor   string            `json:"actor" enum:"system"`
	Calls   int               `json:"calls"`
	CostUSD float64           `json:"cost_usd"`
	Rows    []DecisionCostRow `json:"rows"`
}

func decisionCostOut(c service.DecisionCost) DecisionCost {
	rows := make([]DecisionCostRow, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = DecisionCostRow(r)
	}
	return DecisionCost{Actor: c.Actor, Calls: c.Calls, CostUSD: c.CostUSD, Rows: rows}
}

type setDecisionIn struct {
	CompanyPath
	Body struct {
		BaseURL    string `json:"base_url,omitempty" doc:"Raiz da API OpenAI-compatível. Padrão: OpenRouter." example:"https://openrouter.ai/api/v1"`
		APIKeyRef  string `json:"api_key_ref,omitempty" doc:"Referência ${secret:NOME} ao segredo da empresa; vazio = endpoint sem chave." example:"${secret:openrouter}"`
		Model      string `json:"model" minLength:"1" maxLength:"200" doc:"Id do modelo no catálogo do provedor."`
		TimeoutSec int    `json:"timeout_sec,omitempty" minimum:"0" maximum:"300" doc:"Padrão 30."`
		Enabled    *bool  `json:"enabled,omitempty" doc:"Padrão true."`
	}
}

type decisionCatalogIn struct {
	CompanyPath
	BaseURL   string `query:"base_url" doc:"Opcional: consulta outro endpoint antes de salvar."`
	APIKeyRef string `query:"api_key_ref" doc:"Opcional: referência ${secret:NOME} para esse endpoint."`
}

type decisionFeatureIn struct {
	CompanyPath
	Feature string `path:"feature" enum:"triage,phase_classifier,name_summary,fast_response,card_summary"`
	Body    struct {
		Enabled bool   `json:"enabled"`
		Model   string `json:"model,omitempty" maxLength:"200" doc:"Modelo próprio da feature; vazio usa o da empresa."`
	}
}

type decisionInvocationsIn struct {
	CompanyPath
	Limit int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// Triage é o resultado da triagem de uma task.
type Triage struct {
	TaskKey    string             `json:"task_key"`
	Persona    string             `json:"persona" doc:"Slug da persona sugerida."`
	PersonaID  string             `json:"persona_id" format:"uuid"`
	Priority   string             `json:"priority" enum:"critical,high,medium,low"`
	Reason     string             `json:"reason,omitempty"`
	CommentID  string             `json:"comment_id" format:"uuid" doc:"Comentário do sistema com a sugestão."`
	Invocation DecisionInvocation `json:"invocation"`
}

// PhaseSuggestion é a etapa sugerida para um texto.
type PhaseSuggestion struct {
	Pipeline   string             `json:"pipeline"`
	Stage      string             `json:"stage"`
	StageName  string             `json:"stage_name"`
	Reason     string             `json:"reason,omitempty"`
	Invocation DecisionInvocation `json:"invocation"`
}

// NameSummary é o nome e o resumo gerados.
type NameSummary struct {
	Name       string             `json:"name"`
	Summary    string             `json:"summary"`
	Invocation DecisionInvocation `json:"invocation"`
}

type classifyPhaseIn struct {
	CompanyPath
	Body struct {
		Pipeline string `json:"pipeline" minLength:"1" doc:"Id ou chave do pipeline."`
		Text     string `json:"text" minLength:"1" maxLength:"100000"`
	}
}

type nameSummaryIn struct {
	CompanyPath
	Body struct {
		Text string `json:"text" minLength:"1" maxLength:"100000"`
	}
}

func (s *server) registerDecision(api huma.API) {
	const tag = "decision"
	base := "/companies/{company}/decision"
	// guard resolve backend e empresa; as operações abaixo só diferem no que chamam.
	guard := func(ctx context.Context, p CompanyPath) (decisionBackend, *scope2, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, nil, err
		}
		b, err := s.decisionSvc()
		if err != nil {
			return nil, nil, err
		}
		sc, err := s.scope(ctx, p.Company)
		if err != nil {
			return nil, nil, err
		}
		return b, &scope2{actor: sc.Actor, company: sc.Company.ID}, nil
	}

	huma.Register(api, op("get-decision-model", http.MethodGet, base, tag, "Decision model da empresa e estado das features"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[Decision], error) {
			b, sc, err := guard(ctx, *in)
			if err != nil {
				return nil, err
			}
			v, err := b.GetDecisionModel(ctx, sc.company)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Decision]{Body: decisionOut(v)}, nil
		})

	huma.Register(api, op("set-decision-model", http.MethodPut, base, tag, "Configura o decision model (admin): endpoint, chave por segredo e modelo do catálogo"),
		func(ctx context.Context, in *setDecisionIn) (*ItemOut[Decision], error) {
			b, sc, err := guard(ctx, in.CompanyPath)
			if err != nil {
				return nil, err
			}
			v, err := b.SetDecisionModel(ctx, sc.actor, sc.company, service.SetDecisionModelInput{BaseURL: in.Body.BaseURL,
				APIKeyRef: in.Body.APIKeyRef, Model: in.Body.Model, TimeoutSec: in.Body.TimeoutSec, Enabled: in.Body.Enabled})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Decision]{Body: decisionOut(v)}, nil
		})

	huma.Register(api, noContent(op("delete-decision-model", http.MethodDelete, base, tag, "Remove o decision model e desliga as features (admin)")),
		func(ctx context.Context, in *CompanyPath) (*struct{}, error) {
			b, sc, err := guard(ctx, *in)
			if err != nil {
				return nil, err
			}
			if err := b.DeleteDecisionModel(ctx, sc.actor, sc.company); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("list-decision-catalog", http.MethodGet, base+"/catalog", tag, "Catálogo de modelos do provedor do decision model"),
		func(ctx context.Context, in *decisionCatalogIn) (*ListOut[DecisionCatalogModel], error) {
			b, sc, err := guard(ctx, in.CompanyPath)
			if err != nil {
				return nil, err
			}
			ms, err := b.DecisionCatalog(ctx, sc.company, in.BaseURL, in.APIKeyRef)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ms, Page{Limit: len(ms)}, func(m provider.CatalogModel) DecisionCatalogModel {
				return DecisionCatalogModel{ID: m.ID, Name: m.Name, ContextLength: m.ContextLength, PromptUSD: m.PromptUSD, CompletionUSD: m.CompletionUSD}
			}), nil
		})

	huma.Register(api, op("set-decision-feature", http.MethodPut, base+"/features/{feature}", tag, "Liga ou desliga uma feature do decision model (admin)"),
		func(ctx context.Context, in *decisionFeatureIn) (*ItemOut[Decision], error) {
			b, sc, err := guard(ctx, in.CompanyPath)
			if err != nil {
				return nil, err
			}
			v, err := b.SetDecisionFeature(ctx, sc.actor, sc.company, domain.DecisionFeature(in.Feature), in.Body.Enabled, in.Body.Model)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Decision]{Body: decisionOut(v)}, nil
		})

	huma.Register(api, op("list-decision-invocations", http.MethodGet, base+"/invocations", tag, "Últimas invocações do decision model (ator system)"),
		func(ctx context.Context, in *decisionInvocationsIn) (*ListOut[DecisionInvocation], error) {
			b, sc, err := guard(ctx, in.CompanyPath)
			if err != nil {
				return nil, err
			}
			list, err := b.ListDecisionInvocations(ctx, sc.company, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: in.Limit}, decisionInvocationOut), nil
		})

	huma.Register(api, op("triage-task", http.MethodPost, "/companies/{company}/tasks/{task}/triage", tag,
		"Triagem pelo decision model: sugere persona e prioridade como comentário do sistema (não atribui)"),
		func(ctx context.Context, in *TaskPath) (*ItemOut[Triage], error) {
			b, sc, err := guard(ctx, in.CompanyPath)
			if err != nil {
				return nil, err
			}
			o, err := b.Triage(ctx, sc.actor, sc.company, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Triage]{Body: Triage{TaskKey: o.TaskKey, Persona: o.Persona, PersonaID: idStr(o.PersonaID), Priority: o.Priority,
				Reason: o.Reason, CommentID: idStr(o.CommentID), Invocation: decisionInvocationOut(o.Invocation)}}, nil
		})

	huma.Register(api, op("classify-phase", http.MethodPost, base+"/classify-phase", tag, "Sugere a etapa de um pipeline para um texto"),
		func(ctx context.Context, in *classifyPhaseIn) (*ItemOut[PhaseSuggestion], error) {
			b, sc, err := guard(ctx, in.CompanyPath)
			if err != nil {
				return nil, err
			}
			o, err := b.ClassifyPhase(ctx, sc.actor, sc.company, in.Body.Pipeline, in.Body.Text)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PhaseSuggestion]{Body: PhaseSuggestion{Pipeline: o.Pipeline, Stage: o.Stage, StageName: o.StageName, Reason: o.Reason,
				Invocation: decisionInvocationOut(o.Invocation)}}, nil
		})

	huma.Register(api, op("name-summary", http.MethodPost, base+"/name-summary", tag, "Gera nome curto e resumo para um texto"),
		func(ctx context.Context, in *nameSummaryIn) (*ItemOut[NameSummary], error) {
			b, sc, err := guard(ctx, in.CompanyPath)
			if err != nil {
				return nil, err
			}
			o, err := b.NameSummary(ctx, sc.actor, sc.company, in.Body.Text)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[NameSummary]{Body: NameSummary{Name: o.Name, Summary: o.Summary, Invocation: decisionInvocationOut(o.Invocation)}}, nil
		})
}

// scope2 é o que as operações do decision model usam do escopo da requisição.
type scope2 struct {
	actor   domain.Actor
	company domain.ID
}
