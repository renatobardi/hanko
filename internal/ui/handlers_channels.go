package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Configurações › Canais (ADR-0046): Telegram e GitHub.

type channelsBackend interface {
	ListChannels(ctx context.Context, companyID domain.ID) ([]service.ChannelView, error)
	GetChannel(ctx context.Context, companyID domain.ID, ref string) (service.ChannelView, error)
	CreateChannel(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateChannelInput) (service.ChannelView, error)
	UpdateChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID, in service.UpdateChannelInput) (service.ChannelView, error)
	DeleteChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) error
	ConnectChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) (service.ChannelConnection, error)
	CreateChannelLink(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) (service.ChannelLink, error)
	MyChannelIdentity(ctx context.Context, companyID, channelID domain.ID) (*domain.ChannelIdentity, error)
	ListChannelIdentities(ctx context.Context, companyID, channelID domain.ID) ([]domain.ChannelIdentity, error)
	RevokeChannelIdentity(ctx context.Context, actor domain.Actor, companyID, channelID, identityID domain.ID) error
	ListChannelMessages(ctx context.Context, companyID, channelID domain.ID, limit int) ([]domain.ChannelMessage, error)
	ResendChannelMessage(ctx context.Context, actor domain.Actor, companyID, messageID domain.ID) (domain.ChannelMessage, error)
}

func (s *server) channelsBE() (channelsBackend, bool) {
	b, ok := s.be.(channelsBackend)
	return b, ok
}

var channelNotices = map[string]string{
	"criado":        i18n.N("Canal criado. Ligue-o depois de conectar ao provedor."),
	"salvo":         i18n.N("Canal atualizado."),
	"ligado":        i18n.N("Canal ligado."),
	"desligado":     i18n.N("Canal desligado."),
	"apagado":       i18n.N("Canal apagado."),
	"revogado":      i18n.N("Vínculo revogado."),
	"reenfileirado": i18n.N("Mensagem de volta à fila."),
}

func channelKindLabel(k domain.ChannelKind) string { return k.Label() }

func (s *server) channelSecrets(ctx context.Context, co domain.Company) []string {
	var out []string
	if sb, ok := s.secretsBE(); ok {
		if list, err := sb.ListSecrets(ctx, co.ID, domain.SecretScopeCompany, false); err == nil {
			for _, sec := range list {
				out = append(out, "${secret:"+sec.Name+"}")
			}
		}
	}
	return out
}

func (s *server) channelForm(r *http.Request, co domain.Company, action string, edit bool) views.ChannelFormVM {
	ctx := r.Context()
	personas, _ := s.be.ListPersonas(ctx, co.ID)
	return views.ChannelFormVM{Action: action, Edit: edit, Kind: "telegram", FieldErr: map[string]string{}, Secrets: s.channelSecrets(ctx, co),
		KindOptions: []views.Option{{Value: "telegram", Label: "Telegram", Selected: true}, {Value: "github", Label: "GitHub"}},
		PersonaOpts: personaOptions(personas, nil, i18n.T(ctx, "Nenhuma"))}
}

func (s *server) handleChannels(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.channelsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	cs, err := be.ListChannels(r.Context(), co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderChannels(w, r, co, http.StatusOK, s.channelsVM(r, co, cs), "")
}

func (s *server) channelsVM(r *http.Request, co domain.Company, cs []service.ChannelView) views.ChannelsVM {
	ctx := r.Context()
	base := "/c/" + co.Slug + "/configuracoes/canais"
	vm := views.ChannelsVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "canais"), Notice: i18n.T(ctx, channelNotices[r.URL.Query().Get("ok")]),
		NewForm: s.channelForm(r, co, base, false)}
	for _, c := range cs {
		vm.Rows = append(vm.Rows, views.ChannelRow{Name: c.Name, Kind: string(c.Kind), KindLabel: channelKindLabel(c.Kind), Href: base + "/" + url.PathEscape(c.ID.String()),
			Enabled: c.Enabled, Persona: c.PersonaName, Linked: c.Linked, Pending: c.Pending, Failed: c.FailedOut})
	}
	return vm
}

func (s *server) renderChannels(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.ChannelsVM, errMsg string) {
	vm.Error = errMsg
	sh := s.settingsShell(r, co, i18n.T(r.Context(), "Canais"))
	s.render(w, r, status, views.ChannelsPage(sh, vm))
}

