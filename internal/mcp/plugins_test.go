package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Estado das tools de plugin por fake (fora da struct para não mexer no fake comum).
type fakePlugins struct {
	tools []service.PluginToolView
	calls []service.PluginToolCall
	fail  error
}

var (
	pluginFakesMu sync.Mutex
	pluginFakes   = map[*fake]*fakePlugins{}
)

func pluginsOf(f *fake) *fakePlugins {
	pluginFakesMu.Lock()
	defer pluginFakesMu.Unlock()
	p := pluginFakes[f]
	if p == nil {
		p = &fakePlugins{}
		pluginFakes[f] = p
	}
	return p
}

func (f *fake) PluginTools(_ context.Context, companyID domain.ID) ([]service.PluginToolView, error) {
	if companyID != f.company.ID {
		return nil, nil
	}
	pluginFakesMu.Lock()
	defer pluginFakesMu.Unlock()
	return append([]service.PluginToolView(nil), pluginsOf0(f).tools...), nil
}

func pluginsOf0(f *fake) *fakePlugins {
	p := pluginFakes[f]
	if p == nil {
		return &fakePlugins{}
	}
	return p
}

func (f *fake) CallPluginTool(_ context.Context, _ domain.Actor, companyID domain.ID, in service.PluginToolCall) (service.PluginToolResult, error) {
	pluginFakesMu.Lock()
	defer pluginFakesMu.Unlock()
	p := pluginsOf0(f)
	p.calls = append(p.calls, in)
	if p.fail != nil {
		return service.PluginToolResult{}, p.fail
	}
	if companyID != f.company.ID {
		return service.PluginToolResult{}, domain.ErrNotFound
	}
	var a struct{ Text string }
	_ = json.Unmarshal(in.Arguments, &a)
	return service.PluginToolResult{Text: "eco: " + a.Text}, nil
}

func toolNames(t *testing.T, e *env, token string) []string {
	t.Helper()
	cs, err := e.connect(t, token)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tl := range res.Tools {
		out = append(out, tl.Name)
	}
	return out
}

func TestPluginToolsOnlyForTheCompanyWithThePlugin(t *testing.T) {
	e := setup(t, true)
	if names := toolNames(t, e, e.tok); strings.Contains(strings.Join(names, ","), "plugin_") {
		t.Fatalf("tools de plugin sem plugin ligado: %v", names)
	}
	fp := pluginsOf(e.f)
	pluginFakesMu.Lock()
	fp.tools = []service.PluginToolView{{Name: "plugin_hello_hello_echo", Description: "[plugin Hello] Ecoa", PluginKey: "hello", Version: "1.0.0",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)}}
	pluginFakesMu.Unlock()

	names := strings.Join(toolNames(t, e, e.tok), ",")
	if !strings.Contains(names, "plugin_hello_hello_echo") || !strings.Contains(names, "hanko_task_get") {
		t.Fatalf("tools com plugin ligado: %s", names)
	}
	// sem token (trusted): não há empresa da run, então nada de tools de plugin
	if names := toolNames(t, e, ""); strings.Contains(strings.Join(names, ","), "plugin_") {
		t.Fatalf("tools de plugin sem token: %v", names)
	}

	cs, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, "plugin_hello_hello_echo", map[string]any{"text": "oi"})
	if res.IsError || text(res) != "eco: oi" {
		t.Fatalf("chamada: %v %s", res.IsError, text(res))
	}
	pluginFakesMu.Lock()
	got := fp.calls[0]
	pluginFakesMu.Unlock()
	if got.PersonaID == nil || *got.PersonaID != e.f.personas[1].ID || got.RunID == nil || *got.RunID != e.runID || !strings.Contains(string(got.Arguments), `"oi"`) {
		t.Fatalf("contexto da run não chegou: %+v", got)
	}
	// erro ou timeout do plugin vira erro da tool
	pluginFakesMu.Lock()
	fp.fail = errors.New("a tool plugin_hello_hello_echo falhou: tempo esgotado")
	pluginFakesMu.Unlock()
	res = call(t, cs, "plugin_hello_hello_echo", map[string]any{"text": "oi"})
	if !res.IsError || !strings.Contains(text(res), "tempo esgotado") {
		t.Fatalf("falha: %v %s", res.IsError, text(res))
	}
	pluginFakesMu.Lock()
	fp.fail = domain.ErrNotFound
	pluginFakesMu.Unlock()
	res = call(t, cs, "plugin_hello_hello_echo", map[string]any{"text": "oi"})
	if !res.IsError || !strings.Contains(text(res), "não está disponível") {
		t.Fatalf("plugin desligado: %v %s", res.IsError, text(res))
	}
}
