-- Chaves de ingestão (ADR-0076 item 3): escopo `ingest`, uma por host, presa a uma empresa.

-- name: CreateIngestKey :one
INSERT INTO api_keys (id, user_id, name, prefix, hash, scope, company_id, created_at)
VALUES (@id, @user_id, @name, @prefix, @hash, 'ingest', @company_id, @now)
RETURNING *;

-- name: ListIngestKeys :many
SELECT * FROM api_keys
WHERE company_id = @company_id AND scope = 'ingest' AND (@include_revoked::bool OR revoked_at IS NULL)
ORDER BY created_at DESC, id
LIMIT @page_limit::int OFFSET @page_offset::int;

-- name: RevokeIngestKey :one
UPDATE api_keys SET revoked_at = @now::timestamptz
WHERE id = @id AND company_id = @company_id AND scope = 'ingest' AND revoked_at IS NULL
RETURNING *;
