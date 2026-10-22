package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/service"
)

// Plugins de terceiros (ADR-0038). /plugins é da instalação (dono da instalação: disponíveis, instalar,
// atualizar, aprovar capacidades, desligar, desinstalar, logs e saúde); /companies/{c}/plugins é da empresa
// (qualquer membro lê; admin liga e configura). Run tokens não usam nenhuma rota de escrita nem o estado.
// Valores de campos secretos nunca voltam: só os nomes dos definidos.

type pluginsBackend interface {
	PluginOverview(ctx context.Context) (service.PluginOverview, error)
	GetPlugin(ctx context.Context, ref string) (service.PluginView, error)
	InstallPlugin(ctx context.Context, key string) (service.PluginView, error)
	SetPluginEnabled(ctx context.Context, ref string, on bool) (service.PluginView, error)
	SetPluginCapability(ctx context.Context, ref, capability string, granted bool) (service.PluginView, error)
	PluginTabs(ctx context.Context, companyID domain.ID) ([]service.PluginTabView, error)
	PluginPageCall(ctx context.Context, companyID domain.ID, pluginKey, pageKey, method string, params json.RawMessage) (json.RawMessage, error)
	PluginFlowTriggers(ctx context.Context, companyID domain.ID, ref string) ([]domain.PluginFlowTrigger, []service.PluginFlowLink, error)
	LinkPluginFlowTrigger(ctx context.Context, actor domain.Actor, companyID domain.ID, ref, trigger, flowRef string, link bool) error
	PluginSkillSources(ctx context.Context, companyID domain.ID) ([]service.PluginSkillSourceView, error)
	UpgradePlugin(ctx context.Context, ref string) (service.PluginView, error)
	ApprovePluginUpgrade(ctx context.Context, ref string) (service.PluginView, error)
	UninstallPlugin(ctx context.Context, ref string) error
	ListPluginLogs(ctx context.Context, ref string, limit int) ([]domain.PluginLog, error)
	GetPluginHealth(ctx context.Context, ref string) (service.PluginHealth, error)
	ListCompanyPlugins(ctx context.Context, companyID domain.ID) ([]service.CompanyPlugin, error)
	GetCompanyPlugin(ctx context.Context, companyID domain.ID, ref string) (service.CompanyPlugin, error)
	UpdateCompanyPlugin(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in service.UpdateCompanyPluginInput) (service.CompanyPlugin, error)
	ListPluginState(ctx context.Context, companyID domain.ID, ref, prefix string, limit int) ([]service.PluginStateItem, error)
	PluginWidgets(ctx context.Context, companyID domain.ID, w service.PluginWidgetRef) ([]service.PluginWidgetView, error)
}

func (s *server) plugins() (pluginsBackend, error) {
	b, ok := s.svc.(pluginsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "plugins indisponíveis neste servidor")
	}
	return b, nil
}

// ---------- DTOs ----------

// PluginProcess é o estado do processo do plugin.
type PluginProcess struct {
	Running     bool       `json:"running" doc:"Há processo gerenciado."`
	Ready       bool       `json:"ready" doc:"initialize e health ok."`
	PID         int        `json:"pid,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	Restarts    int        `json:"restarts" doc:"Reinícios desde que o servidor subiu."`
	LastError   string     `json:"last_error,omitempty"`
	NextRestart *time.Time `json:"next_restart,omitempty" doc:"Próxima tentativa, quando o processo caiu."`
}

func processOut(p plugin.Status) PluginProcess {
	return PluginProcess{Running: p.Running, Ready: p.Ready, PID: p.PID, StartedAt: p.StartedAt, Restarts: p.Restarts, LastError: p.LastError, NextRestart: p.NextRestart}
}

// PluginWidgetDef é um widget declarado no manifesto.
type PluginWidgetDef struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Placement string `json:"placement" enum:"dashboard,task,persona"`
}

// PluginJobDef é um job declarado no manifesto.
type PluginJobDef struct {
	Key          string `json:"key"`
	EveryMinutes int    `json:"every_minutes"`
}

// PluginPageDef é uma aba (página em iframe sandboxed) declarada no manifesto.
type PluginPageDef struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

// PluginFlowTriggerDef é um gatilho de fluxo declarado no manifesto.
type PluginFlowTriggerDef struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// PluginEnvProviderDef é um provider de ambiente declarado no manifesto.
type PluginEnvProviderDef struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Provider string `json:"provider" doc:"Nome a usar em cloud.provider do ambiente (plugin:<id>/<key>)."`
}

// PluginSkillSourceDef é uma fonte de skills declarada no manifesto.
type PluginSkillSourceDef struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url" doc:"URL a usar ao criar a fonte de skills da empresa (plugin://<id>/<key>)."`
}

