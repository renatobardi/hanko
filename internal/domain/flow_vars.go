package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Variáveis e gatilhos de fluxo (ADR-0047). Tudo aqui é puro: sem banco, sem relógio, sem rede.

// Limites das variáveis de fluxo.
const (
	MaxFlowVariables     = 20
	MaxFlowVariableValue = 2000
	MaxFlowVariableLabel = 80
	MaxFlowTriggers      = 20
	MaxFlowTriggerName   = 80
	MaxFlowPayloadBytes  = 256 << 10
)

// FlowFireSource é de onde veio um disparo.
type FlowFireSource string

// Origens de disparo.
const (
	FireSchedule FlowFireSource = "schedule"
	FireManual   FlowFireSource = "manual"
	FireWebhook  FlowFireSource = "webhook"
	FireEvent    FlowFireSource = "event"
)

// Valid diz se s é uma origem conhecida.
func (s FlowFireSource) Valid() bool {
	switch s {
	case FireSchedule, FireManual, FireWebhook, FireEvent:
		return true
	}
	return false
}

// Label é o valor da variável built-in {{disparo}}.
func (s FlowFireSource) Label() string {
	switch s {
	case FireManual:
		return "manual"
	case FireWebhook:
		return "webhook"
	case FireEvent:
		return "evento"
	}
	return "agenda"
}

