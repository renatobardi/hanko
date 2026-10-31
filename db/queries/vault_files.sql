-- name: ListSecretFileFieldsAfter :many
-- Campos arquivo do cofre (ADR-0068 §5.1). A listagem é paginada por id: o rekey e a migração entre drivers leem um
-- campo por vez, nunca o conteúdo de todos de uma vez.
SELECT f.id, f.company_id, f.secret_id FROM secret_fields f
WHERE f.kind = 'file' AND f.provider = 'local'
  AND (sqlc.narg(company_id)::uuid IS NULL OR f.company_id = sqlc.narg(company_id)::uuid)
  AND f.id > @after_id
ORDER BY f.id
LIMIT @lim;

-- name: ListSecretFileVersionsAfter :many
-- Versões arquivadas de campo arquivo (ADR-0068 §3.1): o rekey e a migração entre drivers leem uma página por id.
-- O AAD da versão entra no rekey: secret_id, id do campo e número da versão.
SELECT v.id, v.company_id, v.field_id, f.secret_id, v.version, v.aad_version
FROM secret_field_versions v JOIN secret_fields f ON f.id = v.field_id
WHERE v.blob_key <> ''
  AND (sqlc.narg(company_id)::uuid IS NULL OR v.company_id = sqlc.narg(company_id)::uuid)
  AND v.id > @after_id
ORDER BY v.id
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

-- name: ReplaceSecretFieldFile :one
-- Troca o conteúdo de um campo arquivo: metadados novos, versão nova e data da troca. Quem chama já arquivou a versão
-- anterior (secret_field_versions e o blob arquivado) e regravou o blob, na mesma transação.
UPDATE secret_fields SET file_name = @file_name, content_type = @content_type, size_bytes = @size_bytes,
       version = version + 1, rotated_at = @now, updated_at = @now
WHERE id = @id AND company_id = @company_id AND kind = 'file'
RETURNING *;

-- name: ReplaceSecretFieldBlob :execrows
-- Sobrescreve o texto cifrado do campo no driver postgres e devolve as linhas afetadas (0 = o campo não tinha linha aqui).
UPDATE secret_field_blobs SET content = @content WHERE company_id = @company_id AND field_id = @field_id;
