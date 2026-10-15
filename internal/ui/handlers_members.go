package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

func memberRoleLabel(ctx context.Context, r domain.MemberRole) string {
	switch r {
	case domain.MemberOwner:
		return i18n.T(ctx, "Dono")
	case domain.MemberAdmin:
		return "Admin"
	case domain.MemberMember:
		return i18n.T(ctx, "Membro")
	case domain.MemberViewer:
		return i18n.T(ctx, "Leitor")
	}
	return string(r)
}

// membersVM monta a seção "Membros" da empresa. Só existe no modo authenticated.
func (s *server) membersVM(r *http.Request, co domain.Company) (views.MembersVM, error) {
	if s.auth == nil {
		return views.MembersVM{}, nil
	}
	ctx := r.Context()
	me, ok := auth.IdentityFrom(ctx)
	if !ok {
		return views.MembersVM{}, nil
	}
	role, err := s.be.MyRole(ctx, co.ID)
	if err != nil {
		return views.MembersVM{}, err
	}
	ms, err := s.be.ListMembers(ctx, co.ID)
	if err != nil {
		return views.MembersVM{}, err
	}
	vm := views.MembersVM{Show: true, MyRole: string(role), CanInvite: role.AtLeast(domain.MemberAdmin)}
	for _, target := range []domain.MemberRole{domain.MemberOwner, domain.MemberAdmin, domain.MemberMember, domain.MemberViewer} {
		if role.Manages(target) {
			vm.Roles = append(vm.Roles, views.RoleOption{Value: string(target), Label: memberRoleLabel(ctx, target)})
		}
	}
	now := s.now()
	for _, m := range ms {
		vm.Members = append(vm.Members, views.MemberRow{ID: m.UserID.String(), Name: displayName(m), Email: m.Email, Role: string(m.Role),
			RoleLabel: memberRoleLabel(ctx, m.Role), Joined: relTimeCtx(ctx, m.CreatedAt, now, time.UTC),
			Self: m.UserID == me.User.ID, CanEdit: role.Manages(m.Role)})
	}
	if vm.CanInvite {
		is, err := s.be.ListInvites(ctx, co.ID)
		if err != nil {
			return views.MembersVM{}, err
		}
		for _, in := range is {
			vm.Invites = append(vm.Invites, views.InviteRow{ID: in.ID.String(), Email: in.Email, RoleLabel: memberRoleLabel(ctx, in.Role),
				Expires: relTimeCtx(ctx, in.ExpiresAt, now, time.UTC), Expired: in.Expired(now), CanRevoke: role.Manages(in.Role)})
		}
	}
	return vm, nil
}

func displayName(m domain.Member) string {
	if n := strings.TrimSpace(m.Name); n != "" {
		return n
	}
	local, _, _ := strings.Cut(m.Email, "@")
	return local
}

// memberFail re-renders the settings page with the error in the "Membros" section.
func (s *server) memberFail(w http.ResponseWriter, r *http.Request, co domain.Company, err error, email string) {
	st, title, msg := friendly(r.Context(), err)
	if st >= 500 || st == http.StatusNotFound {
		s.fail(w, r, &co, err)
		return
	}
	vm, verr := s.settingsVM(r)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	vm.Members.Error, vm.Members.Email = title+". "+msg, email
	s.renderSettings(w, r, co, st, vm)
}

func (s *server) requireAuth(w http.ResponseWriter, r *http.Request, co domain.Company) bool {
	if s.auth == nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return false
	}
	return true
}

func (s *server) handleInviteMember(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.requireAuth(w, r, co) {
		return
	}
	email := trimForm(r, "email")
	inv, tok, err := s.be.InviteMember(r.Context(), actorFrom(r.Context()), co.ID, email, domain.MemberRole(trimForm(r, "role")))
	if err != nil {
		s.memberFail(w, r, co, err, email)
		return
	}
	vm, err := s.settingsVM(r)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Members.NewLink, vm.Members.NewLinkEmail = s.inviteLink(r, tok), inv.Email
	s.renderSettings(w, r, co, http.StatusCreated, vm)
}

