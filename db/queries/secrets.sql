-- name: CreateSecret :one
INSERT INTO secrets (id, company_id, scope, owner_user_id, name, description, provider, value_enc, external_ref, version,
                     revealed_at, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @scope, sqlc.narg(owner_user_id), @name, @description, @provider, @value_enc, @external_ref, 1,
        sqlc.narg(revealed_at), @created_by_type, @created_by_id, @now, @now)
RETURNING *;

-- name: GetCompanySecretByName :one
SELECT * FROM secrets WHERE company_id = @company_id AND scope = 'company' AND name = @name;

-- name: GetCompanySecretByNameForUpdate :one
SELECT * FROM secrets WHERE company_id = @company_id AND scope = 'company' AND name = @name FOR UPDATE;

-- name: GetUserSecretByName :one
SELECT * FROM secrets WHERE company_id = @company_id AND scope = 'user' AND owner_user_id = @owner_user_id AND name = @name;

-- name: GetUserSecretByNameForUpdate :one
SELECT * FROM secrets WHERE company_id = @company_id AND scope = 'user' AND owner_user_id = @owner_user_id AND name = @name FOR UPDATE;

-- name: ListCompanySecrets :many
SELECT * FROM secrets WHERE company_id = @company_id AND scope = 'company' ORDER BY name;

-- name: ListUserSecrets :many
-- Só os do dono: nenhuma outra consulta devolve segredo de usuário.
SELECT * FROM secrets WHERE company_id = @company_id AND scope = 'user' AND owner_user_id = @owner_user_id ORDER BY name;

-- name: RotateSecret :one
UPDATE secrets SET provider = @provider, value_enc = @value_enc, external_ref = @external_ref, version = version + 1,
       revealed_at = sqlc.narg(revealed_at), description = @description, updated_at = @now
WHERE id = @id AND company_id = @company_id
RETURNING *;

-- name: MarkSecretRevealed :one
-- Revelar uma vez por versão: só afeta a linha se ainda não foi revelada (atômico, sem corrida).
UPDATE secrets SET revealed_at = @now WHERE id = @id AND company_id = @company_id AND revealed_at IS NULL
RETURNING *;

-- name: TouchSecretUsed :exec
UPDATE secrets SET last_used_at = @now WHERE id = @id AND company_id = @company_id;

-- name: DeleteSecret :execrows
DELETE FROM secrets WHERE id = @id AND company_id = @company_id;

-- name: ListAllSecretsForRekey :many
SELECT id, value_enc FROM secrets WHERE provider = 'local' AND value_enc <> '';

-- name: SetSecretValueEnc :exec
UPDATE secrets SET value_enc = @value_enc WHERE id = @id;

-- name: ListPersonasWithEnv :many
-- Personas cujo adapter_config.env tem alguma variável (candidatas à migração para segredos).
SELECT id, company_id, slug, agent, adapter_config FROM personas
WHERE jsonb_typeof(adapter_config -> 'env') = 'object' AND adapter_config -> 'env' <> '{}'::jsonb
ORDER BY company_id, slug;

-- name: SetPersonaAdapterConfig :exec
UPDATE personas SET adapter_config = @adapter_config, updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: ListPersonasReferencingSecrets :many
-- Candidatas a usar algum segredo (o service confere a referência exata; o LIKE só reduz o conjunto).
SELECT id, slug, adapter_config FROM personas
WHERE company_id = @company_id AND adapter_config::text LIKE '%${secret:%'
ORDER BY slug;
