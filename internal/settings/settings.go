// Package settings mantém os toggles NÃO sensíveis da instalação (ADR-0050 §C). Um registro em código declara cada
// chave (tipo, padrão, variável de ambiente equivalente). O valor efetivo segue a precedência
// env > banco > yaml/padrão; a variável de ambiente definida trava a chave. Segredos nunca são chaves registradas.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind é o tipo do valor de uma chave.
type Kind string

const (
	Bool       Kind = "bool"
	Int        Kind = "int"
	Float      Kind = "float"
	Duration   Kind = "duration"
	StringList Kind = "string_list"
)

// Chaves registradas.
const (
	ACPAllow               = "acp.allow"
	ACPCommands            = "acp.commands"
	IntegrationsAllowStdio = "integrations.allow_stdio"
	IntegrationsAllowPriv  = "integrations.allow_private"
	ArtifactsMaxBytes      = "artifacts.max_bytes"
	ArtifactsQuotaBytes    = "artifacts.quota_bytes"
	RunReserveUSD          = "run.reserve_usd"
	AlertsSilentFactor     = "alerts.silent_factor"
	AlertsRunStuckAfter    = "alerts.run_stuck_after"
	RateLimitEnabled       = "ratelimit.enabled"
	RateLimitWrites        = "ratelimit.writes_per_min"
	RateLimitIP            = "ratelimit.ip_per_min"
	RateLimitMCP           = "ratelimit.mcp_per_min"
	RateLimitWebhooks      = "ratelimit.webhooks_per_min"
	RateLimitBrowserErrors = "ratelimit.browser_errors_per_min"
	SecretsHistoryVersions = "secrets.history_keep_versions"
	SecretsHistoryDays     = "secrets.history_keep_days"
)

// Def declara uma chave.
type Def struct {
	Key   string
	Group string // acp | integrations | artifacts | run | alerts | ratelimit | experimental
	Kind  Kind
	Env   string // variável de ambiente equivalente; definida, ela vence e trava a chave
	Label string // texto pt-BR (a UI traduz pelo i18n)
	Help  string
	Min   float64 // limites de int/float (Max 0 = sem teto)
	Max   float64
}

