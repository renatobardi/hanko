package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Conexões OAuth (ADR-0043): a aba Conexões em Integrações e o retorno do navegador (/oauth/callback).

// connectionBackend é o que a aba pede ao service; opcional para que os fakes que não a usam sigam valendo.
type connectionBackend interface {
	CreateConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateConnectionInput) (service.ConnectionView, error)
	ListConnections(ctx context.Context, companyID domain.ID) ([]service.ConnectionView, error)
	AuthorizeConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectionAuthStart, error)
	CompleteConnectionAuth(ctx context.Context, actor domain.Actor, in service.CompleteConnectionInput) (service.ConnectionView, error)
	ProbeConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectionView, error)
	RefreshConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectionView, error)
	DisconnectConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectionView, error)
	DeleteConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) error
}

var connectionNotices = map[string]string{
	"criada":       i18n.N("Conexão criada. Autorize para concluir."),
	"conectada":    i18n.N("Conexão autorizada."),
	"testada":      i18n.N("Teste concluído."),
	"renovada":     i18n.N("Token renovado."),
	"desconectada": i18n.N("Conexão desconectada."),
	"apagada":      i18n.N("Conexão apagada."),
}

func (s *server) connectionRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return s.sameOrigin(h) }
	const b = "/c/{company}/ferramentas/conexoes"
	mux.HandleFunc("GET "+b, co(s.handleConnections))
	mux.HandleFunc("POST "+b, post(co(s.handleCreateConnection)))
	mux.HandleFunc("POST "+b+"/{connection}/autorizar", post(co(s.handleAuthorizeConnection)))
	mux.HandleFunc("POST "+b+"/{connection}/testar", post(co(s.handleConnectionOp("testada"))))
	mux.HandleFunc("POST "+b+"/{connection}/renovar", post(co(s.handleConnectionOp("renovada"))))
	mux.HandleFunc("POST "+b+"/{connection}/desconectar", post(co(s.handleConnectionOp("desconectada"))))
	mux.HandleFunc("POST "+b+"/{connection}/apagar", post(co(s.handleConnectionOp("apagada"))))
	// o retorno do servidor de autorização (GET de topo, com a sessão); fora do /c/{company} porque o state é que acha a conexão
	mux.HandleFunc("GET "+service.OAuthCallbackPath, s.handleOAuthCallback)
}

func (s *server) connBackend(w http.ResponseWriter, r *http.Request, co *domain.Company) (connectionBackend, bool) {
	b, ok := s.be.(connectionBackend)
	if !ok {
		s.fail(w, r, co, domain.ErrNotFound)
	}
	return b, ok
}

func connStatusBadge(ctx context.Context, st domain.ConnectionStatus) views.Badge {
	switch st {
	case domain.ConnConnected:
		return views.Badge{Label: i18n.T(ctx, "conectada"), Class: "done"}
	case domain.ConnPendingAuth:
		return views.Badge{Label: i18n.T(ctx, "aguardando autorização"), Class: "warn"}
	case domain.ConnExpired:
		return views.Badge{Label: i18n.T(ctx, "expirada"), Class: "danger"}
	case domain.ConnRevoked:
		return views.Badge{Label: i18n.T(ctx, "revogada"), Class: "danger"}
	case domain.ConnError:
		return views.Badge{Label: i18n.T(ctx, "com erro"), Class: "danger"}
	}
	return views.Badge{Label: i18n.T(ctx, "rascunho"), Class: "outline"}
}

func connSetupText(ctx context.Context, st domain.Setup) string {
	switch st {
	case domain.SetupAuthorize:
		return i18n.T(ctx, "autorizar no navegador")
	case domain.SetupFinish:
		return i18n.T(ctx, "concluir a autorização no navegador (ou recomeçar)")
	case domain.SetupReconnect:
		return i18n.T(ctx, "reconectar")
	case domain.SetupProbe:
		return i18n.T(ctx, "testar de novo")
	}
	return ""
}

