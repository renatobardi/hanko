package store

// Consultas da ingestão (ADR-0075 a 0078, ADR-0076). Escritas em SQL direto, fora de db/queries/: a fatia 1 do #125 não
// regera o código gerado pelo sqlc. Cada função recebe o DBTX da transação do chamador; nenhuma guarda regra de negócio
// (store não tem regra, AGENTS.md).

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TelemetrySpanRow é um span como chegou, com a empresa e a chave que o entregou (ADR-0076 item 3). Campos opcionais
// nulos quando o fato não traz o atributo.
type TelemetrySpanRow struct {
	CompanyID          uuid.UUID
	DedupeKey          string
	TimeUnixNano       int64
	EndUnixNano        *int64
	DurationNs         *int64
	HostName           *string
	OuteInstance       *string
	OuteAgent          *string
	ServiceName        *string
	SessionID          *string
	OuteTaskID         *string
	OuteSwarmRound     *string
	OuteRepo           *string
	OuteSubscription   *string
	TraceID            string
	SpanID             string
	ParentSpanID       *string
	Name               *string
	Kind               *int32
	StatusCode         *int32
	StatusMessage      *string
	ScopeName          *string
	ResourceAttributes []byte
	Attributes         []byte
	Events             []byte
	Links              []byte
	ReceivedUnixNano   int64
	IngestedByKeyID    uuid.UUID
}

