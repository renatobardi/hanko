package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Readiness da persona (ADR-0055, #10): o probe do harness com passos nomeados. Uma persona "não pronta" não
// recebe runs até o próximo teste passar; o motivo é o primeiro passo que falhou.

// ReadinessStatus é o veredito do último teste.
type ReadinessStatus string

const (
	ReadinessReady    ReadinessStatus = "ready"
	ReadinessNotReady ReadinessStatus = "not_ready"
)

// Valid informa se s é um veredito conhecido.
func (s ReadinessStatus) Valid() bool { return s == ReadinessReady || s == ReadinessNotReady }

// ReadinessStepStatus é o resultado de um passo. warn não bloqueia; skip = não se aplica ou não foi possível testar.
type ReadinessStepStatus string

const (
	StepOK   ReadinessStepStatus = "ok"
	StepWarn ReadinessStepStatus = "warn"
	StepFail ReadinessStepStatus = "fail"
	StepSkip ReadinessStepStatus = "skip"
)

// Passos nomeados do probe, na ordem em que rodam.
const (
	StepEnvironment = "environment" // o ambiente de execução responde (conexão, imagem, sandbox)
	StepBinary      = "binary"      // o binário do harness existe no alvo
	StepAuth        = "auth"        // há credencial utilizável (login ou chave de API)
	StepModel       = "model"       // o modelo é conhecido e tem preço
)

// ReadinessSteps lista os passos na ordem do probe.
var ReadinessSteps = []string{StepEnvironment, StepBinary, StepAuth, StepModel}

// ReadinessStep é o resultado de um passo do probe.
type ReadinessStep struct {
	Name   string              `json:"name"`
	Status ReadinessStepStatus `json:"status"`
	Detail string              `json:"detail,omitempty"`
}

// ReadinessTrigger diz o que pediu o teste.
type ReadinessTrigger string

const (
	ReadinessManual   ReadinessTrigger = "manual"   // "Testar agora"
	ReadinessPeriodic ReadinessTrigger = "periodic" // checagem periódica (river)
	ReadinessHire     ReadinessTrigger = "hire"     // logo após a contratação
	ReadinessUpdate   ReadinessTrigger = "update"   // depois de mudar harness, modelo, env ou ambiente
)

// Valid informa se t é um gatilho conhecido.
func (t ReadinessTrigger) Valid() bool {
	return t == ReadinessManual || t == ReadinessPeriodic || t == ReadinessHire || t == ReadinessUpdate
}

// Readiness é o último resultado do teste de uma persona.
type Readiness struct {
	PersonaID ID `json:"persona_id"`
	CompanyID ID `json:"company_id"`
	// Status: ready ou not_ready; uma persona sem teste não tem Readiness (nunca testada não bloqueia).
	Status ReadinessStatus `json:"status"`
	Agent  string          `json:"agent"`
	Model  string          `json:"model,omitempty"`
	// EnvironmentID/EnvironmentSource: o ambiente testado e a origem dele (persona > projeto > padrão > local).
	EnvironmentID     *ID             `json:"environment_id,omitempty"`
	EnvironmentSource EnvSource       `json:"environment_source"`
	Steps             []ReadinessStep `json:"steps"`
	// Reason é o motivo do bloqueio (o primeiro passo que falhou); vazio quando pronta.
	Reason       string           `json:"reason,omitempty"`
	Trigger      ReadinessTrigger `json:"trigger"`
	DurationMs   int              `json:"duration_ms"`
	CheckedAt    time.Time        `json:"checked_at"`
	FailingSince *time.Time       `json:"failing_since,omitempty"`
}

// Blocks informa se o resultado barra o despacho da persona com o harness e o modelo de hoje: um teste de um
// harness ou modelo antigo não vale (a persona mudou depois dele).
func (r Readiness) Blocks(agent, model string) bool {
	return r.Status == ReadinessNotReady && r.Agent == agent && r.Model == model
}

// SummarizeReadiness resume os passos: not_ready se algum falhou, com o motivo do primeiro que falhou.
func SummarizeReadiness(steps []ReadinessStep) (ReadinessStatus, string) {
	for _, s := range steps {
		if s.Status == StepFail {
			if s.Detail != "" {
				return ReadinessNotReady, s.Name + ": " + s.Detail
			}
			return ReadinessNotReady, s.Name
		}
	}
	return ReadinessReady, ""
}

// BlockedError é o erro de despacho de uma persona não pronta; desembrulha para ErrPersonaNotReady.
func BlockedError(personaName, reason string) error {
	return fmt.Errorf("persona %s não está pronta: %s: %w", personaName, reason, ErrPersonaNotReady)
}

// ---- catálogo de modelos (#17) ----

// ModelOrigin diz de onde veio uma entrada do catálogo.
type ModelOrigin string

const (
	ModelBuiltin ModelOrigin = "builtin" // alias conhecido do harness
	ModelPriced  ModelOrigin = "price"   // linha de model_prices
	ModelProbed  ModelOrigin = "probe"   // visto por um teste de readiness
	ModelUsed    ModelOrigin = "run"     // usado por uma run da empresa
)

// CatalogModel é um modelo selecionável de um harness.
type CatalogModel struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
	// Origins são as fontes que citam o modelo, em ordem estável.
	Origins []ModelOrigin `json:"origins"`
	// Priced diz se há preço próprio (prefixo de model_prices); sem ele a run cai no preço de reserva.
	Priced bool `json:"priced"`
	// PriceModel é o prefixo de model_prices que casou ("" quando não há).
	PriceModel string `json:"price_model,omitempty"`
}

