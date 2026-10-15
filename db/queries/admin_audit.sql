-- name: InsertAdminAudit :exec
INSERT INTO admin_audit (id, at, actor_id, actor_email, action, target_id, target_email, result)
VALUES (@id, @at, @actor_id, @actor_email, @action, @target_id, @target_email, @result);

-- name: ListAdminAudit :many
SELECT * FROM admin_audit ORDER BY at DESC, id DESC LIMIT @max_rows;
