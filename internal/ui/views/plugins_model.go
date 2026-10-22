package views

// Plugins de terceiros (ADR-0038): view models de Configurações › Plugins e dos blocos "De plugins".

// PluginCap é uma capacidade com o rótulo para pessoas; Warn destaca as que pedem atenção do operador.
type PluginCap struct {
	Name, Label string
	Warn        bool
}

// PluginCardVM é um plugin instalado na lista (e o cabeçalho da página de detalhe).
type PluginCardVM struct {
	Key, Name, Desc, Version, Author, Homepage string
	Badge                                      Badge
	Caps                                       []PluginCap
	// Enabled: ligado nesta empresa; Active: ligado e atendendo.
	Enabled, Active bool
	// ToggleHref liga/desliga nesta empresa; Href é o detalhe.
	ToggleHref, Href             string
	Tools, Widgets, Jobs, Events int
	LastError                    string
	// Atualização pendente: a versão e as capacidades a mais.
	PendingVersion string
	PendingCaps    []PluginCap
	// Ações do dono da instalação (POST <OwnerBase>/<ação>); vazio = quem vê não é o dono.
	OwnerBase             string
	CanSuspend, CanResume bool
	// CapToggles são as capacidades declaradas no manifesto com o estado da concessão (só o dono da instalação,
	// ADR-0045); conceder/revogar é POST <OwnerBase>/conceder|revogar com cap=<nome>.
	CapToggles []PluginCapToggle
	// Contribuições do M18 (contagens para o cartão).
	Pages, Triggers, Providers, Sources int
}

// PluginCapToggle é uma capacidade declarada, concedida ou não.
type PluginCapToggle struct {
	PluginCap
	Granted bool
	// Action é o POST que concede ou revoga.
	Action string
}

// AvailablePluginVM é um diretório de HANKO_PLUGINS_DIR.
type AvailablePluginVM struct {
	Key, Name, Version, Desc, Dir string
	Caps                          []PluginCap
	Error                         string
	Installed                     string
	Upgrade                       bool
	InstallAction, UpgradeAction  string
}

// PluginsPageVM é Configurações › Plugins.
type PluginsPageVM struct {
	Sections  []TabLink
	On        bool
	Dir       string
	Owner     bool
	Notice    string
	Error     string
	Installed []PluginCardVM
	Available []AvailablePluginVM
	Builtin   []PluginItem
}

// PluginFieldVM é um campo do formulário gerado do config_schema.
type PluginFieldVM struct {
	Key, Label, Desc, Type string
	Value                  string
	Checked                bool
	Options                []Option
	Required               bool
	Secret, SecretSet      bool
	Placeholder            string
	Err                    string
}

// PluginToolRow é uma tool exposta às personas.
type PluginToolRow struct{ Name, MCPName, Desc string }

// PluginLogRow é uma linha do log.
type PluginLogRow struct{ When, Level, Message string }

// PluginJobRow é uma rodada de job.
type PluginJobRow struct {
	Key, Started, Finished, Error string
	Badge                         Badge
}

// PluginHealthVM é a saúde do processo (só para o dono da instalação).
type PluginHealthVM struct {
	Badge                       Badge
	PID, Uptime, Restarts, Next string
	LastError                   string
}

// PluginDetailVM é a página de um plugin na empresa.
type PluginDetailVM struct {
	Sections   []TabLink
	Card       PluginCardVM
	BackHref   string
	FormAction string
	Fields     []PluginFieldVM
	Notice     string
	Error      string
	Tools      []PluginToolRow
	Events     []string
	Jobs       []PluginJobRow
	StateKeys  int
	Health     *PluginHealthVM
	Logs       []PluginLogRow
	// M18 (ADR-0045): abas, gatilhos de fluxo, providers de ambiente e fontes de skills do plugin.
	Pages     []PluginPageRow
	Triggers  []PluginTriggerVM
	Providers []PluginProviderRow
	Sources   []PluginSourceRow
}

// PluginPageRow é uma aba do plugin (link para a página hospedeira).
type PluginPageRow struct{ Title, Href string }

// PluginTriggerVM é um gatilho de fluxo com as ligações da empresa e o formulário para ligar mais um.
type PluginTriggerVM struct {
	Key, Title, Desc string
	Links            []PluginTriggerLink
	// Admin: pode ligar/desligar. LinkAction recebe flow=<slug>; FlowOptions são os fluxos ainda não ligados.
	Admin       bool
	LinkAction  string
	FlowOptions []Option
}

// PluginTriggerLink é um fluxo ligado ao gatilho.
type PluginTriggerLink struct{ Name, Slug, UnlinkAction string }

// PluginProviderRow é um provider de ambiente (valor de cloud.provider).
type PluginProviderRow struct{ Title, Provider string }

// PluginSourceRow é uma fonte de skills do plugin (URL a usar ao conectar).
type PluginSourceRow struct{ Title, URL string }

// PluginTab é uma aba de plugin no menu da empresa.
type PluginTab struct{ Title, Href, Icon string }

// PluginPageVM é a página hospedeira de uma aba: o iframe sandboxed e a ponte.
type PluginPageVM struct {
	Title, PluginName string
	FrameSrc          string
	BridgeURL         string
	Company           string
}

// PluginWidgetVM é um widget renderizado pelo Hanko (markdown sanitizado e/ou tabela).
type PluginWidgetVM struct {
	Plugin, Title string
	Body          HTML
	Columns       []string
	Rows          [][]string
	Error         string
}
