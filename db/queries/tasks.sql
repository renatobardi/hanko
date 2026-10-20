-- name: CreateTask :one
INSERT INTO tasks (id, company_id, number, title, description, status, priority, persona_id, project_id,
                   workspace_id, parent_id, created_by_type, created_by_id, model, goal_id, created_at, updated_at)
VALUES (@id, @company_id, @number, @title, @description, @status, @priority, @persona_id, @project_id,
        @workspace_id, @parent_id, @created_by_type, @created_by_id, @model, @goal_id, @now, @now)
RETURNING *;

-- name: GetTaskByID :one
SELECT * FROM tasks WHERE company_id = @company_id AND id = @id;

-- name: GetTaskByIDForUpdate :one
SELECT * FROM tasks WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetTaskView :one
SELECT sqlc.embed(t), COALESCE(p.name, '')::text AS persona_name, COALESCE(pr.name, '')::text AS project_name,
       ar.id AS active_run_id, c.task_prefix,
       EXISTS (SELECT 1 FROM memory_consolidations mc WHERE mc.task_id = t.id)::boolean AS system_task
FROM tasks t
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
LEFT JOIN projects pr ON pr.id = t.project_id
LEFT JOIN runs ar ON ar.task_id = t.id AND ar.status IN ('queued', 'running')
WHERE t.company_id = @company_id AND t.id = @id;

-- name: GetTaskViewByNumber :one
SELECT sqlc.embed(t), COALESCE(p.name, '')::text AS persona_name, COALESCE(pr.name, '')::text AS project_name,
       ar.id AS active_run_id, c.task_prefix,
       EXISTS (SELECT 1 FROM memory_consolidations mc WHERE mc.task_id = t.id)::boolean AS system_task
FROM tasks t
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
LEFT JOIN projects pr ON pr.id = t.project_id
LEFT JOIN runs ar ON ar.task_id = t.id AND ar.status IN ('queued', 'running')
WHERE t.company_id = @company_id AND t.number = @number;

-- name: ListTaskViews :many
-- Ordem: updated_at DESC, number DESC (índice tasks_company_updated_idx). A página é escolhida primeiro só pelas
-- chaves (CTE "page": com OFFSET grande o custo é de varrer o índice, sem juntar nada das linhas puladas) e só
-- então recebe os joins; a ordenação é repetida porque o join não preserva a ordem.
WITH page AS (
    SELECT t0.id FROM tasks t0
    WHERE t0.company_id = @company_id
      AND (sqlc.narg(statuses)::text[] IS NULL OR t0.status = ANY(sqlc.narg(statuses)::text[]))
      AND (sqlc.narg(persona_id)::uuid IS NULL OR t0.persona_id = sqlc.narg(persona_id)::uuid)
      AND (sqlc.narg(project_id)::uuid IS NULL OR t0.project_id = sqlc.narg(project_id)::uuid)
      AND (sqlc.narg(goal_id)::uuid IS NULL OR t0.goal_id = sqlc.narg(goal_id)::uuid)
      -- "Minhas" (ADR-0042): tasks em que a pessoa participa (task_participation, migration 00123)
      AND (sqlc.narg(mine_user)::uuid IS NULL OR t0.id IN (SELECT tp.task_id FROM task_participation(@company_id, sqlc.narg(mine_user)::uuid) tp))
      AND (sqlc.narg(q)::text IS NULL OR t0.title ILIKE '%' || sqlc.narg(q)::text || '%'
           OR t0.description ILIKE '%' || sqlc.narg(q)::text || '%')
    ORDER BY t0.updated_at DESC, t0.number DESC
    LIMIT @lim OFFSET @off
)
SELECT sqlc.embed(t), COALESCE(p.name, '')::text AS persona_name, COALESCE(pr.name, '')::text AS project_name,
       ar.id AS active_run_id, c.task_prefix
FROM page
JOIN tasks t ON t.id = page.id
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
LEFT JOIN projects pr ON pr.id = t.project_id
LEFT JOIN runs ar ON ar.task_id = t.id AND ar.status IN ('queued', 'running')
ORDER BY t.updated_at DESC, t.number DESC;

-- name: UpdateTask :one
UPDATE tasks
SET title = @title, description = @description, status = @status, priority = @priority,
    persona_id = @persona_id, project_id = @project_id, model = @model, closed_at = @closed_at, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetTaskStatus :exec
UPDATE tasks SET status = @status, updated_at = @now WHERE id = @id;

-- name: TouchTaskHumanChange :exec
-- Marca uma mudança não vista (status/atribuição por ator não-persona) para a rede de segurança do heartbeat.
UPDATE tasks SET last_human_change_at = @now WHERE id = @id;
