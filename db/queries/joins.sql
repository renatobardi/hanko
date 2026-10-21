-- Pedidos de entrada na empresa (ADR-0057).

-- name: UpsertJoinLink :one
-- Um link por empresa: gerar outro troca o token (o anterior deixa de valer) e liga o link.
INSERT INTO company_join_links (company_id, token_hash, enabled, created_by, created_at)
VALUES (@company_id, @token_hash, true, @created_by, @now)
ON CONFLICT (company_id) DO UPDATE
SET token_hash = EXCLUDED.token_hash, enabled = true, created_by = EXCLUDED.created_by, created_at = EXCLUDED.created_at
RETURNING *;

-- name: GetJoinLink :one
SELECT * FROM company_join_links WHERE company_id = @company_id;

-- name: GetJoinLinkByHash :one
SELECT * FROM company_join_links WHERE token_hash = @token_hash AND enabled;

-- name: DisableJoinLink :execrows
UPDATE company_join_links SET enabled = false WHERE company_id = @company_id AND enabled;

-- name: CreateJoinRequest :one
INSERT INTO company_join_requests (id, company_id, user_id, message, created_at)
VALUES (@id, @company_id, @user_id, @message, @now)
RETURNING *;

-- name: GetJoinRequest :one
SELECT * FROM company_join_requests WHERE company_id = @company_id AND id = @id;

-- name: GetJoinRequestForUpdate :one
SELECT * FROM company_join_requests WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: ListJoinRequests :many
SELECT r.id, r.company_id, r.user_id, r.message, r.status, r.role, r.decided_by, r.created_at, r.decided_at,
       u.email, u.name
FROM company_join_requests r JOIN users u ON u.id = r.user_id
WHERE r.company_id = @company_id AND (NOT @only_pending::boolean OR r.status = 'pending')
ORDER BY (r.status = 'pending') DESC, r.created_at DESC, r.id
LIMIT 200;

-- name: DecideJoinRequest :one
UPDATE company_join_requests SET status = @status, role = @role, decided_by = @decided_by, decided_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: ListJoinRequestsForUser :many
SELECT * FROM company_join_requests WHERE user_id = @user_id AND status = 'pending';
