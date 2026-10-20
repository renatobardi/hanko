package domain

import (
	"slices"
	"time"
)

// Motor de política de tools (ADR-0053). Uma política diz "neste escopo, para este alvo, o modo é off, ask ou allow".
// Ela fica POR CIMA do perfil de ferramentas (ADR-0036): o perfil continua sendo o padrão reutilizável; a política é
// a exceção explícita por persona (e o padrão da empresa para tools do harness). Tudo aqui é puro: sem I/O.

// ToolPolicyMode é o que a política manda fazer com a chamada.
type ToolPolicyMode string

const (
	PolicyOff   ToolPolicyMode = "off"   // desligada: a chamada é recusada
	PolicyAsk   ToolPolicyMode = "ask"   // pergunta antes (gate tool)
	PolicyAllow ToolPolicyMode = "allow" // passa direto
)

// PolicyModes lista os modos do mais restritivo ao mais livre.
var PolicyModes = []ToolPolicyMode{PolicyOff, PolicyAsk, PolicyAllow}

func (m ToolPolicyMode) Valid() bool { return slices.Contains(PolicyModes, m) }

// restriction: quanto maior, mais restritivo (o desempate "deny vence").
func (m ToolPolicyMode) restriction() int {
	switch m {
	case PolicyOff:
		return 3
	case PolicyAsk:
		return 2
	case PolicyAllow:
		return 1
	}
	return 0
}

// ToolPolicyScope é onde a política vale.
type ToolPolicyScope string

const (
	PolicyScopeCompany ToolPolicyScope = "company" // padrão da empresa
	PolicyScopePersona ToolPolicyScope = "persona" // exceção de uma persona: vence a da empresa
)

func (s ToolPolicyScope) Valid() bool { return s == PolicyScopeCompany || s == PolicyScopePersona }

func (s ToolPolicyScope) rank() int {
	switch s {
	case PolicyScopePersona:
		return 2
	case PolicyScopeCompany:
		return 1
	}
	return 0
}

// ToolPolicyTarget é o que a política alcança.
type ToolPolicyTarget string

const (
	PolicyTargetAll         ToolPolicyTarget = "all"         // qualquer tool de integração e do harness
	PolicyTargetIntegration ToolPolicyTarget = "integration" // a integração inteira
	PolicyTargetTool        ToolPolicyTarget = "tool"        // uma tool de integração
	PolicyTargetNative      ToolPolicyTarget = "native"      // uma tool do harness (Bash, Edit...) ou de plugin, pelo nome
)

func (t ToolPolicyTarget) Valid() bool {
	switch t {
	case PolicyTargetAll, PolicyTargetIntegration, PolicyTargetTool, PolicyTargetNative:
		return true
	}
	return false
}

func (t ToolPolicyTarget) specificity() int {
	switch t {
	case PolicyTargetTool, PolicyTargetNative:
		return 3
	case PolicyTargetIntegration:
		return 2
	case PolicyTargetAll:
		return 1
	}
	return 0
}

// ToolPolicyOrigin diz como a política nasceu.
type ToolPolicyOrigin string

const (
	PolicyOriginManual ToolPolicyOrigin = "manual" // um admin escreveu
	PolicyOriginAlways ToolPolicyOrigin = "always" // "sempre permitir" na decisão de um gate
)

// MaxToolPolicies é o teto de políticas por empresa.
const MaxToolPolicies = 2000

