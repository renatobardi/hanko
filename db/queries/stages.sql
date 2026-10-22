-- Pipelines sobre tasks (ADR-0048): etapas por projeto, posição da task e histórico.

-- name: ListProjectStages :many
SELECT sqlc.embed(s), COALESCE(p.slug, '')::text AS persona_slug, COALESCE(p.name, '')::text AS persona_name
FROM project_stages s
LEFT JOIN personas p ON p.id = s.persona_id
WHERE s.company_id = @company_id AND s.project_id = @project_id
ORDER BY s.position;

-- name: GetProjectStageByID :one
SELECT sqlc.embed(s), COALESCE(p.slug, '')::text AS persona_slug, COALESCE(p.name, '')::text AS persona_name
FROM project_stages s
LEFT JOIN personas p ON p.id = s.persona_id
WHERE s.company_id = @company_id AND s.id = @id;

-- name: InsertProjectStage :one
INSERT INTO project_stages (id, company_id, project_id, key, name, position, status, persona_id, exit_gate, instructions, created_at, updated_at)
VALUES (@id, @company_id, @project_id, @key, @name, @position, @status, @persona_id, @exit_gate, @instructions, @now, @now)
RETURNING *;

-- name: UpdateProjectStage :exec
UPDATE project_stages
SET name = @name, position = @position, status = @status, persona_id = @persona_id, exit_gate = @exit_gate,
    instructions = @instructions, updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: DeleteProjectStage :exec
DELETE FROM project_stages WHERE company_id = @company_id AND id = @id;

-- name: LockProjectStages :exec
-- Serializa a troca da lista de etapas de um projeto (e o enrolamento de tasks nela).
SELECT pg_advisory_xact_lock(hashtextextended('project_stages:' || (@project_id::uuid)::text, 0));

-- name: CountOpenTasksInStage :one
SELECT count(*)::bigint FROM task_stages WHERE stage_id = @stage_id AND state <> 'finished';

-- name: ProjectStageCounts :many
SELECT stage_id, count(*)::bigint AS tasks
FROM task_stages
WHERE company_id = @company_id AND project_id = @project_id AND state <> 'finished'
GROUP BY stage_id;

-- name: GetTaskStage :one
SELECT * FROM task_stages WHERE company_id = @company_id AND task_id = @task_id;

-- name: GetTaskStageForUpdate :one
SELECT * FROM task_stages WHERE company_id = @company_id AND task_id = @task_id FOR UPDATE;

-- name: UpsertTaskStage :one
INSERT INTO task_stages (task_id, company_id, project_id, stage_id, state, entered_at, updated_at)
VALUES (@task_id, @company_id, @project_id, @stage_id, @state, @now, @now)
ON CONFLICT (task_id) DO UPDATE
SET project_id = EXCLUDED.project_id, stage_id = EXCLUDED.stage_id, state = EXCLUDED.state,
    entered_at = EXCLUDED.entered_at, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: SetTaskStageState :exec
UPDATE task_stages SET state = @state, updated_at = @now WHERE task_id = @task_id;

-- name: ListTaskStagesForTasks :many
SELECT ts.task_id, ts.state, s.key AS stage_key, s.name AS stage_name, s.position AS stage_position
FROM task_stages ts
JOIN project_stages s ON s.id = ts.stage_id
WHERE ts.company_id = @company_id AND ts.task_id = ANY(@task_ids::uuid[]);

-- name: InsertTaskStageEvent :exec
INSERT INTO task_stage_events (id, company_id, project_id, task_id, kind, stage_key, to_stage_key, actor_type, actor_id,
                               gate_id, decision, note, created_at)
VALUES (@id, @company_id, @project_id, @task_id, @kind, @stage_key, @to_stage_key, @actor_type, @actor_id,
        @gate_id, @decision, @note, @now);

-- name: ListTaskStageEvents :many
SELECT * FROM task_stage_events WHERE company_id = @company_id AND task_id = @task_id ORDER BY created_at, id;

-- name: ListStageLearnings :many
-- Decisões de gate da etapa (mais novas primeiro): as que têm nota ou que não aprovaram sem comentário.
SELECT e.stage_key, e.decision, e.note, e.gate_id, e.created_at, t.number AS task_number
FROM task_stage_events e
JOIN tasks t ON t.id = e.task_id
WHERE e.company_id = @company_id AND e.project_id = @project_id AND e.kind = 'gate_decided'
  AND (@stage_key::text = '' OR e.stage_key = @stage_key::text)
  AND (e.note <> '' OR e.decision <> 'approved')
ORDER BY e.created_at DESC, e.id DESC
LIMIT @lim;

-- name: ListProjectTaskStages :many
-- As tasks de um projeto no pipeline, com a etapa (para o quadro).
SELECT ts.task_id, ts.state, ts.stage_id, t.number AS task_number, t.title AS task_title, t.status AS task_status
FROM task_stages ts
JOIN tasks t ON t.id = ts.task_id
WHERE ts.company_id = @company_id AND ts.project_id = @project_id AND ts.state <> 'finished'
ORDER BY ts.entered_at;
