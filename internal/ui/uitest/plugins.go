package uitest

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/service"
)

// PluginsFake é o Fake com os métodos opcionais de plugins (ADR-0038). O Fake puro continua sem eles, e a
// tela responde como "indisponível" nos testes que o usam.
type PluginsFake struct {
	*Fake
	pmu sync.Mutex
	// On liga a funcionalidade (HANKO_PLUGINS_DIR); Owner diz se quem chama é o dono da instalação.
	On, Owner bool
	Installed []service.PluginView
	Available []service.AvailablePlugin
	Settings  map[string]*PluginSetting
	Widgets   []service.PluginWidgetView
	Logs      []domain.PluginLog
	// Actions registra as operações (instalação e empresa), na ordem.
	Actions []string
	bridge  []string
	links   []string
}

// PluginSetting é o estado do plugin na empresa (os segredos ficam em Secrets e nunca saem nas views).
type PluginSetting struct {
	Enabled bool
	Config  map[string]any
	Secrets map[string]string
}

// HelloManifest é o manifesto do plugin de exemplo usado nas telas de teste.
func HelloManifest() domain.PluginManifest {
	m := domain.PluginManifest{ID: "hello", APIVersion: 1, Version: "1.0.0", Name: "Hello", Description: "Plugin de exemplo.",
		Author: "Equipe", Homepage: "https://example.com", Command: "hello",
		Capabilities: []string{domain.CapTasksRead, domain.CapTasksWrite, domain.CapAgentTools, domain.CapWidgets, domain.CapState},
		ConfigSchema: domain.PluginConfigSchema{Type: "object", Required: []string{"greeting"}, Properties: map[string]domain.PluginConfigField{
			"greeting":  {Type: domain.ConfigString, Title: "Saudação", Default: "Olá"},
			"mode":      {Type: domain.ConfigString, Title: "Modo", Enum: []string{"curto", "longo"}},
			"loud":      {Type: domain.ConfigBoolean, Title: "Em voz alta"},
			"api_token": {Type: domain.ConfigString, Title: "Token", Secret: true},
		}},
		Tools:   []domain.PluginTool{{Name: "hello_echo", Description: "Ecoa o texto.", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Widgets: []domain.PluginWidget{{Key: "resumo", Title: "Resumo", Placement: domain.WidgetDashboard}},
	}
	return m
}

// NewPluginsFake devolve o Fake de amostra com o plugin hello instalado e um diretório disponível.
func NewPluginsFake() *PluginsFake {
	m := HelloManifest()
	now := Now()
	started := now.Add(-90 * 60e9)
	f := &PluginsFake{Fake: Sample(), On: true, Owner: true, Settings: map[string]*PluginSetting{}}
	f.Installed = []service.PluginView{{Plugin: domain.Plugin{ID: ID("plugin:hello"), Key: "hello", Version: m.Version, APIVersion: 1, Manifest: m,
		Dir: "/srv/plugins/hello", Status: domain.PluginReady, GrantedCapabilities: m.Capabilities, PendingCapabilities: []string{},
		InstalledAt: now, UpdatedAt: now}, Companies: 1, Process: plugin.Status{Running: true, Ready: true, PID: 4242, StartedAt: &started, Restarts: 1}}}
	f.Settings["hello"] = &PluginSetting{Enabled: true, Config: map[string]any{"greeting": "Oi"}, Secrets: map[string]string{"api_token": "SEGREDO-UI"}} //nolint:gosec // valor fictício de teste
	other := domain.PluginManifest{ID: "relatorios", APIVersion: 1, Version: "0.2.0", Name: "Relatórios", Description: "Resumo semanal.",
		Command: "bin/rel", Capabilities: []string{domain.CapTasksRead, domain.CapHTTPOutbound}}
	f.Available = []service.AvailablePlugin{{Key: "hello", Dir: "/srv/plugins/hello", Manifest: &m, Installed: "1.0.0"},
		{Key: "relatorios", Dir: "/srv/plugins/relatorios", Manifest: &other}, {Key: "quebrado", Dir: "/srv/plugins/quebrado", Error: "sem hanko-plugin.json"}}
	f.Widgets = []service.PluginWidgetView{{PluginKey: "hello", PluginName: "Hello", Key: "resumo", Title: "Resumo",
		Markdown: "**3** eventos <script>alert(1)</script>", Table: &service.PluginTable{Columns: []string{"Job", "Quando"}, Rows: [][]string{{"tick", "agora"}}}}}
	f.Logs = []domain.PluginLog{{ID: ID("log:1"), Level: domain.PluginLogHost, Message: "processo pronto", CreatedAt: now}}
	return f
}

func (f *PluginsFake) role(ctx context.Context, min domain.MemberRole) error {
	if r, _ := f.MyRole(ctx, uuid.Nil); !r.AtLeast(min) {
		return domain.ErrForbidden
	}
	return nil
}

func (f *PluginsFake) owner() error {
	if !f.Owner {
		return domain.ErrForbidden
	}
	if !f.On {
		return domain.Invalid("plugins", "plugins estão desligados neste servidor (defina HANKO_PLUGINS_DIR)")
	}
	return nil
}

func (f *PluginsFake) find(ref string) (*service.PluginView, error) {
	for i := range f.Installed {
		if f.Installed[i].Key == ref && f.Installed[i].Status != domain.PluginUninstalled {
			return &f.Installed[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *PluginsFake) PluginsEnabled() bool { return f.On }

func (f *PluginsFake) PluginOverview(_ context.Context) (service.PluginOverview, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if !f.Owner {
		return service.PluginOverview{}, domain.ErrForbidden
	}
	ov := service.PluginOverview{Enabled: f.On, Available: []service.AvailablePlugin{}, Installed: []service.PluginView{}}
	if !f.On {
		return ov, nil
	}
	ov.Dir = "/srv/plugins"
	ov.Available = append(ov.Available, f.Available...)
	for _, p := range f.Installed {
		if p.Status != domain.PluginUninstalled {
			ov.Installed = append(ov.Installed, p)
		}
	}
	return ov, nil
}

func (f *PluginsFake) GetPlugin(_ context.Context, ref string) (service.PluginView, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if !f.Owner {
		return service.PluginView{}, domain.ErrForbidden
	}
	p, err := f.find(ref)
	if err != nil {
		return service.PluginView{}, err
	}
	return *p, nil
}

func (f *PluginsFake) InstallPlugin(_ context.Context, key string) (service.PluginView, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if err := f.owner(); err != nil {
		return service.PluginView{}, err
	}
	if _, err := f.find(key); err == nil {
		return service.PluginView{}, domain.ErrConflict
	}
	for _, a := range f.Available {
		if a.Key == key && a.Manifest != nil {
			v := service.PluginView{Plugin: domain.Plugin{ID: ID("plugin:" + key), Key: key, Version: a.Manifest.Version, Manifest: *a.Manifest,
				Status: domain.PluginReady, GrantedCapabilities: a.Manifest.Capabilities, PendingCapabilities: []string{}}}
			f.Installed = append(f.Installed, v)
			f.Actions = append(f.Actions, "install "+key)
			return v, nil
		}
	}
	return service.PluginView{}, domain.Invalid("manifest", "sem hanko-plugin.json")
}

func (f *PluginsFake) setStatus(ref string, to domain.PluginStatus, action string) (service.PluginView, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if err := f.owner(); err != nil {
		return service.PluginView{}, err
	}
	p, err := f.find(ref)
	if err != nil {
		return service.PluginView{}, err
	}
	if p.Status != to && !p.Status.CanBecome(to) {
		return service.PluginView{}, domain.ErrConflict
	}
	p.Status = to
	f.Actions = append(f.Actions, action+" "+ref)
	return *p, nil
}

func (f *PluginsFake) SetPluginEnabled(_ context.Context, ref string, on bool) (service.PluginView, error) {
	if on {
		return f.setStatus(ref, domain.PluginReady, "enable")
	}
	return f.setStatus(ref, domain.PluginDisabled, "disable")
}

func (f *PluginsFake) UpgradePlugin(_ context.Context, ref string) (service.PluginView, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if err := f.owner(); err != nil {
		return service.PluginView{}, err
	}
	p, err := f.find(ref)
	if err != nil {
		return service.PluginView{}, err
	}
	m := p.Manifest
	m.Version = "2.0.0"
	m.Capabilities = append(append([]string{}, m.Capabilities...), domain.CapPersonasRead)
	p.PendingManifest, p.PendingCapabilities, p.Status = &m, m.Capabilities, domain.PluginUpgradePending
	f.Actions = append(f.Actions, "upgrade "+ref)
	return *p, nil
}

func (f *PluginsFake) ApprovePluginUpgrade(_ context.Context, ref string) (service.PluginView, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if err := f.owner(); err != nil {
		return service.PluginView{}, err
	}
	p, err := f.find(ref)
	if err != nil {
		return service.PluginView{}, err
	}
	if p.PendingManifest == nil {
		return service.PluginView{}, domain.ErrConflict
	}
	p.Manifest, p.Version, p.GrantedCapabilities = *p.PendingManifest, p.PendingManifest.Version, p.PendingCapabilities
	p.PendingManifest, p.PendingCapabilities, p.Status = nil, []string{}, domain.PluginReady
	f.Actions = append(f.Actions, "approve "+ref)
	return *p, nil
}

func (f *PluginsFake) UninstallPlugin(_ context.Context, ref string) error {
	_, err := f.setStatus(ref, domain.PluginUninstalled, "uninstall")
	f.pmu.Lock()
	delete(f.Settings, ref)
	f.pmu.Unlock()
	return err
}

func (f *PluginsFake) ListPluginLogs(_ context.Context, ref string, _ int) ([]domain.PluginLog, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if !f.Owner {
		return nil, domain.ErrForbidden
	}
	if _, err := f.find(ref); err != nil {
		return nil, err
	}
	return append([]domain.PluginLog(nil), f.Logs...), nil
}

func (f *PluginsFake) companyView(p service.PluginView) service.CompanyPlugin {
	m := p.Manifest
	cp := service.CompanyPlugin{PluginID: p.ID, Key: p.Key, Name: m.Name, Description: m.Description, Author: m.Author, Homepage: m.Homepage,
		Version: p.Version, Status: p.Status, Capabilities: p.GrantedCapabilities, ConfigSchema: m.ConfigSchema, Tools: m.Tools, Events: m.Events,
		Widgets: m.Widgets, Jobs: m.Jobs, Pages: m.Pages, FlowTriggers: m.FlowTriggers, EnvProviders: m.EnvProviders, SkillSources: m.SkillSources,
		Config: map[string]any{}, SecretsSet: []string{}}
	if st := f.Settings[p.Key]; st != nil {
		cp.Enabled = st.Enabled
		for k, v := range st.Config {
			cp.Config[k] = v
		}
		for k := range st.Secrets {
			cp.SecretsSet = append(cp.SecretsSet, k)
		}
		sort.Strings(cp.SecretsSet)
	}
	cp.Active = cp.Enabled && p.Status.Active()
	return cp
}

func (f *PluginsFake) ListCompanyPlugins(ctx context.Context, _ domain.ID) ([]service.CompanyPlugin, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if !f.On {
		return []service.CompanyPlugin{}, nil
	}
	var out []service.CompanyPlugin
	for _, p := range f.Installed {
		if p.Status != domain.PluginUninstalled {
			out = append(out, f.companyView(p))
		}
	}
	return out, nil
}

func (f *PluginsFake) GetCompanyPlugin(_ context.Context, _ domain.ID, ref string) (service.CompanyPlugin, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	p, err := f.find(ref)
	if err != nil {
		return service.CompanyPlugin{}, err
	}
	return f.companyView(*p), nil
}

func (f *PluginsFake) UpdateCompanyPlugin(ctx context.Context, _ domain.Actor, _ domain.ID, ref string, in service.UpdateCompanyPluginInput) (service.CompanyPlugin, error) {
	if err := f.role(ctx, domain.MemberAdmin); err != nil {
		return service.CompanyPlugin{}, err
	}
	f.pmu.Lock()
	defer f.pmu.Unlock()
	p, err := f.find(ref)
	if err != nil {
		return service.CompanyPlugin{}, err
	}
	st := f.Settings[ref]
	if st == nil {
		st = &PluginSetting{Config: map[string]any{}, Secrets: map[string]string{}}
		f.Settings[ref] = st
	}
	for k, v := range in.Config {
		fd, ok := p.Manifest.ConfigSchema.Properties[k]
		if !ok {
			return service.CompanyPlugin{}, domain.Invalid("config."+k, "campo desconhecido")
		}
		if fd.Secret {
			st.Secrets[k] = v.(string)
			continue
		}
		if v == "" || v == nil {
			delete(st.Config, k)
			continue
		}
		cv, err := fd.Coerce(v)
		if err != nil {
			return service.CompanyPlugin{}, domain.Invalid("config."+k, err.Error())
		}
		st.Config[k] = cv
	}
	for _, k := range in.ClearSecrets {
		delete(st.Secrets, k)
	}
	if in.Enabled != nil {
		st.Enabled = *in.Enabled
		f.Actions = append(f.Actions, "company-enabled "+ref)
	}
	f.Actions = append(f.Actions, "company-update "+ref)
	return f.companyView(*p), nil
}

func (f *PluginsFake) ListPluginState(ctx context.Context, _ domain.ID, ref, _ string, _ int) ([]service.PluginStateItem, error) {
	if err := f.role(ctx, domain.MemberAdmin); err != nil {
		return nil, err
	}
	return []service.PluginStateItem{{Key: "events", Value: json.RawMessage(`3`), UpdatedAt: Now()}}, nil
}

func (f *PluginsFake) PluginWidgets(_ context.Context, _ domain.ID, w service.PluginWidgetRef) ([]service.PluginWidgetView, error) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if !domain.ValidWidgetPlacement(w.Placement) {
		return nil, domain.ErrInvalid
	}
	if w.Placement != domain.WidgetDashboard {
		return []service.PluginWidgetView{}, nil
	}
	return append([]service.PluginWidgetView(nil), f.Widgets...), nil
}
