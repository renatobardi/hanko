-- name: CreateWorkspace :one
INSERT INTO workspaces (id, company_id, slug, name, path, repo_url, default_branch, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @path, @repo_url, @default_branch, @now, @now)
RETURNING *;

-- name: GetWorkspaceByID :one
SELECT * FROM workspaces WHERE company_id = @company_id AND id = @id;

-- name: GetWorkspaceBySlug :one
SELECT * FROM workspaces WHERE company_id = @company_id AND slug = @slug;

-- name: ListWorkspaces :many
SELECT * FROM workspaces WHERE company_id = @company_id ORDER BY created_at, id;

-- name: CreateProject :one
INSERT INTO projects (id, company_id, slug, name, description, workspace_id, status, budget_monthly_cents, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description, @workspace_id, @status, @budget_monthly_cents, @now, @now)
RETURNING *;

-- name: GetProjectByID :one
SELECT * FROM projects WHERE company_id = @company_id AND id = @id;

-- name: GetProjectBySlug :one
SELECT * FROM projects WHERE company_id = @company_id AND slug = @slug;

-- name: ListProjects :many
SELECT * FROM projects WHERE company_id = @company_id ORDER BY created_at, id;

-- name: UpdateWorkspace :one
UPDATE workspaces
SET name = @name, path = @path, repo_url = @repo_url, default_branch = @default_branch, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteWorkspace :execrows
DELETE FROM workspaces WHERE company_id = @company_id AND id = @id;

-- name: ListWorkspacePersonas :many
-- Personas que usam o workspace como padrão.
SELECT id, slug, name FROM personas WHERE company_id = @company_id AND workspace_id = @workspace_id ORDER BY name, id;

-- name: ListWorkspaceProjects :many
SELECT id, slug, name FROM projects WHERE company_id = @company_id AND workspace_id = @workspace_id ORDER BY name, id;

-- name: CountWorkspaceOpenTasks :one
-- Tasks abertas com este workspace escolhido de propósito (as fechadas não seguram a exclusão).
SELECT count(*)::bigint FROM tasks
WHERE company_id = @company_id AND workspace_id = @workspace_id AND status NOT IN ('done', 'cancelled');

-- name: UnlinkWorkspacePersonas :many
UPDATE personas SET workspace_id = NULL, updated_at = @now
WHERE company_id = @company_id AND workspace_id = @workspace_id
RETURNING id;

-- name: UnlinkWorkspaceProjects :many
UPDATE projects SET workspace_id = NULL, updated_at = @now
WHERE company_id = @company_id AND workspace_id = @workspace_id
RETURNING id;

-- name: UnlinkWorkspaceTasks :execrows
UPDATE tasks SET workspace_id = NULL
WHERE company_id = @company_id AND workspace_id = @workspace_id;

-- name: UpdateProject :one
UPDATE projects
SET name = @name, description = @description, workspace_id = @workspace_id, status = @status,
    budget_monthly_cents = @budget_monthly_cents, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: ProjectTaskCounts :many
-- Tasks do projeto por status (progresso).
SELECT status, count(*)::bigint AS n FROM tasks
WHERE company_id = @company_id AND project_id = @project_id
GROUP BY status;

-- name: ProjectTaskCountsAll :many
-- Tasks de todos os projetos da empresa por (projeto, status): a lista de projetos.
SELECT project_id, status, count(*)::bigint AS n FROM tasks
WHERE company_id = @company_id AND project_id IS NOT NULL
GROUP BY project_id, status;

-- name: ProjectPersonas :many
-- Personas com task no projeto, com a contagem de tasks abertas e totais.
SELECT p.id, p.slug, p.name, count(*)::bigint AS tasks,
       (count(*) FILTER (WHERE t.status NOT IN ('done', 'cancelled')))::bigint AS open_tasks
FROM tasks t
JOIN personas p ON p.id = t.persona_id
WHERE t.company_id = @company_id AND t.project_id = @project_id
GROUP BY p.id, p.slug, p.name
ORDER BY open_tasks DESC, p.name, p.id;

-- name: SpendByProject :many
-- Gasto em USD por projeto no intervalo [from, to): só runs de tasks com projeto.
SELECT t.project_id, COALESCE(SUM(r.cost_usd), 0)::float8 AS spend
FROM runs r
JOIN tasks t ON t.id = r.task_id
WHERE t.company_id = @company_id AND t.project_id IS NOT NULL
  AND r.created_at >= @from_ts AND r.created_at < @to_ts
GROUP BY t.project_id;

-- name: SpendBetweenProject :one
-- Gasto em USD das runs das tasks de um projeto no intervalo [from, to).
SELECT COALESCE(SUM(r.cost_usd), 0)::float8 AS spend
FROM tasks t
JOIN runs r ON r.task_id = t.id
WHERE t.company_id = @company_id AND t.project_id = @project_id
  AND r.created_at >= @from_ts AND r.created_at < @to_ts;

-- name: ActiveRunReservesProject :many
-- Como ActiveRunReserves, mas só das runs abertas de tasks do projeto.
SELECT r.persona_id, count(*)::bigint AS active,
       GREATEST(COALESCE((SELECT avg(x.cost_usd) FROM (
                   SELECT cost_usd FROM runs WHERE persona_id = r.persona_id AND cost_usd IS NOT NULL
                   ORDER BY created_at DESC, id DESC LIMIT 10) x), 0), @default_reserve::float8)::float8 AS reserve_usd
FROM runs r
JOIN tasks t ON t.id = r.task_id
WHERE r.company_id = @company_id AND t.project_id = @project_id AND r.status IN ('queued', 'running')
GROUP BY r.persona_id;

-- name: ListRunningRunIDsForProject :many
SELECT r.id FROM runs r
JOIN tasks t ON t.id = r.task_id
WHERE r.company_id = @company_id AND t.project_id = @project_id AND r.status = 'running'
ORDER BY r.created_at, r.id;

-- name: WorkspaceUsageCounts :many
-- Quantas personas, projetos e tasks abertas usam cada workspace da empresa.
SELECT w.id,
       (SELECT count(*) FROM personas p WHERE p.workspace_id = w.id)::bigint AS personas,
       (SELECT count(*) FROM projects pr WHERE pr.workspace_id = w.id)::bigint AS projects,
       (SELECT count(*) FROM tasks t WHERE t.workspace_id = w.id AND t.status NOT IN ('done', 'cancelled'))::bigint AS open_tasks
FROM workspaces w
WHERE w.company_id = @company_id;
