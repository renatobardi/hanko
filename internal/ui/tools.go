package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Apps, gateway de tools e perfis de ferramentas (ADR-0036): as abas Tools, Perfis, Gateways e Auditoria dentro de
// Integrações, mais o painel "Tools efetivas" da persona. Tudo sob /c/{empresa}/ferramentas, para não disputar
// rota com os slugs das integrações (/integracoes/{integração}).

var toolNotices = map[string]string{
	"atualizado": i18n.N("Catálogo atualizado."),
	"liberada":   i18n.N("Tool liberada."),
	"desligada":  i18n.N("Tool desligada: some do tools/list e as chamadas são recusadas."),
	"revisadas":  i18n.N("Tools em quarentena liberadas."),
	"criado":     i18n.N("Perfil criado. Agora escolha as tools."),
	"salvo":      i18n.N("Perfil atualizado."),
	"escolhas":   i18n.N("Escolhas salvas."),
	"regra":      i18n.N("Regra gravada."),
	"vinculo":    i18n.N("Vínculo gravado."),
	"removido":   i18n.N("Removido."),
	"apagado":    i18n.N("Apagado."),
	"gateway":    i18n.N("Gateway criado. Gere um token para conectar o cliente."),
	"revogado":   i18n.N("Token revogado: o cliente que o usava deixa de funcionar."),
	"ligado":     i18n.N("Gateway ligado."),
	"desligado":  i18n.N("Gateway desligado: os tokens dele param de valer até ser ligado de novo."),
}

func (s *server) toolRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	const b = "/c/{company}/ferramentas"
	mux.HandleFunc("GET "+b, co(s.handleToolsCatalog))
	mux.HandleFunc("POST "+b+"/catalogo/{integration}/atualizar", post(co(s.handleToolsRefresh)))
	mux.HandleFunc("POST "+b+"/catalogo/{integration}/revisar", post(co(s.handleToolsReview)))
	mux.HandleFunc("POST "+b+"/catalogo/{integration}/tools/{tool}/liberar", post(co(s.handleToolStatus(domain.ToolActive))))
	mux.HandleFunc("POST "+b+"/catalogo/{integration}/tools/{tool}/desligar", post(co(s.handleToolStatus(domain.ToolDisabled))))
	mux.HandleFunc("GET "+b+"/perfis", co(s.handleToolProfiles))
	mux.HandleFunc("GET "+b+"/perfis/novo", co(s.handleNewToolProfile))
	mux.HandleFunc("POST "+b+"/perfis/novo", post(co(s.handleCreateToolProfile)))
	mux.HandleFunc("GET "+b+"/perfis/{profile}", co(s.handleToolProfile))
	mux.HandleFunc("POST "+b+"/perfis/{profile}", post(co(s.handleUpdateToolProfile)))
	mux.HandleFunc("POST "+b+"/perfis/{profile}/remover", post(co(s.handleDeleteToolProfile)))
	mux.HandleFunc("POST "+b+"/perfis/{profile}/integracoes/{integration}", post(co(s.handleToolChoices)))
	mux.HandleFunc("POST "+b+"/perfis/{profile}/riscos", post(co(s.handleRiskRule)))
	mux.HandleFunc("POST "+b+"/perfis/{profile}/regras/{rule}/remover", post(co(s.handleDeleteToolRule)))
	mux.HandleFunc("POST "+b+"/perfis/{profile}/vinculos", post(co(s.handleBindToolProfile)))
	mux.HandleFunc("POST "+b+"/perfis/{profile}/vinculos/{binding}/remover", post(co(s.handleUnbindToolProfile)))
	mux.HandleFunc("GET "+b+"/gateways", co(s.handleToolGateways))
	mux.HandleFunc("POST "+b+"/gateways/novo", post(co(s.handleCreateToolGateway)))
	mux.HandleFunc("GET "+b+"/gateways/{gateway}", co(s.handleToolGateway))
	mux.HandleFunc("POST "+b+"/gateways/{gateway}/ativar", post(co(s.handleToolGatewayEnabled)))
	mux.HandleFunc("POST "+b+"/gateways/{gateway}/perfil", post(co(s.handleToolGatewayProfile)))
	mux.HandleFunc("POST "+b+"/gateways/{gateway}/tokens", post(co(s.handleCreateGatewayToken)))
	mux.HandleFunc("POST "+b+"/gateways/{gateway}/tokens/{token}/revogar", post(co(s.handleRevokeGatewayToken)))
	mux.HandleFunc("POST "+b+"/gateways/{gateway}/remover", post(co(s.handleDeleteToolGateway)))
	mux.HandleFunc("GET "+b+"/auditoria", co(s.handleToolAudit))
	mux.HandleFunc("GET "+b+"/efetivas", co(s.handleEffectiveTools))
	s.toolPolicyRoutes(mux) // política de tools (ADR-0053)
	s.gatesPlusRoutes(mux)  // gates estendidos: pergunta, plano, incidentes, persona principal (ADR-0041)
}

func toolsBase(co domain.Company) string { return "/c/" + co.Slug + "/ferramentas" }

func (s *server) toolsShell(r *http.Request, co domain.Company, title string, crumbs ...views.Crumb) views.Shell {
	ctx := r.Context()
	cs := append([]views.Crumb{{Label: i18n.T(ctx, "Integrações"), Href: "/c/" + co.Slug + "/integracoes"}}, crumbs...)
	return s.shell(r, co, "integracoes", title, cs...)
}

