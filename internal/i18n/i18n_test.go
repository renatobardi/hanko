package i18n

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"pt-BR": PT, "en": EN, " en ": EN} {
		if got, ok := Parse(in); !ok || got != want {
			t.Errorf("Parse(%q) = %v, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "fr", "EN", "pt", "en-US"} {
		if _, ok := Parse(in); ok {
			t.Errorf("Parse(%q) must be rejected", in)
		}
	}
}

func TestFromAccept(t *testing.T) {
	cases := map[string]Lang{
		"":                          PT,
		"en":                        EN,
		"en-US,en;q=0.9":            EN,
		"pt-BR,pt;q=0.9,en;q=0.8":   PT,
		"en;q=0.9,pt-BR;q=0.8":      EN,
		"fr-FR,fr;q=0.9":            PT,
		"fr,en;q=0.5":               EN,
		"*":                         PT,
		"en;q=abc":                  EN,
		"de, en-GB;q=0.7, pt;q=0.6": EN,
	}
	for in, want := range cases {
		if got := FromAccept(in); got != want {
			t.Errorf("FromAccept(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTAndTf(t *testing.T) {
	pt := context.Background()
	en := WithLang(pt, EN)
	if FromContext(pt) != PT || FromContext(en) != EN {
		t.Fatal("FromContext")
	}
	if got := T(pt, "Entrar"); got != "Entrar" {
		t.Errorf("pt T = %q", got)
	}
	if got := T(en, "Entrar"); got != "Sign in" {
		t.Errorf("en T = %q", got)
	}
	if got := T(en, "texto sem tradução"); got != "texto sem tradução" {
		t.Errorf("fallback = %q", got)
	}
	if got := Tf(en, "%d tasks", 3); got != "3 tasks" {
		t.Errorf("en Tf = %q", got)
	}
	if got := Tf(pt, "%d tasks", 3); got != "3 tasks" {
		t.Errorf("pt Tf = %q", got)
	}
	if got := Tf(en, "Muitas tentativas. Tente novamente em %d min.", 5); got != "Too many attempts. Try again in 5 min." {
		t.Errorf("en Tf = %q", got)
	}
	if N("x") != "x" {
		t.Error("N must be a no-op")
	}
}

var verb = regexp.MustCompile(`%[a-z%]`)

// The catalog must be well formed: no empty value and the same printf verbs as the key.
func TestCatalog(t *testing.T) {
	if len(English()) < 600 {
		t.Fatalf("catalog has only %d entries", len(English()))
	}
	for k, v := range English() {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			t.Errorf("empty key or value: %q => %q", k, v)
		}
		if a, b := verb.FindAllString(k, -1), verb.FindAllString(v, -1); strings.Join(a, "") != strings.Join(b, "") {
			t.Errorf("verbs differ: %q => %q", k, v)
		}
		if strings.Contains(strings.ToLower(v), "firebase") || strings.Contains(strings.ToLower(v), "openai") || strings.Contains(strings.ToLower(v), "claude max") {
			t.Errorf("third-party name in English text: %q", v)
		}
	}
}

func TestFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Lang
	}{
		{"nothing set", nil, PT},
		{"HANKO_LANG en", map[string]string{"HANKO_LANG": "en"}, EN},
		{"HANKO_LANG pt-BR", map[string]string{"HANKO_LANG": "pt-BR"}, PT},
		{"HANKO_LANG is case-insensitive", map[string]string{"HANKO_LANG": "EN"}, EN},
		{"HANKO_LANG beats the locale", map[string]string{"HANKO_LANG": "pt-BR", "LC_ALL": "en_US.UTF-8"}, PT},
		{"HANKO_LANG en beats a pt locale", map[string]string{"HANKO_LANG": "en", "LANG": "pt_BR.UTF-8"}, EN},
		{"unknown HANKO_LANG falls through", map[string]string{"HANKO_LANG": "fr", "LANG": "en_US.UTF-8"}, EN},
		{"LC_ALL beats LC_MESSAGES and LANG", map[string]string{"LC_ALL": "pt_BR.UTF-8", "LC_MESSAGES": "en_US", "LANG": "en_US"}, PT},
		{"LC_MESSAGES beats LANG", map[string]string{"LC_MESSAGES": "en_GB.UTF-8", "LANG": "pt_BR.UTF-8"}, EN},
		{"LANG alone", map[string]string{"LANG": "en_US.UTF-8"}, EN},
		{"C locale is not English", map[string]string{"LANG": "C"}, PT},
		{"empty LC_ALL is skipped", map[string]string{"LC_ALL": "", "LANG": "en_US.UTF-8"}, EN},
		{"another language", map[string]string{"LANG": "de_DE.UTF-8"}, PT},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FromEnv(func(k string) string { return c.env[k] }); got != c.want {
				t.Errorf("FromEnv = %v, want %v", got, c.want)
			}
		})
	}
	if FromEnv(nil) != PT {
		t.Error("FromEnv(nil) must be pt-BR")
	}
}
