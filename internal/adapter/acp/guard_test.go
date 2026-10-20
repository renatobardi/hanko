package acp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

// A política de tools do Hanko (ADR-0053) vem antes do adapter_config no permission request do ACP.

type guardFunc func(req adapter.ToolRequest) adapter.ToolVerdict

func (f guardFunc) Check(_ context.Context, req adapter.ToolRequest) adapter.ToolVerdict {
	return f(req)
}

func TestToolGuardDecidesPermissionRequests(t *testing.T) {
	var mu sync.Mutex
	var seen []adapter.ToolRequest
	g := guardFunc(func(req adapter.ToolRequest) adapter.ToolVerdict {
		mu.Lock()
		seen = append(seen, req)
		mu.Unlock()
		switch req.Name {
		case "edit":
			return adapter.ToolVerdict{Decision: adapter.ToolAllow, Reason: "aprovado no gate"}
		case "read":
			return adapter.ToolVerdict{Decision: adapter.ToolDeny, Reason: "desligada pela política"}
		}
		return adapter.ToolVerdict{} // sem opinião: vale o adapter_config
	})
	spec := fakeSpec(t, "perm", map[string]string{"ACPFAKE_KINDS": "read,edit,execute,none"}, map[string]any{"permission": "read_only"})
	spec.ToolGuard = g
	res, col, err := run(t, spec)
	mustOK(t, res, err)
	// read negada pela política (a read_only liberaria), edit liberada (a read_only negaria), execute e none seguem read_only
	if want := "read=selected/n1 edit=selected/y1 execute=selected/n1 none=selected/n1"; res.Summary != want {
		t.Fatalf("got  %q\nwant %q", res.Summary, want)
	}
	if !col.has(domain.EventSystem, "permissão concedida (política tools): do edit [edit]") ||
		!col.has(domain.EventSystem, "permissão negada (política tools): do read [read]") ||
		!col.has(domain.EventSystem, "permissão negada (política read_only): do execute [execute]") {
		t.Fatalf("eventos de permissão:\n%s", col.dump())
	}
	for _, e := range col.ofKind(domain.EventSystem) {
		if e.Text == "permissão concedida (política tools): do edit [edit]" {
			var p map[string]any
			_ = json.Unmarshal(e.Payload, &p)
			if p["reason"] != "aprovado no gate" || p["policy"] != "tools" || p["allow"] != true {
				t.Fatalf("payload: %v", p)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	// a chamada sem tipo chega ao guarda como "other"; o título segue junto
	if len(seen) != 4 || seen[0].Name != "read" || seen[0].Title != "do read" || seen[3].Name != "other" {
		t.Fatalf("o guarda viu: %+v", seen)
	}
}

func TestToolGuardIgnoredInReviewRuns(t *testing.T) {
	called := false
	spec := fakeSpec(t, "perm", map[string]string{"ACPFAKE_KINDS": "edit"}, map[string]any{"permission": "allow"})
	spec.ReadOnly = true
	spec.ToolGuard = guardFunc(func(adapter.ToolRequest) adapter.ToolVerdict {
		called = true
		return adapter.ToolVerdict{Decision: adapter.ToolAllow}
	})
	res, _, err := run(t, spec)
	mustOK(t, res, err)
	if called || res.Summary != "edit=selected/n1" {
		t.Fatalf("revisão é somente leitura e não consulta o guarda: called=%v %q", called, res.Summary)
	}
}
