package service

import (
	"context"
	"encoding/json"
	"fmt"
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
)

// Readiness da persona e catálogo de modelos (ADR-0055). O teste executa o probe do harness no ambiente da
// persona (fora de transação: pode levar segundos), grava o resultado e abre/fecha o alerta; um resultado
// not_ready barra o despacho com o motivo.

// Padrões da checagem periódica.
const (
	DefaultReadinessInterval = 15 * time.Minute
	// ReadinessBatch é quantas personas um tick testa (as nunca testadas primeiro).
	ReadinessBatch = 20
	// readinessTimeout limita um teste inteiro (o do sandbox em nuvem inclui criar o sandbox).
	readinessTimeout = execenv.ProbeCloudTimeout + time.Minute
)

var readinessCounter metric.Int64Counter

func init() {
	c, err := otel.Meter("hanko/service").Int64Counter("hanko.persona.readiness",
		metric.WithDescription("Testes de readiness de persona por resultado e gatilho."))
	if err == nil {
		readinessCounter = c
	}
}

func readinessFromRow(r store.PersonaReadiness) domain.Readiness {
	out := domain.Readiness{PersonaID: r.PersonaID, CompanyID: r.CompanyID, Status: domain.ReadinessStatus(r.Status), Agent: r.Agent, Model: r.Model,
		EnvironmentID: r.EnvironmentID, EnvironmentSource: domain.EnvSource(r.EnvironmentSource), Reason: r.Reason,
		Trigger: domain.ReadinessTrigger(r.Trigger), DurationMs: int(r.DurationMs), CheckedAt: r.CheckedAt, FailingSince: r.FailingSince}
	_ = json.Unmarshal(r.Steps, &out.Steps)
	if out.Steps == nil {
		out.Steps = []domain.ReadinessStep{}
	}
	return out
}

// GetPersonaReadiness devolve o último teste da persona; nil quando ela nunca foi testada.
func (s *Service) GetPersonaReadiness(ctx context.Context, companyID, personaID domain.ID) (*domain.Readiness, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	r, err := s.read().GetPersonaReadiness(ctx, store.GetPersonaReadinessParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get persona readiness: %w", err)
	}
	out := readinessFromRow(r)
	return &out, nil
}

// ListPersonaReadiness devolve o último teste de cada persona testada da empresa, por persona.
func (s *Service) ListPersonaReadiness(ctx context.Context, companyID domain.ID) (map[domain.ID]domain.Readiness, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListPersonaReadiness(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list persona readiness: %w", err)
	}
	out := make(map[domain.ID]domain.Readiness, len(rows))
	for _, r := range rows {
		out[r.PersonaID] = readinessFromRow(r)
	}
	return out, nil
}

// TestPersonaReadiness é o "Testar agora": admin da empresa; roda o probe e grava o resultado.
func (s *Service) TestPersonaReadiness(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID) (domain.Readiness, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Readiness{}, err
	}
	return s.checkReadiness(ctx, actor, companyID, personaID, domain.ReadinessManual)
}

// CheckPersonaReadiness testa uma persona sem identidade de usuário (job periódico, pós-contratação).
func (s *Service) CheckPersonaReadiness(ctx context.Context, companyID, personaID domain.ID, trigger domain.ReadinessTrigger) (domain.Readiness, error) {
	return s.checkReadiness(ctx, domain.SystemActor, companyID, personaID, trigger)
}

func (s *Service) runReadinessScript(ctx context.Context, tg execenv.Target, script string, env map[string]string) ([]byte, error) {
	if s.ReadinessRunner != nil {
		return s.ReadinessRunner(ctx, tg, script, env)
	}
	return execenv.RunScript(ctx, s.Environments, tg, script, env)
}

