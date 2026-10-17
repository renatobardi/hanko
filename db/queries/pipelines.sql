-- Pipelines, cases, fila de revisão, aprendizados e exemplos de decisão (ADR-0034).

-- ---------- pipelines ----------

-- name: CreatePipeline :one
INSERT INTO pipelines (id, company_id, project_id, key, name, description, enforce_transitions, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @project_id, @key, @name, @description, @enforce_transitions, @created_by_type, @created_by_id, @now, @now)
RETURNING *;

-- name: GetPipelineByID :one
SELECT * FROM pipelines WHERE company_id = @company_id AND id = @id;

-- name: GetPipelineByKey :one
SELECT * FROM pipelines WHERE company_id = @company_id AND key = @key;

-- name: GetPipelineForUpdate :one
SELECT * FROM pipelines WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: ListPipelines :many
SELECT * FROM pipelines WHERE company_id = @company_id
ORDER BY archived_at NULLS FIRST, name, id;

-- name: UpdatePipeline :one
UPDATE pipelines SET project_id = @project_id, key = @key, name = @name, description = @description,
    enforce_transitions = @enforce_transitions, archived_at = @archived_at, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeletePipeline :execrows
DELETE FROM pipelines WHERE company_id = @company_id AND id = @id;

-- name: PipelineCaseCounts :many
-- Por pipeline: cases abertos (não terminais) e, deles, os que estão numa etapa de revisão.
SELECT c.pipeline_id::uuid AS pipeline_id,
       count(*) FILTER (WHERE c.terminal_kind IS NULL)::bigint AS open_cases,
       count(*) FILTER (WHERE c.terminal_kind IS NULL AND s.kind = 'review')::bigint AS in_review,
       count(*)::bigint AS total
FROM cases c JOIN pipeline_stages s ON s.id = c.stage_id
WHERE c.company_id = @company_id AND c.pipeline_id IS NOT NULL
GROUP BY c.pipeline_id;

-- name: CountCasesInPipeline :one
SELECT count(*)::bigint FROM cases WHERE company_id = @company_id AND pipeline_id = @pipeline_id;

-- ---------- etapas ----------

-- name: CreateStage :one
INSERT INTO pipeline_stages (id, company_id, pipeline_id, key, name, position, kind, config, created_at, updated_at)
VALUES (@id, @company_id, @pipeline_id, @key, @name, @position, @kind, @config, @now, @now)
RETURNING *;

-- name: ListStages :many
SELECT * FROM pipeline_stages WHERE company_id = @company_id AND pipeline_id = @pipeline_id ORDER BY position, id;

-- name: ListCompanyStages :many
SELECT * FROM pipeline_stages WHERE company_id = @company_id ORDER BY pipeline_id, position, id;

-- name: GetStageByID :one
SELECT * FROM pipeline_stages WHERE company_id = @company_id AND id = @id;

-- name: UpdateStage :one
UPDATE pipeline_stages SET key = @key, name = @name, kind = @kind, config = @config, position = @position, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetStagePosition :exec
UPDATE pipeline_stages SET position = @position, updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: DeleteStage :execrows
DELETE FROM pipeline_stages WHERE company_id = @company_id AND id = @id;

-- name: CountCasesInStage :one
SELECT count(*)::bigint FROM cases WHERE company_id = @company_id AND stage_id = @stage_id;

-- ---------- transições ----------

-- name: ListTransitions :many
SELECT * FROM pipeline_transitions WHERE company_id = @company_id AND pipeline_id = @pipeline_id ORDER BY from_stage_id, to_stage_id;

-- name: DeleteTransitions :exec
DELETE FROM pipeline_transitions WHERE company_id = @company_id AND pipeline_id = @pipeline_id;

-- name: CreateTransition :exec
INSERT INTO pipeline_transitions (id, company_id, pipeline_id, from_stage_id, to_stage_id, label)
VALUES (@id, @company_id, @pipeline_id, @from_stage_id, @to_stage_id, @label);

-- name: HasTransition :one
SELECT EXISTS (SELECT 1 FROM pipeline_transitions
               WHERE pipeline_id = @pipeline_id AND from_stage_id = @from_stage_id AND to_stage_id = @to_stage_id)::boolean;

-- ---------- cases ----------

-- name: NextCaseNumber :one
INSERT INTO case_sequences (company_id, last_number) VALUES (@company_id, 1)
ON CONFLICT (company_id) DO UPDATE SET last_number = case_sequences.last_number + 1
RETURNING last_number;

