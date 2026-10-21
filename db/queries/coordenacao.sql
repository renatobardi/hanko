-- Coordenação de tasks (ADR-0040): dependências, árvore, pausa herdada e política de revisão por camada.

-- ---------- dependências ----------

-- name: AddTaskDependency :execrows
INSERT INTO task_dependencies (company_id, task_id, depends_on_id, created_by_type, created_by_id, created_at)
VALUES (@company_id, @task_id, @depends_on_id, @created_by_type, @created_by_id, @now)
ON CONFLICT (task_id, depends_on_id) DO NOTHING;

-- name: LockTaskDependencies :exec
-- Serializa a escrita do grafo de dependências da empresa (a mesma chave que o trigger usa).
SELECT pg_advisory_xact_lock(hashtextextended('task_dependencies:' || (@company_id::uuid)::text, 0));

-- name: RemoveTaskDependency :execrows
DELETE FROM task_dependencies WHERE company_id = @company_id AND task_id = @task_id AND depends_on_id = @depends_on_id;

-- name: CountTaskDependencies :one
SELECT count(*)::int FROM task_dependencies WHERE task_id = @task_id;

-- name: ListTaskDependencies :many
-- As tasks de que @task_id depende, com o estado delas (a ordem é a da criação da dependência).
SELECT t.id, t.number, c.task_prefix, t.title, t.status, t.persona_id, COALESCE(p.name, '')::text AS persona_name,
       d.created_at AS added_at
FROM task_dependencies d
JOIN tasks t ON t.id = d.depends_on_id
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
WHERE d.task_id = @task_id
ORDER BY d.created_at, t.number;

-- name: ListTaskDependents :many
-- As tasks que dependem de @task_id.
SELECT t.id, t.number, c.task_prefix, t.title, t.status, t.persona_id, COALESCE(p.name, '')::text AS persona_name,
       d.created_at AS added_at
FROM task_dependencies d
JOIN tasks t ON t.id = d.task_id
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
WHERE d.depends_on_id = @task_id
ORDER BY d.created_at, t.number;

-- name: ListReachableDependencyEdges :many
-- Todas as arestas alcançáveis a partir de @start seguindo "depende de". É a base da checagem de ciclo (a mesma
-- regra pura de domain.WouldCycle) e da mensagem com a cadeia.
WITH RECURSIVE reach(id) AS (
    SELECT @start::uuid
    UNION
    SELECT d.depends_on_id FROM task_dependencies d JOIN reach r ON d.task_id = r.id
)
SELECT d.task_id, d.depends_on_id FROM task_dependencies d WHERE d.task_id IN (SELECT id FROM reach);

-- name: ListOpenDependentIDs :many
-- Tasks abertas que dependem de @depends_on_id, em ordem de id (a ordem de lock que evita deadlock entre fechamentos).
SELECT t.id FROM task_dependencies d JOIN tasks t ON t.id = d.task_id
WHERE d.depends_on_id = @depends_on_id AND t.company_id = @company_id AND t.status NOT IN ('done', 'cancelled')
ORDER BY t.id;

-- name: ListDependentsToRelease :many
-- Tasks abertas que dependem de @depends_on_id e já não têm nenhuma dependência aberta (liberadas pelo fechamento
-- dela ou pela remoção da última dependência aberta). cancelled_deps diz quantas dependências foram canceladas.
SELECT t.id, t.persona_id, t.status,
       (SELECT count(*) FROM task_dependencies x JOIN tasks xt ON xt.id = x.depends_on_id
         WHERE x.task_id = t.id AND xt.status = 'cancelled')::int AS cancelled_deps
FROM task_dependencies d
JOIN tasks t ON t.id = d.task_id
WHERE d.depends_on_id = @depends_on_id
  AND t.company_id = @company_id
  AND t.status NOT IN ('done', 'cancelled')
  AND NOT EXISTS (SELECT 1 FROM task_dependencies o JOIN tasks ot ON ot.id = o.depends_on_id
                  WHERE o.task_id = t.id AND ot.status NOT IN ('done', 'cancelled'))
