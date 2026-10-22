package execenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// Provider de ambiente vindo de plugin (ADR-0045): o driver é resolvido por CloudPlugins a cada uso.

type pluginDriver struct {
	name string
	prov *pluginProv
}

func (d *pluginDriver) Name() string                 { return d.name }
func (d *pluginDriver) KnownHosts() string           { return "gw.example.com ssh-ed25519 AAAAC3Nza" }
func (d *pluginDriver) New(key string) CloudProvider { d.prov.key = key; return d.prov }

type pluginProv struct {
	mu      sync.Mutex
	key     string
	created []CloudSpec
	deleted []string
}

func (p *pluginProv) Ping(context.Context) error { return nil }
func (p *pluginProv) Create(_ context.Context, s CloudSpec) (CloudSandbox, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created = append(p.created, s)
	return CloudSandbox{ID: "box-1"}, nil
}
func (p *pluginProv) SSH(context.Context, string, time.Duration) (CloudSSH, error) {
	return CloudSSH{Host: "gw.example.com", Port: 22, User: "tok_ABC"}, nil
}
func (p *pluginProv) Delete(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleted = append(p.deleted, id)
	return nil
}

func TestCloudPluginProviderSession(t *testing.T) {
	prov := &pluginProv{}
	const name = "plugin:nuvem-x/principal"
	cfg := Config{SSHBin: fakeSSH(t), LookPath: func(s string) (string, error) { return s, nil },
		CloudPlugins: func(p string) (CloudDriver, error) {
			if p != name {
				return nil, errors.New("plugin desconhecido")
			}
			return &pluginDriver{name: name, prov: prov}, nil
		}}
	if err := cfg.DriverStatus(domain.EnvCloud); err != nil {
		t.Fatalf("só providers de plugin já liga o driver cloud: %v", err)
	}
	tg := Target{Driver: domain.EnvCloud, Name: "nuvem", APIKey: "chave-do-plugin", Cloud: domain.CloudConfig{Provider: name, Network: "full"}}
	tg.Cloud.Root = filepath.Join(t.TempDir(), "remote")
	local := localWorkspace(t)
	sess, err := Open(cfg, tg, RunInfo{RunID: "0190plug", Company: "acme", Workspace: "web", LocalDir: local})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	pr, err := procrun.Run(context.Background(), procrun.Options{Bin: "sh", Args: []string{"-c", `printf 'ok' > feito.txt`}, Target: sess})
	if err != nil || pr.ExitCode != 0 {
		t.Fatalf("run: %+v %v", pr, err)
	}
	if err := sess.Finish(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(local, "feito.txt")); err != nil || string(b) != "ok" {
		t.Fatalf("o trabalho deve voltar: %q %v", b, err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if prov.key != "chave-do-plugin" || len(prov.created) != 1 || len(prov.deleted) != 1 || prov.deleted[0] != "box-1" {
		t.Fatalf("provider = %+v", prov)
	}
	if !strings.HasPrefix(prov.created[0].Name, "hanko-run-") || prov.created[0].IdleStop == 0 || prov.created[0].Reap == 0 {
		t.Fatalf("pedido sem rede de segurança: %+v", prov.created[0])
	}
}

func TestCloudPluginProviderFailures(t *testing.T) {
	tg := Target{Driver: domain.EnvCloud, Name: "nuvem", APIKey: "k", Cloud: domain.CloudConfig{Provider: "plugin:nuvem-x/principal", Network: "full"}}
	run := RunInfo{RunID: "r", Company: "c"}
	// sem resolvedor (plugins desligados) e sem driver do operador: o driver cloud está desligado
	if _, err := Open(Config{}, tg, run); !errors.Is(err, ErrCloudOff) {
		t.Fatalf("sem nada = %v", err)
	}
	// só o driver do operador: o provider de plugin não é resolvido
	d, err := NewDaytona(DaytonaOptions{KnownHosts: "gw ssh-ed25519 AAAA"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Cloud: d}, tg, run); err == nil || !strings.Contains(err.Error(), "providers de plugin estão desligados") {
		t.Fatalf("sem resolvedor = %v", err)
	}
	// resolvedor que recusa (plugin sem a capacidade, por exemplo)
	cfg := Config{CloudPlugins: func(string) (CloudDriver, error) { return nil, errors.New("o plugin não tem a capacidade") }}
	if _, err := Open(cfg, tg, run); err == nil || !strings.Contains(err.Error(), "capacidade") {
		t.Fatalf("resolvedor recusa = %v", err)
	}
	// o nome devolvido pelo driver precisa ser o do ambiente
	cfg = Config{CloudPlugins: func(string) (CloudDriver, error) {
		return &pluginDriver{name: "plugin:outro/x", prov: &pluginProv{}}, nil
	}}
	if _, err := Open(cfg, tg, run); err == nil {
		t.Fatal("driver de outro provider deve falhar")
	}
	// daytona sem driver do operador, mesmo com plugins ligados
	tg.Cloud.Provider = "daytona"
	cfg = Config{CloudPlugins: func(string) (CloudDriver, error) { return nil, errors.New("x") }}
	if _, err := Open(cfg, tg, run); !errors.Is(err, ErrCloudOff) {
		t.Fatalf("daytona sem driver = %v", err)
	}
}

func TestCloudConfigNormalizeAcceptsPluginProvider(t *testing.T) {
	for p, ok := range map[string]bool{"plugin:nuvem-x/principal": true, "Plugin:nuvem-x/principal": true, "plugin:x": false, "plugin:/a": false,
		"plugin:A/b": false, "plugin:nuvem-x/B c": false, "e2b": false, "": true, "daytona": true} {
		c := domain.CloudConfig{Provider: p}
		if err := c.Normalize(); (err == nil) != ok {
			t.Errorf("provider %q: erro = %v, esperado ok=%v", p, err, ok)
		}
	}
}
