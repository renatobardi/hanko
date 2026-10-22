package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/ratelimit"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/webhook"
)

// Gatilhos de fluxo, revisões e arquivar (ADR-0047).

// Erros do webhook de entrada; a camada HTTP os traduz em 401 e 429.
var (
	// ErrWebhookUnauthorized: assinatura ausente, inválida, vencida ou segredo indisponível.
	ErrWebhookUnauthorized = errors.New("webhook: assinatura inválida")
	// ErrWebhookThrottled: o gatilho passou do limite por minuto.
	ErrWebhookThrottled = errors.New("webhook: limite do gatilho excedido")
)

// FlowTriggerView é um gatilho com o endereço do webhook; SecretValue só vem na criação (segredo gerado).
type FlowTriggerView struct {
	domain.FlowTrigger
	// Path é o caminho do webhook de entrada (webhook) relativo à URL pública.
	Path        string `json:"path,omitempty"`
	SecretValue string `json:"secret_value,omitempty"`
}

// FlowWebhookPath é o caminho público do webhook de um gatilho.
func FlowWebhookPath(triggerID domain.ID) string { return "/hooks/flows/" + triggerID.String() }

func flowTriggerFromRow(r store.FlowTrigger) domain.FlowTrigger {
	tr := domain.FlowTrigger{ID: r.ID, CompanyID: r.CompanyID, FlowID: r.FlowID, Kind: domain.FlowTriggerKind(r.Kind), Name: r.Name,
		Enabled: r.Enabled, SecretRef: r.SecretRef, RatePerMin: int(r.RatePerMin), EventVerb: r.EventVerb,
		LastFiredAt: r.LastFiredAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if len(r.EventFilter) > 2 {
		_ = json.Unmarshal(r.EventFilter, &tr.EventFilter)
	}
	if len(r.Mapping) > 2 {
		_ = json.Unmarshal(r.Mapping, &tr.Mapping)
	}
	return tr
}

func triggerView(r store.FlowTrigger) FlowTriggerView {
	v := FlowTriggerView{FlowTrigger: flowTriggerFromRow(r)}
	if v.Kind == domain.TriggerKindWebhook {
		v.Path = FlowWebhookPath(r.ID)
	}
	return v
}

// ---------- revisões ----------

// flowSnapshot é a definição vigente do fluxo (com os gatilhos, sem segredo) como a revisão a guarda.
func (t *txc) flowSnapshot(f store.Flow) (domain.FlowSnapshot, error) {
	trs, err := t.q.ListFlowTriggers(t.ctx, store.ListFlowTriggersParams{CompanyID: f.CompanyID, FlowID: f.ID})
	if err != nil {
		return domain.FlowSnapshot{}, fmt.Errorf("list flow triggers: %w", err)
	}
	snap := domain.FlowSnapshot{Name: f.Name, Description: f.Description, Cron: f.Cron, Timezone: f.Timezone, PersonaID: f.PersonaID,
		ProjectID: f.ProjectID, Priority: f.Priority, TaskTitle: f.TaskTitle, TaskDescription: f.TaskDescription,
		Variables: flowVarsOf(f.Variables), Triggers: []domain.FlowSnapshotTrigger{}}
	for _, r := range trs {
		tr := flowTriggerFromRow(r)
		snap.Triggers = append(snap.Triggers, domain.FlowSnapshotTrigger{Kind: tr.Kind, Name: tr.Name, Enabled: tr.Enabled,
			RatePerMin: tr.RatePerMin, EventVerb: tr.EventVerb, EventFilter: tr.EventFilter, Mapping: tr.Mapping})
	}
	return snap, nil
}

// insertFlowRevision grava a revisão rev com a definição que o fluxo tem agora (já gravada nesta transação).
func (t *txc) insertFlowRevision(actor domain.Actor, flowID domain.ID, rev int, origin, summary string, restoredFrom *int) error {
	f, err := t.q.GetFlowByIDForUpdate(t.ctx, flowID)
	if err != nil {
		return mapErr(err, "get flow")
	}
	snap, err := t.flowSnapshot(f)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal flow snapshot: %w", err)
	}
	var rf *int32
	if restoredFrom != nil {
		v := int32(*restoredFrom) //nolint:gosec // número de revisão pequeno
		rf = &v
	}
	if _, err := t.q.InsertFlowRevision(t.ctx, store.InsertFlowRevisionParams{ID: domain.NewID(), CompanyID: f.CompanyID, FlowID: flowID,
		Revision: int32(rev), Snapshot: raw, Summary: truncateRunes(summary, 500), ActorType: string(actor.Type), ActorID: actor.ID, //nolint:gosec // idem
		Origin: origin, RestoredFrom: rf, Now: t.now}); err != nil {
		return mapErr(err, "insert flow revision")
	}
	return nil
}

