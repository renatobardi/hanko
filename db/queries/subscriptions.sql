-- name: CreateSubscription :one
INSERT INTO subscriptions (id, company_id, name, agent, monthly_usd, model_prefixes, weekly_limit_usd,
                           weekly_guard_pct, active, created_at, updated_at)
VALUES (@id, @company_id, @name, @agent, @monthly_usd, @model_prefixes, @weekly_limit_usd,
        @weekly_guard_pct, @active, @now, @now)
RETURNING *;

-- name: GetSubscriptionByID :one
SELECT * FROM subscriptions WHERE company_id = @company_id AND id = @id;

-- name: GetSubscriptionByName :one
SELECT * FROM subscriptions WHERE company_id = @company_id AND name = @name;

-- name: GetSubscriptionByIDForUpdate :one
SELECT * FROM subscriptions WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: ListSubscriptions :many
SELECT * FROM subscriptions WHERE company_id = @company_id ORDER BY created_at, id;

-- name: UpdateSubscription :one
-- ended_at marca quando a assinatura foi desativada (o rateio do custo pago para nesse mês); reativar limpa.
UPDATE subscriptions
SET name = @name, agent = @agent, monthly_usd = @monthly_usd, model_prefixes = @model_prefixes,
    weekly_limit_usd = @weekly_limit_usd, weekly_guard_pct = @weekly_guard_pct, active = @active,
    ended_at = @ended_at, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteSubscription :exec
DELETE FROM subscriptions WHERE company_id = @company_id AND id = @id;

-- name: SubscriptionHasRuns :one
SELECT EXISTS (SELECT 1 FROM runs WHERE subscription_id = @subscription_id) AS has_runs;

-- name: SubscriptionWeekUsage :one
-- Uso estimado da semana [from, to): custo de lista das runs cobertas pela assinatura (sem lista, o custo gravado).
SELECT COALESCE(SUM(COALESCE(list_cost_usd, cost_usd)), 0)::float8 AS usage_usd
FROM runs
WHERE subscription_id = @subscription_id AND created_at >= @from_ts AND created_at < @to_ts;

-- name: InsertSubscriptionGuardAlert :one
-- Devolve a linha só se o aviso da semana é novo (ON CONFLICT DO NOTHING => sem linhas).
INSERT INTO subscription_guard_alerts (subscription_id, week, created_at)
VALUES (@subscription_id, @week, @now)
ON CONFLICT (subscription_id, week) DO NOTHING
RETURNING *;

-- name: SubscriptionRunsInPeriod :many
-- Runs cobertas por alguma assinatura no período, com o peso do rateio do custo pago
-- (custo de lista; sem lista, o custo gravado) e as chaves de agrupamento da tela de custos.
SELECT r.id, r.subscription_id,
       to_char(r.created_at AT TIME ZONE @tz::text, 'YYYY-MM-DD')::text AS day,
       r.persona_id::text AS persona_key,
       CASE WHEN r.conversation_id IS NOT NULL THEN 'chat' ELSE COALESCE(t.project_id::text, '') END::text AS project_key,
       r.model AS model_key,
       r.agent AS harness_key,
       COALESCE(r.list_cost_usd, r.cost_usd, 0)::float8 AS weight
FROM runs r
LEFT JOIN tasks t ON t.id = r.task_id
WHERE r.company_id = @company_id AND r.subscription_id IS NOT NULL
  AND r.created_at >= @from_ts AND r.created_at < @to_ts
ORDER BY r.created_at, r.id;

-- name: OpenSubscriptionPeriod :exec
INSERT INTO subscription_periods (subscription_id, started_at) VALUES (@subscription_id, @now);

-- name: CloseSubscriptionPeriod :exec
UPDATE subscription_periods SET ended_at = GREATEST(@now::timestamptz, started_at)
WHERE subscription_id = @subscription_id AND ended_at IS NULL;

-- name: ListSubscriptionPeriods :many
-- Períodos de vigência das assinaturas da empresa (rateio do custo pago: só os meses cobertos por algum período).
SELECT sp.* FROM subscription_periods sp
JOIN subscriptions s ON s.id = sp.subscription_id
WHERE s.company_id = @company_id
ORDER BY sp.subscription_id, sp.started_at;
