-- name: CreateCompany :one
INSERT INTO companies (id, slug, name, task_prefix, timezone, created_at, updated_at)
VALUES (@id, @slug, @name, @task_prefix, @timezone, @now, @now)
RETURNING *;

-- name: GetCompanyByID :one
SELECT * FROM companies WHERE id = @id;

-- name: GetCompanyBySlug :one
SELECT * FROM companies WHERE slug = @slug;

-- name: ListCompanies :many
SELECT * FROM companies ORDER BY created_at, id;

-- name: AllocateTaskNumber :one
-- Atomic per-company counter; the row lock serializes concurrent creators.
UPDATE companies
SET next_task_number = next_task_number + 1
WHERE id = @id
RETURNING (next_task_number - 1)::bigint AS number, task_prefix;

-- name: SetCompanyBudget :one
UPDATE companies SET budget_monthly_cents = @budget_monthly_cents, updated_at = @now
WHERE id = @id
RETURNING *;