// inviteLink é o link absoluto de aceite; o host da requisição já passou pelo host check.
func (s *server) inviteLink(r *http.Request, token string) string {
	scheme := "http"
	if s.secure || r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + auth.InvitePath + url.PathEscape(token)
}

func (s *server) handleRevokeInvite(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.requireAuth(w, r, co) {
		return
	}
	id, err := domain.ParseID(r.PathValue("invite"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.be.RevokeInvite(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.memberFail(w, r, co, err, "")
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes#membros")
}

func (s *server) handleMemberRole(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.requireAuth(w, r, co) {
		return
	}
	id, err := domain.ParseID(r.PathValue("user"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if _, err := s.be.SetMemberRole(r.Context(), actorFrom(r.Context()), co.ID, id, domain.MemberRole(trimForm(r, "role"))); err != nil {
		s.memberFail(w, r, co, err, "")
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes#membros")
}

func (s *server) handleMemberRemove(w http.ResponseWriter, r *http.Request, co domain.Company) {
	if !s.requireAuth(w, r, co) {
		return
	}
	id, err := domain.ParseID(r.PathValue("user"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.be.RemoveMember(r.Context(), actorFrom(r.Context()), co.ID, id); err != nil {
		s.memberFail(w, r, co, err, "")
		return
	}
	if me, ok := auth.IdentityFrom(r.Context()); ok && me.User.ID == id { // saiu: a empresa some para ele
		redirect(w, r, "/")
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes#membros")
}

// ---------- /convite/{token} ----------

func (s *server) inviteBase(r *http.Request, pv service.InvitePreview, token string) views.InviteVM {
	return views.InviteVM{Version: s.version, Token: token, Company: pv.CompanyName, Email: pv.Email, RoleLabel: memberRoleLabel(r.Context(), pv.Role)}
}

func (s *server) renderInvalidInvite(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.render(w, r, status, views.InvitePage(views.InviteVM{Version: s.version, State: "invalid", Error: msg}))
}

// resolveInvite vê o convite e define a tela. Falha de token conta no limitador por IP; o convite
// inválido nunca diz o motivo (expirado, revogado, usado ou inexistente).
func (s *server) resolveInvite(w http.ResponseWriter, r *http.Request) (service.InvitePreview, views.InviteVM, bool) {
	token := r.PathValue("token")
	ip := s.auth.Proxies.ClientIP(r)
	if ok, wait := s.auth.Limiter.Allow(ip, ""); !ok {
		w.Header().Set("Retry-After", itoa(int(wait.Seconds())+1))
		s.renderInvalidInvite(w, r, http.StatusTooManyRequests, i18n.Tf(r.Context(), "Muitas tentativas. Tente novamente em %d min.", int(wait.Minutes())+1))
		return service.InvitePreview{}, views.InviteVM{}, false
	}
	pv, err := s.auth.be.PreviewInvite(r.Context(), token)
	if err != nil {
		if errors.Is(err, domain.ErrInviteInvalid) {
			s.auth.Limiter.Fail(ip, "")
			s.renderInvalidInvite(w, r, http.StatusNotFound, "")
			return service.InvitePreview{}, views.InviteVM{}, false
		}
		s.fail(w, r, nil, err)
		return service.InvitePreview{}, views.InviteVM{}, false
	}
	vm := s.inviteBase(r, pv, token)
	switch me, signed := auth.IdentityFrom(r.Context()); {
	case signed && strings.EqualFold(me.User.Email, pv.Email):
		vm.State = "confirm"
	case signed:
		vm.State = "wrong"
	case s.auth.Provider == "firebase":
		vm.State = "firebase"
		fb := s.auth.Firebase
		vm.Firebase = &fb
	case pv.HasAccount:
		vm.State = "login"
	default:
		vm.State = "create"
	}
	return pv, vm, true
}

func (s *server) handleInvitePage(w http.ResponseWriter, r *http.Request) {
	_, vm, ok := s.resolveInvite(w, r)
	if !ok {
		return
	}
	w.Header().Set("Referrer-Policy", "same-origin")
	s.render(w, r, http.StatusOK, views.InvitePage(vm))
}

func (s *server) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	pv, vm, ok := s.resolveInvite(w, r)
	if !ok {
		return
	}
	w.Header().Set("Referrer-Policy", "same-origin")
	ctx := r.Context()
	ip := s.auth.Proxies.ClientIP(r)
	token := vm.Token
	fail := func(status int, msg string) {
		vm.Error = msg
		s.render(w, r, status, views.InvitePage(vm))
	}
	done := func(u domain.User, fresh bool) {
		if fresh {
			if err := s.startSession(w, r, u); err != nil {
				s.fail(w, r, nil, err)
				return
			}
		}
		http.Redirect(w, r, "/c/"+pv.CompanySlug+"/", http.StatusSeeOther)
	}
	invalid := func() {
		s.auth.Limiter.Fail(ip, "")
		s.renderInvalidInvite(w, r, http.StatusNotFound, "")
	}
	generic := func(err error) bool {
		switch {
		case errors.Is(err, auth.ErrBusy):
			w.Header().Set("Retry-After", "2")
			fail(http.StatusServiceUnavailable, i18n.T(ctx, "Muitos logins ao mesmo tempo. Tente de novo em instantes."))
		case errors.Is(err, domain.ErrInviteInvalid):
			invalid()
		case errors.Is(err, domain.ErrInvalid):
			msg := i18n.T(ctx, "Confira os dados.")
			var ve *domain.ValidationError
			if errors.As(err, &ve) {
				msg = ve.Field + ": " + ve.Reason + "."
			}
			fail(http.StatusUnprocessableEntity, msg)
		default:
			return false
		}
		return true
	}

	switch vm.State {
	case "confirm":
		me, _ := auth.IdentityFrom(ctx)
		if _, err := s.auth.be.AcceptInvite(ctx, token, me.User); err != nil {
			if !generic(err) {
				s.fail(w, r, nil, err)
			}
			return
		}
		done(me.User, false)
	case "wrong":
		fail(http.StatusForbidden, i18n.T(ctx, "Você está conectado com outro e-mail. Saia e entre com a conta convidada."))
	case "login":
		if ok, wait := s.auth.Limiter.Allow(ip, pv.Email); !ok {
			w.Header().Set("Retry-After", itoa(int(wait.Seconds())+1))
			fail(http.StatusTooManyRequests, i18n.Tf(ctx, "Muitas tentativas. Tente novamente em %d min.", int(wait.Minutes())+1))
			return
		}
		u, err := s.auth.be.AuthenticateLocal(ctx, pv.Email, r.PostFormValue("password"))
		if errors.Is(err, domain.ErrUnauthenticated) {
			s.auth.Limiter.Fail(ip, pv.Email)
			fail(http.StatusUnauthorized, i18n.T(ctx, "E-mail ou senha incorretos."))
			return
		}
		if err != nil {
			if !generic(err) {
				s.fail(w, r, nil, err)
			}
			return
		}
		if _, err := s.auth.be.AcceptInvite(ctx, token, u); err != nil {
			if !generic(err) {
				s.fail(w, r, nil, err)
			}
			return
		}
		s.auth.Limiter.Success(ip, pv.Email)
		done(u, true)
	case "create":
		vm.Name = trimForm(r, "name")
		u, _, err := s.auth.be.AcceptInviteLocal(ctx, token, vm.Name, r.PostFormValue("password"))
		if errors.Is(err, domain.ErrConflict) { // a conta apareceu no meio: peça a senha
			vm.State = "login"
			fail(http.StatusConflict, i18n.T(ctx, "Já existe uma conta com este e-mail. Entre com a senha para aceitar."))
			return
		}
		if err != nil {
			if !generic(err) {
				s.fail(w, r, nil, err)
			}
			return
		}
		done(u, true)
	default: // firebase: o aceite acontece em POST /auth/firebase
		s.renderInvalidInvite(w, r, http.StatusNotFound, "")
	}
}