ORDER BY t.number;

-- name: ListTaskCoordination :many
-- Contadores derivados de várias tasks de uma vez: dependências abertas e filhos.
SELECT t.id AS task_id,
       (SELECT count(*) FROM task_dependencies d JOIN tasks dt ON dt.id = d.depends_on_id
         WHERE d.task_id = t.id AND dt.status NOT IN ('done', 'cancelled'))::int AS open_dependencies,
       (SELECT count(*) FROM task_dependencies d WHERE d.task_id = t.id)::int AS dependencies_total,
       (SELECT count(*) FROM tasks c WHERE c.parent_id = t.id)::int AS children_total,
       (SELECT count(*) FROM tasks c WHERE c.parent_id = t.id AND c.status NOT IN ('done', 'cancelled'))::int AS children_open
FROM tasks t
WHERE t.company_id = @company_id AND t.id = ANY(@ids::uuid[]);

-- name: TaskHasOpenDependencies :one
SELECT EXISTS (SELECT 1 FROM task_dependencies d JOIN tasks dt ON dt.id = d.depends_on_id
               WHERE d.task_id = @task_id AND dt.status NOT IN ('done', 'cancelled'))::boolean;

-- ---------- árvore ----------

-- name: ListTaskChildren :many
SELECT t.id, t.number, c.task_prefix, t.title, t.status, t.persona_id, COALESCE(p.name, '')::text AS persona_name
FROM tasks t
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
WHERE t.company_id = @company_id AND t.parent_id = @parent_id
ORDER BY t.number;

-- name: ListOpenChildren :many
SELECT t.id, t.number, c.task_prefix, t.title, t.status, t.persona_id, COALESCE(p.name, '')::text AS persona_name
FROM tasks t
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
WHERE t.company_id = @company_id AND t.parent_id = @parent_id AND t.status NOT IN ('done', 'cancelled')
ORDER BY t.number;

-- name: CountOpenChildren :one
SELECT count(*)::int FROM tasks WHERE parent_id = @parent_id AND status NOT IN ('done', 'cancelled');

-- name: ListTaskAncestors :many
-- Os ancestrais de @task_id, do mais próximo (profundidade 1) ao raiz.
WITH RECURSIVE up(id, parent_id, depth) AS (
    SELECT t.id, t.parent_id, 1 FROM tasks t WHERE t.id = (SELECT x.parent_id FROM tasks x WHERE x.id = @task_id AND x.company_id = @company_id)
    UNION ALL
    SELECT t.id, t.parent_id, up.depth + 1 FROM tasks t JOIN up ON t.id = up.parent_id WHERE up.depth < 64
)
SELECT t.id, t.number, c.task_prefix, t.title, t.status, t.persona_id, COALESCE(p.name, '')::text AS persona_name, up.depth::int AS depth
FROM up
JOIN tasks t ON t.id = up.id
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
ORDER BY up.depth DESC;

-- name: SetTaskParent :exec
UPDATE tasks SET parent_id = @parent_id, updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: ListTaskSubtree :many
-- @root_id e todos os descendentes (a raiz tem profundidade 0).
WITH RECURSIVE down(id, depth) AS (
    SELECT t.id, 0 FROM tasks t WHERE t.id = @root_id AND t.company_id = @company_id
    UNION ALL
    SELECT t.id, down.depth + 1 FROM tasks t JOIN down ON t.parent_id = down.id WHERE down.depth < 64
)
SELECT t.id, t.status, t.persona_id, down.depth::int AS depth FROM down JOIN tasks t ON t.id = down.id ORDER BY down.depth, t.number;

-- ---------- pausa ----------

