-- Cofre para agentes (ADR-0068): campos, histórico, coleções, concessões, delegações e registro de uso.

-- ---------- campos ----------

-- name: CreateSecretField :one
INSERT INTO secret_fields (id, company_id, secret_id, name, kind, sensitive, provider, value_enc, external_ref, blob_key,
                           file_name, content_type, size_bytes, value_hmac, aad_version, position, version,
                           revealed_at, rotated_at, created_at, updated_at)
VALUES (@id, @company_id, @secret_id, @name, @kind, @sensitive, @provider, @value_enc, @external_ref, @blob_key,
        @file_name, @content_type, @size_bytes, @value_hmac, @aad_version, @position, 1,
        sqlc.narg(revealed_at), @now, @now, @now)
RETURNING *;

-- name: ListSecretFields :many
SELECT * FROM secret_fields WHERE company_id = @company_id AND secret_id = @secret_id ORDER BY position, name;

-- name: GetSecretField :one
SELECT * FROM secret_fields WHERE company_id = @company_id AND secret_id = @secret_id AND name = @name;

-- name: GetSecretFieldForUpdate :one
SELECT * FROM secret_fields WHERE company_id = @company_id AND secret_id = @secret_id AND name = @name FOR UPDATE;

-- name: RotateSecretField :one
-- Troca o valor e sobe a versão; quem chama já copiou o valor anterior para secret_field_versions na mesma transação.
UPDATE secret_fields SET provider = @provider, value_enc = @value_enc, external_ref = @external_ref, blob_key = @blob_key,
       file_name = @file_name, content_type = @content_type, size_bytes = @size_bytes, value_hmac = @value_hmac,
       aad_version = @aad_version, version = version + 1, revealed_at = sqlc.narg(revealed_at), rotated_at = @now,
       updated_at = @now
WHERE id = @id AND company_id = @company_id
RETURNING *;

-- name: UpdateSecretFieldMeta :one
UPDATE secret_fields SET sensitive = @sensitive, position = @position, updated_at = @now
WHERE id = @id AND company_id = @company_id
RETURNING *;

-- name: MarkSecretFieldRevealed :one
-- Revelar uma vez por versão: só afeta a linha se ainda não foi revelada (atômico, sem corrida).
UPDATE secret_fields SET revealed_at = @now WHERE id = @id AND company_id = @company_id AND revealed_at IS NULL
RETURNING *;

-- name: TouchSecretFieldUsed :exec
UPDATE secret_fields SET last_used_at = @now WHERE id = @id AND company_id = @company_id;

-- name: DeleteSecretField :execrows
DELETE FROM secret_fields WHERE id = @id AND company_id = @company_id;

-- name: ListSecretFieldsForRekey :many
SELECT id, secret_id, value_enc, aad_version FROM secret_fields WHERE provider = 'local' AND length(value_enc) > 0;

-- name: SetSecretFieldValueEnc :exec
UPDATE secret_fields SET value_enc = @value_enc, aad_version = @aad_version WHERE id = @id;

-- name: ListDuplicateSecretFieldHmacs :many
-- Relatório de saúde: campos com o mesmo HMAC de valor (ADR-0068 §7). Nunca devolve valor.
SELECT f.value_hmac, s.name AS secret_name, f.name AS field_name
FROM secret_fields f JOIN secrets s ON s.id = f.secret_id
WHERE f.company_id = @company_id AND f.value_hmac <> '' AND s.scope = 'company'
  AND f.value_hmac IN (SELECT value_hmac FROM secret_fields
                       WHERE company_id = @company_id AND value_hmac <> ''
                       GROUP BY value_hmac HAVING count(*) > 1)
ORDER BY f.value_hmac, s.name, f.name;

-- ---------- histórico ----------

-- name: CreateSecretFieldVersion :one
INSERT INTO secret_field_versions (id, company_id, field_id, version, provider, value_enc, external_ref, blob_key,
                                   file_name, content_type, size_bytes, aad_version, revealed_at,
                                   replaced_by_type, replaced_by_id, created_at, replaced_at)
VALUES (@id, @company_id, @field_id, @version, @provider, @value_enc, @external_ref, @blob_key,
        @file_name, @content_type, @size_bytes, @aad_version, sqlc.narg(revealed_at),
        @replaced_by_type, @replaced_by_id, @created_at, @now)
RETURNING *;

-- name: ListSecretFieldVersions :many
-- Do mais novo para o mais antigo (a ordem que domain.PruneHistory espera).
SELECT * FROM secret_field_versions WHERE company_id = @company_id AND field_id = @field_id ORDER BY version DESC;

-- name: GetSecretFieldVersion :one
SELECT * FROM secret_field_versions WHERE company_id = @company_id AND field_id = @field_id AND version = @version;

-- name: MarkSecretFieldVersionRevealed :one
UPDATE secret_field_versions SET revealed_at = @now
WHERE id = @id AND company_id = @company_id AND revealed_at IS NULL
RETURNING *;

-- name: DeleteSecretFieldVersions :execrows
DELETE FROM secret_field_versions WHERE company_id = @company_id AND id = ANY(@ids::uuid[]);

