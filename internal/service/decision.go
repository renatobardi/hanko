package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/decision"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/provider"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// Decision model (ADR-0056): classificador externo por empresa, chamado pelo Hanko (ator system). A chave é uma
// referência ${secret:NOME}; o valor só existe em memória durante a chamada.

const (
	decisionMaxTokens = 400
	// maxDecisionPersonas limita a lista de personas enviada à triagem.
	maxDecisionPersonas = 60
)

var (
	decisionMetricsOnce sync.Once
	decisionCalls       metric.Int64Counter
	decisionCost        metric.Float64Counter
)

func decisionInstruments() (metric.Int64Counter, metric.Float64Counter) {
	decisionMetricsOnce.Do(func() {
		m := otel.Meter("hanko/decision")
		decisionCalls, _ = m.Int64Counter("hanko.decision.calls", metric.WithDescription("invocações do decision model, por feature e resultado"))
		decisionCost, _ = m.Float64Counter("hanko.decision.cost_usd", metric.WithDescription("custo (USD) das invocações do decision model, por feature"))
	})
	return decisionCalls, decisionCost
}

// DecisionView é a configuração do decision model com o estado de cada feature registrada.
type DecisionView struct {
	// Config é nil enquanto a empresa não configurou o decision model.
	Config   *domain.DecisionModel         `json:"config,omitempty"`
	Features []domain.DecisionFeatureState `json:"features"`
	// Warning explica algo que não impediu de salvar (catálogo indisponível: sem preço para estimar custo).
	Warning string `json:"warning,omitempty"`
}

// SetDecisionModelInput configura o decision model. BaseURL vazio usa o do OpenRouter.
type SetDecisionModelInput struct {
	BaseURL    string
	APIKeyRef  string
	Model      string
	TimeoutSec int
	Enabled    *bool
}

