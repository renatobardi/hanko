-- Sessão externa (ADR-0078 item 2; fatia 1 da v0.8, #125). A sessão é o `oute.task.id` que a fonte declara. Cada
-- atributo guarda a hora do fato que o trouxe: quem chega com hora mais antiga troca o valor, e a regra fica no UPDATE,
-- sob o lock da linha.

-- name: InsertExternalSession :one
-- Sem linha de retorno quando a sessão já existia (outra transação criou antes): quem chamou relê.
INSERT INTO external_session (id, company_id, task_id, created_at, updated_at)
VALUES (@id, @company_id, @task_id, @now, @now)
ON CONFLICT (company_id, task_id) DO NOTHING
RETURNING id;

-- name: ExternalSessionIDByTask :one
SELECT id FROM external_session WHERE company_id = @company_id AND task_id = @task_id;

-- name: UpdateExternalSessionFacts :exec
UPDATE external_session SET
  oute_repo    = CASE WHEN sqlc.narg(repo_at)::bigint IS NOT NULL AND (oute_repo_at IS NULL OR sqlc.narg(repo_at)::bigint < oute_repo_at)
                      THEN @oute_repo::text ELSE oute_repo END,
  oute_repo_at = CASE WHEN sqlc.narg(repo_at)::bigint IS NOT NULL AND (oute_repo_at IS NULL OR sqlc.narg(repo_at)::bigint < oute_repo_at)
                      THEN sqlc.narg(repo_at)::bigint ELSE oute_repo_at END,
  workspace_id = CASE WHEN sqlc.narg(repo_at)::bigint IS NOT NULL AND (oute_repo_at IS NULL OR sqlc.narg(repo_at)::bigint < oute_repo_at)
                      THEN sqlc.narg(workspace_id)::uuid ELSE workspace_id END,
  agent_id     = CASE WHEN sqlc.narg(agent_at)::bigint IS NOT NULL AND (agent_at IS NULL OR sqlc.narg(agent_at)::bigint < agent_at)
                      THEN sqlc.narg(agent_id)::uuid ELSE agent_id END,
  agent_at     = CASE WHEN sqlc.narg(agent_at)::bigint IS NOT NULL AND (agent_at IS NULL OR sqlc.narg(agent_at)::bigint < agent_at)
                      THEN sqlc.narg(agent_at)::bigint ELSE agent_at END,
  round        = CASE WHEN sqlc.narg(round_at)::bigint IS NOT NULL AND (round_at IS NULL OR sqlc.narg(round_at)::bigint < round_at)
                      THEN @round::text ELSE round END,
  round_at     = CASE WHEN sqlc.narg(round_at)::bigint IS NOT NULL AND (round_at IS NULL OR sqlc.narg(round_at)::bigint < round_at)
                      THEN sqlc.narg(round_at)::bigint ELSE round_at END,
  first_at     = LEAST(COALESCE(first_at, @first_at::bigint), @first_at::bigint),
  last_at      = GREATEST(COALESCE(last_at, @last_at::bigint), @last_at::bigint),
  updated_at   = @now
WHERE id = @id;