-- name: CreateCase :one
INSERT INTO cases (id, company_id, number, case_type, key, title, summary, fields, status, pipeline_id, stage_id,
                   parent_case_id, project_id, version, terminal_kind, terminal_at, origin_run_id, created_by_type,
                   created_by_id, completed_at, created_at, updated_at)
VALUES (@id, @company_id, @number, @case_type, @key, @title, @summary, @fields, @status, @pipeline_id, @stage_id,
        @parent_case_id, @project_id, 1, @terminal_kind, @terminal_at, @origin_run_id, @created_by_type,
        @created_by_id, @completed_at, @now, @now)
RETURNING *;

-- name: GetCaseByID :one
SELECT * FROM cases WHERE company_id = @company_id AND id = @id;

-- name: GetCaseByIDForUpdate :one
SELECT * FROM cases WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetCaseByNumber :one
SELECT * FROM cases WHERE company_id = @company_id AND number = @number;

-- name: GetCaseByTypeKeyForUpdate :one
SELECT * FROM cases WHERE company_id = @company_id AND case_type = @case_type AND key = @key FOR UPDATE;

-- name: UpdateCase :one
-- Grava o estado inteiro do case (o service lê sob FOR UPDATE, muda e grava). Contadores de filhos ficam fora.
UPDATE cases SET title = @title, summary = @summary, fields = @fields, status = @status, pipeline_id = @pipeline_id,
    stage_id = @stage_id, parent_case_id = @parent_case_id, project_id = @project_id, version = @version,
    pending_suggestion = @pending_suggestion, approved_version = @approved_version,
    reapproval_required = @reapproval_required, attention = @attention, attention_at = @attention_at,
    terminal_kind = @terminal_kind, terminal_at = @terminal_at, completed_at = @completed_at, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: AdjustCaseChildCounts :one
UPDATE cases SET child_count = GREATEST(child_count + @d_children::int, 0),
    terminal_child_count = GREATEST(terminal_child_count + @d_terminal::int, 0)
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetCaseAttention :exec
UPDATE cases SET attention = @attention, attention_at = @now
WHERE company_id = @company_id AND id = @id AND terminal_kind IS NULL;

-- name: ListCaseViews :many
-- Lista de cases com filtros opcionais; roots_only = só os sem pai (a visão árvore busca os filhos à parte).
SELECT sqlc.embed(c), co.task_prefix,
       COALESCE(p.key, '')::text AS pipeline_key, COALESCE(p.name, '')::text AS pipeline_name,
       COALESCE(s.key, '')::text AS stage_key, COALESCE(s.name, '')::text AS stage_name, COALESCE(s.kind, '')::text AS stage_kind,
       COALESCE(pr.slug, '')::text AS project_slug, COALESCE(pr.name, '')::text AS project_name,
       COALESCE(pc.number, 0)::bigint AS parent_number, COALESCE(pc.title, '')::text AS parent_title
