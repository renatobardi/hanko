// Package i18n is Hanko's tiny gettext-style translation layer for the web UI.
//
// pt-BR is the source language: the lookup key is the pt-BR text exactly as the
// template or handler writes it. For English the key is looked up in the catalog
// (en.go); any text without an entry (user data, domain errors, a key someone
// forgot) is returned unchanged. The language travels in the request context.
//
// The CLI uses the same layer: its language is resolved once from the environment
// (FromEnv) and carried in a context.
package i18n

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Lang is a UI language.
type Lang string

const (
	// PT is Brazilian Portuguese, the default and the source language.
	PT Lang = "pt-BR"
	// EN is English.
	EN Lang = "en"
)

// Cookie is the cookie that stores the language chosen by the person.
const Cookie = "hanko_lang"

// Langs lists the supported languages in display order.
var Langs = []Lang{PT, EN}

type ctxKey struct{}

// WithLang returns ctx carrying the language.
func WithLang(ctx context.Context, l Lang) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext is the language of ctx; PT when none was set.
func FromContext(ctx context.Context) Lang {
	if l, ok := ctx.Value(ctxKey{}).(Lang); ok && l != "" {
		return l
	}
	return PT
}

// Parse accepts the two cookie/form values: "pt-BR" and "en".
func Parse(v string) (Lang, bool) {
	switch Lang(strings.TrimSpace(v)) {
	case PT:
		return PT, true
	case EN:
		return EN, true
	}
	return PT, false
}

// FromAccept picks the language from an Accept-Language header: the supported
// tag with the highest q wins (en* is English, pt* is pt-BR); a header with no
// supported tag falls back to pt-BR.
func FromAccept(header string) Lang {
	best, bestQ := PT, -1.0
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag = strings.ToLower(strings.TrimSpace(tag))
		q := 1.0
		if p := strings.TrimSpace(params); strings.HasPrefix(p, "q=") {
			if v, err := strconv.ParseFloat(strings.TrimSpace(p[2:]), 64); err == nil {
				q = v
			}
		}
		var l Lang
		switch {
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			l = EN
		case tag == "pt" || strings.HasPrefix(tag, "pt-"):
			l = PT
		default:
			continue
		}
		if q > bestQ {
			best, bestQ = l, q
		}
	}
	return best
}

// EnvVar is the environment variable that forces the CLI language ("pt-BR" or "en").
const EnvVar = "HANKO_LANG"

// FromEnv resolves the CLI language: HANKO_LANG (pt*/en*, case-insensitive; any other value is
// ignored) wins; otherwise the first non-empty of LC_ALL, LC_MESSAGES and LANG decides (en* is
// English, anything else pt-BR); with none set, pt-BR.
func FromEnv(getenv func(string) string) Lang {
	if getenv == nil {
		return PT
	}
	switch v := strings.ToLower(strings.TrimSpace(getenv(EnvVar))); {
	case v == "en" || strings.HasPrefix(v, "en-") || strings.HasPrefix(v, "en_"):
		return EN
	case v == "pt" || strings.HasPrefix(v, "pt-") || strings.HasPrefix(v, "pt_"):
		return PT
	}
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := strings.ToLower(strings.TrimSpace(getenv(k))); v != "" {
			if strings.HasPrefix(v, "en") {
				return EN
			}
			return PT
		}
	}
	return PT
}

// T translates s (a pt-BR text) for the language of ctx. Unknown text is returned as is.
func T(ctx context.Context, s string) string {
	if FromContext(ctx) == EN {
		if t, ok := en[s]; ok {
			return t
		}
	}
	return s
}

// Tf is T for a printf format: the format is translated, then filled in.
func Tf(ctx context.Context, format string, args ...any) string {
	return fmt.Sprintf(T(ctx, format), args...)
}

// N marks a text for translation without translating it now (gettext's N_): use it for
// package-level tables, then call T on the value where it is shown.
func N(s string) string { return s }

// English returns the English catalog (read-only use; tests).
func English() map[string]string { return en }
