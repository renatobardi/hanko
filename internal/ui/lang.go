package ui

import (
	"net/http"
	"net/url"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
)

// langPath is where the language switch posts.
const langPath = "/idioma"

// langCookieTTL is how long the choice is remembered.
const langCookieTTL = 365 * 24 * time.Hour

// handlerRegistry is what route registration needs from a mux; langMux implements it.
type handlerRegistry interface {
	Handle(pattern string, h http.Handler)
	HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request))
}

// langMux registers every route behind the language middleware, so each handler sees
// the language in its request context (tests mount s.routes on a bare mux, so the
// middleware lives here and not around the mux).
type langMux struct{ mux *http.ServeMux }

func (m langMux) Handle(pattern string, h http.Handler) { m.mux.Handle(pattern, withLang(h)) }
func (m langMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.mux.Handle(pattern, withLang(http.HandlerFunc(h)))
}

// withLang resolves the UI language of the request: the hanko_lang cookie wins over
// Accept-Language (en* is English, anything else pt-BR).
func withLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Cookie, Accept-Language")
		next.ServeHTTP(w, r.WithContext(i18n.WithLang(r.Context(), requestLang(r))))
	})
}

func requestLang(r *http.Request) i18n.Lang {
	if c, err := r.Cookie(i18n.Cookie); err == nil {
		if l, ok := i18n.Parse(c.Value); ok {
			return l
		}
	}
	return i18n.FromAccept(r.Header.Get("Accept-Language"))
}

// handleLang stores the chosen language and goes back to the page the person was on.
func (s *server) handleLang(w http.ResponseWriter, r *http.Request) {
	l, ok := i18n.Parse(r.PostFormValue("lang"))
	if !ok {
		s.fail(w, r, nil, domain.Invalid("lang", i18n.T(r.Context(), "idioma desconhecido")))
		return
	}
	// Secure follows the session cookie's config (https public URL); HttpOnly and Lax always.
	http.SetCookie(w, &http.Cookie{Name: i18n.Cookie, Value: string(l), Path: "/", MaxAge: int(langCookieTTL / time.Second), //nolint:gosec // Secure vem da config (https)
		Expires: s.now().Add(langCookieTTL), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	redirect(w, r, backTo(r))
}

// backTo is the local path of the Referer ("/" when absent, foreign or unusable). The sign-in
// page keeps its ?next=, which auth.SafeNext alone would drop.
func backTo(r *http.Request) string {
	u, err := url.Parse(r.Referer())
	if err != nil || r.Referer() == "" || (u.Host != "" && u.Host != r.Host) {
		return "/"
	}
	switch u.Path {
	case langPath:
		return "/"
	case auth.LoginPath:
		if next := auth.SafeNext(u.Query().Get("next")); next != "/" {
			return auth.LoginPath + "?next=" + url.QueryEscape(next)
		}
		return auth.LoginPath
	}
	return auth.SafeNext(u.RequestURI())
}
