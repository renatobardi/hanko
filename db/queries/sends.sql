-- Envio com chave no fragmento do link (ADR-0069). O banco só vê texto cifrado.

-- name: CreateSecretSend :one
INSERT INTO secret_sends (id, company_id, public_id, kind, ciphertext, blob_key, size_bytes, label, kdf_salt,
                          password_verifier, max_accesses, expires_at, source_secret_id, source_field, run_id,
                          created_by_type, created_by_id, created_at)
VALUES (@id, @company_id, @public_id, @kind, @ciphertext, @blob_key, @size_bytes, @label, @kdf_salt,
        @password_verifier, sqlc.narg(max_accesses), @expires_at, sqlc.narg(source_secret_id), @source_field, sqlc.narg(run_id),
        @created_by_type, @created_by_id, @now)
RETURNING *;

-- name: ListSecretSends :many
-- Admin (created_by_id nulo) vê todos os envios da empresa; membro, só os próprios.
SELECT * FROM secret_sends WHERE company_id = @company_id
  AND (sqlc.narg(created_by_id)::uuid IS NULL OR created_by_id = sqlc.narg(created_by_id)::uuid)
ORDER BY created_at DESC LIMIT @lim OFFSET @off;

-- name: GetSecretSend :one
SELECT * FROM secret_sends WHERE company_id = @company_id AND id = @id;

-- name: LockSecretSend :one
SELECT * FROM secret_sends WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetSecretSendByPublicID :one
SELECT * FROM secret_sends WHERE public_id = @public_id;

-- name: OpenSecretSend :one
-- Conta o acesso de forma atômica: só abre se não venceu, não foi apagado, não está bloqueado e cabe no limite.
UPDATE secret_sends SET access_count = access_count + 1, last_accessed_at = @now::timestamptz, failed_attempts = 0
WHERE public_id = @public_id AND purged_at IS NULL AND expires_at > @now
  AND (locked_until IS NULL OR locked_until <= @now)
  AND (max_accesses IS NULL OR access_count < max_accesses)
RETURNING *;

-- name: FailSecretSendPassword :one
-- Senha errada: conta a falha e bloqueia por um tempo depois de @max_fails seguidas.
UPDATE secret_sends SET failed_attempts = failed_attempts + 1,
       locked_until = CASE WHEN failed_attempts + 1 >= @max_fails::int THEN sqlc.narg(lock_until)::timestamptz ELSE locked_until END
WHERE public_id = @public_id AND purged_at IS NULL
RETURNING failed_attempts, locked_until;

-- name: PurgeSecretSend :one
-- Apaga o conteúdo e mantém a linha como registro. Devolve o blob para quem chama apagar no BlobStore.
UPDATE secret_sends SET ciphertext = '', blob_key = '', purged_at = @now
WHERE company_id = @company_id AND id = @id AND purged_at IS NULL
RETURNING id, company_id, blob_key AS old_blob_key;

-- name: ListSecretSendsToPurge :many
-- Vencidos ou no limite de acessos, ainda com conteúdo (o job do river varre e apaga).
SELECT id, company_id, blob_key FROM secret_sends
WHERE purged_at IS NULL AND (expires_at <= @now OR (max_accesses IS NOT NULL AND access_count >= max_accesses))
ORDER BY expires_at LIMIT @lim;

-- name: CreateSecretSendBlob :exec
-- Grava o blob cifrado do envio de arquivo (driver postgres; fs e s3 usam o BlobStore, ADR-0051). Sobrescreve:
-- subir o conteúdo de novo troca o blob.
INSERT INTO secret_send_blobs (send_id, company_id, content) VALUES (@send_id, @company_id, @content)
ON CONFLICT (send_id) DO UPDATE SET content = EXCLUDED.content;

-- name: DeleteSecretSendBlob :execrows
-- Apaga o blob de um envio purgado, na mesma transação do purge.
DELETE FROM secret_send_blobs WHERE company_id = @company_id AND send_id = @send_id;