func riskBadge(ctx context.Context, r domain.ToolRisk) views.Badge {
	switch r {
	case domain.ToolRiskRead:
		return views.Badge{Label: i18n.T(ctx, "leitura"), Class: "done"}
	case domain.ToolRiskWrite:
		return views.Badge{Label: i18n.T(ctx, "escrita"), Class: "outline"}
	case domain.ToolRiskDestructive:
		return views.Badge{Label: i18n.T(ctx, "destrutiva"), Class: "danger"}
	}
	return views.Badge{Label: i18n.T(ctx, "desconhecido"), Class: "soft"}
}

func toolStatusBadge(ctx context.Context, st domain.ToolStatus) views.Badge {
	switch st {
	case domain.ToolActive:
		return views.Badge{Label: i18n.T(ctx, "ativa"), Class: "outline"}
	case domain.ToolQuarantined:
		return views.Badge{Label: i18n.T(ctx, "quarentena"), Class: "warn"}
	case domain.ToolDisabled:
		return views.Badge{Label: i18n.T(ctx, "desligada"), Class: "soft"}
	}
	return views.Badge{Label: i18n.T(ctx, "removida"), Class: "soft"}
}

func decisionBadge(ctx context.Context, d domain.ToolDecision) views.Badge {
	switch d {
	case domain.ToolDecisionAllow:
		return views.Badge{Label: i18n.T(ctx, "permitida"), Class: "done"}
	case domain.ToolDecisionAsk:
		return views.Badge{Label: i18n.T(ctx, "perguntar antes"), Class: "warn"}
	case domain.ToolDecisionQuarantined:
		return views.Badge{Label: i18n.T(ctx, "quarentena"), Class: "warn"}
	case domain.ToolDecisionError:
		return views.Badge{Label: i18n.T(ctx, "erro"), Class: "danger"}
	}
	return views.Badge{Label: i18n.T(ctx, "negada"), Class: "danger"}
}

func choiceOptions(ctx context.Context) []views.Option {
	return []views.Option{
		{Value: service.ToolChoiceDefault, Label: i18n.T(ctx, "Padrão do perfil")},
		{Value: service.ToolChoiceOff, Label: i18n.T(ctx, "Desligado")},
		{Value: service.ToolChoiceAsk, Label: i18n.T(ctx, "Perguntar antes")},
		{Value: service.ToolChoiceAllow, Label: i18n.T(ctx, "Permitido")},
	}
}

