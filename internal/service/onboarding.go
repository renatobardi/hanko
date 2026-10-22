package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/readiness"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/teams"
)

// Onboarding (ADR-0061, #67): assistente retomável empresa → harness (probe) → primeira persona ou pacote de time →
// primeira task. Os passos são derivados do que a empresa já tem (domain.ComputeOnboarding); o banco guarda só o que não
// se deduz (último probe, passos pulados, dispensa).

var onboardingCounter metric.Int64Counter

func init() {
	c, err := otel.Meter("hanko/service").Int64Counter("hanko.onboarding",
		metric.WithDescription("Ações do assistente de primeiros passos por tipo e resultado."))
	if err == nil {
		onboardingCounter = c
	}
}

func countOnboarding(ctx context.Context, action, result string) {
	if onboardingCounter != nil {
		onboardingCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("action", action), attribute.String("result", result)))
	}
}

// onboardingAgents são os harnesses que o assistente sabe testar sem persona (acp exige um comando por persona).
var onboardingAgents = []string{"claude", "codex", "fake"}

// OnboardingProbeResult é o resultado do teste de um harness feito antes de existir persona.
type OnboardingProbeResult struct {
	Agent      string                 `json:"agent"`
	Model      string                 `json:"model,omitempty"`
	Status     domain.ReadinessStatus `json:"status"`
	Reason     string                 `json:"reason,omitempty"`
	Steps      []domain.ReadinessStep `json:"steps"`
	DurationMs int                    `json:"duration_ms"`
}

func (s *Service) onboardingFacts(ctx context.Context, q *store.Queries, companyID domain.ID) (domain.OnboardingFacts, error) {
	var f domain.OnboardingFacts
	st, err := q.GetOnboardingState(ctx, companyID)
	switch {
	case err == nil:
		f.HarnessAgent, f.HarnessStatus = st.HarnessAgent, st.HarnessStatus
		f.Skipped = st.Skipped
		f.FirstTask = st.FirstTaskID != nil
		f.Dismissed = st.DismissedAt != nil
	case !isNoRows(err):
		return f, fmt.Errorf("get onboarding state: %w", err)
	}
	n, err := q.CountCompanyPersonas(ctx, companyID)
	if err != nil {
		return f, fmt.Errorf("count personas: %w", err)
	}
	f.Personas = int(n)
	if f.HasRun, err = q.CompanyHasRun(ctx, companyID); err != nil {
		return f, fmt.Errorf("company has run: %w", err)
	}
	return f, nil
}

// OnboardingStatus devolve o estado do assistente da empresa. Qualquer membro lê.
func (s *Service) OnboardingStatus(ctx context.Context, companyID domain.ID) (domain.OnboardingStatus, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.OnboardingStatus{}, err
	}
	f, err := s.onboardingFacts(ctx, s.read(), companyID)
	if err != nil {
		return domain.OnboardingStatus{}, err
	}
	return domain.ComputeOnboarding(f), nil
}

