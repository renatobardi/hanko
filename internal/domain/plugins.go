package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Plugins de terceiros (ADR-0038). Um plugin é um processo externo que fala JSON-RPC 2.0 por stdin/stdout; o
// Hanko nunca carrega código de plugin no próprio processo. Este arquivo tem só regras puras: o manifesto, as
// capacidades, os estados e as transições.

// PluginAPIVersion é a versão do protocolo de plugins que este Hanko fala.
const PluginAPIVersion = 1

// ManifestFile é o nome do manifesto na raiz do diretório de cada plugin.
const ManifestFile = "hanko-plugin.json"

// Limites do manifesto e do estado.
const (
	PluginManifestMaxBytes  = 256 << 10
	PluginMaxTools          = 32
	PluginMaxEvents         = 32
	PluginMaxWidgets        = 16
	PluginMaxJobs           = 16
	PluginMaxConfigFields   = 32
	PluginMaxArgs           = 32
	PluginToolSchemaMax     = 16 << 10
	PluginToolTimeoutMaxSec = 300
	PluginJobMinMinutes     = 5
	PluginJobMaxMinutes     = 1440
	PluginStateKeyMax       = 200
	PluginStateValueMax     = 64 << 10
	PluginStateMaxKeys      = 1000
	PluginLogMessageMax     = 4 << 10
	PluginLogsKeep          = 1000
	PluginConfigValueMax    = 4 << 10
)

// PluginStatus é o estado de uma instalação de plugin.
type PluginStatus string

const (
	PluginInstalled      PluginStatus = "installed"
	PluginReady          PluginStatus = "ready"
	PluginDisabled       PluginStatus = "disabled"
	PluginError          PluginStatus = "error"
	PluginUpgradePending PluginStatus = "upgrade_pending"
	PluginUninstalled    PluginStatus = "uninstalled"
)

var pluginTransitions = map[PluginStatus][]PluginStatus{
	PluginInstalled:      {PluginReady, PluginError, PluginUninstalled},
	PluginReady:          {PluginDisabled, PluginError, PluginUpgradePending, PluginUninstalled},
	PluginDisabled:       {PluginReady, PluginUninstalled},
	PluginError:          {PluginReady, PluginUninstalled},
	PluginUpgradePending: {PluginReady, PluginError, PluginUninstalled},
	// reinstalar um plugin desinstalado reaproveita a linha (plugin_key é único).
	PluginUninstalled: {PluginInstalled},
}

// Valid informa se s é um estado conhecido.
func (s PluginStatus) Valid() bool { _, ok := pluginTransitions[s]; return ok }

// CanBecome informa se a transição s → to é permitida.
func (s PluginStatus) CanBecome(to PluginStatus) bool { return slices.Contains(pluginTransitions[s], to) }

// Active: o plugin atende as empresas ligadas (tools, eventos, widgets e jobs). Num upgrade pendente o plugin
// continua atendendo com o manifesto e as capacidades já aprovados.
func (s PluginStatus) Active() bool { return s == PluginReady || s == PluginUpgradePending }

// WantsProcess: o processo deve estar de pé quando há ao menos uma empresa ligada. Em error o gerenciador
// continua tentando subir (com backoff), para o plugin voltar sozinho quando o problema passar.
func (s PluginStatus) WantsProcess() bool { return s.Active() || s == PluginError }

// Capacidades que um plugin pode pedir. O host recusa qualquer chamada fora das concedidas. Decidir gate,
// mexer em orçamento, autenticação, ler segredos do Hanko e acessar o banco não existem como capacidade.
const (
	CapTasksRead      = "tasks.read"
	CapTasksWrite     = "tasks.write"
	CapPersonasRead   = "personas.read"
	CapProjectsRead   = "projects.read"
	CapActivityRead   = "activity.read"
	CapEventsSubscribe = "events.subscribe"
	CapAgentTools     = "agent.tools"
	CapWidgets        = "widgets"
	CapJobs           = "jobs"
	CapState          = "state"
	// CapHTTPOutbound é declarativa: avisa o operador que o plugin fala com a rede. O Hanko não tem como
	// impedir rede de um processo; quem quiser isola o processo (container, firewall).
	CapHTTPOutbound = "http.outbound"
)

