package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Gates estendidos na tela (ADR-0041): responder/dispensar/reabrir perguntas, ficha de contratação, histórico de
// reenvios, incidentes de orçamento na Caixa, persona principal e de sistema, política de plano e de contratação.

// gatesPlusBackend é o que estas telas pedem ao service; opcional para que os fakes que não o têm sigam valendo.
type gatesPlusBackend interface {
	AnswerGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in service.AnswerGateInput) (service.GateView, error)
	DismissGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, note string) (service.GateView, error)
	ReopenGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID) (service.GateView, error)
	GateRevisions(ctx context.Context, companyID, gateID domain.ID) (service.GateHistory, error)
	TaskPlanPolicy(ctx context.Context, companyID, taskID domain.ID) (service.PlanPolicyView, error)
	PlanPolicyLayer(ctx context.Context, companyID domain.ID, scope domain.PlanScope, targetID domain.ID) (*bool, error)
	SetPlanPolicy(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.PlanScope, targetID domain.ID, require *bool) error
	RequireHireGate(ctx context.Context, companyID domain.ID) (bool, error)
	SetRequireHireGate(ctx context.Context, actor domain.Actor, companyID domain.ID, on bool) error
	PrimaryPersona(ctx context.Context, companyID domain.ID) (*domain.Persona, error)
	SetPrimaryPersona(ctx context.Context, actor domain.Actor, companyID domain.ID, personaID *domain.ID) (*domain.Persona, error)
	SetPersonaSystem(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, system bool) (domain.Persona, error)
	ResolveBudgetIncident(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.ResolveIncidentInput) (domain.BudgetIncident, error)
}

var _ gatesPlusBackend = (*service.Service)(nil)

func (s *server) gatesPlusBE() (gatesPlusBackend, bool) {
	b, ok := s.be.(gatesPlusBackend)
	return b, ok
}

var plusNotices = map[string]string{
	"respondido": i18n.N("Pergunta respondida. A resposta foi para a thread da task e a persona é acordada."),
	"dispensado": i18n.N("Pergunta dispensada. A persona segue sem a resposta."),
	"reaberto":   i18n.N("Pergunta reaberta: a persona volta a esperar."),
	"incidente":  i18n.N("Incidente de orçamento resolvido."),
	"principal":  i18n.N("Persona principal atualizada."),
	"sistema":    i18n.N("Persona de sistema atualizada."),
	"plano":      i18n.N("Política de plano gravada."),
	"politicas":  i18n.N("Política da empresa gravada."),
}

func (s *server) gatesPlusRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	mux.HandleFunc("POST /c/{company}/gates/{gate}/responder", post(co(s.handleAnswerGate)))
	mux.HandleFunc("POST /c/{company}/gates/{gate}/dispensar", post(co(s.handleDismissGate)))
	mux.HandleFunc("POST /c/{company}/gates/{gate}/reabrir", post(co(s.handleReopenGate)))
	mux.HandleFunc("POST /c/{company}/incidentes/{incident}/resolver", post(co(s.handleResolveIncident)))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/principal", post(co(s.handlePrimaryPersona)))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/sistema", post(co(s.handlePersonaSystem)))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/plano", post(co(s.handlePersonaPlan)))
	mux.HandleFunc("POST /c/{company}/tasks/{task}/politica-plano", post(co(s.handleTaskPlan)))
	mux.HandleFunc("POST /c/{company}/politicas", post(co(s.handleCompanyPolicies)))
}

// ---------- vistas ----------

func audienceLabel(ctx context.Context, a domain.GateAudience) string {
	var parts []string
	if len(a.Roles) > 0 {
		roles := make([]string, len(a.Roles))
		for i, r := range a.Roles {
			roles[i] = roleName(ctx, r)
		}
		parts = append(parts, strings.Join(roles, ", "))
	}
	if n := len(a.Users); n > 0 {
		parts = append(parts, i18n.Tf(ctx, "%d pessoa(s)", n))
	}
	return strings.Join(parts, " · ")
}

// roleName é o rótulo curto de um papel (sem depender do pacote de views).
func roleName(ctx context.Context, r domain.MemberRole) string {
	switch r {
	case domain.MemberOwner:
		return i18n.T(ctx, "Dono")
	case domain.MemberAdmin:
		return "Admin"
	case domain.MemberMember:
		return i18n.T(ctx, "Membro")
	}
	return i18n.T(ctx, "Leitor")
}

