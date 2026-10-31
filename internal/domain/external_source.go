package domain

import (
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Fonte externa e ingestão (ADR-0075 a 0079, 0084 e 0086). O Hanko só observa: nada aqui executa um agente ou um host.
// Os valores são os da migration 00208, em inglês (ADR-0078 item 4).

// SourceKindExternal é a origem de todo conceito externo no v0.8 (ADR-0086 item 2). Um valor novo exige migration
// que altere a constraint `source_kind` das tabelas.
const SourceKindExternal = "external"

// ExternalNameMax é o tamanho máximo do nome de um agente ou host externo.
const ExternalNameMax = 100

// ValidateExternalName confere o nome de um agente ou host externo: de 1 a ExternalNameMax caracteres, sem espaço no
// começo nem no fim (a constraint do banco faz o mesmo).
func ValidateExternalName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return Invalid("name", "must not be empty or start or end with a space")
	}
	if utf8.RuneCountInString(name) > ExternalNameMax {
		return Invalid("name", "must be at most 100 characters")
	}
	return nil
}

// ---------- emparelhamento ----------

// RepoKeyKind diz de onde vem a chave de um emparelhamento (ADR-0075 item 3).
type RepoKeyKind string

const (
	RepoKeyURL  RepoKeyKind = "url"  // URL do remoto, normalizada
	RepoKeyName RepoKeyKind = "name" // nome do repositório, quando o fato não traz URL
)

// Valid informa se o tipo é conhecido.
func (k RepoKeyKind) Valid() bool { return k == RepoKeyURL || k == RepoKeyName }

// RepoKeyMax é o tamanho máximo de uma chave de repositório (o mesmo da constraint do banco).
const RepoKeyMax = 500

var scpLike = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]+):([^/].*)$`)

// NormalizeRepoURL reduz a URL de um remoto à chave `host/dono/repo` (ADR-0075 item 4.1): `https://`, `ssh://` e
// `git@host:dono/repo` do mesmo repositório dão a mesma chave; o `.git` final e a barra final não contam; o host vale
// em minúsculas; usuário, senha e porta saem. ok é falso quando o texto não parece o remoto de um repositório.
func NormalizeRepoURL(raw string) (key string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > RepoKeyMax {
		return "", false
	}
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", false
		}
		host, path = u.Hostname(), u.Path
	} else if m := scpLike.FindStringSubmatch(raw); m != nil {
		host, path = m[1], m[2]
	} else {
		return "", false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	path = strings.Trim(path, "/")
	if path == "" || strings.ContainsAny(path, " \t\r\n") {
		return "", false
	}
	return strings.ToLower(host) + "/" + path, true
}

// ---------- conversa ----------

// ConversationClassOrigin diz que sinal decidiu a classe de uma conversa externa (ADR-0079 item 3).
type ConversationClassOrigin string

const (
	ClassOriginOpening       ConversationClassOrigin = "opening"
	ClassOriginLabel         ConversationClassOrigin = "label"
	ClassOriginSkill         ConversationClassOrigin = "skill"
	ClassOriginRole          ConversationClassOrigin = "role"
	ClassOriginAction        ConversationClassOrigin = "action"
	ClassOriginDecisionModel ConversationClassOrigin = "decision_model"
	ClassOriginManual        ConversationClassOrigin = "manual"
)

// Valid informa se a origem é conhecida.
func (o ConversationClassOrigin) Valid() bool {
	switch o {
	case ClassOriginOpening, ClassOriginLabel, ClassOriginSkill, ClassOriginRole, ClassOriginAction,
		ClassOriginDecisionModel, ClassOriginManual:
		return true
	}
	return false
}

// ClassConfidence é a confiança da classificação (ADR-0079 item 3).
type ClassConfidence string

const (
	ClassConfidenceHigh ClassConfidence = "high"
	ClassConfidenceLow  ClassConfidence = "low"
)