// bumpFlowRevision registra uma revisão nova por mudança que não passa por UpdateFlow (gatilhos).
func (t *txc) bumpFlowRevision(actor domain.Actor, flowID domain.ID, summary string) error {
	f, err := t.q.GetFlowByIDForUpdate(t.ctx, flowID)
	if err != nil {
		return mapErr(err, "get flow")
	}
	rev := int(f.Revision) + 1
	if err := t.q.UpdateFlowExtras(t.ctx, store.UpdateFlowExtrasParams{ID: flowID, Variables: f.Variables, Revision: int32(rev), Now: t.now}); err != nil { //nolint:gosec // idem
		return fmt.Errorf("bump flow revision: %w", err)
	}
	return t.insertFlowRevision(actor, flowID, rev, "edit", summary, nil)
}

func flowRevisionFromRow(r store.FlowRevision) domain.FlowRevision {
	rev := domain.FlowRevision{ID: r.ID, FlowID: r.FlowID, Revision: int(r.Revision), Summary: r.Summary,
		Actor: actorOf(r.ActorType, r.ActorID), Origin: r.Origin, CreatedAt: r.CreatedAt}
	_ = json.Unmarshal(r.Snapshot, &rev.Snapshot)
	if r.RestoredFrom != nil {
		v := int(*r.RestoredFrom)
		rev.RestoredFrom = &v
	}
	return rev
}

// FlowRevisions lista as revisões do fluxo, da mais nova para a mais antiga.
func (s *Service) FlowRevisions(ctx context.Context, companyID, flowID domain.ID, limit int) ([]domain.FlowRevision, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetFlowByID(ctx, store.GetFlowByIDParams{CompanyID: companyID, ID: flowID}); err != nil {
		return nil, mapErr(err, "get flow")
	}
	rows, err := q.ListFlowRevisions(ctx, store.ListFlowRevisionsParams{CompanyID: companyID, FlowID: flowID, Lim: i32(clamp(limit, 50, 500))})
	if err != nil {
		return nil, fmt.Errorf("list flow revisions: %w", err)
	}
	out := make([]domain.FlowRevision, len(rows))
	for i, r := range rows {
		out[i] = flowRevisionFromRow(r)
	}
	return out, nil
}

// GetFlowRevision devolve uma revisão.
func (s *Service) GetFlowRevision(ctx context.Context, companyID, flowID domain.ID, revision int) (domain.FlowRevision, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.FlowRevision{}, err
	}
	r, err := s.read().GetFlowRevision(ctx, store.GetFlowRevisionParams{CompanyID: companyID, FlowID: flowID, Revision: i32(revision)})
	if err != nil {
		return domain.FlowRevision{}, mapErr(err, "get flow revision")
	}
	return flowRevisionFromRow(r), nil
}

// RestoreFlowRevision reaplica a definição de uma revisão como uma edição nova (a história só cresce). Os gatilhos não
// são restaurados: eles carregam endereço e segredo da instalação.
func (s *Service) RestoreFlowRevision(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, revision int) (FlowView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return FlowView{}, err
	}
	rev, err := s.GetFlowRevision(ctx, companyID, flowID, revision)
	if err != nil {
		return FlowView{}, err
	}
	sn := rev.Snapshot
	prio := domain.Priority(sn.Priority)
	in := UpdateFlowInput{Name: &sn.Name, Description: &sn.Description, Cron: &sn.Cron, Timezone: &sn.Timezone,
		TaskTitle: &sn.TaskTitle, TaskDescription: &sn.TaskDescription, PersonaID: &sn.PersonaID, Priority: &prio,
		Variables: &sn.Variables}
	if sn.ProjectID != nil {
		in.ProjectID = sn.ProjectID
	} else {
		in.ClearProject = true
	}
	return s.updateFlow(ctx, actor, companyID, flowID, in, "restore", &revision)
}

