-- name: CreateFlow :one
INSERT INTO flows (id, company_id, slug, name, description, cron, timezone, enabled, persona_id, project_id,
                   priority, task_title, task_description, next_fire_at, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description, @cron, @timezone, @enabled, @persona_id, @project_id,
        @priority, @task_title, @task_description, @next_fire_at, @now, @now)
RETURNING *;

-- name: GetFlowByID :one
SELECT * FROM flows WHERE company_id = @company_id AND id = @id;

-- name: GetFlowByIDForUpdate :one
SELECT * FROM flows WHERE id = @id FOR UPDATE;

-- name: GetFlowBySlug :one
SELECT * FROM flows WHERE company_id = @company_id AND slug = @slug;

-- name: ListFlows :many
SELECT * FROM flows WHERE company_id = @company_id ORDER BY enabled DESC, name, id;

-- name: UpdateFlow :one
UPDATE flows
SET name = @name, description = @description, cron = @cron, timezone = @timezone, enabled = @enabled,
    persona_id = @persona_id, project_id = @project_id, priority = @priority,
    task_title = @task_title, task_description = @task_description,
    next_fire_at = @next_fire_at, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: MarkFlowFired :exec
UPDATE flows SET last_fired_at = @last_fired_at, next_fire_at = @next_fire_at, updated_at = @now
WHERE id = @id;

-- name: DueFlows :many
SELECT * FROM flows
WHERE enabled AND next_fire_at IS NOT NULL AND next_fire_at <= @now
ORDER BY next_fire_at, id
LIMIT @lim;

-- name: InsertFlowFire :one
-- Sem linhas quando (flow_id, scheduled_for) já existe.
INSERT INTO flow_fires (id, flow_id, company_id, scheduled_for, created_at)
VALUES (@id, @flow_id, @company_id, @scheduled_for, @now)
ON CONFLICT (flow_id, scheduled_for) DO NOTHING
RETURNING *;

-- name: GetFlowFire :one
SELECT * FROM flow_fires WHERE flow_id = @flow_id AND scheduled_for = @scheduled_for;

-- name: SetFlowFireTask :exec
UPDATE flow_fires SET task_id = @task_id WHERE id = @id;

-- name: CountFlowFires :one
SELECT count(*)::bigint FROM flow_fires WHERE flow_id = @flow_id;

-- name: ListFlowFires :many
SELECT f.*, COALESCE(c.task_prefix || '-' || t.number, '')::text AS task_key, t.title AS task_title, t.status AS task_status
FROM flow_fires f
LEFT JOIN tasks t ON t.id = f.task_id
LEFT JOIN companies c ON c.id = f.company_id
WHERE f.flow_id = @flow_id AND f.company_id = @company_id
ORDER BY f.scheduled_for DESC
LIMIT @lim;

-- name: ListFlowViews :many
SELECT sqlc.embed(f), p.name AS persona_name, COALESCE(pr.name, '')::text AS project_name
FROM flows f
JOIN personas p ON p.id = f.persona_id
LEFT JOIN projects pr ON pr.id = f.project_id
WHERE f.company_id = @company_id
ORDER BY f.enabled DESC, f.name, f.id;

-- name: GetFlowView :one
SELECT sqlc.embed(f), p.name AS persona_name, COALESCE(pr.name, '')::text AS project_name
FROM flows f
JOIN personas p ON p.id = f.persona_id
LEFT JOIN projects pr ON pr.id = f.project_id
WHERE f.company_id = @company_id AND f.id = @id;