// OnboardingProbe testa o harness escolhido no ambiente padrão da empresa (ou no local) e guarda o resultado. Admin.
// Reaproveita o probe de readiness (ADR-0055); a diferença é que aqui ainda não há persona.
func (s *Service) OnboardingProbe(ctx context.Context, actor domain.Actor, companyID domain.ID, agent, model string) (OnboardingProbeResult, error) {
	ctx, span := otel.Tracer("hanko/service").Start(ctx, "hanko.onboarding.probe", trace.WithAttributes(
		attribute.String("hanko.company_id", companyID.String()), attribute.String("hanko.onboarding.agent", agent)))
	defer span.End()
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return OnboardingProbeResult{}, err
	}
	agent, model = strings.TrimSpace(agent), strings.TrimSpace(model)
	if !slices.Contains(onboardingAgents, agent) {
		return OnboardingProbeResult{}, domain.Invalid("agent", fmt.Sprintf("o assistente testa %v; para outro harness, contrate a persona e use \"Testar agora\"", onboardingAgents))
	}
	q := s.read()
	envName, tg := "local", execenv.Target{Driver: domain.EnvLocal}
	var tgErr error
	if d, err := q.GetDefaultEnvironment(ctx, companyID); err == nil {
		envName = d.Name
		if tg, tgErr = s.target(ctx, d); tgErr != nil {
			tg = execenv.Target{Driver: domain.EnvironmentDriver(d.Driver)}
		}
	} else if !isNoRows(err) {
		return OnboardingProbeResult{}, fmt.Errorf("get default environment: %w", err)
	}
	prices, err := q.ListModelPrices(ctx)
	if err != nil {
		return OnboardingProbeResult{}, fmt.Errorf("list model prices: %w", err)
	}
	priceModels := make([]string, len(prices))
	for i, p := range prices {
		priceModels[i] = p.Model
	}
	in := readiness.Input{Agent: agent, Model: model, AdapterConfig: json.RawMessage(`{}`), EnvironmentName: envName, PriceModels: priceModels}
	cctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	run := func(c context.Context, script string, env map[string]string) ([]byte, error) {
		if tgErr != nil {
			return nil, tgErr
		}
		return s.runReadinessScript(c, tg, script, env)
	}
	out := readiness.Check(cctx, s.Readiness, in, run)
	status, reason := domain.SummarizeReadiness(out.Steps)
	steps, err := json.Marshal(out.Steps)
	if err != nil {
		return OnboardingProbeResult{}, fmt.Errorf("marshal onboarding steps: %w", err)
	}
	err = s.withTx(ctx, func(t *txc) error {
		now := t.now
		if _, err := t.q.SaveOnboardingHarness(ctx, store.SaveOnboardingHarnessParams{CompanyID: companyID, Agent: agent, Model: model,
			Status: string(status), Reason: reason, Steps: steps, Now: &now}); err != nil {
			return mapErr(err, "save onboarding harness")
		}
		return t.activity(actor, companyID, "onboarding.harness_probed", "company", companyID,
			map[string]any{"agent": agent, "model": model, "status": status, "reason": reason, "environment": envName}, "")
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save")
		countOnboarding(ctx, "probe", "error")
		return OnboardingProbeResult{}, err
	}
	countOnboarding(ctx, "probe", string(status))
	span.SetAttributes(attribute.String("hanko.onboarding.status", string(status)))
	return OnboardingProbeResult{Agent: agent, Model: model, Status: status, Reason: reason, Steps: out.Steps, DurationMs: out.DurationMs}, nil
}

// OnboardingLastProbe devolve o último probe guardado (nil se nunca houve).
func (s *Service) OnboardingLastProbe(ctx context.Context, companyID domain.ID) (*OnboardingProbeResult, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	st, err := s.read().GetOnboardingState(ctx, companyID)
	if isNoRows(err) || (err == nil && st.HarnessCheckedAt == nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get onboarding state: %w", err)
	}
	r := &OnboardingProbeResult{Agent: st.HarnessAgent, Model: st.HarnessModel, Status: domain.ReadinessStatus(st.HarnessStatus), Reason: st.HarnessReason}
	_ = json.Unmarshal(st.HarnessSteps, &r.Steps)
	if r.Steps == nil {
		r.Steps = []domain.ReadinessStep{}
	}
	return r, nil
}

// OnboardingSkip pula um passo que admite (o do harness). Admin.
func (s *Service) OnboardingSkip(ctx context.Context, actor domain.Actor, companyID domain.ID, step domain.OnboardingStepKey) (domain.OnboardingStatus, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.OnboardingStatus{}, err
	}
	if !step.Valid() || !step.Skippable() {
		return domain.OnboardingStatus{}, domain.Invalid("step", "só o passo do harness pode ser pulado")
	}
	err := s.withTx(ctx, func(t *txc) error {
		skipped := []string{}
		if st, err := t.q.GetOnboardingState(ctx, companyID); err == nil {
			skipped = append(skipped, st.Skipped...)
		} else if !isNoRows(err) {
			return fmt.Errorf("get onboarding state: %w", err)
		}
		if slices.Contains(skipped, string(step)) {
			return nil
		}
		skipped = append(skipped, string(step))
		if _, err := t.q.SetOnboardingSkipped(ctx, store.SetOnboardingSkippedParams{CompanyID: companyID, Skipped: skipped, Now: t.now}); err != nil {
			return mapErr(err, "set onboarding skipped")
		}
		return t.activity(actor, companyID, "onboarding.step_skipped", "company", companyID, map[string]any{"step": step}, "")
	})
	if err != nil {
		return domain.OnboardingStatus{}, err
	}
	countOnboarding(ctx, "skip", "ok")
	return s.OnboardingStatus(ctx, companyID)
}