// ---------- arquivar ----------

// ArchiveFlow arquiva (ou desarquiva) um fluxo. Arquivado: sai da lista padrão e não dispara por caminho nenhum; volta
// desativado ao desarquivar (ADR-0047 §C).
func (s *Service) ArchiveFlow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, archive bool) (FlowView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return FlowView{}, err
	}
	var out FlowView
	err := s.withTx(ctx, func(t *txc) error {
		f, err := t.q.GetFlowByIDForUpdate(ctx, flowID)
		if err != nil || f.CompanyID != companyID {
			return notFound("get flow")
		}
		if archive == (f.ArchivedAt != nil) {
			out, err = loadFlowView(ctx, t.q, companyID, flowID)
			return err
		}
		var at *time.Time
		verb := "flow.unarchived"
		if archive {
			at, verb = &t.now, "flow.archived"
		}
		if err := t.q.SetFlowArchived(ctx, store.SetFlowArchivedParams{ID: flowID, ArchivedAt: at, Enabled: false, NextFireAt: nil, Now: t.now}); err != nil {
			return fmt.Errorf("archive flow: %w", err)
		}
		if err := t.activity(actor, companyID, verb, "flow", flowID, map[string]any{"slug": f.Slug}, ""); err != nil {
			return err
		}
		out, err = loadFlowView(ctx, t.q, companyID, flowID)
		return err
	})
	return out, err
}

// ---------- CRUD de gatilhos ----------

// CreateFlowTriggerInput define um gatilho novo.
type CreateFlowTriggerInput struct {
	Kind        domain.FlowTriggerKind
	Name        string
	Enabled     *bool // padrão true
	SecretRef   string
	RatePerMin  int
	EventVerb   string
	EventFilter map[string]string
	Mapping     map[string]string
}

// UpdateFlowTriggerInput muda um gatilho; nil mantém o valor.
type UpdateFlowTriggerInput struct {
	Name        *string
	Enabled     *bool
	RatePerMin  *int
	EventVerb   *string
	EventFilter *map[string]string
	Mapping     *map[string]string
}

// soleSecretRef exige que s seja exatamente uma referência ${secret:NOME} da empresa.
func soleSecretRef(s string) (string, bool) {
	refs := secretref.Find(s)
	if len(refs) != 1 || refs[0].User || strings.TrimSpace(s) != refs[0].String() {
		return "", false
	}
	return refs[0].Name, true
}

