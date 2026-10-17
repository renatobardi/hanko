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
}

// PluginWidgetVM é um widget renderizado pelo Hanko (markdown sanitizado e/ou tabela).
type PluginWidgetVM struct {
	Plugin, Title string
	Body          HTML
	Columns       []string
	Rows          [][]string
	Error         string
}
