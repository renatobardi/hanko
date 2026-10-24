-- Passkeys e reautenticação recente (ADR-0070).

-- name: CreateUserPasskey :one
INSERT INTO user_passkeys (id, user_id, credential_id, public_key, sign_count, aaguid, transports, backup_eligible,
                           backup_state, name, created_at)
VALUES (@id, @user_id, @credential_id, @public_key, @sign_count, sqlc.narg(aaguid), @transports, @backup_eligible,
        @backup_state, @name, @now)
RETURNING *;

-- name: ListUserPasskeys :many
SELECT * FROM user_passkeys WHERE user_id = @user_id ORDER BY created_at;

-- name: CountUserPasskeys :one
SELECT count(*) FROM user_passkeys WHERE user_id = @user_id;

-- name: GetPasskeyByCredentialID :one
SELECT * FROM user_passkeys WHERE credential_id = @credential_id;

-- name: TouchPasskey :exec
-- Grava o contador novo da assinatura (quem chama já conferiu que ele subiu) e o último uso.
UPDATE user_passkeys SET sign_count = @sign_count, backup_state = @backup_state, last_used_at = @now
WHERE id = @id AND user_id = @user_id;

-- name: DeleteUserPasskey :execrows
DELETE FROM user_passkeys WHERE id = @id AND user_id = @user_id;

-- name: CreateWebauthnChallenge :one
INSERT INTO webauthn_challenges (id, user_id, session_id, purpose, challenge, expires_at, created_at)
VALUES (@id, @user_id, sqlc.narg(session_id), @purpose, @challenge, @expires_at, @now)
RETURNING *;

-- name: ConsumeWebauthnChallenge :one
-- Uso único: apaga e devolve o desafio, só se ainda vale e é do mesmo usuário e propósito.
DELETE FROM webauthn_challenges
WHERE id = @id AND user_id = @user_id AND purpose = @purpose AND expires_at > @now
RETURNING *;

-- name: PruneWebauthnChallenges :execrows
DELETE FROM webauthn_challenges WHERE expires_at <= @now;

-- name: MarkSessionReauth :exec
UPDATE sessions SET reauth_at = @now, reauth_method = @reauth_method WHERE id = @id AND user_id = @user_id;