-- name: LockTaskHoldsShared :exec
-- O checkout de run segura este lock compartilhado; pausar e retomar o seguram exclusivo, então uma run não abre
-- no meio de uma pausa em andamento. Sempre o primeiro lock da transação (antes de qualquer task).
SELECT pg_advisory_xact_lock_shared(hashtextextended('task_hold:' || (@company_id::uuid)::text, 0));

-- name: LockTaskHoldsExclusive :exec
SELECT pg_advisory_xact_lock(hashtextextended('task_hold:' || (@company_id::uuid)::text, 0));

-- name: UpsertTaskHold :exec
INSERT INTO task_holds (task_id, company_id, reason, held_by_type, held_by_id, held_at)
VALUES (@task_id, @company_id, @reason, @held_by_type, @held_by_id, @now)
ON CONFLICT (task_id) DO UPDATE SET reason = EXCLUDED.reason, held_by_type = EXCLUDED.held_by_type,
    held_by_id = EXCLUDED.held_by_id, held_at = EXCLUDED.held_at;

-- name: GetTaskHold :one
SELECT * FROM task_holds WHERE task_id = @task_id;

-- name: DeleteTaskHold :execrows
DELETE FROM task_holds WHERE company_id = @company_id AND task_id = @task_id;

-- name: ListEffectiveHolds :many
-- A pausa efetiva de cada task pedida: a da própria task ou, na falta, a do ancestral mais próximo que tem uma.
-- Task sem pausa efetiva não volta linha. origin_* é a raiz da pausa.
WITH RECURSIVE up(origin_id, cur_id, parent_id, depth) AS (
    SELECT t.id, t.id, t.parent_id, 0 FROM tasks t WHERE t.company_id = @company_id AND t.id = ANY(@ids::uuid[])
    UNION ALL
    SELECT up.origin_id, p.id, p.parent_id, up.depth + 1 FROM up JOIN tasks p ON p.id = up.parent_id WHERE up.depth < 64
)
SELECT DISTINCT ON (up.origin_id) up.origin_id AS task_id, h.task_id AS hold_task_id, h.reason, h.held_by_type, h.held_by_id,
       h.held_at, c.task_prefix, ht.number AS hold_number
FROM up
JOIN task_holds h ON h.task_id = up.cur_id
JOIN tasks ht ON ht.id = h.task_id
JOIN companies c ON c.id = ht.company_id
ORDER BY up.origin_id, up.depth;

-- name: ListHeldRoots :many
-- As raízes de pausa da empresa (as que um humano pausou), mais recentes primeiro.
SELECT sqlc.embed(t), h.reason, h.held_by_type, h.held_by_id, h.held_at, c.task_prefix,
       COALESCE(p.name, '')::text AS persona_name
FROM task_holds h
JOIN tasks t ON t.id = h.task_id
JOIN companies c ON c.id = t.company_id
LEFT JOIN personas p ON p.id = t.persona_id
WHERE h.company_id = @company_id AND t.status NOT IN ('done', 'cancelled')
ORDER BY h.held_at DESC
LIMIT @lim;

-- name: RecordHeldWake :exec
INSERT INTO held_wakes (task_id, company_id, persona_id, trigger, requested_at)
VALUES (@task_id, @company_id, @persona_id, @trigger, @now)
ON CONFLICT (task_id) DO UPDATE SET persona_id = EXCLUDED.persona_id, trigger = EXCLUDED.trigger,
    requested_at = EXCLUDED.requested_at;

-- name: DeleteHeldWake :exec
DELETE FROM held_wakes WHERE task_id = @task_id;

-- name: ListHeldWakesInSubtree :many
-- Os wakes recusados durante a pausa na subárvore de @root_id (a raiz inclusive).
WITH RECURSIVE down(id, depth) AS (
    SELECT t.id, 0 FROM tasks t WHERE t.id = @root_id AND t.company_id = @company_id
    UNION ALL
    SELECT t.id, down.depth + 1 FROM tasks t JOIN down ON t.parent_id = down.id WHERE down.depth < 64
)
SELECT w.task_id, w.persona_id, w.trigger, w.requested_at
FROM held_wakes w JOIN down ON down.id = w.task_id
ORDER BY w.requested_at;

