-- name: CreateActivity :one
INSERT INTO activity_events (id, company_id, actor_type, actor_id, verb, subject_type, subject_id, data,
                             event_id, occurred_at, received_at)
VALUES (@id, @company_id, @actor_type, @actor_id, @verb, @subject_type, @subject_id, @data,
        @event_id, @occurred_at, @occurred_at)
RETURNING *;

-- name: GetActivityByEventID :one
SELECT * FROM activity_events WHERE company_id = @company_id AND event_id = @event_id;

-- name: ListActivity :many
SELECT * FROM activity_events
WHERE company_id = @company_id
  AND (sqlc.narg(subject_type)::text IS NULL OR subject_type = sqlc.narg(subject_type)::text)
  AND (sqlc.narg(subject_id)::uuid IS NULL OR subject_id = sqlc.narg(subject_id)::uuid)
  AND (sqlc.narg(actor_type)::text IS NULL OR actor_type = sqlc.narg(actor_type)::text)
  AND (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(before)::timestamptz IS NULL OR occurred_at < sqlc.narg(before)::timestamptz)
ORDER BY occurred_at DESC, id DESC
LIMIT @lim;

-- name: DashboardCounts :one
SELECT
  (SELECT count(*) FROM personas pp WHERE pp.company_id = @company_id AND pp.status IN ('active', 'idle', 'running'))::bigint AS active_personas,
  (SELECT count(*) FROM tasks tp WHERE tp.company_id = @company_id AND tp.status = 'in_progress')::bigint AS tasks_in_progress,
  (SELECT count(*) FROM tasks to2 WHERE to2.company_id = @company_id AND to2.status NOT IN ('done', 'cancelled'))::bigint AS open_tasks,
  (SELECT count(*) FROM runs rr WHERE rr.company_id = @company_id AND rr.status = 'running')::bigint AS running_runs;

-- name: MonthCost :one
SELECT COALESCE(SUM(cost_usd), 0)::float8 AS cost
FROM runs
WHERE company_id = @company_id AND created_at >= @from_ts AND created_at < @to_ts;
