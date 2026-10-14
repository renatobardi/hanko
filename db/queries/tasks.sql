-- name: CreateTask :one
INSERT INTO tasks (id, company_id, number, title, description, status, priority, persona_id, project_id,
                   workspace_id, parent_id, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @number, @title, @description, @status, @priority, @persona_id, @project_id,
        @workspace_id, @parent_id, @created_by_type, @created_by_id, @now, @now)
RETURNING *;

-- name: GetTaskByID :one
SELECT * FROM tasks WHERE company_id = @company_id AND id = @id;

-- name: GetTaskByIDForUpdate :one
SELECT * FROM tasks WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetTaskView :one
SELECT sqlc.embed(t), COALESCE(p.name, '')::text AS persona_name, COALESCE(pr.name, '')::text AS project_name,
       ar.id AS active_run_id, c.task_prefix
FROM tasks t
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
LEFT JOIN projects pr ON pr.id = t.project_id
LEFT JOIN runs ar ON ar.task_id = t.id AND ar.status IN ('queued', 'running')
WHERE t.company_id = @company_id AND t.id = @id;

-- name: GetTaskViewByNumber :one
SELECT sqlc.embed(t), COALESCE(p.name, '')::text AS persona_name, COALESCE(pr.name, '')::text AS project_name,
       ar.id AS active_run_id, c.task_prefix
FROM tasks t
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
LEFT JOIN projects pr ON pr.id = t.project_id
LEFT JOIN runs ar ON ar.task_id = t.id AND ar.status IN ('queued', 'running')
WHERE t.company_id = @company_id AND t.number = @number;

-- name: ListTaskViews :many
SELECT sqlc.embed(t), COALESCE(p.name, '')::text AS persona_name, COALESCE(pr.name, '')::text AS project_name,
       ar.id AS active_run_id, c.task_prefix
FROM tasks t
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
LEFT JOIN projects pr ON pr.id = t.project_id
LEFT JOIN runs ar ON ar.task_id = t.id AND ar.status IN ('queued', 'running')
WHERE t.company_id = @company_id
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY(sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(persona_id)::uuid IS NULL OR t.persona_id = sqlc.narg(persona_id)::uuid)
  AND (sqlc.narg(project_id)::uuid IS NULL OR t.project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(q)::text IS NULL OR t.title ILIKE '%' || sqlc.narg(q)::text || '%'
       OR t.description ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY t.updated_at DESC, t.number DESC
LIMIT @lim OFFSET @off;

-- name: UpdateTask :one
UPDATE tasks
SET title = @title, description = @description, status = @status, priority = @priority,
    persona_id = @persona_id, project_id = @project_id, closed_at = @closed_at, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetTaskStatus :exec
UPDATE tasks SET status = @status, updated_at = @now WHERE id = @id;

-- name: TouchTaskHumanChange :exec
-- Marca uma mudança não vista (status/atribuição por ator não-persona) para a rede de segurança do heartbeat.
UPDATE tasks SET last_human_change_at = @now WHERE id = @id;
