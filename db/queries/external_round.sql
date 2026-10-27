-- Rodada, etapa, marca e aprovação externas (ADR-0080 e 0081; grupo 3 da v0.8, #124). Só leitura: a escrita vem com a
-- ingestão (fatia 3, #127), que monta cada linha na mesma transação do fato.

-- name: ListExternalRounds :many
-- Rodadas da empresa, a mais nova primeiro. A aberta (sem `closed_at`) vem junto das fechadas, com o ciclo.
SELECT * FROM external_round
WHERE company_id = @company_id
  AND (sqlc.narg(cycle_id)::uuid IS NULL OR cycle_id = sqlc.narg(cycle_id)::uuid)
ORDER BY opened_at DESC NULLS LAST, id
LIMIT @page_limit::int;

-- name: GetExternalRound :one
SELECT * FROM external_round WHERE company_id = @company_id AND id = @id;

-- name: ListRoundSteps :many
-- Etapas da rodada: só a revisão vigente de cada uma. O texto fica no fato (`fact_dedupe_key`), não aqui.
SELECT * FROM round_step
WHERE company_id = @company_id AND round_id = @round_id
ORDER BY kind, step_key;

-- name: ListStepActions :many
SELECT * FROM step_action WHERE company_id = @company_id AND step_id = @step_id ORDER BY action_id;

-- name: LatestActionMarks :many
-- Estado de cada ação da etapa: a marca mais nova vence (ADR-0080 item 4). `marked_unix_nano` decide, o `id` só desempata.
SELECT DISTINCT ON (action_id) action_id, state, marked_by_type, marked_by_id, marked_unix_nano, migrated
FROM action_mark
WHERE company_id = @company_id AND step_id = @step_id
ORDER BY action_id, marked_unix_nano DESC, id DESC;

-- name: ListExternalApprovals :many
-- Pedidos do canal da empresa, filtrados por estado (`pending` ou `decided`), o pendente mais antigo primeiro na Caixa.
SELECT * FROM external_approval
WHERE company_id = @company_id
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state)::text)
ORDER BY requested_at ASC NULLS LAST, id
LIMIT @page_limit::int;

-- name: GetExternalApproval :one
SELECT * FROM external_approval WHERE company_id = @company_id AND id = @id;

-- name: ListApprovalVersions :many
SELECT * FROM external_approval_version WHERE approval_id = @approval_id ORDER BY seen_at, sha256;

-- name: GetOutboxSnapshot :one
-- Última lista de pedidos da outbox de um host (ADR-0081 item 5).
SELECT * FROM external_outbox_snapshot WHERE company_id = @company_id AND host_id = @host_id;