// Contributions são as contribuições do M18 declaradas no manifesto.
type Contributions struct {
	Pages        []PluginPageDef        `json:"pages"`
	FlowTriggers []PluginFlowTriggerDef `json:"flow_triggers"`
	EnvProviders []PluginEnvProviderDef `json:"environment_providers"`
	SkillSources []PluginSkillSourceDef `json:"skill_sources"`
}

func contributionsOf(id string, pages []domain.PluginPage, trig []domain.PluginFlowTrigger, envs []domain.PluginEnvProvider, srcs []domain.PluginSkillSource) Contributions {
	c := Contributions{Pages: []PluginPageDef{}, FlowTriggers: []PluginFlowTriggerDef{}, EnvProviders: []PluginEnvProviderDef{}, SkillSources: []PluginSkillSourceDef{}}
	for _, p := range pages {
		c.Pages = append(c.Pages, PluginPageDef{Key: p.Key, Title: p.Title, Icon: p.Icon})
	}
	for _, t := range trig {
		c.FlowTriggers = append(c.FlowTriggers, PluginFlowTriggerDef{Key: t.Key, Title: t.Title, Description: t.Description})
	}
	for _, e := range envs {
		c.EnvProviders = append(c.EnvProviders, PluginEnvProviderDef{Key: e.Key, Title: e.Title, Provider: domain.PluginProviderName(id, e.Key)})
	}
	for _, s := range srcs {
		c.SkillSources = append(c.SkillSources, PluginSkillSourceDef{Key: s.Key, Title: s.Title, Description: s.Description, URL: domain.PluginSkillURL(id, s.Key)})
	}
	return c
}

// PluginToolDef é uma tool declarada no manifesto.
type PluginToolDef struct {
	Name        string `json:"name"`
	MCPName     string `json:"mcp_name" doc:"Nome da tool no MCP do Hanko (plugin_<id>_<tool>)."`
	Description string `json:"description"`
}