// channelFormValues lê o formulário (criar ou editar) e devolve o que o service aceita.
func channelFormValues(r *http.Request, vm *views.ChannelFormVM) (domain.ChannelKind, domain.ChannelConfig) {
	kind := domain.ChannelKind(trimForm(r, "kind"))
	vm.Name = trimForm(r, "name")
	vm.TokenRef, vm.SecretRef = trimForm(r, "token_ref"), trimForm(r, "webhook_secret_ref")
	vm.Repos, vm.Mention, vm.APIBase = r.PostFormValue("repos"), trimForm(r, "mention"), trimForm(r, "api_base")
	if kind != "" {
		vm.Kind = string(kind)
		for i := range vm.KindOptions {
			vm.KindOptions[i].Selected = vm.KindOptions[i].Value == string(kind)
		}
	}
	selectOption(vm.PersonaOpts, trimForm(r, "persona"))
	var repos []string
	for _, line := range strings.Split(vm.Repos, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			repos = append(repos, l)
		}
	}
	return kind, domain.ChannelConfig{APIBase: vm.APIBase, Repos: repos, Mention: vm.Mention}
}

func channelFormError(ctx context.Context, vm *views.ChannelFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		msg := ve.Field + ": " + ve.Reason + "."
		vm.FieldErr[ve.Field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func (s *server) handleCreateChannel(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.channelsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	base := "/c/" + co.Slug + "/configuracoes/canais"
	form := s.channelForm(r, co, base, false)
	kind, cfg := channelFormValues(r, &form)
	persona, okp := parseID(trimForm(r, "persona"))
	bad := func(err error) {
		channelFormError(ctx, &form, err)
		cs, _ := be.ListChannels(ctx, co.ID)
		vm := s.channelsVM(r, co, cs)
		vm.Notice, vm.NewForm = "", form
		s.renderChannels(w, r, co, statusFor(err), vm, "")
	}
	if !okp {
		bad(domain.Invalid("persona_id", i18n.T(ctx, "inválida")))
		return
	}
	ch, err := be.CreateChannel(ctx, actorFrom(ctx), co.ID, service.CreateChannelInput{Kind: kind, Name: form.Name, PersonaID: persona,
		TokenRef: form.TokenRef, WebhookSecretRef: form.SecretRef, Config: cfg})
	if err != nil {
		bad(err)
		return
	}
	redirect(w, r, base+"/"+url.PathEscape(ch.ID.String())+"?ok=criado")
}

func (s *server) channelByParam(w http.ResponseWriter, r *http.Request, co domain.Company) (channelsBackend, service.ChannelView, bool) {
	be, ok := s.channelsBE()
	if !ok {
		s.unavailable(w, r)
		return nil, service.ChannelView{}, false
	}
	c, err := be.GetChannel(r.Context(), co.ID, r.PathValue("channel"))
	if err != nil {
		s.fail(w, r, &co, err)
		return nil, service.ChannelView{}, false
	}
	return be, c, true
}

func (s *server) channelVM(r *http.Request, co domain.Company, be channelsBackend, c service.ChannelView) (views.ChannelVM, error) {
	ctx := r.Context()
	base := "/c/" + co.Slug + "/configuracoes/canais"
	self := base + "/" + url.PathEscape(c.ID.String())
	loc := loadLoc(co.Timezone)
	vm := views.ChannelVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "canais"), Name: c.Name, Kind: string(c.Kind), KindLabel: channelKindLabel(c.Kind),
		Enabled: c.Enabled, Persona: c.PersonaName, Notice: i18n.T(ctx, channelNotices[r.URL.Query().Get("ok")]), BackHref: base,
		URL: s.publicBase(r) + c.HookPath, ToggleHref: self + "/ligar", ConnectHref: self + "/conectar", CanConnect: c.Kind == domain.ChannelTelegram,
		DeleteHref: self + "/apagar", LinkHref: self + "/vincular"}
	if c.Enabled {
		vm.ToggleHref = self + "/desligar"
	}
	if me, err := be.MyChannelIdentity(ctx, co.ID, c.ID); err == nil && me != nil {
		vm.MyStatus = string(me.Status)
		vm.MyRevoke = self + "/vinculos/" + me.ID.String() + "/revogar"
	}
	form := s.channelForm(r, co, self, true)
	form.Name, form.TokenRef, form.SecretRef = c.Name, c.TokenRef, c.WebhookSecretRef
	form.Repos, form.Mention, form.APIBase = strings.Join(c.Config.Repos, "\n"), c.Config.Mention, c.Config.APIBase
	form.Kind = string(c.Kind)
	if c.PersonaID != nil {
		selectOption(form.PersonaOpts, c.PersonaID.String())
	}
	vm.Form = form
	if !views.RoleFrom(ctx).AtLeast(domain.MemberAdmin) {
		return vm, nil
	}
	ids, err := be.ListChannelIdentities(ctx, co.ID, c.ID)
	if err != nil {
		return vm, err
	}
	for _, i := range ids {
		who := i.UserEmail
		if i.UserName != "" && i.UserName != i.UserEmail {
			who = i.UserName + " · " + i.UserEmail
		}
		row := views.IdentityRow{User: who, External: i.ExternalLabel, Status: identityStatusLabel(ctx, i.Status), Revoke: self + "/vinculos/" + i.ID.String() + "/revogar"}
		if i.LinkedAt != nil {
			row.Since = fmtDateTime(ctx, *i.LinkedAt, loc)
		}
		vm.Identities = append(vm.Identities, row)
	}
	msgs, err := be.ListChannelMessages(ctx, co.ID, c.ID, 30)
	if err != nil {
		return vm, err
	}
	for _, m := range msgs {
		row := views.MessageRow{When: fmtDateTime(ctx, m.CreatedAt, loc), Kind: m.Kind, Body: truncateRunesUI(m.Body, 140), Attempts: m.Attempts,
			Status: channelStatusLabel(ctx, m.Status), StatusCls: channelStatusClass(m.Status), Note: m.LastError}
		if row.Note == "" {
			row.Note = m.StatusReason
		}
		if m.Direction == domain.MessageIn {
			row.DirLabel = i18n.T(ctx, "entrada")
		} else {
			row.DirLabel = i18n.T(ctx, "saída")
			if m.Status == domain.MsgFailed {
				row.Resend = self + "/mensagens/" + m.ID.String() + "/reenviar"
			}
		}
		vm.Messages = append(vm.Messages, row)
	}
	return vm, nil
}

