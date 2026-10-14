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
    workspace_id = @workspace_id, updated_at = @now,
    consecutive_failures = CASE WHEN @status::text <> status THEN 0 ELSE consecutive_failures END
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetPersonaRunning :exec
UPDATE personas SET status = 'running', updated_at = @now
WHERE id = @id AND status IN ('active', 'idle', 'running');

-- name: SetPersonaAfterRun :exec
-- Fecha o ciclo de uma run: a persona volta a idle e registra a falha. Só vira error depois de
-- @max_failures falhas seguidas; sucesso zera o contador; outcome neutro (cancelada, interrompida)
-- não mexe nele. Não mexe na persona se ela ainda tem outra run ativa.
UPDATE personas p SET
    consecutive_failures = CASE @outcome::text
        WHEN 'fail' THEN p.consecutive_failures + 1
        WHEN 'ok' THEN 0
        ELSE p.consecutive_failures END,
    status = CASE WHEN @outcome::text = 'fail' AND p.consecutive_failures + 1 >= @max_failures::int
                  THEN 'error' ELSE 'idle' END,
    last_error = CASE @outcome::text WHEN 'fail' THEN @last_error::text WHEN 'ok' THEN NULL ELSE p.last_error END,
    last_error_at = CASE @outcome::text WHEN 'fail' THEN @now::timestamptz WHEN 'ok' THEN NULL ELSE p.last_error_at END,
    updated_at = @now
WHERE p.id = @id AND p.status = 'running'
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.persona_id = p.id AND r.status IN ('queued', 'running'));