-- name: ListFieldsWithHistory :many
-- Campos que têm histórico (o job de retenção percorre um por vez); secret_id é o sujeito do fato.
SELECT DISTINCT v.company_id, v.field_id, f.secret_id
FROM secret_field_versions v JOIN secret_fields f ON f.id = v.field_id;

-- name: ListSecretFieldVersionsForRekey :many
-- Histórico cifrado a reescrever na troca de HANKO_SECRET_KEY (secret_id e version entram no AAD).
SELECT v.id, v.field_id, f.secret_id, v.version, v.value_enc, v.aad_version
FROM secret_field_versions v JOIN secret_fields f ON f.id = v.field_id
WHERE v.provider = 'local' AND length(v.value_enc) > 0;

-- name: SetSecretFieldVersionValueEnc :exec
UPDATE secret_field_versions SET value_enc = @value_enc WHERE id = @id;

-- ---------- coleções ----------

-- name: CreateSecretCollection :one
INSERT INTO secret_collections (id, company_id, name, description, is_default, rotate_after_days,
                                created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @name, @description, @is_default, sqlc.narg(rotate_after_days),
        @created_by_type, @created_by_id, @now, @now)
RETURNING *;

-- name: ListSecretCollections :many
SELECT * FROM secret_collections WHERE company_id = @company_id ORDER BY is_default DESC, name;

-- name: ListSecretCollectionsWithCounts :many
-- Coleções da empresa com quantos segredos cada uma tem (a resposta da API leva a contagem).
SELECT c.*, count(i.secret_id)::bigint AS secrets_count
FROM secret_collections c LEFT JOIN secret_collection_items i ON i.collection_id = c.id
WHERE c.company_id = @company_id
GROUP BY c.id ORDER BY c.is_default DESC, c.name;

-- name: GetSecretCollection :one
SELECT * FROM secret_collections WHERE company_id = @company_id AND id = @id;

-- name: GetSecretCollectionByName :one
SELECT * FROM secret_collections WHERE company_id = @company_id AND name = @name;

-- name: GetDefaultSecretCollection :one
SELECT * FROM secret_collections WHERE company_id = @company_id AND is_default;

-- name: UpdateSecretCollection :one
UPDATE secret_collections SET name = @name, description = @description, rotate_after_days = sqlc.narg(rotate_after_days),
       updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteSecretCollection :execrows
-- A coleção padrão não se apaga (quem chama recusa antes; o filtro é a segunda barreira).
DELETE FROM secret_collections WHERE company_id = @company_id AND id = @id AND NOT is_default;

-- name: AddSecretToCollection :exec
INSERT INTO secret_collection_items (company_id, collection_id, secret_id, added_at)
VALUES (@company_id, @collection_id, @secret_id, @now)
ON CONFLICT (collection_id, secret_id) DO NOTHING;

-- name: RemoveSecretFromCollection :execrows
DELETE FROM secret_collection_items WHERE company_id = @company_id AND collection_id = @collection_id AND secret_id = @secret_id;

-- name: ListCollectionSecrets :many
SELECT s.* FROM secrets s JOIN secret_collection_items i ON i.secret_id = s.id
WHERE i.company_id = @company_id AND i.collection_id = @collection_id
ORDER BY s.name;

-- name: ListSecretCollectionsOf :many
SELECT c.* FROM secret_collections c JOIN secret_collection_items i ON i.collection_id = c.id
WHERE i.company_id = @company_id AND i.secret_id = @secret_id
ORDER BY c.is_default DESC, c.name;

-- ---------- concessões ----------

-- name: CreateCollectionGrant :one
INSERT INTO secret_collection_grants (id, company_id, collection_id, subject_type, subject_id, permission,
                                      created_by_type, created_by_id, created_at)
VALUES (@id, @company_id, @collection_id, @subject_type, sqlc.narg(subject_id), @permission,
        @created_by_type, @created_by_id, @now)
RETURNING *;

-- name: ListCollectionGrants :many
SELECT * FROM secret_collection_grants WHERE company_id = @company_id AND collection_id = @collection_id
ORDER BY subject_type, created_at;

-- name: DeleteCollectionGrant :execrows
DELETE FROM secret_collection_grants WHERE company_id = @company_id AND collection_id = @collection_id AND id = @id;

-- name: PersonaCanUseSecret :one
-- A persona alcança o segredo se alguma coleção que o contém foi concedida (use) a ela, a todas as personas ou a uma
-- persona acima dela no organograma (persona_tree). Recursão limitada a 50 níveis, como o organograma (ADR-0068 §2).
WITH RECURSIVE chain AS (
    SELECT p.id, p.reports_to, 0 AS depth FROM personas p WHERE p.company_id = @company_id AND p.id = @persona_id
    UNION ALL
    SELECT p.id, p.reports_to, c.depth + 1 FROM personas p JOIN chain c ON p.id = c.reports_to
    WHERE p.company_id = @company_id AND c.depth < 50
)
SELECT EXISTS (
    SELECT 1 FROM secret_collection_items i
    JOIN secret_collection_grants g ON g.collection_id = i.collection_id AND g.permission = 'use'
    WHERE i.company_id = @company_id AND i.secret_id = @secret_id
      AND (g.subject_type = 'all_personas'
           OR (g.subject_type = 'persona' AND g.subject_id = @persona_id::uuid)
           OR (g.subject_type = 'persona_tree' AND g.subject_id IN (SELECT id FROM chain)))
)::boolean AS allowed;

