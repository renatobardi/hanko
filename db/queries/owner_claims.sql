-- name: RevokeOpenOwnerClaims :execrows
-- Invalida (sem apagar o histórico) os claims ainda abertos: used_at preenchido e used_by nulo = revogado.
UPDATE owner_claims SET used_at = sqlc.arg(now)::timestamptz WHERE used_at IS NULL;

-- name: InsertOwnerClaim :exec
INSERT INTO owner_claims (id, token_hash, created_at, expires_at) VALUES (@id, @token_hash, sqlc.arg(now)::timestamptz, @expires_at);

-- name: GetOwnerClaimByHashForUpdate :one
SELECT * FROM owner_claims WHERE token_hash = @token_hash FOR UPDATE;

-- name: GetOwnerClaimByHash :one
SELECT * FROM owner_claims WHERE token_hash = @token_hash;

-- name: UseOwnerClaim :exec
UPDATE owner_claims SET used_at = sqlc.arg(now)::timestamptz, used_by = @used_by WHERE id = @id;
