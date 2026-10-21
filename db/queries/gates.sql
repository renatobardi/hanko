-- name: CreateGate :one
INSERT INTO gates (id, company_id, kind, status, title, body, task_id, run_id, persona_id,
                   requested_by_type, requested_by_id, event_id, requested_status,
                   review_reviewer, review_verdict, review_justification, review_artifact_sha256, review_run_id,
                   payload, audience, created_at)
VALUES (@id, @company_id, @kind, 'pending', @title, @body, @task_id, @run_id, @persona_id,
        @requested_by_type, @requested_by_id, @event_id, @requested_status,
        @review_reviewer, @review_verdict, @review_justification, @review_artifact_sha256, @review_run_id,
        @payload, @audience, @now)
RETURNING *;

-- name: GetGateByID :one
SELECT * FROM gates WHERE company_id = @company_id AND id = @id;

-- name: GetGateByIDForUpdate :one
SELECT * FROM gates WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetGateByEventID :one
SELECT * FROM gates WHERE company_id = @company_id AND event_id = @event_id;

-- name: GetGateView :one
SELECT sqlc.embed(g), COALESCE(c.task_prefix || '-' || t.number, '')::text AS task_key, COALESCE(t.title, '')::text AS task_title,
       COALESCE(p.name, '')::text AS persona_name
FROM gates g
JOIN companies c ON c.id = g.company_id
LEFT JOIN tasks t ON t.id = g.task_id
LEFT JOIN personas p ON p.id = g.persona_id
WHERE g.company_id = @company_id AND g.id = @id;

-- name: ListGateViews :many
-- Pendentes primeiro (mais antigas primeiro), depois as decididas (mais recentes primeiro). A página é escolhida
-- só na tabela gates (CTE "page") e só então recebe os joins; a ordenação é repetida porque o join não a preserva.
WITH page AS (
    SELECT g0.id FROM gates g0
    WHERE g0.company_id = @company_id
      AND (sqlc.narg(statuses)::text[] IS NULL OR g0.status = ANY(sqlc.narg(statuses)::text[]))
      AND (sqlc.narg(kinds)::text[] IS NULL OR g0.kind = ANY(sqlc.narg(kinds)::text[]))
      AND (sqlc.narg(task_id)::uuid IS NULL OR g0.task_id = sqlc.narg(task_id)::uuid)
      AND (sqlc.narg(persona_id)::uuid IS NULL OR g0.persona_id = sqlc.narg(persona_id)::uuid)
      -- "Minhas" (ADR-0042): gate que eu pedi ou decidi, ou numa task em que participo (task_participation)
      AND (sqlc.narg(mine_user)::uuid IS NULL
           OR (g0.requested_by_type = 'human' AND g0.requested_by_id = sqlc.narg(mine_user)::uuid)
           OR (g0.decided_by_type = 'human' AND g0.decided_by_id = sqlc.narg(mine_user)::uuid)
           OR g0.task_id IN (SELECT tp.task_id FROM task_participation(@company_id, sqlc.narg(mine_user)::uuid) tp))
    ORDER BY CASE WHEN g0.status = 'pending' THEN g0.created_at END ASC NULLS LAST,
             g0.decided_at DESC NULLS LAST, g0.created_at DESC, g0.id DESC
    LIMIT @lim OFFSET @off
)
SELECT sqlc.embed(g), COALESCE(c.task_prefix || '-' || t.number, '')::text AS task_key, COALESCE(t.title, '')::text AS task_title,
       COALESCE(p.name, '')::text AS persona_name
FROM page
JOIN gates g ON g.id = page.id
JOIN companies c ON c.id = g.company_id
LEFT JOIN tasks t ON t.id = g.task_id
LEFT JOIN personas p ON p.id = g.persona_id
ORDER BY CASE WHEN g.status = 'pending' THEN g.created_at END ASC NULLS LAST,
         g.decided_at DESC NULLS LAST, g.created_at DESC, g.id DESC;

-- name: DecideGate :one
UPDATE gates
SET status = @status, decided_by_type = @decided_by_type, decided_by_id = @decided_by_id,
    decision_note = @decision_note, decided_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: CountPendingGates :one
