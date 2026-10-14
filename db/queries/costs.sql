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
     WHEN 'project' THEN COALESCE(t.project_id::text, '')
     WHEN 'model' THEN r.model
     ELSE r.agent END)::text AS key,
  (CASE @group_by::text
     WHEN 'day' THEN to_char(r.created_at AT TIME ZONE @tz::text, 'YYYY-MM-DD')
     WHEN 'persona' THEN p.name
     WHEN 'project' THEN COALESCE(pr.name, '')
     WHEN 'model' THEN r.model
     ELSE r.agent END)::text AS label,
  COALESCE(SUM(r.cost_usd), 0)::float8 AS cost_usd,
  COALESCE(SUM(r.cost_usd) FILTER (WHERE r.cost_source = 'estimated'), 0)::float8 AS estimated_usd,
  count(*)::bigint AS runs,
  COALESCE(SUM(r.input_tokens), 0)::bigint AS input_tokens,
  COALESCE(SUM(r.output_tokens), 0)::bigint AS output_tokens,
  COALESCE(SUM(r.cache_read_tokens + r.cache_creation_tokens), 0)::bigint AS cache_tokens
FROM runs r
JOIN personas p ON p.id = r.persona_id
JOIN tasks t ON t.id = r.task_id
LEFT JOIN projects pr ON pr.id = t.project_id
WHERE r.company_id = @company_id AND r.created_at >= @from_ts AND r.created_at < @to_ts
GROUP BY 1, 2
ORDER BY 1;

-- name: InsertBudgetAlert :one
-- Devolve a linha só se o alerta é novo (ON CONFLICT DO NOTHING => sem linhas).
INSERT INTO budget_alerts (id, company_id, scope, target_id, month, level, created_at)
VALUES (@id, @company_id, @scope, @target_id, @month, @level, @now)
ON CONFLICT (target_id, month, level) DO NOTHING
RETURNING *;
