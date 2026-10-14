-- name: LockUsers :exec
-- Serializa o cadastro de usuários (bootstrap do primeiro dono, admissão).
SELECT pg_advisory_xact_lock(7420005);

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (id, email, name, provider, external_id, password_hash, role, created_at, last_login_at)
VALUES (@id, @email, @name, @provider, @external_id, @password_hash, @role, @now, @now)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(@email);

-- name: GetUserByExternal :one
SELECT * FROM users WHERE provider = @provider AND external_id = @external_id;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at, id;

-- name: TouchUserLogin :exec
UPDATE users SET last_login_at = @now::timestamptz WHERE id = @id;

-- name: UpdateUserProfile :one
UPDATE users SET email = @email, name = @name, last_login_at = @now::timestamptz WHERE id = @id
RETURNING *;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at, user_agent, ip)
VALUES (@id, @user_id, @token_hash, @now, @expires_at, @now, @user_agent, @ip)
RETURNING *;

-- name: GetSessionByHash :one
SELECT * FROM sessions WHERE token_hash = @token_hash AND expires_at > @now;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = @now, expires_at = @expires_at WHERE id = @id;

-- name: DeleteSessionByHash :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= @now;

-- name: CreateAPIKey :one
INSERT INTO api_keys (id, user_id, name, prefix, hash, created_at)
VALUES (@id, @user_id, @name, @prefix, @hash, @now)
RETURNING *;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE hash = @hash AND revoked_at IS NULL;

-- name: ListAPIKeys :many
SELECT * FROM api_keys WHERE user_id = @user_id ORDER BY created_at DESC, id;

-- name: RevokeAPIKey :one
UPDATE api_keys SET revoked_at = @now::timestamptz WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL
RETURNING *;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = @now::timestamptz WHERE id = @id;
