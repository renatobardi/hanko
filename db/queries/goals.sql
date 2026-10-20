-- name: CreateGoal :one
INSERT INTO goals (id, company_id, project_id, parent_id, slug, title, description, metric, unit, start_value, target_value,
                   current_value, due_date, status, owner_persona_id, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @project_id, @parent_id, @slug, @title, @description, @metric, @unit, @start_value, @target_value,
        @current_value, @due_date, @status, @owner_persona_id, @created_by_type, @created_by_id, @now, @now)
RETURNING *;

-- name: GetGoalByID :one
SELECT * FROM goals WHERE company_id = @company_id AND id = @id;

-- name: GetGoalByIDForUpdate :one
SELECT * FROM goals WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetGoalBySlug :one
SELECT * FROM goals WHERE company_id = @company_id AND slug = @slug;

-- name: ListGoals :many
-- status e project são filtros opcionais; ativas primeiro, depois atingidas e abandonadas.
SELECT * FROM goals
WHERE company_id = @company_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'achieved' THEN 1 ELSE 2 END, due_date NULLS LAST, created_at, id;

-- name: UpdateGoal :one
UPDATE goals
SET project_id = @project_id, title = @title, description = @description, metric = @metric, unit = @unit,
    start_value = @start_value, target_value = @target_value, current_value = @current_value, due_date = @due_date,
    status = @status, owner_persona_id = @owner_persona_id, achieved_at = @achieved_at, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: CreateGoalUpdate :one
INSERT INTO goal_updates (id, company_id, goal_id, value, note, author_type, author_id, created_at)
VALUES (@id, @company_id, @goal_id, @value, @note, @author_type, @author_id, @now)
RETURNING *;

-- name: ListGoalUpdates :many
-- Os mais recentes (até lim), devolvidos do mais novo para o mais antigo.
SELECT * FROM goal_updates WHERE company_id = @company_id AND goal_id = @goal_id
ORDER BY created_at DESC, id DESC LIMIT @lim;

-- name: GoalTaskCounts :many
SELECT status, count(*)::bigint AS n FROM tasks
WHERE company_id = @company_id AND goal_id = @goal_id
GROUP BY status;

-- name: GoalTaskCountsAll :many
SELECT goal_id, status, count(*)::bigint AS n FROM tasks
WHERE company_id = @company_id AND goal_id IS NOT NULL
GROUP BY goal_id, status;

-- name: SetTaskGoal :exec
UPDATE tasks SET goal_id = @goal_id, updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: ListGoalsForBriefing :many
-- Metas ativas do projeto da task, mais a meta da própria task (se ativa); a da task vem sempre primeiro, para
-- não ser cortada pelo limite.
SELECT * FROM goals
WHERE company_id = @company_id AND status = 'active'
  AND ((sqlc.narg(project_id)::uuid IS NOT NULL AND project_id = sqlc.narg(project_id)::uuid)
       OR (sqlc.narg(goal_id)::uuid IS NOT NULL AND id = sqlc.narg(goal_id)::uuid))
ORDER BY (sqlc.narg(goal_id)::uuid IS NOT NULL AND id = sqlc.narg(goal_id)::uuid) DESC, due_date NULLS LAST, created_at, id
LIMIT 6;

-- name: ListRecentlyAchievedGoals :many
SELECT * FROM goals
WHERE company_id = @company_id AND status = 'achieved' AND achieved_at >= @since
ORDER BY achieved_at DESC, id DESC
LIMIT 20;

-- name: ListGoalNodes :many
-- A floresta de metas da empresa (árvore, progresso agregado, cadeia do briefing). Poucas linhas por empresa.
SELECT id, parent_id, slug, title, status, start_value, target_value, current_value
FROM goals WHERE company_id = @company_id
ORDER BY created_at, id;

-- name: SetGoalParent :exec
UPDATE goals SET parent_id = @parent_id, updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: SetGoalStatus :exec
-- Propagação do agregado: a meta-pai atinge o alvo quando as submetas atingem (ou volta a ativa).
UPDATE goals SET status = @status, achieved_at = @achieved_at, updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: CountGoalChildren :one
SELECT count(*)::bigint FROM goals WHERE company_id = @company_id AND parent_id = @id AND status <> 'abandoned';

-- name: ListGoalsByIDs :many
SELECT * FROM goals WHERE company_id = @company_id AND id = ANY(@ids::uuid[]);