// questionVM monta o formulário (gate pendente) e a resposta (gate decidido) de uma pergunta.
func questionVM(ctx context.Context, g service.GateView, base string) *views.QuestionVM {
	var form domain.QuestionForm
	if err := json.Unmarshal(g.Payload, &form); err != nil || len(form.Fields) == 0 {
		return nil
	}
	gb := base + "/gates/" + g.ID.String()
	q := &views.QuestionVM{AnswerHref: gb + "/responder", DismissHref: gb + "/dispensar", ReopenHref: gb + "/reabrir", Dismissed: g.Status == domain.GateDismissed}
	for _, f := range form.Fields {
		fv := views.QuestionFieldVM{Key: f.Key, Label: f.Label, Type: string(f.Type), Help: f.Help, Required: f.Required}
		for _, o := range f.Options {
			lbl := o.Label
			if lbl == "" {
				lbl = o.Value
			}
			fv.Options = append(fv.Options, views.QuestionOptionVM{Value: o.Value, Label: lbl})
		}
		q.Fields = append(q.Fields, fv)
	}
	if len(g.Answer) > 0 {
		var a domain.QuestionAnswer
		if json.Unmarshal(g.Answer, &a) == nil {
			for _, f := range form.Fields {
				vals := a.Values[f.Key]
				if len(vals) == 0 {
					continue
				}
				labels := make([]string, len(vals))
				for i, v := range vals {
					labels[i] = v
					for _, o := range f.Options {
						if o.Value == v && o.Label != "" {
							labels[i] = o.Label
						}
					}
				}
				q.Answers = append(q.Answers, views.AnswerLine{Label: f.Label, Value: strings.Join(labels, ", ")})
			}
		}
	}
	return q
}

// hireVM resume a ficha de um gate hire (nil sem ficha).
func hireVM(ctx context.Context, g service.GateView, names lookup) *views.HireVM {
	var p struct {
		Spec *domain.HireSpec `json:"spec"`
	}
	if json.Unmarshal(g.Payload, &p) != nil || p.Spec == nil {
		return nil
	}
	sp := p.Spec
	h := &views.HireVM{Name: sp.Name, Slug: domain.Slugify(sp.Name), Title: sp.Title, Agent: sp.Agent, Model: sp.Model, Instructions: sp.Instructions, RequireReview: sp.RequireReview}
	if sp.Slug != "" {
		h.Slug = sp.Slug
	}
	boss := g.PersonaName
	if sp.ReportsTo != nil {
		if pp, ok := names.personas[*sp.ReportsTo]; ok {
			boss = pp.Name
		}
	}
	if boss == "" {
		boss = i18n.T(ctx, "Você (dono)")
	}
	h.Reports = boss
	if sp.BudgetMonthlyCents != nil {
		h.Budget = fmt.Sprintf("US$ %.2f", float64(*sp.BudgetMonthlyCents)/100)
	}
	return h
}

// historyVM monta o histórico de um gate reenviado (nil quando nunca foi reenviado).
func (s *server) historyVM(ctx context.Context, co domain.Company, g service.GateView) *views.GateHistoryVM {
	b, ok := s.gatesPlusBE()
	if !ok || g.Revision <= 1 {
		return nil
	}
	h, err := b.GateRevisions(ctx, co.ID, g.ID)
	if err != nil || len(h.Revisions) == 0 {
		return nil
	}
	out := &views.GateHistoryVM{Revision: g.Revision, Added: h.Diff.Added, Removed: h.Diff.Removed, Diff: textdiff.Unified(h.Diff, 3, 64<<10)}
	for _, r := range h.Revisions {
		out.Revisions = append(out.Revisions, views.GateRevisionVM{Revision: r.Revision, When: fmtDateTime(ctx, r.CreatedAt, loadLoc(co.Timezone)),
			Note: r.DecisionNote, Body: renderMD(r.Body)})
	}
	return out
}

func (s *server) companyPolicyVM(ctx context.Context, co domain.Company) views.CompanyPolicyVM {
	b, ok := s.gatesPlusBE()
	if !ok {
		return views.CompanyPolicyVM{}
	}
	on, err := b.RequireHireGate(ctx, co.ID)
	if err != nil {
		return views.CompanyPolicyVM{}
	}
	return views.CompanyPolicyVM{Enabled: true, On: on, Action: "/c/" + co.Slug + "/politicas"}
}

func planOptions(ctx context.Context, cur *bool, inheritLabel string) []views.Option {
	sel := "inherit"
	if cur != nil {
		sel = "no"
		if *cur {
			sel = "yes"
		}
	}
	return optionList(sel, [][2]string{{"inherit", inheritLabel}, {"yes", i18n.T(ctx, "Exigir")}, {"no", i18n.T(ctx, "Dispensar")}})
}

