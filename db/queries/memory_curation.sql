-- Consolidação de memória governada (ADR-0030).

-- name: GetMemorySettings :one
SELECT * FROM memory_settings WHERE company_id = @company_id;

-- name: GetMemorySettingsForUpdate :one
SELECT * FROM memory_settings WHERE company_id = @company_id FOR UPDATE;

-- name: UpsertMemorySettings :one
INSERT INTO memory_settings (company_id, enabled, interval_hours, curator_persona_id, next_fire_at, updated_at)
VALUES (@company_id, @enabled, @interval_hours, @curator_persona_id, @next_fire_at, @now)
ON CONFLICT (company_id) DO UPDATE
SET enabled = EXCLUDED.enabled, interval_hours = EXCLUDED.interval_hours,
    curator_persona_id = EXCLUDED.curator_persona_id, next_fire_at = EXCLUDED.next_fire_at,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DueMemorySettings :many
SELECT * FROM memory_settings
WHERE enabled AND curator_persona_id IS NOT NULL AND next_fire_at IS NOT NULL AND next_fire_at <= @now
  AND NOT EXISTS (SELECT 1 FROM companies c WHERE c.id = memory_settings.company_id AND c.archived_at IS NOT NULL)
ORDER BY next_fire_at, company_id
LIMIT @lim;

-- name: MarkMemoryFired :exec
UPDATE memory_settings SET last_fired_at = @last_fired_at, next_fire_at = @next_fire_at, updated_at = @now
WHERE company_id = @company_id;

-- name: InsertMemoryConsolidation :one
-- Sem linhas quando a rodada já existe (mesma origem e horário) ou o workspace já tem uma aberta.
INSERT INTO memory_consolidations (id, company_id, workspace_id, workspace_slug, origin, scheduled_for, created_at)
VALUES (@id, @company_id, @workspace_id, @workspace_slug, @origin, @scheduled_for, @now)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: SetMemoryConsolidationTask :exec
UPDATE memory_consolidations SET task_id = @task_id WHERE id = @id;

-- name: GetMemoryConsolidationByTaskForUpdate :one
SELECT * FROM memory_consolidations WHERE task_id = @task_id FOR UPDATE;

-- name: IsMemoryConsolidationTask :one
-- A task é a task de sistema de uma rodada de curadoria (qualquer estado da rodada).
SELECT EXISTS (SELECT 1 FROM memory_consolidations WHERE task_id = @task_id)::boolean;

-- name: FinishMemoryConsolidation :one
UPDATE memory_consolidations
SET status = @status, run_id = @run_id, summary = @summary, finished_at = @now
WHERE id = @id AND status = 'open'
RETURNING *;

-- name: ListMemoryConsolidations :many
SELECT sqlc.embed(c), COALESCE(co.task_prefix || '-' || t.number, '')::text AS task_key,
       r.id AS last_run_id, COALESCE(r.status, '')::text AS run_status,
       COALESCE(r.name, '')::text AS run_name, r.cost_usd AS run_cost_usd,
       (SELECT count(*) FROM memory_proposals p WHERE p.run_id = r.id)::bigint AS proposals
FROM memory_consolidations c
JOIN companies co ON co.id = c.company_id
LEFT JOIN tasks t ON t.id = c.task_id
LEFT JOIN LATERAL (
    SELECT rr.id, rr.status, rr.name, rr.cost_usd FROM runs rr WHERE rr.task_id = c.task_id ORDER BY rr.created_at DESC LIMIT 1
) r ON true
WHERE c.company_id = @company_id
ORDER BY c.created_at DESC, c.id DESC
LIMIT @lim;

-- name: ListStaleMemoryConsolidations :many
-- Abertas há tempo demais e sem run ativa: a run nunca começou (orçamento, curador parado).
SELECT c.* FROM memory_consolidations c
WHERE c.status = 'open' AND c.created_at < @before
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.task_id = c.task_id AND r.status IN ('queued','running'))
ORDER BY c.created_at
LIMIT @lim;

-- name: InsertMemoryProposal :one
-- Sem linhas quando a mesma chamada já foi proposta nesta run.
INSERT INTO memory_proposals (id, company_id, workspace_id, workspace_slug, run_id, kind, tool, arguments, targets,
                              proposed_content, current_content, diff, sha256, created_at, updated_at)
