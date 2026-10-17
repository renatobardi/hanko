package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// recTarget é um alvo remoto de teste: guarda o que o adapter pediu e roda "true" localmente.
type recTarget struct {
	aux     string
	bin     string
	args    []string
	env     map[string]string
	auxSeen []string
}

func (r *recTarget) Prepare(_ context.Context, bin string, args []string, env map[string]string) (procrun.Invocation, error) {
	r.bin, r.args, r.env = bin, args, env
	_ = filepath.Walk(r.aux, func(p string, _ os.FileInfo, _ error) error { r.auxSeen = append(r.auxSeen, p); return nil })
	return procrun.Invocation{Bin: "true"}, nil
}
func (r *recTarget) Abort(context.Context)          {}
func (r *recTarget) WorkDir() string                { return "/remoto/work" }
func (r *recTarget) AuxDir() (local, remote string) { return r.aux, "/remoto/.hanko" }
func (r *recTarget) Describe() string               { return "teste" }

func TestRemoteRunUsesTargetPaths(t *testing.T) {
	tg := &recTarget{aux: t.TempDir()}
	spec := adapter.Spec{RunID: domain.NewID(), Agent: "claude", Prompt: "oi", Exec: tg,
		MCPConfig: []byte(`{"mcpServers":{"hanko":{"type":"http","url":"https://h/mcp"}}}`),
		Skills:    []domain.Skill{{Slug: "revisar", Name: "revisar", Body: "x"}},
		Env:       map[string]string{"HANKO_RUN_TOKEN": "tok"}}
	_, _ = New(Options{Bin: "claude"}).Run(context.Background(), spec, adapter.SinkFunc(func(context.Context, adapter.Event) error { return nil }))
	if tg.bin != "claude" {
		t.Fatalf("bin: %q", tg.bin)
	}
	i := slices.Index(tg.args, "--mcp-config")
	if i < 0 || !strings.HasPrefix(tg.args[i+1], "/remoto/.hanko/hanko-mcp-") {
		t.Fatalf("mcp config deveria apontar para o alvo: %v", tg.args)
	}
	j := slices.Index(tg.args, "--add-dir")
	if j < 0 || !strings.HasPrefix(tg.args[j+1], "/remoto/.hanko/hanko-skills-") {
		t.Fatalf("skills deveriam apontar para o alvo: %v", tg.args)
	}
	if tg.env["HANKO_RUN_TOKEN"] != "tok" || tg.env["PATH"] != "" {
		t.Fatalf("no alvo só as variáveis da run: %v", tg.env)
	}
	found := false
	for _, p := range tg.auxSeen {
		if strings.HasSuffix(p, "SKILL.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a skill deveria estar no diretório auxiliar antes do Prepare: %v", tg.auxSeen)
	}
}