// ModelReport é um modelo visto por um teste ou por uma run.
type ModelReport struct {
	Agent  string
	Model  string
	Origin ModelOrigin
}

// builtinModels são os aliases que o harness aceita em --model (sem rede); o resto vem de preços e de testes.
var builtinModels = map[string][]string{
	"claude": {"opus", "sonnet", "haiku"},
}

// builtinPriceAlias leva um alias ao prefixo de preço da família (model_prices guarda prefixos como claude-opus).
var builtinPriceAlias = map[string]string{
	"opus": "claude-opus", "sonnet": "claude-sonnet", "haiku": "claude-haiku",
}

// AgentOfModel infere o harness de um nome de modelo pelo prefixo conhecido; "" quando serve a qualquer um.
func AgentOfModel(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "claude"), m == "opus", m == "sonnet", m == "haiku":
		return "claude"
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "codex"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return "codex"
	}
	return ""
}

// MatchPrice devolve o prefixo de preço mais longo de prefixes que casa com model; ok=false quando nenhum casa
// (prefixo vazio é o preço de reserva e não conta). Alias conhecidos (opus...) casam pela família.
func MatchPrice(prefixes []string, model string) (string, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return "", false
	}
	probe := []string{m}
	if fam, ok := builtinPriceAlias[m]; ok {
		probe = append(probe, fam)
	}
	best := ""
	for _, p := range prefixes {
		pl := strings.ToLower(p)
		if pl == "" {
			continue
		}
		for _, x := range probe {
			if strings.HasPrefix(x, pl) && len(pl) > len(best) {
				best = p
			}
		}
	}
	return best, best != ""
}

// BuildModelCatalog une os aliases do harness, as linhas de model_prices e o que testes e runs relataram, sem
// duplicar, e marca quais têm preço. agent vazio devolve todos os harnesses; um agent devolve os dele e os que
// servem a qualquer harness. Ordem: agent, modelo.
func BuildModelCatalog(agent string, priceModels []string, reports []ModelReport) []CatalogModel {
	type key struct{ agent, model string }
	idx := map[key]*CatalogModel{}
	add := func(a, m string, o ModelOrigin) {
		m = strings.TrimSpace(m)
		if m == "" {
			return
		}
		k := key{a, m}
		c := idx[k]
		if c == nil {
			c = &CatalogModel{Agent: a, Model: m}
			idx[k] = c
		}
		for _, x := range c.Origins {
			if x == o {
				return
			}
		}
		c.Origins = append(c.Origins, o)
	}
	for a, ms := range builtinModels {
		for _, m := range ms {
			add(a, m, ModelBuiltin)
		}
	}
	for _, p := range priceModels {
		if strings.TrimSpace(p) == "" || strings.HasSuffix(p, "-") {
			continue // prefixo de família ("gpt-"), não um modelo selecionável
		}
		add(AgentOfModel(p), p, ModelPriced)
	}
	for _, r := range reports {
		a := r.Agent
		if a == "" {
			a = AgentOfModel(r.Model)
		}
		add(a, r.Model, r.Origin)
	}
	out := make([]CatalogModel, 0, len(idx))
	for _, c := range idx {
		if agent != "" && c.Agent != "" && c.Agent != agent {
			continue
		}
		sort.Slice(c.Origins, func(i, j int) bool { return c.Origins[i] < c.Origins[j] })
		c.PriceModel, c.Priced = MatchPrice(priceModels, c.Model)
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Agent != out[j].Agent {
			return out[i].Agent < out[j].Agent
		}
		return out[i].Model < out[j].Model
	})
	return out
}