func (s *Service) checkReadiness(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, trigger domain.ReadinessTrigger) (domain.Readiness, error) {
	ctx, span := otel.Tracer("hanko/service").Start(ctx, "hanko.persona.readiness", trace.WithAttributes(
		attribute.String("hanko.company_id", companyID.String()),
		attribute.String("hanko.persona_id", personaID.String()),
		attribute.String("hanko.readiness.trigger", string(trigger))))
	defer span.End()
	q := s.read()
	p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
	if err != nil {
		return domain.Readiness{}, mapErr(err, "get persona")
	}
	res, err := resolveEnvChoice(ctx, q, companyID, personaID, nil)
	if err != nil {
		return domain.Readiness{}, err
	}
	envName := "local"
	tg := execenv.Target{Driver: domain.EnvLocal}
	var tgErr error
	if res.env != nil {
		envName = res.env.Name
		if tg, tgErr = s.target(ctx, *res.env); tgErr != nil {
			tg = execenv.Target{Driver: domain.EnvironmentDriver(res.env.Driver)}
		}
	}
	cfg, _, cfgErr := s.ResolveAdapterConfig(ctx, companyID, p.AdapterConfig)
	prices, err := q.ListModelPrices(ctx)
	if err != nil {
		return domain.Readiness{}, fmt.Errorf("list model prices: %w", err)
	}
	priceModels := make([]string, len(prices))
	for i, pr := range prices {
		priceModels[i] = pr.Model
	}

	in := readiness.Input{Agent: p.Agent, Model: p.Model, AdapterConfig: cfg, EnvironmentName: envName, PriceModels: priceModels}
	cctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	var out readiness.Result
	switch {
	case tgErr != nil:
		// segredo do ambiente indisponível: o passo "environment" falha com o motivo (sem o valor)
		out = readiness.Check(cctx, s.Readiness, in, func(context.Context, string, map[string]string) ([]byte, error) { return nil, tgErr })
	case cfgErr != nil:
		out = readiness.Check(cctx, s.Readiness, in, func(context.Context, string, map[string]string) ([]byte, error) { return nil, cfgErr })
	default:
		out = readiness.Check(cctx, s.Readiness, in, func(c context.Context, script string, env map[string]string) ([]byte, error) {
			return s.runReadinessScript(c, tg, script, env)
		})
	}
	status, reason := domain.SummarizeReadiness(out.Steps)
	steps, err := json.Marshal(out.Steps)
	if err != nil {
		return domain.Readiness{}, fmt.Errorf("marshal readiness steps: %w", err)
	}

	var saved domain.Readiness
	err = s.withTx(ctx, func(t *txc) error {
		var prev *store.PersonaReadiness
		if r, err := t.q.GetPersonaReadiness(ctx, store.GetPersonaReadinessParams{CompanyID: companyID, PersonaID: personaID}); err == nil {
			prev = &r
		} else if !isNoRows(err) {
			return fmt.Errorf("get persona readiness: %w", err)
		}
		// a persona pode ter mudado de harness/modelo enquanto o probe rodava: o resultado dela não vale mais
		cur, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		if cur.Agent != p.Agent || cur.Model != p.Model {
			saved = domain.Readiness{PersonaID: personaID, CompanyID: companyID, Status: status, Agent: p.Agent, Model: p.Model, Steps: out.Steps,
				Reason: reason, Trigger: trigger, DurationMs: out.DurationMs, CheckedAt: t.now, EnvironmentSource: res.choice.Source}
			return nil
		}
		row, err := t.q.UpsertPersonaReadiness(ctx, store.UpsertPersonaReadinessParams{PersonaID: personaID, CompanyID: companyID, Status: string(status),
			Agent: p.Agent, Model: p.Model, EnvironmentID: res.choice.ID, EnvironmentSource: string(res.choice.Source), Steps: steps, Reason: reason,
			Trigger: string(trigger), DurationMs: i32(out.DurationMs), Now: t.now})
		if err != nil {
			return mapErr(err, "save persona readiness")
		}
		saved = readinessFromRow(row)
		for _, m := range out.ReportedModels {
			if err := t.q.UpsertModelReport(ctx, store.UpsertModelReportParams{CompanyID: companyID, Agent: p.Agent, Model: m, Source: string(domain.ModelProbed), Now: t.now}); err != nil {
				return fmt.Errorf("save model report: %w", err)
			}
		}
		changed := prev == nil || prev.Status != string(status)
		verb := "persona.readiness_checked"
		if changed {
			verb = "persona.readiness_changed"
		}
		if !changed && trigger == domain.ReadinessPeriodic {
			return nil // uma checagem periódica sem mudança não polui a atividade
		}
		return t.activity(actor, companyID, verb, "persona", personaID,
			map[string]any{"slug": p.Slug, "status": status, "reason": reason, "trigger": trigger, "environment_source": res.choice.Source}, "")
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save")
		return domain.Readiness{}, err
	}
	if readinessCounter != nil {
		readinessCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("status", string(status)), attribute.String("trigger", string(trigger))))
	}
	span.SetAttributes(attribute.String("hanko.readiness.status", string(status)))
	return saved, nil
}

