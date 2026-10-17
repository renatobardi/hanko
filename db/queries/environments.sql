-- Ambientes de execução (ADR-0037).

-- name: CreateEnvironment :one
INSERT INTO environments (id, company_id, slug, name, description, driver, status, config, secret_enc, is_default,
                          created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description, @driver, 'active', @config, @secret_enc, false,
        @actor_type, @actor_id, @now, @now)
RETURNING *;

-- name: GetEnvironmentByID :one
SELECT * FROM environments WHERE company_id = @company_id AND id = @id;

-- name: GetEnvironmentByIDForUpdate :one
SELECT * FROM environments WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetEnvironmentBySlug :one
SELECT * FROM environments WHERE company_id = @company_id AND slug = @slug;

-- name: ListEnvironments :many
SELECT * FROM environments WHERE company_id = @company_id ORDER BY status, name, id;

-- name: GetDefaultEnvironment :one
SELECT * FROM environments WHERE company_id = @company_id AND is_default AND status = 'active';

-- name: UpdateEnvironment :one
UPDATE environments
SET name = @name, description = @description, config = @config, secret_enc = @secret_enc, status = @status,
    is_default = CASE WHEN @status::text = 'active' THEN is_default ELSE false END, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: ClearDefaultEnvironment :exec
UPDATE environments SET is_default = false, updated_at = @now WHERE company_id = @company_id AND is_default;

-- name: SetDefaultEnvironment :one
UPDATE environments SET is_default = true, updated_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'active'
RETURNING *;

-- name: SetEnvironmentProbe :one
UPDATE environments SET last_probe_at = @now::timestamptz, last_probe_ok = @ok, last_probe_report = @report
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: ListAllSecretEnvironments :many
-- Para a rotação de chave: ambientes com segredo guardado.
SELECT id, company_id, secret_enc FROM environments WHERE secret_enc <> '' ORDER BY id;

-- name: SetEnvironmentSecretEnc :exec
UPDATE environments SET secret_enc = @secret_enc WHERE id = @id;

-- name: SetPersonaEnvironment :one
UPDATE personas SET environment_id = sqlc.narg(environment_id), updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: MovePersonasEnvironment :execrows
-- Arquivar um ambiente leva as personas dele para outro (ou para o padrão, com nulo).
UPDATE personas SET environment_id = sqlc.narg(to_id), updated_at = @now
WHERE company_id = @company_id AND environment_id = @from_id;

-- name: ListEnvironmentPersonas :many
SELECT p.id, p.slug, p.name, p.environment_id FROM personas p
WHERE p.company_id = @company_id AND p.environment_id IS NOT NULL
ORDER BY p.name, p.id;

-- name: CreateEnvironmentLease :one
INSERT INTO environment_leases (id, company_id, environment_id, run_id, status, remote_dir, acquired_at)
VALUES (@id, @company_id, @environment_id, @run_id, 'active', @remote_dir, @now)
ON CONFLICT (run_id) DO UPDATE SET status = 'active', remote_dir = EXCLUDED.remote_dir,
    environment_id = EXCLUDED.environment_id, acquired_at = EXCLUDED.acquired_at, released_at = NULL, failure_reason = ''
RETURNING *;

-- name: ReleaseEnvironmentLease :one
UPDATE environment_leases SET status = @status, released_at = @now::timestamptz, failure_reason = @failure_reason
WHERE company_id = @company_id AND run_id = @run_id
RETURNING *;

-- name: ListEnvironmentLeases :many
-- Usos de um ambiente, mais recentes primeiro, com o estado da run: um uso ativo cuja run já terminou é órfão
-- (o servidor caiu no meio).
SELECT l.*, r.status AS run_status, COALESCE(r.name, '')::text AS run_name, COALESCE(p.name, '')::text AS persona_name
FROM environment_leases l
JOIN runs r ON r.id = l.run_id
LEFT JOIN personas p ON p.id = r.persona_id
WHERE l.company_id = @company_id AND l.environment_id = @environment_id
ORDER BY l.acquired_at DESC, l.id DESC
LIMIT @lim;

-- name: CountActiveEnvironmentLeases :many
SELECT l.environment_id, count(*)::int AS active
FROM environment_leases l
JOIN runs r ON r.id = l.run_id
WHERE l.company_id = @company_id AND l.status = 'active' AND r.status IN ('queued','running')
GROUP BY l.environment_id;

-- name: GetRunEnvironmentLease :one
SELECT l.*, e.name AS environment_name, e.driver AS environment_driver, e.slug AS environment_slug
FROM environment_leases l
JOIN environments e ON e.id = l.environment_id
WHERE l.company_id = @company_id AND l.run_id = @run_id;

-- name: LockEnvironmentDefault :exec
-- Serializa a troca do ambiente padrão de uma empresa.
SELECT pg_advisory_xact_lock(hashtext('hanko:env-default:' || (@company_id::uuid)::text));