SELECT count(*)::bigint AS n FROM gates WHERE company_id = @company_id AND status = 'pending';

-- name: HasPendingGateForTask :one
SELECT EXISTS (SELECT 1 FROM gates WHERE task_id = @task_id AND status = 'pending') AS found;

-- name: HasPendingGateOfKind :one
SELECT EXISTS (SELECT 1 FROM gates WHERE task_id = @task_id AND kind = @kind AND status = 'pending') AS found;

-- name: CountBlockingGatesForPersona :one
-- Gates que seguram a persona em awaiting_gate (a revisão de task não segura).
SELECT count(*)::bigint AS n FROM gates
WHERE persona_id = @persona_id AND status = 'pending' AND kind IN ('approval', 'hire', 'question', 'plan');

-- name: HasGateDecidedSince :one
SELECT EXISTS (
    SELECT 1 FROM gates
    WHERE company_id = @company_id AND task_id = @task_id AND decided_at > @since
) AS found;

-- name: SetPersonaAwaitingGate :exec
-- A persona espera o gate, a menos que tenha run ativa em outra task.
UPDATE personas p SET status = 'awaiting_gate', updated_at = @now
WHERE p.id = @id AND p.status IN ('active', 'idle', 'running')
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.persona_id = p.id AND r.status IN ('queued', 'running') AND r.task_id IS DISTINCT FROM @task_id::uuid);

-- name: ReleasePersonaFromGate :exec
-- Fecha a espera: volta a running se ainda há run ativa, senão idle.
UPDATE personas p SET
    status = CASE WHEN EXISTS (SELECT 1 FROM runs r WHERE r.persona_id = p.id AND r.status IN ('queued', 'running'))
                  THEN 'running' ELSE 'idle' END,
    updated_at = @now
WHERE p.id = @id AND p.status = 'awaiting_gate';

-- name: ListFailedRuns :many
-- Execuções que falharam e ainda não foram superadas por outra execução da mesma task (ou, numa run de chat,
-- da mesma conversa). Uma run de chat não tem task: entra pelo LEFT JOIN, com o título da conversa.
SELECT sqlc.embed(r), COALESCE(c.task_prefix || '-' || t.number, '')::text AS task_key, COALESCE(t.title, '')::text AS task_title,
       p.name AS persona_name, COALESCE(cv.title, '')::text AS conversation_title
FROM runs r
LEFT JOIN tasks t ON t.id = r.task_id
LEFT JOIN conversations cv ON cv.id = r.conversation_id
JOIN personas p ON p.id = r.persona_id
JOIN companies c ON c.id = r.company_id
WHERE r.company_id = @company_id AND r.status IN ('failed', 'expired') AND r.created_at > @since AND r.decision = ''
  AND (t.id IS NULL OR t.status NOT IN ('done', 'cancelled'))
  AND (r.task_id IS NOT NULL OR r.conversation_id IS NOT NULL)
  AND NOT EXISTS (SELECT 1 FROM runs n WHERE n.created_at > r.created_at
                  AND (n.task_id = r.task_id OR n.conversation_id = r.conversation_id))
ORDER BY r.created_at DESC, r.id DESC
LIMIT @lim;

-- name: ListErrorPersonas :many
SELECT * FROM personas WHERE company_id = @company_id AND status = 'error' ORDER BY updated_at DESC, id;

-- name: ExpirePendingGatesForTask :many
-- Task encerrada: os gates que ainda esperavam decisão perdem o objeto e expiram.
UPDATE gates SET status = 'expired', decided_by_type = 'system', decided_at = @now,
                 decision_note = 'Task encerrada antes da decisão.'
WHERE company_id = @company_id AND task_id = @task_id AND status = 'pending'
RETURNING *;

-- name: AnswerGate :one
-- Pergunta respondida: vira approved com a resposta estruturada (já validada contra o formulário no service).
UPDATE gates
SET status = 'approved', answer = @answer, decided_by_type = @decided_by_type, decided_by_id = @decided_by_id,
    decision_note = @decision_note, decided_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'pending' AND kind = 'question'
RETURNING *;

-- name: DismissGate :one
UPDATE gates
SET status = 'dismissed', decided_by_type = @decided_by_type, decided_by_id = @decided_by_id,
    decision_note = @decision_note, decided_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'pending' AND kind = 'question'
