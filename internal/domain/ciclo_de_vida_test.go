package domain

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestArchiveAllowsVerb(t *testing.T) {
	for verb, want := range map[string]bool{
		"company.archived": true, "company.restored": true, "company.deleted": true, "run.cancelled": true, "run.finished": true, "run.queued": false, "run.started": false,
		"task.created": false, "persona.updated": false, "comment.created": false, "flow.enabled": false, "": false,
	} {
		if got := ArchiveAllowsVerb(verb); got != want {
			t.Errorf("ArchiveAllowsVerb(%q) = %v, quer %v", verb, got, want)
		}
	}
}

func TestKeyScope(t *testing.T) {
	cases := []struct {
		scope        KeyScope
		method, path string
		want         bool
	}{
		{"", http.MethodPost, "/api/v1/tasks", true},
		{KeyScopeFull, http.MethodDelete, "/api/v1/x", true},
		{KeyScopeFull, http.MethodPost, "/mcp", true},
		{KeyScopeRead, http.MethodGet, "/api/v1/companies", true},
		{KeyScopeRead, http.MethodHead, "/api/v1/companies", true},
		{KeyScopeRead, http.MethodOptions, "/api/v1/companies", true},
		{KeyScopeRead, http.MethodPost, "/api/v1/companies", false},
		{KeyScopeRead, http.MethodPatch, "/api/v1/companies/x", false},
		{KeyScopeRead, http.MethodDelete, "/api/v1/companies/x", false},
		{KeyScopeRead, http.MethodGet, "/mcp", false},
		{KeyScopeRead, http.MethodGet, "/metrics", false},
		{KeyScope("outro"), http.MethodGet, "/api/v1/x", false},
	}
	for _, c := range cases {
		if got := c.scope.Allows(c.method, c.path); got != c.want {
			t.Errorf("%q.Allows(%s %s) = %v, quer %v", c.scope, c.method, c.path, got, c.want)
		}
	}
	if !KeyScopeFull.Valid() || !KeyScopeRead.Valid() || KeyScope("x").Valid() || KeyScope("").Valid() {
		t.Error("Valid errado")
	}
}

func TestCLIAuthRequestExpired(t *testing.T) {
	now := time.Now()
	r := CLIAuthRequest{ExpiresAt: now.Add(time.Minute)}
	if r.Expired(now) {
		t.Error("ainda vale")
	}
	if !r.Expired(now.Add(time.Minute)) || !r.Expired(now.Add(time.Hour)) {
		t.Error("deveria ter expirado")
	}
}

func TestConflictAction(t *testing.T) {
	for _, a := range []ConflictAction{ConflictSkip, ConflictRename, ConflictReplace} {
		if !a.Valid() {
			t.Errorf("%q deveria ser válida", a)
		}
	}
	if ConflictAction("merge").Valid() || ConflictAction("").Valid() {
		t.Error("ação desconhecida aceita")
	}
	if ConflictSkip.ItemOf() != ItemSkip || ConflictRename.ItemOf() != ItemRename || ConflictReplace.ItemOf() != ItemReplace {
		t.Error("ItemOf errado")
	}
}

func TestRenameTo(t *testing.T) {
	taken := map[string]bool{"api": true, "api-2": true, "api-3": true, "Ana (2)": true}
	if got := RenameTo("api", false, func(s string) bool { return taken[s] }); got != "api-4" {
		t.Errorf("slug: %q", got)
	}
	if got := RenameTo("Ana", true, func(s string) bool { return taken[s] }); got != "Ana (3)" {
		t.Errorf("nome: %q", got)
	}
	long := strings.Repeat("a", 63)
	got := RenameTo(long, false, func(s string) bool { return s == long })
	if len(got) > 63 || !ValidSlug(got) || !strings.HasSuffix(got, "-2") {
		t.Errorf("slug longo: %q (%d)", got, len(got))
	}
	// base que termina em traço depois do corte não deixa "--"
	b := strings.Repeat("a", 60) + "-bbb"
	if got := RenameTo(b, false, func(s string) bool { return s == b }); strings.Contains(got, "--") || !ValidSlug(got) {
		t.Errorf("traço duplo: %q", got)
	}
}

func TestParseResolution(t *testing.T) {
	k, key, a, err := ParseResolution("personas/dev=rename")
	if err != nil || k != "personas" || key != "dev" || a != ConflictRename {
		t.Fatalf("%v %q %q %q", err, k, key, a)
	}
	k, key, a, err = ParseResolution("subscriptions/Plano Pro=replace")
	if err != nil || k != "subscriptions" || key != "Plano Pro" || a != ConflictReplace {
		t.Fatalf("%v %q %q %q", err, k, key, a)
	}
	for _, bad := range []string{"", "personas/dev", "personas=skip", "/dev=skip", "personas/=skip", "personas/dev=merge"} {
		if _, _, _, err := ParseResolution(bad); err == nil {
			t.Errorf("%q deveria falhar", bad)
		}
	}
}

func TestCompanyArchived(t *testing.T) {
	now := time.Now()
	if (Company{}).Archived() || !(Company{ArchivedAt: &now}).Archived() {
		t.Error("Archived errado")
	}
}