// CreateFlowTrigger cria um gatilho. Webhook sem secret_ref ganha um segredo gerado (valor devolvido uma vez).
func (s *Service) CreateFlowTrigger(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, in CreateFlowTriggerInput) (FlowTriggerView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return FlowTriggerView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return FlowTriggerView{}, domain.ErrForbidden
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > domain.MaxFlowTriggerName {
		return FlowTriggerView{}, domain.Invalid("name", fmt.Sprintf("1 a %d caracteres", domain.MaxFlowTriggerName))
	}
	if !in.Kind.Valid() {
		return FlowTriggerView{}, domain.Invalid("kind", "use webhook ou event")
	}
	rate := in.RatePerMin
	if rate == 0 {
		rate = domain.DefaultTriggerRatePerMin
	}
	if rate < 1 || rate > domain.MaxTriggerRatePerMin {
		return FlowTriggerView{}, domain.Invalid("rate_per_min", fmt.Sprintf("1 a %d", domain.MaxTriggerRatePerMin))
	}
	filter, err := domain.NormalizeEventFilter(in.EventFilter)
	if err != nil {
		return FlowTriggerView{}, err
	}
	verb := strings.TrimSpace(in.EventVerb)
	switch in.Kind {
	case domain.TriggerKindEvent:
		if err := domain.ValidEventVerb(verb); err != nil {
			return FlowTriggerView{}, err
		}
		if in.SecretRef != "" {
			return FlowTriggerView{}, domain.Invalid("secret_ref", "só vale para webhook")
		}
	default:
		if verb != "" || len(filter) > 0 {
			return FlowTriggerView{}, domain.Invalid("event_verb", "só vale para gatilho de evento")
		}
	}
	var out FlowTriggerView
	err = s.withTx(ctx, func(t *txc) error {
		f, err := t.q.GetFlowByIDForUpdate(ctx, flowID)
		if err != nil || f.CompanyID != companyID {
			return notFound("get flow")
		}
		if f.ArchivedAt != nil {
			return errFlowArchived()
		}
		if n, err := t.q.CountFlowTriggers(ctx, flowID); err != nil {
			return fmt.Errorf("count flow triggers: %w", err)
		} else if n >= domain.MaxFlowTriggers {
			return domain.Invalid("name", fmt.Sprintf("o fluxo já tem %d gatilhos (o máximo)", domain.MaxFlowTriggers))
		}
		mapping, err := domain.NormalizeTriggerMapping(in.Mapping, flowVarsOf(f.Variables))
		if err != nil {
			return err
		}
		id := domain.NewID()
		ref, revealed := strings.TrimSpace(in.SecretRef), ""
		if in.Kind == domain.TriggerKindWebhook {
			if ref != "" {
				if _, ok := soleSecretRef(ref); !ok {
					return domain.Invalid("secret_ref", "use uma única referência ${secret:NOME}")
				}
				if err := s.checkSecretRefs(ctx, t.q, companyID, "secret_ref", ref); err != nil {
					return err
				}
			} else {
				secName := genHookSecretName(f.Slug, id)
				sid := uuid.New()
				m, err := s.prepareMaterial(sid, "", "", true, "")
				if err != nil {
					return err
				}
				var sv SecretView
				if err := s.createSecretTx(t, actor, companyID, domain.SecretScopeCompany, nil, sid, secName,
					"Segredo HMAC do gatilho "+name+" do fluxo "+f.Slug, m, &sv); err != nil {
					return err
				}
				ref, revealed = "${secret:"+secName+"}", sv.RevealedValue
			}
		}
		fb, _ := json.Marshal(filter)
		mb, _ := json.Marshal(mapping)
		enabled := in.Enabled == nil || *in.Enabled
		row, err := t.q.CreateFlowTrigger(ctx, store.CreateFlowTriggerParams{ID: id, CompanyID: companyID, FlowID: flowID, Kind: string(in.Kind),
			Name: name, Enabled: enabled, SecretRef: ref, RatePerMin: i32(rate), EventVerb: verb, EventFilter: fb, Mapping: mb, Now: t.now})
		if err != nil {
			return mapErr(err, "create flow trigger")
		}
		if err := t.activity(actor, companyID, "flow.trigger_created", "flow", flowID,
			map[string]any{"trigger_id": id, "kind": in.Kind, "name": name, "event_verb": verb}, ""); err != nil {
			return err
		}
		if err := t.bumpFlowRevision(actor, flowID, "triggers"); err != nil {
			return err
		}
		out = triggerView(row)
		out.SecretValue = revealed
		return nil
	})
	return out, err
}

func genHookSecretName(slug string, id domain.ID) string {
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return "hook-" + strings.TrimRight(slug, "-") + "-" + strings.ReplaceAll(id.String(), "-", "")[:8]
}

// ListFlowTriggers lista os gatilhos de um fluxo.
func (s *Service) ListFlowTriggers(ctx context.Context, companyID, flowID domain.ID) ([]FlowTriggerView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetFlowByID(ctx, store.GetFlowByIDParams{CompanyID: companyID, ID: flowID}); err != nil {
		return nil, mapErr(err, "get flow")
	}
	rows, err := q.ListFlowTriggers(ctx, store.ListFlowTriggersParams{CompanyID: companyID, FlowID: flowID})
	if err != nil {
		return nil, fmt.Errorf("list flow triggers: %w", err)
	}
	out := make([]FlowTriggerView, len(rows))
	for i, r := range rows {
		out[i] = triggerView(r)
	}
	return out, nil
}