RETURNING *;

-- name: ReopenGate :one
-- Reabre uma pergunta dispensada: volta a pending e esquece a decisão.
UPDATE gates
SET status = 'pending', decided_by_type = NULL, decided_by_id = NULL, decision_note = '', decided_at = NULL, answer = NULL
WHERE company_id = @company_id AND id = @id AND status = 'dismissed'
RETURNING *;

-- name: ResubmitGate :one
-- Reenvio no mesmo gate (só a partir de changes_requested): texto novo, revisão +1, volta a pending.
UPDATE gates
SET title = @title, body = @body, payload = @payload, revision = revision + 1, status = 'pending',
    decided_by_type = NULL, decided_by_id = NULL, decision_note = '', decided_at = NULL
WHERE company_id = @company_id AND id = @id AND status = 'changes_requested'
RETURNING *;

-- name: InsertGateRevision :one
INSERT INTO gate_revisions (id, company_id, gate_id, revision, title, body, payload, decision_note, sha256, created_at)
VALUES (@id, @company_id, @gate_id, @revision, @title, @body, @payload, @decision_note, @sha256, @now)
RETURNING *;

-- name: ListGateRevisions :many
SELECT * FROM gate_revisions WHERE company_id = @company_id AND gate_id = @gate_id ORDER BY revision DESC;

-- name: CountPendingHireGatesForPersona :one
SELECT count(*)::bigint AS n FROM gates WHERE persona_id = @persona_id AND kind = 'hire' AND status = 'pending';

-- name: LatestApprovedPlanGate :one
-- O plano aprovado vigente da task: o gate plan aprovado mais recente.
SELECT * FROM gates WHERE task_id = @task_id AND kind = 'plan' AND status = 'approved'
ORDER BY decided_at DESC, id DESC LIMIT 1;

-- name: HasPendingPlanGate :one
SELECT EXISTS (SELECT 1 FROM gates WHERE task_id = @task_id AND kind = 'plan' AND status = 'pending') AS found;

-- name: UpsertPlanPolicy :exec
INSERT INTO plan_policies (company_id, scope, target_id, require_plan, updated_by_type, updated_by_id, updated_at)
VALUES (@company_id, @scope, @target_id, @require_plan, @updated_by_type, @updated_by_id, @now)
ON CONFLICT (scope, target_id) DO UPDATE
SET require_plan = EXCLUDED.require_plan, updated_by_type = EXCLUDED.updated_by_type,
    updated_by_id = EXCLUDED.updated_by_id, updated_at = EXCLUDED.updated_at;

-- name: DeletePlanPolicy :exec
DELETE FROM plan_policies WHERE company_id = @company_id AND scope = @scope AND target_id = @target_id;

-- name: GetPlanPolicy :one
SELECT require_plan FROM plan_policies WHERE scope = @scope AND target_id = @target_id;

-- name: GetCompanyPolicies :one
SELECT * FROM company_policies WHERE company_id = @company_id;

-- name: UpsertCompanyPolicies :exec
INSERT INTO company_policies (company_id, require_hire_gate, updated_at) VALUES (@company_id, @require_hire_gate, @now)
ON CONFLICT (company_id) DO UPDATE SET require_hire_gate = EXCLUDED.require_hire_gate, updated_at = EXCLUDED.updated_at;

-- name: GetPrimaryPersona :one
SELECT persona_id FROM member_primary_persona WHERE company_id = @company_id AND user_id = @user_id;

-- name: UpsertPrimaryPersona :exec
INSERT INTO member_primary_persona (company_id, user_id, persona_id, updated_at) VALUES (@company_id, @user_id, @persona_id, @now)
ON CONFLICT (company_id, user_id) DO UPDATE SET persona_id = EXCLUDED.persona_id, updated_at = EXCLUDED.updated_at;

-- name: DeletePrimaryPersona :exec
DELETE FROM member_primary_persona WHERE company_id = @company_id AND user_id = @user_id;

-- name: SetPersonaSystem :one
UPDATE personas SET system = @system, updated_at = @now WHERE company_id = @company_id AND id = @id RETURNING *;
