package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Assistente Conectar (ADR-0089) numa tela: os cinco passos sobre o service. A tela só monta o que cada passo pede;
// quem grava é o service, numa transação, com activity_event (AGENTS.md). Nada vem marcado por padrão: personas e
// modelo só entram quando a pessoa escolhe, na URL do passo 5.

// connectBackend é o subconjunto do service do assistente; a UI assume o que o service já implementa.
type connectBackend interface {
	ConnectBegin(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.ConnectBeginInput) (service.ConnectView, error)
	ConnectState(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectView, error)
	ConnectDiscover(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectView, error)
	ConnectDiscard(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) error
	ConnectPreview(ctx context.Context, actor domain.Actor, companyID domain.ID, ref, templateRef string, personaIDs []domain.ID) (service.ConnectPreviewView, error)
	ConnectComplete(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in service.ConnectCompleteInput) (service.ConnectResultView, error)
	ListAccessTemplates(ctx context.Context, companyID domain.ID) ([]domain.AccessTemplate, error)
}

var connectNotices = map[string]string{
	"descartado": i18n.N("Rascunho descartado: a integração criada pelo assistente foi apagada."),
}

func (s *server) connectBE() (connectBackend, bool) {
	b, ok := s.be.(connectBackend)
	return b, ok
}

func (s *server) connectRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	const connectBase = "/c/{company}/conectar"
	mux.HandleFunc("GET "+connectBase, co(s.handleConnectNew))
	mux.HandleFunc("POST "+connectBase, post(co(s.handleConnectBegin)))
	mux.HandleFunc("GET "+connectBase+"/{integration}", co(s.handleConnectState))
	mux.HandleFunc("POST "+connectBase+"/{integration}/descobrir", post(co(s.handleConnectDiscover)))
	mux.HandleFunc("POST "+connectBase+"/{integration}/descartar", post(co(s.handleConnectDiscard)))
	mux.HandleFunc("POST "+connectBase+"/{integration}/concluir", post(co(s.handleConnectComplete)))
}

func connectBaseOf(co domain.Company) string { return "/c/" + co.Slug + "/conectar" }

// connectServerVM é o passo 1 em branco (ou com a notice da volta do descarte).
func connectServerVM(ctx context.Context, co domain.Company, r *http.Request) views.ConnectVM {
	base := connectBaseOf(co)
	return views.ConnectVM{Action: base, CancelHref: "/c/" + co.Slug + "/integracoes",
		Notice:         i18n.T(ctx, connectNotices[r.URL.Query().Get("ok")]),
		FieldErr:       map[string]string{},
		NewProfileHref: "/c/" + co.Slug + "/ferramentas/perfis/novo"}
}

// fillConnectForm devolve ao passo 1 o que foi enviado. O headers (segredo) nunca volta.
func fillConnectForm(r *http.Request, vm *views.ConnectVM) {
	vm.Name, vm.URL = trimForm(r, "name"), trimForm(r, "url")
	vm.OAuth = r.PostFormValue("auth") == "oauth"
	vm.Scopes, vm.ClientID = trimForm(r, "scopes"), trimForm(r, "client_id")
}

// connectFormError põe o erro do service no passo 1 (campo, quando há).
func connectFormError(ctx context.Context, vm *views.ConnectVM, err error) {
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
		return
	}
	msg := ve.Reason
	if ve.Field == "headers" {
		msg = i18n.T(ctx, "Header inválido: use Nome: valor, sem quebras de linha.")
	}
	vm.FieldErr[ve.Field] = msg
	vm.Error = msg
}

func (s *server) renderConnect(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.ConnectVM) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "integracoes", i18n.T(ctx, "Conectar ferramenta"),
		views.Crumb{Label: i18n.T(ctx, "Integrações"), Href: base + "/integracoes"}, views.Crumb{Label: i18n.T(ctx, "Conectar")})
	s.render(w, r, status, views.ConnectPage(sh, vm))
}

func (s *server) handleConnectNew(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if _, ok := s.connectBE(); !ok {
		s.unavailable(w, r)
		return
	}
	s.renderConnect(w, r, co, http.StatusOK, connectServerVM(r.Context(), co, r))
}

