-- name: CreatePersona :one
INSERT INTO personas (id, company_id, slug, name, title, instructions, agent, model, adapter_config,
                      status, reports_to, workspace_id, heartbeat_interval_sec, budget_monthly_cents,
                      created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @title, @instructions, @agent, @model, @adapter_config,
        @status, @reports_to, @workspace_id, @heartbeat_interval_sec, @budget_monthly_cents,
        @now, @now)
RETURNING *;

-- name: GetPersonaByID :one
SELECT * FROM personas WHERE company_id = @company_id AND id = @id;

-- name: GetPersonaByIDForUpdate :one
SELECT * FROM personas WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetPersonaBySlug :one
SELECT * FROM personas WHERE company_id = @company_id AND slug = @slug;

-- name: ListPersonas :many
SELECT * FROM personas WHERE company_id = @company_id ORDER BY created_at, id;

-- name: UpdatePersona :one
UPDATE personas
SET name = @name, title = @title, instructions = @instructions, model = @model,
    status = @status, heartbeat_interval_sec = @heartbeat_interval_sec,
    workspace_id = @workspace_id, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetPersonaRunning :exec
UPDATE personas SET status = 'running', updated_at = @now
WHERE id = @id AND status IN ('active', 'idle', 'running');

-- name: SetPersonaAfterRun :exec
-- Moves a running persona to idle/error unless it still has another active run.
UPDATE personas p SET status = @status, updated_at = @now
WHERE p.id = @id AND p.status = 'running'
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.persona_id = p.id AND r.status IN ('queued', 'running'));