func truncateRunesUI(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func identityStatusLabel(ctx context.Context, st domain.IdentityStatus) string {
	switch st {
	case domain.IdentityLinked:
		return i18n.T(ctx, "vinculado")
	case domain.IdentityPending:
		return i18n.T(ctx, "aguardando código")
	}
	return i18n.T(ctx, "revogado")
}

func channelStatusLabel(ctx context.Context, st domain.ChannelMessageStatus) string {
	switch st {
	case domain.MsgReceived:
		return i18n.T(ctx, "recebida")
	case domain.MsgProcessed:
		return i18n.T(ctx, "processada")
	case domain.MsgIgnored:
		return i18n.T(ctx, "ignorada")
	case domain.MsgPending:
		return i18n.T(ctx, "na fila")
	case domain.MsgSent:
		return i18n.T(ctx, "entregue")
	}
	return i18n.T(ctx, "falhou")
}

func channelStatusClass(st domain.ChannelMessageStatus) string {
	switch st {
	case domain.MsgSent, domain.MsgProcessed:
		return "done"
	case domain.MsgFailed:
		return "danger"
	case domain.MsgPending, domain.MsgReceived:
		return "warn"
	}
	return "soft"
}

func (s *server) renderChannel(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.ChannelVM) {
	sh := s.settingsShell(r, co, vm.Name)
	s.render(w, r, status, views.ChannelPage(sh, vm))
}

func (s *server) handleChannel(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, c, ok := s.channelByParam(w, r, co)
	if !ok {
		return
	}
	vm, err := s.channelVM(r, co, be, c)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderChannel(w, r, co, http.StatusOK, vm)
}

func (s *server) channelSelf(co domain.Company, c service.ChannelView) string {
	return "/c/" + co.Slug + "/configuracoes/canais/" + url.PathEscape(c.ID.String())
}

func (s *server) handleUpdateChannel(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, c, ok := s.channelByParam(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	vm, err := s.channelVM(r, co, be, c)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	_, cfg := channelFormValues(r, &vm.Form)
	vm.Notice = ""
	persona, okp := parseID(trimForm(r, "persona"))
	bad := func(err error) {
		channelFormError(ctx, &vm.Form, err)
		s.renderChannel(w, r, co, statusFor(err), vm)
	}
	if !okp {
		bad(domain.Invalid("persona_id", i18n.T(ctx, "inválida")))
		return
	}
	f := vm.Form
	in := service.UpdateChannelInput{Name: &f.Name, TokenRef: &f.TokenRef, WebhookSecretRef: &f.SecretRef, Config: &cfg}
	if persona == nil {
		in.ClearPersona = true
	} else {
		in.PersonaID = persona
	}
	if _, err := be.UpdateChannel(ctx, actorFrom(ctx), co.ID, c.ID, in); err != nil {
		bad(err)
		return
	}
	redirect(w, r, s.channelSelf(co, c)+"?ok=salvo")
}

func (s *server) handleChannelEnabled(on bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		be, c, ok := s.channelByParam(w, r, co)
		if !ok {
			return
		}
		ctx := r.Context()
		if _, err := be.UpdateChannel(ctx, actorFrom(ctx), co.ID, c.ID, service.UpdateChannelInput{Enabled: &on}); err != nil {
			vm, verr := s.channelVM(r, co, be, c)
			if verr != nil {
				s.fail(w, r, &co, err)
				return
			}
			vm.Notice = ""
			_, title, msg := friendly(ctx, err)
			var ve *domain.ValidationError
			if errors.As(err, &ve) {
				vm.Error = ve.Field + ": " + ve.Reason + "."
			} else {
				vm.Error = title + ". " + msg
			}
			s.renderChannel(w, r, co, statusFor(err), vm)
			return
		}
		ok2 := "desligado"
		if on {
			ok2 = "ligado"
		}
		redirect(w, r, s.channelSelf(co, c)+"?ok="+ok2)
	}
}

func (s *server) handleConnectChannel(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, c, ok := s.channelByParam(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	vm, err := s.channelVM(r, co, be, c)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Notice = ""
	res, err := be.ConnectChannel(ctx, actorFrom(ctx), co.ID, c.ID)
	if err != nil {
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			vm.Error = ve.Field + ": " + ve.Reason + "."
		} else {
			_, title, msg := friendly(ctx, err)
			vm.Error = title + ". " + msg
		}
		s.renderChannel(w, r, co, statusFor(err), vm)
		return
	}
	vm.URL = s.publicBase(r) + c.HookPath
	switch {
	case res.Registered:
		vm.Notice = i18n.T(ctx, "Webhook registrado no provedor. Ligue o canal para começar a receber.")
	case res.Note != "":
		vm.Notice = res.Note
	}
	s.renderChannel(w, r, co, http.StatusOK, vm)
}

func (s *server) handleDeleteChannel(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, c, ok := s.channelByParam(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := be.DeleteChannel(ctx, actorFrom(ctx), co.ID, c.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/canais?ok=apagado")
}

func (s *server) handleLinkChannel(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, c, ok := s.channelByParam(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	link, err := be.CreateChannelLink(ctx, actorFrom(ctx), co.ID, c.ID)
	vm, verr := s.channelVM(r, co, be, c)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	vm.Notice = ""
	if err != nil {
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
		s.renderChannel(w, r, co, statusFor(err), vm)
		return
	}
	vm.LinkCode, vm.LinkHint = link.Code, link.Hint
	vm.MyStatus = "pending"
	s.renderChannel(w, r, co, http.StatusOK, vm)
}

func (s *server) handleRevokeIdentity(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, c, ok := s.channelByParam(w, r, co)
	if !ok {
		return
	}
	id, err := domain.ParseID(r.PathValue("identity"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	if err := be.RevokeChannelIdentity(ctx, actorFrom(ctx), co.ID, c.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, s.channelSelf(co, c)+"?ok=revogado")
}

func (s *server) handleResendChannelMessage(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, c, ok := s.channelByParam(w, r, co)
	if !ok {
		return
	}
	id, err := domain.ParseID(r.PathValue("message"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	if _, err := be.ResendChannelMessage(ctx, actorFrom(ctx), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, s.channelSelf(co, c)+"?ok=reenfileirado")
}
