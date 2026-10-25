-- name: ListSecretFileFieldsAfter :many
-- Campos arquivo do cofre (ADR-0068 §5.1). A listagem é paginada por id: o rekey e a migração entre drivers leem um
-- campo por vez, nunca o conteúdo de todos de uma vez.
SELECT f.id, f.company_id, f.secret_id FROM secret_fields f
WHERE f.kind = 'file' AND f.provider = 'local'
  AND (sqlc.narg(company_id)::uuid IS NULL OR f.company_id = sqlc.narg(company_id)::uuid)
  AND f.id > @after_id
ORDER BY f.id
LIMIT @lim;

-- name: ListUserSecretFileFields :many
-- Campos arquivo dos segredos pessoais de um usuário: a purga na saída apaga os blobs dos drivers depois do commit.
SELECT f.id, f.company_id FROM secret_fields f
JOIN secrets s ON s.id = f.secret_id
WHERE s.company_id = @company_id AND s.scope = 'user' AND s.owner_user_id = @owner_user_id
  AND f.kind = 'file' AND f.provider = 'local';

-- name: DeleteSecretFieldBlob :exec
-- Apaga a linha do conteúdo cifrado de um campo arquivo (driver postgres), depois de copiada para outro driver.
DELETE FROM secret_field_blobs WHERE field_id = @field_id;
