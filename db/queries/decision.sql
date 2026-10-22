-- name: GetDecisionModel :one
SELECT * FROM decision_models WHERE company_id = @company_id;

-- name: UpsertDecisionModel :one
INSERT INTO decision_models (company_id, provider, base_url, api_key_ref, model, prompt_usd_token, completion_usd_token,
                             timeout_sec, enabled, created_at, updated_at)
VALUES (@company_id, @provider, @base_url, @api_key_ref, @model, @prompt_usd_token, @completion_usd_token,
        @timeout_sec, @enabled, @now, @now)
ON CONFLICT (company_id) DO UPDATE SET provider = EXCLUDED.provider, base_url = EXCLUDED.base_url,
    api_key_ref = EXCLUDED.api_key_ref, model = EXCLUDED.model, prompt_usd_token = EXCLUDED.prompt_usd_token,
    completion_usd_token = EXCLUDED.completion_usd_token, timeout_sec = EXCLUDED.timeout_sec,
    enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteDecisionModel :execrows
DELETE FROM decision_models WHERE company_id = @company_id;

-- name: ListDecisionFeatures :many
SELECT * FROM decision_features WHERE company_id = @company_id ORDER BY feature;

-- name: UpsertDecisionFeature :one
INSERT INTO decision_features (company_id, feature, enabled, model, updated_at)
VALUES (@company_id, @feature, @enabled, @model, @now)
ON CONFLICT (company_id, feature) DO UPDATE SET enabled = EXCLUDED.enabled, model = EXCLUDED.model, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: InsertDecisionInvocation :one
INSERT INTO decision_invocations (id, company_id, feature, task_id, model, status, error, input_tokens, output_tokens,
                                  cost_usd, cost_source, latency_ms, created_at)
VALUES (@id, @company_id, @feature, sqlc.narg(task_id), @model, @status, @error, @input_tokens, @output_tokens,
        @cost_usd, @cost_source, @latency_ms, @now)
RETURNING *;

-- name: DecisionCostBreakdown :many
-- Custo do decision model por feature e modelo no intervalo [from, to).
SELECT feature::text AS feature, model::text AS model,
       count(*)::bigint AS calls,
       count(*) FILTER (WHERE status = 'error')::bigint AS errors,
       COALESCE(SUM(cost_usd), 0)::float8 AS cost_usd,
       COALESCE(SUM(cost_usd) FILTER (WHERE cost_source = 'estimated'), 0)::float8 AS estimated_usd,
       COALESCE(SUM(input_tokens), 0)::bigint AS input_tokens,
       COALESCE(SUM(output_tokens), 0)::bigint AS output_tokens
FROM decision_invocations
WHERE company_id = @company_id AND created_at >= @from_ts AND created_at < @to_ts
GROUP BY feature, model
ORDER BY cost_usd DESC, feature;

-- name: ListDecisionInvocations :many
SELECT * FROM decision_invocations WHERE company_id = @company_id ORDER BY created_at DESC, id DESC LIMIT @lim;

-- name: DecisionSpendBetween :one
SELECT COALESCE(SUM(cost_usd), 0)::float8 AS spend FROM decision_invocations
WHERE company_id = @company_id AND created_at >= @from_ts AND created_at < @to_ts;