func planSummary(ctx context.Context, v service.PlanPolicyView) string {
	if !v.Effective.Required {
		return i18n.T(ctx, "A execução começa direto, sem plano.")
	}
	src := map[domain.PlanScope]string{domain.PlanScopeTask: i18n.T(ctx, "task"), domain.PlanScopeProject: i18n.T(ctx, "projeto"), domain.PlanScopePersona: i18n.T(ctx, "persona")}[v.Effective.Source]
	switch v.Phase {
	case domain.PlanPhaseExecute:
		return i18n.Tf(ctx, "Plano exigido (origem: %s) e já aprovado: a execução segue o plano.", src)
	}
	if v.PendingPlan {
		return i18n.Tf(ctx, "Plano exigido (origem: %s): há um plano esperando a sua decisão.", src)
	}
	return i18n.Tf(ctx, "Plano exigido (origem: %s): o primeiro run só planeja.", src)
}

// taskPlanVM é o bloco "Plano" do painel da task.
func (s *server) taskPlanVM(ctx context.Context, co domain.Company, t service.TaskView) views.CoordPlan {
	b, ok := s.gatesPlusBE()
	if !ok {
		return views.CoordPlan{}
	}
	v, err := b.TaskPlanPolicy(ctx, co.ID, t.ID)
	if err != nil {
		return views.CoordPlan{}
	}
	return views.CoordPlan{Enabled: true, Summary: planSummary(ctx, v), Action: "/c/" + co.Slug + "/tasks/" + t.Key + "/politica-plano",
		Options: planOptions(ctx, v.Task, i18n.T(ctx, "Herdar (projeto/persona)"))}
}

// personaPlusVM monta os controles do M15 na página da persona.
func (s *server) personaPlusVM(ctx context.Context, co domain.Company, p domain.Persona) views.PersonaPlusVM {
	b, ok := s.gatesPlusBE()
	if !ok {
		return views.PersonaPlusVM{}
	}
	base := "/c/" + co.Slug + "/personas/" + p.Slug
	vm := views.PersonaPlusVM{Enabled: true, System: p.System, PrimaryHref: base + "/principal", SystemHref: base + "/sistema", PlanHref: base + "/plano",
		CanEditSystem: views.RoleFrom(ctx).AtLeast(domain.MemberOwner)}
	if prim, err := b.PrimaryPersona(ctx, co.ID); err == nil && prim != nil && prim.ID == p.ID {
		vm.IsPrimary = true
	}
	cur, _ := b.PlanPolicyLayer(ctx, co.ID, domain.PlanScopePersona, p.ID)
	switch {
	case cur == nil:
		vm.PlanSummary = i18n.T(ctx, "Herda (sem exigência)")
	case *cur:
		vm.PlanSummary = i18n.T(ctx, "Exige plano aprovado")
	default:
		vm.PlanSummary = i18n.T(ctx, "Dispensa o plano")
	}
	vm.PlanOptions = planOptions(ctx, cur, i18n.T(ctx, "Herdar"))
	return vm
}

// incidentRows monta os incidentes de orçamento da Caixa.
func incidentRows(ctx context.Context, in []domain.BudgetIncident, base string, names lookup, ago func(domain.BudgetIncident) string) []views.InboxIncident {
	out := make([]views.InboxIncident, 0, len(in))
	for _, i := range in {
		row := views.InboxIncident{Target: i.TargetName, Month: i.Month, Spent: fmt.Sprintf("US$ %.2f", i.SpentUSD),
			Budget: fmt.Sprintf("US$ %.2f", float64(i.BudgetCents)/100), When: ago(i), Released: i.ReleaseRuns,
			ResolveHref: base + "/incidentes/" + i.ID.String() + "/resolver"}
		switch i.Scope {
		case domain.BudgetScopeCompany:
			row.Scope = i18n.T(ctx, "empresa")
		case domain.BudgetScopeProject:
			row.Scope = i18n.T(ctx, "projeto")
			row.TargetHref = base + "/custos"
		default:
			row.Scope = i18n.T(ctx, "persona")
			if p, ok := names.personas[i.TargetID]; ok {
				row.TargetHref = base + "/personas/" + p.Slug
			}
		}
		out = append(out, row)
	}
	return out
}

// ---------- ações ----------

func (s *server) plusBackend(w http.ResponseWriter, r *http.Request, co domain.Company) (gatesPlusBackend, bool) {
	b, ok := s.gatesPlusBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
	}
	return b, ok
}

// gateBack devolve para onde a pessoa estava: a Caixa, a task, o painel ou o gate.
func gateBack(r *http.Request, base string, g service.GateView, ok string) string {
	to := base + "/gates/" + g.ID.String() + "?ok=" + ok
	switch trimForm(r, "next") {
	case "caixa":
		to = base + "/caixa?ok=" + ok
	case "painel":
		to = base + "/?ok=" + ok
	case "task":
		if g.TaskKey != "" {
			to = base + "/tasks/" + g.TaskKey + "?ok=" + ok
		}
	}
	return to
}