// UpdateFlowTrigger muda um gatilho.
func (s *Service) UpdateFlowTrigger(ctx context.Context, actor domain.Actor, companyID, flowID, triggerID domain.ID, in UpdateFlowTriggerInput) (FlowTriggerView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return FlowTriggerView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return FlowTriggerView{}, domain.ErrForbidden
	}
	var out FlowTriggerView
	err := s.withTx(ctx, func(t *txc) error {
		f, err := t.q.GetFlowByIDForUpdate(ctx, flowID)
		if err != nil || f.CompanyID != companyID {
			return notFound("get flow")
		}
		if f.ArchivedAt != nil {
			return errFlowArchived()
		}
		cur, err := t.q.GetFlowTrigger(ctx, store.GetFlowTriggerParams{CompanyID: companyID, FlowID: flowID, ID: triggerID})
		if err != nil {
			return mapErr(err, "get flow trigger")
		}
		tr := flowTriggerFromRow(cur)
		var changed []string
		if in.Name != nil && strings.TrimSpace(*in.Name) != tr.Name {
			n := strings.TrimSpace(*in.Name)
			if n == "" || len([]rune(n)) > domain.MaxFlowTriggerName {
				return domain.Invalid("name", fmt.Sprintf("1 a %d caracteres", domain.MaxFlowTriggerName))
			}
			tr.Name = n
			changed = append(changed, "name")
		}
		if in.Enabled != nil && *in.Enabled != tr.Enabled {
			tr.Enabled = *in.Enabled
			changed = append(changed, "enabled")
		}
		if in.RatePerMin != nil && *in.RatePerMin != tr.RatePerMin {
			if *in.RatePerMin < 1 || *in.RatePerMin > domain.MaxTriggerRatePerMin {
				return domain.Invalid("rate_per_min", fmt.Sprintf("1 a %d", domain.MaxTriggerRatePerMin))
			}
			tr.RatePerMin = *in.RatePerMin
			changed = append(changed, "rate_per_min")
		}
		if in.EventVerb != nil && strings.TrimSpace(*in.EventVerb) != tr.EventVerb {
			if tr.Kind != domain.TriggerKindEvent {
				return domain.Invalid("event_verb", "só vale para gatilho de evento")
			}
			v := strings.TrimSpace(*in.EventVerb)
			if err := domain.ValidEventVerb(v); err != nil {
				return err
			}
			tr.EventVerb = v
			changed = append(changed, "event_verb")
		}
		if in.EventFilter != nil {
			if tr.Kind != domain.TriggerKindEvent && len(*in.EventFilter) > 0 {
				return domain.Invalid("event_filter", "só vale para gatilho de evento")
			}
			nf, err := domain.NormalizeEventFilter(*in.EventFilter)
			if err != nil {
				return err
			}
			tr.EventFilter = nf
			changed = append(changed, "event_filter")
		}
		if in.Mapping != nil {
			nm, err := domain.NormalizeTriggerMapping(*in.Mapping, flowVarsOf(f.Variables))
			if err != nil {
				return err
			}
			tr.Mapping = nm
			changed = append(changed, "mapping")
		}
		if len(changed) == 0 {
			out = triggerView(cur)
			return nil
		}
		fb, _ := json.Marshal(nonNilMap(tr.EventFilter))
		mb, _ := json.Marshal(nonNilMap(tr.Mapping))
		row, err := t.q.UpdateFlowTrigger(ctx, store.UpdateFlowTriggerParams{CompanyID: companyID, FlowID: flowID, ID: triggerID, Name: tr.Name,
			Enabled: tr.Enabled, RatePerMin: i32(tr.RatePerMin), EventVerb: tr.EventVerb, EventFilter: fb, Mapping: mb, Now: t.now})
		if err != nil {
			return mapErr(err, "update flow trigger")
		}
		if err := t.activity(actor, companyID, "flow.trigger_updated", "flow", flowID,
			map[string]any{"trigger_id": triggerID, "fields": changed}, ""); err != nil {
			return err
		}
		if err := t.bumpFlowRevision(actor, flowID, "triggers"); err != nil {
			return err
		}
		out = triggerView(row)
		return nil
	})
	return out, err
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// DeleteFlowTrigger remove um gatilho. O segredo gerado para ele continua em Segredos (apagar é decisão de quem o criou).
func (s *Service) DeleteFlowTrigger(ctx context.Context, actor domain.Actor, companyID, flowID, triggerID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		f, err := t.q.GetFlowByIDForUpdate(ctx, flowID)
		if err != nil || f.CompanyID != companyID {
			return notFound("get flow")
		}
		if f.ArchivedAt != nil {
			return errFlowArchived()
		}
		n, err := t.q.DeleteFlowTrigger(ctx, store.DeleteFlowTriggerParams{CompanyID: companyID, FlowID: flowID, ID: triggerID})
		if err != nil {
			return fmt.Errorf("delete flow trigger: %w", err)
		}
		if n == 0 {
			return notFound("get flow trigger")
		}
		if err := t.activity(actor, companyID, "flow.trigger_deleted", "flow", flowID, map[string]any{"trigger_id": triggerID}, ""); err != nil {
			return err
		}
		return t.bumpFlowRevision(actor, flowID, "triggers")
	})
}

