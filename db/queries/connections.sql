-- name: CreateConnection :one
INSERT INTO connections (id, company_id, slug, name, url, identity, owner_user_id, status, integration_id, scopes, client_id,
                         client_secret_ref, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @url, @identity, sqlc.narg(owner_user_id), 'draft', sqlc.narg(integration_id), @scopes,
        @client_id, @client_secret_ref, @created_by_type, @created_by_id, @now, @now)
RETURNING *;

-- name: GetConnectionByID :one
SELECT * FROM connections WHERE company_id = @company_id AND id = @id;

-- name: GetConnectionByIDForUpdate :one
SELECT * FROM connections WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetConnectionBySlug :one
SELECT * FROM connections WHERE company_id = @company_id AND slug = @slug;

-- name: GetConnectionByIntegration :one
SELECT * FROM connections WHERE company_id = @company_id AND integration_id = @integration_id;

-- name: GetConnectionByStateHash :one
SELECT * FROM connections WHERE auth_state_hash = @auth_state_hash FOR UPDATE;

-- name: GetConnectionByGate :one
SELECT * FROM connections WHERE company_id = @company_id AND gate_id = @gate_id;

-- name: ListConnections :many
-- Conexões da empresa visíveis ao chamador: as da empresa e as pessoais dele.
SELECT * FROM connections
WHERE company_id = @company_id AND (identity = 'company' OR owner_user_id = sqlc.narg(viewer_user_id))
ORDER BY name, id;

-- name: CountConnections :one
SELECT count(*) FROM connections WHERE company_id = @company_id;

-- name: SetConnectionDiscovery :one
UPDATE connections SET resource = @resource, issuer = @issuer, authorization_endpoint = @authorization_endpoint,
       token_endpoint = @token_endpoint, registration_endpoint = @registration_endpoint, client_id = @client_id,
       scopes = @scopes, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: StartConnectionAuth :one
UPDATE connections SET status = 'pending_auth', auth_state_hash = @auth_state_hash, auth_verifier_enc = @auth_verifier_enc,
       auth_started_by_id = @auth_started_by_id, auth_expires_at = @auth_expires_at, last_error = '', updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: FinishConnectionAuth :one
-- Consome o state (uso único) e marca a conexão como conectada.
UPDATE connections SET status = 'connected', auth_state_hash = NULL, auth_verifier_enc = '', auth_started_by_id = NULL,
       auth_expires_at = NULL, expires_at = sqlc.narg(expires_at), connected_at = @now, last_refresh_at = NULL,
       refreshing_until = NULL, last_error = '', last_probe_status = '', updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: ClearConnectionAuth :one
-- Autorização que falhou ou foi negada: apaga state/verifier e volta ao rascunho com o motivo.
UPDATE connections SET status = 'draft', auth_state_hash = NULL, auth_verifier_enc = '', auth_started_by_id = NULL,
       auth_expires_at = NULL, last_error = @last_error, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetConnectionStatus :one
UPDATE connections SET status = @status, last_error = @last_error, refreshing_until = NULL, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetConnectionProbe :one
UPDATE connections SET last_probe_at = @now, last_probe_status = @last_probe_status, status = @status, last_error = @last_error,
       updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetConnectionToken :one
-- Renovação concluída: nova validade; o valor do token foi para o segredo.
UPDATE connections SET expires_at = sqlc.narg(expires_at), last_refresh_at = @now, refreshing_until = NULL, status = 'connected',
       last_error = '', updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: ClaimConnectionRefresh :one
-- Reserva a renovação (uma por vez); a chamada de rede roda fora de transação.
UPDATE connections SET refreshing_until = @until
WHERE company_id = @company_id AND id = @id AND status = 'connected' AND (refreshing_until IS NULL OR refreshing_until < @now)
RETURNING *;

-- name: ReleaseConnectionRefresh :exec
UPDATE connections SET refreshing_until = NULL WHERE company_id = @company_id AND id = @id;

-- name: DueConnectionRefreshes :many
SELECT id, company_id FROM connections
WHERE status = 'connected' AND expires_at IS NOT NULL AND expires_at < @before AND (refreshing_until IS NULL OR refreshing_until < @now)
ORDER BY expires_at LIMIT @lim;

-- name: SetConnectionGate :exec
UPDATE connections SET gate_id = sqlc.narg(gate_id), updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: UpdateConnectionSettings :one
UPDATE connections SET name = @name, scopes = @scopes, client_id = @client_id, client_secret_ref = @client_secret_ref, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteConnection :exec
DELETE FROM connections WHERE company_id = @company_id AND id = @id;

-- name: SetIntegrationEnabled :exec
UPDATE integrations SET enabled = @enabled, updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: DisconnectConnection :one
UPDATE connections SET status = 'draft', auth_state_hash = NULL, auth_verifier_enc = '', auth_started_by_id = NULL,
       auth_expires_at = NULL, expires_at = NULL, connected_at = NULL, refreshing_until = NULL, last_refresh_at = NULL,
       last_probe_status = '', last_error = '', gate_id = NULL, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetConnectionError :exec
UPDATE connections SET last_error = @last_error, updated_at = @now WHERE company_id = @company_id AND id = @id;