// FlowVariable é uma variável declarada de um fluxo.
type FlowVariable struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`
}

// BuiltinFlowVars são os nomes reservados, preenchidos pelo servidor a cada disparo.
var BuiltinFlowVars = []string{"data", "n", "semana", "disparo"}

// IsBuiltinFlowVar diz se name é uma variável built-in.
func IsBuiltinFlowVar(name string) bool {
	for _, b := range BuiltinFlowVars {
		if b == name {
			return true
		}
	}
	return false
}

var (
	flowVarNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	flowVarRefRE  = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
)

// ValidFlowVarName diz se name é um nome de variável aceitável.
func ValidFlowVarName(name string) bool { return flowVarNameRE.MatchString(name) }

// NormalizeFlowVariables apara e valida a lista declarada. Devolve a lista limpa (nunca nil).
func NormalizeFlowVariables(in []FlowVariable) ([]FlowVariable, error) {
	if len(in) > MaxFlowVariables {
		return nil, Invalid("variables", fmt.Sprintf("no máximo %d variáveis", MaxFlowVariables))
	}
	out := make([]FlowVariable, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v.Name = strings.TrimSpace(v.Name)
		v.Label = strings.TrimSpace(v.Label)
		switch {
		case !ValidFlowVarName(v.Name):
			return nil, Invalid("variables", fmt.Sprintf("nome %q inválido: use minúsculas, dígitos e _, começando por letra (até 40)", v.Name))
		case IsBuiltinFlowVar(v.Name):
			return nil, Invalid("variables", fmt.Sprintf("%q é uma variável embutida (%s) e não pode ser declarada", v.Name, strings.Join(BuiltinFlowVars, ", ")))
		case seen[v.Name]:
			return nil, Invalid("variables", fmt.Sprintf("variável %q repetida", v.Name))
		case utf8.RuneCountInString(v.Label) > MaxFlowVariableLabel:
			return nil, Invalid("variables", fmt.Sprintf("o rótulo de %q passa de %d caracteres", v.Name, MaxFlowVariableLabel))
		case utf8.RuneCountInString(v.Default) > MaxFlowVariableValue:
			return nil, Invalid("variables", fmt.Sprintf("o padrão de %q passa de %d caracteres", v.Name, MaxFlowVariableValue))
		}
		seen[v.Name] = true
		out = append(out, v)
	}
	return out, nil
}

// FlowVarRefs lista, sem repetir e na ordem de aparição, os nomes citados como {{nome}} em tpl.
func FlowVarRefs(tpl string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range flowVarRefRE.FindAllStringSubmatch(tpl, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// CheckFlowTemplate recusa um modelo que cita {{x}} sem ser built-in nem declarada.
func CheckFlowTemplate(field, tpl string, vars []FlowVariable) error {
	declared := map[string]bool{}
	for _, v := range vars {
		declared[v.Name] = true
	}
	var unknown []string
	for _, n := range FlowVarRefs(tpl) {
		if !IsBuiltinFlowVar(n) && !declared[n] {
			unknown = append(unknown, "{{"+n+"}}")
		}
	}
	if len(unknown) > 0 {
		return Invalid(field, "variável não declarada: "+strings.Join(unknown, ", "))
	}
	return nil
}

// CheckScheduleVariables: a agenda não tem quem preencha, então toda variável obrigatória precisa de padrão.
func CheckScheduleVariables(vars []FlowVariable) error {
	var missing []string
	for _, v := range vars {
		if v.Required && strings.TrimSpace(v.Default) == "" {
			missing = append(missing, v.Name)
		}
	}
	if len(missing) > 0 {
		return Invalid("variables", "a agenda exige um valor padrão para as variáveis obrigatórias: "+strings.Join(missing, ", "))
	}
	return nil
}

// ResolveFlowVariables aplica valor informado, depois o padrão, a cada variável declarada. Faltando obrigatória, devolve
// ErrInvalid listando todas. strict recusa valor para variável não declarada (disparo manual); sem strict o excedente é
// ignorado (webhook e evento recebem carga arbitrária). Valores passam de MaxFlowVariableValue → erro.
func ResolveFlowVariables(decl []FlowVariable, given map[string]string, strict bool) (map[string]string, error) {
	out := make(map[string]string, len(decl))
	known := map[string]bool{}
	var missing []string
	for _, v := range decl {
		known[v.Name] = true
		val := strings.TrimSpace(given[v.Name])
		if val == "" {
			val = v.Default
		}
		if val == "" && v.Required {
			missing = append(missing, v.Name)
			continue
		}
		if utf8.RuneCountInString(val) > MaxFlowVariableValue {
			return nil, Invalid("variables", fmt.Sprintf("o valor de %q passa de %d caracteres", v.Name, MaxFlowVariableValue))
		}
		out[v.Name] = val
	}
	if strict {
		var extra []string
		for k := range given {
			if !known[k] {
				extra = append(extra, k)
			}
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			return nil, Invalid("variables", "variável não declarada: "+strings.Join(extra, ", "))
		}
	}
	if len(missing) > 0 {
		return nil, Invalid("variables", "faltam variáveis obrigatórias: "+strings.Join(missing, ", "))
	}
	return out, nil
}

// FlowBuiltins monta os valores das variáveis embutidas de um disparo: data e semana ISO no fuso loc.
func FlowBuiltins(at time.Time, loc *time.Location, n int64, src FlowFireSource) map[string]string {
	if loc == nil {
		loc = time.UTC
	}
	t := at.In(loc)
	y, w := t.ISOWeek()
	return map[string]string{
		"data":    t.Format("2006-01-02"),
		"n":       strconv.FormatInt(n, 10),
		"semana":  fmt.Sprintf("%04d-W%02d", y, w),
		"disparo": src.Label(),
	}
}

// RenderFlowTemplate troca {{nome}} pelos valores em UMA passada: o valor entra como texto e não é expandido de novo.
// Nome sem valor (nem built-in nem declarado) fica como está.
func RenderFlowTemplate(tpl string, builtins, vars map[string]string) string {
	if !strings.Contains(tpl, "{{") {
		return tpl
	}
	return flowVarRefRE.ReplaceAllStringFunc(tpl, func(m string) string {
		name := flowVarRefRE.FindStringSubmatch(m)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		if v, ok := builtins[name]; ok {
			return v
		}
		return m
	})
}

// ---------- gatilhos ----------

// FlowTriggerKind é o tipo de um gatilho de fluxo.
type FlowTriggerKind string

// Tipos de gatilho.
const (
	TriggerKindWebhook FlowTriggerKind = "webhook"
	TriggerKindEvent   FlowTriggerKind = "event"
)

// Valid diz se k é um tipo conhecido.
func (k FlowTriggerKind) Valid() bool { return k == TriggerKindWebhook || k == TriggerKindEvent }

// Limites e padrões do rate limit por gatilho.
const (
	DefaultTriggerRatePerMin = 30
	MaxTriggerRatePerMin     = 600
)

// FlowTrigger é um gatilho de fluxo (a agenda mora no próprio fluxo).
type FlowTrigger struct {
	ID          ID                `json:"id"`
	CompanyID   ID                `json:"company_id"`
	FlowID      ID                `json:"flow_id"`
	Kind        FlowTriggerKind   `json:"kind"`
	Name        string            `json:"name"`
	Enabled     bool              `json:"enabled"`
	SecretRef   string            `json:"secret_ref,omitempty"` // ${secret:NOME} do HMAC (webhook)
	RatePerMin  int               `json:"rate_per_min"`
	EventVerb   string            `json:"event_verb,omitempty"`
	EventFilter map[string]string `json:"event_filter,omitempty"`
	Mapping     map[string]string `json:"mapping,omitempty"`
	LastFiredAt *time.Time        `json:"last_fired_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

var eventVerbRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*(\.\*)?$`)

// ValidEventVerb aceita "task.created" ou o prefixo "gate.*". Fatos flow.* são recusados (sem laço, ADR-0047 §B.3).
func ValidEventVerb(v string) error {
	if !eventVerbRE.MatchString(v) || len(v) > 80 || !strings.Contains(v, ".") {
		return Invalid("event_verb", "use um verbo de atividade como task.created ou um prefixo como gate.*")
	}
	if strings.HasPrefix(v, "flow.") {
		return Invalid("event_verb", "fatos de fluxo (flow.*) não disparam fluxos")
	}
	return nil
}

// MatchEventVerb diz se o verbo do fato casa com o padrão do gatilho (exato ou prefixo "x.*").
func MatchEventVerb(pattern, verb string) bool {
	if p, ok := strings.CutSuffix(pattern, ".*"); ok {
		return strings.HasPrefix(verb, p+".")
	}
	return pattern == verb
}

// NormalizeTriggerMapping valida {variável: caminho}: variável declarada ou built-in não, caminho não vazio.
func NormalizeTriggerMapping(m map[string]string, vars []FlowVariable) (map[string]string, error) {
	declared := map[string]bool{}
	for _, v := range vars {
		declared[v.Name] = true
	}
	out := make(map[string]string, len(m))
	for k, p := range m {
		k, p = strings.TrimSpace(k), strings.TrimSpace(p)
		if !declared[k] {
			return nil, Invalid("mapping", fmt.Sprintf("%q não é uma variável declarada do fluxo", k))
		}
		if p == "" || len(p) > 200 {
			return nil, Invalid("mapping", fmt.Sprintf("caminho de %q vazio ou longo demais", k))
		}
		out[k] = p
	}
	return out, nil
}

// NormalizeEventFilter valida o filtro {chave: texto} (até 10 pares).
func NormalizeEventFilter(f map[string]string) (map[string]string, error) {
	if len(f) > 10 {
		return nil, Invalid("event_filter", "no máximo 10 condições")
	}
	out := make(map[string]string, len(f))
	for k, v := range f {
		k = strings.TrimSpace(k)
		if k == "" || len(k) > 200 || len(v) > 500 {
			return nil, Invalid("event_filter", "chave vazia ou valor longo demais")
		}
		out[k] = v
	}
	return out, nil
}

// JSONPath lê um caminho pontuado ("a.b.0.c") de um documento decodificado de JSON. Números de lista indexam.
func JSONPath(doc any, path string) (any, bool) {
	cur := doc
	for _, part := range strings.Split(path, ".") {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// JSONText é o valor como texto de variável: string como está, número e booleano como no JSON, objeto e lista como
// JSON compacto, tudo cortado em MaxFlowVariableValue.
func JSONText(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s = x
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		s = strconv.FormatBool(x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		s = string(b)
	}
	if utf8.RuneCountInString(s) > MaxFlowVariableValue {
		s = string([]rune(s)[:MaxFlowVariableValue])
	}
	return s
}

// MapVariables extrai as variáveis declaradas de um documento: pelo mapping, ou pela chave de mesmo nome no topo.
func MapVariables(decl []FlowVariable, mapping map[string]string, doc any) map[string]string {
	out := map[string]string{}
	for _, v := range decl {
		path, ok := mapping[v.Name]
		if !ok {
			path = v.Name
		}
		if val, found := JSONPath(doc, path); found {
			if t := JSONText(val); t != "" {
				out[v.Name] = t
			}
		}
	}
	return out
}

// EventFilterMatches confere o filtro {caminho: texto} contra o documento do fato; filtro vazio casa tudo.
func EventFilterMatches(filter map[string]string, doc any) bool {
	for path, want := range filter {
		got, ok := JSONPath(doc, path)
		if !ok || JSONText(got) != want {
			return false
		}
	}
	return true
}

// FlowSnapshot é a definição de um fluxo como a revisão a guarda (sem segredos e sem ids de gatilho).
type FlowSnapshot struct {
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	Cron            string                `json:"cron"`
	Timezone        string                `json:"timezone"`
	PersonaID       ID                    `json:"persona_id"`
	ProjectID       *ID                   `json:"project_id,omitempty"`
	Priority        string                `json:"priority"`
	TaskTitle       string                `json:"task_title"`
	TaskDescription string                `json:"task_description"`
	Variables       []FlowVariable        `json:"variables"`
	Triggers        []FlowSnapshotTrigger `json:"triggers"`
}

// FlowSnapshotTrigger é um gatilho dentro do snapshot.
type FlowSnapshotTrigger struct {
	Kind        FlowTriggerKind   `json:"kind"`
	Name        string            `json:"name"`
	Enabled     bool              `json:"enabled"`
	RatePerMin  int               `json:"rate_per_min"`
	EventVerb   string            `json:"event_verb,omitempty"`
	EventFilter map[string]string `json:"event_filter,omitempty"`
	Mapping     map[string]string `json:"mapping,omitempty"`
}

// DiffFlowSnapshots lista os campos que diferem (para o resumo da revisão), em ordem estável.
func DiffFlowSnapshots(a, b FlowSnapshot) []string {
	var out []string
	add := func(changed bool, name string) {
		if changed {
			out = append(out, name)
		}
	}
	add(a.Name != b.Name, "name")
	add(a.Description != b.Description, "description")
	add(a.Cron != b.Cron, "cron")
	add(a.Timezone != b.Timezone, "timezone")
	add(a.PersonaID != b.PersonaID, "persona_id")
	add((a.ProjectID == nil) != (b.ProjectID == nil) || (a.ProjectID != nil && *a.ProjectID != *b.ProjectID), "project_id")
	add(a.Priority != b.Priority, "priority")
	add(a.TaskTitle != b.TaskTitle, "task_title")
	add(a.TaskDescription != b.TaskDescription, "task_description")
	add(!equalJSON(a.Variables, b.Variables), "variables")
	add(!equalJSON(a.Triggers, b.Triggers), "triggers")
	return out
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// FlowRevision é uma revisão append-only da definição de um fluxo.
type FlowRevision struct {
	ID           ID           `json:"id"`
	FlowID       ID           `json:"flow_id"`
	Revision     int          `json:"revision"`
	Snapshot     FlowSnapshot `json:"snapshot"`
	Summary      string       `json:"summary,omitempty"`
	Actor        Actor        `json:"actor"`
	Origin       string       `json:"origin"`
	RestoredFrom *int         `json:"restored_from,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
}
