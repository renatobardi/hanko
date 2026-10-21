package ui

import (
	"net/http"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
)

// themePath is where the theme switch posts (Kubo DS: light/dark toggle in the sidebar footer).
const themePath = "/tema"

// themeCookie keeps the explicit choice; without it the page follows prefers-color-scheme.
const themeCookie = "hanko_theme"

// requestTheme is the explicit theme of the request ("light" or "dark"), or "" to follow the system.
func requestTheme(r *http.Request) string {
	if t, ok := r.Context().Value(themeOverrideKey{}).(string); ok {
		return t // rota que força o tema (catálogo do DS)
	}
	if c, err := r.Cookie(themeCookie); err == nil && (c.Value == "light" || c.Value == "dark") {
		return c.Value
	}
	return ""
}

// handleTheme stores the chosen theme ("system" clears it) and goes back to the page the person was on.
func (s *server) handleTheme(w http.ResponseWriter, r *http.Request) {
	switch v := r.PostFormValue("theme"); v {
	case "light", "dark":
		http.SetCookie(w, &http.Cookie{Name: themeCookie, Value: v, Path: "/", MaxAge: int(langCookieTTL / time.Second), //nolint:gosec // Secure vem da config (https)
			Expires: s.now().Add(langCookieTTL), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	case "system":
		http.SetCookie(w, &http.Cookie{Name: themeCookie, Value: "", Path: "/", MaxAge: -1, //nolint:gosec // Secure vem da config (https)
			HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	default:
		s.fail(w, r, nil, domain.Invalid("theme", i18n.T(r.Context(), "tema desconhecido")))
		return
	}
	redirect(w, r, backTo(r))
}
