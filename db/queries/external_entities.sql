-- Agentes e hosts externos: listar, ver, renomear, arquivar, sempre ligado e juntar (ADR-0075 item 2; ADR-0077 itens 3 e 4).
-- Juntar move apelidos e as referências de sessão e de conversa; o que ficou no agente juntado é arquivado.

-- name: ListExternalAgentViews :many
SELECT a.id, a.name, a.archived_at, a.created_at, a.updated_at,
       COALESCE(array_agg(al.alias ORDER BY al.alias) FILTER (WHERE al.alias IS NOT NULL), '{}')::text[] AS aliases
FROM external_agent a
LEFT JOIN external_agent_alias al ON al.agent_id = a.id
WHERE a.company_id = @company_id AND (@include_archived::bool OR a.archived_at IS NULL)
GROUP BY a.id
ORDER BY lower(a.name), a.id
LIMIT @lim::int OFFSET @off::int;

-- name: GetExternalAgentView :one
SELECT a.id, a.name, a.archived_at, a.created_at, a.updated_at,
       COALESCE(array_agg(al.alias ORDER BY al.alias) FILTER (WHERE al.alias IS NOT NULL), '{}')::text[] AS aliases
FROM external_agent a
LEFT JOIN external_agent_alias al ON al.agent_id = a.id
WHERE a.company_id = @company_id AND a.id = @id
GROUP BY a.id;

-- name: RenameExternalAgent :one
UPDATE external_agent SET name = @name, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING id;

-- name: SetExternalAgentArchived :exec
UPDATE external_agent SET archived_at = sqlc.narg(archived_at)::timestamptz, updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: MoveExternalAgentAliases :exec
UPDATE external_agent_alias SET agent_id = @to_id
WHERE company_id = @company_id AND agent_id = @from_id;

-- name: MoveExternalSessionsAgent :exec
UPDATE external_session SET agent_id = @to_id, updated_at = @now
WHERE company_id = @company_id AND agent_id = @from_id;

-- name: MoveExternalConversationsAgent :exec
UPDATE external_conversation SET agent_id = @to_id, updated_at = @now
WHERE company_id = @company_id AND agent_id = @from_id;

-- name: ListExternalHostViews :many
SELECT h.id, h.name, h.always_on, h.last_seen_at, h.archived_at, h.created_at, h.updated_at,
       COALESCE(array_agg(al.alias ORDER BY al.alias) FILTER (WHERE al.alias IS NOT NULL), '{}')::text[] AS aliases
FROM external_host h
LEFT JOIN external_host_alias al ON al.host_id = h.id
WHERE h.company_id = @company_id AND (@include_archived::bool OR h.archived_at IS NULL)
GROUP BY h.id
ORDER BY lower(h.name), h.id
LIMIT @lim::int OFFSET @off::int;

-- name: GetExternalHostView :one
SELECT h.id, h.name, h.always_on, h.last_seen_at, h.archived_at, h.created_at, h.updated_at,
       COALESCE(array_agg(al.alias ORDER BY al.alias) FILTER (WHERE al.alias IS NOT NULL), '{}')::text[] AS aliases
FROM external_host h
LEFT JOIN external_host_alias al ON al.host_id = h.id
WHERE h.company_id = @company_id AND h.id = @id
GROUP BY h.id;

-- name: RenameExternalHost :one
UPDATE external_host SET name = @name, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING id;

-- name: SetExternalHostAlwaysOn :exec
UPDATE external_host SET always_on = @always_on, updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: SetExternalHostArchived :exec
UPDATE external_host SET archived_at = sqlc.narg(archived_at)::timestamptz, updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: MoveExternalHostAliases :exec
UPDATE external_host_alias SET host_id = @to_id
WHERE company_id = @company_id AND host_id = @from_id;

-- name: MoveExternalConversationsHost :exec
UPDATE external_conversation SET host_id = @to_id, updated_at = @now
WHERE company_id = @company_id AND host_id = @from_id;

-- name: MergeExternalHostFacts :exec
-- O host que fica herda o sempre ligado de qualquer um dos dois, e a última hora de fato do mais recente.
UPDATE external_host SET
  always_on = always_on OR @from_always_on::bool,
  last_seen_at = GREATEST(last_seen_at, sqlc.narg(from_seen)::timestamptz),
  updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: LockExternalAgent :one
-- Trava a linha do agente na junção (ordem de id, para duas junções cruzadas não se esperarem).
SELECT id, archived_at FROM external_agent WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: LockExternalHost :one
SELECT id, archived_at FROM external_host WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: ArchiveMergedExternalAgent :exec
-- Arquiva o juntado sem mexer no horário de quando foi arquivado antes (COALESCE).
UPDATE external_agent SET archived_at = COALESCE(archived_at, @now), updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: ArchiveMergedExternalHost :exec
UPDATE external_host SET archived_at = COALESCE(archived_at, @now), updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: MoveModelCallsAgent :exec
UPDATE model_calls SET agent_id = @to_id WHERE company_id = @company_id AND agent_id = @from_id;