// handleConnectBegin é o passo 1 e o 2 (a descoberta roda no begin). Falha de tools/list não é erro aqui: fica no
// catálogo e a tela do passo 2 oferece tentar de novo ou descartar (ADR-0089, item 11).
func (s *server) handleConnectBegin(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.connectBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	vm := connectServerVM(ctx, co, r)
	fillConnectForm(r, &vm)
	bad := func(err error) {
		connectFormError(ctx, &vm, err)
		s.renderConnect(w, r, co, statusFor(err), vm)
	}
	headers, err := pairs(r.PostFormValue("headers"), ":")
	if err != nil {
		bad(domain.Invalid("headers", err.Error()))
		return
	}
	in := service.ConnectBeginInput{Name: vm.Name, URL: vm.URL, Headers: headers, OAuth: vm.OAuth,
		Scopes: csv(vm.Scopes), ClientID: vm.ClientID}
	v, err := ib.ConnectBegin(ctx, actorFrom(ctx), co.ID, in)
	if err != nil {
		bad(err)
		return
	}
	redirect(w, r, connectBaseOf(co)+"/"+url.PathEscape(v.Integration.Slug))
}

func (s *server) handleConnectState(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.connectBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	ref := r.PathValue("integration")
	v, err := ib.ConnectState(ctx, actorFrom(ctx), co.ID, ref)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.connectStateVM(ctx, ib, co, r, v)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderConnect(w, r, co, http.StatusOK, vm)
}

// connectStateVM monta os passos 2 a 5. Personas e modelo vêm da URL (GET); o passo 5 só roda com os dois.
// Erro de conferência (modelo ou persona inválidos) vai para a tela, não para uma página de erro.
func (s *server) connectStateVM(ctx context.Context, ib connectBackend, co domain.Company, r *http.Request, v service.ConnectView) (views.ConnectVM, error) {
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		return views.ConnectVM{}, err
	}
	templates, err := ib.ListAccessTemplates(ctx, co.ID)
	if err != nil {
		return views.ConnectVM{}, err
	}
	q := r.URL.Query()
	vm := connectStateBase(ctx, co, v)
	vm.Personas = connectPersonaOpts(personas, q["personas"])
	vm.Templates = connectTemplateOpts(ctx, templates, q.Get("template"))
	vm.ChosenTemplate = q.Get("template")
	if !vm.CanComplete || len(q["personas"]) == 0 || vm.ChosenTemplate == "" {
		return vm, nil
	}
	ids, err := connectChosenPersonas(q["personas"], personas)
	if err != nil {
		vm.Error = connectErrText(ctx, err)
		return vm, nil
	}
	pv, err := ib.ConnectPreview(ctx, actorFrom(ctx), co.ID, v.Integration.Slug, vm.ChosenTemplate, ids)
	if err != nil {
		vm.Error = connectErrText(ctx, err)
		return vm, nil
	}
	vm.Preview = connectPreviewVM(ctx, pv)
	return vm, nil
}

// connectStateBase preenche o que é do estado da integração (não depende da URL).
func connectStateBase(ctx context.Context, co domain.Company, v service.ConnectView) views.ConnectVM {
	slug := v.Integration.Slug
	base := connectBaseOf(co) + "/" + url.PathEscape(slug)
	vm := views.ConnectVM{Slug: slug, Base: base, PersonasURL: base, FieldErr: map[string]string{},
		IntegrationHref: "/c/" + co.Slug + "/integracoes/" + url.PathEscape(slug),
		DiscoverHref:    base + "/descobrir", DiscardHref: base + "/descartar", CompleteHref: base + "/concluir",
		NewProfileHref: "/c/" + co.Slug + "/ferramentas/perfis/novo",
		CatalogErr:     v.CatalogErr, CanComplete: v.CanComplete, PersonasLinked: v.PersonasLinked}
	if v.AuthURL != "" && v.Connection != nil {
		vm.ConsentHref = "/c/" + co.Slug + "/ferramentas/conexoes/" + url.PathEscape(v.Connection.Slug) + "/autorizar"
	}
	for _, t := range v.Tools {
		if t.Quarantined {
			vm.Quarantined++
		}
		vm.Tools = append(vm.Tools, views.ConnectToolRow{Name: t.Name, Title: t.Title, Risk: riskBadge(ctx, t.Risk), Quarantined: t.Quarantined})
	}
	return vm
}

// connectPersonaOpts lista as personas da empresa; marca só as que a URL trouxe (nada vem marcado sozinho).
func connectPersonaOpts(personas []domain.Persona, chosen []string) []views.ConnectPersonaOpt {
	on := map[string]bool{}
	for _, c := range chosen {
		on[c] = true
	}
	out := make([]views.ConnectPersonaOpt, 0, len(personas))
	for _, p := range personas {
		id := p.ID.String()
		out = append(out, views.ConnectPersonaOpt{ID: id, Name: p.Name, Checked: on[id]})
	}
	return out
}

