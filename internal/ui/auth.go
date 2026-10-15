package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// AuthBackend is the subset of *service.Service used by sign-in, sessions and API keys.
type AuthBackend interface {
	CountUsers(ctx context.Context) (int, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	CreateOwner(ctx context.Context, email, name, password string) (domain.User, error)
	AuthenticateLocal(ctx context.Context, email, password string) (domain.User, error)
	LoginFirebase(ctx context.Context, in service.FirebaseLogin) (domain.User, error)
	CreateSession(ctx context.Context, userID domain.ID, meta service.SessionMeta, replaceToken string) (string, domain.Session, error)
	EndSession(ctx context.Context, token string) error
	CreateAPIKey(ctx context.Context, userID domain.ID, name string) (string, domain.APIKey, error)
	ListAPIKeys(ctx context.Context, userID domain.ID) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, userID, keyID domain.ID) (domain.APIKey, error)
}

var _ AuthBackend = (*service.Service)(nil)

// IDTokenVerifier validates Firebase ID tokens (*auth.FirebaseVerifier).
type IDTokenVerifier interface {
	Verify(ctx context.Context, idToken string) (auth.FirebaseClaims, error)
}

// AuthDeps configures authenticated mode in the UI.
type AuthDeps struct {
	// Backend defaults to Deps.Svc.
	Backend AuthBackend
	// Provider is "local" or "firebase".
	Provider string
	// Firebase is the public web config sent to the browser (provider firebase).
	Firebase views.FirebaseWeb
	// Verifier validates ID tokens (provider firebase).
	Verifier IDTokenVerifier
	// Admission is the allow-list policy (provider firebase).
	Admission auth.Admission
	// Limiter throttles failed sign-ins. Nil = a default limiter (5 failures / 15 min).
	Limiter *auth.LoginLimiter
	// Secure marks the session cookie Secure (PUBLIC_URL is https).
	Secure bool
	// Proxies are the peers whose X-Forwarded-For is believed (HANKO_TRUSTED_PROXIES). The zero
	// value trusts nobody: the client IP is the connection's RemoteAddr.
	Proxies auth.TrustedProxies
	// OwnerEmail is the only e-mail that becomes owner at first Firebase login (HANKO_AUTH_OWNER_EMAIL).
	OwnerEmail string
	// WebBootstrap lets the sign-in page create the first (owner) account of the local provider.
	// Off, the page tells the operator to run `hanko auth create-owner`.
	WebBootstrap bool
}

type authState struct {
	AuthDeps
	be  AuthBackend
	now func() time.Time
}

func newAuthState(d AuthDeps, be AuthBackend, now func() time.Time) *authState {
	if d.Limiter == nil {
		d.Limiter = &auth.LoginLimiter{}
	}
	if d.Provider == "" {
		d.Provider = "local"
	}
	return &authState{AuthDeps: d, be: be, now: now}
}

func (s *server) authRoutes(mux handlerRegistry) {
	if s.auth == nil {
		return
	}
	mux.HandleFunc("GET "+auth.LoginPath, s.handleLogin)
	mux.HandleFunc("POST "+auth.LoginPath, s.sameOrigin(s.handleLoginLocal))
	mux.HandleFunc("POST /entrar/dono", s.sameOrigin(s.handleCreateOwner))
	mux.HandleFunc("POST /auth/firebase", s.handleFirebase)
	mux.HandleFunc("POST /sair", s.sameOrigin(s.handleLogout))
}

func (s *server) loginVM(r *http.Request) views.LoginVM {
	vm := views.LoginVM{Provider: s.auth.Provider, Next: auth.SafeNext(r.FormValue("next")), Version: s.version}
	if s.auth.Provider == "firebase" {
		fb := s.auth.Firebase
		vm.Firebase = &fb
	}
	return vm
}

