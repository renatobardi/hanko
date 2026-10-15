-- name: GetModelPrice :one
-- Preço vigente no instante @at: o prefixo mais longo que casa, com a vigência mais recente.
SELECT * FROM model_prices
WHERE starts_with(@model::text, model) AND effective_at <= @at::timestamptz
ORDER BY length(model) DESC, effective_at DESC
LIMIT 1;

-- name: ListModelPrices :many
SELECT * FROM model_prices ORDER BY model, effective_at DESC;

-- name: SpendBetween :one
-- Gasto em USD da empresa (ou de uma persona) no intervalo [from, to).
SELECT COALESCE(SUM(cost_usd), 0)::float8 AS spend
FROM runs
WHERE company_id = @company_id
  AND (sqlc.narg(persona_id)::uuid IS NULL OR persona_id = sqlc.narg(persona_id)::uuid)
  AND created_at >= @from_ts AND created_at < @to_ts;

-- name: CostBreakdown :many
-- Custo agrupado por dimensão ('day' no fuso @tz, 'persona', 'project', 'model', 'harness').
SELECT
  (CASE @group_by::text
     WHEN 'day' THEN to_char(r.created_at AT TIME ZONE @tz::text, 'YYYY-MM-DD')
     WHEN 'persona' THEN r.persona_id::text
     WHEN 'project' THEN CASE WHEN r.conversation_id IS NOT NULL THEN 'chat' ELSE COALESCE(t.project_id::text, '') END
     WHEN 'model' THEN r.model
     ELSE r.agent END)::text AS key,
  (CASE @group_by::text
     WHEN 'day' THEN to_char(r.created_at AT TIME ZONE @tz::text, 'YYYY-MM-DD')
     WHEN 'persona' THEN p.name
     WHEN 'project' THEN CASE WHEN r.conversation_id IS NOT NULL THEN 'Chat' ELSE COALESCE(pr.name, '') END
     WHEN 'model' THEN r.model
     ELSE r.agent END)::text AS label,
  COALESCE(SUM(r.cost_usd), 0)::float8 AS cost_usd,
  COALESCE(SUM(r.cost_usd) FILTER (WHERE r.cost_source = 'estimated'), 0)::float8 AS estimated_usd,
  COALESCE(SUM(r.list_cost_usd), 0)::float8 AS list_usd,
  count(*)::bigint AS runs,
  COALESCE(SUM(r.input_tokens), 0)::bigint AS input_tokens,
  COALESCE(SUM(r.output_tokens), 0)::bigint AS output_tokens,
  COALESCE(SUM(r.cache_read_tokens + r.cache_creation_tokens), 0)::bigint AS cache_tokens
FROM runs r
JOIN personas p ON p.id = r.persona_id
LEFT JOIN tasks t ON t.id = r.task_id
LEFT JOIN projects pr ON pr.id = t.project_id
WHERE r.company_id = @company_id AND r.created_at >= @from_ts AND r.created_at < @to_ts
  AND (sqlc.narg(project_id)::uuid IS NULL OR t.project_id = sqlc.narg(project_id)::uuid)
GROUP BY 1, 2
ORDER BY 1;

-- name: InsertBudgetAlert :one
-- Devolve a linha só se o alerta é novo (ON CONFLICT DO NOTHING => sem linhas).
INSERT INTO budget_alerts (id, company_id, scope, target_id, month, level, created_at)
VALUES (@id, @company_id, @scope, @target_id, @month, @level, @now)
ON CONFLICT (target_id, month, level) DO NOTHING
RETURNING *;

-- name: LockBudget :exec
-- Serializa a checagem de orçamento da empresa: duas runs abrindo juntas não furam o teto.
SELECT pg_advisory_xact_lock(hashtext('hanko:budget:' || @company_id::text));

-- name: ActiveRunReserves :many
-- Reserva por persona com runs queued/running: o maior entre a média de custo das últimas 10
-- runs com custo e o piso @default_reserve, vezes o número de runs abertas.
SELECT r.persona_id, count(*)::bigint AS active,
       GREATEST(COALESCE((SELECT avg(x.cost_usd) FROM (
                   SELECT cost_usd FROM runs WHERE persona_id = r.persona_id AND cost_usd IS NOT NULL
                   ORDER BY created_at DESC, id DESC LIMIT 10) x), 0), @default_reserve::float8)::float8 AS reserve_usd
FROM runs r
WHERE r.company_id = @company_id AND r.status IN ('queued', 'running')
GROUP BY r.persona_id;

-- name: PersonaRunReserve :one
-- Reserva de uma run nova da persona (mesma regra de ActiveRunReserves).
SELECT GREATEST(COALESCE((SELECT avg(x.cost_usd) FROM (
                   SELECT cost_usd FROM runs WHERE persona_id = @persona_id AND cost_usd IS NOT NULL
                   ORDER BY created_at DESC, id DESC LIMIT 10) x), 0), @default_reserve::float8)::float8 AS reserve_usd;

-- name: ListRunningRunIDs :many
-- Runs em execução da persona (ou, sem persona, da empresa inteira).
SELECT id FROM runs
WHERE company_id = @company_id AND status = 'running'
  AND (sqlc.narg(persona_id)::uuid IS NULL OR persona_id = sqlc.narg(persona_id)::uuid)
ORDER BY created_at, id;

-- name: InsertCostNotice :one
-- Linha só na primeira vez do (empresa, modelo, mês).
INSERT INTO cost_notices (company_id, model, month, created_at)
VALUES (@company_id, @model, @month, @now)
ON CONFLICT (company_id, model, month) DO NOTHING
RETURNING *;

-- name: DeleteBudgetAlert :exec
DELETE FROM budget_alerts WHERE target_id = @target_id AND month = @month AND level = @level;
