package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Contrato da fonte externa e da ingestão (ADR-0075 a 0079, 0084 e 0086), grupo 1 da v0.8 (#124). Cada operação já
// tem entrada e saída no OpenAPI e responde 501 até a fatia que a implementa ligar o backend; a descrição de cada uma
// diz qual (issues #125 e #126). O Hanko só observa: nenhuma operação executa algo num agente ou num host externo.
// As rotas de ingestão (`/v1/traces|logs|metrics` e `/v1/replay/*`) ficam no listener da ingestão e têm o OpenAPI
// próprio (`api/openapi-ingest.json`).

// pendingContractOp registra uma operação de um contrato da v0.8 que ainda responde 501; `contract` diz de qual (por
// exemplo "Contrato da fonte externa (ADR-0075 a 0079)") e `slice` em qual fatia ela fica pronta.
func pendingContractOp[I, O any](api huma.API, o huma.Operation, contract, slice string) {
	o.Description = contract + ". Responde 501 até a " + slice + "."
	o.Errors = append(o.Errors, http.StatusNotImplemented)
	huma.Register(api, o, func(context.Context, *I) (*O, error) {
		return nil, problem(http.StatusNotImplemented, "operação ainda não implementada")
	})
}

// pendingExternalOp registra uma operação do contrato da fonte externa que ainda responde 501.
func pendingExternalOp[I, O any](api huma.API, o huma.Operation, slice string) {
	pendingContractOp[I, O](api, o, "Contrato da fonte externa (ADR-0075 a 0079)", slice)
}

const (
	sliceExternalSource = "fatia 1 (#125)"
	sliceExternalUsage  = "fatia 2 (#126)"
)

// ---------- recursos ----------

// ExternalAgent é um agente que aparece na telemetria; o Hanko não o executa (ADR-0075 item 1).
type ExternalAgent struct {
	ID         string     `json:"id" format:"uuid"`
	Name       string     `json:"name" example:"claude"`
	Aliases    []string   `json:"aliases" doc:"Os textos de agente que já chegaram e apontam para este agente."`
	SourceKind string     `json:"source_kind" enum:"external"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ExternalHost é a máquina onde roda um agente externo (ADR-0077).
type ExternalHost struct {
	ID         string     `json:"id" format:"uuid"`
	Name       string     `json:"name" example:"oute-server"`
	Aliases    []string   `json:"aliases"`
	SourceKind string     `json:"source_kind" enum:"external"`
	AlwaysOn   bool       `json:"always_on" doc:"Só host sempre ligado gera o alerta host_no_data."`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty" doc:"Hora do último fato recebido."`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// RepoPairing liga a chave de um repositório a um workspace da empresa (ADR-0075 itens 3 a 5).
type RepoPairing struct {
	ID            string    `json:"id" format:"uuid"`
	RepoKey       string    `json:"repo_key" example:"github.com/renatobardi/oute-agent"`
	KeyKind       string    `json:"key_kind" enum:"url,name" doc:"url: remoto normalizado. name: nome do repositório, quando o fato não traz URL."`
	WorkspaceID   string    `json:"workspace_id" format:"uuid"`
	WorkspaceSlug string    `json:"workspace_slug"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ExternalSession agrupa conversas externas pelo `oute.task.id` que a fonte declara (ADR-0078 item 2).
type ExternalSession struct {
	ID            string     `json:"id" format:"uuid"`
	SourceKind    string     `json:"source_kind" enum:"external"`
	TaskID        string     `json:"task_id"`
	OuteRepo      string     `json:"oute_repo,omitempty" doc:"Repositório como chegou."`
	WorkspaceID   *string    `json:"workspace_id,omitempty" format:"uuid"`
	Slug          string     `json:"slug,omitempty"`
	AgentID       *string    `json:"agent_id,omitempty" format:"uuid"`
	State         string     `json:"state,omitempty"`
	Round         string     `json:"round,omitempty"`
	Worker        string     `json:"worker,omitempty"`
	Conversations int        `json:"conversations"`
	FirstAt       *time.Time `json:"first_at,omitempty"`
	LastAt        *time.Time `json:"last_at,omitempty"`
}

// ExternalConversation é a conversa de um agente externo, identificada pelo `session.id` que ele emite (ADR-0078).
type ExternalConversation struct {
	ID                  string     `json:"id" format:"uuid"`
	SourceKind          string     `json:"source_kind" enum:"external"`
	SessionID           string     `json:"session_id"`
	Name                string     `json:"name" doc:"Nome do gerador único (ADR-0084)." example:"Veloz_Raposa"`
	ExternalSessionID   *string    `json:"external_session_id,omitempty" format:"uuid"`
	HostID              *string    `json:"host_id,omitempty" format:"uuid"`
	AgentID             *string    `json:"agent_id,omitempty" format:"uuid"`
	OuteInstance        string     `json:"oute_instance,omitempty"`
	ServiceName         string     `json:"service_name,omitempty"`
	SubscriptionKey     string     `json:"subscription_key,omitempty" enum:",claude,zai,codex"`
	OuteRepo            string     `json:"oute_repo,omitempty"`
	WorkspaceID         *string    `json:"workspace_id,omitempty" format:"uuid"`
	InferredAttribution bool       `json:"inferred_attribution" doc:"A atribuição ao repositório foi inferida, não declarada (ADR-0075 item 7)."`
	FirstAt             *time.Time `json:"first_at,omitempty"`
	LastAt              *time.Time `json:"last_at,omitempty"`
	Calls               int64      `json:"calls"`
	Errors              int64      `json:"errors"`
	InputTokens         int64      `json:"input_tokens"`
	OutputTokens        int64      `json:"output_tokens"`
	CacheReadTokens     int64      `json:"cache_read_tokens"`
	CacheCreationTokens int64      `json:"cache_creation_tokens"`
	CostRealUSD         float64    `json:"cost_real_usd"`
	CostListUSD         float64    `json:"cost_list_usd"`
	UnpricedCalls       int64      `json:"unpriced_calls" doc:"Chamadas sem preço: nunca contam como zero."`
	P95Ms               *int       `json:"p95_ms,omitempty"`
	ContextAvg          *int64     `json:"context_avg,omitempty"`
	ContextMax          *int64     `json:"context_max,omitempty"`
	Class               string     `json:"class,omitempty" doc:"Classe atual (no classificador aidlc, a fase do AI-DLC)."`
	ClassOrigin         string     `json:"class_origin,omitempty" enum:",opening,label,skill,role,action,decision_model,manual"`
	ClassConfidence     string     `json:"class_confidence" enum:"high,low"`
	Classifier          string     `json:"classifier,omitempty"`
	Tags                []string   `json:"tags"`
}

// ConversationClassInput é uma entrada da classificação que não se refaz: a resposta do decision model ou a troca
// manual (ADR-0079 itens 1, 4 e 8). Só recebe linhas novas.
type ConversationClassInput struct {
	ID         string    `json:"id" format:"uuid"`
	Kind       string    `json:"kind" enum:"decision_model,manual"`
	Class      string    `json:"class"`
	Confidence string    `json:"confidence" enum:"high,low"`
	ActorType  string    `json:"actor_type" enum:"human,system"`
	ActorID    *string   `json:"actor_id,omitempty" format:"uuid"`
	Migrated   bool      `json:"migrated" doc:"Veio do agent-studio na migração."`
	CreatedAt  time.Time `json:"created_at"`
}

// ConversationContentItem é um trecho do conteúdo completo (prompt ou resposta) de uma conversa externa.
type ConversationContentItem struct {
	Time time.Time `json:"time"`
	Role string    `json:"role" enum:"prompt,response"`
	Text string    `json:"text" doc:"Texto já mascarado pelo collector (ADR-0076 item 8)."`
}

// ConversationContent é o conteúdo completo de uma conversa externa; só owner e admin, e cada leitura gera
// `activity_event` (ADR-0076 item 9).
type ConversationContent struct {
	ConversationID string                    `json:"conversation_id" format:"uuid"`
	Items          []ConversationContentItem `json:"items"`
}

// IngestKey é uma chave de escopo `ingest` (ADR-0076 item 3): uma por host, presa à empresa; só grava telemetria.
type IngestKey struct {
	ID         string     `json:"id" format:"uuid"`
	Name       string     `json:"name" example:"oute-server" doc:"Por convenção, o nome do host."`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// IngestKeyCreated é a resposta da criação: o token aparece uma vez só.
type IngestKeyCreated struct {
	IngestKey
	Token string `json:"token" doc:"A chave em claro. SÓ nesta resposta."`
}

// ExternalUsageRow é uma linha do uso externo, por dia, host, agente, modelo, workspace e assinatura (ADR-0078 item 5).
// O "pago" é do rateio e chega com a fatia 2.
type ExternalUsageRow struct {
	Day                 *string `json:"day,omitempty" format:"date"`
	HostID              *string `json:"host_id,omitempty" format:"uuid"`
	AgentID             *string `json:"agent_id,omitempty" format:"uuid"`
	WorkspaceID         *string `json:"workspace_id,omitempty" format:"uuid"`
	Model               string  `json:"model,omitempty"`
	SubscriptionKey     string  `json:"subscription_key,omitempty"`
	Calls               int64   `json:"calls"`
	Errors              int64   `json:"errors"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CostRealUSD         float64 `json:"cost_real_usd"`
	CostListUSD         float64 `json:"cost_list_usd"`
	UnpricedCalls       int64   `json:"unpriced_calls"`
}

// ---------- entradas ----------

type ExternalAgentPath struct {
	CompanyPath
	Agent string `path:"agent" format:"uuid" doc:"Id do agente externo."`
}

type ExternalHostPath struct {
	CompanyPath
	Host string `path:"host" format:"uuid" doc:"Id do host externo."`
}

type RepoPairingPath struct {
	CompanyPath
	Pairing string `path:"pairing" format:"uuid" doc:"Id do emparelhamento."`
}

type ExternalSessionPath struct {
	CompanyPath
	Session string `path:"session" format:"uuid" doc:"Id da sessão externa."`
}

type ExternalConversationPath struct {
	CompanyPath
	Conversation string `path:"conversation" format:"uuid" doc:"Id da conversa externa."`
}

type IngestKeyPath struct {
	CompanyPath
	Key string `path:"key" format:"uuid" doc:"Id da chave de ingestão."`
}

type listExternalNamedIn struct {
	CompanyPath
	Page
	IncludeArchived bool `query:"include_archived" default:"false" doc:"Inclui os arquivados."`
}

type updateExternalAgentIn struct {
	ExternalAgentPath
	Body struct {
		Name     *string `json:"name,omitempty" maxLength:"100" doc:"Novo nome (owner ou admin)."`
		Archived *bool   `json:"archived,omitempty" doc:"Arquiva ou volta o agente."`
	}
}

type updateExternalHostIn struct {
	ExternalHostPath
	Body struct {
		Name     *string `json:"name,omitempty" maxLength:"100"`
		AlwaysOn *bool   `json:"always_on,omitempty"`
		Archived *bool   `json:"archived,omitempty"`
	}
}

type mergeExternalAgentIn struct {
	ExternalAgentPath
	Body struct {
		Into string `json:"into" format:"uuid" doc:"O agente que fica. O passado deste passa para ele."`
	}
}

type mergeExternalHostIn struct {
	ExternalHostPath
	Body struct {
		Into string `json:"into" format:"uuid" doc:"O host que fica. O passado deste passa para ele."`
	}
}

type updateRepoPairingIn struct {
	RepoPairingPath
	Body struct {
		WorkspaceID string `json:"workspace_id" format:"uuid" doc:"O workspace novo. Custo, conversas e telemetria antigas passam a contar nele."`
	}
}

type listExternalSessionsIn struct {
	CompanyPath
	Page
	Workspace string `query:"workspace" format:"uuid"`
	Agent     string `query:"agent" format:"uuid"`
}

type listExternalConversationsIn struct {
	CompanyPath
	Page
	Host      string    `query:"host" format:"uuid"`
	Agent     string    `query:"agent" format:"uuid"`
	Workspace string    `query:"workspace" format:"uuid"`
	Session   string    `query:"session" format:"uuid"`
	Class     string    `query:"class"`
	Tag       string    `query:"tag"`
	Since     time.Time `query:"since"`
	Until     time.Time `query:"until"`
	Sort      string    `query:"sort" enum:"last,cost" default:"last"`
}

type classifyExternalConversationIn struct {
	ExternalConversationPath
	Body struct {
		Class string `json:"class" minLength:"1" maxLength:"40" doc:"A classe nova. A troca manual vale sobre tudo (ADR-0079 item 1)."`
	}
}

type externalUsageIn struct {
	CompanyPath
	Since   time.Time `query:"since"`
	Until   time.Time `query:"until"`
	GroupBy []string  `query:"group_by" enum:"day,host,agent,model,workspace,subscription" doc:"Dimensões do agrupamento."`
	Round   string    `query:"round" format:"uuid" doc:"Só as chamadas da rodada externa (ADR-0080 item 8), para o teto de custo do modo dark."`
}

// ---------- registro ----------

func (s *server) registerExternalSource(api huma.API) {
	const tag = "external-source"
	b := "/companies/{company}"

	s.registerExternalEntities(api)

	pendingExternalOp[listExternalNamedIn, ListOut[RepoPairing]](api, op("list-repo-pairings", http.MethodGet, b+"/repo-pairings", tag,
		"Lista os emparelhamentos de repositório com workspace"), sliceExternalSource)
	pendingExternalOp[updateRepoPairingIn, ItemOut[RepoPairing]](api, op("update-repo-pairing", http.MethodPatch, b+"/repo-pairings/{pairing}", tag,
		"Troca o workspace de um emparelhamento; o passado vai junto (owner ou admin)"), sliceExternalSource)

	pendingExternalOp[listExternalSessionsIn, ListOut[ExternalSession]](api, op("list-external-sessions", http.MethodGet, b+"/external-sessions", tag,
		"Lista as sessões externas"), sliceExternalSource)
	pendingExternalOp[ExternalSessionPath, ItemOut[ExternalSession]](api, op("get-external-session", http.MethodGet, b+"/external-sessions/{session}", tag,
		"Mostra uma sessão externa"), sliceExternalSource)

	pendingExternalOp[listExternalConversationsIn, ListOut[ExternalConversation]](api, op("list-external-conversations", http.MethodGet, b+"/external-conversations", tag,
		"Lista as conversas externas, com filtro por host, agente, workspace, sessão, classe e marca"), sliceExternalSource)
	pendingExternalOp[ExternalConversationPath, ItemOut[ExternalConversation]](api, op("get-external-conversation", http.MethodGet, b+"/external-conversations/{conversation}", tag,
		"Mostra uma conversa externa"), sliceExternalSource)
	pendingExternalOp[ExternalConversationPath, ListOut[ConversationClassInput]](api, op("list-conversation-class-inputs", http.MethodGet, b+"/external-conversations/{conversation}/class-inputs", tag,
		"Lista as entradas da classificação que não se refazem: respostas do decision model e trocas manuais"), sliceExternalSource)
	pendingExternalOp[classifyExternalConversationIn, ItemOut[ExternalConversation]](api, op("classify-external-conversation", http.MethodPost, b+"/external-conversations/{conversation}/class", tag,
		"Troca a classe de uma conversa à mão; vale sobre tudo (owner ou admin)"), sliceExternalSource)
	pendingExternalOp[ExternalConversationPath, ItemOut[ConversationContent]](api, op("get-external-conversation-content", http.MethodGet, b+"/external-conversations/{conversation}/content", tag,
		"Mostra o conteúdo completo da conversa (owner ou admin; cada leitura gera activity_event)"), sliceExternalSource)

	pendingExternalOp[externalUsageIn, ListOut[ExternalUsageRow]](api, op("get-external-usage", http.MethodGet, b+"/external-usage", tag,
		"Uso e custo de lista das chamadas externas, agrupados"), sliceExternalUsage)
}
