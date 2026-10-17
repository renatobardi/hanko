-- name: ListInstanceSettings :many
SELECT key, value, updated_by, updated_at FROM instance_settings ORDER BY key;

-- name: UpsertInstanceSetting :exec
INSERT INTO instance_settings (key, value, updated_by, updated_at) VALUES (@key, @value, sqlc.narg(updated_by), @now)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at;

-- name: GetInstanceSettingForUpdate :one
SELECT key, value, updated_by, updated_at FROM instance_settings WHERE key = @key FOR UPDATE;

-- name: DeleteInstanceSetting :execrows
DELETE FROM instance_settings WHERE key = @key;

-- name: CreateInstanceSettingAudit :exec
INSERT INTO instance_setting_audit (id, key, old_value, new_value, actor_type, actor_id, occurred_at)
VALUES (@id, @key, sqlc.narg(old_value), sqlc.narg(new_value), @actor_type, @actor_id, @now);

-- name: ListInstanceSettingAudit :many
SELECT * FROM instance_setting_audit ORDER BY occurred_at DESC, id DESC LIMIT @lim;
