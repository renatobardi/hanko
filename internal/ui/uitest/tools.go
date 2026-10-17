package uitest

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Apps, gateway de tools e perfis de ferramentas (ADR-0036) em memória. Os dados de exemplo nascem na primeira
// chamada (um MCP http com três tools, um stdio, um perfil ligado à primeira persona, um gateway com token).

// ToolsData é o estado das telas de tools do Fake.
type ToolsData struct {
	seeded       bool
	Integrations []service.ToolIntegration
	Catalog      map[domain.ID][]domain.IntegrationTool
	Profiles     []service.ToolProfileView
	Gateways     []service.ToolGatewayView
	Invocations  []domain.ToolInvocation
	// LastChoices é a última chamada de SetIntegrationToolChoices (para os testes).
	LastChoices service.IntegrationToolChoices
}

func (f *Fake) toolsSeed() {
	if f.Tools.seeded {
		return
	}
	f.Tools.seeded = true
	now := Now()
	synced := now.Add(-2 * time.Hour)
	gh, local := ID("int:github"), ID("int:local")
	f.Tools.Integrations = []service.ToolIntegration{
		{ID: gh, Slug: "github", Name: "GitHub", Transport: domain.TransportHTTP, Enabled: true,
			Catalog: domain.ToolCatalogState{IntegrationID: gh, SyncedAt: &synced, ToolsCount: 3}, Tools: 3, Quarantined: 1},
		{ID: local, Slug: "local", Name: "Ferramentas locais", Transport: domain.TransportStdio, Enabled: true,
			Catalog: domain.ToolCatalogState{IntegrationID: local}},
	}
	ro, des, nd := true, true, false
	mk := func(name, desc string, risk domain.ToolRisk, st domain.ToolStatus, a domain.ToolAnnotations) domain.IntegrationTool {
		return domain.IntegrationTool{ID: ID("tool:" + name), IntegrationID: gh, IntegrationSlug: "github", Name: name, Description: desc,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"}}}`), Annotations: a, SchemaSHA256: "ab12cd34ef56",
			Risk: risk, Status: st, FirstSeenAt: now.Add(-48 * time.Hour), LastSeenAt: synced}
	}
	f.Tools.Catalog = map[domain.ID][]domain.IntegrationTool{gh: {
		mk("create_issue", "Abre uma issue no repositório.", domain.ToolRiskWrite, domain.ToolActive, domain.ToolAnnotations{DestructiveHint: &nd}),
		mk("delete_repo", "Apaga um repositório.", domain.ToolRiskDestructive, domain.ToolQuarantined, domain.ToolAnnotations{DestructiveHint: &des}),
		mk("list_issues", "Lista as issues abertas.", domain.ToolRiskRead, domain.ToolActive, domain.ToolAnnotations{ReadOnlyHint: &ro}),
	}}
	pid := ID("profile:seguro")
	ghID := gh
	prof := service.ToolProfileView{ToolProfile: domain.ToolProfile{ID: pid, CompanyID: f.Companies[0].ID, Slug: "seguro", Name: "Seguro",
		DefaultAction: domain.ToolDefaultDeny, RateLimitPerMin: 120, CreatedAt: now, UpdatedAt: now,
		Rules: []domain.ToolRule{
			{ID: ID("rule:gh"), Target: domain.ToolTargetIntegration, IntegrationID: &ghID, IntegrationSlug: "github", Effect: domain.ToolInclude, Mode: domain.ToolModeAllow},
			{ID: ID("rule:create"), Target: domain.ToolTargetTool, IntegrationID: &ghID, IntegrationSlug: "github", ToolName: "create_issue", Effect: domain.ToolInclude, Mode: domain.ToolModeAsk},
			{ID: ID("rule:destructive"), Target: domain.ToolTargetRisk, Risk: domain.ToolRiskDestructive, Effect: domain.ToolExclude, Mode: domain.ToolModeAllow},
		}}, Gateways: 1}
	if len(f.Personas) > 0 {
		p := f.Personas[0]
		prof.Bindings = []domain.ToolBinding{{ID: ID("binding:p0"), ProfileID: pid, Scope: domain.ToolScopePersona, PersonaID: &p.ID, TargetName: p.Name,
			CreatedAt: now, ProfileSlug: "seguro"}}
	}
	f.Tools.Profiles = []service.ToolProfileView{prof}
	used := now.Add(-15 * time.Minute)
	gwID := ID("gateway:ide")
	f.Tools.Gateways = []service.ToolGatewayView{{ToolGateway: domain.ToolGateway{ID: gwID, CompanyID: f.Companies[0].ID, Slug: "minha-ide", Name: "Minha IDE",
		ProfileID: pid, ProfileSlug: "seguro", ProfileName: "Seguro", Enabled: true, ActiveTokens: 1, LastUsedAt: &used, CreatedAt: now, UpdatedAt: now},
		Tokens: []domain.ToolGatewayToken{{ID: ID("token:ide"), GatewayID: gwID, Label: "notebook", Prefix: "hkgw_Ab12Cd", Actions: domain.GatewayActions,
			ExpiresAt: now.Add(80 * 24 * time.Hour), LastUsedAt: &used, CreatedAt: now}}}}
	if len(f.Personas) > 0 {
		p := f.Personas[0]
		gate := ID("gate:tool")
		f.Tools.Invocations = []domain.ToolInvocation{
			{ID: ID("inv:1"), PersonaID: &p.ID, PersonaName: p.Name, IntegrationID: &ghID, IntegrationSlug: "github", Tool: "list_issues",
				ArgsSHA256: "9f86d081884c7d659a2feaa0c55ad015", ArgsBytes: 24, Decision: domain.ToolDecisionAllow, Status: domain.ToolCallOK, LatencyMs: 182, CreatedAt: now.Add(-10 * time.Minute)},
			{ID: ID("inv:2"), PersonaID: &p.ID, PersonaName: p.Name, IntegrationID: &ghID, IntegrationSlug: "github", Tool: "create_issue",
				ArgsSHA256: "60303ae22b998861bce3b28f33eec1be", ArgsBytes: 88, Decision: domain.ToolDecisionAsk, GateID: &gate, Status: domain.ToolCallPending, CreatedAt: now.Add(-5 * time.Minute)},
			{ID: ID("inv:3"), GatewayID: &gwID, GatewaySlug: "minha-ide", IntegrationID: &ghID, IntegrationSlug: "github", Tool: "delete_repo",
				ArgsSHA256: "fd61a03af4f77d870fc21e05e7e80678", ArgsBytes: 30, Decision: domain.ToolDecisionQuarantined, Status: domain.ToolCallBlocked,
				Error: "a tool está em quarentena", CreatedAt: now.Add(-2 * time.Minute)},
		}
	}
}