func decisionFromRow(r store.DecisionModel) domain.DecisionModel {
	return domain.DecisionModel{CompanyID: r.CompanyID, Provider: r.Provider, BaseURL: r.BaseUrl, APIKeyRef: r.ApiKeyRef,
		Model: r.Model, PromptUSDToken: r.PromptUsdToken, CompletionUSDToken: r.CompletionUsdToken, TimeoutSec: int(r.TimeoutSec),
		Enabled: r.Enabled, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func invocationFromRow(r store.DecisionInvocation) domain.DecisionInvocation {
	return domain.DecisionInvocation{ID: r.ID, CompanyID: r.CompanyID, Feature: domain.DecisionFeature(r.Feature), Actor: domain.ActorSystem,
		TaskID: r.TaskID, Model: r.Model, Status: r.Status, Error: r.Error, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		CostUSD: r.CostUsd, CostSource: r.CostSource, LatencyMs: int(r.LatencyMs), CreatedAt: r.CreatedAt}
}

// decisionHTTP é o cliente de saída do decision model: o de testes, ou o do netguard (SSRF) com o prazo pedido.
func (s *Service) decisionHTTP(timeout time.Duration) *http.Client {
	if s.Integrations.HTTP != nil {
		return s.Integrations.HTTP
	}
	return netguard.NewClient(s.Integrations.policy(), timeout)
}

func decisionTimeout(sec int) time.Duration {
	if sec <= 0 {
		sec = domain.DefaultDecisionTimeout
	}
	return time.Duration(sec) * time.Second
}

// checkDecisionKeyRef aceita vazio (endpoint sem chave, como um modelo local) ou exatamente uma referência
// ${secret:NOME} da empresa; chave em claro nunca é aceita.
func (s *Service) checkDecisionKeyRef(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	refs := secretref.Find(ref)
	if len(refs) != 1 || refs[0].String() != ref {
		return "", domain.Invalid("api_key_ref", "use uma referência ${secret:NOME} a um segredo da empresa; a chave não é guardada aqui")
	}
	if err := s.checkSecretRefs(ctx, q, companyID, "api_key_ref", ref); err != nil {
		return "", err
	}
	return ref, nil
}

func (s *Service) decisionClient(ctx context.Context, companyID domain.ID, baseURL, keyRef string, timeout time.Duration) (*provider.Client, []string, error) {
	if _, err := netguard.ValidateURL(baseURL, true, s.Integrations.policy()); err != nil && s.Integrations.HTTP == nil {
		return nil, nil, domain.Invalid("base_url", err.Error())
	}
	key, used, err := "", []string(nil), error(nil)
	if keyRef != "" {
		if key, used, err = s.ResolveSecretText(ctx, companyID, keyRef); err != nil {
			return nil, nil, fmt.Errorf("chave do decision model: %w", err)
		}
	}
	return &provider.Client{BaseURL: baseURL, APIKey: key, HTTP: s.decisionHTTP(timeout),
		Headers: map[string]string{"X-Title": "Hanko"}}, used, nil
}

// GetDecisionModel devolve a configuração e as features. Qualquer membro lê.
func (s *Service) GetDecisionModel(ctx context.Context, companyID domain.ID) (DecisionView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return DecisionView{}, err
	}
	return s.decisionView(ctx, s.read(), companyID)
}

func (s *Service) decisionView(ctx context.Context, q *store.Queries, companyID domain.ID) (DecisionView, error) {
	out := DecisionView{Features: []domain.DecisionFeatureState{}}
	row, err := q.GetDecisionModel(ctx, companyID)
	switch {
	case err == nil:
		c := decisionFromRow(row)
		out.Config = &c
	case !isNoRows(err):
		return out, fmt.Errorf("get decision model: %w", err)
	}
	fs, err := q.ListDecisionFeatures(ctx, companyID)
	if err != nil {
		return out, fmt.Errorf("list decision features: %w", err)
	}
	by := map[string]store.DecisionFeature{}
	for _, f := range fs {
		by[f.Feature] = f
	}
	for _, spec := range domain.DecisionFeatures {
		st := domain.DecisionFeatureState{DecisionFeatureSpec: spec}
		if f, ok := by[string(spec.Feature)]; ok {
			st.Enabled, st.Model = f.Enabled, f.Model
		}
		out.Features = append(out.Features, st)
	}
	return out, nil
}

// DecisionCatalog busca o catálogo de modelos do endpoint da empresa. baseURL e apiKeyRef vazios usam a
// configuração salva (ou o endpoint padrão sem chave): serve para escolher o modelo antes de salvar.
func (s *Service) DecisionCatalog(ctx context.Context, companyID domain.ID, baseURL, apiKeyRef string) ([]provider.CatalogModel, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	q := s.read()
	baseURL, apiKeyRef = strings.TrimSpace(baseURL), strings.TrimSpace(apiKeyRef)
	timeout := time.Duration(domain.DefaultDecisionTimeout) * time.Second
	if row, err := q.GetDecisionModel(ctx, companyID); err == nil {
		if baseURL == "" {
			baseURL = row.BaseUrl
			if apiKeyRef == "" {
				apiKeyRef = row.ApiKeyRef
			}
		}
		timeout = decisionTimeout(int(row.TimeoutSec))
	}
	if baseURL == "" {
		baseURL = domain.DefaultDecisionBaseURL
	}
	if err := domain.ValidateDecisionBaseURL(baseURL); err != nil {
		return nil, err
	}
	if _, err := s.checkDecisionKeyRef(ctx, q, companyID, apiKeyRef); err != nil {
		return nil, err
	}
	c, _, err := s.decisionClient(ctx, companyID, baseURL, apiKeyRef, timeout)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ms, err := c.Catalog(cctx)
	if err != nil {
		return nil, fmt.Errorf("catálogo de modelos: %w", err)
	}
	return ms, nil
}

// SetDecisionModel grava a configuração (admin). O modelo precisa estar no catálogo do endpoint; se o catálogo
// estiver indisponível o modelo é aceito, sem preço, com um aviso.
func (s *Service) SetDecisionModel(ctx context.Context, actor domain.Actor, companyID domain.ID, in SetDecisionModelInput) (DecisionView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return DecisionView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return DecisionView{}, domain.ErrForbidden
	}
	base := strings.TrimSpace(in.BaseURL)
	if base == "" {
		base = domain.DefaultDecisionBaseURL
	}
	if err := domain.ValidateDecisionBaseURL(base); err != nil {
		return DecisionView{}, err
	}
	model := strings.TrimSpace(in.Model)
	if model == "" || len(model) > domain.MaxDecisionModelLen {
		return DecisionView{}, domain.Invalid("model", "choose a model from the catalog")
	}
	timeout := in.TimeoutSec
	if timeout == 0 {
		timeout = domain.DefaultDecisionTimeout
	}
	if timeout < 1 || timeout > domain.MaxDecisionTimeout {
		return DecisionView{}, domain.Invalid("timeout_sec", fmt.Sprintf("must be between 1 and %d", domain.MaxDecisionTimeout))
	}
	q := s.read()
	ref, err := s.checkDecisionKeyRef(ctx, q, companyID, in.APIKeyRef)
	if err != nil {
		return DecisionView{}, err
	}
	var promptUSD, completionUSD float64
	warning := ""
	c, _, err := s.decisionClient(ctx, companyID, base, ref, decisionTimeout(timeout))
	if err != nil {
		return DecisionView{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, decisionTimeout(timeout))
	ms, cerr := c.Catalog(cctx)
	cancel()
	if cerr != nil {
		warning = "catálogo indisponível (" + cerr.Error() + "); modelo aceito sem preço: o custo só será conhecido quando a resposta o informar"
	} else {
		found := false
		for _, m := range ms {
			if m.ID == model {
				promptUSD, completionUSD, found = m.PromptUSD, m.CompletionUSD, true
				break
			}
		}
		if !found {
			return DecisionView{}, domain.Invalid("model", fmt.Sprintf("%q is not in the provider catalog", model))
		}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.UpsertDecisionModel(t.ctx, store.UpsertDecisionModelParams{CompanyID: companyID, Provider: domain.DecisionProviderOpenRouter,
			BaseUrl: base, ApiKeyRef: ref, Model: model, PromptUsdToken: promptUSD, CompletionUsdToken: completionUSD,
			TimeoutSec: int32(timeout), Enabled: enabled, Now: t.now}) //nolint:gosec // validado entre 1 e 300
		if err != nil {
			return mapErr(err, "set decision model")
		}
		return t.activity(actor, companyID, "decision.configured", "decision_model", companyID,
			map[string]any{"model": row.Model, "base_url": row.BaseUrl, "api_key_ref": row.ApiKeyRef, "enabled": row.Enabled}, "")
	})
	if err != nil {
		return DecisionView{}, err
	}
	v, err := s.decisionView(ctx, s.read(), companyID)
	v.Warning = warning
	return v, err
}

