package codex

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/adapter"
)

// PlanOnly (plan-review, ADR-0041): sandbox somente leitura e as extra_args da persona não o desfazem.
func TestBuildArgsPlanOnly(t *testing.T) {
	got := BuildArgs(adapter.Spec{PlanOnly: true}, Config{Sandbox: "danger-full-access", ExtraArgs: []string{"--sandbox", "danger-full-access"}}, "/w", nil)
	want := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only", "-C", "/w", "-"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
}