-- name: MoveModelCallsHost :exec
UPDATE model_calls SET host_id = @to_id WHERE company_id = @company_id AND host_id = @from_id;

-- name: MoveExternalApprovalsAgent :exec
UPDATE external_approval SET agent_id = @to_id WHERE company_id = @company_id AND agent_id = @from_id;

-- name: MoveExternalApprovalsHost :exec
UPDATE external_approval SET host_id = @to_id WHERE company_id = @company_id AND host_id = @from_id;

-- Agregado diário (#153): juntar agente soma as linhas do agente juntado nas do que fica. Quando o que fica já tem a
-- mesma chave (dia, host, workspace, modelo, assinatura), os contadores somam; a linha do juntado sai depois.
-- name: MergeCallsDailyAgent :exec
INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key, calls, errors,
    input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_real_usd, cost_list_usd, unpriced_calls)
SELECT company_id, day, host_id, @to_id, workspace_id, model, subscription_key, calls, errors,
    input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_real_usd, cost_list_usd, unpriced_calls
FROM calls_daily WHERE calls_daily.company_id = @company_id AND calls_daily.agent_id = @from_id
ON CONFLICT (company_id, day, host_id, agent_id, workspace_id, model, subscription_key) DO UPDATE SET
    calls = calls_daily.calls + EXCLUDED.calls,
    errors = calls_daily.errors + EXCLUDED.errors,
    input_tokens = calls_daily.input_tokens + EXCLUDED.input_tokens,
    output_tokens = calls_daily.output_tokens + EXCLUDED.output_tokens,
    cache_read_tokens = calls_daily.cache_read_tokens + EXCLUDED.cache_read_tokens,
    cache_creation_tokens = calls_daily.cache_creation_tokens + EXCLUDED.cache_creation_tokens,
    cost_real_usd = calls_daily.cost_real_usd + EXCLUDED.cost_real_usd,
    cost_list_usd = calls_daily.cost_list_usd + EXCLUDED.cost_list_usd,
    unpriced_calls = calls_daily.unpriced_calls + EXCLUDED.unpriced_calls;

-- name: DeleteCallsDailyOfAgent :exec
DELETE FROM calls_daily WHERE company_id = @company_id AND agent_id = @agent_id;

-- name: MergeCallsDailyHost :exec
INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key, calls, errors,
    input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_real_usd, cost_list_usd, unpriced_calls)
SELECT company_id, day, @to_id, agent_id, workspace_id, model, subscription_key, calls, errors,
    input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_real_usd, cost_list_usd, unpriced_calls
FROM calls_daily WHERE calls_daily.company_id = @company_id AND calls_daily.host_id = @from_id
ON CONFLICT (company_id, day, host_id, agent_id, workspace_id, model, subscription_key) DO UPDATE SET
    calls = calls_daily.calls + EXCLUDED.calls,
    errors = calls_daily.errors + EXCLUDED.errors,
    input_tokens = calls_daily.input_tokens + EXCLUDED.input_tokens,
    output_tokens = calls_daily.output_tokens + EXCLUDED.output_tokens,
    cache_read_tokens = calls_daily.cache_read_tokens + EXCLUDED.cache_read_tokens,
    cache_creation_tokens = calls_daily.cache_creation_tokens + EXCLUDED.cache_creation_tokens,
    cost_real_usd = calls_daily.cost_real_usd + EXCLUDED.cost_real_usd,
    cost_list_usd = calls_daily.cost_list_usd + EXCLUDED.cost_list_usd,
    unpriced_calls = calls_daily.unpriced_calls + EXCLUDED.unpriced_calls;

-- name: DeleteCallsDailyOfHost :exec
DELETE FROM calls_daily WHERE company_id = @company_id AND host_id = @host_id;

-- Lista da outbox do host (ADR-0081 item 5; #153): a de hora mais nova vence. Na junção, a lista do juntado só entra se
-- for mais nova; em empate, fica a do que fica.
-- name: MergeOutboxSnapshotHost :exec
INSERT INTO external_outbox_snapshot (host_id, company_id, snapshot_at, canal_ids, received_at)
SELECT @to_id, company_id, snapshot_at, canal_ids, received_at
FROM external_outbox_snapshot WHERE external_outbox_snapshot.company_id = @company_id AND external_outbox_snapshot.host_id = @from_id
ON CONFLICT (host_id) DO UPDATE SET
    snapshot_at = EXCLUDED.snapshot_at,
    canal_ids = EXCLUDED.canal_ids,
    received_at = EXCLUDED.received_at
WHERE external_outbox_snapshot.snapshot_at < EXCLUDED.snapshot_at;

-- name: DeleteOutboxSnapshotOfHost :exec
DELETE FROM external_outbox_snapshot WHERE company_id = @company_id AND host_id = @host_id;
