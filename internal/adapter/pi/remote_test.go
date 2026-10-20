package pi

import (
	"context"
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

// Num ambiente remoto (ADR-0037) só as variáveis da run vão para o alvo; o ambiente do servidor nunca.
func TestRemoteRunSendsOnlyRunEnv(t *testing.T) {
	t.Setenv("HANKO_SECRET", "server-secret")
	tg := &recTarget{}
	spec := adapter.Spec{RunID: domain.NewID(), CompanyID: domain.NewID(), Agent: "pi", Prompt: "oi", Exec: tg,
		Env: map[string]string{"HANKO_RUN_TOKEN": "tok", "HANKO_URL": "https://h"}}
	_, _ = New("pi", Options{}).Run(context.Background(), spec, adapter.SinkFunc(func(context.Context, adapter.Event) error { return nil }))
	if tg.env["HANKO_TOKEN"] != "tok" || tg.env["HANKO_URL"] != "https://h" || tg.env["HANKO_SECRET"] != "" || tg.env["PATH"] != "" {
		t.Fatalf("variáveis do alvo: %v", tg.env)
	}
	if len(tg.args) < 3 || tg.args[0] != "--mode" {
		t.Fatalf("argv: %v", tg.args)
	}
}