// ---------- disparo por gatilho ----------

// takeBucket consome um token do balde key na própria transação (sem pegar outra conexão do pool).
func (t *txc) takeBucket(key string, perMin int64) bool {
	p := ratelimit.PerMinute(perMin)
	row, err := t.q.TakeRateToken(t.ctx, store.TakeRateTokenParams{Key: key, Capacity: p.Capacity, Cost: 1, Now: t.now, RefillPerSec: p.RefillPerSec})
	return err != nil || row.LastAllowed // erro do balde não derruba o fato: deixa passar
}

// triggerAllows diz se o gatilho ainda tem token no minuto (rate_per_min).
func (t *txc) triggerAllows(tr store.FlowTrigger) bool {
	return t.takeBucket("flowtrigger:"+tr.ID.String(), int64(tr.RatePerMin))
}

// fireTriggered cria o disparo de um gatilho (webhook ou evento) dentro da transação com o fluxo já travado. Variáveis
// obrigatórias faltando → ErrInvalid sem escrever nada.
func (t *txc) fireTriggered(f store.Flow, tr store.FlowTrigger, src domain.FlowFireSource, dedupe string, given map[string]string) (FlowFireResult, error) {
	ctx := t.ctx
	var out FlowFireResult
	vars, err := domain.ResolveFlowVariables(flowVarsOf(f.Variables), given, false)
	if err != nil {
		return out, err
	}
	raw, _ := json.Marshal(vars)
	trID := tr.ID
	at := t.now.Truncate(time.Microsecond)
	fire, err := t.q.InsertFlowFire(ctx, store.InsertFlowFireParams{ID: domain.NewID(), FlowID: f.ID, CompanyID: f.CompanyID,
		ScheduledFor: at, Manual: true, Source: string(src), TriggerID: &trID, DedupeKey: dedupe, Variables: raw, Now: t.now})
	if isNoRows(err) {
		old, gerr := t.q.GetFlowFireByDedupe(ctx, store.GetFlowFireByDedupeParams{FlowID: f.ID, DedupeKey: dedupe})
		if gerr != nil {
			return out, mapErr(gerr, "get flow fire")
		}
		out.Fire, out.Duplicate = flowFireFromRow(old), true
		if old.TaskID != nil {
			if tv, terr := loadTaskView(ctx, t.q, f.CompanyID, *old.TaskID); terr == nil {
				out.Task = &tv
			}
		}
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("insert flow fire: %w", err)
	}
	task, err := t.createFlowTask(domain.SystemActor, f, &fire, at, src, vars)
	if err != nil {
		return out, err
	}
	if err := t.q.MarkFlowTriggerFired(ctx, store.MarkFlowTriggerFiredParams{ID: tr.ID, At: &t.now}); err != nil {
		return out, fmt.Errorf("mark trigger fired: %w", err)
	}
	if err := t.q.MarkFlowFired(ctx, store.MarkFlowFiredParams{ID: f.ID, LastFiredAt: &t.now, NextFireAt: f.NextFireAt, Now: t.now}); err != nil {
		return out, fmt.Errorf("mark flow fired: %w", err)
	}
	if err := t.activity(domain.SystemActor, f.CompanyID, "flow.fired", "flow", f.ID, map[string]any{
		"task_id": task.ID, "task_key": task.Key, "source": src, "trigger_id": tr.ID, "trigger": tr.Name, "variables": sortedKeys(vars)}, ""); err != nil {
		return out, err
	}
	t.publish(companyTopic(f.CompanyID), "flow.fired", map[string]any{"flow_id": f.ID, "task_id": task.ID})
	out.Fire, out.Task = flowFireFromRow(fire), &task
	return out, nil
}

