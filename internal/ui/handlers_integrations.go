package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

type integrationsBackend interface {
	CreateIntegration(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateIntegrationInput) (service.IntegrationView, error)
	UpdateIntegration(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.UpdateIntegrationInput) (service.IntegrationView, error)
	DeleteIntegration(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error
	ListIntegrations(ctx context.Context, companyID domain.ID) ([]service.IntegrationView, error)
	GetIntegration(ctx context.Context, companyID domain.ID, ref string) (service.IntegrationView, error)
	ListIntegrationPersonas(ctx context.Context, companyID, id domain.ID) ([]domain.IntegrationPersona, error)
	ListPersonaIntegrations(ctx context.Context, companyID, personaID domain.ID) ([]service.PersonaIntegration, error)
	SetPersonaIntegration(ctx context.Context, actor domain.Actor, companyID, personaID, integrationID domain.ID, on bool) error
	ListWebhookDeliveries(ctx context.Context, companyID, integrationID domain.ID, limit int) ([]domain.WebhookDelivery, error)
	TestIntegration(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (service.IntegrationTest, error)
}

func (s *server) integrationsBE() (integrationsBackend, bool) {
	b, ok := s.be.(integrationsBackend)
	return b, ok
}

var integrationNotices = map[string]string{
	"criada":    i18n.N("Integração criada."),
	"salva":     i18n.N("Integração atualizada."),
	"ligada":    i18n.N("Integração ligada."),
	"desligada": i18n.N("Integração desligada: não entra nas execuções nem recebe eventos até ser ligada de novo."),
	"apagada":   i18n.N("Integração apagada."),
	"persona":   i18n.N("Personas atualizadas."),
}

// labelFor traduz o nome de um evento de webhook.
func eventLabel(ctx context.Context, ev string) string {
	switch ev {
	case domain.EventTaskCreated:
		return i18n.T(ctx, "Task criada")
	case domain.EventTaskCompleted:
		return i18n.T(ctx, "Task concluída")
	case domain.EventGateOpened:
		return i18n.T(ctx, "Gate aberto")
	case domain.EventGateDecided:
		return i18n.T(ctx, "Gate decidido")
	case domain.EventRunFailed:
		return i18n.T(ctx, "Execução com falha")
	case domain.EventAlertOpened:
		return i18n.T(ctx, "Alerta aberto")
	}
	return ev
}

func deliveryBadge(ctx context.Context, status string) views.Badge {
	switch status {
	case domain.DeliverySuccess:
		return views.Badge{Label: i18n.T(ctx, "Entregue"), Class: "done"}
	case domain.DeliveryRetrying:
		return views.Badge{Label: i18n.T(ctx, "Tentando de novo"), Class: "warn"}
	case domain.DeliveryFailed:
		return views.Badge{Label: i18n.T(ctx, "Falhou"), Class: "danger"}
	}
	return views.Badge{Label: i18n.T(ctx, "Na fila"), Class: "outline"}
}

func integrationTarget(i service.IntegrationView) string {
	if i.Config.Transport == domain.TransportStdio {
		return strings.TrimSpace(i.Config.Command + " " + strings.Join(i.Config.Args, " "))
	}
	return i.Config.URL
}

func (s *server) integrationRow(ctx context.Context, base string, i service.IntegrationView) views.IntegrationRow {
	href := base + "/integracoes/" + url.PathEscape(i.Slug)
	row := views.IntegrationRow{Name: i.Name, Slug: i.Slug, Href: href, Target: integrationTarget(i), Enabled: i.Enabled, ToggleHref: href + "/ativar"}
	if i.Kind == domain.IntegrationWebhook {
		labels := make([]string, 0, len(i.Events))
		for _, e := range i.Events {
			labels = append(labels, eventLabel(ctx, e))
		}
		row.Scope = strings.Join(labels, ", ")
		if d := i.LastDelivery; d != nil {
			row.Last, row.LastBadge = d.Status, deliveryBadge(ctx, d.Status)
		}
	} else if i.AllPersonas {
		row.Scope = i18n.T(ctx, "todas as personas")
	} else {
		row.Scope = i18n.T(ctx, "personas ligadas")
	}
	return row
}

func (s *server) handleIntegrations(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.integrationsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	list, err := ib.ListIntegrations(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	vm := views.IntegrationsVM{NewMCPHref: base + "/integracoes/nova?tipo=mcp", NewWebhookHref: base + "/integracoes/nova?tipo=webhook",
		Notice:   i18n.T(ctx, integrationNotices[r.URL.Query().Get("ok")]),
		Subtitle: i18n.T(ctx, "Serviços externos conectados à empresa: servidores MCP para as execuções e webhooks de saída para os seus sistemas.")}
	for _, i := range list {
		row := s.integrationRow(ctx, base, i)
		if i.Kind == domain.IntegrationWebhook {
			vm.Webhooks = append(vm.Webhooks, row)
		} else {
			vm.MCP = append(vm.MCP, row)
		}
	}
	sh := s.shell(r, co, "integracoes", i18n.T(ctx, "Integrações"), views.Crumb{Label: i18n.T(ctx, "Integrações")})
	s.render(w, r, http.StatusOK, views.IntegrationsPage(sh, vm))
}

func (s *server) blankIntegrationForm(ctx context.Context, co domain.Company, kind string) views.IntegrationFormVM {
	base := "/c/" + co.Slug
	vm := views.IntegrationFormVM{Kind: kind, Action: base + "/integracoes/nova", CancelHref: base + "/integracoes", Enabled: true, FieldErr: map[string]string{},
		Transport: domain.TransportHTTP}
	vm.TransportOptions = []views.Option{{Value: domain.TransportHTTP, Label: "http", Selected: true}, {Value: domain.TransportStdio, Label: "stdio"}}
	vm.EventOptions = eventOptions(ctx, nil)
	vm.AllPersonas = kind == string(domain.IntegrationMCP)
	return vm
}

func eventOptions(ctx context.Context, checked []string) []views.EventOption {
	out := make([]views.EventOption, 0, len(domain.WebhookEvents))
	for _, e := range domain.WebhookEvents {
		on := false
		for _, c := range checked {
			on = on || c == e
		}
		out = append(out, views.EventOption{Value: e, Label: eventLabel(ctx, e), Checked: on})
	}
	return out
}

func kindFrom(v string) string {
	if v == string(domain.IntegrationWebhook) {
		return v
	}
	return string(domain.IntegrationMCP)
}

func (s *server) renderNewIntegration(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.IntegrationFormVM) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "integracoes", i18n.T(ctx, "Nova integração"), views.Crumb{Label: i18n.T(ctx, "Integrações"), Href: base + "/integracoes"}, views.Crumb{Label: i18n.T(ctx, "Nova")})
	s.render(w, r, status, views.NewIntegrationPage(sh, vm))
}

func (s *server) handleNewIntegration(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if _, ok := s.integrationsBE(); !ok {
		s.unavailable(w, r)
		return
	}
	s.renderNewIntegration(w, r, co, http.StatusOK, s.blankIntegrationForm(r.Context(), co, kindFrom(r.URL.Query().Get("tipo"))))
}

// integrationFormError põe o erro do service no campo do formulário.
func integrationFormError(ctx context.Context, vm *views.IntegrationFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		field := ve.Field
		switch field {
		case "command", "args":
			field = "command"
		case "secrets":
			field = "secret"
		}
		msg := map[string]string{
			"name":    i18n.T(ctx, "Dê um nome à integração."),
			"url":     i18n.T(ctx, "Endereço inválido: webhook exige https e destinos internos são bloqueados; MCP aceita http ou https."),
			"command": i18n.T(ctx, "Informe o comando; stdio só funciona se o operador o liberou no servidor."),
			"events":  i18n.T(ctx, "Escolha ao menos um evento."),
			"secret":  i18n.T(ctx, "Segredo inválido: use ao menos 16 caracteres, e confirme que o servidor tem HANKO_SECRET_KEY."),
			"headers": i18n.T(ctx, "Header inválido: use Nome: valor, sem quebras de linha."),
			"env":     i18n.T(ctx, "Variável inválida: use NOME=valor."),
			"slug":    i18n.T(ctx, "Identificador reservado ou inválido: troque o nome."),
		}[field]
		if msg == "" {
			msg = ve.Field + ": " + ve.Reason + "."
		}
		vm.FieldErr[field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

// lines separa um texto em linhas não vazias.
func lines(v string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(v, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// pairs lê "Nome<sep>valor" por linha. Linha sem o separador é erro (o formulário mostra o campo).
func pairs(v, sep string) (map[string]string, error) {
	ls := lines(v)
	if len(ls) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(ls))
	for _, l := range ls {
		k, val, ok := strings.Cut(l, sep)
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("linha sem %q: %q", sep, l)
		}
		out[k] = strings.TrimSpace(val)
	}
	return out, nil
}

func csv(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// fillIntegrationForm devolve ao formulário o que foi enviado (menos os segredos, que nunca voltam).
func fillIntegrationForm(r *http.Request, vm *views.IntegrationFormVM) {
	vm.Name, vm.URL = trimForm(r, "name"), trimForm(r, "url")
	vm.Command, vm.ArgsText = trimForm(r, "command"), r.PostFormValue("args")
	vm.DropHeaders, vm.DropEnv = trimForm(r, "drop_headers"), trimForm(r, "drop_env")
	vm.AllPersonas = r.PostFormValue("all_personas") != ""
	vm.Enabled = r.PostFormValue("enabled") != ""
	vm.RotateSecret = r.PostFormValue("rotate_secret") != ""
	if t := trimForm(r, "transport"); t != "" {
		vm.Transport = t
		for i := range vm.TransportOptions {
			vm.TransportOptions[i].Selected = vm.TransportOptions[i].Value == t
		}
	}
	vm.EventOptions = eventOptions(r.Context(), r.PostForm["events"])
}

func (s *server) handleCreateIntegration(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.integrationsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	kind := kindFrom(r.URL.Query().Get("tipo"))
	vm := s.blankIntegrationForm(ctx, co, kind)
	vm.Action = "/c/" + co.Slug + "/integracoes/nova?tipo=" + kind
	fillIntegrationForm(r, &vm)
	bad := func(err error) {
		integrationFormError(ctx, &vm, err)
		s.renderNewIntegration(w, r, co, statusFor(err), vm)
	}
	headers, herr := pairs(r.PostFormValue("headers"), ":")
	env, eerr := pairs(r.PostFormValue("env"), "=")
	if herr != nil {
		bad(domain.Invalid("headers", herr.Error()))
		return
	}
	if eerr != nil {
		bad(domain.Invalid("env", eerr.Error()))
		return
	}
	enabled := vm.Enabled
	in := service.CreateIntegrationInput{Kind: domain.IntegrationKind(kind), Name: vm.Name, URL: vm.URL, Enabled: &enabled}
	if kind == string(domain.IntegrationWebhook) {
		in.Events, in.Secret = r.PostForm["events"], trimForm(r, "secret")
	} else {
		in.Transport, in.Command, in.Args, in.Headers, in.Env, in.AllPersonas = vm.Transport, vm.Command, lines(vm.ArgsText), headers, env, vm.AllPersonas
	}
	v, err := ib.CreateIntegration(ctx, actorFrom(ctx), co.ID, in)
	if err != nil {
		bad(err)
		return
	}
	// O segredo gerado só existe nesta resposta: mostra a página da integração aqui mesmo, sem redirecionar.
	if v.RevealedSecret != "" {
		s.renderIntegration(w, r, co, ib, v, http.StatusCreated, v.RevealedSecret)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/integracoes/"+url.PathEscape(v.Slug)+"?ok=criada")
}

func (s *server) integrationVM(r *http.Request, co domain.Company, ib integrationsBackend, v service.IntegrationView) (views.IntegrationVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	href := base + "/integracoes/" + url.PathEscape(v.Slug)
	loc := loadLoc(co.Timezone)
	now := s.now()
	vm := views.IntegrationVM{Name: v.Name, Kind: string(v.Kind), Target: integrationTarget(v), Enabled: v.Enabled,
		ToggleHref: href + "/ativar", TestHref: href + "/testar", DeleteHref: href + "/remover",
		Notice: i18n.T(ctx, integrationNotices[r.URL.Query().Get("ok")])}
	if v.Enabled {
		vm.Badge = views.Badge{Label: i18n.T(ctx, "Ligada"), Class: "run"}
	} else {
		vm.Badge = views.Badge{Label: i18n.T(ctx, "Desligada"), Class: "soft"}
	}
	f := views.IntegrationFormVM{Kind: string(v.Kind), Edit: true, Action: href, CancelHref: base + "/integracoes", Name: v.Name, URL: v.Config.URL,
		Enabled: v.Enabled, FieldErr: map[string]string{}, Transport: v.Config.Transport, Command: v.Config.Command,
		ArgsText: strings.Join(v.Config.Args, "\n"), HeaderNames: v.Config.HeaderNames, EnvNames: v.Config.EnvNames, AllPersonas: v.AllPersonas,
		SecretSet: v.SecretSet, SecretHint: v.SecretHint, EventOptions: eventOptions(ctx, v.Events)}
	vm.Form = f
	if v.Kind == domain.IntegrationWebhook {
		vm.KindLabel = i18n.T(ctx, "Webhook de saída")
		ds, err := ib.ListWebhookDeliveries(ctx, co.ID, v.ID, 20)
		if err != nil {
			return vm, err
		}
		for _, d := range ds {
			row := views.DeliveryRow{When: agoCtx(ctx, d.CreatedAt, now, loc), Event: d.Event, Test: d.Test, Badge: deliveryBadge(ctx, d.Status),
				Attempts: d.Attempts, Error: d.Error}
			if d.HTTPStatus != nil {
				row.HTTP = fmt.Sprint(*d.HTTPStatus)
			}
			if d.LatencyMs != nil {
				row.Latency = fmt.Sprintf("%d ms", *d.LatencyMs)
			}
			if d.NextAttemptAt != nil {
				row.Next = fmtWhen(ctx, *d.NextAttemptAt, now, loc)
			}
			vm.Deliveries = append(vm.Deliveries, row)
		}
		return vm, nil
	}
	vm.KindLabel = i18n.T(ctx, "Servidor MCP")
	if v.AllPersonas {
		vm.Scope = i18n.T(ctx, "todas as personas")
	}
	ps, err := ib.ListIntegrationPersonas(ctx, co.ID, v.ID)
	if err != nil {
		return vm, err
	}
	for _, p := range ps {
		vm.Personas = append(vm.Personas, views.ToggleRow{Name: p.Name, Slug: p.Slug, Href: base + "/personas/" + url.PathEscape(p.Slug), Enabled: p.Enabled,
			Fixed: v.AllPersonas, ToggleHref: href + "/personas/" + url.PathEscape(p.Slug)})
	}
	return vm, nil
}

func (s *server) renderIntegration(w http.ResponseWriter, r *http.Request, co domain.Company, ib integrationsBackend, v service.IntegrationView, status int, revealed string) {
	vm, err := s.integrationVM(r, co, ib, v)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.RevealedSecret = revealed
	s.renderIntegrationVM(w, r, co, status, vm)
}

func (s *server) renderIntegrationVM(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.IntegrationVM) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "integracoes", vm.Name, views.Crumb{Label: i18n.T(ctx, "Integrações"), Href: base + "/integracoes"}, views.Crumb{Label: vm.Name})
	s.render(w, r, status, views.IntegrationPage(sh, vm))
}

func (s *server) handleIntegration(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.integrationsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	v, err := ib.GetIntegration(r.Context(), co.ID, r.PathValue("integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderIntegration(w, r, co, ib, v, http.StatusOK, "")
}

func (s *server) handleUpdateIntegration(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.integrationsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	cur, err := ib.GetIntegration(ctx, co.ID, r.PathValue("integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	bad := func(err error) {
		vm, verr := s.integrationVM(r, co, ib, cur)
		if verr != nil {
			s.fail(w, r, &co, verr)
			return
		}
		fillIntegrationForm(r, &vm.Form)
		vm.Form.Edit = true
		integrationFormError(ctx, &vm.Form, err)
		s.renderIntegrationVM(w, r, co, statusFor(err), vm)
	}
	headers, herr := pairs(r.PostFormValue("headers"), ":")
	env, eerr := pairs(r.PostFormValue("env"), "=")
	if herr != nil {
		bad(domain.Invalid("headers", herr.Error()))
		return
	}
	if eerr != nil {
		bad(domain.Invalid("env", eerr.Error()))
		return
	}
	name, u, enabled := trimForm(r, "name"), trimForm(r, "url"), r.PostFormValue("enabled") != ""
	up := service.UpdateIntegrationInput{Name: &name, URL: &u, Enabled: &enabled}
	if cur.Kind == domain.IntegrationWebhook {
		ev := r.PostForm["events"]
		up.Events = &ev
		if sec := trimForm(r, "secret"); sec != "" {
			up.Secret = &sec
		}
		up.RotateSecret = r.PostFormValue("rotate_secret") != "" && up.Secret == nil
	} else {
		cmd, args, all := trimForm(r, "command"), lines(r.PostFormValue("args")), r.PostFormValue("all_personas") != ""
		up.AllPersonas, up.SetHeaders, up.SetEnv = &all, headers, env
		up.DropHeaders, up.DropEnv = csv(r.PostFormValue("drop_headers")), csv(r.PostFormValue("drop_env"))
		if cur.Config.Transport == domain.TransportStdio {
			up.Command, up.Args = &cmd, &args
			up.URL = nil
		}
	}
	v, err := ib.UpdateIntegration(ctx, actorFrom(ctx), co.ID, cur.ID, up)
	if err != nil {
		bad(err)
		return
	}
	if v.RevealedSecret != "" { // rotação: o valor novo só existe nesta resposta
		s.renderIntegration(w, r, co, ib, v, http.StatusOK, v.RevealedSecret)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/integracoes/"+url.PathEscape(v.Slug)+"?ok=salva")
}

func (s *server) handleIntegrationEnabled(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.integrationsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	cur, err := ib.GetIntegration(ctx, co.ID, r.PathValue("integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	on := enabledForm(r)
	if cur.Enabled != on {
		if _, err := ib.UpdateIntegration(ctx, actorFrom(ctx), co.ID, cur.ID, service.UpdateIntegrationInput{Enabled: &on}); err != nil {
			s.fail(w, r, &co, err)
			return
		}
	}
	ok2 := "desligada"
	if on {
		ok2 = "ligada"
	}
	to := "/c/" + co.Slug + "/integracoes?ok=" + ok2
	if ref := r.Header.Get("HX-Current-URL"); strings.Contains(ref, "/integracoes/") {
		to = "/c/" + co.Slug + "/integracoes/" + url.PathEscape(cur.Slug) + "?ok=" + ok2
	}
	redirect(w, r, to)
}

func (s *server) handleTestIntegration(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.integrationsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	cur, err := ib.GetIntegration(ctx, co.ID, r.PathValue("integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	res, err := ib.TestIntegration(ctx, actorFrom(ctx), co.ID, cur.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := views.IntegrationTestVM{OK: res.OK, Error: res.Error, Note: res.Note}
	if res.Status > 0 {
		vm.Status = fmt.Sprint(res.Status)
	}
	if res.LatencyMs > 0 {
		vm.Latency = fmt.Sprintf("%d ms", res.LatencyMs)
	}
	s.render(w, r, http.StatusOK, views.IntegrationTestResult(vm))
}

func (s *server) handleDeleteIntegration(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ib, ok := s.integrationsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	cur, err := ib.GetIntegration(ctx, co.ID, r.PathValue("integration"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := ib.DeleteIntegration(ctx, actorFrom(ctx), co.ID, cur.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/integracoes?ok=apagada")
}

// handleIntegrationPersona liga ou desliga um servidor MCP para uma persona; fromPersona diz de qual página veio.
func (s *server) handleIntegrationPersona(fromPersona bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		ib, ok := s.integrationsBE()
		if !ok {
			s.unavailable(w, r)
			return
		}
		ctx := r.Context()
		p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		cur, err := ib.GetIntegration(ctx, co.ID, r.PathValue("integration"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		if err := ib.SetPersonaIntegration(ctx, actorFrom(ctx), co.ID, p.ID, cur.ID, enabledForm(r)); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		base := "/c/" + co.Slug
		if fromPersona {
			redirect(w, r, base+"/personas/"+url.PathEscape(p.Slug)+"#mcp")
			return
		}
		redirect(w, r, base+"/integracoes/"+url.PathEscape(cur.Slug)+"#personas")
	}
}