// PluginCapabilities lista as capacidades na ordem de exibição.
var PluginCapabilities = []string{CapTasksRead, CapTasksWrite, CapPersonasRead, CapProjectsRead, CapActivityRead,
	CapEventsSubscribe, CapAgentTools, CapWidgets, CapJobs, CapState, CapHTTPOutbound}

// ValidPluginCapability informa se c é uma capacidade conhecida.
func ValidPluginCapability(c string) bool { return slices.Contains(PluginCapabilities, c) }

// SortCapabilities ordena e remove repetidas, na ordem de PluginCapabilities.
func SortCapabilities(in []string) []string {
	out := make([]string, 0, len(in))
	for _, c := range PluginCapabilities {
		if slices.Contains(in, c) {
			out = append(out, c)
		}
	}
	return out
}

// ExtraCapabilities devolve as de want que não estão em have (o que um upgrade pede a mais).
func ExtraCapabilities(have, want []string) []string {
	var out []string
	for _, c := range SortCapabilities(want) {
		if !slices.Contains(have, c) {
			out = append(out, c)
		}
	}
	return out
}

// PluginMethodCapability é a capacidade exigida por cada método que o plugin chama no host. Métodos fora do
// mapa não existem. Vazio = só exige o plugin ligado na empresa (o próprio log e a própria configuração).
var PluginMethodCapability = map[string]string{
	"tasks.list":          CapTasksRead,
	"tasks.get":           CapTasksRead,
	"tasks.create":        CapTasksWrite,
	"tasks.comment":       CapTasksWrite,
	"tasks.update_status": CapTasksWrite,
	"personas.list":       CapPersonasRead,
	"projects.list":       CapProjectsRead,
	"activity.list":       CapActivityRead,
	"state.get":           CapState,
	"state.set":           CapState,
	"state.delete":        CapState,
	"state.list":          CapState,
	"config.get":          "",
	"log":                 "",
}

// Widget placements.
const (
	WidgetDashboard = "dashboard"
	WidgetTask      = "task"
	WidgetPersona   = "persona"
)

// ValidWidgetPlacement informa se p é um lugar de widget.
func ValidWidgetPlacement(p string) bool {
	return p == WidgetDashboard || p == WidgetTask || p == WidgetPersona
}

// Tipos de campo do config_schema.
const (
	ConfigString  = "string"
	ConfigNumber  = "number"
	ConfigBoolean = "boolean"
)

