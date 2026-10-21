package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// Gates estendidos (ADR-0041): pergunta, plano, reenvio com revisões, audiência, contratação por gate, persona
// principal e personas de sistema, política de plano e incidentes de orçamento.

type gatesPlusBackend interface {
	AnswerGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in service.AnswerGateInput) (service.GateView, error)
	DismissGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, note string) (service.GateView, error)
	ReopenGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID) (service.GateView, error)
	ResubmitGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in service.ResubmitGateInput) (service.GateView, error)
	GateRevisions(ctx context.Context, companyID, gateID domain.ID) (service.GateHistory, error)
	TaskPlanPolicy(ctx context.Context, companyID, taskID domain.ID) (service.PlanPolicyView, error)
	SetPlanPolicy(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.PlanScope, targetID domain.ID, require *bool) error
	RequireHireGate(ctx context.Context, companyID domain.ID) (bool, error)
	SetRequireHireGate(ctx context.Context, actor domain.Actor, companyID domain.ID, on bool) error
	PrimaryPersona(ctx context.Context, companyID domain.ID) (*domain.Persona, error)
	SetPrimaryPersona(ctx context.Context, actor domain.Actor, companyID domain.ID, personaID *domain.ID) (*domain.Persona, error)
	SetPersonaSystem(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, system bool) (domain.Persona, error)
	ListBudgetIncidents(ctx context.Context, companyID domain.ID, onlyOpen bool, limit int) ([]domain.BudgetIncident, error)
	ResolveBudgetIncident(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.ResolveIncidentInput) (domain.BudgetIncident, error)
}

var _ gatesPlusBackend = (*service.Service)(nil)

func (s *server) gatesPlus() (gatesPlusBackend, error) {
	b, ok := s.svc.(gatesPlusBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "gates estendidos indisponíveis neste servidor")
	}
	return b, nil
}

// ---------- DTOs ----------

// GateFormOption é uma opção de um campo de escolha.
type GateFormOption struct {
	Value string `json:"value" minLength:"1" maxLength:"300" doc:"O que volta na resposta."`
	Label string `json:"label,omitempty" maxLength:"300" doc:"Rótulo exibido; vazio = o próprio valor."`
}

// GateFormField é um campo do formulário de uma pergunta.
type GateFormField struct {
	Key      string           `json:"key" pattern:"^[a-z][a-z0-9_]{0,39}$" doc:"Chave do campo na resposta."`
	Label    string           `json:"label" minLength:"1" maxLength:"300"`
	Type     string           `json:"type" enum:"single,multi,text" doc:"single: uma opção; multi: uma ou mais; text: texto livre."`
	Options  []GateFormOption `json:"options,omitempty" doc:"Opções (single e multi; até 20)."`
	Required bool             `json:"required,omitempty"`
	Help     string           `json:"help,omitempty" maxLength:"300"`
}

// GateForm é o formulário de um gate question (até 12 campos).
type GateForm struct {
	Fields []GateFormField `json:"fields" minItems:"1" maxItems:"12"`
}

// GateAudience restringe quem decide o gate e quem o vê na Caixa; o owner da empresa sempre pode.
type GateAudience struct {
	Users []string `json:"users,omitempty" format:"uuid" doc:"Usuários (membros da empresa) que podem decidir."`
	Roles []string `json:"roles,omitempty" enum:"owner,admin,member,viewer" doc:"Papéis que podem decidir."`
}

