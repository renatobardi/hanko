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

-- name: ReplaceSecretFieldFile :one
-- Troca o conteúdo de um campo arquivo: metadados novos e data da troca. Não sobe a versão nem arquiva a anterior
-- (o histórico não guarda o conteúdo de versões de arquivo); quem chama já regravou o blob na mesma transação.
UPDATE secret_fields SET file_name = @file_name, content_type = @content_type, size_bytes = @size_bytes,
       rotated_at = @now, updated_at = @now
WHERE id = @id AND company_id = @company_id AND kind = 'file'
RETURNING *;

-- name: ReplaceSecretFieldBlob :execrows
-- Sobrescreve o texto cifrado do campo no driver postgres e devolve as linhas afetadas (0 = o campo não tinha linha aqui).
UPDATE secret_field_blobs SET content = @content WHERE company_id = @company_id AND field_id = @field_id;
