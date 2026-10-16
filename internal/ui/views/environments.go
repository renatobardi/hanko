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
	// SSHOff e SandboxOff explicam por que o driver está desligado neste servidor ("" = disponível).
	SSHOff, SandboxOff string
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
}

// runEnvHref é o fragmento com o ambiente da execução, relativo à página dela (/c/{empresa}/execucoes/{id}).
func runEnvHref(vm RunVM) string { return vm.ID + "/ambiente" }

// personaEnvHref deriva, do POST do glifo da persona, o fragmento com o ambiente dela.
func personaEnvHref(vm PersonaVM) string {
	return strings.TrimSuffix(vm.IconAction, "/icone") + "/ambiente"
}