// connectTemplateOpts lista os modelos; o sugerido ganha a etiqueta, e só o escolhido na URL vem marcado.
func connectTemplateOpts(ctx context.Context, list []domain.AccessTemplate, chosen string) []views.ConnectTemplateOpt {
	out := make([]views.ConnectTemplateOpt, 0, len(list))
	for _, t := range list {
		id := t.ID.String()
		out = append(out, views.ConnectTemplateOpt{ID: id, Name: t.Name, Description: t.Description, Suggested: t.Suggested,
			Checked: chosen != "" && (chosen == id || chosen == t.Slug),
			Read:    policyModeBadge(ctx, t.ReadMode), Write: policyModeBadge(ctx, t.WriteMode),
			Destructive: policyModeBadge(ctx, t.DestructiveMode)})
	}
	return out
}

// connectChosenPersonas valida as personas da URL contra as da empresa: nunca aceita id de outra empresa.
func connectChosenPersonas(refs []string, personas []domain.Persona) ([]domain.ID, error) {
	byID := make(map[string]domain.ID, len(personas))
	for _, p := range personas {
		byID[p.ID.String()] = p.ID
	}
	out := make([]domain.ID, 0, len(refs))
	for _, ref := range refs {
		id, ok := byID[ref]
		if !ok {
			return nil, domain.Invalid("personas", "persona não encontrada nesta empresa")
		}
		out = append(out, id)
	}
	return out, nil
}

// connectPreviewVM traduz o passo 5 do service para a tela.
func connectPreviewVM(ctx context.Context, pv service.ConnectPreviewView) *views.ConnectPreviewVM {
	out := &views.ConnectPreviewVM{Template: pv.Template.ID.String(), Released: pv.Released}
	for _, p := range pv.Personas {
		row := views.ConnectPreviewPersonaVM{Name: p.Name}
		for _, t := range p.Tools {
			row.Tools = append(row.Tools, views.ConnectPreviewToolVM{Name: t.Name, Risk: riskBadge(ctx, t.Risk),
				Mode: policyModeBadge(ctx, t.Mode), Callable: connectCallableBadge(ctx, t.Effective), Note: t.Note})
		}
		out.Personas = append(out.Personas, row)
	}
	return out
}

func connectCallableBadge(ctx context.Context, effective bool) views.Badge {
	if effective {
		return views.Badge{Label: i18n.T(ctx, "pode chamar"), Class: "done"}
	}
	return views.Badge{Label: i18n.T(ctx, "não pode chamar"), Class: "soft"}
}

// connectErrText dá a mensagem curta de um erro do assistente para a tela.
func connectErrText(ctx context.Context, err error) string {
	_, title, msg := friendly(ctx, err)
	return title + ". " + msg
}

func (s *server) handleConnectDiscover(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.connectBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	ref := r.PathValue("integration")
	if _, err := ib.ConnectDiscover(ctx, actorFrom(ctx), co.ID, ref); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, connectBaseOf(co)+"/"+url.PathEscape(ref))
}

func (s *server) handleConnectDiscard(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.connectBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	if err := ib.ConnectDiscard(ctx, actorFrom(ctx), co.ID, r.PathValue("integration")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, connectBaseOf(co)+"?ok=descartado")
}

// handleConnectComplete é o passo final: perfil, vínculos e liberação numa transação só (service). Erro volta para
// o passo 5 com a mensagem, sem perder o que já foi feito antes.
func (s *server) handleConnectComplete(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.connectBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, &co, domain.Invalid("form", "formulário inválido"))
		return
	}
	ctx := r.Context()
	ref := r.PathValue("integration")
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	ids, err := connectChosenPersonas(r.PostForm["personas"], personas)
	if err != nil {
		s.connectStateError(w, r, co, ib, ref, err)
		return
	}
	res, err := ib.ConnectComplete(ctx, actorFrom(ctx), co.ID, ref, service.ConnectCompleteInput{
		Template: trimForm(r, "template"), PersonaIDs: ids})
	if err != nil {
		s.connectStateError(w, r, co, ib, ref, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/integracoes/"+url.PathEscape(res.Integration.Slug)+"?ok=conectada")
}

// connectStateError mostra o erro de uma escrita do passo 5 na tela do estado da integração.
func (s *server) connectStateError(w http.ResponseWriter, r *http.Request, co domain.Company, ib connectBackend, ref string, err error) {
	ctx := r.Context()
	v, serr := ib.ConnectState(ctx, actorFrom(ctx), co.ID, ref)
	if serr != nil {
		s.fail(w, r, &co, serr)
		return
	}
	vm, verr := s.connectStateVM(ctx, ib, co, r, v)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	vm.Error = connectErrText(ctx, err)
	s.renderConnect(w, r, co, statusFor(err), vm)
}
