package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// fakeRuntime é um PluginRuntime sem processo: responde às chamadas do host com o que o teste programou.
type fakeRuntime struct {
	mu    sync.Mutex
	calls []fakeCall
	reply func(method string, params map[string]any) (any, error)
}

type fakeCall struct {
	Method string
	Params map[string]any
}

func (f *fakeRuntime) Start(context.Context, plugin.Spec)   {}
func (f *fakeRuntime) Restart(context.Context, plugin.Spec) {}
func (f *fakeRuntime) Stop(context.Context, string) error   { return nil }
func (f *fakeRuntime) Ready(context.Context, string) error  { return nil }
func (f *fakeRuntime) Running() []string                    { return nil }
func (f *fakeRuntime) Status(string) plugin.Status          { return plugin.Status{Running: true, Ready: true} }
func (f *fakeRuntime) Call(_ context.Context, _, method string, params any, _ time.Duration) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{method, p})
	reply := f.reply
	f.mu.Unlock()
	if reply == nil {
		return json.RawMessage(`{}`), nil
	}
	res, err := reply(method, p)
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}

func (f *fakeRuntime) last(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == method {
			return f.calls[i].Params
		}
	}
	return nil
}

const envPluginManifest = `{
  "id": "nuvem-x", "api_version": 1, "version": "1.0.0", "name": "Nuvem X", "command": "nuvem-x",
  "capabilities": ["environments.provider"],
  "environment_providers": [{"key": "principal", "title": "Nuvem X", "known_hosts": "gw.nuvem.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5"}]
}`

func newFakePlugEnv(t *testing.T) (*env, *fakeRuntime, string) {
	t.Helper()
	pool := testdb.New(t)
	jobs := &pluginJobs{}
	svc := service.New(pool, jobs, events.NewBroker())
	box, err := secretbox.New(pluginTestKey, "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Integrations = service.IntegrationsConfig{Secrets: box}
	root := t.TempDir()
	rt := &fakeRuntime{}
	svc.Plugins = service.PluginsConfig{Dir: root, DataDir: t.TempDir(), Runtime: rt}
	svc.Environments = execenv.Config{CloudPlugins: svc.PluginCloudDriver}
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme Corp"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "nuvem-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, domain.ManifestFile), []byte(envPluginManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nuvem-x"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // executável de teste
		t.Fatal(err)
	}
	return &env{svc: svc, jobs: &jobs.fakeJobs, co: co}, rt, root
}