-- name: UserCollectionPermissions :many
-- Permissões de um humano sobre um segredo, pelas coleções que o contêm (view/manage).
SELECT DISTINCT g.permission FROM secret_collection_items i
JOIN secret_collection_grants g ON g.collection_id = i.collection_id
WHERE i.company_id = @company_id AND i.secret_id = @secret_id
  AND g.subject_type = 'user' AND g.subject_id = @user_id::uuid;

-- ---------- delegações ----------

-- name: CreateSecretDelegation :one
INSERT INTO secret_delegations (id, company_id, owner_user_id, secret_id, target_type, target_id, expires_at, created_at)
VALUES (@id, @company_id, @owner_user_id, @secret_id, @target_type, @target_id, @expires_at, @now)
RETURNING *;

-- name: ListUserDelegations :many
-- Só as do dono (ADR-0050 §7: ninguém mais vê segredo de usuário).
SELECT * FROM secret_delegations WHERE company_id = @company_id AND owner_user_id = @owner_user_id
ORDER BY created_at DESC LIMIT @lim;

-- name: RevokeSecretDelegation :one
UPDATE secret_delegations SET revoked_at = @now
WHERE company_id = @company_id AND owner_user_id = @owner_user_id AND id = @id AND revoked_at IS NULL
RETURNING *;

-- name: ActiveDelegationForTarget :one
-- A delegação válida do segredo para o alvo (a mais nova, se houver mais de uma).
SELECT * FROM secret_delegations
WHERE company_id = @company_id AND secret_id = @secret_id AND target_type = @target_type AND target_id = @target_id
  AND revoked_at IS NULL AND expires_at > @now
ORDER BY created_at DESC LIMIT 1;

-- name: CountActiveDelegationsOfUser :one
SELECT count(*) FROM secret_delegations
WHERE company_id = @company_id AND owner_user_id = @owner_user_id AND revoked_at IS NULL AND expires_at > @now;

-- name: DeleteUserSecrets :execrows
-- Saída do humano da empresa (ADR-0068 §6.5): apaga os segredos de usuário dele; delegações e campos caem em cascata.
DELETE FROM secrets WHERE company_id = @company_id AND scope = 'user' AND owner_user_id = @owner_user_id;

-- ---------- registro de uso ----------

-- name: LogSecretAccess :exec
INSERT INTO secret_access_log (id, company_id, secret_id, field_name, via, run_id, persona_id, delegation_id, accessed_at)
VALUES (@id, @company_id, @secret_id, @field_name, @via, sqlc.narg(run_id), sqlc.narg(persona_id), sqlc.narg(delegation_id), @now);

-- name: ListSecretAccess :many
SELECT * FROM secret_access_log WHERE company_id = @company_id AND secret_id = @secret_id
ORDER BY accessed_at DESC LIMIT @lim;

-- name: PruneSecretAccessLog :execrows
DELETE FROM secret_access_log WHERE accessed_at < @before;

-- name: ListSecretsDueRotation :many
-- Campos de segredos da empresa sem rotacao alem do prazo: o do item ou, sem prazo no item, o menor das colecoes.
WITH due AS (
    SELECT s.id, s.name,
           COALESCE(s.rotate_after_days, (SELECT min(c.rotate_after_days) FROM secret_collections c
                                          JOIN secret_collection_items i ON i.collection_id = c.id
                                          WHERE i.secret_id = s.id)) AS limit_days
    FROM secrets s
    WHERE s.company_id = @company_id AND s.scope = 'company'
)
SELECT due.id AS secret_id, due.name AS secret_name, f.name AS field_name, f.rotated_at, due.limit_days::integer AS limit_days
FROM due JOIN secret_fields f ON f.secret_id = due.id
WHERE due.limit_days IS NOT NULL AND f.rotated_at + make_interval(days => due.limit_days) < @now::timestamptz
ORDER BY due.name, f.name;

-- ---------- arquivo (driver postgres; ADR-0068 §5.1) ----------

-- name: CreateSecretFieldBlob :exec
INSERT INTO secret_field_blobs (field_id, company_id, content) VALUES (@field_id, @company_id, @content);

-- name: GetSecretFieldBlob :one
SELECT content FROM secret_field_blobs WHERE company_id = @company_id AND field_id = @field_id;

-- name: ListSecretFieldBlobsForRekey :many
SELECT b.field_id, f.secret_id, b.content FROM secret_field_blobs b JOIN secret_fields f ON f.id = b.field_id;

-- name: SetSecretFieldBlob :exec
UPDATE secret_field_blobs SET content = @content WHERE field_id = @field_id;

-- name: ListSecretFileFieldsForRekey :many
SELECT id, company_id, secret_id FROM secret_fields WHERE kind = 'file' AND provider = 'local';
