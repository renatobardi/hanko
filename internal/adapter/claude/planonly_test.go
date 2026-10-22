package claude

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/adapter"
)

// PlanOnly (plan-review, ADR-0041): modo plan e sem Edit/Write, mas as tools MCP do Hanko seguem liberadas e as
// extra_args da persona não reabrem a escrita.
func TestBuildArgsPlanOnly(t *testing.T) {
	got := BuildArgs(adapter.Spec{PlanOnly: true}, Config{PermissionMode: "bypassPermissions", ExtraArgs: []string{"--dangerously-skip-permissions"}}, "/tmp/m.json", "")
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "plan", "--disallowedTools", "Edit", "Write", "NotebookEdit",
		"--mcp-config", "/tmp/m.json", "--allowedTools", "mcp__hanko"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
}
