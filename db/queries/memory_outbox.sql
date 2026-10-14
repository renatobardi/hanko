-- name: InsertMemoryOutbox :one
INSERT INTO memory_outbox (id, company_id, workspace_id, kind, payload, event_id, created_at)
VALUES (@id, @company_id, @workspace_id, @kind, @payload, @event_id, @now)
ON CONFLICT (company_id, event_id) DO NOTHING
RETURNING id;

-- name: GetMemoryOutbox :one
SELECT m.*, c.slug AS company_slug
FROM memory_outbox m JOIN companies c ON c.id = m.company_id
WHERE m.id = @id;

-- name: MarkMemorySent :exec
UPDATE memory_outbox SET status = 'sent', sent_at = @now, last_error = '' WHERE id = @id;

-- name: MarkMemoryFailed :exec
UPDATE memory_outbox
SET attempts = attempts + 1, last_error = @last_error,
    status = CASE WHEN attempts + 1 >= @max_attempts THEN 'failed' ELSE 'pending' END
WHERE id = @id;

-- name: ListPendingMemory :many
SELECT id FROM memory_outbox
WHERE status = 'pending' AND created_at < @older_than
ORDER BY created_at
LIMIT @lim;