// DeleteDecisionModel remove a configuração e desliga as features (admin). O livro de invocações fica.
func (s *Service) DeleteDecisionModel(ctx context.Context, actor domain.Actor, companyID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	if actor.Type == domain.ActorPersona {
		return domain.ErrForbidden
	}
	return s.withTx(ctx, func(t *txc) error {
		n, err := t.q.DeleteDecisionModel(t.ctx, companyID)
		if err != nil {
			return mapErr(err, "delete decision model")
		}
		if n == 0 {
			return notFound("decision model")
		}
		for _, spec := range domain.DecisionFeatures {
			if _, err := t.q.UpsertDecisionFeature(t.ctx, store.UpsertDecisionFeatureParams{CompanyID: companyID, Feature: string(spec.Feature), Enabled: false, Now: t.now}); err != nil {
				return mapErr(err, "disable decision feature")
			}
		}
		return t.activity(actor, companyID, "decision.removed", "decision_model", companyID, nil, "")
	})
}

// SetDecisionFeature liga ou desliga uma feature e escolhe o modelo dela (admin). Ligar exige o decision model
// configurado e ligado.
func (s *Service) SetDecisionFeature(ctx context.Context, actor domain.Actor, companyID domain.ID, feature domain.DecisionFeature, enabled bool, model string) (DecisionView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return DecisionView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return DecisionView{}, domain.ErrForbidden
	}
	if !feature.Valid() {
		return DecisionView{}, domain.Invalid("feature", "unknown feature")
	}
	model = strings.TrimSpace(model)
	if len(model) > domain.MaxDecisionModelLen {
		return DecisionView{}, domain.Invalid("model", "too long")
	}
	err := s.withTx(ctx, func(t *txc) error {
		if enabled {
			if _, err := t.q.GetDecisionModel(t.ctx, companyID); err != nil {
				if isNoRows(err) {
					return domain.Invalid("feature", "configure the decision model before enabling features")
				}
				return mapErr(err, "get decision model")
			}
		}
		if _, err := t.q.UpsertDecisionFeature(t.ctx, store.UpsertDecisionFeatureParams{CompanyID: companyID, Feature: string(feature), Enabled: enabled, Model: model, Now: t.now}); err != nil {
			return mapErr(err, "set decision feature")
		}
		return t.activity(actor, companyID, "decision.feature_set", "decision_model", companyID,
			map[string]any{"feature": feature, "enabled": enabled, "model": model}, "")
	})
	if err != nil {
		return DecisionView{}, err
	}
	return s.decisionView(ctx, s.read(), companyID)
}

// ---------- invocação ----------