// WebhookFireInput é uma entrega recebida no webhook de um gatilho.
type WebhookFireInput struct {
	TriggerID domain.ID
	Body      []byte
	Signature string // X-Hanko-Signature
	Delivery  string // X-Hanko-Delivery ou Idempotency-Key; vazio = sem deduplicação
}

// FireFlowWebhook trata uma entrega no webhook de um gatilho: confere o HMAC, o estado do fluxo e o limite, extrai as
// variáveis da carga e cria o disparo. Erros: domain.ErrNotFound (gatilho), ErrWebhookUnauthorized, domain.ErrConflict
// (fluxo desativado/arquivado), ErrWebhookThrottled e domain.ErrInvalid (variável obrigatória faltando).
func (s *Service) FireFlowWebhook(ctx context.Context, in WebhookFireInput) (FlowFireResult, error) {
	ctx, span := otel.Tracer("hanko/flows").Start(ctx, "hanko.flow.webhook", trace.WithAttributes(attribute.String("hanko.trigger.id", in.TriggerID.String())))
	defer span.End()
	fail := func(result string, err error) (FlowFireResult, error) {
		countFlowFire(ctx, domain.FireWebhook, result)
		return FlowFireResult{}, err
	}
	tr, err := s.read().GetFlowTriggerByID(ctx, in.TriggerID)
	if err != nil || tr.Kind != string(domain.TriggerKindWebhook) {
		return fail("not_found", notFound("flow trigger"))
	}
	secret, _, err := s.ResolveSecretText(ctx, tr.CompanyID, tr.SecretRef)
	if err != nil || secret == "" {
		return fail("no_secret", ErrWebhookUnauthorized)
	}
	if err := webhook.Verify(secret, in.Signature, in.Body, s.Now(), 0); err != nil {
		return fail("unauthorized", ErrWebhookUnauthorized)
	}
	var doc any = map[string]any{}
	if len(strings.TrimSpace(string(in.Body))) > 0 {
		if err := json.Unmarshal(in.Body, &doc); err != nil {
			doc = nil
		}
	}
	var out FlowFireResult
	err = s.withTx(ctx, func(t *txc) error {
		f, err := t.q.GetFlowByIDForUpdate(ctx, tr.FlowID)
		if err != nil {
			return notFound("get flow")
		}
		cur, err := t.q.GetFlowTriggerByID(ctx, tr.ID)
		if err != nil {
			return notFound("flow trigger")
		}
		if !cur.Enabled || !f.Enabled || f.ArchivedAt != nil {
			return fmt.Errorf("o gatilho ou o fluxo está desativado ou arquivado: %w", domain.ErrConflict)
		}
		if !t.triggerAllows(cur) {
			return ErrWebhookThrottled
		}
		decl := flowVarsOf(f.Variables)
		given := map[string]string{}
		if _, isObj := doc.(map[string]any); isObj {
			given = domain.MapVariables(decl, flowTriggerFromRow(cur).Mapping, doc)
		}
		delivery := strings.TrimSpace(in.Delivery)
		if len(delivery) > 200 {
			delivery = delivery[:200]
		}
		out, err = t.fireTriggered(f, cur, domain.FireWebhook, delivery, given)
		return err
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrWebhookThrottled):
			return fail("throttled", err)
		case errors.Is(err, domain.ErrInvalid):
			return fail("invalid", err)
		case errors.Is(err, domain.ErrConflict):
			return fail("inactive", err)
		}
		return fail("error", err)
	}
	if out.Duplicate {
		countFlowFire(ctx, domain.FireWebhook, "duplicate")
	} else {
		countFlowFire(ctx, domain.FireWebhook, "fired")
	}
	return out, nil
}