// Defs é o registro. Ordem de exibição.
var Defs = []Def{
	{Key: ACPAllow, Group: "acp", Kind: Bool, Env: "HANKO_ACP_ALLOW", Label: "Liberar o harness ACP", Help: "Personas acp executam comandos nesta máquina."},
	{Key: ACPCommands, Group: "acp", Kind: StringList, Env: "HANKO_ACP_COMMANDS", Label: "Executáveis ACP permitidos", Help: "Vazio = qualquer comando (com o harness liberado)."},
	{Key: IntegrationsAllowStdio, Group: "integrations", Kind: Bool, Env: "HANKO_INTEGRATIONS_ALLOW_STDIO", Label: "Liberar MCP stdio", Help: "Servidores MCP stdio executam comandos nesta máquina."},
	{Key: IntegrationsAllowPriv, Group: "integrations", Kind: Bool, Env: "HANKO_INTEGRATIONS_ALLOW_PRIVATE", Label: "Liberar destinos internos", Help: "Webhooks e testes para loopback e redes privadas."},
	{Key: ArtifactsMaxBytes, Group: "artifacts", Kind: Int, Env: "HANKO_ARTIFACT_MAX_BYTES", Label: "Tamanho máximo de artefato (bytes)", Min: 1, Max: 64 << 20},
	{Key: ArtifactsQuotaBytes, Group: "artifacts", Kind: Int, Env: "HANKO_ARTIFACT_QUOTA_BYTES", Label: "Cota de artefatos por empresa (bytes)", Min: 1},
	{Key: RunReserveUSD, Group: "run", Kind: Float, Env: "HANKO_RUN_RESERVE_USD", Label: "Piso da reserva de orçamento por run (US$)", Min: 0, Max: 1000},
	{Key: AlertsSilentFactor, Group: "alerts", Kind: Int, Env: "HANKO_ALERT_SILENT_FACTOR", Label: "Fator do alerta de persona silenciosa", Min: 1, Max: 1000},
	{Key: AlertsRunStuckAfter, Group: "alerts", Kind: Duration, Env: "HANKO_ALERT_RUN_STUCK_AFTER", Label: "Tempo para uma run presa virar alerta", Min: 1},
	{Key: RateLimitEnabled, Group: "ratelimit", Kind: Bool, Env: "HANKO_RATELIMIT_ENABLED", Label: "Ligar o rate limit geral", Help: "Token bucket por API key, usuário e IP para escritas, MCP e webhooks de entrada (429 + Retry-After)."},
	{Key: RateLimitWrites, Group: "ratelimit", Kind: Int, Env: "HANKO_RATELIMIT_WRITES_PER_MIN", Label: "Escritas por minuto (por API key ou usuário)", Min: 10, Max: 1000000},
	{Key: RateLimitIP, Group: "ratelimit", Kind: Int, Env: "HANKO_RATELIMIT_IP_PER_MIN", Label: "Escritas por minuto (por IP, somando identidades)", Min: 10, Max: 1000000},
	{Key: RateLimitMCP, Group: "ratelimit", Kind: Int, Env: "HANKO_RATELIMIT_MCP_PER_MIN", Label: "Chamadas MCP por minuto (por identidade ou run)", Min: 10, Max: 1000000},
	{Key: RateLimitWebhooks, Group: "ratelimit", Kind: Int, Env: "HANKO_RATELIMIT_WEBHOOKS_PER_MIN", Label: "Webhooks de entrada por minuto (por IP)", Min: 1, Max: 1000000},
	{Key: RateLimitBrowserErrors, Group: "ratelimit", Kind: Int, Env: "HANKO_RATELIMIT_BROWSER_ERRORS_PER_MIN", Label: "Erros de navegador por minuto (por IP)", Min: 1, Max: 100000},
	{Key: SecretsHistoryVersions, Group: "secrets", Kind: Int, Env: "HANKO_SECRETS_HISTORY_KEEP_VERSIONS", Label: "Versões de histórico por campo", Help: "Retenção do histórico de cada campo: as últimas N versões ou as de até D dias, o que guardar menos (ADR-0068 §3.2).", Min: 1, Max: 1000},
	{Key: SecretsHistoryDays, Group: "secrets", Kind: Int, Env: "HANKO_SECRETS_HISTORY_KEEP_DAYS", Label: "Dias de histórico por campo", Min: 1, Max: 3650},
}

var (
	// ErrUnknownKey: a chave não está no registro (só o registro é configurável por aqui).
	ErrUnknownKey = errors.New("chave de configuração desconhecida")
	// ErrLocked: a variável de ambiente da chave está definida e vence o banco.
	ErrLocked = errors.New("a chave está travada por variável de ambiente")

	credentialKey = regexp.MustCompile(`(?i)(secret|token|password|passwd|credential|api_?key|private_?key|access_?key)`)
)

