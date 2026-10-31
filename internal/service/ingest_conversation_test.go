package service

import (
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
)

func str(s string) *string { return &s }

func TestCallRole(t *testing.T) {
	span := func(name, attrs, res string, round *string) ingest.SpanRow {
		return ingest.SpanRow{Name: str(name), Attributes: attrs, ResourceAttributes: res, Fixed: ingest.Fixed{OuteSwarmRound: round}}
	}
	for _, c := range []struct {
		name string
		row  ingest.SpanRow
		sess string
		want domain.CallRole
	}{
		{"subagente pelo query_source", span("claude_code.llm_request", `{"query_source_safe":"agent.explore"}`, `{}`, nil), "worker", domain.CallRoleSubagent},
		{"subagente pelo agent_id", span("claude_code.llm_request", `{"agent_id":"a1"}`, `{}`, nil), "", domain.CallRoleSubagent},
		{"agent_id fora do span de chamada não é subagente", span("claude_code.tool", `{"agent_id":"a1"}`, `{}`, nil), "", domain.CallRoleStandalone},
		{"revisor", span("claude_code.llm_request", `{}`, `{"oute.swarm.step":"triagem"}`, str("swarm-1")), "worker", domain.CallRoleReviewer},
		{"com sessão de worker no resource não é revisor", span("claude_code.llm_request", `{}`, `{"oute.swarm.step":"x","oute.swarm.session":"s"}`, str("swarm-1")), "", domain.CallRoleWorker},
		{"papel da abertura da sessão", span("session_task.turn", `{}`, `{}`, nil), "dispatcher", domain.CallRoleDispatcher},
		{"sem abertura: worker pelo resource", span("session_task.turn", `{}`, `{"oute.swarm.session":"s"}`, nil), "", domain.CallRoleWorker},
		{"sem abertura: dispatcher pela rodada", span("session_task.turn", `{}`, `{}`, str("swarm-1")), "", domain.CallRoleDispatcher},
		{"sem nada: standalone", span("session_task.turn", `{}`, `{}`, nil), "", domain.CallRoleStandalone},
	} {
		if got := callRole(c.row, c.sess); got != c.want {
			t.Errorf("%s: %q, quer %q", c.name, got, c.want)
		}
	}
}
