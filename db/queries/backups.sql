-- name: InsertInstanceBackup :one
INSERT INTO instance_backups (id, kind, status, label, actor_type, actor_id, scheduled_for, started_at)
VALUES (@id, @kind, 'running', @label, @actor_type, sqlc.narg(actor_id), sqlc.narg(scheduled_for), @now)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: FinishInstanceBackup :one
UPDATE instance_backups SET status = @status, file_name = @file_name, size_bytes = @size_bytes, schema_version = @schema_version,
    blobs = @blobs, error = @error, finished_at = @now
WHERE id = @id
RETURNING *;

-- name: ListInstanceBackups :many
SELECT * FROM instance_backups ORDER BY started_at DESC, id DESC LIMIT @lim;

-- name: GetInstanceBackup :one
SELECT * FROM instance_backups WHERE id = @id;

-- Um backup 'running' há mais que o teto é de um processo que morreu: libera a vaga (índice de um por vez).
-- name: FailStaleInstanceBackups :execrows
UPDATE instance_backups SET status = 'failed', error = 'interrompido: o servidor parou durante o backup', finished_at = @now
WHERE status = 'running' AND started_at < @before;

-- name: HasRunningInstanceBackup :one
SELECT EXISTS (SELECT 1 FROM instance_backups WHERE status = 'running');

-- name: GetSystemFlow :one
SELECT * FROM system_flows WHERE key = @key;

-- name: GetSystemFlowForUpdate :one
SELECT * FROM system_flows WHERE key = @key FOR UPDATE;

-- name: UpsertSystemFlow :one
INSERT INTO system_flows (key, cron, timezone, enabled, next_fire_at, updated_by, updated_at)
VALUES (@key, @cron, @timezone, @enabled, sqlc.narg(next_fire_at), sqlc.narg(updated_by), @now)
ON CONFLICT (key) DO UPDATE SET cron = EXCLUDED.cron, timezone = EXCLUDED.timezone, enabled = EXCLUDED.enabled,
    next_fire_at = EXCLUDED.next_fire_at, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DueSystemFlows :many
SELECT * FROM system_flows WHERE enabled AND next_fire_at <= @now ORDER BY next_fire_at LIMIT 10;

-- name: MarkSystemFlowFired :exec
UPDATE system_flows SET last_fired_at = @last_fired_at, next_fire_at = @next_fire_at, updated_at = @now WHERE key = @key;