func (s *server) connectionsVM(ctx context.Context, r *http.Request, co domain.Company, b connectionBackend) (views.ConnectionsVM, error) {
	tb := toolsBase(co) + "/conexoes"
	q := r.URL.Query()
	vm := views.ConnectionsVM{Notice: i18n.T(ctx, connectionNotices[q.Get("ok")]), Error: q.Get("erro"), CreateHref: tb,
		NoPublicURL: s.platform.PublicURL == "" && s.platform.AgentURL == ""}
	vm.IdentityOpts = []views.Option{{Value: "user", Label: i18n.T(ctx, "Pessoal (só eu uso)")}}
	if views.RoleFrom(ctx).AtLeast(domain.MemberAdmin) {
		vm.IdentityOpts = []views.Option{{Value: "company", Label: i18n.T(ctx, "Empresa (as personas usam)"), Selected: true}, vm.IdentityOpts[0]}
	}
	list, err := b.ListConnections(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	now := time.Now()
	for _, c := range list {
		row := views.ConnectionRowVM{Name: c.Name, Slug: c.Slug, URL: c.URL, Identity: i18n.T(ctx, "Empresa"), Status: connStatusBadge(ctx, c.Status),
			Setup: connSetupText(ctx, c.Setup), Error: c.LastError, Connected: c.Status == domain.ConnConnected,
			AuthorizeLabel: i18n.T(ctx, "Autorizar"), AuthorizeHref: tb + "/" + url.PathEscape(c.Slug) + "/autorizar",
			ProbeHref: tb + "/" + url.PathEscape(c.Slug) + "/testar", RefreshHref: tb + "/" + url.PathEscape(c.Slug) + "/renovar",
			DisconnectHref: tb + "/" + url.PathEscape(c.Slug) + "/desconectar", DeleteHref: tb + "/" + url.PathEscape(c.Slug) + "/apagar"}
		if c.Identity == domain.ConnIdentityUser {
			row.Identity = i18n.T(ctx, "Pessoal")
		}
		if c.Status != domain.ConnDraft && c.Status != domain.ConnPendingAuth {
			row.AuthorizeLabel = i18n.T(ctx, "Reconectar")
		}
		if c.ExpiresAt != nil {
			row.Expires = c.ExpiresAt.In(time.Local).Format("02/01 15:04")
			if c.ExpiresAt.Before(now) {
				row.Expires += " · " + i18n.T(ctx, "vencido")
			}
		}
		row.CanOperate = c.Identity == domain.ConnIdentityUser || views.RoleFrom(ctx).AtLeast(domain.MemberAdmin)
		if c.IntegrationSlug != "" {
			row.IntegrationHref = "/c/" + co.Slug + "/integracoes/" + url.PathEscape(c.IntegrationSlug)
		}
		if c.GateID != nil && c.Status != domain.ConnConnected {
			row.GateHref = "/c/" + co.Slug + "/gates/" + c.GateID.String()
		}
		vm.Rows = append(vm.Rows, row)
	}
	return vm, nil
}

func (s *server) handleConnections(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.connBackend(w, r, &co)
	if !ok {
		return
	}
	ctx := r.Context()
	vm, err := s.connectionsVM(ctx, r, co, b)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	sh := s.toolsShell(r, co, i18n.T(ctx, "Conexões"), views.Crumb{Label: i18n.T(ctx, "Conexões")})
	s.render(w, r, http.StatusOK, views.ConnectionsPage(sh, vm))
}

func (s *server) handleCreateConnection(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.connBackend(w, r, &co)
	if !ok {
		return
	}
	ctx := r.Context()
	in := service.CreateConnectionInput{Name: trimForm(r, "name"), URL: trimForm(r, "url"), Identity: domain.ConnectionIdentity(trimForm(r, "identity")),
		Scopes: strings.Fields(trimForm(r, "scopes")), ClientID: trimForm(r, "client_id")}
	if _, err := b.CreateConnection(ctx, actorFrom(ctx), co.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/conexoes?ok=criada")
}

func (s *server) handleAuthorizeConnection(w http.ResponseWriter, r *http.Request, co domain.Company) {
	b, ok := s.connBackend(w, r, &co)
	if !ok {
		return
	}
	ctx := r.Context()
	st, err := b.AuthorizeConnection(ctx, actorFrom(ctx), co.ID, r.PathValue("connection"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	// vai para o servidor de autorização (URL externa, montada pelo oauth.AuthURL a partir dos metadados já validados)
	w.Header().Set("Referrer-Policy", "no-referrer")
	if isHX(r) {
		w.Header().Set("HX-Redirect", st.URL)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, st.URL, http.StatusSeeOther)
}

func (s *server) handleConnectionOp(notice string) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		b, ok := s.connBackend(w, r, &co)
		if !ok {
			return
		}
		ctx, ref := r.Context(), r.PathValue("connection")
		var err error
		switch notice {
		case "testada":
			_, err = b.ProbeConnection(ctx, actorFrom(ctx), co.ID, ref)
		case "renovada":
			_, err = b.RefreshConnection(ctx, actorFrom(ctx), co.ID, ref)
		case "desconectada":
			_, err = b.DisconnectConnection(ctx, actorFrom(ctx), co.ID, ref)
		default:
			err = b.DeleteConnection(ctx, actorFrom(ctx), co.ID, ref)
		}
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		redirect(w, r, toolsBase(co)+"/conexoes?ok="+notice)
	}
}

// handleOAuthCallback é o retorno do servidor de autorização. Exige sessão (o Guard redireciona para /entrar); o state
// (uso único) acha a conexão e o service confere que quem volta é quem começou. A resposta é sempre um redirect para uma
// URL limpa: code e state não ficam no histórico nem no Referer.
func (s *server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	b, ok := s.be.(connectionBackend)
	if !ok {
		s.fail(w, r, nil, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	v, err := b.CompleteConnectionAuth(ctx, actorFrom(ctx), service.CompleteConnectionInput{State: q.Get("state"), Code: q.Get("code"),
		ErrorCode: q.Get("error"), ErrorDesc: q.Get("error_description")})
	if err != nil {
		var company *domain.Company
		if v.CompanyID != (domain.ID{}) {
			if c, cerr := s.be.GetCompany(ctx, v.CompanyID.String()); cerr == nil {
				company = &c
			}
		}
		if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrConflict) {
			// o erro é de domínio e sem segredo: vai para a tela de conexões, se soubermos a empresa
			if company != nil {
				redirect(w, r, toolsBase(*company)+"/conexoes?erro="+url.QueryEscape(err.Error()))
				return
			}
		}
		s.fail(w, r, company, err)
		return
	}
	co, err := s.be.GetCompany(ctx, v.CompanyID.String())
	if err != nil {
		s.fail(w, r, nil, err)
		return
	}
	redirect(w, r, toolsBase(co)+"/conexoes?ok=conectada")
}