-- name: ListQueuedRunsInSubtree :many
-- Runs ainda na fila (não começaram) de @root_id e dos descendentes: a pausa as cancela.
WITH RECURSIVE down(id, depth) AS (
    SELECT t.id, 0 FROM tasks t WHERE t.id = @root_id AND t.company_id = @company_id
    UNION ALL
    SELECT t.id, down.depth + 1 FROM tasks t JOIN down ON t.parent_id = down.id WHERE down.depth < 64
)
SELECT r.id FROM runs r JOIN down ON r.task_id = down.id WHERE r.status = 'queued' ORDER BY r.created_at;

-- ---------- política de revisão ----------

-- name: GetTaskReviewOverride :one
SELECT * FROM task_review_overrides WHERE task_id = @task_id;

-- name: UpsertTaskReviewOverride :one
INSERT INTO task_review_overrides (task_id, company_id, require_review, reviewer_mode, reviewer_persona_id, reviewer_agent,
                                   reviewer_model, updated_by_type, updated_by_id, updated_at)
VALUES (@task_id, @company_id, @require_review, @reviewer_mode, @reviewer_persona_id, @reviewer_agent,
        @reviewer_model, @updated_by_type, @updated_by_id, @now)
ON CONFLICT (task_id) DO UPDATE SET require_review = EXCLUDED.require_review, reviewer_mode = EXCLUDED.reviewer_mode,
    reviewer_persona_id = EXCLUDED.reviewer_persona_id, reviewer_agent = EXCLUDED.reviewer_agent,
    reviewer_model = EXCLUDED.reviewer_model, updated_by_type = EXCLUDED.updated_by_type,
    updated_by_id = EXCLUDED.updated_by_id, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteTaskReviewOverride :execrows
DELETE FROM task_review_overrides WHERE company_id = @company_id AND task_id = @task_id;

-- name: GetProjectReviewPolicy :one
SELECT * FROM project_review_policies WHERE project_id = @project_id;

-- name: UpsertProjectReviewPolicy :one
INSERT INTO project_review_policies (project_id, company_id, require_review, reviewer_mode, reviewer_persona_id,
                                     reviewer_agent, reviewer_model, updated_by_type, updated_by_id, updated_at)
VALUES (@project_id, @company_id, @require_review, @reviewer_mode, @reviewer_persona_id, @reviewer_agent,
        @reviewer_model, @updated_by_type, @updated_by_id, @now)
ON CONFLICT (project_id) DO UPDATE SET require_review = EXCLUDED.require_review, reviewer_mode = EXCLUDED.reviewer_mode,
    reviewer_persona_id = EXCLUDED.reviewer_persona_id, reviewer_agent = EXCLUDED.reviewer_agent,
    reviewer_model = EXCLUDED.reviewer_model, updated_by_type = EXCLUDED.updated_by_type,
    updated_by_id = EXCLUDED.updated_by_id, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteProjectReviewPolicy :execrows
DELETE FROM project_review_policies WHERE company_id = @company_id AND project_id = @project_id;

-- name: UpsertRunPolicyOrigin :exec
INSERT INTO run_policy_origins (run_id, company_id, review_required, review_required_source, reviewer_source, updated_at)
VALUES (@run_id, @company_id, @review_required, @review_required_source, @reviewer_source, @now)
ON CONFLICT (run_id) DO UPDATE SET review_required = EXCLUDED.review_required,
    review_required_source = EXCLUDED.review_required_source, reviewer_source = EXCLUDED.reviewer_source,
    updated_at = EXCLUDED.updated_at;

-- name: GetRunPolicyOrigin :one
SELECT * FROM run_policy_origins WHERE run_id = @run_id;