func (s *server) renderLogin(w http.ResponseWriter, r *http.Request, status int, vm views.LoginVM) {
	if s.auth.Provider != "firebase" {
		n, err := s.auth.be.CountUsers(r.Context())
		if err != nil {
			s.log.Error("ui: contar usuários", "err", err)
		}
		empty := err == nil && n == 0
		vm.FirstRun = empty && s.auth.WebBootstrap
		vm.NeedsCLI = empty && !s.auth.WebBootstrap
	}
	s.render(w, r, status, views.LoginPage(vm))
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.IdentityFrom(r.Context()); ok {
		http.Redirect(w, r, auth.SafeNext(r.URL.Query().Get("next")), http.StatusSeeOther) //nolint:gosec // SafeNext só aceita caminho local
		return
	}
	s.renderLogin(w, r, http.StatusOK, s.loginVM(r))
}

// startSession opens a session for the user, rotating any session the browser presented.
func (s *server) startSession(w http.ResponseWriter, r *http.Request, u domain.User) error {
	old := ""
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		old = c.Value
	}
	tok, sess, err := s.auth.be.CreateSession(r.Context(), u.ID, service.SessionMeta{UserAgent: r.UserAgent(), IP: s.auth.Proxies.ClientIP(r)}, old)
	if err != nil {
		return err
	}
	// Secure acompanha HANKO_PUBLIC_URL (https em prd); HttpOnly e SameSite=Lax sempre.
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: tok, Path: "/", Expires: sess.ExpiresAt, //nolint:gosec // Secure vem da config (https)
		MaxAge: int(auth.SessionTTL / time.Second), HttpOnly: true, Secure: s.auth.Secure, SameSite: http.SameSiteLaxMode})
	return nil
}

func (s *server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0), //nolint:gosec // Secure vem da config (https)
		HttpOnly: true, Secure: s.auth.Secure, SameSite: http.SameSiteLaxMode})
}

func (s *server) handleLoginLocal(w http.ResponseWriter, r *http.Request) {
	if s.auth.Provider != "local" {
		http.NotFound(w, r)
		return
	}
	vm := s.loginVM(r)
	vm.Email = trimForm(r, "email")
	password := r.PostFormValue("password")
	ip := s.auth.Proxies.ClientIP(r)
	if ok, wait := s.auth.Limiter.Allow(ip, vm.Email); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		vm.Error = i18n.Tf(r.Context(), "Muitas tentativas. Tente novamente em %d min.", int(wait.Minutes())+1)
		s.renderLogin(w, r, http.StatusTooManyRequests, vm)
		return
	}
	u, err := s.auth.be.AuthenticateLocal(r.Context(), vm.Email, password)
	if err != nil {
		if errors.Is(err, auth.ErrBusy) {
			s.busy(w, r, vm)
			return
		}
		if errors.Is(err, domain.ErrUnauthenticated) {
			s.auth.Limiter.Fail(ip, vm.Email)
			vm.Error = i18n.T(r.Context(), "E-mail ou senha incorretos.")
			s.renderLogin(w, r, http.StatusUnauthorized, vm)
			return
		}
		s.fail(w, r, nil, err)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, r, nil, err)
		return
	}
	s.auth.Limiter.Success(ip, vm.Email)
	http.Redirect(w, r, vm.Next, http.StatusSeeOther)
}

// busy answers 503 when every password-hashing slot is taken (the failure does not count against the limiter).
func (s *server) busy(w http.ResponseWriter, r *http.Request, vm views.LoginVM) {
	w.Header().Set("Retry-After", "2")
	vm.Error = i18n.T(r.Context(), "Muitos logins ao mesmo tempo. Tente de novo em instantes.")
	s.renderLogin(w, r, http.StatusServiceUnavailable, vm)
}