// GateHireSpec é a ficha de contratação de um gate hire. Aprovar o gate cria a persona.
type GateHireSpec struct {
	Name                 string         `json:"name" minLength:"1"`
	Slug                 string         `json:"slug,omitempty"`
	Title                string         `json:"title,omitempty"`
	Icon                 string         `json:"icon,omitempty"`
	Instructions         string         `json:"instructions,omitempty"`
	Agent                string         `json:"agent" enum:"claude,codex,acp,fake"`
	Model                string         `json:"model,omitempty"`
	AdapterConfig        map[string]any `json:"adapter_config,omitempty" doc:"Sem a chave env: segredos entram depois da contratação."`
	ReportsTo            string         `json:"reports_to,omitempty" doc:"Id ou slug do chefe; vazio = quem pediu."`
	WorkspaceID          string         `json:"workspace_id,omitempty" format:"uuid"`
	HeartbeatIntervalSec int            `json:"heartbeat_interval_sec,omitempty"`
	BudgetMonthlyCents   *int64         `json:"budget_monthly_cents,omitempty"`
	SubscriptionID       string         `json:"subscription_id,omitempty" format:"uuid"`
	RequireReview        bool           `json:"require_review,omitempty"`
	ReviewerPersona      string         `json:"reviewer_persona,omitempty" doc:"Id ou slug."`
	ReviewerAgent        string         `json:"reviewer_agent,omitempty"`
	ReviewerModel        string         `json:"reviewer_model,omitempty"`
}

func formOut(f domain.QuestionForm) *GateForm {
	o := &GateForm{Fields: make([]GateFormField, 0, len(f.Fields))}
	for _, fl := range f.Fields {
		fo := GateFormField{Key: fl.Key, Label: fl.Label, Type: string(fl.Type), Required: fl.Required, Help: fl.Help}
		for _, op := range fl.Options {
			fo.Options = append(fo.Options, GateFormOption{Value: op.Value, Label: op.Label})
		}
		o.Fields = append(o.Fields, fo)
	}
	return o
}

func formIn(f *GateForm) *domain.QuestionForm {
	if f == nil {
		return nil
	}
	o := &domain.QuestionForm{Fields: make([]domain.QuestionField, 0, len(f.Fields))}
	for _, fl := range f.Fields {
		fo := domain.QuestionField{Key: fl.Key, Label: fl.Label, Type: domain.QuestionFieldType(fl.Type), Required: fl.Required, Help: fl.Help}
		for _, op := range fl.Options {
			fo.Options = append(fo.Options, domain.QuestionOption{Value: op.Value, Label: op.Label})
		}
		o.Fields = append(o.Fields, fo)
	}
	return o
}

func audienceOut(a domain.GateAudience) *GateAudience {
	if a.Empty() {
		return nil
	}
	o := &GateAudience{}
	for _, u := range a.Users {
		o.Users = append(o.Users, u.String())
	}
	for _, r := range a.Roles {
		o.Roles = append(o.Roles, string(r))
	}
	return o
}

func audienceIn(a *GateAudience) (domain.GateAudience, error) {
	var o domain.GateAudience
	if a == nil {
		return o, nil
	}
	for _, u := range a.Users {
		id, err := domain.ParseID(u)
		if err != nil {
			return o, domain.Invalid("audience.users", "não é um uuid")
		}
		o.Users = append(o.Users, id)
	}
	for _, r := range a.Roles {
		o.Roles = append(o.Roles, domain.MemberRole(r))
	}
	return o, nil
}

func hireOut(s domain.HireSpec) *GateHireSpec {
	o := &GateHireSpec{Name: s.Name, Slug: s.Slug, Title: s.Title, Icon: s.Icon, Instructions: s.Instructions, Agent: s.Agent, Model: s.Model,
		ReportsTo: idPtr(s.ReportsTo), WorkspaceID: idPtr(s.WorkspaceID), HeartbeatIntervalSec: s.HeartbeatIntervalSec,
		BudgetMonthlyCents: s.BudgetMonthlyCents, SubscriptionID: idPtr(s.SubscriptionID), RequireReview: s.RequireReview,
		ReviewerPersona: idPtr(s.ReviewerPersona), ReviewerAgent: s.ReviewerAgent, ReviewerModel: s.ReviewerModel}
	if len(s.AdapterConfig) > 0 {
		_ = json.Unmarshal(s.AdapterConfig, &o.AdapterConfig)
	}
	return o
}