// featureRun é o que uma feature entrega à invocação: o prompt, como ler a resposta e o que gravar junto do
// registro (na mesma transação).
type featureRun struct {
	feature domain.DecisionFeature
	taskID  *domain.ID
	prompt  decision.Prompt
	// handle lê o conteúdo da resposta; devolve o que gravar na transação do registro (pode ser nil) ou erro.
	handle func(content string) (apply func(t *txc) error, err error)
}

// runFeature chama o decision model para uma feature, grava a invocação no ledger (ator system, ok ou erro) e, se a
// resposta é válida, o efeito da feature, tudo na mesma transação de registro.
func (s *Service) runFeature(ctx context.Context, companyID domain.ID, fr featureRun) (inv domain.DecisionInvocation, err error) {
	ctx, span := otel.Tracer("hanko/decision").Start(ctx, "hanko.decision.invoke",
		trace.WithAttributes(attribute.String("hanko.company_id", companyID.String()), attribute.String("hanko.decision.feature", string(fr.feature))))
	defer func() {
		result := "ok"
		if err != nil {
			result = "error"
			span.SetStatus(codes.Error, "decision invoke")
		}
		if calls, cost := decisionInstruments(); calls != nil {
			attrs := metric.WithAttributes(attribute.String("feature", string(fr.feature)), attribute.String("result", result))
			calls.Add(ctx, 1, attrs)
			if cost != nil && inv.CostUSD > 0 {
				cost.Add(ctx, inv.CostUSD, metric.WithAttributes(attribute.String("feature", string(fr.feature))))
			}
		}
		span.End()
	}()

	view, err := s.decisionView(ctx, s.read(), companyID)
	if err != nil {
		return inv, err
	}
	if view.Config == nil || !view.Config.Enabled {
		return inv, domain.Invalid("decision_model", "the decision model is not configured or is turned off for this company")
	}
	var fstate domain.DecisionFeatureState
	for _, f := range view.Features {
		if f.Feature == fr.feature {
			fstate = f
		}
	}
	if !fstate.Enabled {
		return inv, domain.Invalid("feature", fmt.Sprintf("feature %q is turned off for this company", fr.feature))
	}
	cfg := view.Config
	model := cfg.Model
	if fstate.Model != "" {
		model = fstate.Model
	}
	timeout := decisionTimeout(cfg.TimeoutSec)
	client, _, err := s.decisionClient(ctx, companyID, cfg.BaseURL, cfg.APIKeyRef, timeout)
	if err != nil {
		return inv, err
	}
	zero := 0.0
	cctx, cancel := context.WithTimeout(ctx, timeout)
	start := time.Now()
	resp, callErr := client.Chat(cctx, provider.ChatRequest{Model: model, JSONObject: true, Temperature: &zero, MaxTokens: decisionMaxTokens,
		Messages: []provider.Message{{Role: "system", Content: fr.prompt.System}, {Role: "user", Content: fr.prompt.User}}})
	cancel()
	latency := int(time.Since(start).Milliseconds())

	rec := store.InsertDecisionInvocationParams{ID: domain.NewID(), CompanyID: companyID, Feature: string(fr.feature), TaskID: fr.taskID,
		Model: model, Status: "ok", CostSource: "estimated", LatencyMs: int32(min(latency, 1<<30))} //nolint:gosec // limitado acima
	var apply func(t *txc) error
	failure := callErr
	if callErr == nil {
		rec.InputTokens, rec.OutputTokens = resp.Usage.PromptTokens, resp.Usage.CompletionTokens
		if resp.Usage.CostUSD != nil {
			rec.CostUsd, rec.CostSource = *resp.Usage.CostUSD, "reported"
		} else {
			rec.CostUsd = provider.EstimateCost(resp.Usage, cfg.PromptUSDToken, cfg.CompletionUSDToken)
		}
		apply, failure = fr.handle(resp.Message.Content)
	}
	if failure != nil {
		rec.Status, rec.Error = "error", decision.Clip(failure.Error(), 500)
	}
	txErr := s.withTx(ctx, func(t *txc) error {
		rec.Now = t.now
		row, err := t.q.InsertDecisionInvocation(t.ctx, rec)
		if err != nil {
			return mapErr(err, "insert decision invocation")
		}
		inv = invocationFromRow(row)
		if err := t.activity(domain.SystemActor, companyID, "decision.invoked", "decision_invocation", row.ID,
			map[string]any{"feature": fr.feature, "model": row.Model, "status": row.Status, "cost_usd": row.CostUsd, "cost_source": row.CostSource}, ""); err != nil {
			return err
		}
		if failure == nil && apply != nil {
			return apply(t)
		}
		return nil
	})
	switch {
	case txErr != nil:
		return inv, txErr
	case failure != nil:
		var he *provider.HTTPError
		if errors.As(failure, &he) {
			return inv, fmt.Errorf("decision model: %w", failure)
		}
		return inv, fmt.Errorf("decision model (%s): %w", fr.feature, failure)
	}
	return inv, nil
}

