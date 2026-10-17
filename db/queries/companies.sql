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

-- name: GetCompanyByIDForUpdate :one
SELECT * FROM companies WHERE id = @id FOR UPDATE;

-- name: IsCompanyArchived :one
-- Empresa inexistente conta como não arquivada (o erro vem da escrita que a referencia).
SELECT COALESCE((SELECT archived_at IS NOT NULL FROM companies WHERE id = @id), false)::boolean;

-- name: UpdateCompanyProfile :one
UPDATE companies SET name = @name, timezone = @timezone, description = @description, task_prefix = @task_prefix, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: CountCompanyTasks :one
SELECT count(*) FROM tasks WHERE company_id = @company_id;

-- name: SetCompanyArchived :one
UPDATE companies SET archived_at = @now, archived_by = @archived_by, archive_snapshot = @snapshot, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: ClearCompanyArchived :one
UPDATE companies SET archived_at = NULL, archived_by = NULL, archive_snapshot = '{}'::jsonb, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: ArchivePausePersonas :many
-- Arquivar pausa quem pode trabalhar, SEM pause_reason: o retomar-por-orçamento não as reativa.
UPDATE personas SET status = 'paused', updated_at = @now
WHERE company_id = @company_id AND status IN ('active', 'idle', 'running', 'awaiting_gate', 'error')
RETURNING id;

-- name: ArchiveDisableFlows :many
UPDATE flows SET enabled = false, updated_at = @now
WHERE company_id = @company_id AND enabled
RETURNING id;

-- name: ListActiveRunIDs :many
SELECT id FROM runs WHERE company_id = @company_id AND status IN ('queued', 'running') ORDER BY created_at, id;

-- name: RestoreResumePersonas :many
-- Só reativa quem ainda está como o arquivamento deixou (pausada, sem motivo de orçamento).
UPDATE personas SET status = 'idle', updated_at = @now
WHERE company_id = @company_id AND id = ANY(@ids::uuid[]) AND status = 'paused' AND pause_reason IS NULL
RETURNING id;

-- name: InsertCompanyDeletion :one
INSERT INTO company_deletions (id, company_id, slug, name, deleted_by, deleted_at, export_gz, export_bytes, sha256)
VALUES (@id, @company_id, @slug, @name, @deleted_by, @now, @export_gz, @export_bytes, @sha256)
RETURNING id, company_id, slug, name, deleted_by, deleted_at, export_bytes, sha256;

-- name: ListCompanyDeletions :many
SELECT id, company_id, slug, name, deleted_by, deleted_at, export_bytes, sha256
FROM company_deletions ORDER BY deleted_at DESC, id DESC LIMIT @lim;

-- name: GetCompanyDeletion :one
SELECT * FROM company_deletions WHERE id = @id;

-- name: DeleteCompany :execrows
DELETE FROM companies WHERE id = @id;

-- name: ListCompanyBlobArtifacts :many
-- Artefatos com conteúdo fora do Postgres, para limpar os blobs depois de apagar a empresa.
SELECT a.id, a.storage FROM artifacts a WHERE a.company_id = @company_id AND a.storage <> 'postgres';