func TestPluginEnvironmentProvider(t *testing.T) {
	e, rt, _ := newFakePlugEnv(t)
	ctx := context.Background()
	if _, err := e.svc.InstallPlugin(ctx, "nuvem-x"); err != nil {
		t.Fatal(err)
	}
	cloud := &domain.CloudConfig{Provider: "plugin:nuvem-x/principal"}
	in := service.CreateEnvironmentInput{Name: "Nuvem X", Driver: domain.EnvCloud, Cloud: cloud, APIKey: "chave-secreta-do-provider"}

	// plugin instalado mas não ligado na empresa: o ambiente não pode usar o provider
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("plugin não ligado = %v", err)
	}
	on := true
	if _, err := e.svc.UpdateCompanyPlugin(ctx, domain.HumanActor, e.co.ID, "nuvem-x", service.UpdateCompanyPluginInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	ev, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in)
	if err != nil || ev.Config.Cloud == nil || ev.Config.Cloud.Provider != "plugin:nuvem-x/principal" {
		t.Fatalf("criar = %+v, %v", ev, err)
	}
	// provider que o plugin não declara
	bad := in
	bad.Name, bad.Cloud = "Outro", &domain.CloudConfig{Provider: "plugin:nuvem-x/outro"}
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("provider não declarado = %v", err)
	}
	// daytona sem o driver do operador continua recusado
	bad.Cloud = &domain.CloudConfig{Provider: "daytona"}
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("daytona sem driver = %v", err)
	}

	// o driver resolvido fala com o plugin pelos métodos env.*, com a chave do ambiente
	rt.reply = func(method string, p map[string]any) (any, error) {
		switch method {
		case "env.create":
			return map[string]any{"id": "box-9"}, nil
		case "env.ssh":
			return map[string]any{"host": "gw.nuvem.example", "port": 2222, "user": "tok_XYZ"}, nil
		case "env.ping", "env.delete":
			return map[string]any{"ok": true}, nil
		}
		return nil, errors.New("método inesperado " + method)
	}
	drv, err := e.svc.PluginCloudDriver("plugin:nuvem-x/principal")
	if err != nil || drv.Name() != "plugin:nuvem-x/principal" || !strings.Contains(drv.KnownHosts(), "gw.nuvem.example") {
		t.Fatalf("driver = %v, %v", drv, err)
	}
	prov := drv.New("chave-secreta-do-provider")
	if err := prov.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	box, err := prov.Create(ctx, execenv.CloudSpec{Name: "hanko-run-1", CPUs: 2, MemoryGiB: 4, DiskGiB: 10, IdleStop: 15 * time.Minute, Reap: time.Hour,
		Labels: map[string]string{"hanko.run": "1"}})
	if err != nil || box.ID != "box-9" {
		t.Fatalf("create = %+v, %v", box, err)
	}
	if p := rt.last("env.create"); p["api_key"] != "chave-secreta-do-provider" || p["provider"] != "principal" {
		t.Fatalf("params de env.create = %v", p)
	}
	spec, _ := rt.last("env.create")["spec"].(map[string]any)
	if spec["cpus"] != float64(2) || spec["idle_stop_sec"] != float64(900) || spec["reap_sec"] != float64(3600) {
		t.Fatalf("spec = %v", spec)
	}
	acc, err := prov.SSH(ctx, "box-9", time.Hour)
	if err != nil || acc.Host != "gw.nuvem.example" || acc.Port != 2222 || acc.User != "tok_XYZ" {
		t.Fatalf("ssh = %+v, %v", acc, err)
	}
	if err := prov.Delete(ctx, "box-9"); err != nil {
		t.Fatal(err)
	}

	// respostas inválidas do plugin viram erro, e a chave de API nunca sai numa mensagem de erro
	rt.reply = func(method string, _ map[string]any) (any, error) {
		if method == "env.create" {
			return map[string]any{"id": ""}, nil
		}
		return nil, errors.New("falhou com a chave chave-secreta-do-provider no texto")
	}
	if _, err := prov.Create(ctx, execenv.CloudSpec{}); err == nil {
		t.Fatal("id vazio deve falhar")
	}
	if err := prov.Ping(ctx); err == nil || strings.Contains(err.Error(), "chave-secreta-do-provider") {
		t.Fatalf("a chave vazou no erro: %v", err)
	}

	// revogar environments.provider corta o uso: o resolvedor recusa, o provider em uso falha e o ambiente novo é recusado
	if _, err := e.svc.SetPluginCapability(ctx, "nuvem-x", domain.CapEnvProvider, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PluginCloudDriver("plugin:nuvem-x/principal"); err == nil || !strings.Contains(err.Error(), domain.CapEnvProvider) {
		t.Fatalf("resolver sem a capacidade = %v", err)
	}
	if err := prov.Ping(ctx); err == nil || !strings.Contains(err.Error(), domain.CapEnvProvider) {
		t.Fatalf("provider em uso sem a capacidade = %v", err)
	}
	in.Name = "Terceiro"
	if _, err := e.svc.CreateEnvironment(ctx, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("criar sem a capacidade = %v", err)
	}
	if _, err := e.svc.PluginCloudDriver("plugin:../x"); err == nil {
		t.Fatal("nome inválido deve falhar")
	}
}

func TestPluginManifestM18Validation(t *testing.T) {
	e, _, root := newFakePlugEnv(t)
	ctx := context.Background()
	write := func(manifest string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "nuvem-x", domain.ManifestFile), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// o manifesto que declara provider sem a capacidade é recusado na instalação
	write(strings.Replace(envPluginManifest, `"capabilities": ["environments.provider"],`, `"capabilities": [],`, 1))
	if _, err := e.svc.InstallPlugin(ctx, "nuvem-x"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("provider sem capacidade = %v", err)
	}
	write(strings.Replace(envPluginManifest, "gw.nuvem.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5", "* ssh-ed25519 AAAAC3NzaC1lZDI1NTE5", 1))
	if _, err := e.svc.InstallPlugin(ctx, "nuvem-x"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("known_hosts com coringa = %v", err)
	}
	write(envPluginManifest)
	if _, err := e.svc.InstallPlugin(ctx, "nuvem-x"); err != nil {
		t.Fatal(err)
	}
}