func (s *server) handleCreateOwner(w http.ResponseWriter, r *http.Request) {
	if s.auth.Provider != "local" || !s.auth.WebBootstrap {
		http.NotFound(w, r)
		return
	}
	vm := s.loginVM(r)
	vm.Email, vm.Name = trimForm(r, "email"), trimForm(r, "name")
	u, err := s.auth.be.CreateOwner(r.Context(), vm.Email, vm.Name, r.PostFormValue("password"))
	switch {
	case err == nil:
	case errors.Is(err, auth.ErrBusy):
		s.busy(w, r, vm)
		return
	case errors.Is(err, domain.ErrConflict):
		vm.Email, vm.Name = "", ""
		vm.Notice = i18n.T(r.Context(), "Já existe um usuário. Entre com a sua conta.")
		s.renderLogin(w, r, http.StatusConflict, vm)
		return
	case errors.Is(err, domain.ErrInvalid):
		var ve *domain.ValidationError
		vm.Error = i18n.T(r.Context(), "Confira os dados.")
		if errors.As(err, &ve) {
			vm.Error = ve.Field + ": " + ve.Reason + "."
		}
		s.renderLogin(w, r, http.StatusUnprocessableEntity, vm)
		return
	default:
		s.fail(w, r, nil, err)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, r, nil, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		if err := s.auth.be.EndSession(r.Context(), c.Value); err != nil {
			s.log.Error("ui: encerrar sessão", "err", err)
		}
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, auth.LoginPath, http.StatusSeeOther)
}

