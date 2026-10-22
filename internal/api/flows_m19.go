package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Variáveis, gatilhos, revisões e arquivar de fluxos (ADR-0047).

// flowsBackend é a parte do service que o M19 acrescenta aos fluxos; opcional como secretsBackend.
type flowsBackend interface {
	ListFlowsFiltered(ctx context.Context, companyID domain.ID, archived bool) ([]service.FlowView, error)
	RunFlowNowWith(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, vars map[string]string) (service.FlowFireResult, error)
	ArchiveFlow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, archive bool) (service.FlowView, error)
	ListFlowTriggers(ctx context.Context, companyID, flowID domain.ID) ([]service.FlowTriggerView, error)
	CreateFlowTrigger(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, in service.CreateFlowTriggerInput) (service.FlowTriggerView, error)
	UpdateFlowTrigger(ctx context.Context, actor domain.Actor, companyID, flowID, triggerID domain.ID, in service.UpdateFlowTriggerInput) (service.FlowTriggerView, error)
	DeleteFlowTrigger(ctx context.Context, actor domain.Actor, companyID, flowID, triggerID domain.ID) error
	FlowRevisions(ctx context.Context, companyID, flowID domain.ID, limit int) ([]domain.FlowRevision, error)
	GetFlowRevision(ctx context.Context, companyID, flowID domain.ID, revision int) (domain.FlowRevision, error)
	RestoreFlowRevision(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, revision int) (service.FlowView, error)
}

func (s *server) flowsM19() (flowsBackend, error) {
	b, ok := s.svc.(flowsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "gatilhos e revisões de fluxo indisponíveis neste servidor")
	}
	return b, nil
}

// FlowVariable é uma variável declarada de um fluxo.
type FlowVariable struct {
	Name     string `json:"name" pattern:"^[a-z][a-z0-9_]{0,39}$" example:"repo" doc:"Minúsculas, dígitos e _. Não pode ser data, n, semana nem disparo."`
	Label    string `json:"label,omitempty" maxLength:"80"`
	Required bool   `json:"required,omitempty" doc:"Obrigatória no Rodar agora e na carga do gatilho; a agenda exige padrão."`
	Default  string `json:"default,omitempty" maxLength:"2000"`
}

func flowVarsIn(in []FlowVariable) []domain.FlowVariable {
	out := make([]domain.FlowVariable, len(in))
	for i, v := range in {
		out[i] = domain.FlowVariable{Name: v.Name, Label: v.Label, Required: v.Required, Default: v.Default}
	}
	return out
}

func flowVarsOut(in []domain.FlowVariable) []FlowVariable {
	out := make([]FlowVariable, len(in))
	for i, v := range in {
		out[i] = FlowVariable{Name: v.Name, Label: v.Label, Required: v.Required, Default: v.Default}
	}
	return out
}

