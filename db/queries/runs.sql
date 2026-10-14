-- name: CreateRun :one
INSERT INTO runs (id, company_id, task_id, persona_id, status, trigger, agent, model, created_at)
VALUES (@id, @company_id, @task_id, @persona_id, 'queued', @trigger, @agent, @model, @now)
RETURNING *;

-- name: GetRunByID :one
SELECT * FROM runs WHERE company_id = @company_id AND id = @id;

-- name: GetRunByIDForUpdate :one
SELECT * FROM runs WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetRunView :one
SELECT sqlc.embed(r), (c.task_prefix || '-' || t.number)::text AS task_key, t.title AS task_title, p.name AS persona_name
FROM runs r
JOIN tasks t ON t.id = r.task_id
JOIN personas p ON p.id = r.persona_id
JOIN companies c ON c.id = r.company_id
WHERE r.company_id = @company_id AND r.id = @id;

-- name: ListRunViews :many
SELECT sqlc.embed(r), (c.task_prefix || '-' || t.number)::text AS task_key, t.title AS task_title, p.name AS persona_name
FROM runs r
JOIN tasks t ON t.id = r.task_id
JOIN personas p ON p.id = r.persona_id
JOIN companies c ON c.id = r.company_id
WHERE r.company_id = @company_id
  AND (sqlc.narg(task_id)::uuid IS NULL OR r.task_id = sqlc.narg(task_id)::uuid)
  AND (sqlc.narg(persona_id)::uuid IS NULL OR r.persona_id = sqlc.narg(persona_id)::uuid)
  AND (sqlc.narg(statuses)::text[] IS NULL OR r.status = ANY(sqlc.narg(statuses)::text[]))
ORDER BY r.created_at DESC, r.id DESC
LIMIT @lim;

-- name: ActiveRunForTask :one
SELECT * FROM runs WHERE task_id = @task_id AND status IN ('queued', 'running');

-- name: MarkRunStarted :one
UPDATE runs SET status = 'running', started_at = @now, trace_id = @trace_id, handoff_in = @handoff_in
WHERE company_id = @company_id AND id = @id AND status = 'queued'
RETURNING *;

-- name: FinishRun :one
UPDATE runs
SET status = @status, exit_code = @exit_code, error = @error, summary = @summary, cost_usd = @cost_usd,
    input_tokens = @input_tokens, output_tokens = @output_tokens,
    cache_read_tokens = @cache_read_tokens, cache_creation_tokens = @cache_creation_tokens,
    session_ref = @session_ref, model = @model, finished_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: CancelQueuedRun :one
UPDATE runs SET status = 'cancelled', finished_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'queued'
RETURNING *;

-- name: InterruptActiveRuns :many
UPDATE runs SET status = 'interrupted', finished_at = @now,
    error = CASE WHEN error = '' THEN 'interrupted: process restarted' ELSE error END
WHERE status IN ('queued', 'running')
RETURNING *;

-- name: ListWakeCandidates :many
SELECT t.company_id, t.persona_id AS persona_id, t.id AS task_id
FROM tasks t
JOIN personas p ON p.id = t.persona_id
WHERE t.status IN ('todo', 'in_progress')
  AND p.status IN ('active', 'idle', 'running')
  AND p.heartbeat_interval_sec > 0
  AND NOT EXISTS (SELECT 1 FROM runs a WHERE a.task_id = t.id AND a.status IN ('queued', 'running'))
  AND COALESCE((SELECT max(COALESCE(r.finished_at, r.created_at)) FROM runs r WHERE r.task_id = t.id), 'epoch'::timestamptz)
      + make_interval(secs => p.heartbeat_interval_sec) <= @now
ORDER BY t.updated_at
LIMIT @lim;

-- name: AppendRunEvent :one
INSERT INTO run_events (id, company_id, run_id, seq, kind, text, payload, occurred_at, received_at)
SELECT @id, r.company_id, r.id,
       COALESCE((SELECT max(e.seq) FROM run_events e WHERE e.run_id = r.id), 0) + 1,
       @kind, @text, @payload, @occurred_at, @now
FROM runs r
WHERE r.company_id = @company_id AND r.id = @run_id
RETURNING *;

-- name: ListRunEvents :many
SELECT * FROM run_events
WHERE company_id = @company_id AND run_id = @run_id AND seq > @after_seq
ORDER BY seq
LIMIT @lim;
