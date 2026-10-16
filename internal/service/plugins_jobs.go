package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/store"
)

// Eventos e jobs de plugin (ADR-0038). Um fato de atividade de uma empresa que ligou o plugin, com o verbo em
// events[] do manifesto, vira na MESMA transação um job da fila (pelo menos uma vez, com retry curto; o plugin
// deduplica pelo id do evento). Os jobs periódicos do manifesto são agendados pelo tick do host, sem
// sobreposição por (plugin, empresa, job).

// PluginJobs é a parte de Service.Jobs que enfileira trabalho de plugin; opcional como WebhookJobs.
type PluginJobs interface {
	DeliverPluginEventTx(ctx context.Context, tx pgx.Tx, pluginID, companyID, activityID domain.ID) error
	RunPluginJobTx(ctx context.Context, tx pgx.Tx, jobRunID domain.ID) error
}

// PluginEventMaxAttempts é o total de tentativas de entrega de um evento a um plugin.
const PluginEventMaxAttempts = 5

// PluginEventBackoff é a espera depois da tentativa attempt (1-based): 5 s, 15 s, 45 s, 2 min 15 s.
func PluginEventBackoff(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 3
	}
	return d
}

// enqueuePluginEvents registra a entrega do fato aos plugins assinantes. Chamada por txc.activity. Fatos
// causados pelo próprio plugin não voltam para ele (evita laço: um plugin que cria task ao receber task.created).
func (t *txc) enqueuePluginEvents(companyID domain.ID, verb string, actor domain.Actor, activityID domain.ID) error {
	if !t.s.Plugins.Enabled() {
		return nil
	}
	pj, ok := t.s.Jobs.(PluginJobs)
	if !ok {
		return nil
	}
	rows, err := t.q.ListPluginsForEvent(t.ctx, store.ListPluginsForEventParams{CompanyID: companyID, Verb: verb})
	if err != nil {
		return fmt.Errorf("list plugins for event: %w", err)
	}
	for _, r := range rows {
		if actor.Type == domain.ActorSystem && actor.ID != nil && *actor.ID == r.ID {
			continue
		}
		if err := pj.DeliverPluginEventTx(t.ctx, t.tx, r.ID, companyID, activityID); err != nil {
			return fmt.Errorf("enqueue plugin event: %w", err)
		}
	}
	return nil
}

// pluginEvent é o fato de atividade como o plugin o recebe.
type pluginEvent struct {
	ID         string          `json:"id"`
	Verb       string          `json:"verb"`
	OccurredAt string          `json:"occurred_at"`
	Actor      map[string]any  `json:"actor"`
	Subject    map[string]any  `json:"subject"`
	Data       json.RawMessage `json:"data"`
}

func toPluginEvent(e domain.ActivityEvent) pluginEvent {
	actor := map[string]any{"type": string(e.Actor.Type)}
	if e.Actor.ID != nil {
		actor["id"] = e.Actor.ID.String()
	}
	data := e.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	return pluginEvent{ID: e.ID.String(), Verb: e.Verb, OccurredAt: e.OccurredAt.UTC().Format(time.RFC3339Nano), Actor: actor,
		Subject: map[string]any{"type": e.SubjectType, "id": e.SubjectID.String()}, Data: adapter.RedactJSON(data)}
}

// ErrPluginRetry: a entrega não aconteceu agora e deve ser tentada de novo pela fila.
var ErrPluginRetry = errors.New("plugin: tentar de novo")

// DeliverPluginEvent entrega um fato ao plugin (job plugin_event). Plugin desligado, desinstalado, sem a
// capacidade ou sem o verbo assinado encerra sem retry; processo fora do ar ou erro do plugin pede retry.
func (s *Service) DeliverPluginEvent(ctx context.Context, pluginID, companyID, activityID domain.ID) error {
	q := s.read()
	r, err := q.GetPluginByID(ctx, pluginID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get plugin: %w", err)
	}
	p := pluginFromRow(r)
	if !p.Status.Active() || !p.Has(domain.CapEventsSubscribe) {
		return nil
	}
	st, err := q.GetPluginSettings(ctx, store.GetPluginSettingsParams{PluginID: p.ID, CompanyID: companyID})
	if err != nil || !st.Enabled {
		return nil
	}
	a, err := q.GetActivityByID(ctx, store.GetActivityByIDParams{CompanyID: companyID, ID: activityID})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get activity: %w", err)
	}
	ev := toPluginEvent(activityFromRow(a))
	subscribed := false
	for _, v := range p.Manifest.Events {
		subscribed = subscribed || v == ev.Verb
	}
	if !subscribed {
		return nil
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return mapErr(err, "company")
	}
	if s.Plugins.Runtime == nil {
		return fmt.Errorf("%w: sem gerenciador de processos", ErrPluginRetry)
	}
	if _, err := s.Plugins.Runtime.Call(ctx, p.Key, "on_event", map[string]any{"company": refOf(co), "event": ev}, pluginEventTimeout); err != nil {
		return fmt.Errorf("%w: on_event %s: %w", ErrPluginRetry, ev.Verb, err)
	}
	return nil
}

// LogPluginEventGiveUp registra no log do plugin um evento que esgotou as tentativas.
func (s *Service) LogPluginEventGiveUp(ctx context.Context, pluginID, companyID domain.ID, cause error) {
	msg := "evento não entregue depois de todas as tentativas"
	if cause != nil {
		msg += ": " + cause.Error()
	}
	s.pluginLog(ctx, pluginID, &companyID, domain.PluginLogHost, msg)
}