VALUES (@id, @company_id, @workspace_id, @workspace_slug, @run_id, @kind, @tool, @arguments, @targets,
        @proposed_content, @current_content, @diff, @sha256, @now, @now)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetMemoryProposalByRunSHA :one
SELECT * FROM memory_proposals WHERE run_id = @run_id AND sha256 = @sha256;

-- name: SetMemoryProposalGate :exec
UPDATE memory_proposals SET gate_id = @gate_id, updated_at = @now WHERE id = @id;

-- name: GetMemoryProposal :one
SELECT * FROM memory_proposals WHERE company_id = @company_id AND id = @id;

-- name: GetMemoryProposalForUpdate :one
SELECT * FROM memory_proposals WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetMemoryProposalByGateForUpdate :one
SELECT * FROM memory_proposals WHERE company_id = @company_id AND gate_id = @gate_id FOR UPDATE;

-- name: ListMemoryProposals :many
SELECT * FROM memory_proposals
WHERE company_id = @company_id
  AND (sqlc.narg(statuses)::text[] IS NULL OR status = ANY(sqlc.narg(statuses)::text[]))
ORDER BY CASE WHEN status = 'pending' THEN created_at END ASC NULLS LAST, created_at DESC, id DESC
LIMIT @lim;

-- name: DecideMemoryProposal :one
UPDATE memory_proposals
SET status = @status, decided_by_type = @decided_by_type, decided_by_id = @decided_by_id,
    decision_note = @decision_note, decided_at = @now, next_attempt_at = @next_attempt_at, updated_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: MarkMemoryProposalApplied :one
UPDATE memory_proposals
SET status = 'applied', applied_at = @now, last_error = '', next_attempt_at = NULL, attempt_started_at = NULL,
    attempts = attempts + 1, updated_at = @now
WHERE company_id = @company_id AND id = @id AND status IN ('approved','failed')
RETURNING *;

-- name: MarkMemoryProposalFailed :one
-- A aplicação falhou: fica falha com a próxima tentativa agendada (next_attempt_at nulo = desistiu; só retry manual).
UPDATE memory_proposals
SET status = 'failed', attempts = attempts + 1, last_error = @last_error, next_attempt_at = @next_attempt_at,
    attempt_started_at = NULL, updated_at = @now
WHERE company_id = @company_id AND id = @id AND status IN ('approved','failed')
RETURNING *;

-- name: SetMemoryProposalAttemptStarted :exec
-- Marca o início de uma tentativa de aplicação, antes de chamar o provedor de memória.
UPDATE memory_proposals SET attempt_started_at = @now, updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: RetryMemoryProposal :one
UPDATE memory_proposals
SET status = 'approved', next_attempt_at = @now, updated_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'failed'
RETURNING *;

-- name: DueMemoryProposals :many
SELECT id, company_id FROM memory_proposals
WHERE status IN ('approved','failed') AND next_attempt_at IS NOT NULL AND next_attempt_at <= @now
  AND NOT EXISTS (SELECT 1 FROM companies c WHERE c.id = memory_proposals.company_id AND c.archived_at IS NOT NULL)
ORDER BY next_attempt_at, id
LIMIT @lim;

-- name: UpsertMemoryGrant :one
INSERT INTO memory_grants (company_id, persona_id, workspace_id, access, created_at)
VALUES (@company_id, @persona_id, @workspace_id, @access, @now)
ON CONFLICT (persona_id, workspace_id) DO UPDATE SET access = EXCLUDED.access
RETURNING *;

-- name: DeleteMemoryGrant :execrows
DELETE FROM memory_grants WHERE company_id = @company_id AND persona_id = @persona_id AND workspace_id = @workspace_id;

-- name: GetMemoryGrantAccess :one
SELECT access FROM memory_grants WHERE company_id = @company_id AND persona_id = @persona_id AND workspace_id = @workspace_id;

-- name: ListMemoryGrants :many
SELECT sqlc.embed(g), p.name AS persona_name, w.slug AS workspace_slug
FROM memory_grants g
JOIN personas p ON p.id = g.persona_id
JOIN workspaces w ON w.id = g.workspace_id
WHERE g.company_id = @company_id
ORDER BY w.slug, p.name, g.persona_id;

-- name: CountPendingGatesOfKind :one
SELECT count(*)::bigint AS n FROM gates WHERE company_id = @company_id AND kind = @kind AND status = 'pending';