func (f *Fake) toolsAdmin(ctx context.Context) error {
	if r, _ := f.MyRole(ctx, domain.ID{}); !r.AtLeast(domain.MemberAdmin) {
		return domain.ErrForbidden
	}
	return nil
}

func (f *Fake) ToolIntegrations(context.Context, domain.ID) ([]service.ToolIntegration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ToolIntegrations"); err != nil {
		return nil, err
	}
	f.toolsSeed()
	return append([]service.ToolIntegration(nil), f.Tools.Integrations...), nil
}

func (f *Fake) ListIntegrationTools(_ context.Context, _, integrationID domain.ID) (service.IntegrationToolsView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolsSeed()
	for _, it := range f.Tools.Integrations {
		if it.ID == integrationID {
			v := service.IntegrationToolsView{Catalog: it.Catalog, Stdio: it.Transport == domain.TransportStdio,
				Tools: append([]domain.IntegrationTool(nil), f.Tools.Catalog[it.ID]...)}
			v.Integration.ID, v.Integration.Slug, v.Integration.Name, v.Integration.Kind = it.ID, it.Slug, it.Name, domain.IntegrationMCP
			v.Integration.Config.Transport = it.Transport
			return v, nil
		}
	}
	return service.IntegrationToolsView{}, domain.ErrNotFound
}

