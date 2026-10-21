package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Readiness da persona (#10), seletor de modelo (#17) e ambiente do projeto (#35), ADR-0055.

func (s *server) readinessRoutes(mux langMux, post func(http.HandlerFunc) http.HandlerFunc) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	mux.HandleFunc("GET /c/{company}/personas/{persona}/prontidao", co(s.handlePersonaReadiness))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/prontidao", post(co(s.handleTestPersonaReadiness)))
	mux.HandleFunc("GET /c/{company}/projetos/{project}/ambiente", co(s.handleProjectEnvironment))
	mux.HandleFunc("POST /c/{company}/projetos/{project}/ambiente", post(co(s.handleSetProjectEnvironment)))
}

// ---- seletor de modelo ----

const modelOther = "__other"

// modelFromForm lê o modelo do formulário: o select (<nome>_choice) e, em "Outro…", o campo livre. Sem o select (cliente
// antigo, teste) vale o campo livre, como sempre.
func modelFromForm(r *http.Request, name string) string {
	free := trimForm(r, name)
	choice, has := r.PostForm[name+"_choice"]
	if !has || len(choice) == 0 {
		return free
	}
	switch c := choice[0]; c {
	case modelOther:
		return free
	default:
		return c
	}
}

// modelPicker monta o seletor com o catálogo híbrido (aliases do harness, preços, o que testes e execuções viram).
// Se o catálogo não carrega, o seletor cai para "Padrão" + "Outro…": nunca bloqueia a contratação.
func (s *server) modelPicker(ctx context.Context, co domain.Company, id, name, current string) views.ModelPickerVM {
	vm := views.ModelPickerVM{ID: id, Name: name, Value: current}
	cat, err := s.be.ModelCatalog(ctx, co.ID, "")
	if err != nil {
		vm.Other = current != ""
		return vm
	}
	groups := map[string]*views.ModelGroupVM{}
	order := []string{}
	label := map[string]string{"claude": "Claude Code", "codex": "Codex", "acp": "ACP", "": i18n.T(ctx, "Qualquer harness")}
	found := false
	for _, m := range cat {
		g := groups[m.Agent]
		if g == nil {
			l := label[m.Agent]
			if l == "" {
				l = m.Agent
			}
			g = &views.ModelGroupVM{Label: l}
			groups[m.Agent] = g
			order = append(order, m.Agent)
		}
		sel := m.Model == current
		found = found || sel
		g.Options = append(g.Options, views.ModelOptionVM{Value: m.Model, Label: m.Model, Selected: sel, Unpriced: !m.Priced})
	}
	for _, k := range order {
		vm.Groups = append(vm.Groups, *groups[k])
	}
	vm.Other = current != "" && !found
	return vm
}

// ---- prontidão da persona ----

func readinessStepLabel(ctx context.Context, name string) string {
	switch name {
	case domain.StepEnvironment:
		return i18n.T(ctx, "Ambiente de execução")
	case domain.StepBinary:
		return i18n.T(ctx, "Binário do harness")
	case domain.StepAuth:
		return i18n.T(ctx, "Login ou chave de API")
	case domain.StepModel:
		return i18n.T(ctx, "Modelo")
	}
	return name
}

func (s *server) readinessVM(ctx context.Context, co domain.Company, p domain.Persona, rd *domain.Readiness) views.PersonaReadinessVM {
	vm := views.PersonaReadinessVM{Status: "untested", Badge: views.Badge{Label: i18n.T(ctx, "Nunca testada"), Class: "soft"},
		Action: "/c/" + co.Slug + "/personas/" + url.PathEscape(p.Slug) + "/prontidao"}
	if rd == nil {
		return vm
	}
	vm.Status = string(rd.Status)
	switch rd.Status {
	case domain.ReadinessReady:
		vm.Badge = views.Badge{Label: i18n.T(ctx, "Pronta"), Class: "done"}
	default:
		vm.Badge = views.Badge{Label: i18n.T(ctx, "Não pronta"), Class: "danger"}
	}
	vm.Reason, vm.Blocking = rd.Reason, rd.Blocks(p.Agent, p.Model)
	vm.When = relTimeCtx(ctx, rd.CheckedAt, s.now(), loadLoc(co.Timezone))
	vm.Env = i18n.Tf(ctx, "ambiente: %s", envSourceText(ctx, rd.EnvironmentSource))
	if rd.DurationMs > 0 {
		vm.Duration = i18n.Tf(ctx, "%d ms", rd.DurationMs)
	}
	for _, st := range rd.Steps {
		c := views.EnvCheck{OK: st.Status != domain.StepFail, Label: readinessStepLabel(ctx, st.Name), Detail: st.Detail}
		switch st.Status {
		case domain.StepWarn:
			c.Detail = i18n.T(ctx, "aviso") + ": " + st.Detail
		case domain.StepSkip:
			c.Detail = i18n.T(ctx, "não verificado") + dashPrefix(st.Detail)
		}
		vm.Steps = append(vm.Steps, c)
	}
	return vm
}