func (s *server) handleAnswerGate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	id, err := domain.ParseID(r.PathValue("gate"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if r.PostFormValue("dismiss") != "" { // o botão Dispensar do mesmo formulário
		s.dismissGate(w, r, co, b, id)
		return
	}
	cur, err := s.be.GetGate(r.Context(), co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var form domain.QuestionForm
	if err := json.Unmarshal(cur.Payload, &form); err != nil {
		s.fail(w, r, &co, domain.Invalid("gate", i18n.T(r.Context(), "este gate não tem formulário")))
		return
	}
	answers := map[string][]string{}
	for _, f := range form.Fields {
		answers[f.Key] = r.PostForm["a_"+f.Key]
	}
	g, err := b.AnswerGate(r.Context(), actorFrom(r.Context()), co.ID, id, service.AnswerGateInput{Answers: answers, Note: trimForm(r, "note")})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, gateBack(r, "/c/"+co.Slug, g, "respondido"))
}

func (s *server) dismissGate(w http.ResponseWriter, r *http.Request, co domain.Company, b gatesPlusBackend, id domain.ID) {
	g, err := b.DismissGate(r.Context(), actorFrom(r.Context()), co.ID, id, trimForm(r, "note"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, gateBack(r, "/c/"+co.Slug, g, "dispensado"))
}

func (s *server) handleDismissGate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	id, err := domain.ParseID(r.PathValue("gate"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	s.dismissGate(w, r, co, b, id)
}

func (s *server) handleReopenGate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	id, err := domain.ParseID(r.PathValue("gate"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	g, err := b.ReopenGate(r.Context(), actorFrom(r.Context()), co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/gates/"+g.ID.String()+"?ok=reaberto")
}

func (s *server) handleResolveIncident(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	id, err := domain.ParseID(r.PathValue("incident"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	in := service.ResolveIncidentInput{Resolution: domain.IncidentResolution(trimForm(r, "resolution")), Note: trimForm(r, "note")}
	if in.Resolution == domain.ResolveRaiseCeiling {
		raw := strings.ReplaceAll(trimForm(r, "budget"), ",", ".")
		v, perr := strconv.ParseFloat(raw, 64)
		if raw == "" || perr != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			s.fail(w, r, &co, domain.Invalid(i18n.T(r.Context(), "teto"), i18n.T(r.Context(), "informe o novo teto em dólares (ex.: 50)")))
			return
		}
		cents := int64(math.Round(v * 100))
		in.BudgetCents = &cents
	}
	if _, err := b.ResolveBudgetIncident(r.Context(), actorFrom(r.Context()), co.ID, id, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/caixa?ok=incidente")
}

func (s *server) handlePrimaryPersona(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	p, err := s.be.GetPersona(r.Context(), co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var pid *domain.ID
	if trimForm(r, "clear") == "" {
		pid = &p.ID
	}
	if _, err := b.SetPrimaryPersona(r.Context(), actorFrom(r.Context()), co.ID, pid); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=principal")
}

func (s *server) handlePersonaSystem(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	p, err := s.be.GetPersona(r.Context(), co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := b.SetPersonaSystem(r.Context(), actorFrom(r.Context()), co.ID, p.ID, trimForm(r, "system") == "on"); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=sistema")
}

// planMode lê o campo inherit|yes|no do formulário de política de plano.
func planMode(r *http.Request) *bool {
	switch trimForm(r, "mode") {
	case "yes":
		v := true
		return &v
	case "no":
		v := false
		return &v
	}
	return nil
}

func (s *server) handlePersonaPlan(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	p, err := s.be.GetPersona(r.Context(), co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := b.SetPlanPolicy(r.Context(), actorFrom(r.Context()), co.ID, domain.PlanScopePersona, p.ID, planMode(r)); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/"+p.Slug+"?ok=plano")
}

func (s *server) handleTaskPlan(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	if err := b.SetPlanPolicy(r.Context(), actorFrom(r.Context()), co.ID, domain.PlanScopeTask, t.ID, planMode(r)); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "plano")
}

func (s *server) handleCompanyPolicies(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.plusBackend(w, r, co)
	if !ok {
		return
	}
	if err := b.SetRequireHireGate(r.Context(), actorFrom(r.Context()), co.ID, trimForm(r, "require_hire_gate") != ""); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes?ok=politicas#politicas")
}

func init() {
	// os avisos das ações do M15 valem nas telas que redirecionam com ?ok=
	for _, m := range []map[string]string{gateNotices, inboxNotices, personaNotices, notices} {
		for k, v := range plusNotices {
			if _, taken := m[k]; !taken {
				m[k] = v
			}
		}
	}
}