FROM cases c
JOIN companies co ON co.id = c.company_id
LEFT JOIN pipelines p ON p.id = c.pipeline_id
LEFT JOIN pipeline_stages s ON s.id = c.stage_id
LEFT JOIN projects pr ON pr.id = c.project_id
LEFT JOIN cases pc ON pc.id = c.parent_case_id
WHERE c.company_id = @company_id
  AND (sqlc.narg(case_type)::text IS NULL OR c.case_type = sqlc.narg(case_type)::text)
  AND (sqlc.narg(statuses)::text[] IS NULL OR c.status = ANY(sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(pipeline_id)::uuid IS NULL OR c.pipeline_id = sqlc.narg(pipeline_id)::uuid)
  AND (sqlc.narg(stage_id)::uuid IS NULL OR c.stage_id = sqlc.narg(stage_id)::uuid)
  AND (sqlc.narg(project_id)::uuid IS NULL OR c.project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(parent_id)::uuid IS NULL OR c.parent_case_id = sqlc.narg(parent_id)::uuid)
  AND (NOT @roots_only::boolean OR c.parent_case_id IS NULL)
  AND (NOT @loose_only::boolean OR c.pipeline_id IS NULL)
  AND (sqlc.narg(q)::text IS NULL OR c.title ILIKE '%' || sqlc.narg(q)::text || '%' ESCAPE '\'
       OR c.summary ILIKE '%' || sqlc.narg(q)::text || '%' ESCAPE '\'
       OR COALESCE(c.key, '') ILIKE '%' || sqlc.narg(q)::text || '%' ESCAPE '\')
ORDER BY c.updated_at DESC, c.id DESC
LIMIT @lim OFFSET @off;

-- name: GetCaseView :one
SELECT sqlc.embed(c), co.task_prefix,
       COALESCE(p.key, '')::text AS pipeline_key, COALESCE(p.name, '')::text AS pipeline_name,
       COALESCE(s.key, '')::text AS stage_key, COALESCE(s.name, '')::text AS stage_name, COALESCE(s.kind, '')::text AS stage_kind,
       COALESCE(pr.slug, '')::text AS project_slug, COALESCE(pr.name, '')::text AS project_name,
       COALESCE(pc.number, 0)::bigint AS parent_number, COALESCE(pc.title, '')::text AS parent_title
FROM cases c
JOIN companies co ON co.id = c.company_id
LEFT JOIN pipelines p ON p.id = c.pipeline_id
LEFT JOIN pipeline_stages s ON s.id = c.stage_id
LEFT JOIN projects pr ON pr.id = c.project_id
LEFT JOIN cases pc ON pc.id = c.parent_case_id
WHERE c.company_id = @company_id AND c.id = @id;

-- name: CountChildCases :one
SELECT count(*)::bigint FROM cases WHERE company_id = @company_id AND parent_case_id = @parent_id;

-- ---------- tasks ligadas ----------

-- name: LinkCaseTask :execrows
INSERT INTO case_task_links (case_id, task_id, company_id, role, created_by_run_id, created_at)
VALUES (@case_id, @task_id, @company_id, @role, @created_by_run_id, @now)
ON CONFLICT (case_id, task_id) DO NOTHING;

-- name: UnlinkCaseTask :execrows
DELETE FROM case_task_links WHERE company_id = @company_id AND case_id = @case_id AND task_id = @task_id;

-- name: ListCaseLinks :many
SELECT l.task_id, l.role, l.created_by_run_id, l.created_at, (co.task_prefix || '-' || t.number)::text AS task_key,
       t.title AS task_title, t.status AS task_status, COALESCE(p.name, '')::text AS persona_name
FROM case_task_links l
JOIN tasks t ON t.id = l.task_id
JOIN companies co ON co.id = l.company_id
LEFT JOIN personas p ON p.id = t.persona_id
WHERE l.company_id = @company_id AND l.case_id = @case_id
ORDER BY l.created_at, l.task_id;

-- name: ListCasesForTask :many
SELECT sqlc.embed(c), l.role, co.task_prefix, COALESCE(p.name, '')::text AS pipeline_name, COALESCE(s.name, '')::text AS stage_name
FROM case_task_links l
JOIN cases c ON c.id = l.case_id
JOIN companies co ON co.id = c.company_id
LEFT JOIN pipelines p ON p.id = c.pipeline_id
LEFT JOIN pipeline_stages s ON s.id = c.stage_id
WHERE l.company_id = @company_id AND l.task_id = @task_id
ORDER BY c.number;

-- ---------- bloqueios ----------

-- name: DeleteCaseBlockers :exec
DELETE FROM case_blockers WHERE company_id = @company_id AND case_id = @case_id;

-- name: AddCaseBlocker :exec
INSERT INTO case_blockers (case_id, blocker_case_id, company_id, created_at, resolved_at)
VALUES (@case_id, @blocker_case_id, @company_id, @now, @resolved_at);

-- name: ListCaseBlockers :many
SELECT b.blocker_case_id, b.resolved_at, b.created_at, b2.number, b2.title, b2.status, co.task_prefix
FROM case_blockers b
JOIN cases b2 ON b2.id = b.blocker_case_id
JOIN companies co ON co.id = b.company_id
WHERE b.company_id = @company_id AND b.case_id = @case_id
ORDER BY b2.number;

-- name: ListCasesBlockedBy :many
SELECT b.case_id, c.number, c.title, co.task_prefix
FROM case_blockers b
JOIN cases c ON c.id = b.case_id
JOIN companies co ON co.id = b.company_id
WHERE b.company_id = @company_id AND b.blocker_case_id = @blocker_case_id
ORDER BY c.number;

-- name: CountOpenBlockers :one
SELECT count(*)::bigint FROM case_blockers WHERE company_id = @company_id AND case_id = @case_id AND resolved_at IS NULL;

-- name: ResolveBlockersOf :many
-- O bloqueador chegou a done: resolve os bloqueios dele e devolve os cases liberados.
UPDATE case_blockers SET resolved_at = @now
WHERE company_id = @company_id AND blocker_case_id = @blocker_case_id AND resolved_at IS NULL
RETURNING case_id;

-- name: ListOpenBlockerIDs :many
SELECT blocker_case_id FROM case_blockers WHERE company_id = @company_id AND case_id = @case_id AND resolved_at IS NULL;

-- ---------- eventos ----------

-- name: CreateCaseEvent :one
INSERT INTO case_events (id, company_id, case_id, kind, from_stage_id, to_stage_id, actor_type, actor_id, run_id, payload, created_at)
VALUES (@id, @company_id, @case_id, @kind, @from_stage_id, @to_stage_id, @actor_type, @actor_id, @run_id, @payload, @now)
RETURNING *;

-- name: GetCaseEvent :one
SELECT * FROM case_events WHERE company_id = @company_id AND id = @id;

-- name: ListCaseEvents :many
-- Os mais recentes (até lim), do mais novo para o mais antigo.
SELECT * FROM case_events WHERE company_id = @company_id AND case_id = @case_id
ORDER BY created_at DESC, id DESC LIMIT @lim;

-- name: ListPipelineEvents :many
SELECT e.*, c.number AS case_number, c.title AS case_title
FROM case_events e JOIN cases c ON c.id = e.case_id
WHERE e.company_id = @company_id AND c.pipeline_id = @pipeline_id
ORDER BY e.created_at DESC, e.id DESC LIMIT @lim;

-- name: ListLearnings :many
-- Decisões de revisão e transições forçadas, com o case, o pipeline, a etapa de onde saiu e a de destino.
SELECT e.*, c.number AS case_number, c.title AS case_title, co.task_prefix,
       COALESCE(p.key, '')::text AS pipeline_key, COALESCE(p.name, '')::text AS pipeline_name,
       COALESCE(fs.key, '')::text AS from_key, COALESCE(fs.name, '')::text AS from_name,
       COALESCE(ts.key, '')::text AS to_key, COALESCE(ts.name, '')::text AS to_name,
       COALESCE(u.name, u.email, '')::text AS user_name, COALESCE(pe.name, '')::text AS persona_name
FROM case_events e
JOIN cases c ON c.id = e.case_id
JOIN companies co ON co.id = e.company_id
LEFT JOIN pipelines p ON p.id = c.pipeline_id
LEFT JOIN pipeline_stages fs ON fs.id = e.from_stage_id
LEFT JOIN pipeline_stages ts ON ts.id = e.to_stage_id
LEFT JOIN users u ON e.actor_type = 'human' AND u.id = e.actor_id
LEFT JOIN personas pe ON e.actor_type = 'persona' AND pe.id = e.actor_id
WHERE e.company_id = @company_id AND e.kind IN ('review_decided','transition_forced')
  AND (sqlc.narg(pipeline_id)::uuid IS NULL OR c.pipeline_id = sqlc.narg(pipeline_id)::uuid)
  AND (sqlc.narg(stage_id)::uuid IS NULL OR e.from_stage_id = sqlc.narg(stage_id)::uuid)
  AND (sqlc.narg(decision)::text IS NULL OR e.payload->>'decision' = sqlc.narg(decision)::text)
ORDER BY e.created_at DESC, e.id DESC
LIMIT @lim OFFSET @off;

-- name: StageLearnings :many
-- As últimas decisões com motivo de uma etapa (ou, sem etapa, do pipeline): entram no pacote de contexto da automação.
SELECT e.*, c.number AS case_number, c.title AS case_title, COALESCE(ts.name, '')::text AS to_name
FROM case_events e
JOIN cases c ON c.id = e.case_id
LEFT JOIN pipeline_stages ts ON ts.id = e.to_stage_id
WHERE e.company_id = @company_id AND c.pipeline_id = @pipeline_id
  AND e.kind IN ('review_decided','transition_forced')
  AND COALESCE(e.payload->>'reason', '') <> ''
  AND (e.from_stage_id = @stage_id OR e.to_stage_id = @stage_id)
ORDER BY e.created_at DESC, e.id DESC
LIMIT @lim;

-- name: PipelineLearnings :many
SELECT e.*, c.number AS case_number, c.title AS case_title, COALESCE(ts.name, '')::text AS to_name
FROM case_events e
JOIN cases c ON c.id = e.case_id
LEFT JOIN pipeline_stages ts ON ts.id = e.to_stage_id
WHERE e.company_id = @company_id AND c.pipeline_id = @pipeline_id
  AND e.kind IN ('review_decided','transition_forced')
  AND COALESCE(e.payload->>'reason', '') <> ''
ORDER BY e.created_at DESC, e.id DESC
LIMIT @lim;

-- ---------- fila de revisão ----------

-- name: ListReviewQueue :many
-- Cases não terminais numa etapa de revisão, com sugestão pendente ou marcados para atenção.
SELECT sqlc.embed(c), co.task_prefix,
       COALESCE(p.key, '')::text AS pipeline_key, COALESCE(p.name, '')::text AS pipeline_name,
       COALESCE(s.key, '')::text AS stage_key, COALESCE(s.name, '')::text AS stage_name, COALESCE(s.kind, '')::text AS stage_kind,
       s.config AS stage_config,
       (SELECT max(e.created_at) FROM case_events e
         WHERE e.case_id = c.id AND e.kind IN ('transitioned','transition_forced','review_decided','created'))::timestamptz AS entered_at
FROM cases c
JOIN companies co ON co.id = c.company_id
LEFT JOIN pipelines p ON p.id = c.pipeline_id
LEFT JOIN pipeline_stages s ON s.id = c.stage_id
WHERE c.company_id = @company_id AND c.terminal_kind IS NULL
  AND (p.archived_at IS NULL OR c.pipeline_id IS NULL)
  AND (s.kind = 'review' OR c.pending_suggestion IS NOT NULL OR c.attention IS NOT NULL)
ORDER BY c.updated_at, c.id
LIMIT 500;

-- name: CountReviewQueue :one
SELECT count(*)::bigint FROM cases c
LEFT JOIN pipelines p ON p.id = c.pipeline_id
LEFT JOIN pipeline_stages s ON s.id = c.stage_id
WHERE c.company_id = @company_id AND c.terminal_kind IS NULL
  AND (p.archived_at IS NULL OR c.pipeline_id IS NULL)
  AND (s.kind = 'review' OR c.pending_suggestion IS NOT NULL OR c.attention IS NOT NULL);

-- ---------- exemplos de decisão ----------

-- name: CreateDecisionExample :one
INSERT INTO decision_examples (id, company_id, source_kind, source_id, task_id, case_id, cutoff_at, decision_outcome, notes,
                               notes_history, snapshot, created_by_user_id, created_at, updated_at)
VALUES (@id, @company_id, @source_kind, @source_id, @task_id, @case_id, @cutoff_at, @decision_outcome, @notes,
        '[]'::jsonb, @snapshot, @created_by_user_id, @now, @now)
RETURNING *;

-- name: GetDecisionExample :one
SELECT * FROM decision_examples WHERE company_id = @company_id AND id = @id;

-- name: GetDecisionExampleForUpdate :one
SELECT * FROM decision_examples WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: ListDecisionExamples :many
SELECT * FROM decision_examples
WHERE company_id = @company_id
  AND (sqlc.narg(source_kind)::text IS NULL OR source_kind = sqlc.narg(source_kind)::text)
  AND (sqlc.narg(source_id)::uuid IS NULL OR source_id = sqlc.narg(source_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @lim OFFSET @off;

-- name: UpdateDecisionExampleNotes :one
UPDATE decision_examples SET notes = @notes, notes_history = @notes_history, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteDecisionExample :execrows
DELETE FROM decision_examples WHERE company_id = @company_id AND id = @id;

-- name: ListCommentsUntil :many
-- Comentários da task até o corte (os mais antigos primeiro), para o snapshot de um exemplo de decisão.
SELECT * FROM task_comments WHERE company_id = @company_id AND task_id = @task_id AND created_at <= @cutoff
ORDER BY created_at, id LIMIT @lim;

-- name: ListRunsUntil :many
SELECT * FROM runs WHERE company_id = @company_id AND task_id = @task_id AND created_at <= @cutoff
ORDER BY created_at, id LIMIT @lim;

-- name: ListCaseEventsUntil :many
SELECT * FROM case_events WHERE company_id = @company_id AND case_id = @case_id AND created_at <= @cutoff
ORDER BY created_at, id LIMIT @lim;