// Lookup devolve a definição da chave.
func Lookup(key string) (Def, bool) {
	for _, d := range Defs {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

// SensitiveName informa se o nome de uma chave parece credencial (o teste do registro usa isto).
func SensitiveName(key string) bool {
	if key == SecretsHistoryVersions || key == SecretsHistoryDays {
		return false // retenção do histórico do cofre (ADR-0068 §3.2): inteiros, não credenciais
	}
	return credentialKey.MatchString(key)
}

// Source diz de onde veio o valor efetivo.
type Source string

const (
	FromEnv     Source = "env"
	FromDB      Source = "db"
	FromDefault Source = "default"
)

// Entry é uma chave com o valor efetivo, para telas e API.
type Entry struct {
	Def
	Value  any // bool | int64 | float64 | time.Duration | []string
	Source Source
	Locked bool // variável de ambiente definida
	// Stored é o valor guardado no banco (nil = nenhum), mesmo quando travado.
	Stored any
}

// Manager guarda o valor efetivo de cada chave. Seguro para uso concorrente.
type Manager struct {
	mu       sync.RWMutex
	base     map[string]any // valor de env/yaml/padrão, já tipado
	envSet   map[string]bool
	db       map[string]any
	maxBytes int64 // teto de artifacts.max_bytes para o driver ativo
}

// NewManager monta o gerenciador. base traz o valor efetivo de cada chave vindo da configuração (env > yaml > padrão),
// e envSet marca as variáveis de ambiente definidas (as que travam). Chaves ausentes de base usam o zero do tipo.
func NewManager(base map[string]any, envSet map[string]bool) *Manager {
	m := &Manager{base: map[string]any{}, envSet: map[string]bool{}, db: map[string]any{}, maxBytes: 16 << 20}
	for k, v := range base {
		m.base[k] = v
	}
	for k, v := range envSet {
		m.envSet[k] = v
	}
	return m
}

// SetMaxArtifactBytes define o teto aceito em artifacts.max_bytes (16 MiB no postgres, 64 MiB com s3).
func (m *Manager) SetMaxArtifactBytes(n int64) {
	m.mu.Lock()
	m.maxBytes = n
	m.mu.Unlock()
}

// Locked informa se a variável de ambiente da chave está definida.
func (m *Manager) Locked(key string) bool {
	d, ok := Lookup(key)
	if !ok {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return d.Env != "" && m.envSet[d.Env]
}

// Load substitui os valores do banco (JSON bruto por chave). Chaves desconhecidas ou inválidas são ignoradas e
// devolvidas em skipped (um valor ruim no banco nunca derruba a instalação: vale o de env/padrão).
func (m *Manager) Load(rows map[string]json.RawMessage) (skipped []string) {
	db := map[string]any{}
	for k, raw := range rows {
		d, ok := Lookup(k)
		if !ok {
			skipped = append(skipped, k)
			continue
		}
		v, err := Parse(d, raw)
		if err != nil {
			skipped = append(skipped, k)
			continue
		}
		db[k] = v
	}
	m.mu.Lock()
	m.db = db
	m.mu.Unlock()
	slices.Sort(skipped)
	return skipped
}

// Effective devolve o valor efetivo e a origem.
func (m *Manager) Effective(key string) (any, Source) {
	d, ok := Lookup(key)
	if !ok {
		return nil, FromDefault
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if d.Env != "" && m.envSet[d.Env] {
		return m.zeroIfNil(d, m.base[key]), FromEnv
	}
	if v, ok := m.db[key]; ok {
		return v, FromDB
	}
	return m.zeroIfNil(d, m.base[key]), FromDefault
}

func (m *Manager) zeroIfNil(d Def, v any) any {
	if v != nil {
		return v
	}
	switch d.Kind {
	case Bool:
		return false
	case Int:
		return int64(0)
	case Float:
		return float64(0)
	case Duration:
		return time.Duration(0)
	default:
		return []string(nil)
	}
}

// Bool devolve o valor efetivo de uma chave bool.
func (m *Manager) Bool(key string) bool { v, _ := m.Effective(key); b, _ := v.(bool); return b }

// Int devolve o valor efetivo de uma chave int.
func (m *Manager) Int(key string) int64 { v, _ := m.Effective(key); n, _ := v.(int64); return n }

// Float devolve o valor efetivo de uma chave float.
func (m *Manager) Float(key string) float64 { v, _ := m.Effective(key); f, _ := v.(float64); return f }

// Duration devolve o valor efetivo de uma chave duration.
func (m *Manager) Duration(key string) time.Duration {
	v, _ := m.Effective(key)
	d, _ := v.(time.Duration)
	return d
}

// Strings devolve o valor efetivo de uma chave string_list.
func (m *Manager) Strings(key string) []string {
	v, _ := m.Effective(key)
	s, _ := v.([]string)
	return slices.Clone(s)
}

// Entries lista todas as chaves com o valor efetivo.
func (m *Manager) Entries() []Entry {
	out := make([]Entry, 0, len(Defs))
	for _, d := range Defs {
		v, src := m.Effective(d.Key)
		m.mu.RLock()
		stored := m.db[d.Key]
		locked := d.Env != "" && m.envSet[d.Env]
		m.mu.RUnlock()
		out = append(out, Entry{Def: d, Value: v, Source: src, Locked: locked, Stored: stored})
	}
	return out
}

// Check valida um valor para a chave, devolvendo-o tipado. Aplica o teto de artifacts.max_bytes do driver.
func (m *Manager) Check(key string, raw json.RawMessage) (any, error) {
	d, ok := Lookup(key)
	if !ok {
		return nil, ErrUnknownKey
	}
	v, err := Parse(d, raw)
	if err != nil {
		return nil, err
	}
	if key == ArtifactsMaxBytes {
		m.mu.RLock()
		limit := m.maxBytes
		m.mu.RUnlock()
		if n := v.(int64); n > limit {
			return nil, fmt.Errorf("no máximo %d bytes com o driver de blobs atual", limit)
		}
	}
	return v, nil
}

// Parse decodifica e valida o JSON de um valor conforme a definição.
func Parse(d Def, raw json.RawMessage) (any, error) {
	switch d.Kind {
	case Bool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, errors.New("esperado verdadeiro ou falso")
		}
		return b, nil
	case Int:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || f != float64(int64(f)) {
			return nil, errors.New("esperado um número inteiro")
		}
		n := int64(f)
		if float64(n) < d.Min || (d.Max > 0 && float64(n) > d.Max) {
			return nil, rangeErr(d)
		}
		return n, nil
	case Float:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, errors.New("esperado um número")
		}
		if f < d.Min || (d.Max > 0 && f > d.Max) || f != f {
			return nil, rangeErr(d)
		}
		return f, nil
	case Duration:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, errors.New(`esperado um texto de duração (ex.: "15m")`)
		}
		dur, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil || dur <= 0 || dur > 365*24*time.Hour {
			return nil, errors.New(`duração inválida (ex.: "15m", entre 1ns e 1 ano)`)
		}
		return dur, nil
	case StringList:
		var l []string
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, errors.New("esperado uma lista de textos")
		}
		if len(l) > 100 {
			return nil, errors.New("no máximo 100 itens")
		}
		out := make([]string, 0, len(l))
		for _, s := range l {
			if s = strings.TrimSpace(s); s != "" {
				if len(s) > 500 || strings.ContainsAny(s, "\x00\r\n") {
					return nil, errors.New("item inválido")
				}
				out = append(out, s)
			}
		}
		return out, nil
	}
	return nil, ErrUnknownKey
}

