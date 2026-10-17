package codex

import (
	"context"
	"slices"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

type recTarget struct {
	args []string
	env  map[string]string
}

func (r *recTarget) Prepare(_ context.Context, _ string, args []string, env map[string]string) (procrun.Invocation, error) {
	r.args, r.env = args, env
	return procrun.Invocation{Bin: "true"}, nil
}
func (r *recTarget) Abort(context.Context)          {}
func (r *recTarget) WorkDir() string                { return "/remoto/work" }
func (r *recTarget) AuxDir() (local, remote string) { return "/tmp", "/remoto/.hanko" }
func (r *recTarget) Describe() string               { return "teste" }

func TestRemoteRunUsesTargetDir(t *testing.T) {
	tg := &recTarget{}
	spec := adapter.Spec{RunID: domain.NewID(), Agent: "codex", Prompt: "oi", Dir: "/local/ws", Exec: tg,
		MCPConfig: []byte(`{"mcpServers":{"hanko":{"type":"http","url":"https://h/mcp","headers":{"Authorization":"Bearer tok"}}}}`)}
	_, _ = New(Options{Bin: "codex"}).Run(context.Background(), spec, adapter.SinkFunc(func(context.Context, adapter.Event) error { return nil }))
	i := slices.Index(tg.args, "-C")
	if i < 0 || tg.args[i+1] != "/remoto/work" {
		t.Fatalf("-C deveria ser o diretório do alvo: %v", tg.args)
	}
	if tg.env["HANKO_MCP_TOKEN_HANKO"] != "tok" || tg.env["PATH"] != "" {
		t.Fatalf("variáveis do alvo: %v", tg.env)
	}
}