// hireIn converte a ficha da API na do domínio; as referências a personas aceitam id ou slug.
func (s *server) hireIn(ctx context.Context, cid domain.ID, h *GateHireSpec) (*domain.HireSpec, error) {
	if h == nil {
		return nil, nil
	}
	o := &domain.HireSpec{Name: h.Name, Slug: h.Slug, Title: h.Title, Icon: h.Icon, Instructions: h.Instructions, Agent: h.Agent, Model: h.Model,
		HeartbeatIntervalSec: h.HeartbeatIntervalSec, BudgetMonthlyCents: h.BudgetMonthlyCents, RequireReview: h.RequireReview,
		ReviewerAgent: h.ReviewerAgent, ReviewerModel: h.ReviewerModel}
	if len(h.AdapterConfig) > 0 {
		raw, err := json.Marshal(h.AdapterConfig)
		if err != nil {
			return nil, s.fail(ctx, domain.Invalid("hire.adapter_config", "não é JSON"))
		}
		o.AdapterConfig = raw
	}
	var err error
	if o.ReportsTo, err = s.personaRef(ctx, cid, h.ReportsTo); err != nil {
		return nil, s.fail(ctx, err)
	}
	if o.ReviewerPersona, err = s.personaRef(ctx, cid, h.ReviewerPersona); err != nil {
		return nil, s.fail(ctx, err)
	}
	for field, dst := range map[string]**domain.ID{"hire.workspace_id": &o.WorkspaceID, "hire.subscription_id": &o.SubscriptionID} {
		raw := h.WorkspaceID
		if field == "hire.subscription_id" {
			raw = h.SubscriptionID
		}
		if raw == "" {
			continue
		}
		id, err := domain.ParseID(raw)
		if err != nil {
			return nil, s.fail(ctx, domain.Invalid(field, "não é um uuid"))
		}
		*dst = &id
	}
	return o, nil
}

// gateExtras preenche os campos estruturados de um gate a partir do payload.
func gateExtras(o *Gate, g service.GateView) {
	o.Revision = g.Revision
	o.Audience = audienceOut(g.Audience)
	switch g.Kind {
	case domain.GateQuestion:
		var f domain.QuestionForm
		if json.Unmarshal(g.Payload, &f) == nil && len(f.Fields) > 0 {
			o.Form = formOut(f)
		}
		if len(g.Answer) > 0 {
			var a domain.QuestionAnswer
			if json.Unmarshal(g.Answer, &a) == nil {
				o.Answers = a.Values
			}
		}
	case domain.GateHire:
		var p struct {
			Spec *domain.HireSpec `json:"spec"`
		}
		if json.Unmarshal(g.Payload, &p) == nil && p.Spec != nil {
			o.Hire = hireOut(*p.Spec)
		}
	case domain.GatePlan:
		var p struct {
			ArtifactID string `json:"artifact_id"`
		}
		if json.Unmarshal(g.Payload, &p) == nil {
			o.ArtifactID = p.ArtifactID
		}
	}
}