func (f *Fake) RefreshIntegrationTools(ctx context.Context, _ domain.Actor, _, integrationID domain.ID) (service.ToolCatalogResult, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return service.ToolCatalogResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RefreshIntegrationTools")
	if err := f.fail("RefreshIntegrationTools"); err != nil {
		return service.ToolCatalogResult{OK: false, Error: err.Error()}, nil
	}
	f.toolsSeed()
	return service.ToolCatalogResult{OK: true, Tools: len(f.Tools.Catalog[integrationID])}, nil
}

func (f *Fake) SetToolStatus(ctx context.Context, _ domain.Actor, _, integrationID domain.ID, tool string, status domain.ToolStatus) (domain.IntegrationTool, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return domain.IntegrationTool{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetToolStatus")
	f.toolsSeed()
	ts := f.Tools.Catalog[integrationID]
	for i := range ts {
		if ts[i].Name == tool {
			ts[i].Status = status
			return ts[i], nil
		}
	}
	return domain.IntegrationTool{}, domain.ErrNotFound
}

func (f *Fake) ReviewQuarantinedTools(ctx context.Context, _ domain.Actor, _, integrationID domain.ID, _ []string) (int, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ReviewQuarantinedTools")
	f.toolsSeed()
	n := 0
	ts := f.Tools.Catalog[integrationID]
	for i := range ts {
		if ts[i].Status == domain.ToolQuarantined {
			ts[i].Status = domain.ToolActive
			n++
		}
	}
	return n, nil
}

func (f *Fake) profileIndex(ref string) int {
	for i, p := range f.Tools.Profiles {
		if p.Slug == ref || p.ID.String() == ref {
			return i
		}
	}
	return -1
}

func (f *Fake) CreateToolProfile(ctx context.Context, _ domain.Actor, companyID domain.ID, in service.CreateToolProfileInput) (service.ToolProfileView, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return service.ToolProfileView{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateToolProfile")
	f.toolsSeed()
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return service.ToolProfileView{}, domain.Invalid("name", "required")
	}
	if in.RateLimitPerMin < 0 || in.RateLimitPerMin > domain.MaxToolRateLimit {
		return service.ToolProfileView{}, domain.Invalid("rate_limit_per_min", "use 1 a 10000")
	}
	slug := domain.Slugify(name)
	if f.profileIndex(slug) >= 0 {
		return service.ToolProfileView{}, domain.ErrConflict
	}
	def := in.DefaultAction
	if def == "" {
		def = domain.ToolDefaultDeny
	}
	p := service.ToolProfileView{ToolProfile: domain.ToolProfile{ID: ID("profile:" + slug), CompanyID: companyID, Slug: slug, Name: name,
		Description: in.Description, DefaultAction: def, RateLimitPerMin: max(in.RateLimitPerMin, 1), Rules: []domain.ToolRule{},
		Bindings: []domain.ToolBinding{}, CreatedAt: Now(), UpdatedAt: Now()}}
	f.Tools.Profiles = append(f.Tools.Profiles, p)
	return p, nil
}

func (f *Fake) GetToolProfile(_ context.Context, _ domain.ID, ref string) (service.ToolProfileView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolsSeed()
	if i := f.profileIndex(ref); i >= 0 {
		return f.Tools.Profiles[i], nil
	}
	return service.ToolProfileView{}, domain.ErrNotFound
}

func (f *Fake) ListToolProfiles(context.Context, domain.ID) ([]service.ToolProfileView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolsSeed()
	return append([]service.ToolProfileView(nil), f.Tools.Profiles...), nil
}

func (f *Fake) UpdateToolProfile(ctx context.Context, _ domain.Actor, _, profileID domain.ID, in service.UpdateToolProfileInput) (service.ToolProfileView, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return service.ToolProfileView{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateToolProfile")
	i := f.profileIndex(profileID.String())
	if i < 0 {
		return service.ToolProfileView{}, domain.ErrNotFound
	}
	p := &f.Tools.Profiles[i]
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return service.ToolProfileView{}, domain.Invalid("name", "required")
		}
		p.Name = *in.Name
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.DefaultAction != nil {
		p.DefaultAction = *in.DefaultAction
	}
	if in.RateLimitPerMin != nil {
		if *in.RateLimitPerMin < 1 {
			return service.ToolProfileView{}, domain.Invalid("rate_limit_per_min", "use 1 a 10000")
		}
		p.RateLimitPerMin = *in.RateLimitPerMin
	}
	return *p, nil
}

func (f *Fake) DeleteToolProfile(ctx context.Context, _ domain.Actor, _, profileID domain.ID) error {
	if err := f.toolsAdmin(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeleteToolProfile")
	i := f.profileIndex(profileID.String())
	if i < 0 {
		return domain.ErrNotFound
	}
	if f.Tools.Profiles[i].Gateways > 0 {
		return domain.Invalid("profile", "o perfil é usado por gateway")
	}
	f.Tools.Profiles = append(f.Tools.Profiles[:i], f.Tools.Profiles[i+1:]...)
	return nil
}

func (f *Fake) AddToolProfileRule(ctx context.Context, _ domain.Actor, _, profileID domain.ID, in service.ToolRuleInput) (domain.ToolRule, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return domain.ToolRule{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("AddToolProfileRule")
	i := f.profileIndex(profileID.String())
	if i < 0 {
		return domain.ToolRule{}, domain.ErrNotFound
	}
	if !in.Target.Valid() || !in.Effect.Valid() || in.Target == domain.ToolTargetRisk && !in.Risk.Valid() {
		return domain.ToolRule{}, domain.Invalid("rule", "invalid")
	}
	r := domain.ToolRule{ID: domain.NewID(), Target: in.Target, IntegrationID: in.IntegrationID, ToolName: in.ToolName, Risk: in.Risk,
		Effect: in.Effect, Mode: in.Mode}
	f.Tools.Profiles[i].Rules = append(f.Tools.Profiles[i].Rules, r)
	return r, nil
}

func (f *Fake) DeleteToolProfileRule(ctx context.Context, _ domain.Actor, _, profileID, ruleID domain.ID) error {
	if err := f.toolsAdmin(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeleteToolProfileRule")
	i := f.profileIndex(profileID.String())
	if i < 0 {
		return domain.ErrNotFound
	}
	rs := f.Tools.Profiles[i].Rules
	for j := range rs {
		if rs[j].ID == ruleID {
			f.Tools.Profiles[i].Rules = append(rs[:j], rs[j+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Fake) SetIntegrationToolChoices(ctx context.Context, _ domain.Actor, _, profileID domain.ID, in service.IntegrationToolChoices) (service.ToolProfileView, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return service.ToolProfileView{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("SetIntegrationToolChoices")
	f.Tools.LastChoices = in
	i := f.profileIndex(profileID.String())
	if i < 0 {
		return service.ToolProfileView{}, domain.ErrNotFound
	}
	return f.Tools.Profiles[i], nil
}

func (f *Fake) BindToolProfile(ctx context.Context, _ domain.Actor, _, profileID domain.ID, in service.ToolBindingInput) (domain.ToolBinding, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return domain.ToolBinding{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("BindToolProfile")
	i := f.profileIndex(profileID.String())
	if i < 0 {
		return domain.ToolBinding{}, domain.ErrNotFound
	}
	if !in.Scope.Valid() || in.Scope == domain.ToolScopeProject && in.ProjectID == nil || in.Scope == domain.ToolScopePersona && in.PersonaID == nil {
		return domain.ToolBinding{}, domain.Invalid("scope", "invalid")
	}
	b := domain.ToolBinding{ID: domain.NewID(), ProfileID: profileID, Scope: in.Scope, ProjectID: in.ProjectID, PersonaID: in.PersonaID,
		Priority: in.Priority, CreatedAt: Now()}
	f.Tools.Profiles[i].Bindings = append(f.Tools.Profiles[i].Bindings, b)
	return b, nil
}

func (f *Fake) UnbindToolProfile(ctx context.Context, _ domain.Actor, _, profileID, bindingID domain.ID) error {
	if err := f.toolsAdmin(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UnbindToolProfile")
	i := f.profileIndex(profileID.String())
	if i < 0 {
		return domain.ErrNotFound
	}
	bs := f.Tools.Profiles[i].Bindings
	for j := range bs {
		if bs[j].ID == bindingID {
			f.Tools.Profiles[i].Bindings = append(bs[:j], bs[j+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Fake) EffectiveToolsFor(_ context.Context, _, personaID domain.ID, projectID *domain.ID) (service.EffectiveTools, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolsSeed()
	out := service.EffectiveTools{PersonaID: personaID, ProjectID: projectID, Integrations: []service.EffectiveIntegration{}}
	var best *domain.ToolBinding
	var prof *domain.ToolProfile
	for i := range f.Tools.Profiles {
		for _, b := range f.Tools.Profiles[i].Bindings {
			ok := b.Scope == domain.ToolScopeCompany || b.Scope == domain.ToolScopePersona && b.PersonaID != nil && *b.PersonaID == personaID ||
				b.Scope == domain.ToolScopeProject && projectID != nil && b.ProjectID != nil && *b.ProjectID == *projectID
			if ok && (best == nil || domain.BindingWins(b, *best)) {
				bb := b
				best, prof = &bb, &f.Tools.Profiles[i].ToolProfile
			}
		}
	}
	out.Profile, out.Binding = prof, best
	for _, it := range f.Tools.Integrations {
		ei := service.EffectiveIntegration{IntegrationID: it.ID, Slug: it.Slug, Name: it.Name, Transport: it.Transport, Enabled: it.Enabled,
			Catalog: it.Catalog, Tools: []service.EffectiveTool{}}
		if it.Transport == domain.TransportStdio {
			ei.Verdict = domain.EvaluateIntegration(prof, it.ID)
		}
		for _, t := range f.Tools.Catalog[it.ID] {
			v := domain.EvaluateTool(prof, it.ID, t.Name, t.Risk)
			ei.Tools = append(ei.Tools, service.EffectiveTool{Tool: t, Verdict: v, Visible: v.Allowed && t.Status == domain.ToolActive})
		}
		out.Integrations = append(out.Integrations, ei)
	}
	return out, nil
}

func (f *Fake) gatewayIndex(ref string) int {
	for i, g := range f.Tools.Gateways {
		if g.Slug == ref || g.ID.String() == ref {
			return i
		}
	}
	return -1
}

func (f *Fake) CreateToolGateway(ctx context.Context, _ domain.Actor, companyID domain.ID, in service.CreateToolGatewayInput) (service.ToolGatewayView, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return service.ToolGatewayView{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateToolGateway")
	f.toolsSeed()
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return service.ToolGatewayView{}, domain.Invalid("name", "required")
	}
	pi := f.profileIndex(in.ProfileID.String())
	if pi < 0 {
		return service.ToolGatewayView{}, domain.Invalid("profile", "not found")
	}
	slug := domain.Slugify(name)
	if f.gatewayIndex(slug) >= 0 {
		return service.ToolGatewayView{}, domain.ErrConflict
	}
	p := f.Tools.Profiles[pi]
	g := service.ToolGatewayView{ToolGateway: domain.ToolGateway{ID: ID("gateway:" + slug), CompanyID: companyID, Slug: slug, Name: name,
		ProfileID: p.ID, ProfileSlug: p.Slug, ProfileName: p.Name, Enabled: true, CreatedAt: Now(), UpdatedAt: Now()}, Tokens: []domain.ToolGatewayToken{}}
	f.Tools.Profiles[pi].Gateways++
	f.Tools.Gateways = append(f.Tools.Gateways, g)
	return g, nil
}

func (f *Fake) ListToolGateways(context.Context, domain.ID) ([]domain.ToolGateway, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolsSeed()
	out := make([]domain.ToolGateway, 0, len(f.Tools.Gateways))
	for _, g := range f.Tools.Gateways {
		out = append(out, g.ToolGateway)
	}
	return out, nil
}

func (f *Fake) GetToolGateway(_ context.Context, _ domain.ID, ref string) (service.ToolGatewayView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolsSeed()
	if i := f.gatewayIndex(ref); i >= 0 {
		g := f.Tools.Gateways[i]
		g.Tokens = append([]domain.ToolGatewayToken(nil), g.Tokens...)
		return g, nil
	}
	return service.ToolGatewayView{}, domain.ErrNotFound
}

func (f *Fake) UpdateToolGateway(ctx context.Context, _ domain.Actor, _, gatewayID domain.ID, in service.UpdateToolGatewayInput) (service.ToolGatewayView, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return service.ToolGatewayView{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateToolGateway")
	i := f.gatewayIndex(gatewayID.String())
	if i < 0 {
		return service.ToolGatewayView{}, domain.ErrNotFound
	}
	g := &f.Tools.Gateways[i]
	if in.Enabled != nil {
		g.Enabled = *in.Enabled
	}
	if in.Name != nil {
		g.Name = *in.Name
	}
	if in.ProfileID != nil {
		pi := f.profileIndex(in.ProfileID.String())
		if pi < 0 {
			return service.ToolGatewayView{}, domain.Invalid("profile", "not found")
		}
		g.ProfileID, g.ProfileSlug, g.ProfileName = f.Tools.Profiles[pi].ID, f.Tools.Profiles[pi].Slug, f.Tools.Profiles[pi].Name
	}
	return *g, nil
}

func (f *Fake) DeleteToolGateway(ctx context.Context, _ domain.Actor, _, gatewayID domain.ID) error {
	if err := f.toolsAdmin(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeleteToolGateway")
	i := f.gatewayIndex(gatewayID.String())
	if i < 0 {
		return domain.ErrNotFound
	}
	f.Tools.Gateways = append(f.Tools.Gateways[:i], f.Tools.Gateways[i+1:]...)
	return nil
}

// FakeGatewayToken é o valor que o Fake devolve ao gerar um token (para os testes conferirem o "mostrado uma vez").
const FakeGatewayToken = "hkgw_FAKEtokenValue0123456789abcdefghijk"

func (f *Fake) CreateGatewayToken(ctx context.Context, _ domain.Actor, _, gatewayID domain.ID, in service.CreateGatewayTokenInput) (domain.ToolGatewayToken, string, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return domain.ToolGatewayToken{}, "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateGatewayToken")
	i := f.gatewayIndex(gatewayID.String())
	if i < 0 {
		return domain.ToolGatewayToken{}, "", domain.ErrNotFound
	}
	if len(in.Actions) == 0 || in.ExpiresIn <= 0 {
		return domain.ToolGatewayToken{}, "", domain.Invalid("token", "invalid")
	}
	t := domain.ToolGatewayToken{ID: domain.NewID(), GatewayID: gatewayID, Label: in.Label, Prefix: FakeGatewayToken[:11], Actions: in.Actions,
		ExpiresAt: Now().Add(in.ExpiresIn), CreatedAt: Now()}
	f.Tools.Gateways[i].Tokens = append(f.Tools.Gateways[i].Tokens, t)
	f.Tools.Gateways[i].ActiveTokens++
	return t, FakeGatewayToken, nil
}

func (f *Fake) RevokeGatewayToken(ctx context.Context, _ domain.Actor, _, gatewayID, tokenID domain.ID) (domain.ToolGatewayToken, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return domain.ToolGatewayToken{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RevokeGatewayToken")
	i := f.gatewayIndex(gatewayID.String())
	if i < 0 {
		return domain.ToolGatewayToken{}, domain.ErrNotFound
	}
	for j := range f.Tools.Gateways[i].Tokens {
		t := &f.Tools.Gateways[i].Tokens[j]
		if t.ID == tokenID {
			now := Now()
			t.RevokedAt = &now
			return *t, nil
		}
	}
	return domain.ToolGatewayToken{}, domain.ErrNotFound
}

func (f *Fake) ListToolInvocations(ctx context.Context, _ domain.ID, flt service.ToolInvocationFilter) ([]domain.ToolInvocation, error) {
	if err := f.toolsAdmin(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolsSeed()
	var out []domain.ToolInvocation
	for _, v := range f.Tools.Invocations {
		if flt.Decision != "" && v.Decision != flt.Decision || flt.Tool != "" && v.Tool != flt.Tool ||
			flt.PersonaID != nil && (v.PersonaID == nil || *v.PersonaID != *flt.PersonaID) ||
			flt.IntegrationID != nil && (v.IntegrationID == nil || *v.IntegrationID != *flt.IntegrationID) {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}