// OnboardingDismiss dispensa o assistente (ou, com dismissed=false, volta a oferecê-lo). Admin.
func (s *Service) OnboardingDismiss(ctx context.Context, actor domain.Actor, companyID domain.ID, dismissed bool) (domain.OnboardingStatus, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.OnboardingStatus{}, err
	}
	err := s.withTx(ctx, func(t *txc) error {
		var at *time.Time
		if dismissed {
			now := t.now
			at = &now
		}
		if _, err := t.q.SetOnboardingDismissed(ctx, store.SetOnboardingDismissedParams{CompanyID: companyID, DismissedAt: at, Now: t.now}); err != nil {
			return mapErr(err, "set onboarding dismissed")
		}
		verb := "onboarding.resumed"
		if dismissed {
			verb = "onboarding.dismissed"
		}
		return t.activity(actor, companyID, verb, "company", companyID, nil, "")
	})
	if err != nil {
		return domain.OnboardingStatus{}, err
	}
	countOnboarding(ctx, "dismiss", fmt.Sprint(dismissed))
	return s.OnboardingStatus(ctx, companyID)
}

// OnboardingTeamResult é o que a instalação de um pacote pelo assistente devolve.
type OnboardingTeamResult struct {
	Install TeamInstall `json:"install"`
	// Lead é a persona de topo do pacote, já ativa: a que recebe a primeira task.
	Lead *domain.Persona `json:"lead,omitempty"`
}

// OnboardingInstallTeam instala um pacote do catálogo embutido pelo assistente e ativa a persona de topo (o pacote
// instala pausado, ADR-0044; o assistente existe para chegar numa task rodando, então liga uma). Admin e humano.
func (s *Service) OnboardingInstallTeam(ctx context.Context, actor domain.Actor, companyID domain.ID, slug string) (OnboardingTeamResult, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return OnboardingTeamResult{}, err
	}
	pkg, ok, err := teams.Get(strings.TrimSpace(slug))
	if err != nil {
		return OnboardingTeamResult{}, fmt.Errorf("catálogo de pacotes: %w", err)
	}
	if !ok {
		return OnboardingTeamResult{}, fmt.Errorf("pacote %q: %w", slug, domain.ErrNotFound)
	}
	res, err := s.InstallTeam(ctx, actor, companyID, pkg, TeamInstallOptions{Source: TeamSourceCatalog})
	if err != nil {
		countOnboarding(ctx, "team", "error")
		return OnboardingTeamResult{}, err
	}
	out := OnboardingTeamResult{Install: res}
	leadSlug := ""
	for _, p := range pkg.Personas {
		if p.ReportsTo == "" {
			leadSlug = p.Slug
			break
		}
	}
	if leadSlug == "" && len(pkg.Personas) > 0 {
		leadSlug = pkg.Personas[0].Slug
	}
	if leadSlug != "" {
		p, err := s.GetPersona(ctx, companyID, leadSlug)
		if err != nil {
			return out, fmt.Errorf("persona de topo do pacote: %w", err)
		}
		if p.Status != domain.PersonaActive {
			active := domain.PersonaActive
			if p, err = s.UpdatePersona(ctx, actor, companyID, p.ID, UpdatePersonaInput{Status: &active}); err != nil {
				return out, fmt.Errorf("ativar a persona de topo: %w", err)
			}
		}
		out.Lead = &p
	}
	countOnboarding(ctx, "team", "ok")
	return out, nil
}

// OnboardingStartTask cria a primeira task para a persona escolhida (que acorda sozinha, como em qualquer task atribuída)
// e marca o passo como feito. Membro.
func (s *Service) OnboardingStartTask(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, title, description string) (TaskView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskView{}, err
	}
	tv, err := s.CreateTask(ctx, actor, companyID, CreateTaskInput{Title: title, Description: description, PersonaID: &personaID})
	if err != nil {
		countOnboarding(ctx, "task", "error")
		return TaskView{}, err
	}
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.SetOnboardingFirstTask(ctx, store.SetOnboardingFirstTaskParams{CompanyID: companyID, TaskID: &tv.ID, Now: t.now}); err != nil {
			return mapErr(err, "set onboarding first task")
		}
		return t.activity(actor, companyID, "onboarding.first_task", "task", tv.ID, map[string]any{"persona_id": personaID}, "")
	})
	if err != nil {
		return tv, fmt.Errorf("a task foi criada, mas o assistente não registrou o passo: %w", err)
	}
	countOnboarding(ctx, "task", "ok")
	return tv, nil
}
