-- Cards de status (ADR-0032).

-- name: CountStatusCards :one
SELECT count(*)::bigint FROM status_cards WHERE company_id = @company_id;

-- name: CountStatusCardsByPersona :one
SELECT count(*)::bigint FROM status_cards
WHERE company_id = @company_id AND created_by_type = 'persona' AND created_by_id = @persona_id::uuid;

-- name: LockStatusCardsOfCompany :exec
-- Serializa as criações de uma empresa para os limites de quantidade não serem furados por corrida.
SELECT pg_advisory_xact_lock(hashtextextended('status_cards:' || (@company_id::uuid)::text, 0));

-- name: TryStatusCardTickLock :one
-- Só um tick de cards de status por vez na instalação (a trava vale até o fim da transação).
SELECT pg_try_advisory_xact_lock(hashtext('hanko:status_card_tick'))::boolean;

-- name: InsertStatusCard :one
INSERT INTO status_cards (id, company_id, title, title_pinned, interest, interest_hash, summarizer_persona_id, refresh_policy,
                          state, next_eval_at, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @title, @title_pinned, @interest, @interest_hash, @summarizer_persona_id, @refresh_policy,
        @state, @next_eval_at, @created_by_type, @created_by_id, @now, @now)
RETURNING *;

-- name: GetStatusCard :one
SELECT * FROM status_cards WHERE company_id = @company_id AND id = @id;

-- name: GetStatusCardForUpdate :one
SELECT * FROM status_cards WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetStatusCardForUpdateSkipLocked :one
SELECT * FROM status_cards WHERE company_id = @company_id AND id = @id FOR UPDATE SKIP LOCKED;

-- name: GetStatusCardByGeneratingTaskForUpdate :one
SELECT * FROM status_cards WHERE company_id = @company_id AND generating_task_id = @task_id::uuid FOR UPDATE;

-- name: ListStatusCards :many
SELECT sqlc.embed(c), COALESCE(p.name, '')::text AS summarizer_name, COALESCE(p.slug, '')::text AS summarizer_slug
FROM status_cards c
LEFT JOIN personas p ON p.id = c.summarizer_persona_id
WHERE c.company_id = @company_id
  AND (sqlc.narg(archived)::boolean IS NULL OR (c.archived_at IS NOT NULL) = sqlc.narg(archived)::boolean)
  AND (sqlc.narg(created_by_persona)::uuid IS NULL
       OR (c.created_by_type = 'persona' AND c.created_by_id = sqlc.narg(created_by_persona)::uuid))
ORDER BY c.archived_at NULLS FIRST, c.created_at DESC
LIMIT @lim;

-- name: UpdateStatusCard :one
-- Grava todos os campos mutáveis do card de uma vez (o service decide os valores).
UPDATE status_cards SET
    title = @title, title_pinned = @title_pinned, interest = @interest, interest_hash = @interest_hash,
    summarizer_persona_id = @summarizer_persona_id, queries = @queries, query_version = @query_version,
    query_compiled_at = @query_compiled_at, refresh_policy = @refresh_policy, state = @state,
    pending_change_count = @pending_change_count, pending_change_hash = @pending_change_hash, last_change_at = @last_change_at,
    fingerprint = @fingerprint, fingerprint_at = @fingerprint_at, mentioned_task_ids = @mentioned_task_ids,
    summary = @summary, summary_revision = @summary_revision, summary_query_version = @summary_query_version,
    incrementals_since_full = @incrementals_since_full, last_update_kind = @last_update_kind,
    last_generated_at = @last_generated_at, last_model = @last_model, generating_task_id = @generating_task_id,
    generating_since = @generating_since, failure_reason = @failure_reason, next_eval_at = @next_eval_at,
    archived_at = @archived_at, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteStatusCard :execrows
DELETE FROM status_cards WHERE company_id = @company_id AND id = @id;

-- name: ListDueStatusCards :many
SELECT id, company_id FROM status_cards
WHERE archived_at IS NULL AND next_eval_at IS NOT NULL AND next_eval_at <= @now
  AND NOT EXISTS (SELECT 1 FROM companies c WHERE c.id = status_cards.company_id AND c.archived_at IS NOT NULL)
ORDER BY next_eval_at
LIMIT @lim;

-- name: IsStatusCardTask :one
-- A task é uma task de sistema de card de status (montagem ou atualização), em qualquer estado.
SELECT EXISTS (SELECT 1 FROM status_card_updates WHERE task_id = @task_id::uuid)::boolean;

-- name: InsertStatusCardUpdate :one
INSERT INTO status_card_updates (id, company_id, card_id, kind, trigger, task_id, changes, fingerprint, query_version, started_at)
VALUES (@id, @company_id, @card_id, @kind, @trigger, @task_id, @changes, @fingerprint, @query_version, @now)
RETURNING *;

-- name: GetStatusCardUpdateByTask :one
SELECT * FROM status_card_updates WHERE task_id = @task_id::uuid;

-- name: GetStatusCardUpdateByTaskForUpdate :one
SELECT * FROM status_card_updates WHERE task_id = @task_id::uuid FOR UPDATE;

-- name: FinishStatusCardUpdate :one
UPDATE status_card_updates SET status = @status, error = @error, change_summary = @change_summary, finished_at = @now
WHERE id = @id
RETURNING *;

-- name: SetStatusCardUpdateRun :exec
-- O custo da run da rodada (tokens e US$ do ledger), gravado quando a run termina.
UPDATE status_card_updates
SET run_id = @run_id, input_tokens = @input_tokens, output_tokens = @output_tokens, cost_usd = @cost_usd, model = @model
WHERE id = @id;