func dashPrefix(s string) string {
	if s == "" {
		return ""
	}
	return " — " + s
}

func envSourceText(ctx context.Context, src domain.EnvSource) string {
	switch src {
	case domain.EnvFromPersona:
		return i18n.T(ctx, "da persona")
	case domain.EnvFromProject:
		return i18n.T(ctx, "do projeto")
	case domain.EnvFromDefault:
		return i18n.T(ctx, "padrão da empresa")
	}
	return i18n.T(ctx, "local")
}

func (s *server) handlePersonaReadiness(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	rd, err := s.be.GetPersonaReadiness(ctx, co.ID, p.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := s.readinessVM(ctx, co, p, rd)
	vm.AutoTest = r.URL.Query().Get("auto") == "1" && rd == nil
	s.render(w, r, http.StatusOK, views.PersonaReadinessCard(vm))
}

func (s *server) handleTestPersonaReadiness(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	rd, err := s.be.TestPersonaReadiness(ctx, actorFrom(ctx), co.ID, p.ID)
	if err != nil {
		if !isHX(r) || errors.Is(err, domain.ErrNotAllowed) {
			s.fail(w, r, &co, err)
			return
		}
		cur, _ := s.be.GetPersonaReadiness(ctx, co.ID, p.ID)
		vm := s.readinessVM(ctx, co, p, cur)
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
		s.render(w, r, http.StatusOK, views.PersonaReadinessCard(vm))
		return
	}
	if !isHX(r) {
		redirect(w, r, "/c/"+co.Slug+"/personas/"+url.PathEscape(p.Slug)+"#prontidao")
		return
	}
	vm := s.readinessVM(ctx, co, p, &rd)
	if rd.Status == domain.ReadinessReady {
		vm.Notice = i18n.T(ctx, "Teste concluído: a persona está pronta para receber execuções.")
	}
	s.render(w, r, http.StatusOK, views.PersonaReadinessCard(vm))
}

// ---- ambiente do projeto ----

func (s *server) projectEnvVM(ctx context.Context, co domain.Company, p domain.Project) (views.ProjectEnvVM, error) {
	list, err := s.be.ListEnvironments(ctx, co.ID)
	if err != nil {
		return views.ProjectEnvVM{}, err
	}
	defName := i18n.T(ctx, "Local · este servidor")
	for _, v := range list {
		if v.IsDefault {
			defName = v.Name
		}
	}
	vm := views.ProjectEnvVM{Action: "/c/" + co.Slug + "/projetos/" + url.PathEscape(p.Slug) + "/ambiente", SettingsHref: envBase(co),
		Current: defName, Source: i18n.T(ctx, "(padrão da empresa)")}
	vm.Options = []views.Option{{Value: "default", Label: i18n.Tf(ctx, "Padrão da empresa (%s)", defName), Selected: p.EnvironmentID == nil}}
	for _, v := range list {
		mine := p.EnvironmentID != nil && *p.EnvironmentID == v.ID
		if mine {
			vm.Current, vm.Source = v.Name, "("+string(v.Driver)+")"
			if v.Status == domain.EnvArchived {
				vm.Source = i18n.T(ctx, "(arquivado: as execuções usam o padrão da empresa)")
			}
		}
		if v.Status == domain.EnvActive {
			vm.Options = append(vm.Options, views.Option{Value: v.Slug, Label: v.Name + " (" + string(v.Driver) + ")", Selected: mine})
		}
	}
	return vm, nil
}

func (s *server) handleProjectEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	pv, err := s.be.GetProjectView(ctx, co.ID, r.PathValue("project"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.projectEnvVM(ctx, co, pv.Project)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.ProjectEnvironmentCard(vm))
}

func (s *server) handleSetProjectEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	pv, err := s.be.GetProjectView(ctx, co.ID, r.PathValue("project"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var id *domain.ID
	if ref := trimForm(r, "environment"); ref != "" && ref != "default" && ref != "local" {
		v, err := s.be.GetEnvironment(ctx, co.ID, ref)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		id = &v.ID
	}
	np, err := s.be.SetProjectEnvironment(ctx, actorFrom(ctx), co.ID, pv.ID, id)
	if err != nil {
		if !isHX(r) || errors.Is(err, domain.ErrNotAllowed) {
			s.fail(w, r, &co, err)
			return
		}
		vm, verr := s.projectEnvVM(ctx, co, pv.Project)
		if verr != nil {
			s.fail(w, r, &co, verr)
			return
		}
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
		s.render(w, r, http.StatusOK, views.ProjectEnvironmentCard(vm))
		return
	}
	if !isHX(r) {
		redirect(w, r, "/c/"+co.Slug+"/projetos/"+url.PathEscape(pv.Slug)+"?secao=configuracao#ambiente")
		return
	}
	vm, err := s.projectEnvVM(ctx, co, np)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Notice = i18n.T(ctx, "Ambiente salvo: vale a partir da próxima execução.")
	s.render(w, r, http.StatusOK, views.ProjectEnvironmentCard(vm))
}