// TriageOutcome é o resultado da triagem de uma task.
type TriageOutcome struct {
	Invocation domain.DecisionInvocation `json:"invocation"`
	TaskKey    string                    `json:"task_key"`
	Persona    string                    `json:"persona"` // slug sugerido
	PersonaID  domain.ID                 `json:"persona_id"`
	Priority   string                    `json:"priority"`
	Reason     string                    `json:"reason"`
	CommentID  domain.ID                 `json:"comment_id"`
}

// Triage pede ao decision model a persona e a prioridade para a task e registra a sugestão como comentário do
// sistema. Não atribui nada: a decisão continua humana. Exige a feature triage ligada.
func (s *Service) Triage(ctx context.Context, actor domain.Actor, companyID domain.ID, taskRef string) (TriageOutcome, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TriageOutcome{}, err
	}
	tv, err := s.GetTask(ctx, companyID, taskRef)
	if err != nil {
		return TriageOutcome{}, err
	}
	ps, err := s.read().ListPersonas(ctx, companyID)
	if err != nil {
		return TriageOutcome{}, fmt.Errorf("list personas: %w", err)
	}
	var opts []decision.Option
	bySlug := map[string]store.Persona{}
	for _, p := range ps {
		if domain.PersonaStatus(p.Status) == domain.PersonaOff || len(opts) >= maxDecisionPersonas {
			continue
		}
		opts = append(opts, decision.Option{Key: p.Slug, Label: p.Name + " (" + p.Title + ")", Hint: p.Instructions})
		bySlug[p.Slug] = p
	}
	if len(opts) == 0 {
		return TriageOutcome{}, domain.Invalid("persona", "the company has no persona to triage to")
	}
	out := TriageOutcome{TaskKey: tv.Key}
	taskID := tv.ID
	inv, err := s.runFeature(ctx, companyID, featureRun{feature: domain.FeatureTriage, taskID: &taskID,
		prompt: decision.TriagePrompt(tv.Title, tv.Description, opts),
		handle: func(content string) (func(t *txc) error, error) {
			r, err := decision.ParseTriage(content, opts)
			if err != nil {
				return nil, err
			}
			p := bySlug[r.Persona]
			out.Persona, out.PersonaID, out.Priority, out.Reason = r.Persona, p.ID, r.Priority, r.Reason
			return func(t *txc) error {
				body := fmt.Sprintf("**Triagem (decision model)**: sugiro `%s` (%s), prioridade **%s**.", p.Slug, p.Name, r.Priority)
				if r.Reason != "" {
					body += "\n\n" + r.Reason
				}
				c, err := t.q.CreateComment(t.ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: companyID, TaskID: taskID,
					AuthorType: string(domain.ActorSystem), Body: body, Now: t.now})
				if err != nil {
					return mapErr(err, "triage comment")
				}
				out.CommentID = c.ID
				return t.activity(domain.SystemActor, companyID, "task.triaged", "task", taskID,
					map[string]any{"persona": p.Slug, "priority": r.Priority, "comment_id": c.ID, "requested_by": actor.Type}, "")
			}, nil
		}})
	out.Invocation = inv
	if err != nil {
		return TriageOutcome{Invocation: inv}, err
	}
	return out, nil
}

// PhaseOutcome é a etapa sugerida para um texto.
type PhaseOutcome struct {
	Invocation domain.DecisionInvocation `json:"invocation"`
	Pipeline   string                    `json:"pipeline"`
	Stage      string                    `json:"stage"`
	StageName  string                    `json:"stage_name"`
	Reason     string                    `json:"reason"`
}