-- name: ListStatusCardUpdates :many
SELECT sqlc.embed(u), COALESCE(co.task_prefix || '-' || t.number, '')::text AS task_key, COALESCE(r.name, '')::text AS run_name
FROM status_card_updates u
JOIN companies co ON co.id = u.company_id
LEFT JOIN tasks t ON t.id = u.task_id
LEFT JOIN runs r ON r.id = u.run_id
WHERE u.company_id = @company_id AND u.card_id = @card_id
ORDER BY u.started_at DESC, u.id DESC
LIMIT @lim;

-- name: PruneStatusCardUpdates :exec
DELETE FROM status_card_updates d
WHERE d.card_id = @card_id AND d.status <> 'running' AND d.id NOT IN (
    SELECT k.id FROM status_card_updates k WHERE k.card_id = @card_id ORDER BY k.started_at DESC, k.id DESC LIMIT @keep
);

-- name: CardTokensSince :one
-- Tokens gastos pelas rodadas do card desde o instante (o teto diário).
SELECT COALESCE(sum(input_tokens + output_tokens), 0)::bigint FROM status_card_updates
WHERE card_id = @card_id AND started_at >= @since;

-- name: CardGenerationsSince :many
-- Inícios das gerações (full/incremental) desde o instante, do mais antigo ao mais novo (o teto por hora).
SELECT started_at FROM status_card_updates
WHERE card_id = @card_id AND kind IN ('full','incremental') AND started_at >= @since
ORDER BY started_at;

-- name: InsertStatusCardRevision :one
INSERT INTO status_card_revisions (id, company_id, card_id, revision, markdown, change_summary, kind, run_id, model, mentioned_task_ids, created_at)
VALUES (@id, @company_id, @card_id, @revision, @markdown, @change_summary, @kind, @run_id, @model, @mentioned_task_ids, @now)
RETURNING *;

-- name: ListStatusCardRevisions :many
SELECT * FROM status_card_revisions WHERE company_id = @company_id AND card_id = @card_id
ORDER BY revision DESC
LIMIT @lim;

-- name: GetStatusCardRevision :one
SELECT * FROM status_card_revisions WHERE company_id = @company_id AND card_id = @card_id AND revision = @revision;

-- name: PruneStatusCardRevisions :exec
DELETE FROM status_card_revisions WHERE card_id = @card_id AND revision <= @below;

-- name: StatusCardMatchTasks :many
-- As tasks que casam com um filtro do card, mais recentes primeiro, com o último comentário humano. Tasks de
-- sistema (montagem e atualização de cards, curadoria de memória) nunca entram: o card não observa a si mesmo.
SELECT t.id, t.number, c.task_prefix, t.title, t.status, t.priority, t.persona_id, COALESCE(p.name, '')::text AS persona_name,
       t.updated_at, COALESCE(hc.id::text, '')::text AS human_comment_id,
       COALESCE(hc.created_at, 'epoch'::timestamptz)::timestamptz AS human_comment_at, COALESCE(hc.body, '')::text AS human_comment_body
FROM tasks t
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
LEFT JOIN LATERAL (
    SELECT cm.id, cm.created_at, left(cm.body, 400) AS body FROM task_comments cm
    WHERE cm.task_id = t.id AND cm.author_type = 'human'
    ORDER BY cm.created_at DESC, cm.id DESC LIMIT 1
) hc ON true
WHERE t.company_id = @company_id
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY(sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(priorities)::text[] IS NULL OR t.priority = ANY(sqlc.narg(priorities)::text[]))
  AND (sqlc.narg(persona_id)::uuid IS NULL OR t.persona_id = sqlc.narg(persona_id)::uuid)
  AND (sqlc.narg(project_id)::uuid IS NULL OR t.project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(goal_id)::uuid IS NULL OR t.goal_id = sqlc.narg(goal_id)::uuid)
  AND (sqlc.narg(q)::text IS NULL OR t.title ILIKE '%' || sqlc.narg(q)::text || '%'
       OR t.description ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (NOT @open_only::boolean OR t.status NOT IN ('done','cancelled'))
  AND NOT EXISTS (SELECT 1 FROM status_card_updates su WHERE su.task_id = t.id)
  AND NOT EXISTS (SELECT 1 FROM memory_consolidations mc WHERE mc.task_id = t.id)
ORDER BY t.updated_at DESC, t.number DESC
LIMIT @lim;

-- name: StatusCardTasksByNumber :many
SELECT t.id, t.number, c.task_prefix, t.title, t.status
FROM tasks t JOIN companies c ON c.id = t.company_id
WHERE t.company_id = @company_id AND t.number = ANY(@numbers::bigint[]);

-- name: StatusCardTasksByID :many
SELECT t.id, t.number, c.task_prefix, t.title, t.status, t.priority, COALESCE(p.name, '')::text AS persona_name
FROM tasks t JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
WHERE t.company_id = @company_id AND t.id = ANY(@ids::uuid[])
ORDER BY t.number;

-- name: SetStatusCardDelivery :exec
-- Motor (persona ou decision model), nome do artefato e presença no Painel (ADR-0048). O nome só é gravado uma vez.
UPDATE status_cards
SET engine = @engine, on_dashboard = @on_dashboard,
    artifact_name = CASE WHEN artifact_name = '' THEN @artifact_name ELSE artifact_name END,
    updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: SetStatusCardArtifact :exec
UPDATE status_cards SET artifact_id = @artifact_id WHERE company_id = @company_id AND id = @id;

-- name: ListDashboardStatusCards :many
SELECT * FROM status_cards
WHERE company_id = @company_id AND on_dashboard AND archived_at IS NULL AND summary_revision > 0
ORDER BY last_generated_at DESC NULLS LAST, created_at DESC
LIMIT 6;
