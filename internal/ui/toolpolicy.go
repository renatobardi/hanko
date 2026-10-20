package ui

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Política de tools (ADR-0053): a aba Política em Integrações, com o teste de uma tool "como a persona X".

// toolPolicyBackend é o que a aba pede ao service; opcional para que os fakes que não a usam sigam valendo.
type toolPolicyBackend interface {
	ListToolPolicies(ctx context.Context, companyID domain.ID, personaID *domain.ID) ([]domain.ToolPolicy, error)
	SetToolPolicy(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.ToolPolicyInput) (domain.ToolPolicy, error)
	DeleteToolPolicy(ctx context.Context, actor domain.Actor, companyID, policyID domain.ID) error
	TestToolCall(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.ToolTestInput) (service.ToolTestResult, error)
}

var policyNotices = map[string]string{
	"gravada": i18n.N("Política gravada."),
	"apagada": i18n.N("Política apagada."),
}

func (s *server) toolPolicyRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	const b = "/c/{company}/ferramentas/politica"
	mux.HandleFunc("GET "+b, co(s.handleToolPolicies))
	mux.HandleFunc("POST "+b, post(co(s.handleSetToolPolicy)))
	mux.HandleFunc("POST "+b+"/testar", post(co(s.handleTestTool)))
	mux.HandleFunc("POST "+b+"/{policy}/remover", post(co(s.handleDeleteToolPolicy)))
}

func (s *server) policyBackend(w http.ResponseWriter, r *http.Request, co domain.Company) (toolPolicyBackend, bool) {
	b, ok := s.be.(toolPolicyBackend)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
	}
	return b, ok
}

func policyModeBadge(ctx context.Context, m domain.ToolPolicyMode) views.Badge {
	switch m {
	case domain.PolicyOff:
		return views.Badge{Label: i18n.T(ctx, "desligada"), Class: "danger"}
	case domain.PolicyAsk:
		return views.Badge{Label: i18n.T(ctx, "perguntar antes"), Class: "warn"}
	}
	return views.Badge{Label: i18n.T(ctx, "permitida"), Class: "done"}
}

func policyTargetText(ctx context.Context, p domain.ToolPolicy) string {
	switch p.Target {
	case domain.PolicyTargetIntegration:
		return i18n.Tf(ctx, "integração %s", p.IntegrationSlug)
	case domain.PolicyTargetTool:
		return p.IntegrationSlug + "/" + p.ToolName
	case domain.PolicyTargetNative:
		return i18n.Tf(ctx, "tool nativa %s", p.ToolName)
	}
	return i18n.T(ctx, "qualquer tool")
}