// Plugin é uma instalação de plugin.
type Plugin struct {
	ID                  string            `json:"id" format:"uuid"`
	Key                 string            `json:"key" example:"hello" doc:"Id do manifesto (slug global)."`
	Name                string            `json:"name"`
	Description         string            `json:"description,omitempty"`
	Author              string            `json:"author,omitempty"`
	Homepage            string            `json:"homepage,omitempty"`
	Version             string            `json:"version"`
	APIVersion          int               `json:"api_version"`
	Status              string            `json:"status" enum:"installed,ready,disabled,error,upgrade_pending,uninstalled"`
	Capabilities        []string          `json:"capabilities" doc:"Capacidades concedidas."`
	PendingVersion      string            `json:"pending_version,omitempty" doc:"Versão que espera a aprovação das capacidades."`
	PendingCapabilities []string          `json:"pending_capabilities" doc:"Capacidades pedidas pela versão pendente."`
	LastError           string            `json:"last_error,omitempty"`
	Dir                 string            `json:"dir"`
	Companies           int               `json:"companies" doc:"Empresas que ligaram o plugin."`
	Tools               []PluginToolDef   `json:"tools"`
	Events              []string          `json:"events"`
	Widgets             []PluginWidgetDef `json:"widgets"`
	Jobs                []PluginJobDef    `json:"jobs"`
	Contributions
	Process             PluginProcess     `json:"process"`
	InstalledAt         time.Time         `json:"installed_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

func manifestParts(m domain.PluginManifest) ([]PluginToolDef, []PluginWidgetDef, []PluginJobDef) {
	tools := make([]PluginToolDef, 0, len(m.Tools))
	for _, t := range m.Tools {
		tools = append(tools, PluginToolDef{Name: t.Name, MCPName: domain.PluginToolName(m.ID, t.Name), Description: t.Description})
	}
	widgets := make([]PluginWidgetDef, 0, len(m.Widgets))
	for _, w := range m.Widgets {
		widgets = append(widgets, PluginWidgetDef(w))
	}
	jobs := make([]PluginJobDef, 0, len(m.Jobs))
	for _, j := range m.Jobs {
		jobs = append(jobs, PluginJobDef(j))
	}
	return tools, widgets, jobs
}

func strs(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func pluginOut(v service.PluginView) Plugin {
	m := v.Manifest
	tools, widgets, jobs := manifestParts(m)
	o := Plugin{ID: idStr(v.ID), Key: v.Key, Name: m.Name, Description: m.Description, Author: m.Author, Homepage: m.Homepage,
		Version: v.Version, APIVersion: v.APIVersion, Status: string(v.Status), Capabilities: strs(v.GrantedCapabilities),
		PendingCapabilities: strs(v.PendingCapabilities), LastError: v.LastError, Dir: v.Dir, Companies: v.Companies,
		Tools: tools, Events: strs(m.Events), Widgets: widgets, Jobs: jobs, Process: processOut(v.Process),
		Contributions: contributionsOf(v.Key, m.Pages, m.FlowTriggers, m.EnvProviders, m.SkillSources),
		InstalledAt: v.InstalledAt, UpdatedAt: v.UpdatedAt}
	if v.PendingManifest != nil {
		o.PendingVersion = v.PendingManifest.Version
	}
	return o
}

// AvailablePlugin é um diretório de HANKO_PLUGINS_DIR.
type AvailablePlugin struct {
	Key          string   `json:"key"`
	Dir          string   `json:"dir"`
	Name         string   `json:"name,omitempty"`
	Version      string   `json:"version,omitempty"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities" doc:"Capacidades que o plugin pede."`
	Error        string   `json:"error,omitempty" doc:"Por que o diretório não pode ser instalado."`
	Installed    string   `json:"installed,omitempty" doc:"Versão instalada, quando há."`
	Upgrade      bool     `json:"upgrade,omitempty" doc:"O diretório tem versão mais nova que a instalada."`
}

// PluginsOverview é a visão da instalação.
type PluginsOverview struct {
	Enabled   bool              `json:"enabled" doc:"HANKO_PLUGINS_DIR configurado."`
	Dir       string            `json:"dir,omitempty"`
	Available []AvailablePlugin `json:"available"`
	Installed []Plugin          `json:"installed"`
}