// PluginTick é o tick periódico do host: reconcilia os processos, expira execuções de job perdidas e agenda os
// jobs vencidos.
func (s *Service) PluginTick(ctx context.Context) (int, error) {
	if !s.Plugins.Enabled() {
		return 0, nil
	}
	s.ReconcilePlugins(ctx)
	now := s.Now().UTC()
	if _, err := store.New(s.Pool).ExpirePluginJobRuns(ctx, store.ExpirePluginJobRunsParams{Now: now, Before: now.Add(-(pluginJobTimeout + 5*time.Minute))}); err != nil {
		return 0, fmt.Errorf("expire plugin job runs: %w", err)
	}
	pj, ok := s.Jobs.(PluginJobs)
	if !ok {
		return 0, nil
	}
	rows, err := s.read().ListPlugins(ctx)
	if err != nil {
		return 0, fmt.Errorf("list plugins: %w", err)
	}
	started := 0
	for _, r := range rows {
		p := pluginFromRow(r)
		if !p.Status.Active() || !p.Has(domain.CapJobs) || len(p.Manifest.Jobs) == 0 {
			continue
		}
		if s.Plugins.Runtime == nil || !s.Plugins.Runtime.Status(p.Key).Ready {
			continue // fora do ar: não queima ciclo; o próximo tick tenta
		}
		cos, err := s.read().ListPluginEnabledCompanies(ctx, p.ID)
		if err != nil {
			return started, fmt.Errorf("list plugin companies: %w", err)
		}
		for _, c := range cos {
			n, err := s.schedulePluginJobs(ctx, pj, p, c.ID, now)
			if err != nil {
				return started, err
			}
			started += n
		}
	}
	return started, nil
}

func (s *Service) schedulePluginJobs(ctx context.Context, pj PluginJobs, p domain.Plugin, companyID domain.ID, now time.Time) (int, error) {
	last, err := s.read().LastPluginJobRuns(ctx, store.LastPluginJobRunsParams{PluginID: p.ID, CompanyID: companyID})
	if err != nil {
		return 0, fmt.Errorf("last plugin job runs: %w", err)
	}
	byKey := map[string]store.PluginJobRun{}
	for _, l := range last {
		byKey[l.JobKey] = l
	}
	n := 0
	for _, j := range p.Manifest.Jobs {
		if l, ok := byKey[j.Key]; ok && (l.Status == domain.PluginJobRunning || now.Sub(l.StartedAt) < time.Duration(j.EveryMinutes)*time.Minute) {
			continue
		}
		err := s.withTx(ctx, func(t *txc) error {
			row, err := t.q.StartPluginJobRun(t.ctx, store.StartPluginJobRunParams{ID: domain.NewID(), PluginID: p.ID, CompanyID: companyID, JobKey: j.Key, Now: t.now})
			if isNoRows(err) {
				return nil // já há uma execução em andamento: sem sobreposição
			}
			if err != nil {
				return fmt.Errorf("start plugin job run: %w", err)
			}
			n++
			return pj.RunPluginJobTx(t.ctx, t.tx, row.ID)
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// RunPluginJob executa uma rodada de job (job plugin_run_job) e grava o resultado.
func (s *Service) RunPluginJob(ctx context.Context, jobRunID domain.ID) error {
	q := s.read()
	jr, err := q.GetPluginJobRun(ctx, jobRunID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get plugin job run: %w", err)
	}
	if jr.Status != domain.PluginJobRunning {
		return nil
	}
	finish := func(status, errText string) error {
		w := store.New(s.Pool)
		if _, err := w.FinishPluginJobRun(ctx, store.FinishPluginJobRunParams{ID: jr.ID, Status: status, Error: pluginClip(errText, 1000), Now: s.Now().UTC()}); err != nil {
			return fmt.Errorf("finish plugin job run: %w", err)
		}
		_ = w.PrunePluginJobRuns(ctx, store.PrunePluginJobRunsParams{PluginID: jr.PluginID, Keep: 1000})
		if errText != "" {
			s.pluginLog(ctx, jr.PluginID, &jr.CompanyID, domain.PluginLogHost, "job "+jr.JobKey+" falhou: "+errText)
		}
		return nil
	}
	r, err := q.GetPluginByID(ctx, jr.PluginID)
	if err != nil {
		return finish(domain.PluginJobFailed, "plugin não encontrado")
	}
	p := pluginFromRow(r)
	if _, ok := p.Manifest.Job(jr.JobKey); !ok || !p.Status.Active() || !p.Has(domain.CapJobs) {
		return finish(domain.PluginJobFailed, "job não está mais disponível")
	}
	co, err := q.GetCompanyByID(ctx, jr.CompanyID)
	if err != nil {
		return finish(domain.PluginJobFailed, "empresa não encontrada")
	}
	if st, err := q.GetPluginSettings(ctx, store.GetPluginSettingsParams{PluginID: p.ID, CompanyID: co.ID}); err != nil || !st.Enabled {
		return finish(domain.PluginJobFailed, "plugin desligado na empresa")
	}
	if s.Plugins.Runtime == nil {
		return finish(domain.PluginJobFailed, plugin.ErrNotRunning.Error())
	}
	if _, err := s.Plugins.Runtime.Call(ctx, p.Key, "run_job", map[string]any{"company": refOf(co), "key": jr.JobKey}, pluginJobTimeout); err != nil {
		return finish(domain.PluginJobFailed, err.Error())
	}
	return finish(domain.PluginJobDone, "")
}