// ReadinessTick testa até ReadinessBatch personas sem teste ou com teste mais velho que o intervalo (zero desliga).
// Devolve quantas testou. Um erro numa persona não interrompe as outras.
func (s *Service) ReadinessTick(ctx context.Context) (int, error) {
	if s.ReadinessEvery <= 0 {
		return 0, nil // checagem periódica desligada (HANKO_READINESS_INTERVAL=0); o "Testar agora" segue valendo
	}
	due, err := s.read().ListPersonasDueReadiness(ctx, store.ListPersonasDueReadinessParams{
		OlderThan: s.Now().Add(-s.ReadinessEvery), Lim: ReadinessBatch})
	if err != nil {
		return 0, fmt.Errorf("list personas due readiness: %w", err)
	}
	n := 0
	var firstErr error
	for _, d := range due {
		if ctx.Err() != nil {
			break
		}
		trig := domain.ReadinessPeriodic
		if _, err := s.checkReadiness(ctx, domain.SystemActor, d.CompanyID, d.ID, trig); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("readiness da persona %s: %w", d.ID, err)
			}
			continue
		}
		n++
	}
	return n, firstErr
}

// invalidateReadiness apaga o último teste da persona (mudou harness, modelo, env ou ambiente): o resultado antigo
// não vale e a persona volta a "nunca testada" até o próximo teste. Na transação de quem mudou a persona.
func (t *txc) invalidateReadiness(companyID, personaID domain.ID) error {
	if err := t.q.DeletePersonaReadiness(t.ctx, store.DeletePersonaReadinessParams{CompanyID: companyID, PersonaID: personaID}); err != nil {
		return fmt.Errorf("invalidate readiness: %w", err)
	}
	return nil
}

// readinessGate barra o despacho de uma persona cujo último teste falhou com o harness e o modelo de agora. nil =
// pode seguir (inclusive "nunca testada").
func (t *txc) readinessGate(p store.Persona, agent, model string) error {
	r, err := t.q.GetPersonaReadiness(t.ctx, store.GetPersonaReadinessParams{CompanyID: p.CompanyID, PersonaID: p.ID})
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("get persona readiness: %w", err)
	}
	if rd := readinessFromRow(r); rd.Blocks(agent, model) {
		return domain.BlockedError(p.Name, rd.Reason)
	}
	return nil
}

// ---- catálogo de modelos ----

// ModelCatalog devolve os modelos selecionáveis da empresa para um harness ("" = todos): aliases do harness,
// model_prices, o que os testes de readiness viram e o que as runs usaram nos últimos 90 dias.
func (s *Service) ModelCatalog(ctx context.Context, companyID domain.ID, agent string) ([]domain.CatalogModel, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	prices, err := q.ListModelPrices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list model prices: %w", err)
	}
	priceModels := make([]string, len(prices))
	for i, p := range prices {
		priceModels[i] = p.Model
	}
	var reports []domain.ModelReport
	rr, err := q.ListModelReports(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list model reports: %w", err)
	}
	for _, r := range rr {
		reports = append(reports, domain.ModelReport{Agent: r.Agent, Model: r.Model, Origin: domain.ModelOrigin(r.Source)})
	}
	used, err := q.ListRunModels(ctx, store.ListRunModelsParams{CompanyID: companyID, Since: s.Now().AddDate(0, 0, -90)})
	if err != nil {
		return nil, fmt.Errorf("list run models: %w", err)
	}
	for _, u := range used {
		reports = append(reports, domain.ModelReport{Agent: u.Agent, Model: u.Model, Origin: domain.ModelUsed})
	}
	return domain.BuildModelCatalog(agent, priceModels, reports), nil
}

// ModelPriced diz se o modelo tem preço próprio (sem cair no de reserva) e qual prefixo casou.
func (s *Service) ModelPriced(ctx context.Context, model string) (prefix string, priced bool, err error) {
	prices, err := s.read().ListModelPrices(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list model prices: %w", err)
	}
	models := make([]string, len(prices))
	for i, p := range prices {
		models[i] = p.Model
	}
	prefix, priced = domain.MatchPrice(models, model)
	return prefix, priced, nil
}