// PluginLog é uma linha do log do plugin.
type PluginLog struct {
	ID        string    `json:"id" format:"uuid"`
	CompanyID string    `json:"company_id,omitempty"`
	Level     string    `json:"level" enum:"debug,info,warn,error,stderr,host"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// PluginHealth é a saúde do plugin.
type PluginHealth struct {
	Status    string        `json:"status"`
	LastError string        `json:"last_error,omitempty"`
	Companies int           `json:"companies"`
	Process   PluginProcess `json:"process"`
}

// PluginJobRun é uma rodada de job.
type PluginJobRun struct {
	ID         string     `json:"id" format:"uuid"`
	JobKey     string     `json:"job_key"`
	Status     string     `json:"status" enum:"running,done,failed"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// PluginConfigField é um campo do formulário de configuração.
type PluginConfigField struct {
	Key         string   `json:"key"`
	Type        string   `json:"type" enum:"string,number,boolean"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
	Required    bool     `json:"required"`
	Secret      bool     `json:"secret" doc:"Guardado cifrado; o valor nunca volta."`
}

// CompanyPlugin é um plugin instalado visto pela empresa.
type CompanyPlugin struct {
	PluginID     string              `json:"plugin_id" format:"uuid"`
	Key          string              `json:"key"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Version      string              `json:"version"`
	Status       string              `json:"status" doc:"Estado na instalação."`
	Capabilities []string            `json:"capabilities"`
	Enabled      bool                `json:"enabled" doc:"A empresa ligou o plugin."`
	Active       bool                `json:"active" doc:"Ligado e atendendo (pronto na instalação)."`
	Config       map[string]any      `json:"config" doc:"Valores não secretos."`
	SecretsSet   []string            `json:"secrets_set" doc:"Campos secretos definidos (só os nomes)."`
	Fields       []PluginConfigField `json:"fields" doc:"O formulário gerado do config_schema."`
	Tools        []PluginToolDef     `json:"tools"`
	Events       []string            `json:"events"`
	Widgets      []PluginWidgetDef   `json:"widgets"`
	Jobs         []PluginJobDef      `json:"jobs"`
	Contributions
	JobRuns      []PluginJobRun      `json:"job_runs,omitempty"`
	UpdatedAt    *time.Time          `json:"updated_at,omitempty"`
}

func configFields(s domain.PluginConfigSchema) []PluginConfigField {
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	out := make([]PluginConfigField, 0, len(s.Properties))
	for _, k := range s.Keys() {
		f := s.Properties[k]
		out = append(out, PluginConfigField{Key: k, Type: f.Type, Title: f.Title, Description: f.Description, Enum: f.Enum,
			Default: f.Default, Required: req[k], Secret: f.Secret})
	}
	return out
}

func companyPluginOut(v service.CompanyPlugin) CompanyPlugin {
	m := domain.PluginManifest{ID: v.Key, Tools: v.Tools, Widgets: v.Widgets, Jobs: v.Jobs}
	tools, widgets, jobs := manifestParts(m)
	o := CompanyPlugin{PluginID: idStr(v.PluginID), Key: v.Key, Name: v.Name, Description: v.Description, Version: v.Version,
		Status: string(v.Status), Capabilities: strs(v.Capabilities), Enabled: v.Enabled, Active: v.Active, Config: v.Config,
		SecretsSet: strs(v.SecretsSet), Fields: configFields(v.ConfigSchema), Tools: tools, Events: strs(v.Events), Widgets: widgets,
		Jobs: jobs, UpdatedAt: v.UpdatedAt, Contributions: contributionsOf(v.Key, v.Pages, v.FlowTriggers, v.EnvProviders, v.SkillSources)}
	if o.Config == nil {
		o.Config = map[string]any{}
	}
	for _, r := range v.JobRuns {
		o.JobRuns = append(o.JobRuns, PluginJobRun{ID: idStr(r.ID), JobKey: r.JobKey, Status: r.Status, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Error: r.Error})
	}
	return o
}

// PluginStateItem é uma chave do estado do plugin na empresa.
type PluginStateItem struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// PluginTable é a tabela de um widget.
type PluginTable struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// PluginWidget é o conteúdo de um widget (dados; o Hanko renderiza, nenhum script do plugin chega à página).
type PluginWidget struct {
	Plugin   string       `json:"plugin"`
	Key      string       `json:"key"`
	Title    string       `json:"title"`
	Markdown string       `json:"markdown,omitempty" doc:"Markdown; a UI renderiza sanitizado."`
	Table    *PluginTable `json:"table,omitempty"`
	Error    string       `json:"error,omitempty" doc:"O plugin não respondeu a tempo ou está fora do ar."`
}

// ---------- entradas ----------

type PluginPath struct {
	Plugin string `path:"plugin" doc:"Id do plugin (slug do manifesto) ou id da instalação." example:"hello"`
}

type CompanyPluginPath struct {
	CompanyPath
	Plugin string `path:"plugin" doc:"Id do plugin (slug do manifesto) ou id da instalação." example:"hello"`
}

type installPluginIn struct {
	Body struct {
		Key string `json:"key" pattern:"^[a-z][a-z0-9-]{1,62}$" doc:"Nome do diretório do plugin em HANKO_PLUGINS_DIR (igual ao id do manifesto)." example:"hello"`
	}
}

type pluginCapabilityIn struct {
	PluginPath
	Capability string `path:"capability" enum:"tasks.read,tasks.write,personas.read,projects.read,activity.read,events.subscribe,agent.tools,widgets,jobs,state,http.outbound,pages,flows.trigger,environments.provider,skills.source" doc:"Capacidade do plugin."`
}

type pluginLogsIn struct {
	PluginPath
	Limit int `query:"limit" minimum:"1" maximum:"1000" default:"100" doc:"Máximo de linhas (mais recentes primeiro)."`
}

type listCompanyPluginsIn struct {
	CompanyPath
	Page
}

type updateCompanyPluginIn struct {
	CompanyPluginPath
	Body struct {
		Enabled      *bool          `json:"enabled,omitempty" doc:"Liga ou desliga o plugin nesta empresa."`
		Config       map[string]any `json:"config,omitempty" doc:"Campos do config_schema. Não secreto com null ou \"\" volta ao padrão; secreto com \"\" fica como está."`
		ClearSecrets []string       `json:"clear_secrets,omitempty" doc:"Campos secretos a apagar."`
	}
}

type pluginStateIn struct {
	CompanyPluginPath
	Prefix string `query:"prefix" maxLength:"200" doc:"Só as chaves com este prefixo."`
	Limit  int    `query:"limit" minimum:"1" maximum:"1000" default:"100"`
}

type pluginWidgetsIn struct {
	CompanyPluginPath
	Placement string `query:"placement" enum:"dashboard,task,persona" default:"dashboard"`
	Ref       string `query:"ref" doc:"task: chave ou id; persona: slug ou id; dashboard: vazio."`
}

// ---------- rotas ----------

func (s *server) pluginAdmin(ctx context.Context) (pluginsBackend, error) {
	if err := forbidPersona(ctx); err != nil {
		return nil, err
	}
	return s.plugins()
}

func (s *server) registerPlugins(api huma.API) {
	huma.Register(api, op("list-plugins", http.MethodGet, "/plugins", "plugins", "Plugins disponíveis no diretório e instalados (dono da instalação)"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[PluginsOverview], error) {
			pb, err := s.pluginAdmin(ctx)
			if err != nil {
				return nil, err
			}
			ov, err := pb.PluginOverview(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := PluginsOverview{Enabled: ov.Enabled, Dir: ov.Dir, Available: []AvailablePlugin{}, Installed: []Plugin{}}
			for _, a := range ov.Available {
				ao := AvailablePlugin{Key: a.Key, Dir: a.Dir, Error: a.Error, Installed: a.Installed, Upgrade: a.Upgrade, Capabilities: []string{}}
				if a.Manifest != nil {
					ao.Name, ao.Version, ao.Description, ao.Capabilities = a.Manifest.Name, a.Manifest.Version, a.Manifest.Description, strs(a.Manifest.Capabilities)
				}
				out.Available = append(out.Available, ao)
			}
			for _, p := range ov.Installed {
				out.Installed = append(out.Installed, pluginOut(p))
			}
			return &ItemOut[PluginsOverview]{Body: out}, nil
		})

	huma.Register(api, created(op("install-plugin", http.MethodPost, "/plugins/install", "plugins",
		"Instala o plugin de um diretório de HANKO_PLUGINS_DIR, concedendo as capacidades pedidas (dono da instalação)")),
		func(ctx context.Context, in *installPluginIn) (*ItemOut[Plugin], error) {
			pb, err := s.pluginAdmin(ctx)
			if err != nil {
				return nil, err
			}
			v, err := pb.InstallPlugin(ctx, in.Body.Key)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Plugin]{Body: pluginOut(v)}, nil
		})

	action := func(id, path, summary string, fn func(pb pluginsBackend, ctx context.Context, ref string) (service.PluginView, error)) {
		huma.Register(api, op(id, http.MethodPost, "/plugins/{plugin}/"+path, "plugins", summary),
			func(ctx context.Context, in *PluginPath) (*ItemOut[Plugin], error) {
				pb, err := s.pluginAdmin(ctx)
				if err != nil {
					return nil, err
				}
				v, err := fn(pb, ctx, in.Plugin)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				return &ItemOut[Plugin]{Body: pluginOut(v)}, nil
			})
	}
	action("enable-plugin", "enable", "Liga o plugin na instalação (dono)", func(pb pluginsBackend, ctx context.Context, ref string) (service.PluginView, error) {
		return pb.SetPluginEnabled(ctx, ref, true)
	})
	action("disable-plugin", "disable", "Desliga o plugin na instalação inteira; o processo para e a configuração das empresas fica (dono)",
		func(pb pluginsBackend, ctx context.Context, ref string) (service.PluginView, error) {
			return pb.SetPluginEnabled(ctx, ref, false)
		})
	action("upgrade-plugin", "upgrade", "Aplica a versão nova do diretório; pedindo capacidade a mais, fica upgrade_pending (dono)",
		func(pb pluginsBackend, ctx context.Context, ref string) (service.PluginView, error) {
			return pb.UpgradePlugin(ctx, ref)
		})
	action("approve-plugin-upgrade", "approve-upgrade", "Aprova as capacidades da versão pendente e a aplica (dono)",
		func(pb pluginsBackend, ctx context.Context, ref string) (service.PluginView, error) {
			return pb.ApprovePluginUpgrade(ctx, ref)
		})

	huma.Register(api, noContent(op("uninstall-plugin", http.MethodPost, "/plugins/{plugin}/uninstall", "plugins",
		"Desinstala: para o processo e as tools e apaga configuração e estado em todas as empresas (dono)")),
		func(ctx context.Context, in *PluginPath) (*struct{}, error) {
			pb, err := s.pluginAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := pb.UninstallPlugin(ctx, in.Plugin); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("plugin-logs", http.MethodGet, "/plugins/{plugin}/logs", "plugins", "Últimas linhas do log do plugin (dono)"),
		func(ctx context.Context, in *pluginLogsIn) (*ListOut[PluginLog], error) {
			pb, err := s.pluginAdmin(ctx)
			if err != nil {
				return nil, err
			}
			logs, err := pb.ListPluginLogs(ctx, in.Plugin, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(logs, Page{Limit: in.Limit}, func(l domain.PluginLog) PluginLog {
				o := PluginLog{ID: idStr(l.ID), Level: l.Level, Message: l.Message, CreatedAt: l.CreatedAt}
				if l.CompanyID != nil {
					o.CompanyID = l.CompanyID.String()
				}
				return o
			}), nil
		})

	huma.Register(api, op("plugin-health", http.MethodGet, "/plugins/{plugin}/health", "plugins", "Saúde do plugin: estado, PID, uptime, reinícios e último erro (dono)"),
		func(ctx context.Context, in *PluginPath) (*ItemOut[PluginHealth], error) {
			pb, err := s.pluginAdmin(ctx)
			if err != nil {
				return nil, err
			}
			h, err := pb.GetPluginHealth(ctx, in.Plugin)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PluginHealth]{Body: PluginHealth{Status: string(h.Status), LastError: h.LastError, Companies: h.Companies, Process: processOut(h.Process)}}, nil
		})

	capability := func(id, verb, summary string, granted bool) {
		huma.Register(api, op(id, http.MethodPost, "/plugins/{plugin}/capabilities/{capability}/"+verb, "plugins", summary),
			func(ctx context.Context, in *pluginCapabilityIn) (*ItemOut[Plugin], error) {
				pb, err := s.pluginAdmin(ctx)
				if err != nil {
					return nil, err
				}
				v, err := pb.SetPluginCapability(ctx, in.Plugin, in.Capability, granted)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				return &ItemOut[Plugin]{Body: pluginOut(v)}, nil
			})
	}
	capability("grant-plugin-capability", "grant", "Concede uma capacidade declarada no manifesto do plugin (dono)", true)
	capability("revoke-plugin-capability", "revoke",
		"Revoga uma capacidade: tools, aba, widgets, gatilhos, provider e fonte de skills que dependem dela somem na hora (dono)", false)

	s.registerCompanyPlugins(api)
	s.registerPluginContributions(api)
}

func (s *server) registerCompanyPlugins(api huma.API) {
	huma.Register(api, op("list-company-plugins", http.MethodGet, "/companies/{company}/plugins", "plugins", "Plugins instalados e o estado na empresa"),
		func(ctx context.Context, in *listCompanyPluginsIn) (*ListOut[CompanyPlugin], error) {
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			list, err := pb.ListCompanyPlugins(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, companyPluginOut), nil
		})

	huma.Register(api, op("get-company-plugin", http.MethodGet, "/companies/{company}/plugins/{plugin}", "plugins", "Um plugin na empresa, com o formulário de configuração e as últimas rodadas de job"),
		func(ctx context.Context, in *CompanyPluginPath) (*ItemOut[CompanyPlugin], error) {
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := pb.GetCompanyPlugin(ctx, sc.Company.ID, in.Plugin)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[CompanyPlugin]{Body: companyPluginOut(v)}, nil
		})

	huma.Register(api, op("update-company-plugin", http.MethodPatch, "/companies/{company}/plugins/{plugin}", "plugins",
		"Liga, desliga e configura o plugin na empresa (admin); a configuração passa pelo schema e pelo validate_config do plugin"),
		func(ctx context.Context, in *updateCompanyPluginIn) (*ItemOut[CompanyPlugin], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := pb.UpdateCompanyPlugin(ctx, sc.Actor, sc.Company.ID, in.Plugin, service.UpdateCompanyPluginInput{
				Enabled: in.Body.Enabled, Config: in.Body.Config, ClearSecrets: in.Body.ClearSecrets})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[CompanyPlugin]{Body: companyPluginOut(v)}, nil
		})

	huma.Register(api, op("company-plugin-state", http.MethodGet, "/companies/{company}/plugins/{plugin}/state", "plugins", "Estado chave/valor do plugin na empresa (admin)"),
		func(ctx context.Context, in *pluginStateIn) (*ListOut[PluginStateItem], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			items, err := pb.ListPluginState(ctx, sc.Company.ID, in.Plugin, in.Prefix, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(items, Page{Limit: in.Limit}, func(i service.PluginStateItem) PluginStateItem { return PluginStateItem(i) }), nil
		})

	huma.Register(api, op("company-plugin-widgets", http.MethodGet, "/companies/{company}/plugins/{plugin}/widgets", "plugins",
		"Conteúdo dos widgets do plugin para um lugar (Painel, task ou persona); cache de 60 s"),
		func(ctx context.Context, in *pluginWidgetsIn) (*ListOut[PluginWidget], error) {
			pb, err := s.plugins()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := pb.GetCompanyPlugin(ctx, sc.Company.ID, in.Plugin)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			ws, err := pb.PluginWidgets(ctx, sc.Company.ID, service.PluginWidgetRef{Placement: in.Placement, Ref: in.Ref})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			var mine []service.PluginWidgetView
			for _, w := range ws {
				if w.PluginKey == p.Key {
					mine = append(mine, w)
				}
			}
			return listAll(mine, Page{Limit: len(mine)}, func(w service.PluginWidgetView) PluginWidget {
				o := PluginWidget{Plugin: w.PluginKey, Key: w.Key, Title: w.Title, Markdown: w.Markdown, Error: w.Error}
				if w.Table != nil {
					o.Table = &PluginTable{Columns: w.Table.Columns, Rows: w.Table.Rows}
				}
				return o
			}), nil
		})
}