// PluginConfigField é um campo do formulário de configuração (subconjunto de JSON Schema).
type PluginConfigField struct {
	Type        string   `json:"type"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
	// Secret: o valor é guardado cifrado e nunca volta pela API nem pela UI.
	Secret bool `json:"secret,omitempty"`
}

// PluginConfigSchema é o config_schema: um object com properties e required.
type PluginConfigSchema struct {
	Type       string                       `json:"type,omitempty"`
	Properties map[string]PluginConfigField `json:"properties,omitempty"`
	Required   []string                     `json:"required,omitempty"`
}

// Keys devolve os nomes dos campos em ordem alfabética (ordem estável para a UI).
func (s PluginConfigSchema) Keys() []string {
	keys := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SecretKeys devolve os campos marcados como segredo.
func (s PluginConfigSchema) SecretKeys() []string {
	var out []string
	for _, k := range s.Keys() {
		if s.Properties[k].Secret {
			out = append(out, k)
		}
	}
	return out
}

// PluginTool é uma tool que o plugin expõe às personas.
type PluginTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	// TimeoutSec é o teto da chamada (1..300); 0 usa o padrão de 30 s.
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// PluginWidget é um bloco de dados que o plugin mostra no Painel, na task ou na persona.
type PluginWidget struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Placement string `json:"placement"`
}

// PluginJob é um trabalho periódico que o host agenda.
type PluginJob struct {
	Key          string `json:"key"`
	EveryMinutes int    `json:"every_minutes"`
}

// PluginManifest é o hanko-plugin.json.
type PluginManifest struct {
	ID           string             `json:"id"`
	APIVersion   int                `json:"api_version"`
	Version      string             `json:"version"`
	Name         string             `json:"name"`
	Description  string             `json:"description,omitempty"`
	Author       string             `json:"author,omitempty"`
	Homepage     string             `json:"homepage,omitempty"`
	Command      string             `json:"command"`
	Args         []string           `json:"args,omitempty"`
	Capabilities []string           `json:"capabilities"`
	ConfigSchema PluginConfigSchema `json:"config_schema,omitzero"`
	Tools        []PluginTool       `json:"tools,omitempty"`
	Events       []string           `json:"events,omitempty"`
	Widgets      []PluginWidget     `json:"widgets,omitempty"`
	Jobs         []PluginJob        `json:"jobs,omitempty"`
}

var (
	pluginIDRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)
	pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	pluginKeyRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)
	eventRe      = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	semverRe     = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
)

// ValidPluginID informa se id é um slug de plugin válido.
func ValidPluginID(id string) bool { return pluginIDRe.MatchString(id) }

func limitText(field, v string, max int) error {
	if !utf8.ValidString(v) {
		return Invalid(field, "texto inválido")
	}
	if utf8.RuneCountInString(v) > max {
		return Invalid(field, fmt.Sprintf("até %d caracteres", max))
	}
	return nil
}

// ParsePluginManifest lê e valida um manifesto. Campos desconhecidos são recusados (um erro de digitação
// numa capacidade não pode virar capacidade ignorada em silêncio).
func ParsePluginManifest(b []byte) (PluginManifest, error) {
	var m PluginManifest
	if len(b) > PluginManifestMaxBytes {
		return m, Invalid("manifest", fmt.Sprintf("maior que %d KiB", PluginManifestMaxBytes>>10))
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, Invalid("manifest", "JSON inválido: "+err.Error())
	}
	if dec.More() {
		return m, Invalid("manifest", "conteúdo depois do objeto")
	}
	return m, m.Validate()
}

// Validate confere o manifesto inteiro.
func (m *PluginManifest) Validate() error {
	if !ValidPluginID(m.ID) {
		return Invalid("id", "use ^[a-z][a-z0-9-]{1,62}$")
	}
	if m.APIVersion != PluginAPIVersion {
		return Invalid("api_version", fmt.Sprintf("este Hanko fala a versão %d do protocolo de plugins", PluginAPIVersion))
	}
	if !semverRe.MatchString(m.Version) || len(m.Version) > 64 {
		return Invalid("version", "use semver (ex.: 1.2.3)")
	}
	if strings.TrimSpace(m.Name) == "" {
		return Invalid("name", "obrigatório")
	}
	for _, f := range []struct {
		name, v string
		max     int
	}{{"name", m.Name, 120}, {"description", m.Description, 1000}, {"author", m.Author, 200}, {"homepage", m.Homepage, 500}} {
		if err := limitText(f.name, f.v, f.max); err != nil {
			return err
		}
	}
	if m.Homepage != "" && !strings.HasPrefix(m.Homepage, "https://") && !strings.HasPrefix(m.Homepage, "http://") {
		return Invalid("homepage", "use uma URL http(s)")
	}
	if err := validPluginCommand(m.Command); err != nil {
		return err
	}
	if len(m.Args) > PluginMaxArgs {
		return Invalid("args", fmt.Sprintf("até %d argumentos", PluginMaxArgs))
	}
	for _, a := range m.Args {
		if len(a) > 1024 || strings.ContainsRune(a, 0) {
			return Invalid("args", "argumento inválido")
		}
	}
	if err := m.validateCapabilities(); err != nil {
		return err
	}
	if err := m.ConfigSchema.validate(); err != nil {
		return err
	}
	if err := m.validateTools(); err != nil {
		return err
	}
	if err := m.validateLists(); err != nil {
		return err
	}
	return nil
}

func validPluginCommand(c string) error {
	switch {
	case strings.TrimSpace(c) == "":
		return Invalid("command", "obrigatório")
	case len(c) > 256, strings.ContainsRune(c, 0):
		return Invalid("command", "inválido")
	case strings.HasPrefix(c, "/"):
		return Invalid("command", "use um caminho relativo ao diretório do plugin")
	}
	for _, part := range strings.Split(strings.ReplaceAll(c, `\`, "/"), "/") {
		if part == ".." {
			return Invalid("command", "o comando precisa ficar dentro do diretório do plugin")
		}
	}
	return nil
}

func (m *PluginManifest) validateCapabilities() error {
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if !ValidPluginCapability(c) {
			return Invalid("capabilities", fmt.Sprintf("capacidade desconhecida %q", c))
		}
		if seen[c] {
			return Invalid("capabilities", fmt.Sprintf("capacidade repetida %q", c))
		}
		seen[c] = true
	}
	need := func(field string, n int, cap string) error {
		if n > 0 && !seen[cap] {
			return Invalid(field, fmt.Sprintf("exige a capacidade %s", cap))
		}
		return nil
	}
	if err := need("tools", len(m.Tools), CapAgentTools); err != nil {
		return err
	}
	if err := need("events", len(m.Events), CapEventsSubscribe); err != nil {
		return err
	}
	if err := need("widgets", len(m.Widgets), CapWidgets); err != nil {
		return err
	}
	return need("jobs", len(m.Jobs), CapJobs)
}

func (s PluginConfigSchema) validate() error {
	if s.Type != "" && s.Type != "object" {
		return Invalid("config_schema", `type precisa ser "object"`)
	}
	if len(s.Properties) > PluginMaxConfigFields {
		return Invalid("config_schema", fmt.Sprintf("até %d campos", PluginMaxConfigFields))
	}
	for k, f := range s.Properties {
		field := "config_schema." + k
		if !pluginKeyRe.MatchString(k) {
			return Invalid(field, "nome inválido (use ^[a-z][a-z0-9_-]{0,47}$)")
		}
		switch f.Type {
		case ConfigString, ConfigNumber, ConfigBoolean:
		default:
			return Invalid(field, "type precisa ser string, number ou boolean")
		}
		if len(f.Enum) > 0 && f.Type != ConfigString {
			return Invalid(field, "enum só em campo string")
		}
		if len(f.Enum) > 50 {
			return Invalid(field, "até 50 opções")
		}
		if f.Secret && (f.Type != ConfigString || len(f.Enum) > 0) {
			return Invalid(field, "secret só em campo string livre")
		}
		if f.Secret && f.Default != nil {
			return Invalid(field, "campo secreto não tem valor padrão")
		}
		if err := limitText(field+".title", f.Title, 120); err != nil {
			return err
		}
		if err := limitText(field+".description", f.Description, 500); err != nil {
			return err
		}
		for _, e := range f.Enum {
			if err := limitText(field+".enum", e, 200); err != nil {
				return err
			}
		}
		if f.Default != nil {
			if _, err := f.Coerce(f.Default); err != nil {
				return Invalid(field+".default", err.Error())
			}
		}
	}
	for _, r := range s.Required {
		if _, ok := s.Properties[r]; !ok {
			return Invalid("config_schema.required", fmt.Sprintf("campo %q não existe", r))
		}
	}
	return nil
}

// Coerce converte v (vindo de JSON ou de formulário) no tipo do campo. Texto vazio vale como ausente.
func (f PluginConfigField) Coerce(v any) (any, error) {
	switch f.Type {
	case ConfigString:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("esperado texto")
		}
		if len(s) > PluginConfigValueMax || !utf8.ValidString(s) {
			return nil, fmt.Errorf("texto inválido ou maior que %d KiB", PluginConfigValueMax>>10)
		}
		if len(f.Enum) > 0 && !slices.Contains(f.Enum, s) {
			return nil, fmt.Errorf("use uma das opções: %s", strings.Join(f.Enum, ", "))
		}
		return s, nil
	case ConfigNumber:
		switch n := v.(type) {
		case float64:
			return n, nil
		case int:
			return float64(n), nil
		case json.Number:
			return n.Float64()
		case string:
			x, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
			if err != nil {
				return nil, fmt.Errorf("esperado número")
			}
			return x, nil
		}
		return nil, fmt.Errorf("esperado número")
	case ConfigBoolean:
		switch b := v.(type) {
		case bool:
			return b, nil
		case string:
			switch strings.TrimSpace(strings.ToLower(b)) {
			case "true", "on", "1", "sim":
				return true, nil
			case "false", "off", "0", "", "não", "nao":
				return false, nil
			}
		}
		return nil, fmt.Errorf("esperado verdadeiro ou falso")
	}
	return nil, fmt.Errorf("tipo desconhecido")
}

func (m *PluginManifest) validateTools() error {
	if len(m.Tools) > PluginMaxTools {
		return Invalid("tools", fmt.Sprintf("até %d tools", PluginMaxTools))
	}
	seen := map[string]bool{}
	for i := range m.Tools {
		t := &m.Tools[i]
		field := "tools." + t.Name
		if !pluginNameRe.MatchString(t.Name) {
			return Invalid("tools", fmt.Sprintf("nome de tool inválido %q (use ^[a-z][a-z0-9_]{0,47}$)", t.Name))
		}
		if seen[t.Name] {
			return Invalid("tools", fmt.Sprintf("tool repetida %q", t.Name))
		}
		seen[t.Name] = true
		if strings.TrimSpace(t.Description) == "" {
			return Invalid(field+".description", "obrigatória")
		}
		if err := limitText(field+".description", t.Description, 2000); err != nil {
			return err
		}
		if t.TimeoutSec < 0 || t.TimeoutSec > PluginToolTimeoutMaxSec {
			return Invalid(field+".timeout_sec", fmt.Sprintf("entre 1 e %d", PluginToolTimeoutMaxSec))
		}
		if len(t.InputSchema) == 0 || string(t.InputSchema) == "null" {
			t.InputSchema = json.RawMessage(`{"type":"object"}`)
		}
		if len(t.InputSchema) > PluginToolSchemaMax {
			return Invalid(field+".input_schema", fmt.Sprintf("maior que %d KiB", PluginToolSchemaMax>>10))
		}
		var sch map[string]any
		if err := json.Unmarshal(t.InputSchema, &sch); err != nil {
			return Invalid(field+".input_schema", "precisa ser um objeto JSON Schema")
		}
		if sch["type"] != "object" {
			return Invalid(field+".input_schema", `type precisa ser "object"`)
		}
	}
	return nil
}

func (m *PluginManifest) validateLists() error {
	if len(m.Events) > PluginMaxEvents {
		return Invalid("events", fmt.Sprintf("até %d eventos", PluginMaxEvents))
	}
	seen := map[string]bool{}
	for _, e := range m.Events {
		if !eventRe.MatchString(e) || len(e) > 100 {
			return Invalid("events", fmt.Sprintf("evento inválido %q (ex.: task.created)", e))
		}
		if seen[e] {
			return Invalid("events", fmt.Sprintf("evento repetido %q", e))
		}
		seen[e] = true
	}
	if len(m.Widgets) > PluginMaxWidgets {
		return Invalid("widgets", fmt.Sprintf("até %d widgets", PluginMaxWidgets))
	}
	seen = map[string]bool{}
	for _, w := range m.Widgets {
		if !pluginKeyRe.MatchString(w.Key) {
			return Invalid("widgets", fmt.Sprintf("key inválida %q", w.Key))
		}
		if seen[w.Key] {
			return Invalid("widgets", fmt.Sprintf("widget repetido %q", w.Key))
		}
		seen[w.Key] = true
		if strings.TrimSpace(w.Title) == "" {
			return Invalid("widgets."+w.Key+".title", "obrigatório")
		}
		if err := limitText("widgets."+w.Key+".title", w.Title, 120); err != nil {
			return err
		}
		if !ValidWidgetPlacement(w.Placement) {
			return Invalid("widgets."+w.Key+".placement", "use dashboard, task ou persona")
		}
	}
	if len(m.Jobs) > PluginMaxJobs {
		return Invalid("jobs", fmt.Sprintf("até %d jobs", PluginMaxJobs))
	}
	seen = map[string]bool{}
	for _, j := range m.Jobs {
		if !pluginKeyRe.MatchString(j.Key) {
			return Invalid("jobs", fmt.Sprintf("key inválida %q", j.Key))
		}
		if seen[j.Key] {
			return Invalid("jobs", fmt.Sprintf("job repetido %q", j.Key))
		}
		seen[j.Key] = true
		if j.EveryMinutes < PluginJobMinMinutes || j.EveryMinutes > PluginJobMaxMinutes {
			return Invalid("jobs."+j.Key+".every_minutes", fmt.Sprintf("entre %d e %d", PluginJobMinMinutes, PluginJobMaxMinutes))
		}
	}
	return nil
}

// Tool devolve a tool pelo nome.
func (m PluginManifest) Tool(name string) (PluginTool, bool) {
	for _, t := range m.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return PluginTool{}, false
}

// Widget devolve o widget pela key.
func (m PluginManifest) Widget(key string) (PluginWidget, bool) {
	for _, w := range m.Widgets {
		if w.Key == key {
			return w, true
		}
	}
	return PluginWidget{}, false
}

// Job devolve o job pela key.
func (m PluginManifest) Job(key string) (PluginJob, bool) {
	for _, j := range m.Jobs {
		if j.Key == key {
			return j, true
		}
	}
	return PluginJob{}, false
}

// PluginToolPrefix é o prefixo das tools de plugin no MCP: plugin_<id>_<tool>. O id não tem "_", então o
// primeiro "_" depois do prefixo separa o id do nome da tool.
const PluginToolPrefix = "plugin_"

// PluginToolName monta o nome MCP da tool.
func PluginToolName(pluginID, tool string) string { return PluginToolPrefix + pluginID + "_" + tool }

// ParsePluginToolName separa id do plugin e nome da tool.
func ParsePluginToolName(name string) (pluginID, tool string, ok bool) {
	rest, ok := strings.CutPrefix(name, PluginToolPrefix)
	if !ok {
		return "", "", false
	}
	pluginID, tool, ok = strings.Cut(rest, "_")
	if !ok || !ValidPluginID(pluginID) || !pluginNameRe.MatchString(tool) {
		return "", "", false
	}
	return pluginID, tool, true
}

// CompareSemver compara duas versões semver (a pré-versão vale menos que a versão final; build é ignorado).
// Versões inválidas comparam como texto.
func CompareSemver(a, b string) int {
	pa, oka := semverParts(a)
	pb, okb := semverParts(b)
	if !oka || !okb {
		return strings.Compare(a, b)
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			if pa.n[i] < pb.n[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	}
	return strings.Compare(pa.pre, pb.pre)
}

type semver struct {
	n   [3]uint64
	pre string
}

func semverParts(v string) (semver, bool) {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return semver{}, false
	}
	var s semver
	for i := 0; i < 3; i++ {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return semver{}, false
		}
		s.n[i] = n
	}
	s.pre = strings.TrimPrefix(m[4], "-")
	return s, true
}

// Plugin é uma instalação de plugin (nível da instalação, não da empresa).
type Plugin struct {
	ID                  ID             `json:"id"`
	Key                 string         `json:"key"`
	Version             string         `json:"version"`
	APIVersion          int            `json:"api_version"`
	Manifest            PluginManifest `json:"manifest"`
	PendingManifest     *PluginManifest
	Dir                 string       `json:"dir"`
	Status              PluginStatus `json:"status"`
	GrantedCapabilities []string     `json:"granted_capabilities"`
	PendingCapabilities []string     `json:"pending_capabilities"`
	LastError           string       `json:"last_error"`
	InstalledBy         *ID          `json:"installed_by,omitempty"`
	InstalledAt         time.Time    `json:"installed_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

// Has informa se a capacidade c foi concedida.
func (p Plugin) Has(c string) bool { return slices.Contains(p.GrantedCapabilities, c) }

// PluginLog é uma linha do log de um plugin.
type PluginLog struct {
	ID        ID        `json:"id"`
	CompanyID *ID       `json:"company_id,omitempty"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Níveis de log de plugin.
const (
	PluginLogDebug  = "debug"
	PluginLogInfo   = "info"
	PluginLogWarn   = "warn"
	PluginLogError  = "error"
	PluginLogStderr = "stderr"
	PluginLogHost   = "host"
)

// ValidPluginLogLevel informa se l é um nível que o plugin pode usar.
func ValidPluginLogLevel(l string) bool {
	return l == PluginLogDebug || l == PluginLogInfo || l == PluginLogWarn || l == PluginLogError
}

// PluginJobRun é uma execução de job de plugin.
type PluginJobRun struct {
	ID         ID         `json:"id"`
	CompanyID  ID         `json:"company_id"`
	JobKey     string     `json:"job_key"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Estados de uma execução de job.
const (
	PluginJobRunning = "running"
	PluginJobDone    = "done"
	PluginJobFailed  = "failed"
)