type firebaseReq struct {
	IDToken string `json:"id_token"`
	Next    string `json:"next"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "error": msg})
}

// handleFirebase exchanges a Firebase ID token for a Hanko session cookie.
func (s *server) handleFirebase(w http.ResponseWriter, r *http.Request) {
	if s.auth.Provider != "firebase" || s.auth.Verifier == nil {
		http.NotFound(w, r)
		return
	}
	if !auth.SameOrigin(r) {
		jsonErr(w, http.StatusForbidden, "cross_origin", i18n.T(r.Context(), "Requisição de origem diferente bloqueada."))
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		jsonErr(w, http.StatusUnsupportedMediaType, "bad_request", i18n.T(r.Context(), "Use application/json."))
		return
	}
	var in firebaseReq
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil || json.Unmarshal(body, &in) != nil || in.IDToken == "" {
		jsonErr(w, http.StatusBadRequest, "bad_request", i18n.T(r.Context(), "Corpo inválido."))
		return
	}
	ip := s.auth.Proxies.ClientIP(r)
	if ok, wait := s.auth.Limiter.Allow(ip, ""); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		jsonErr(w, http.StatusTooManyRequests, "rate_limited", i18n.T(r.Context(), "Muitas tentativas. Tente de novo em alguns minutos."))
		return
	}
	claims, err := s.auth.Verifier.Verify(r.Context(), in.IDToken)
	switch {
	case err == nil:
	case errors.Is(err, auth.ErrUnverifiedEmail):
		jsonErr(w, http.StatusForbidden, "email_not_verified", i18n.T(r.Context(), "Confirme seu e-mail pelo link que enviamos e tente de novo."))
		return
	case errors.Is(err, auth.ErrJWKSUnavailable):
		s.log.Error("ui: chaves públicas do Firebase indisponíveis", "err", err)
		jsonErr(w, http.StatusServiceUnavailable, "unavailable", i18n.T(r.Context(), "Não foi possível validar o login agora. Tente de novo."))
		return
	default:
		s.auth.Limiter.Fail(ip, "")
		s.log.Warn("ui: ID token do Firebase recusado", "err", err, "ip", ip)
		jsonErr(w, http.StatusUnauthorized, "invalid_token", i18n.T(r.Context(), "Credencial inválida ou expirada. Tente entrar de novo."))
		return
	}
	u, err := s.auth.be.LoginFirebase(r.Context(), service.FirebaseLogin{
		UID: claims.UID, Email: claims.Email, Name: claims.Name,
		ListsConfigured: s.auth.Admission.Configured(), Admitted: s.auth.Admission.Allows(claims.Email),
		OwnerEmail: s.auth.OwnerEmail,
	})
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrNotAllowed):
		s.log.Warn("ui: login firebase fora da política de admissão", "email", claims.Email)
		jsonErr(w, http.StatusForbidden, "not_admitted", i18n.T(r.Context(), "Este e-mail não tem acesso a esta instalação do Hanko."))
		return
	case errors.Is(err, domain.ErrConflict):
		jsonErr(w, http.StatusConflict, "email_taken", i18n.T(r.Context(), "Este e-mail já está cadastrado por outro método de login."))
		return
	default:
		s.log.Error("ui: login firebase", "err", err)
		jsonErr(w, http.StatusInternalServerError, "internal", i18n.T(r.Context(), "Erro interno. Tente de novo."))
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.log.Error("ui: abrir sessão", "err", err)
		jsonErr(w, http.StatusInternalServerError, "internal", i18n.T(r.Context(), "Erro interno. Tente de novo."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": auth.SafeNext(in.Next)})
}

// ---------- settings: account and API keys ----------

func (s *server) settingsVM(r *http.Request) (views.SettingsVM, error) {
	vm := views.SettingsVM{Authenticated: s.auth != nil}
	id, ok := auth.IdentityFrom(r.Context())
	if s.auth == nil || !ok {
		vm.Authenticated = false
		return vm, nil
	}
	u := id.User
	vm.Name, vm.Email, vm.Provider, vm.Role = u.DisplayName(), u.Email, providerLabel(r.Context(), u.Provider), roleLabel(r.Context(), u.Role)
	keys, err := s.auth.be.ListAPIKeys(r.Context(), u.ID)
	if err != nil {
		return vm, err
	}
	now := s.now()
	for _, k := range keys {
		row := views.APIKeyRow{ID: k.ID.String(), Name: k.Name, Prefix: k.Prefix, Created: relTimeCtx(r.Context(), k.CreatedAt, now, time.UTC),
			LastUsed: i18n.T(r.Context(), "nunca"), Revoked: !k.Active()}
		if k.LastUsedAt != nil {
			row.LastUsed = relTimeCtx(r.Context(), *k.LastUsedAt, now, time.UTC)
		}
		vm.Keys = append(vm.Keys, row)
	}
	return vm, nil
}

func providerLabel(ctx context.Context, p domain.AuthProvider) string {
	if p == domain.ProviderFirebase {
		return i18n.T(ctx, "Firebase (Google ou e-mail)")
	}
	return i18n.T(ctx, "Local (e-mail e senha)")
}

func roleLabel(ctx context.Context, r domain.UserRole) string {
	switch r {
	case domain.RoleOwner:
		return i18n.T(ctx, "Dono")
	case domain.RoleAdmin:
		return "Admin"
	}
	return i18n.T(ctx, "Membro")
}

func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.SettingsVM) {
	ctx := r.Context()
	s.render(w, r, status, views.SettingsPage(s.shell(co, "", i18n.T(ctx, "Configurações"), views.Crumb{Label: i18n.T(ctx, "Configurações")}), vm))
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request, co domain.Company) {
	vm, err := s.settingsVM(r)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderSettings(w, r, co, http.StatusOK, vm)
}

func (s *server) handleCreateKey(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, ok := auth.IdentityFrom(r.Context())
	if s.auth == nil || !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	name := trimForm(r, "name")
	key, _, err := s.auth.be.CreateAPIKey(r.Context(), id.User.ID, name)
	vm, verr := s.settingsVM(r)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	if err != nil {
		if !errors.Is(err, domain.ErrInvalid) {
			s.fail(w, r, &co, err)
			return
		}
		_, title, msg := friendly(r.Context(), err)
		vm.Error = title + ". " + msg
		s.renderSettings(w, r, co, http.StatusUnprocessableEntity, vm)
		return
	}
	vm.NewKey, vm.KeyName = key, name
	s.renderSettings(w, r, co, http.StatusCreated, vm)
}

func (s *server) handleRevokeKey(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, ok := auth.IdentityFrom(r.Context())
	if s.auth == nil || !ok {
		s.fail(w, r, &co, domain.ErrNotAllowed)
		return
	}
	kid, err := domain.ParseID(r.PathValue("key"))
	if err != nil {
		s.fail(w, r, &co, fmt.Errorf("chave: %w", domain.ErrNotFound))
		return
	}
	if _, err := s.auth.be.RevokeAPIKey(r.Context(), id.User.ID, kid); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes")
}