func prettyJSON(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// ---------- Tools (catálogo) ----------

func (s *server) handleToolsCatalog(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	integs, err := s.be.ToolIntegrations(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base, tb := "/c/"+co.Slug, toolsBase(co)
	vm := views.ToolsCatalogVM{Notice: i18n.T(ctx, toolNotices[r.URL.Query().Get("ok")]), HasMCP: len(integs) > 0, Error: r.URL.Query().Get("erro")}
	if vm.Error != "" {
		vm.Error = i18n.T(ctx, "A descoberta falhou: ") + vm.Error
	}
	cur := r.URL.Query().Get("integracao")
	if cur == "" && len(integs) > 0 {
		cur = integs[0].Slug
	}
	var sel *service.ToolIntegration
	for i, it := range integs {
		on := it.Slug == cur
		if on {
			sel = &integs[i]
		}
		vm.Integrations = append(vm.Integrations, views.TabLink{Key: it.Slug, Label: it.Name, Href: tb + "?integracao=" + url.QueryEscape(it.Slug),
			Count: it.Quarantined, On: on})
	}
	if sel != nil {
		loc := loadLoc(co.Timezone)
		v, err := s.be.ListIntegrationTools(ctx, co.ID, sel.ID)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		ib := tb + "/catalogo/" + url.PathEscape(sel.Slug)
		vm.Name, vm.Slug, vm.Stdio, vm.LastError = sel.Name, sel.Slug, v.Stdio, v.Catalog.LastError
		vm.RefreshHref, vm.ReviewHref, vm.IntegrationHref = ib+"/atualizar", ib+"/revisar", base+"/integracoes/"+url.PathEscape(sel.Slug)
		if v.Catalog.SyncedAt != nil {
			vm.Synced = agoCtx(ctx, *v.Catalog.SyncedAt, s.now(), loc)
		}
		for _, t := range v.Tools {
			th := ib + "/tools/" + url.PathEscape(t.Name)
			row := views.ToolRowVM{Name: t.Name, Title: t.Title, Description: excerpt(t.Description, 220), Risk: riskBadge(ctx, t.Risk),
				Status: toolStatusBadge(ctx, t.Status), Seen: agoCtx(ctx, t.LastSeenAt, s.now(), loc), Schema: prettyJSON(t.InputSchema),
				Quarantined: t.Status == domain.ToolQuarantined, Removed: t.Status == domain.ToolRemoved, Disabled: t.Status == domain.ToolDisabled,
				ReleaseHref: th + "/liberar", DisableHref: th + "/desligar"}
			if row.Quarantined {
				vm.Quarantined++
			}
			vm.Rows = append(vm.Rows, row)
		}
	}
	sh := s.toolsShell(r, co, i18n.T(ctx, "Tools"), views.Crumb{Label: i18n.T(ctx, "Tools")})
	s.render(w, r, http.StatusOK, views.ToolsCatalogPage(sh, vm))
}

// toolIntegration resolve a integração MCP da rota.
func (s *server) toolIntegration(ctx context.Context, co domain.Company, slug string) (service.ToolIntegration, error) {
	integs, err := s.be.ToolIntegrations(ctx, co.ID)
	if err != nil {
		return service.ToolIntegration{}, err
	}
	for _, it := range integs {
		if it.Slug == slug || it.ID.String() == slug {
			return it, nil
		}
	}
	return service.ToolIntegration{}, domain.ErrNotFound
}

func (s *server) handleToolsRefresh(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	it, err := s.toolIntegration(ctx, co, r.PathValue("integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	res, err := s.be.RefreshIntegrationTools(ctx, actorFrom(ctx), co.ID, it.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	to := toolsBase(co) + "?integracao=" + url.QueryEscape(it.Slug)
	if !res.OK {
		redirect(w, r, to+"&erro="+url.QueryEscape(res.Error))
		return
	}
	redirect(w, r, to+"&ok=atualizado")
}

func (s *server) handleToolsReview(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	it, err := s.toolIntegration(ctx, co, r.PathValue("integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := s.be.ReviewQuarantinedTools(ctx, actorFrom(ctx), co.ID, it.ID, nil); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"?integracao="+url.QueryEscape(it.Slug)+"&ok=revisadas")
}

func (s *server) handleToolStatus(status domain.ToolStatus) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		ctx := r.Context()
		it, err := s.toolIntegration(ctx, co, r.PathValue("integration"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		if _, err := s.be.SetToolStatus(ctx, actorFrom(ctx), co.ID, it.ID, r.PathValue("tool"), status); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		ok := "liberada"
		if status == domain.ToolDisabled {
			ok = "desligada"
		}
		redirect(w, r, toolsBase(co)+"?integracao="+url.QueryEscape(it.Slug)+"&ok="+ok)
	}
}

// ---------- Perfis ----------

func defaultLabel(ctx context.Context, d domain.ToolDefaultAction) string {
	if d == domain.ToolDefaultAllow {
		return i18n.T(ctx, "Permitir")
	}
	return i18n.T(ctx, "Negar")
}

func (s *server) handleToolProfiles(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	ps, err := s.be.ListToolProfiles(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	tb := toolsBase(co)
	vm := views.ToolProfilesVM{Notice: i18n.T(ctx, toolNotices[r.URL.Query().Get("ok")]), NewHref: tb + "/perfis/novo"}
	for _, p := range ps {
		vm.Rows = append(vm.Rows, views.ToolProfileRowVM{Name: p.Name, Slug: p.Slug, Href: tb + "/perfis/" + url.PathEscape(p.Slug),
			Default: defaultLabel(ctx, p.DefaultAction), Rate: i18n.Tf(ctx, "%d por minuto", p.RateLimitPerMin), Rules: len(p.Rules),
			Bindings: len(p.Bindings), Gateways: p.Gateways})
	}
	sh := s.toolsShell(r, co, i18n.T(ctx, "Perfis"), views.Crumb{Label: i18n.T(ctx, "Perfis")})
	s.render(w, r, http.StatusOK, views.ToolProfilesPage(sh, vm))
}

func (s *server) renderNewToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.ToolProfileFormVM) {
	ctx := r.Context()
	tb := toolsBase(co)
	sh := s.toolsShell(r, co, i18n.T(ctx, "Novo perfil de ferramentas"), views.Crumb{Label: i18n.T(ctx, "Perfis"), Href: tb + "/perfis"},
		views.Crumb{Label: i18n.T(ctx, "Novo")})
	s.render(w, r, status, views.NewToolProfilePage(sh, vm))
}

func (s *server) handleNewToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	tb := toolsBase(co)
	s.renderNewToolProfile(w, r, co, http.StatusOK, views.ToolProfileFormVM{Action: tb + "/perfis/novo", CancelHref: tb + "/perfis",
		Default: string(domain.ToolDefaultDeny), Rate: strconv.Itoa(domain.DefaultToolRateLimit), FieldErr: map[string]string{}})
}

// profileFormError põe o erro do service no formulário do passo 1.
func profileFormError(ctx context.Context, vm *views.ToolProfileFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		msg := ve.Field + ": " + ve.Reason + "."
		switch ve.Field {
		case "name", "slug":
			msg = i18n.T(ctx, "Dê ao perfil um nome que ainda não exista.")
			vm.FieldErr["name"] = msg
		case "rate_limit_per_min":
			msg = i18n.T(ctx, "Use de 1 a 10000 chamadas por minuto.")
			vm.FieldErr["rate_limit_per_min"] = msg
		}
		vm.Error = msg
		return
	}
	if errors.Is(err, domain.ErrConflict) {
		vm.FieldErr["name"] = i18n.T(ctx, "Dê ao perfil um nome que ainda não exista.")
		vm.Error = vm.FieldErr["name"]
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func readProfileForm(r *http.Request) (name, desc string, def domain.ToolDefaultAction, rate int, ok bool) {
	rate, err := strconv.Atoi(trimForm(r, "rate"))
	return trimForm(r, "name"), trimForm(r, "description"), domain.ToolDefaultAction(trimForm(r, "default_action")), rate, err == nil
}

func (s *server) handleCreateToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	tb := toolsBase(co)
	name, desc, def, rate, ok := readProfileForm(r)
	vm := views.ToolProfileFormVM{Action: tb + "/perfis/novo", CancelHref: tb + "/perfis", Name: name, Description: desc, Default: string(def),
		Rate: trimForm(r, "rate"), FieldErr: map[string]string{}}
	if !ok {
		rate = -1
	}
	p, err := s.be.CreateToolProfile(ctx, actorFrom(ctx), co.ID, service.CreateToolProfileInput{Name: name, Description: desc, DefaultAction: def, RateLimitPerMin: rate})
	if err != nil {
		profileFormError(ctx, &vm, err)
		s.renderNewToolProfile(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, tb+"/perfis/"+url.PathEscape(p.Slug)+"?passo=2&ok=criado")
}

func scopeLabel(ctx context.Context, sc domain.ToolBindingScope) string {
	switch sc {
	case domain.ToolScopePersona:
		return i18n.T(ctx, "Persona")
	case domain.ToolScopeProject:
		return i18n.T(ctx, "Projeto")
	}
	return i18n.T(ctx, "Empresa toda")
}

func riskName(ctx context.Context, r domain.ToolRisk) string { return riskBadge(ctx, r).Label }

func (s *server) handleToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetToolProfile(ctx, co.ID, r.PathValue("profile"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	step, _ := strconv.Atoi(r.URL.Query().Get("passo"))
	if step < 1 || step > 3 {
		step = 1
	}
	vm, err := s.toolProfileVM(r, co, p, step)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderToolProfile(w, r, co, http.StatusOK, vm)
}

func (s *server) renderToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.ToolProfileVM) {
	ctx := r.Context()
	sh := s.toolsShell(r, co, vm.Name, views.Crumb{Label: i18n.T(ctx, "Perfis"), Href: toolsBase(co) + "/perfis"}, views.Crumb{Label: vm.Name})
	s.render(w, r, status, views.ToolProfilePage(sh, vm))
}

func (s *server) toolProfileVM(r *http.Request, co domain.Company, p service.ToolProfileView, step int) (views.ToolProfileVM, error) {
	ctx := r.Context()
	tb := toolsBase(co)
	href := tb + "/perfis/" + url.PathEscape(p.Slug)
	vm := views.ToolProfileVM{Notice: i18n.T(ctx, toolNotices[r.URL.Query().Get("ok")]), Name: p.Name, Slug: p.Slug, Step: step,
		DeleteHref: href + "/remover", Gateways: p.Gateways, NextHref: href + "?passo=3", BackHref: tb + "/perfis", ToolsHref: tb,
		RiskAction: href + "/riscos", BindAction: href + "/vinculos", ChoiceOpts: choiceOptions(ctx),
		Form: views.ToolProfileFormVM{Action: href, CancelHref: tb + "/perfis", Edit: true, Name: p.Name, Description: p.Description,
			Default: string(p.DefaultAction), Rate: strconv.Itoa(p.RateLimitPerMin), FieldErr: map[string]string{}}}
	for i, l := range []string{i18n.T(ctx, "1. Nome e padrão"), i18n.T(ctx, "2. Tools"), i18n.T(ctx, "3. Vínculos")} {
		vm.Steps = append(vm.Steps, views.TabLink{Label: l, Href: href + "?passo=" + strconv.Itoa(i+1), On: step == i+1})
	}
	switch step {
	case 2:
		integs, err := s.be.ToolIntegrations(ctx, co.ID)
		if err != nil {
			return vm, err
		}
		group := map[domain.ID]string{}
		perTool := map[string]string{} // integração/tool → escolha
		for _, rule := range p.Rules {
			if rule.IntegrationID == nil {
				continue
			}
			c := service.ToolChoiceAllow
			switch {
			case rule.Effect == domain.ToolExclude:
				c = service.ToolChoiceOff
			case rule.Mode == domain.ToolModeAsk:
				c = service.ToolChoiceAsk
			}
			switch rule.Target {
			case domain.ToolTargetIntegration:
				group[*rule.IntegrationID] = c
			case domain.ToolTargetTool:
				perTool[rule.IntegrationID.String()+"/"+rule.ToolName] = c
			}
		}
		for _, it := range integs {
			pi := views.ProfileIntegrationVM{Name: it.Name, Slug: it.Slug, Action: href + "/integracoes/" + url.PathEscape(it.Slug),
				Stdio: it.Transport == domain.TransportStdio, Group: group[it.ID], NoCatalog: it.Catalog.SyncedAt == nil}
			if !pi.Stdio && !pi.NoCatalog {
				v, err := s.be.ListIntegrationTools(ctx, co.ID, it.ID)
				if err != nil {
					return vm, err
				}
				for _, t := range v.Tools {
					if t.Status == domain.ToolRemoved {
						continue
					}
					pi.Tools = append(pi.Tools, views.ProfileToolVM{Name: t.Name, Risk: riskBadge(ctx, t.Risk), Status: toolStatusBadge(ctx, t.Status),
						Choice: perTool[it.ID.String()+"/"+t.Name]})
				}
			}
			vm.Integrations = append(vm.Integrations, pi)
		}
		for _, rule := range p.Rules {
			if rule.Target != domain.ToolTargetRisk {
				continue
			}
			label := i18n.Tf(ctx, "Risco %s, em todas as integrações", riskName(ctx, rule.Risk))
			if rule.IntegrationSlug != "" {
				label = i18n.Tf(ctx, "Risco %s, em %s", riskName(ctx, rule.Risk), rule.IntegrationSlug)
			}
			eff := i18n.T(ctx, "Desligado")
			if rule.Effect == domain.ToolInclude {
				eff = i18n.T(ctx, "Permitido")
				if rule.Mode == domain.ToolModeAsk {
					eff = i18n.T(ctx, "Perguntar antes")
				}
			}
			vm.RiskRules = append(vm.RiskRules, views.RiskRuleVM{Label: label, Effect: eff, DeleteHref: href + "/regras/" + rule.ID.String() + "/remover"})
		}
		for _, rk := range domain.ToolRisks {
			vm.RiskOpts = append(vm.RiskOpts, views.Option{Value: string(rk), Label: riskName(ctx, rk)})
		}
		vm.RiskIntegOpts = []views.Option{{Value: "", Label: i18n.T(ctx, "Todas as integrações")}}
		for _, it := range integs {
			if it.Transport != domain.TransportStdio {
				vm.RiskIntegOpts = append(vm.RiskIntegOpts, views.Option{Value: it.Slug, Label: it.Name})
			}
		}
	case 3:
		for _, b := range p.Bindings {
			target := b.TargetName
			if b.Scope == domain.ToolScopeCompany {
				target = co.Name
			}
			vm.Bindings = append(vm.Bindings, views.BindingRowVM{Scope: scopeLabel(ctx, b.Scope), Target: target, Priority: b.Priority,
				RemoveHref: href + "/vinculos/" + b.ID.String() + "/remover"})
		}
		projects, err := s.be.ListProjects(ctx, co.ID)
		if err != nil {
			return vm, err
		}
		vm.ProjectOpts = []views.Option{{Value: "", Label: "—"}}
		for _, pr := range projects {
			vm.ProjectOpts = append(vm.ProjectOpts, views.Option{Value: pr.ID.String(), Label: pr.Name})
		}
		personas, err := s.be.ListPersonas(ctx, co.ID)
		if err != nil {
			return vm, err
		}
		vm.PersonaOpts = []views.Option{{Value: "", Label: "—"}}
		for _, pe := range personas {
			vm.PersonaOpts = append(vm.PersonaOpts, views.Option{Value: pe.ID.String(), Label: pe.Name})
		}
	}
	return vm, nil
}

func (s *server) handleUpdateToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetToolProfile(ctx, co.ID, r.PathValue("profile"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	name, desc, def, rate, ok := readProfileForm(r)
	if !ok {
		rate = -1
	}
	if _, err := s.be.UpdateToolProfile(ctx, actorFrom(ctx), co.ID, p.ID, service.UpdateToolProfileInput{Name: &name, Description: &desc,
		DefaultAction: &def, RateLimitPerMin: &rate}); err != nil {
		vm, verr := s.toolProfileVM(r, co, p, 1)
		if verr != nil {
			s.fail(w, r, &co, verr)
			return
		}
		vm.Form.Name, vm.Form.Description, vm.Form.Default, vm.Form.Rate = name, desc, string(def), trimForm(r, "rate")
		profileFormError(ctx, &vm.Form, err)
		s.renderToolProfile(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, toolsBase(co)+"/perfis/"+url.PathEscape(p.Slug)+"?passo=1&ok=salvo")
}

func (s *server) handleDeleteToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetToolProfile(ctx, co.ID, r.PathValue("profile"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := s.be.DeleteToolProfile(ctx, actorFrom(ctx), co.ID, p.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/perfis?ok=apagado")
}

func (s *server) handleToolChoices(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetToolProfile(ctx, co.ID, r.PathValue("profile"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	it, err := s.toolIntegration(ctx, co, r.PathValue("integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", "invalid"))
		return
	}
	in := service.IntegrationToolChoices{IntegrationID: it.ID, Group: r.PostFormValue("group"), Tools: map[string]string{}}
	for k, vals := range r.PostForm {
		if name, ok := strings.CutPrefix(k, "tool:"); ok && len(vals) > 0 {
			in.Tools[name] = vals[0]
		}
	}
	if _, err := s.be.SetIntegrationToolChoices(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/perfis/"+url.PathEscape(p.Slug)+"?passo=2&ok=escolhas")
}

func (s *server) handleRiskRule(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetToolProfile(ctx, co.ID, r.PathValue("profile"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	in := service.ToolRuleInput{Target: domain.ToolTargetRisk, Risk: domain.ToolRisk(trimForm(r, "risk")), Effect: domain.ToolInclude, Mode: domain.ToolModeAllow}
	switch trimForm(r, "choice") {
	case service.ToolChoiceOff:
		in.Effect = domain.ToolExclude
	case service.ToolChoiceAsk:
		in.Mode = domain.ToolModeAsk
	}
	if ref := trimForm(r, "integration"); ref != "" {
		it, err := s.toolIntegration(ctx, co, ref)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		in.IntegrationID = &it.ID
	}
	if _, err := s.be.AddToolProfileRule(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/perfis/"+url.PathEscape(p.Slug)+"?passo=2&ok=regra")
}

func (s *server) handleDeleteToolRule(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetToolProfile(ctx, co.ID, r.PathValue("profile"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	rid, err := domain.ParseID(r.PathValue("rule"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.be.DeleteToolProfileRule(ctx, actorFrom(ctx), co.ID, p.ID, rid); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/perfis/"+url.PathEscape(p.Slug)+"?passo=2&ok=removido")
}

func optFormID(r *http.Request, key string) (*domain.ID, error) {
	v := trimForm(r, key)
	if v == "" {
		return nil, nil
	}
	id, err := domain.ParseID(v)
	if err != nil {
		return nil, domain.Invalid(key, "invalid")
	}
	return &id, nil
}

func (s *server) handleBindToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetToolProfile(ctx, co.ID, r.PathValue("profile"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	in := service.ToolBindingInput{Scope: domain.ToolBindingScope(trimForm(r, "scope"))}
	if v := trimForm(r, "priority"); v != "" {
		if in.Priority, err = strconv.Atoi(v); err != nil {
			s.fail(w, r, &co, domain.Invalid("priority", "use a number"))
			return
		}
	}
	project, err1 := optFormID(r, "project")
	persona, err2 := optFormID(r, "persona")
	if err := errors.Join(err1, err2); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	switch in.Scope {
	case domain.ToolScopeProject:
		in.ProjectID = project
	case domain.ToolScopePersona:
		in.PersonaID = persona
	}
	if _, err := s.be.BindToolProfile(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/perfis/"+url.PathEscape(p.Slug)+"?passo=3&ok=vinculo")
}

func (s *server) handleUnbindToolProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetToolProfile(ctx, co.ID, r.PathValue("profile"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	bid, err := domain.ParseID(r.PathValue("binding"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.be.UnbindToolProfile(ctx, actorFrom(ctx), co.ID, p.ID, bid); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/perfis/"+url.PathEscape(p.Slug)+"?passo=3&ok=removido")
}

// ---------- Gateways ----------

func (s *server) profileOptions(ctx context.Context, co domain.Company, selected domain.ID) ([]views.Option, error) {
	ps, err := s.be.ListToolProfiles(ctx, co.ID)
	if err != nil {
		return nil, err
	}
	out := make([]views.Option, 0, len(ps))
	for _, p := range ps {
		out = append(out, views.Option{Value: p.ID.String(), Label: p.Name, Selected: p.ID == selected})
	}
	return out, nil
}

func (s *server) handleToolGateways(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	gs, err := s.be.ListToolGateways(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	opts, err := s.profileOptions(ctx, co, domain.ID{})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	tb := toolsBase(co)
	loc := loadLoc(co.Timezone)
	vm := views.ToolGatewaysVM{Notice: i18n.T(ctx, toolNotices[r.URL.Query().Get("ok")]), CreateHref: tb + "/gateways/novo", ProfileOpts: opts}
	for _, g := range gs {
		h := tb + "/gateways/" + url.PathEscape(g.Slug)
		row := views.GatewayRowVM{Name: g.Name, Slug: g.Slug, Href: h, Profile: g.ProfileName, Tokens: g.ActiveTokens, Enabled: g.Enabled,
			ToggleHref: h + "/ativar", LastUsed: "—"}
		if g.LastUsedAt != nil {
			row.LastUsed = agoCtx(ctx, *g.LastUsedAt, s.now(), loc)
		}
		vm.Rows = append(vm.Rows, row)
	}
	sh := s.toolsShell(r, co, i18n.T(ctx, "Gateways"), views.Crumb{Label: i18n.T(ctx, "Gateways")})
	s.render(w, r, http.StatusOK, views.ToolGatewaysPage(sh, vm))
}

func (s *server) handleCreateToolGateway(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	pid, err := domain.ParseID(trimForm(r, "profile"))
	if err != nil {
		s.fail(w, r, &co, domain.Invalid("profile", "required"))
		return
	}
	g, err := s.be.CreateToolGateway(ctx, actorFrom(ctx), co.ID, service.CreateToolGatewayInput{Name: trimForm(r, "name"), ProfileID: pid})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/gateways/"+url.PathEscape(g.Slug)+"?ok=gateway")
}

// gatewayEndpoint é a URL que o cliente usa: a URL pública da instalação, senão a do próprio pedido.
func (s *server) gatewayEndpoint(r *http.Request, co domain.Company, slug string) string {
	base := strings.TrimRight(s.platform.PublicURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/mcp/gw/" + co.Slug + "/" + slug
}

func (s *server) toolGatewayVM(r *http.Request, co domain.Company, g service.ToolGatewayView, revealed string) (views.ToolGatewayVM, error) {
	ctx := r.Context()
	h := toolsBase(co) + "/gateways/" + url.PathEscape(g.Slug)
	opts, err := s.profileOptions(ctx, co, g.ProfileID)
	if err != nil {
		return views.ToolGatewayVM{}, err
	}
	loc := loadLoc(co.Timezone)
	now := s.now()
	vm := views.ToolGatewayVM{Notice: i18n.T(ctx, toolNotices[r.URL.Query().Get("ok")]), Name: g.Name, Slug: g.Slug, Profile: g.ProfileName,
		Enabled: g.Enabled, ToggleHref: h + "/ativar", ProfileAction: h + "/perfil", ProfileOpts: opts, EndpointURL: s.gatewayEndpoint(r, co, g.Slug),
		TokenAction: h + "/tokens", Revealed: revealed, DeleteHref: h + "/remover"}
	for _, t := range g.Tokens {
		row := views.GatewayTokenRowVM{Prefix: t.Prefix, Label: t.Label, Actions: strings.Join(t.Actions, ", "), Expires: fmtDateTime(ctx, t.ExpiresAt, loc),
			LastUsed: "—", Active: t.Active(now), RevokeHref: h + "/tokens/" + t.ID.String() + "/revogar"}
		if t.LastUsedAt != nil {
			row.LastUsed = agoCtx(ctx, *t.LastUsedAt, now, loc)
		}
		switch {
		case t.RevokedAt != nil:
			row.State = views.Badge{Label: i18n.T(ctx, "revogado"), Class: "soft"}
		case !now.Before(t.ExpiresAt):
			row.State = views.Badge{Label: i18n.T(ctx, "expirado"), Class: "soft"}
		default:
			row.State = views.Badge{Label: i18n.T(ctx, "ativo"), Class: "done"}
		}
		vm.Tokens = append(vm.Tokens, row)
	}
	return vm, nil
}

func (s *server) renderToolGateway(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.ToolGatewayVM) {
	ctx := r.Context()
	sh := s.toolsShell(r, co, vm.Name, views.Crumb{Label: i18n.T(ctx, "Gateways"), Href: toolsBase(co) + "/gateways"}, views.Crumb{Label: vm.Name})
	s.render(w, r, status, views.ToolGatewayPage(sh, vm))
}

func (s *server) handleToolGateway(w http.ResponseWriter, r *http.Request, co domain.Company) {
	g, err := s.be.GetToolGateway(r.Context(), co.ID, r.PathValue("gateway"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.toolGatewayVM(r, co, g, "")
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderToolGateway(w, r, co, http.StatusOK, vm)
}

func (s *server) handleToolGatewayEnabled(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetToolGateway(ctx, co.ID, r.PathValue("gateway"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	on := enabledForm(r)
	if _, err := s.be.UpdateToolGateway(ctx, actorFrom(ctx), co.ID, g.ID, service.UpdateToolGatewayInput{Enabled: &on}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	ok := "desligado"
	if on {
		ok = "ligado"
	}
	to := toolsBase(co) + "/gateways?ok=" + ok
	if ref := r.Header.Get("HX-Current-URL"); strings.Contains(ref, "/ferramentas/gateways/") {
		to = toolsBase(co) + "/gateways/" + url.PathEscape(g.Slug) + "?ok=" + ok
	}
	redirect(w, r, to)
}

func (s *server) handleToolGatewayProfile(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetToolGateway(ctx, co.ID, r.PathValue("gateway"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	pid, err := domain.ParseID(trimForm(r, "profile"))
	if err != nil {
		s.fail(w, r, &co, domain.Invalid("profile", "required"))
		return
	}
	if _, err := s.be.UpdateToolGateway(ctx, actorFrom(ctx), co.ID, g.ID, service.UpdateToolGatewayInput{ProfileID: &pid}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/gateways/"+url.PathEscape(g.Slug)+"?ok=salvo")
}

func (s *server) handleCreateGatewayToken(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetToolGateway(ctx, co.ID, r.PathValue("gateway"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", "invalid"))
		return
	}
	days, err := strconv.Atoi(trimForm(r, "days"))
	if err != nil || days < 1 {
		s.fail(w, r, &co, domain.Invalid("days", "use 1 a 366"))
		return
	}
	actions := r.PostForm["actions"]
	if len(actions) == 0 {
		s.fail(w, r, &co, domain.Invalid("actions", "escolha ao menos uma ação"))
		return
	}
	_, tok, err := s.be.CreateGatewayToken(ctx, actorFrom(ctx), co.ID, g.ID, service.CreateGatewayTokenInput{Label: trimForm(r, "label"),
		Actions: actions, ExpiresIn: time.Duration(days) * 24 * time.Hour})
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	// O token só existe nesta resposta: mostra a página aqui mesmo, sem redirecionar.
	g, err = s.be.GetToolGateway(ctx, co.ID, g.ID.String())
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.toolGatewayVM(r, co, g, tok)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderToolGateway(w, r, co, http.StatusCreated, vm)
}

func (s *server) handleRevokeGatewayToken(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetToolGateway(ctx, co.ID, r.PathValue("gateway"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	tid, err := domain.ParseID(r.PathValue("token"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.be.RevokeGatewayToken(ctx, actorFrom(ctx), co.ID, g.ID, tid); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/gateways/"+url.PathEscape(g.Slug)+"?ok=revogado")
}

func (s *server) handleDeleteToolGateway(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	g, err := s.be.GetToolGateway(ctx, co.ID, r.PathValue("gateway"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := s.be.DeleteToolGateway(ctx, actorFrom(ctx), co.ID, g.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/gateways?ok=apagado")
}

// ---------- Auditoria ----------

var auditDecisions = []domain.ToolDecision{domain.ToolDecisionAllow, domain.ToolDecisionDeny, domain.ToolDecisionAsk,
	domain.ToolDecisionQuarantined, domain.ToolDecisionError}

func (s *server) handleToolAudit(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	tb := toolsBase(co)
	sh := s.toolsShell(r, co, i18n.T(ctx, "Auditoria"), views.Crumb{Label: i18n.T(ctx, "Auditoria")})
	q := r.URL.Query()
	vm := views.ToolAuditVM{Action: tb + "/auditoria", Tool: q.Get("tool")}
	if !sh.CanAdmin() {
		s.render(w, r, http.StatusOK, views.ToolAuditPage(sh, vm))
		return
	}
	integs, err := s.be.ToolIntegrations(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	f := service.ToolInvocationFilter{Tool: q.Get("tool"), Limit: 200}
	vm.IntegrationOpts = []views.Option{{Value: "", Label: i18n.T(ctx, "Todas as integrações")}}
	for _, it := range integs {
		on := q.Get("integracao") == it.Slug
		if on {
			id := it.ID
			f.IntegrationID = &id
		}
		vm.IntegrationOpts = append(vm.IntegrationOpts, views.Option{Value: it.Slug, Label: it.Name, Selected: on})
	}
	vm.PersonaOpts = []views.Option{{Value: "", Label: i18n.T(ctx, "Todas as personas")}}
	names := map[domain.ID]domain.Persona{}
	for _, p := range personas {
		names[p.ID] = p
		on := q.Get("persona") == p.Slug
		if on {
			id := p.ID
			f.PersonaID = &id
		}
		vm.PersonaOpts = append(vm.PersonaOpts, views.Option{Value: p.Slug, Label: p.Name, Selected: on})
	}
	vm.DecisionOpts = []views.Option{{Value: "", Label: i18n.T(ctx, "Todas as decisões")}}
	for _, d := range auditDecisions {
		on := q.Get("decisao") == string(d)
		if on {
			f.Decision = d
		}
		vm.DecisionOpts = append(vm.DecisionOpts, views.Option{Value: string(d), Label: decisionBadge(ctx, d).Label, Selected: on})
	}
	list, err := s.be.ListToolInvocations(ctx, co.ID, f)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc := loadLoc(co.Timezone)
	base := "/c/" + co.Slug
	for _, v := range list {
		row := views.AuditRowVM{When: fmtDateTime(ctx, v.CreatedAt, loc), Integration: v.IntegrationSlug, Tool: v.Tool,
			Decision: decisionBadge(ctx, v.Decision), Status: v.Status, Latency: fmt.Sprintf("%d ms", v.LatencyMs), Error: v.Error,
			Source: invocationSourceLabel(ctx, v.Source), Policy: v.PolicySource, Result: v.ResultPreview}
		if len(v.ArgsSHA256) >= 12 {
			row.SHA = v.ArgsSHA256[:12]
		}
		switch {
		case v.Source == domain.SourceTest && v.Actor != nil && v.Actor.Type == domain.ActorHuman:
			row.Who = i18n.Tf(ctx, "teste como %s", v.PersonaName)
		case v.GatewayID != nil:
			row.Who, row.Gateway, row.WhoHref = i18n.Tf(ctx, "gateway %s", v.GatewaySlug), true, tb+"/gateways/"+url.PathEscape(v.GatewaySlug)
		case v.PersonaID != nil:
			row.Who = v.PersonaName
			if p, ok := names[*v.PersonaID]; ok {
				row.WhoHref = base + "/personas/" + p.Slug
			}
		default:
			row.Who = "—"
		}
		if v.RunID != nil && row.WhoHref == "" {
			row.WhoHref = base + "/execucoes/" + v.RunID.String()
		}
		if v.GateID != nil {
			row.GateHref = base + "/gates/" + v.GateID.String()
		}
		vm.Rows = append(vm.Rows, row)
	}
	s.render(w, r, http.StatusOK, views.ToolAuditPage(sh, vm))
}

func invocationSourceLabel(ctx context.Context, s domain.ToolInvocationSource) string {
	switch s {
	case domain.SourceGateway:
		return i18n.T(ctx, "gateway")
	case domain.SourceTest:
		return i18n.T(ctx, "teste")
	case domain.SourceHook:
		return i18n.T(ctx, "hook do harness")
	case domain.SourceACP:
		return i18n.T(ctx, "ACP")
	case domain.SourcePlugin:
		return i18n.T(ctx, "plugin")
	}
	return i18n.T(ctx, "execução")
}

// ---------- Caixa de entrada: aba Tools ----------

// countToolGates conta os gates tool pendentes (aba Tools da Caixa de entrada).
func countToolGates(gs []service.GateView) int {
	n := 0
	for _, g := range gs {
		if g.Kind == domain.GateTool {
			n++
		}
	}
	return n
}

// inboxGates filtra os gates da aba: Tools mostra só os gates tool; as outras abas, todos.
func inboxGates(tab string, gs []service.GateView) []service.GateView {
	if tab != "tools" {
		return gs
	}
	out := make([]service.GateView, 0, len(gs))
	for _, g := range gs {
		if g.Kind == domain.GateTool {
			out = append(out, g)
		}
	}
	return out
}

// ---------- persona: tools efetivas ----------

func verdictText(ctx context.Context, v domain.ToolVerdict) (decision, origin string) {
	switch {
	case !v.Allowed:
		decision = i18n.T(ctx, "Desligado")
	case v.Mode == domain.ToolModeAsk:
		decision = i18n.T(ctx, "Perguntar antes")
	default:
		decision = i18n.T(ctx, "Permitido")
	}
	switch v.Source {
	case "policy:persona", "policy:company":
		origin = i18n.T(ctx, "política da empresa")
		if v.Source == "policy:persona" {
			origin = i18n.T(ctx, "política da persona")
		}
	case "no_profile":
		origin = i18n.T(ctx, "sem perfil")
	case "default":
		origin = i18n.T(ctx, "padrão do perfil")
	default:
		origin = i18n.T(ctx, "regra")
		if r := v.Rule; r != nil {
			switch r.Target {
			case domain.ToolTargetTool:
				origin = i18n.Tf(ctx, "regra da tool %s", r.ToolName)
			case domain.ToolTargetIntegration:
				origin = i18n.Tf(ctx, "regra da integração %s", r.IntegrationSlug)
			case domain.ToolTargetRisk:
				origin = i18n.Tf(ctx, "regra de risco %s", riskName(ctx, r.Risk))
			}
		}
	}
	return decision, origin
}

func (s *server) handleEffectiveTools(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.URL.Query().Get("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	v, err := s.be.EffectiveToolsFor(ctx, co.ID, p.ID, nil)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	tb := toolsBase(co)
	vm := views.EffectiveToolsVM{ToolsHref: tb}
	if v.Profile != nil {
		vm.Profile, vm.ProfileHref = v.Profile.Name, tb+"/perfis/"+url.PathEscape(v.Profile.Slug)
		if v.Binding != nil {
			vm.Origin = i18n.Tf(ctx, "vínculo: %s", scopeLabel(ctx, v.Binding.Scope))
			if v.Binding.Scope == domain.ToolScopeProject {
				vm.Origin = i18n.T(ctx, "vínculo de projeto (vale nas tasks do projeto)")
			}
		}
	}
	for _, ei := range v.Integrations {
		if ei.Transport == domain.TransportStdio {
			d, o := verdictText(ctx, ei.Verdict)
			vm.Rows = append(vm.Rows, views.EffectiveRowVM{Integration: ei.Slug, Tool: "*", Risk: views.Badge{Label: "stdio", Class: "soft"},
				Visible: ei.Verdict.Allowed, Decision: d, Origin: o})
			continue
		}
		for _, t := range ei.Tools {
			if t.Tool.Status == domain.ToolRemoved {
				continue
			}
			d, o := verdictText(ctx, t.Verdict)
			if t.Verdict.Allowed && !t.Visible {
				d = toolStatusBadge(ctx, t.Tool.Status).Label
				o = i18n.T(ctx, "estado no catálogo")
			}
			vm.Rows = append(vm.Rows, views.EffectiveRowVM{Integration: ei.Slug, Tool: t.Tool.Name, Risk: riskBadge(ctx, t.Tool.Risk),
				Visible: t.Visible, Decision: d, Origin: o})
		}
	}
	s.render(w, r, http.StatusOK, views.EffectiveToolsCard(vm))
}
