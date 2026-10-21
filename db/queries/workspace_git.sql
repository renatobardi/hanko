-- Workspace de execução com Git (ADR-0054): espelho, worktrees por task, padrões de projeto e "Nova sessão".

-- ---------- espelho do workspace ----------

-- name: UpsertWorkspaceGit :one
INSERT INTO workspace_git (workspace_id, company_id, credential_ref, created_at, updated_at)
VALUES (@workspace_id, @company_id, @credential_ref, @now, @now)
ON CONFLICT (workspace_id) DO UPDATE SET credential_ref = EXCLUDED.credential_ref, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetWorkspaceGit :one
SELECT * FROM workspace_git WHERE company_id = @company_id AND workspace_id = @workspace_id;

-- name: ListWorkspaceGit :many
SELECT * FROM workspace_git WHERE company_id = @company_id ORDER BY created_at, workspace_id;

-- name: DeleteWorkspaceGit :execrows
DELETE FROM workspace_git WHERE company_id = @company_id AND workspace_id = @workspace_id;

-- name: ClaimMirrorSync :one
-- Marca o espelho como sincronizando; uma sincronização presa há mais de stale_before é retomada.
UPDATE workspace_git
SET mirror_state = 'syncing', syncing_since = @now, updated_at = @now
WHERE company_id = @company_id AND workspace_id = @workspace_id
  AND (mirror_state <> 'syncing' OR syncing_since IS NULL OR syncing_since < @stale_before)
RETURNING *;

-- name: FinishMirrorSync :one
UPDATE workspace_git
SET mirror_state = @mirror_state, base_branch = @base_branch, head_sha = sqlc.narg(head_sha), last_error = @last_error,
    last_fetch_at = COALESCE(sqlc.narg(last_fetch_at), last_fetch_at), syncing_since = NULL, updated_at = @now
WHERE company_id = @company_id AND workspace_id = @workspace_id
RETURNING *;

-- ---------- worktrees ----------

-- name: GetTaskWorktree :one
SELECT * FROM task_worktrees WHERE company_id = @company_id AND task_id = @task_id;

-- name: ClaimTaskWorktree :one
-- Reivindica o worktree da task para provisionar: cria a linha, ou volta a provisionar uma que falhou, divergiu ou foi
-- removida (a task foi retomada), ou retoma um provisionamento preso. Linha pronta ou em provisionamento recente: sem
-- retorno (quem chama lê a existente).
INSERT INTO task_worktrees (id, company_id, task_id, workspace_id, branch, base_branch, path, state, claimed_at, created_at, updated_at)
VALUES (@id, @company_id, @task_id, @workspace_id, @branch, @base_branch, @path, 'provisioning', @now, @now, @now)
ON CONFLICT (task_id) DO UPDATE
SET workspace_id = EXCLUDED.workspace_id, branch = EXCLUDED.branch, base_branch = EXCLUDED.base_branch, path = EXCLUDED.path,
    state = 'provisioning', error = '', removed_reason = '', removed_at = NULL, claimed_at = EXCLUDED.claimed_at,
    updated_at = EXCLUDED.updated_at
WHERE task_worktrees.state IN ('failed', 'diverged', 'removed')
   OR (task_worktrees.state = 'provisioning' AND task_worktrees.claimed_at < @stale_before)
RETURNING *;

-- name: FinishTaskWorktree :one
UPDATE task_worktrees
SET state = @state, base_sha = COALESCE(sqlc.narg(base_sha), base_sha), head_sha = COALESCE(sqlc.narg(head_sha), head_sha),
    path = @path, base_branch = @base_branch, error = @error, removed_reason = @removed_reason,
    removed_at = sqlc.narg(removed_at), updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: ListTaskWorktrees :many
-- Worktrees de um workspace (mais recentes primeiro), com a chave e o estado da task.
SELECT w.*, t.number AS task_number, c.task_prefix, t.title AS task_title, t.status AS task_status, t.project_id
FROM task_worktrees w
JOIN tasks t ON t.id = w.task_id
JOIN companies c ON c.id = w.company_id
WHERE w.company_id = @company_id AND w.workspace_id = @workspace_id
ORDER BY w.updated_at DESC, w.id
LIMIT @lim;