// GateRevisionOut é uma versão anterior de um gate reenviado.
type GateRevisionOut struct {
	Revision     int       `json:"revision"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	DecisionNote string    `json:"decision_note,omitempty" doc:"O que o humano pediu quando devolveu esta versão."`
	SHA256       string    `json:"sha256"`
	CreatedAt    time.Time `json:"created_at"`
}

// GateHistoryOut são as versões de um gate e o diff entre a anterior e a vigente.
type GateHistoryOut struct {
	Gate      Gate              `json:"gate"`
	Revisions []GateRevisionOut `json:"revisions" doc:"Da mais nova para a mais antiga; a vigente está em gate."`
	Added     int               `json:"added" doc:"Linhas acrescentadas em relação à revisão anterior."`
	Removed   int               `json:"removed"`
	Diff      string            `json:"diff,omitempty" doc:"Diff em texto (linhas com +, - e dois espaços) entre a revisão anterior e a vigente."`
}

// PlanPolicyOut é a política de plano de uma task por camada.
type PlanPolicyOut struct {
	Task        *bool  `json:"task,omitempty"`
	Project     *bool  `json:"project,omitempty"`
	Persona     *bool  `json:"persona,omitempty"`
	Required    bool   `json:"required" doc:"Resultado da precedência task > projeto > persona."`
	Source      string `json:"source,omitempty" enum:"task,project,persona"`
	Phase       string `json:"phase,omitempty" enum:"plan,execute" doc:"plan: sem plano aprovado, o run só planeja; execute: plano aprovado. Vazio: a política não vale."`
	PendingPlan bool   `json:"pending_plan"`
}

// BudgetIncidentOut é um incidente de orçamento.
type BudgetIncidentOut struct {
	ID          string     `json:"id" format:"uuid"`
	Scope       string     `json:"scope" enum:"company,project,persona"`
	TargetID    string     `json:"target_id" format:"uuid"`
	TargetName  string     `json:"target_name"`
	Month       string     `json:"month" example:"2026-10"`
	Open        bool       `json:"open"`
	BudgetCents int64      `json:"budget_cents"`
	SpentUSD    float64    `json:"spent_usd"`
	ReleaseRuns int        `json:"release_runs" doc:"Runs avulsos liberados e ainda não consumidos."`
	Resolution  string     `json:"resolution,omitempty" enum:"ceiling_raised,kept_paused,released_once,auto"`
	Note        string     `json:"note,omitempty"`
	ResolvedBy  *ActorRef  `json:"resolved_by,omitempty"`
	OpenedAt    time.Time  `json:"opened_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

func incidentOut(i domain.BudgetIncident) BudgetIncidentOut {
	return BudgetIncidentOut{ID: idStr(i.ID), Scope: string(i.Scope), TargetID: idStr(i.TargetID), TargetName: i.TargetName, Month: i.Month,
		Open: i.Open, BudgetCents: i.BudgetCents, SpentUSD: i.SpentUSD, ReleaseRuns: i.ReleaseRuns, Resolution: string(i.Resolution),
		Note: i.Note, ResolvedBy: actorRefPtr(i.ResolvedBy), OpenedAt: i.OpenedAt, ResolvedAt: i.ResolvedAt}
}

// ---------- entradas ----------

type answerGateIn struct {
	GatePath
	Body struct {
		Answers map[string][]string `json:"answers" doc:"Chave do campo → valores (um só em single e text)."`
		Note    string              `json:"note,omitempty"`
	}
}

type dismissGateIn struct {
	GatePath
	Body struct {
		Note string `json:"note,omitempty" doc:"Vai para a thread da task; a persona segue sem a resposta."`
	}
}

type resubmitGateIn struct {
	GatePath
	Body struct {
		Title    string        `json:"title,omitempty" maxLength:"300" doc:"Vazio = mantém."`
		Body     string        `json:"body,omitempty" doc:"Texto novo do pedido."`
		Artifact string        `json:"artifact,omitempty" doc:"Gate plan: nome ou id do artefato com a versão nova do plano."`
		Hire     *GateHireSpec `json:"hire,omitempty" doc:"Gate hire: a ficha ajustada."`
		Note     string        `json:"note,omitempty" doc:"O que mudou; vai para a thread da task."`
	}
}

type taskPlanPolicyIn struct {
	CompanyPath
	Task string `path:"task" doc:"Chave ou id da task."`
}

type setPlanPolicyIn struct {
	CompanyPath
	Body struct {
		Scope  string `json:"scope" enum:"task,project,persona"`
		Target string `json:"target" doc:"Chave/id da task, slug/id do projeto ou da persona."`
		// Require: true liga, false desliga, ausente (null) volta a herdar.
		Require *bool `json:"require,omitempty" doc:"true liga, false desliga; ausente volta a herdar da camada seguinte."`
	}
}

type CompanyPolicies struct {
	RequireHireGate bool `json:"require_hire_gate" doc:"Quando verdadeiro, humanos também contratam por gate (hire) em vez de direto."`
}

type setPoliciesIn struct {
	CompanyPath
	Body struct {
		RequireHireGate bool `json:"require_hire_gate"`
	}
}

type primaryPersonaIn struct {
	CompanyPath
	Body struct {
		Persona string `json:"persona,omitempty" doc:"Id ou slug; vazio limpa a persona principal."`
	}
}

type personaSystemIn struct {
	CompanyPath
	Persona string `path:"persona" doc:"Id ou slug da persona."`
	Body    struct {
		System bool `json:"system" doc:"Persona de sistema: não se desliga e só um owner a edita."`
	}
}

type listIncidentsIn struct {
	CompanyPath
	Open bool `query:"open" doc:"Só os abertos."`
	Page
}

type IncidentPath struct {
	CompanyPath
	Incident string `path:"incident" format:"uuid" doc:"Id do incidente."`
}

type resolveIncidentIn struct {
	IncidentPath
	Body struct {
		Resolution    string `json:"resolution" enum:"ceiling_raised,kept_paused,released_once" doc:"ceiling_raised: sobe (ou remove) o teto e retoma o que estava pausado por orçamento; kept_paused: mantém e registra; released_once: libera só mais um run, sem mexer no teto."`
		BudgetCents   *int64 `json:"budget_cents,omitempty" doc:"Teto novo em centavos de dólar (ceiling_raised); precisa passar do gasto do mês."`
		RemoveCeiling bool   `json:"remove_ceiling,omitempty" doc:"ceiling_raised: remove o teto de vez."`
		Note          string `json:"note,omitempty" maxLength:"500"`
	}
}

func (s *server) registerGatesPlus(api huma.API) {
	gate := func(ctx context.Context, company, gateRef string, human bool) (gatesPlusBackend, scope, domain.ID, error) {
		if human {
			if err := forbidPersona(ctx); err != nil {
				return nil, scope{}, domain.ID{}, err
			}
		}
		b, err := s.gatesPlus()
		if err != nil {
			return nil, scope{}, domain.ID{}, err
		}
		sc, err := s.scope(ctx, company)
		if err != nil {
			return nil, scope{}, domain.ID{}, err
		}
		id, err := domain.ParseID(gateRef)
		if err != nil {
			return nil, scope{}, domain.ID{}, s.fail(ctx, domain.Invalid("gate", "não é um uuid"))
		}
		return b, sc, id, nil
	}

	huma.Register(api, op("answer-gate", http.MethodPost, "/companies/{company}/gates/{gate}/answer", "gates",
		"Responde um gate de pergunta (formulário); só humanos"),
		func(ctx context.Context, in *answerGateIn) (*ItemOut[Gate], error) {
			b, sc, id, err := gate(ctx, in.Company, in.Gate, true)
			if err != nil {
				return nil, err
			}
			g, err := b.AnswerGate(ctx, sc.Actor, sc.Company.ID, id, service.AnswerGateInput{Answers: in.Body.Answers, Note: in.Body.Note})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Gate]{Body: gateOut(g)}, nil
		})

	huma.Register(api, op("dismiss-gate", http.MethodPost, "/companies/{company}/gates/{gate}/dismiss", "gates",
		"Dispensa uma pergunta sem responder; só humanos"),
		func(ctx context.Context, in *dismissGateIn) (*ItemOut[Gate], error) {
			b, sc, id, err := gate(ctx, in.Company, in.Gate, true)
			if err != nil {
				return nil, err
			}
			g, err := b.DismissGate(ctx, sc.Actor, sc.Company.ID, id, in.Body.Note)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Gate]{Body: gateOut(g)}, nil
		})

	huma.Register(api, op("reopen-gate", http.MethodPost, "/companies/{company}/gates/{gate}/reopen", "gates",
		"Reabre uma pergunta dispensada; só humanos"),
		func(ctx context.Context, in *GatePath) (*ItemOut[Gate], error) {
			b, sc, id, err := gate(ctx, in.Company, in.Gate, true)
			if err != nil {
				return nil, err
			}
			g, err := b.ReopenGate(ctx, sc.Actor, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Gate]{Body: gateOut(g)}, nil
		})

	huma.Register(api, op("resubmit-gate", http.MethodPost, "/companies/{company}/gates/{gate}/resubmit", "gates",
		"Reenvia o pedido no mesmo gate depois de mudanças pedidas (a persona que pediu ou um humano)"),
		func(ctx context.Context, in *resubmitGateIn) (*ItemOut[Gate], error) {
			b, sc, id, err := gate(ctx, in.Company, in.Gate, false)
			if err != nil {
				return nil, err
			}
			hire, err := s.hireIn(ctx, sc.Company.ID, in.Body.Hire)
			if err != nil {
				return nil, err
			}
			g, err := b.ResubmitGate(ctx, sc.Actor, sc.Company.ID, id, service.ResubmitGateInput{Title: in.Body.Title, Body: in.Body.Body,
				Artifact: in.Body.Artifact, Hire: hire, Note: in.Body.Note})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Gate]{Body: gateOut(g)}, nil
		})

	huma.Register(api, op("get-gate-revisions", http.MethodGet, "/companies/{company}/gates/{gate}/revisions", "gates",
		"Versões anteriores de um gate reenviado e o diff até a vigente"),
		func(ctx context.Context, in *GatePath) (*ItemOut[GateHistoryOut], error) {
			b, sc, id, err := gate(ctx, in.Company, in.Gate, false)
			if err != nil {
				return nil, err
			}
			h, err := b.GateRevisions(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			o := GateHistoryOut{Gate: gateOut(h.Gate), Revisions: make([]GateRevisionOut, 0, len(h.Revisions)), Added: h.Diff.Added, Removed: h.Diff.Removed}
			for _, r := range h.Revisions {
				o.Revisions = append(o.Revisions, GateRevisionOut{Revision: r.Revision, Title: r.Title, Body: r.Body,
					DecisionNote: r.DecisionNote, SHA256: r.SHA256, CreatedAt: r.CreatedAt})
			}
			if h.Diff.Changed() {
				o.Diff = textdiff.Unified(h.Diff, 3, 64<<10)
			}
			return &ItemOut[GateHistoryOut]{Body: o}, nil
		})

	huma.Register(api, op("get-task-plan-policy", http.MethodGet, "/companies/{company}/tasks/{task}/plan-policy", "gates",
		"Política de plano (plan-review) da task por camada e a fase atual"),
		func(ctx context.Context, in *taskPlanPolicyIn) (*ItemOut[PlanPolicyOut], error) {
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			tid, err := s.taskRef(ctx, sc.Company.ID, in.Task)
			if err != nil || tid == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			v, err := b.TaskPlanPolicy(ctx, sc.Company.ID, *tid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PlanPolicyOut]{Body: PlanPolicyOut{Task: v.Task, Project: v.Project, Persona: v.Persona, Required: v.Effective.Required,
				Source: string(v.Effective.Source), Phase: string(v.Phase), PendingPlan: v.PendingPlan}}, nil
		})

	huma.Register(api, noContent(op("set-plan-policy", http.MethodPut, "/companies/{company}/plan-policy", "gates",
		"Liga, desliga ou limpa a política de plano de uma task (member), projeto ou persona (admin)")),
		func(ctx context.Context, in *setPlanPolicyIn) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			scopeName := domain.PlanScope(in.Body.Scope)
			if err := scopeName.Check(); err != nil {
				return nil, s.fail(ctx, err)
			}
			var target *domain.ID
			switch scopeName {
			case domain.PlanScopeTask:
				target, err = s.taskRef(ctx, sc.Company.ID, in.Body.Target)
			case domain.PlanScopeProject:
				target, err = s.projectRef(ctx, sc.Company.ID, in.Body.Target)
			default:
				target, err = s.personaRef(ctx, sc.Company.ID, in.Body.Target)
			}
			if err != nil || target == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			if err := b.SetPlanPolicy(ctx, sc.Actor, sc.Company.ID, scopeName, *target, in.Body.Require); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("get-company-policies", http.MethodGet, "/companies/{company}/policies", "companies",
		"Políticas da empresa (contratação por gate)"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[CompanyPolicies], error) {
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			on, err := b.RequireHireGate(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[CompanyPolicies]{Body: CompanyPolicies{RequireHireGate: on}}, nil
		})

	huma.Register(api, op("set-company-policies", http.MethodPut, "/companies/{company}/policies", "companies",
		"Grava as políticas da empresa; owner"),
		func(ctx context.Context, in *setPoliciesIn) (*ItemOut[CompanyPolicies], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := b.SetRequireHireGate(ctx, sc.Actor, sc.Company.ID, in.Body.RequireHireGate); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[CompanyPolicies]{Body: CompanyPolicies{RequireHireGate: in.Body.RequireHireGate}}, nil
		})

	huma.Register(api, op("get-primary-persona", http.MethodGet, "/companies/{company}/primary-persona", "personas",
		"A persona principal de quem chama (padrão das tasks e do chat); 404 quando não há"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[Persona], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := b.PrimaryPersona(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if p == nil {
				return nil, problem(http.StatusNotFound, "nenhuma persona principal escolhida")
			}
			return &ItemOut[Persona]{Body: personaOut(*p)}, nil
		})

	huma.Register(api, op("set-primary-persona", http.MethodPut, "/companies/{company}/primary-persona", "personas",
		"Escolhe (ou, com persona vazia, limpa) a persona principal de quem chama"),
		func(ctx context.Context, in *primaryPersonaIn) (*ItemOut[Persona], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pid, err := s.personaRef(ctx, sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			p, err := b.SetPrimaryPersona(ctx, sc.Actor, sc.Company.ID, pid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if p == nil {
				return &ItemOut[Persona]{}, nil
			}
			return &ItemOut[Persona]{Body: personaOut(*p)}, nil
		})

	huma.Register(api, op("set-persona-system", http.MethodPut, "/companies/{company}/personas/{persona}/system", "personas",
		"Marca ou desmarca uma persona como de sistema; owner"),
		func(ctx context.Context, in *personaSystemIn) (*ItemOut[Persona], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pid, err := s.personaRef(ctx, sc.Company.ID, in.Persona)
			if err != nil || pid == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			p, err := b.SetPersonaSystem(ctx, sc.Actor, sc.Company.ID, *pid, in.Body.System)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Persona]{Body: personaOut(p)}, nil
		})

	huma.Register(api, op("list-budget-incidents", http.MethodGet, "/companies/{company}/budget-incidents", "costs",
		"Incidentes de orçamento (tetos estourados esperando decisão)"),
		func(ctx context.Context, in *listIncidentsIn) (*ListOut[BudgetIncidentOut], error) {
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			list, err := b.ListBudgetIncidents(ctx, sc.Company.ID, in.Open, 500)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, incidentOut), nil
		})

	huma.Register(api, op("resolve-budget-incident", http.MethodPost, "/companies/{company}/budget-incidents/{incident}/resolve", "costs",
		"Resolve um incidente de orçamento: subir o teto, manter pausado ou liberar só um run; admin"),
		func(ctx context.Context, in *resolveIncidentIn) (*ItemOut[BudgetIncidentOut], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.gatesPlus()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Incident)
			if err != nil {
				return nil, problem(http.StatusNotFound, "incidente não encontrado")
			}
			r, err := b.ResolveBudgetIncident(ctx, sc.Actor, sc.Company.ID, id, service.ResolveIncidentInput{
				Resolution: domain.IncidentResolution(in.Body.Resolution), BudgetCents: in.Body.BudgetCents,
				RemoveCeiling: in.Body.RemoveCeiling, Note: in.Body.Note})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[BudgetIncidentOut]{Body: incidentOut(r)}, nil
		})
}