// dispatchEventTriggers avalia os gatilhos de evento contra o fato recém-gravado, na mesma transação. Chamada por
// txc.activity. Fatos flow.* e os gerados enquanto um disparo cria a sua task não disparam nada.
func (t *txc) dispatchEventTriggers(companyID domain.ID, verb string, actor domain.Actor, subjectType string, subjectID domain.ID, data json.RawMessage, activityID domain.ID) error {
	if t.flowFiring || strings.HasPrefix(verb, "flow.") {
		return nil
	}
	trs, err := t.q.ListEventTriggers(t.ctx, companyID)
	if err != nil {
		return fmt.Errorf("list event triggers: %w", err)
	}
	var doc map[string]any
	for _, tr := range trs {
		if !domain.MatchEventVerb(tr.EventVerb, verb) {
			continue
		}
		if doc == nil {
			var d any
			_ = json.Unmarshal(data, &d)
			a := map[string]any{"type": string(actor.Type)}
			if actor.ID != nil {
				a["id"] = actor.ID.String()
			}
			doc = map[string]any{"verb": verb, "subject_type": subjectType, "subject_id": subjectID.String(), "actor": a, "data": d}
		}
		trig := flowTriggerFromRow(tr)
		if !domain.EventFilterMatches(trig.EventFilter, doc) {
			continue
		}
		f, err := t.q.GetFlowByIDForUpdate(t.ctx, tr.FlowID)
		if err != nil {
			return mapErr(err, "get flow")
		}
		if !f.Enabled || f.ArchivedAt != nil {
			continue
		}
		if !t.triggerAllows(tr) {
			if t.takeBucket("flowtrigger-note:"+tr.ID.String(), 1) {
				if err := t.activity(domain.SystemActor, companyID, "flow.throttled", "flow", f.ID,
					map[string]any{"trigger_id": tr.ID, "trigger": tr.Name, "verb": verb}, ""); err != nil {
					return err
				}
			}
			countFlowFire(t.ctx, domain.FireEvent, "throttled")
			continue
		}
		given := domain.MapVariables(flowVarsOf(f.Variables), trig.Mapping, doc)
		if _, err := t.fireTriggered(f, tr, domain.FireEvent, activityID.String()+"/"+tr.ID.String(), given); err != nil {
			if errors.Is(err, domain.ErrInvalid) { // variável obrigatória faltando: o fato original não pode falhar por isso
				countFlowFire(t.ctx, domain.FireEvent, "invalid")
				if aerr := t.activity(domain.SystemActor, companyID, "flow.skipped", "flow", f.ID,
					map[string]any{"reason": "variables", "detail": err.Error(), "trigger_id": tr.ID, "verb": verb}, ""); aerr != nil {
					return aerr
				}
				continue
			}
			return err
		}
		countFlowFire(t.ctx, domain.FireEvent, "fired")
	}
	return nil
}

// importFlowVariables converte as variáveis do arquivo e as valida como o cadastro faz.
func importFlowVariables(x export.Flow) ([]domain.FlowVariable, error) {
	in := make([]domain.FlowVariable, len(x.Variables))
	for i, v := range x.Variables {
		in[i] = domain.FlowVariable{Name: v.Name, Label: v.Label, Required: v.Required, Default: v.Default}
	}
	return domain.NormalizeFlowVariables(in)
}

// afterFlowImport grava o que o ImportFlow não leva: variáveis, estado arquivado e a revisão (origem import). replace
// diz que o fluxo já existia (a revisão avança em vez de começar em 1).
func (t *txc) afterFlowImport(flowID domain.ID, x export.Flow, replace bool) error {
	vars, err := importFlowVariables(x)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(vars)
	f, err := t.q.GetFlowByIDForUpdate(t.ctx, flowID)
	if err != nil {
		return mapErr(err, "get flow")
	}
	rev := 1
	if replace {
		rev = int(f.Revision) + 1
	}
	if err := t.q.UpdateFlowExtras(t.ctx, store.UpdateFlowExtrasParams{ID: flowID, Variables: raw, Revision: int32(rev), Now: t.now}); err != nil { //nolint:gosec // número de revisão pequeno
		return fmt.Errorf("import flow variables: %w", err)
	}
	if err := t.insertFlowRevision(domain.SystemActor, flowID, rev, "import", "", nil); err != nil {
		return err
	}
	if x.Archived {
		return t.q.SetFlowArchived(t.ctx, store.SetFlowArchivedParams{ID: flowID, ArchivedAt: &t.now, Enabled: false, NextFireAt: nil, Now: t.now})
	}
	return nil
}