// ToolPolicy é uma política de tool.
type ToolPolicy struct {
	ID              ID               `json:"id"`
	CompanyID       ID               `json:"company_id"`
	Scope           ToolPolicyScope  `json:"scope"`
	PersonaID       *ID              `json:"persona_id,omitempty"`
	PersonaName     string           `json:"persona_name,omitempty"`
	Target          ToolPolicyTarget `json:"target"`
	IntegrationID   *ID              `json:"integration_id,omitempty"`
	IntegrationSlug string           `json:"integration_slug,omitempty"`
	ToolName        string           `json:"tool_name,omitempty"`
	Mode            ToolPolicyMode   `json:"mode"`
	Origin          ToolPolicyOrigin `json:"origin"`
	CreatedBy       Actor            `json:"created_by"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// ToolSubjectKind é a família da tool que está sendo decidida.
type ToolSubjectKind string

const (
	SubjectIntegration ToolSubjectKind = "integration" // tool de uma integração MCP (pelo gateway)
	SubjectNative      ToolSubjectKind = "native"      // tool do harness
	SubjectPlugin      ToolSubjectKind = "plugin"      // tool contribuída por um plugin (servidor MCP do Hanko)
)

// ToolSubject é a chamada que a política avalia.
type ToolSubject struct {
	Kind          ToolSubjectKind
	PersonaID     *ID // quem chama; nil = sem persona (gateway nomeado): só valem as políticas da empresa
	IntegrationID *ID // SubjectIntegration
	Tool          string
}

// Matches informa se a política alcança a chamada.
func (p ToolPolicy) Matches(s ToolSubject) bool {
	switch p.Scope {
	case PolicyScopeCompany:
	case PolicyScopePersona:
		if p.PersonaID == nil || s.PersonaID == nil || *p.PersonaID != *s.PersonaID {
			return false
		}
	default:
		return false
	}
	switch p.Target {
	case PolicyTargetAll:
		// o curinga cobre as tools de integração e as do harness; plugin e as hanko_* só por nome (uma política
		// "tudo desligado" não pode derrubar o protocolo do Hanko)
		return s.Kind == SubjectIntegration || s.Kind == SubjectNative
	case PolicyTargetIntegration:
		return s.Kind == SubjectIntegration && p.IntegrationID != nil && s.IntegrationID != nil && *p.IntegrationID == *s.IntegrationID
	case PolicyTargetTool:
		return s.Kind == SubjectIntegration && p.IntegrationID != nil && s.IntegrationID != nil &&
			*p.IntegrationID == *s.IntegrationID && p.ToolName == s.Tool
	case PolicyTargetNative:
		return (s.Kind == SubjectNative || s.Kind == SubjectPlugin) && p.ToolName == s.Tool
	}
	return false
}

// precedes informa se a vence b: escopo mais estreito (persona > empresa), depois alvo mais específico (tool > integração
// > curinga), depois o modo mais restritivo (deny vence) e, para a ordem ser total, o id maior.
func (p ToolPolicy) precedes(o ToolPolicy) bool {
	if a, b := p.Scope.rank(), o.Scope.rank(); a != b {
		return a > b
	}
	if a, b := p.Target.specificity(), o.Target.specificity(); a != b {
		return a > b
	}
	if a, b := p.Mode.restriction(), o.Mode.restriction(); a != b {
		return a > b
	}
	return p.ID.String() > o.ID.String()
}

// ResolveToolPolicy devolve a política que decide a chamada, se alguma a alcança. O resultado não depende da ordem de
// policies.
func ResolveToolPolicy(policies []ToolPolicy, s ToolSubject) (ToolPolicy, bool) {
	var best ToolPolicy
	found := false
	for _, p := range policies {
		if !p.Matches(s) {
			continue
		}
		if !found || p.precedes(best) {
			best, found = p, true
		}
	}
	return best, found
}

// policyVerdict traduz a política em veredito.
func policyVerdict(p ToolPolicy) ToolVerdict {
	pp := p
	src := "policy:" + string(p.Scope)
	switch p.Mode {
	case PolicyOff:
		return ToolVerdict{Allowed: false, Source: src, Policy: &pp}
	case PolicyAsk:
		return ToolVerdict{Allowed: true, Mode: ToolModeAsk, Source: src, Policy: &pp}
	}
	return ToolVerdict{Allowed: true, Mode: ToolModeAllow, Source: src, Policy: &pp}
}

// ApplyToolPolicies aplica as políticas ao veredito do perfil: a política que alcança a chamada vence o perfil
// (persona > empresa > perfil); sem política, o veredito do perfil segue como está.
func ApplyToolPolicies(profile ToolVerdict, policies []ToolPolicy, s ToolSubject) ToolVerdict {
	if p, ok := ResolveToolPolicy(policies, s); ok {
		return policyVerdict(p)
	}
	return profile
}

// NativeToolVerdict decide uma tool nativa do harness (ou de plugin). opinion=false: nenhuma política a alcança e
// vale o que o harness já fazia (modo de permissão do claude, política do ACP).
func NativeToolVerdict(policies []ToolPolicy, s ToolSubject) (v ToolVerdict, opinion bool) {
	p, ok := ResolveToolPolicy(policies, s)
	if !ok {
		return ToolVerdict{Source: "no_policy"}, false
	}
	return policyVerdict(p), true
}

// PolicyKey identifica o alvo de uma política dentro de um escopo (o que a unicidade do banco garante).
type PolicyKey struct {
	Scope         ToolPolicyScope
	PersonaID     ID
	Target        ToolPolicyTarget
	IntegrationID ID
	Tool          string
}

// Key devolve a chave única da política.
func (p ToolPolicy) Key() PolicyKey {
	k := PolicyKey{Scope: p.Scope, Target: p.Target, Tool: p.ToolName}
	if p.PersonaID != nil {
		k.PersonaID = *p.PersonaID
	}
	if p.IntegrationID != nil {
		k.IntegrationID = *p.IntegrationID
	}
	return k
}

// UpsertPolicy devolve policies com p no lugar da política de mesma chave (ou acrescentada). Aplicar duas vezes dá
// o mesmo resultado que uma (idempotente); é o modelo em memória do UPSERT do banco.
func UpsertPolicy(policies []ToolPolicy, p ToolPolicy) []ToolPolicy {
	out := make([]ToolPolicy, 0, len(policies)+1)
	replaced := false
	for _, q := range policies {
		if q.Key() == p.Key() {
			if !replaced {
				out = append(out, p)
				replaced = true
			}
			continue
		}
		out = append(out, q)
	}
	if !replaced {
		out = append(out, p)
	}
	return out
}

// ToolInvocationSource diz por onde a chamada entrou.
type ToolInvocationSource string

const (
	SourceRun     ToolInvocationSource = "run"     // run da persona pelo gateway /mcp/run
	SourceGateway ToolInvocationSource = "gateway" // gateway nomeado (cliente de fora)
	SourceTest    ToolInvocationSource = "test"    // teste na UI/API/CLI "como a persona X"
	SourceHook    ToolInvocationSource = "hook"    // hook PreToolUse do claude
	SourceACP     ToolInvocationSource = "acp"     // permission request do protocolo ACP
	SourcePlugin  ToolInvocationSource = "plugin"  // tool de plugin chamada pelo servidor MCP do Hanko
)

// Valid informa se a origem é conhecida.
func (s ToolInvocationSource) Valid() bool {
	switch s {
	case SourceRun, SourceGateway, SourceTest, SourceHook, SourceACP, SourcePlugin:
		return true
	}
	return false
}