// FlowTrigger é um gatilho de fluxo.
type FlowTrigger struct {
	ID          string            `json:"id" format:"uuid"`
	FlowID      string            `json:"flow_id" format:"uuid"`
	Kind        string            `json:"kind" enum:"webhook,event"`
	Name        string            `json:"name"`
	Enabled     bool              `json:"enabled"`
	SecretRef   string            `json:"secret_ref,omitempty" doc:"Referência ${secret:NOME} do HMAC (webhook); o valor está em Segredos."`
	RatePerMin  int               `json:"rate_per_min"`
	Path        string            `json:"path,omitempty" doc:"Caminho do webhook de entrada, relativo à URL pública: POST com X-Hanko-Signature."`
	EventVerb   string            `json:"event_verb,omitempty" example:"gate.decided"`
	EventFilter map[string]string `json:"event_filter,omitempty"`
	Mapping     map[string]string `json:"mapping,omitempty" doc:"{variável: caminho.pontuado} da carga (webhook) ou do fato (evento: data.<campo>, subject_id, verb, actor.id)."`
	SecretValue string            `json:"secret_value,omitempty" doc:"Só na criação, quando o servidor gerou o segredo: guarde agora."`
	LastFiredAt *time.Time        `json:"last_fired_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

func triggerOut(t service.FlowTriggerView) FlowTrigger {
	return FlowTrigger{ID: idStr(t.ID), FlowID: idStr(t.FlowID), Kind: string(t.Kind), Name: t.Name, Enabled: t.Enabled, SecretRef: t.SecretRef,
		RatePerMin: t.RatePerMin, Path: t.Path, EventVerb: t.EventVerb, EventFilter: t.EventFilter, Mapping: t.Mapping,
		SecretValue: t.SecretValue, LastFiredAt: t.LastFiredAt, CreatedAt: t.CreatedAt}
}

func triggersOut(in []domain.FlowTrigger) []FlowTrigger {
	if len(in) == 0 {
		return nil
	}
	out := make([]FlowTrigger, len(in))
	for i, t := range in {
		v := service.FlowTriggerView{FlowTrigger: t}
		if t.Kind == domain.TriggerKindWebhook {
			v.Path = service.FlowWebhookPath(t.ID)
		}
		out[i] = triggerOut(v)
	}
	return out
}

// FlowRevision é uma revisão append-only da definição do fluxo.
type FlowRevision struct {
	Revision     int       `json:"revision"`
	Summary      string    `json:"summary,omitempty" doc:"Campos alterados."`
	Origin       string    `json:"origin" enum:"backfill,create,edit,restore,import"`
	RestoredFrom *int      `json:"restored_from,omitempty"`
	Actor        ActorRef  `json:"actor"`
	Snapshot     FlowSnap  `json:"snapshot"`
	CreatedAt    time.Time `json:"created_at"`
}

// FlowSnap é a definição guardada numa revisão (sem segredos).
type FlowSnap struct {
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	Cron            string         `json:"cron,omitempty"`
	Timezone        string         `json:"timezone"`
	PersonaID       string         `json:"persona_id" format:"uuid"`
	ProjectID       string         `json:"project_id,omitempty" format:"uuid"`
	Priority        string         `json:"priority"`
	TaskTitle       string         `json:"task_title"`
	TaskDescription string         `json:"task_description"`
	Variables       []FlowVariable `json:"variables"`
	Triggers        []FlowSnapTrig `json:"triggers"`
}

// FlowSnapTrig é um gatilho dentro de uma revisão.
type FlowSnapTrig struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	RatePerMin int    `json:"rate_per_min"`
	EventVerb  string `json:"event_verb,omitempty"`
}

func revisionOut(r domain.FlowRevision) FlowRevision {
	sn := r.Snapshot
	out := FlowRevision{Revision: r.Revision, Summary: r.Summary, Origin: r.Origin, RestoredFrom: r.RestoredFrom,
		Actor: actorRef(r.Actor), CreatedAt: r.CreatedAt,
		Snapshot: FlowSnap{Name: sn.Name, Description: sn.Description, Cron: sn.Cron, Timezone: sn.Timezone, PersonaID: idStr(sn.PersonaID),
			ProjectID: idPtr(sn.ProjectID), Priority: sn.Priority, TaskTitle: sn.TaskTitle, TaskDescription: sn.TaskDescription,
			Variables: flowVarsOut(sn.Variables), Triggers: []FlowSnapTrig{}}}
	for _, t := range sn.Triggers {
		out.Snapshot.Triggers = append(out.Snapshot.Triggers, FlowSnapTrig{Kind: string(t.Kind), Name: t.Name, Enabled: t.Enabled,
			RatePerMin: t.RatePerMin, EventVerb: t.EventVerb})
	}
	return out
}

type flowTriggerPath struct {
	FlowPath
	Trigger string `path:"trigger" format:"uuid" doc:"Id do gatilho."`
}

type flowRevisionPath struct {
	FlowPath
	Revision int `path:"revision" minimum:"1"`
}

type createFlowTriggerIn struct {
	FlowPath
	Body struct {
		Kind        string            `json:"kind" enum:"webhook,event"`
		Name        string            `json:"name" minLength:"1" maxLength:"80" example:"github"`
		Enabled     *bool             `json:"enabled,omitempty" doc:"Padrão true."`
		SecretRef   string            `json:"secret_ref,omitempty" example:"${secret:hook-github}" doc:"Webhook: segredo HMAC existente. Omitido, o servidor gera um (valor devolvido uma vez)."`
		RatePerMin  int               `json:"rate_per_min,omitempty" minimum:"0" maximum:"600" doc:"Entregas por minuto (padrão 30)."`
		EventVerb   string            `json:"event_verb,omitempty" example:"task.created" doc:"Evento: verbo de atividade (task.created) ou prefixo (gate.*)."`
		EventFilter map[string]string `json:"event_filter,omitempty" doc:"Evento: {caminho: texto} que o fato precisa cumprir (data.status)."`
		Mapping     map[string]string `json:"mapping,omitempty"`
	}
}

type updateFlowTriggerIn struct {
	CompanyPath
	Flow    string `path:"flow" doc:"Id ou slug do fluxo."`
	Trigger string `path:"trigger" format:"uuid" doc:"Id do gatilho."`
	Body    struct {
		Name        *string            `json:"name,omitempty"`
		Enabled     *bool              `json:"enabled,omitempty"`
		RatePerMin  *int               `json:"rate_per_min,omitempty" minimum:"1" maximum:"600"`
		EventVerb   *string            `json:"event_verb,omitempty"`
		EventFilter *map[string]string `json:"event_filter,omitempty"`
		Mapping     *map[string]string `json:"mapping,omitempty"`
	}
}

type listRevisionsIn struct {
	FlowPath
	Limit int `query:"limit" minimum:"1" maximum:"500" default:"50"`
}

func (s *server) registerFlowsM19(api huma.API) {
	type flowCtx struct {
		fb flowsBackend
		sc scope
		f  service.FlowView
	}
	prep := func(ctx context.Context, company, flow string, write bool) (flowCtx, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return flowCtx{}, err
			}
		}
		fb, err := s.flowsM19()
		if err != nil {
			return flowCtx{}, err
		}
		sc, err := s.scope(ctx, company)
		if err != nil {
			return flowCtx{}, err
		}
		f, err := s.svc.GetFlow(ctx, sc.Company.ID, flow)
		if err != nil {
			return flowCtx{}, s.fail(ctx, err)
		}
		return flowCtx{fb: fb, sc: sc, f: f}, nil
	}

	huma.Register(api, op("archive-flow", http.MethodPost, "/companies/{company}/flows/{flow}/archive", "flows", "Arquiva um fluxo (some da lista; não dispara)"),
		func(ctx context.Context, in *FlowPath) (*ItemOut[Flow], error) {
			c, err := prep(ctx, in.Company, in.Flow, true)
			if err != nil {
				return nil, err
			}
			f, err := c.fb.ArchiveFlow(ctx, c.sc.Actor, c.sc.Company.ID, c.f.ID, true)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Flow]{Body: flowOut(f)}, nil
		})

	huma.Register(api, op("unarchive-flow", http.MethodPost, "/companies/{company}/flows/{flow}/unarchive", "flows", "Desarquiva um fluxo (volta desativado)"),
		func(ctx context.Context, in *FlowPath) (*ItemOut[Flow], error) {
			c, err := prep(ctx, in.Company, in.Flow, true)
			if err != nil {
				return nil, err
			}
			f, err := c.fb.ArchiveFlow(ctx, c.sc.Actor, c.sc.Company.ID, c.f.ID, false)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Flow]{Body: flowOut(f)}, nil
		})

	huma.Register(api, op("list-flow-triggers", http.MethodGet, "/companies/{company}/flows/{flow}/triggers", "flows", "Lista os gatilhos de um fluxo"),
		func(ctx context.Context, in *FlowPath) (*ListOut[FlowTrigger], error) {
			c, err := prep(ctx, in.Company, in.Flow, false)
			if err != nil {
				return nil, err
			}
			ts, err := c.fb.ListFlowTriggers(ctx, c.sc.Company.ID, c.f.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ts, Page{Limit: len(ts)}, triggerOut), nil
		})

	huma.Register(api, created(op("create-flow-trigger", http.MethodPost, "/companies/{company}/flows/{flow}/triggers", "flows", "Cria um gatilho (webhook HMAC ou evento interno)")),
		func(ctx context.Context, in *createFlowTriggerIn) (*ItemOut[FlowTrigger], error) {
			c, err := prep(ctx, in.Company, in.Flow, true)
			if err != nil {
				return nil, err
			}
			b := in.Body
			t, err := c.fb.CreateFlowTrigger(ctx, c.sc.Actor, c.sc.Company.ID, c.f.ID, service.CreateFlowTriggerInput{
				Kind: domain.FlowTriggerKind(b.Kind), Name: b.Name, Enabled: b.Enabled, SecretRef: b.SecretRef, RatePerMin: b.RatePerMin,
				EventVerb: b.EventVerb, EventFilter: b.EventFilter, Mapping: b.Mapping})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[FlowTrigger]{Body: triggerOut(t)}, nil
		})

	huma.Register(api, op("update-flow-trigger", http.MethodPatch, "/companies/{company}/flows/{flow}/triggers/{trigger}", "flows", "Atualiza um gatilho"),
		func(ctx context.Context, in *updateFlowTriggerIn) (*ItemOut[FlowTrigger], error) {
			c, err := prep(ctx, in.Company, in.Flow, true)
			if err != nil {
				return nil, err
			}
			tid, err := domain.ParseID(in.Trigger)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("trigger", "id inválido"))
			}
			b := in.Body
			t, err := c.fb.UpdateFlowTrigger(ctx, c.sc.Actor, c.sc.Company.ID, c.f.ID, tid, service.UpdateFlowTriggerInput{
				Name: b.Name, Enabled: b.Enabled, RatePerMin: b.RatePerMin, EventVerb: b.EventVerb, EventFilter: b.EventFilter, Mapping: b.Mapping})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[FlowTrigger]{Body: triggerOut(t)}, nil
		})

	huma.Register(api, noContent(op("delete-flow-trigger", http.MethodDelete, "/companies/{company}/flows/{flow}/triggers/{trigger}", "flows", "Remove um gatilho")),
		func(ctx context.Context, in *flowTriggerPath) (*struct{}, error) {
			c, err := prep(ctx, in.Company, in.Flow, true)
			if err != nil {
				return nil, err
			}
			tid, err := domain.ParseID(in.Trigger)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("trigger", "id inválido"))
			}
			if err := c.fb.DeleteFlowTrigger(ctx, c.sc.Actor, c.sc.Company.ID, c.f.ID, tid); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("list-flow-revisions", http.MethodGet, "/companies/{company}/flows/{flow}/revisions", "flows", "Revisões da definição do fluxo (append-only)"),
		func(ctx context.Context, in *listRevisionsIn) (*ListOut[FlowRevision], error) {
			c, err := prep(ctx, in.Company, in.Flow, false)
			if err != nil {
				return nil, err
			}
			rs, err := c.fb.FlowRevisions(ctx, c.sc.Company.ID, c.f.ID, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(rs, Page{Limit: in.Limit}, revisionOut), nil
		})

	huma.Register(api, op("get-flow-revision", http.MethodGet, "/companies/{company}/flows/{flow}/revisions/{revision}", "flows", "Uma revisão do fluxo"),
		func(ctx context.Context, in *flowRevisionPath) (*ItemOut[FlowRevision], error) {
			c, err := prep(ctx, in.Company, in.Flow, false)
			if err != nil {
				return nil, err
			}
			r, err := c.fb.GetFlowRevision(ctx, c.sc.Company.ID, c.f.ID, in.Revision)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[FlowRevision]{Body: revisionOut(r)}, nil
		})

	huma.Register(api, op("restore-flow-revision", http.MethodPost, "/companies/{company}/flows/{flow}/revisions/{revision}/restore", "flows", "Restaura uma revisão (vira uma edição nova; os gatilhos não voltam)"),
		func(ctx context.Context, in *flowRevisionPath) (*ItemOut[Flow], error) {
			c, err := prep(ctx, in.Company, in.Flow, true)
			if err != nil {
				return nil, err
			}
			f, err := c.fb.RestoreFlowRevision(ctx, c.sc.Actor, c.sc.Company.ID, c.f.ID, in.Revision)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Flow]{Body: flowOut(f)}, nil
		})
}