-- name: ListProjectTaskWorktrees :many
SELECT w.*, t.number AS task_number, c.task_prefix, t.title AS task_title, t.status AS task_status, t.project_id
FROM task_worktrees w
JOIN tasks t ON t.id = w.task_id
JOIN companies c ON c.id = w.company_id
WHERE w.company_id = @company_id AND t.project_id = @project_id
ORDER BY w.updated_at DESC, w.id
LIMIT @lim;

-- name: ListLiveWorktrees :many
-- Worktrees que ainda ocupam disco, com o que a faxina precisa: estado da task e identificação do espelho.
SELECT w.*, t.number AS task_number, c.task_prefix, t.status AS task_status, c.slug AS company_slug,
       ws.slug AS workspace_slug, COALESCE(g.base_branch, '')::text AS mirror_base, g.last_fetch_at AS mirror_fetched_at
FROM task_worktrees w
JOIN tasks t ON t.id = w.task_id
JOIN companies c ON c.id = w.company_id
JOIN workspaces ws ON ws.id = w.workspace_id
LEFT JOIN workspace_git g ON g.workspace_id = w.workspace_id
WHERE w.state <> 'removed'
  AND (w.state <> 'provisioning' OR w.claimed_at < @stale_before)
  -- nunca apaga a pasta debaixo de uma run em andamento (a task pode ter sido concluída com a persona ainda rodando)
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.task_id = w.task_id AND r.status IN ('queued', 'running'))
ORDER BY w.updated_at, w.id
LIMIT @lim;

-- name: ListWorkspaceWorktreesLive :many
SELECT * FROM task_worktrees WHERE company_id = @company_id AND workspace_id = @workspace_id AND state <> 'removed';

-- ---------- padrões do projeto ----------

-- name: GetProjectDefaults :one
SELECT * FROM project_defaults WHERE company_id = @company_id AND project_id = @project_id;

-- name: UpsertProjectDefaults :one
INSERT INTO project_defaults (project_id, company_id, instructions, goal_id, import_repo_skills, skills_path, updated_at)
VALUES (@project_id, @company_id, @instructions, sqlc.narg(goal_id), @import_repo_skills, @skills_path, @now)
ON CONFLICT (project_id) DO UPDATE
SET instructions = EXCLUDED.instructions, goal_id = EXCLUDED.goal_id, import_repo_skills = EXCLUDED.import_repo_skills,
    skills_path = EXCLUDED.skills_path, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: ListProjectsImportingSkills :many
-- Projetos cujo workspace é @workspace_id e que pedem o import das skills do repositório.
SELECT d.project_id, d.skills_path FROM project_defaults d
JOIN projects p ON p.id = d.project_id
WHERE d.company_id = @company_id AND p.workspace_id = @workspace_id AND d.import_repo_skills
ORDER BY d.project_id;

-- name: GetSkillSourceByURL :one
SELECT * FROM skill_sources WHERE company_id = @company_id AND url = @url AND tracking_ref = @tracking_ref;

-- ---------- nova sessão ----------

-- name: CreateTaskSessionReset :one
INSERT INTO task_session_resets (id, company_id, task_id, reason, requested_by_type, requested_by_id, created_at)
VALUES (@id, @company_id, @task_id, @reason, @actor_type, @actor_id, @now)
RETURNING *;

-- name: ConsumeTaskSessionResets :many
-- O run recém-aberto consome os pedidos pendentes da task.
UPDATE task_session_resets SET consumed_run_id = @run_id, consumed_at = @now
WHERE company_id = @company_id AND task_id = @task_id AND consumed_at IS NULL
RETURNING *;

-- name: LatestTaskSessionReset :one
SELECT * FROM task_session_resets WHERE company_id = @company_id AND task_id = @task_id ORDER BY created_at DESC, id LIMIT 1;

-- name: ResetOfRun :one
SELECT * FROM task_session_resets WHERE company_id = @company_id AND consumed_run_id = @run_id ORDER BY created_at DESC LIMIT 1;
