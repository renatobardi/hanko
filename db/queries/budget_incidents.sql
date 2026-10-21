-- name: OpenBudgetIncident :one
-- Abre o incidente do (alvo, mês). Se já existia e foi resolvido (o teto estourou de novo), reabre; se já está aberto, não muda.
INSERT INTO budget_incidents (id, company_id, scope, target_id, target_name, month, status, budget_cents, spent_usd, opened_at)
VALUES (@id, @company_id, @scope, @target_id, @target_name, @month, 'open', @budget_cents, @spent_usd, @now)
ON CONFLICT (scope, target_id, month) DO UPDATE
SET status = 'open', resolution = NULL, note = '', resolved_by_type = NULL, resolved_by_id = NULL, resolved_at = NULL,
    budget_cents = EXCLUDED.budget_cents, spent_usd = EXCLUDED.spent_usd, opened_at = EXCLUDED.opened_at, release_runs = 0
WHERE budget_incidents.status = 'resolved'
RETURNING *;

-- name: GetBudgetIncident :one
SELECT * FROM budget_incidents WHERE company_id = @company_id AND id = @id;

-- name: GetBudgetIncidentForUpdate :one
SELECT * FROM budget_incidents WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: ListBudgetIncidents :many
SELECT * FROM budget_incidents
WHERE company_id = @company_id AND (NOT @only_open::boolean OR status = 'open')
ORDER BY (status = 'open') DESC, opened_at DESC, id
LIMIT @lim;

-- name: ResolveBudgetIncident :one
UPDATE budget_incidents
SET status = 'resolved', resolution = @resolution, note = @note, release_runs = @release_runs,
    resolved_by_type = @resolved_by_type, resolved_by_id = @resolved_by_id, resolved_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'open'
RETURNING *;

-- name: ListOpenBudgetIncidentsForTarget :many
SELECT * FROM budget_incidents WHERE scope = @scope AND target_id = @target_id AND status = 'open' FOR UPDATE;

-- name: ConsumeBudgetRelease :one
-- Consome um run avulso liberado para o (alvo, mês); sem linhas = não há crédito.
UPDATE budget_incidents SET release_runs = release_runs - 1
WHERE scope = @scope AND target_id = @target_id AND month = @month AND release_runs > 0
RETURNING id;

-- name: AutoResolveBudgetIncidentsForTarget :many
-- O teto do alvo subiu (ou saiu) e ele deixou de estourar: os incidentes abertos dele se resolvem sozinhos.
UPDATE budget_incidents
SET status = 'resolved', resolution = 'auto', note = @note, resolved_by_type = 'system', resolved_at = @now
WHERE scope = @scope AND target_id = @target_id AND status = 'open'
RETURNING *;

-- name: AutoResolveStaleBudgetIncidents :many
-- O mês virou: o gasto zerou, então o incidente do mês anterior não tem mais o que decidir.
UPDATE budget_incidents b
SET status = 'resolved', resolution = 'auto', note = 'o mês virou', resolved_by_type = 'system', resolved_at = @now
FROM companies c
WHERE c.id = b.company_id AND b.status = 'open'
  AND b.month < to_char(timezone(c.timezone, @now::timestamptz), 'YYYY-MM')
RETURNING b.*;