// InsertTelemetrySpan grava o span se a chave de dedupe é nova. ok é falso quando o span já estava: o chamador não
// refaz nada do que o fato traria (ADR-0076 item 6).
func InsertTelemetrySpan(ctx context.Context, db DBTX, r TelemetrySpanRow) (bool, error) {
	tag, err := db.Exec(ctx, `INSERT INTO telemetry_spans (
    company_id, dedupe_key, time_unix_nano, end_unix_nano, duration_ns, host_name, oute_instance, oute_agent,
    service_name, session_id, oute_task_id, oute_swarm_round, oute_repo, oute_subscription, trace_id, span_id,
    parent_span_id, name, kind, status_code, status_message, scope_name, resource_attributes, attributes, events,
    links, received_unix_nano, ingested_by_key_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24,
    $25, $26, $27, $28)
ON CONFLICT (company_id, dedupe_key, time_unix_nano) DO NOTHING`,
		r.CompanyID, r.DedupeKey, r.TimeUnixNano, r.EndUnixNano, r.DurationNs, r.HostName, r.OuteInstance, r.OuteAgent,
		r.ServiceName, r.SessionID, r.OuteTaskID, r.OuteSwarmRound, r.OuteRepo, r.OuteSubscription, r.TraceID, r.SpanID,
		r.ParentSpanID, r.Name, r.Kind, r.StatusCode, r.StatusMessage, r.ScopeName, r.ResourceAttributes, r.Attributes,
		r.Events, r.Links, r.ReceivedUnixNano, r.IngestedByKeyID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// UpsertExternalAgent cria o agente externo com o nome dado, se ainda não existe na empresa (a comparação ignora caixa),
// e registra o texto que chegou como apelido. created diz se a linha é nova.
func UpsertExternalAgent(ctx context.Context, db DBTX, companyID, newID uuid.UUID, name string, now time.Time) (uuid.UUID, bool, error) {
	return upsertExternalEntity(ctx, db, externalEntity{
		table: "external_agent", aliasTable: "external_agent_alias", aliasKey: "agent_id",
	}, companyID, newID, name, now)
}

// UpsertExternalHost é o mesmo que UpsertExternalAgent para o host externo (ADR-0077).
func UpsertExternalHost(ctx context.Context, db DBTX, companyID, newID uuid.UUID, name string, now time.Time) (uuid.UUID, bool, error) {
	return upsertExternalEntity(ctx, db, externalEntity{
		table: "external_host", aliasTable: "external_host_alias", aliasKey: "host_id",
	}, companyID, newID, name, now)
}

// externalEntity são os nomes da tabela de entidade e da de apelidos, que só diferem entre agente e host.
type externalEntity struct {
	table      string
	aliasTable string
	aliasKey   string
}

func upsertExternalEntity(ctx context.Context, db DBTX, e externalEntity, companyID, newID uuid.UUID, name string, now time.Time) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := db.QueryRow(ctx, `INSERT INTO `+e.table+` (id, company_id, name, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (company_id, lower(name)) DO NOTHING
RETURNING id`, newID, companyID, name, now).Scan(&id)
	created := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		err = db.QueryRow(ctx, `SELECT id FROM `+e.table+` WHERE company_id = $1 AND lower(name) = lower($2)`,
			companyID, name).Scan(&id)
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if _, err := db.Exec(ctx, `INSERT INTO `+e.aliasTable+` (company_id, alias, `+e.aliasKey+`, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING`, companyID, name, id, now); err != nil {
		return uuid.Nil, false, err
	}
	return id, created, nil
}

// TouchExternalHost guarda a hora do último fato do host, sem recuar (ADR-0077; alerta de host parado, fatia seguinte).
func TouchExternalHost(ctx context.Context, db DBTX, hostID uuid.UUID, seen time.Time) error {
	_, err := db.Exec(ctx, `UPDATE external_host
SET last_seen_at = GREATEST(COALESCE(last_seen_at, $2), $2)
WHERE id = $1`, hostID, seen)
	return err
}

// ExternalConversationRow é a conversa externa como o banco a guarda (ADR-0078). Cada atributo tem a hora do fato que o
// trouxe: *At nulo quando o valor ainda não chegou.
type ExternalConversationRow struct {
	ID                uuid.UUID
	CompanyID         uuid.UUID
	SessionID         string
	Name              string
	HostID            *uuid.UUID
	HostAt            *int64
	AgentID           *uuid.UUID
	AgentAt           *int64
	OuteInstance      string
	OuteInstanceAt    *int64
	ServiceName       string
	ServiceNameAt     *int64
	SubscriptionKey   string
	SubscriptionKeyAt *int64
	OuteRepo          string
	OuteRepoAt        *int64
	FirstAt           int64
	LastAt            int64
}

// LockExternalConversation devolve a conversa do session.id, travada até o fim da transação; found é falso se ela ainda
// não existe.
func LockExternalConversation(ctx context.Context, db DBTX, companyID uuid.UUID, sessionID string) (ExternalConversationRow, bool, error) {
	var r ExternalConversationRow
	err := db.QueryRow(ctx, `SELECT id, company_id, session_id, name, host_id, host_at, agent_id, agent_at,
    oute_instance, oute_instance_at, service_name, service_name_at, subscription_key, subscription_key_at,
    oute_repo, oute_repo_at, first_at, last_at
FROM external_conversation
WHERE company_id = $1 AND session_id = $2
FOR UPDATE`, companyID, sessionID).Scan(&r.ID, &r.CompanyID, &r.SessionID, &r.Name, &r.HostID, &r.HostAt, &r.AgentID,
		&r.AgentAt, &r.OuteInstance, &r.OuteInstanceAt, &r.ServiceName, &r.ServiceNameAt, &r.SubscriptionKey,
		&r.SubscriptionKeyAt, &r.OuteRepo, &r.OuteRepoAt, &r.FirstAt, &r.LastAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExternalConversationRow{}, false, nil
	}
	if err != nil {
		return ExternalConversationRow{}, false, err
	}
	return r, true, nil
}

// ExternalConversationNameTaken diz se o nome já é de uma conversa da empresa (o gerador tenta o próximo).
func ExternalConversationNameTaken(ctx context.Context, db DBTX, companyID uuid.UUID, name string) (bool, error) {
	var taken bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM external_conversation WHERE company_id = $1 AND name = $2)`,
		companyID, name).Scan(&taken)
	return taken, err
}

// InsertExternalConversation cria a conversa. ok é falso se outra transação a criou antes (session_id ou nome repetido):
// o chamador relê a linha, ou tenta de novo na próxima chegada.
func InsertExternalConversation(ctx context.Context, db DBTX, r ExternalConversationRow, now time.Time) (bool, error) {
	tag, err := db.Exec(ctx, `INSERT INTO external_conversation (id, company_id, session_id, name, host_id, host_at,
    agent_id, agent_at, oute_instance, oute_instance_at, service_name, service_name_at, subscription_key,
    subscription_key_at, oute_repo, oute_repo_at, first_at, last_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $19)
ON CONFLICT DO NOTHING`, r.ID, r.CompanyID, r.SessionID, r.Name, r.HostID, r.HostAt, r.AgentID, r.AgentAt,
		r.OuteInstance, r.OuteInstanceAt, r.ServiceName, r.ServiceNameAt, r.SubscriptionKey, r.SubscriptionKeyAt,
		r.OuteRepo, r.OuteRepoAt, r.FirstAt, r.LastAt, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// UpdateExternalConversation grava os atributos, a janela e o horário de uma conversa já existente.
func UpdateExternalConversation(ctx context.Context, db DBTX, r ExternalConversationRow, now time.Time) error {
	_, err := db.Exec(ctx, `UPDATE external_conversation
SET host_id = $2, host_at = $3, agent_id = $4, agent_at = $5, oute_instance = $6, oute_instance_at = $7,
    service_name = $8, service_name_at = $9, subscription_key = $10, subscription_key_at = $11,
    oute_repo = $12, oute_repo_at = $13, first_at = $14, last_at = $15, updated_at = $16
WHERE id = $1`, r.ID, r.HostID, r.HostAt, r.AgentID, r.AgentAt, r.OuteInstance, r.OuteInstanceAt, r.ServiceName,
		r.ServiceNameAt, r.SubscriptionKey, r.SubscriptionKeyAt, r.OuteRepo, r.OuteRepoAt, r.FirstAt, r.LastAt, now)
	return err
}

// InsertIngestAPIKey grava uma chave de escopo ingest, presa à empresa (ADR-0076 item 3). user_id é quem a criou.
func InsertIngestAPIKey(ctx context.Context, db DBTX, id, companyID, userID uuid.UUID, name, prefix, hash, device string, now time.Time) error {
	_, err := db.Exec(ctx, `INSERT INTO api_keys (id, user_id, name, prefix, hash, scope, device, created_at, company_id)
VALUES ($1, $2, $3, $4, $5, 'ingest', $6, $7, $8)`, id, userID, name, prefix, hash, device, now, companyID)
	return err
}