func (s *server) policyVM(ctx context.Context, r *http.Request, co domain.Company, b toolPolicyBackend) (views.ToolPolicyVM, error) {
	tb := toolsBase(co) + "/politica"
	vm := views.ToolPolicyVM{Notice: i18n.T(ctx, policyNotices[r.URL.Query().Get("ok")]), Error: r.URL.Query().Get("erro"),
		CreateHref: tb, TestHref: tb + "/testar"}
	pols, err := b.ListToolPolicies(ctx, co.ID, nil)
	if err != nil {
		return vm, err
	}
	for _, p := range pols {
		row := views.PolicyRowVM{Scope: i18n.T(ctx, "Empresa (padrão)"), Target: policyTargetText(ctx, p), Mode: policyModeBadge(ctx, p.Mode),
			Origin: i18n.T(ctx, "manual"), RemoveHref: tb + "/" + p.ID.String() + "/remover"}
		if p.Scope == domain.PolicyScopePersona {
			row.Scope = p.PersonaName
		}
		if p.Origin == domain.PolicyOriginAlways {
			row.Origin = i18n.T(ctx, "sempre permitir")
		}
		vm.Rows = append(vm.Rows, row)
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	vm.PersonaOpts = []views.Option{{Value: "", Label: i18n.T(ctx, "Padrão da empresa")}}
	for _, p := range personas {
		vm.PersonaOpts = append(vm.PersonaOpts, views.Option{Value: p.Slug, Label: p.Name})
		vm.TestPersonaOpts = append(vm.TestPersonaOpts, views.Option{Value: p.Slug, Label: p.Name})
	}
	integs, err := s.be.ToolIntegrations(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	vm.IntegrationOpts = []views.Option{{Value: "", Label: "—"}}
	for _, it := range integs {
		if it.Transport == domain.TransportStdio {
			continue // stdio não passa pelo gateway
		}
		vm.IntegrationOpts = append(vm.IntegrationOpts, views.Option{Value: it.Slug, Label: it.Name})
		vm.TestIntegration = append(vm.TestIntegration, views.Option{Value: it.Slug, Label: it.Name})
	}
	vm.TargetOpts = []views.Option{
		{Value: string(domain.PolicyTargetAll), Label: i18n.T(ctx, "Qualquer tool")},
		{Value: string(domain.PolicyTargetIntegration), Label: i18n.T(ctx, "Uma integração")},
		{Value: string(domain.PolicyTargetTool), Label: i18n.T(ctx, "Uma tool de integração")},
		{Value: string(domain.PolicyTargetNative), Label: i18n.T(ctx, "Tool do harness ou de plugin")},
	}
	vm.ModeOpts = []views.Option{
		{Value: string(domain.PolicyOff), Label: i18n.T(ctx, "Desligada")},
		{Value: string(domain.PolicyAsk), Label: i18n.T(ctx, "Perguntar antes"), Selected: true},
		{Value: string(domain.PolicyAllow), Label: i18n.T(ctx, "Permitida")},
	}
	return vm, nil
}

func (s *server) handleToolPolicies(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.policyBackend(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	vm, err := s.policyVM(ctx, r, co, b)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	sh := s.toolsShell(r, co, i18n.T(ctx, "Política"), views.Crumb{Label: i18n.T(ctx, "Política")})
	s.render(w, r, http.StatusOK, views.ToolPolicyPage(sh, vm))
}

func (s *server) integrationIDByRef(ctx context.Context, co domain.Company, slug string) (domain.ID, error) {
	it, err := s.toolIntegration(ctx, co, slug)
	return it.ID, err
}

func (s *server) handleSetToolPolicy(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.policyBackend(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	in := service.ToolPolicyInput{Scope: domain.PolicyScopeCompany, Target: domain.ToolPolicyTarget(trimForm(r, "target")),
		ToolName: trimForm(r, "tool"), Mode: domain.ToolPolicyMode(trimForm(r, "mode"))}
	if ref := trimForm(r, "persona"); ref != "" {
		p, err := s.be.GetPersona(ctx, co.ID, ref)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		id := p.ID
		in.Scope, in.PersonaID = domain.PolicyScopePersona, &id
	}
	if slug := trimForm(r, "integration"); slug != "" && (in.Target == domain.PolicyTargetIntegration || in.Target == domain.PolicyTargetTool) {
		id, err := s.integrationIDByRef(ctx, co, slug)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		in.IntegrationID = &id
	}
	if in.Target != domain.PolicyTargetTool && in.Target != domain.PolicyTargetNative {
		in.ToolName = ""
	}
	if _, err := b.SetToolPolicy(ctx, actorFrom(ctx), co.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/politica?ok=gravada")
}

func (s *server) handleDeleteToolPolicy(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.policyBackend(w, r, co)
	if !ok {
		return
	}
	id, err := domain.ParseID(r.PathValue("policy"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := b.DeleteToolPolicy(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/politica?ok=apagada")
}

func (s *server) handleTestTool(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.policyBackend(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	persona, err := s.be.GetPersona(ctx, co.ID, trimForm(r, "persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	integID, err := s.integrationIDByRef(ctx, co, trimForm(r, "integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	in := service.ToolTestInput{IntegrationID: integID, Tool: trimForm(r, "tool"), PersonaID: persona.ID, Execute: trimForm(r, "execute") == "1"}
	args := trimForm(r, "args")
	if args != "" {
		if !json.Valid([]byte(args)) {
			s.fail(w, r, &co, domain.Invalid("args", i18n.T(ctx, "não é um JSON válido")))
			return
		}
		in.Arguments = json.RawMessage(args)
	}
	res, err := b.TestToolCall(ctx, actorFrom(ctx), co.ID, in)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.policyVM(ctx, r, co, b)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.TestTool, vm.TestArgs = in.Tool, args
	for i := range vm.TestPersonaOpts {
		vm.TestPersonaOpts[i].Selected = vm.TestPersonaOpts[i].Value == persona.Slug
	}
	for i := range vm.TestIntegration {
		vm.TestIntegration[i].Selected = vm.TestIntegration[i].Value == res.Integration
	}
	t := &views.ToolTestVM{Persona: res.Persona, Tool: res.Integration + "/" + res.Tool, Decision: decisionBadge(ctx, res.Decision), Message: res.Message, Output: res.Output}
	if res.Status != "" {
		t.Status, t.Risk = toolStatusBadge(ctx, res.Status), riskBadge(ctx, res.Risk)
		d, o := verdictText(ctx, res.Verdict)
		t.Verdict, t.Origin = d, o
	}
	if res.GateID != nil {
		t.GateHref = "/c/" + co.Slug + "/gates/" + res.GateID.String()
	}
	vm.Test = t
	sh := s.toolsShell(r, co, i18n.T(ctx, "Política"), views.Crumb{Label: i18n.T(ctx, "Política")})
	s.render(w, r, http.StatusOK, views.ToolPolicyPage(sh, vm))
}