func rangeErr(d Def) error {
	if d.Max > 0 {
		return fmt.Errorf("fora do intervalo (de %g a %g)", d.Min, d.Max)
	}
	return fmt.Errorf("fora do intervalo (mínimo %g)", d.Min)
}

// ToJSON serializa um valor tipado do manager para o JSON guardado no banco.
func ToJSON(v any) json.RawMessage {
	if d, ok := v.(time.Duration); ok {
		v = d.String()
	}
	b, _ := json.Marshal(v)
	return b
}

// FromText converte o texto digitado (CLI, formulário) para o JSON da chave: "true", "42", "15m", "a,b".
func FromText(d Def, text string) (json.RawMessage, error) {
	text = strings.TrimSpace(text)
	switch d.Kind {
	case Bool:
		b, err := strconv.ParseBool(text)
		if err != nil {
			return nil, errors.New("use true ou false")
		}
		return ToJSON(b), nil
	case Int:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, errors.New("use um número inteiro")
		}
		return ToJSON(n), nil
	case Float:
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, errors.New("use um número")
		}
		return ToJSON(f), nil
	case Duration:
		return ToJSON(text), nil
	default:
		var l []string
		for _, s := range strings.Split(text, ",") {
			if s = strings.TrimSpace(s); s != "" {
				l = append(l, s)
			}
		}
		if l == nil {
			l = []string{}
		}
		return ToJSON(l), nil
	}
}

// Display formata um valor efetivo para pessoas.
func Display(v any) string {
	switch x := v.(type) {
	case []string:
		if len(x) == 0 {
			return "(vazio)"
		}
		return strings.Join(x, ", ")
	case time.Duration:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}