// Valid informa se a confiança é conhecida.
func (c ClassConfidence) Valid() bool { return c == ClassConfidenceHigh || c == ClassConfidenceLow }

// ClassInputKind é o que uma entrada de classificação guarda e não se refaz (ADR-0079 itens 1 e 8).
type ClassInputKind string

const (
	ClassInputDecisionModel ClassInputKind = "decision_model"
	ClassInputManual        ClassInputKind = "manual"
)

// Valid informa se o tipo é conhecido.
func (k ClassInputKind) Valid() bool { return k == ClassInputDecisionModel || k == ClassInputManual }

// ClassMax é o tamanho máximo do nome de uma classe.
const ClassMax = 40

// ValidateClass confere o nome de uma classe de conversa: de 1 a ClassMax caracteres.
func ValidateClass(class string) error {
	if class == "" || utf8.RuneCountInString(class) > ClassMax {
		return Invalid("class", "must have 1 to 40 characters")
	}
	return nil
}

// CallRole é o papel de uma chamada ao modelo (ADR-0078 item 3).
type CallRole string

const (
	CallRoleSubagent   CallRole = "subagent"
	CallRoleReviewer   CallRole = "reviewer"
	CallRoleWorker     CallRole = "worker"
	CallRoleDispatcher CallRole = "dispatcher"
	CallRoleStandalone CallRole = "standalone"
)

// Valid informa se o papel é conhecido.
func (r CallRole) Valid() bool {
	switch r {
	case CallRoleSubagent, CallRoleReviewer, CallRoleWorker, CallRoleDispatcher, CallRoleStandalone:
		return true
	}
	return false
}

// CallCostClass é a classe do custo de uma chamada (ADR-0082 item 2). `unpriced` nunca vira zero: as somas o contam à parte.
type CallCostClass string

const (
	CallCostReal      CallCostClass = "real"
	CallCostListed    CallCostClass = "listed"
	CallCostEstimated CallCostClass = "estimated"
	CallCostUnpriced  CallCostClass = "unpriced"
)

// Valid informa se a classe é conhecida.
func (c CallCostClass) Valid() bool {
	return c == CallCostReal || c == CallCostListed || c == CallCostEstimated || c == CallCostUnpriced
}

// ---------- nome de conversa ----------

// externalConversationName é o formato do nome de uma conversa externa: o par `Adjetivo_Substantivo` do gerador
// único (ADR-0084), com o sufixo de 3 dígitos do último recurso (o mesmo da constraint do banco).
var externalConversationName = regexp.MustCompile(`^[A-Z][a-z]+_[A-Z][a-z]+(_[1-9][0-9]{2})?$`)

// ValidExternalConversationName informa se o nome segue o formato do gerador único.
func ValidExternalConversationName(name string) bool {
	return externalConversationName.MatchString(name)
}

// ---------- chave de ingestão ----------

// IngestKey é uma chave de escopo `ingest` (ADR-0076 item 3): uma por host, presa a uma empresa. Só grava telemetria; o
// token em claro existe só na criação.
type IngestKey struct {
	ID         ID         `json:"id"`
	CompanyID  ID         `json:"company_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// KeyScopeIngest é o escopo da chave de ingestão. Não entra em KeyScope.Valid: nenhuma rota pública cria uma chave
// assim, e a chave nunca autentica a API pública nem o /mcp (KeyScope.Allows recusa tudo que não é full ou read).
const KeyScopeIngest KeyScope = "ingest"

// SubscriptionExternalKeys são as chaves externas de assinatura que o Hanko conhece (ADR-0082 item 4).
var SubscriptionExternalKeys = []string{"claude", "zai", "codex"}

// ValidateSubscriptionExternalKey confere a chave externa de uma assinatura.
func ValidateSubscriptionExternalKey(key string) error {
	for _, k := range SubscriptionExternalKeys {
		if key == k {
			return nil
		}
	}
	return Invalid("external_key", "want claude, zai or codex")
}
