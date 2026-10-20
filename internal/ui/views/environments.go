package views

import "strings"

// Ambientes de execução (ADR-0037): Configurações › Ambientes, a página de um ambiente, o seletor na página
// da persona e o ambiente na página da execução.

// EnvLocalCard é o ambiente local (implícito): as verificações reais deste servidor.
type EnvLocalCard struct {
	Checks    []EnvCheck
	OK, Total int
	Facts     []KV
	IsDefault bool
}

// EnvRow é uma linha da lista de ambientes.
type EnvRow struct {
	Name, Slug, Href   string
	Description        string
	Driver, Target     string
	Badge              Badge
	Default            bool
	Personas, InUse    int
	LastProbe          string
	LastProbeOK        bool
	HasProbe           bool
	TestAction         string
	Unavailable        string
	DriverIcon, Status string
}

// EnvHubVM é Configurações › Ambientes.
type EnvHubVM struct {
	Sections                   []TabLink
	Notice                     string
	Local                      EnvLocalCard
	Rows, Archived             []EnvRow
	DefaultAction              string
	DefaultOptions             []Option
	NewSSHHref, NewSandboxHref string
	// NewLocalHref e NewCloudHref abrem o formulário do local explícito e do sandbox em nuvem (ADR-0055).
	NewLocalHref, NewCloudHref string
	// SSHOff, SandboxOff e CloudOff explicam por que o driver está desligado neste servidor ("" = disponível).
	SSHOff, SandboxOff, CloudOff string
}

// EnvFormVM é o formulário de um ambiente (novo ou edição).
type EnvFormVM struct {
	Driver                  string
	Edit                    bool
	Action, ProbeAction     string
	CancelHref              string
	Error                   string
	FieldErr                map[string]string
	Name, Slug, Description string
	// SSH
	Host, Port, User, Root, KnownHosts string
	KeySet                             bool
	// Sandbox
	Image, Memory, CPUs, PIDs string
	NetworkOptions            []Option
	// Cloud (ADR-0055)
	Snapshot, CloudCPUs, CloudMemory, CloudDisk, CloudRoot string
	CloudNetworkOptions                                    []Option
	// Variáveis
	EnvNames []string
	DropEnv  string
	// DriverTabs troca o driver de um formulário novo.
	DriverTabs []TabLink
}

// ProbeStepVM é um passo do teste de conexão.
type ProbeStepVM struct {
	Label, Detail string
	OK            bool
}

// ProbeVM é o relatório de um teste de conexão.
type ProbeVM struct {
	OK       bool
	When     string
	System   string
	Duration string
	Steps    []ProbeStepVM
	Warnings []string
	Error    string
	// Capabilities são o que o alvo oferece, medido pelo teste (ADR-0055).
	Capabilities []string
}

// LeaseRow é um uso do ambiente por uma execução.
type LeaseRow struct {
	When, Run, RunHref, Persona string
	Badge                       Badge
	RemoteDir, Reason           string
}

// EnvPageVM é a página de um ambiente.
type EnvPageVM struct {
	Sections                        []TabLink
	Name, Slug, Description         string
	Badge                           Badge
	DriverLabel, Target             string
	Facts                           []KV
	Unavailable, Notice             string
	Warnings                        []string
	TestAction                      string
	Probe                           *ProbeVM
	Form                            EnvFormVM
	Leases                          []LeaseRow
	Archived                        bool
	ArchiveAction, ReactivateAction string
	MoveOptions                     []Option
	PersonaNames                    []string
	// ProjectNames são os projetos que escolheram este ambiente (ADR-0055).
	ProjectNames []string
}

// PersonaEnvVM é o cartão de ambiente na página da persona.
type PersonaEnvVM struct {
	Current, Source string
	Options         []Option
	Action          string
	SettingsHref    string
	Notice, Error   string
}

// RunEnvVM é o ambiente em que uma execução rodou.
type RunEnvVM struct {
	Label, Href string
	Badge       *Badge
	RemoteDir   string
	// Source diz por que: "persona", "projeto", "padrão da empresa" ou "local" (ADR-0055); vazio em execuções antigas.
	Source string
}

// ProjectEnvVM é o cartão de ambiente na página do projeto (ADR-0055).
type ProjectEnvVM struct {
	Current, Source string
	Options         []Option
	Action          string
	SettingsHref    string
	Notice, Error   string
}

// PersonaReadinessVM é o cartão de prontidão da persona (ADR-0055, #10).
type PersonaReadinessVM struct {
	// Status: ready, not_ready ou untested.
	Status        string
	Badge         Badge
	Reason        string
	When, Env     string
	Duration      string
	Steps         []EnvCheck
	Action        string
	Blocking      bool
	AutoTest      bool
	Notice, Error string
}

// ModelOptionVM é uma entrada do seletor de modelo (ADR-0055, #17).
type ModelOptionVM struct {
	Value, Label string
	Selected     bool
	// Unpriced: o modelo não tem preço próprio; o custo cai no preço de reserva.
	Unpriced bool
}

// ModelGroupVM agrupa os modelos de um harness no seletor.
type ModelGroupVM struct {
	Label   string
	Options []ModelOptionVM
}

// ModelPickerVM é o seletor de modelo com a opção "Outro…".
type ModelPickerVM struct {
	// ID e Name: o id do select e o nome do campo (o texto livre vai em Name; a escolha em Name+"_choice").
	ID, Name string
	// Value é o modelo atual (vazio = padrão do harness); Other: o valor não está no catálogo e vai no campo livre.
	Value  string
	Other  bool
	Groups []ModelGroupVM
}

// runEnvHref é o fragmento com o ambiente da execução, relativo à página dela (/c/{empresa}/execucoes/{id}).
func runEnvHref(vm RunVM) string { return vm.ID + "/ambiente" }

// personaEnvHref deriva, do POST do glifo da persona, o fragmento com o ambiente dela.
func personaEnvHref(vm PersonaVM) string {
	return strings.TrimSuffix(vm.IconAction, "/icone") + "/ambiente"
}
