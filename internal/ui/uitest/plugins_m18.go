package uitest

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Métodos do Fake de plugins para o M18 (ADR-0045): abas, ponte, capacidades e gatilhos de fluxo.

// ShowcaseManifest é o manifesto do plugin de exemplo do SDK, com as contribuições do M18.
func ShowcaseManifest() domain.PluginManifest {
	return domain.PluginManifest{ID: "showcase", APIVersion: 1, Version: "1.0.0", Name: "Showcase", Description: "Aba, gatilho, provider e fonte de skills.",
		Command:      "showcase",
		Capabilities: []string{domain.CapPages, domain.CapFlowTriggers, domain.CapEnvProvider, domain.CapSkillSource},
		Pages:        []domain.PluginPage{{Key: "painel", Title: "Painel Showcase"}},
		FlowTriggers: []domain.PluginFlowTrigger{{Key: "novo-pedido", Title: "Novo pedido", Description: "Disparado pela aba."}},
		EnvProviders: []domain.PluginEnvProvider{{Key: "principal", Title: "Nuvem X", KnownHosts: "gw.example ssh-ed25519 AAAA"}},
		SkillSources: []domain.PluginSkillSource{{Key: "catalogo", Title: "Catálogo"}},
	}
}

// WithShowcase instala o plugin showcase (ligado na empresa) no Fake.
func (f *PluginsFake) WithShowcase() *PluginsFake {
	m := ShowcaseManifest()
	now := Now()
	f.Installed = append(f.Installed, service.PluginView{Plugin: domain.Plugin{ID: ID("plugin:showcase"), Key: "showcase", Version: m.Version, APIVersion: 1,
		Manifest: m, Dir: "/srv/plugins/showcase", Status: domain.PluginReady, GrantedCapabilities: m.Capabilities, PendingCapabilities: []string{},
		InstalledAt: now, UpdatedAt: now}, Companies: 1})
	f.Settings["showcase"] = &PluginSetting{Enabled: true, Config: map[string]any{}, Secrets: map[string]string{}}
	return f
}

// BridgeCalls registra as chamadas da ponte (método), na ordem.
func (f *PluginsFake) BridgeCalls() []string {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	return append([]string(nil), f.bridge...)
}

// Links devolve as ligações de gatilho ("plugin/gatilho/fluxo").
func (f *PluginsFake) Links() []string {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	return append([]string(nil), f.links...)
}

func (f *PluginsFake) SetPluginCapability(_ context.Context, ref, capability string, granted bool) (service.PluginView, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if err := f.owner(); err != nil {
		return service.PluginView{}, err
	}
	p, err := f.find(ref)
	if err != nil {
		return service.PluginView{}, err
	}
	if !domain.ValidPluginCapability(capability) {
		return service.PluginView{}, domain.Invalid("capability", "capacidade desconhecida")
	}
	var next []string
	for _, c := range p.GrantedCapabilities {
		if c != capability {
			next = append(next, c)
		}
	}
	verb := "revoke "
	if granted {
		next = append(next, capability)
		verb = "grant "
	}
	p.GrantedCapabilities = domain.SortCapabilities(next)
	f.Actions = append(f.Actions, verb+ref+" "+capability)
	return *p, nil
}

func (f *PluginsFake) tabs() []service.PluginTabView {
	var out []service.PluginTabView
	for _, p := range f.Installed {
		st := f.Settings[p.Key]
		if !p.Status.Active() || !p.Has(domain.CapPages) || st == nil || !st.Enabled {
			continue
		}
		for _, pg := range p.Manifest.Pages {
			out = append(out, service.PluginTabView{PluginKey: p.Key, PluginName: p.Manifest.Name, Key: pg.Key, Title: pg.Title, Icon: pg.Icon})
		}
	}
	return out
}

func (f *PluginsFake) PluginTabs(_ context.Context, _ domain.ID) ([]service.PluginTabView, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	return f.tabs(), nil
}

// PageHTML é o HTML que o plugin devolve na página.
const PageHTML = `<h1 id="ola">Olá do plugin</h1><script>window.hanko.call("registrar", {})</script>`

func (f *PluginsFake) PluginPageHTML(_ context.Context, _ domain.ID, pluginKey, pageKey string) (string, string, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	for _, t := range f.tabs() {
		if t.PluginKey == pluginKey && t.Key == pageKey {
			return t.Title, PageHTML, nil
		}
	}
	return "", "", domain.ErrNotFound
}

func (f *PluginsFake) PluginPageCall(_ context.Context, _ domain.ID, pluginKey, pageKey, method string, _ json.RawMessage) (json.RawMessage, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	found := false
	for _, t := range f.tabs() {
		found = found || (t.PluginKey == pluginKey && t.Key == pageKey)
	}
	if !found {
		return nil, domain.ErrNotFound
	}
	if method == "quebra" {
		return nil, domain.Invalid("plugin", "ponte: recusado pelo plugin")
	}
	f.bridge = append(f.bridge, method)
	return json.RawMessage(`{"total":1}`), nil
}

func (f *PluginsFake) PluginFlowTriggers(_ context.Context, _ domain.ID, ref string) ([]domain.PluginFlowTrigger, []service.PluginFlowLink, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	p, err := f.find(ref)
	if err != nil {
		return nil, nil, err
	}
	var links []service.PluginFlowLink
	for _, l := range f.links {
		parts := strings.SplitN(l, "/", 3)
		if parts[0] == ref {
			links = append(links, service.PluginFlowLink{Trigger: parts[1], FlowSlug: parts[2], FlowName: "Fluxo " + parts[2]})
		}
	}
	return p.Manifest.FlowTriggers, links, nil
}

func (f *PluginsFake) LinkPluginFlowTrigger(ctx context.Context, _ domain.Actor, _ domain.ID, ref, trigger, flowRef string, link bool) error {
	if err := f.role(ctx, domain.MemberAdmin); err != nil {
		return err
	}
	f.pmu.Lock()
	defer f.pmu.Unlock()
	key := ref + "/" + trigger + "/" + flowRef
	if link {
		f.links = append(f.links, key)
		return nil
	}
	for i, l := range f.links {
		if l == key {
			f.links = append(f.links[:i], f.links[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}
