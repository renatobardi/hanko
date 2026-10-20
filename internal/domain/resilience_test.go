package domain

import (
	"encoding/json"
	"testing"
)

func ev(seq int64, kind RunEventKind, text, payload string) RunEvent {
	e := RunEvent{Seq: seq, Kind: kind, Text: text}
	if payload != "" {
		e.Payload = json.RawMessage(payload)
	}
	return e
}

func TestSteerModeFor(t *testing.T) {
	for agent, want := range map[string]SteerMode{
		"claude": SteerInject, "acp": SteerInject, "fake": SteerInject, " Claude ": SteerInject,
		"codex": SteerRestart, "": SteerRestart, "outro": SteerRestart,
	} {
		if got := SteerModeFor(agent); got != want {
			t.Errorf("SteerModeFor(%q) = %s, want %s", agent, got, want)
		}
	}
}

func TestToolHasExternalEffect(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		want       bool
	}{
		{"Read", "", false}, {"Grep", "", false}, {"WebSearch", "", false}, {"read", "", false},
		{"Bash", "", true}, {"Write", "", true}, {"Edit", "", true}, {"mcp__github__create_pull_request", "", true},
		{"mcp__hanko__hanko_task_get", "", false}, {"mcp__hanko__hanko_task_list", "", false},
		{"hanko_task_comment", "", true}, {"hanko_task_update", "", true}, {"hanko_whoami", "", false},
		{"Read: /x/y", "", false}, {"run tests", "execute", true}, {"run tests", "read", false},
		{"qualquer", "search", false}, {"", "", true},
	} {
		if got := ToolHasExternalEffect(tc.name, tc.kind); got != tc.want {
			t.Errorf("ToolHasExternalEffect(%q, %q) = %v, want %v", tc.name, tc.kind, got, tc.want)
		}
	}
}

func TestOpenToolCalls(t *testing.T) {
	t.Run("casa por id e deixa a aberta", func(t *testing.T) {
		evs := []RunEvent{
			ev(1, EventToolUse, "Read: a", `{"id":"t1","name":"Read"}`),
			ev(2, EventToolUse, "Bash: git push", `{"id":"t2","name":"Bash"}`),
			ev(3, EventToolResult, "ok", `{"tool_use_id":"t1"}`),
		}
		open := OpenToolCalls(evs)
		if len(open) != 1 || open[0].Name != "Bash" || open[0].Seq != 2 {
			t.Fatalf("open = %+v", open)
		}
		u := UncertainOf(open)
		if u == nil || u.Name != "Bash" || u.Input != "Bash: git push" {
			t.Fatalf("uncertain = %+v", u)
		}
	})
	t.Run("sem id casa por ordem", func(t *testing.T) {
		evs := []RunEvent{
			ev(1, EventToolUse, "Write: x", ""),
			ev(2, EventToolResult, "ok", ""),
			ev(3, EventToolUse, "Edit: y", ""),
		}
		open := OpenToolCalls(evs)
		if len(open) != 1 || open[0].Name != "Edit" {
			t.Fatalf("open = %+v", open)
		}
	})
	t.Run("tudo fechado", func(t *testing.T) {
		evs := []RunEvent{ev(1, EventToolUse, "Bash: ls", `{"id":"a"}`), ev(2, EventToolResult, "x", `{"tool_use_id":"a"}`)}
		if open := OpenToolCalls(evs); len(open) != 0 || UncertainOf(open) != nil {
			t.Fatalf("open = %+v", open)
		}
	})
	t.Run("aberta só de leitura não é incerta", func(t *testing.T) {
		open := OpenToolCalls([]RunEvent{ev(1, EventToolUse, "Grep: foo", `{"id":"g","name":"Grep"}`)})
		if len(open) != 1 || UncertainOf(open) != nil {
			t.Fatalf("open = %+v", open)
		}
	})
	t.Run("acp: kind do payload", func(t *testing.T) {
		open := OpenToolCalls([]RunEvent{
			ev(1, EventToolUse, "Listing files", `{"tool_call_id":"c1","kind":"read"}`),
			ev(2, EventToolUse, "Rodando migração", `{"tool_call_id":"c2","kind":"execute"}`),
		})
		u := UncertainOf(open)
		if len(open) != 2 || u == nil || u.Seq != 2 {
			t.Fatalf("open = %+v uncertain = %+v", open, u)
		}
	})
}

func TestContinuationReasonResumesSession(t *testing.T) {
	for r, want := range map[ContinuationReason]bool{ContinueRecovery: true, ContinueSteer: true, ContinueManual: true, ContinueRetry: false, ContinueNone: false} {
		if got := r.ResumesSession(); got != want {
			t.Errorf("%q.ResumesSession() = %v, want %v", r, got, want)
		}
	}
}
