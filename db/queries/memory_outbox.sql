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
-- Erro permanente (permanent) encerra em 'failed'; erro transitório mantém 'pending' e agenda a
-- próxima tentativa em @next_attempt_at (backoff calculado pelo service).
UPDATE memory_outbox
SET attempts = attempts + 1, last_error = @last_error,
    status = CASE WHEN @permanent::boolean THEN 'failed' ELSE 'pending' END,
    next_attempt_at = CASE WHEN @permanent::boolean THEN NULL ELSE @next_attempt_at::timestamptz END
WHERE id = @id;

-- name: ListPendingMemory :many
SELECT id FROM memory_outbox
WHERE status = 'pending' AND created_at < @older_than
  AND (next_attempt_at IS NULL OR next_attempt_at <= @now)
ORDER BY created_at
LIMIT @lim;