// ClassifyPhase pede a etapa do pipeline em que o texto se encaixa. Só sugere; nada é movido.
func (s *Service) ClassifyPhase(ctx context.Context, actor domain.Actor, companyID domain.ID, pipelineRef, text string) (PhaseOutcome, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return PhaseOutcome{}, err
	}
	if strings.TrimSpace(text) == "" {
		return PhaseOutcome{}, domain.Invalid("text", "required")
	}
	pv, err := s.GetPipeline(ctx, companyID, pipelineRef)
	if err != nil {
		return PhaseOutcome{}, err
	}
	var opts []decision.Option
	names := map[string]string{}
	for _, st := range pv.Stages {
		opts = append(opts, decision.Option{Key: st.Key, Label: st.Name, Hint: string(st.Kind)})
		names[st.Key] = st.Name
	}
	if len(opts) == 0 {
		return PhaseOutcome{}, domain.Invalid("pipeline", "the pipeline has no stages")
	}
	out := PhaseOutcome{Pipeline: pv.Key}
	inv, err := s.runFeature(ctx, companyID, featureRun{feature: domain.FeaturePhaseClassifier,
		prompt: decision.PhasePrompt(text, opts),
		handle: func(content string) (func(t *txc) error, error) {
			r, err := decision.ParsePhase(content, opts)
			if err != nil {
				return nil, err
			}
			out.Stage, out.StageName, out.Reason = r.Stage, names[r.Stage], r.Reason
			return nil, nil
		}})
	out.Invocation = inv
	_ = actor
	if err != nil {
		return PhaseOutcome{Invocation: inv}, err
	}
	return out, nil
}

// NameSummaryOutcome é o nome e o resumo gerados.
type NameSummaryOutcome struct {
	Invocation domain.DecisionInvocation `json:"invocation"`
	Name       string                    `json:"name"`
	Summary    string                    `json:"summary"`
}

// NameSummary gera um nome curto e um resumo para um texto.
func (s *Service) NameSummary(ctx context.Context, actor domain.Actor, companyID domain.ID, text string) (NameSummaryOutcome, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return NameSummaryOutcome{}, err
	}
	if strings.TrimSpace(text) == "" {
		return NameSummaryOutcome{}, domain.Invalid("text", "required")
	}
	var out NameSummaryOutcome
	inv, err := s.runFeature(ctx, companyID, featureRun{feature: domain.FeatureNameSummary,
		prompt: decision.NameSummaryPrompt(text),
		handle: func(content string) (func(t *txc) error, error) {
			r, err := decision.ParseNameSummary(content)
			if err != nil {
				return nil, err
			}
			out.Name, out.Summary = r.Name, r.Summary
			return nil, nil
		}})
	out.Invocation = inv
	_ = actor
	if err != nil {
		return NameSummaryOutcome{Invocation: inv}, err
	}
	return out, nil
}

// ListDecisionInvocations devolve as últimas invocações do decision model (qualquer membro).
func (s *Service) ListDecisionInvocations(ctx context.Context, companyID domain.ID, limit int) ([]domain.DecisionInvocation, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.read().ListDecisionInvocations(ctx, store.ListDecisionInvocationsParams{CompanyID: companyID, Lim: int32(limit)}) //nolint:gosec // 1..200
	if err != nil {
		return nil, fmt.Errorf("list decision invocations: %w", err)
	}
	out := make([]domain.DecisionInvocation, len(rows))
	for i, r := range rows {
		out[i] = invocationFromRow(r)
	}
	return out, nil
}

// DecisionCostRow é o custo do decision model de uma feature e modelo no período.
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

// DecisionCost é o bloco "Decision model" do Custos: invocações com ator system, fora das runs de persona.
type DecisionCost struct {
	Actor   string            `json:"actor"` // sempre "system"
	Calls   int               `json:"calls"`
	CostUSD float64           `json:"cost_usd"`
	Rows    []DecisionCostRow `json:"rows"`
}

func (s *Service) decisionCost(ctx context.Context, q *store.Queries, companyID domain.ID, from, to time.Time) (DecisionCost, error) {
	rows, err := q.DecisionCostBreakdown(ctx, store.DecisionCostBreakdownParams{CompanyID: companyID, FromTs: from, ToTs: to})
	if err != nil {
		return DecisionCost{}, fmt.Errorf("decision cost: %w", err)
	}
	out := DecisionCost{Actor: string(domain.ActorSystem), Rows: []DecisionCostRow{}}
	for _, r := range rows {
		out.Rows = append(out.Rows, DecisionCostRow{Feature: r.Feature, Model: r.Model, Calls: int(r.Calls), Errors: int(r.Errors),
			CostUSD: r.CostUsd, EstimatedUSD: r.EstimatedUsd, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens})
		out.Calls += int(r.Calls)
		out.CostUSD += r.CostUsd
	}
	return out, nil
}
