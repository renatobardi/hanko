-- name: CreateChannel :one
INSERT INTO channels (id, company_id, kind, name, enabled, persona_id, project_id, token_ref, webhook_secret_ref, config,
                      inbound_token, created_at, updated_at)
VALUES (@id, @company_id, @kind, @name, @enabled, @persona_id, @project_id, @token_ref, @webhook_secret_ref, @config,
        @inbound_token, @now, @now)
RETURNING *;

-- name: GetChannel :one
SELECT * FROM channels WHERE company_id = @company_id AND id = @id;

-- name: GetChannelForUpdate :one
SELECT * FROM channels WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetChannelByName :one
SELECT * FROM channels WHERE company_id = @company_id AND name = @name;

-- name: GetChannelByInboundToken :one
-- Entrada pública: o canal é achado só pelo identificador da URL.
SELECT * FROM channels WHERE inbound_token = @inbound_token;

-- name: ListChannels :many
SELECT * FROM channels WHERE company_id = @company_id ORDER BY name, id;

-- name: ListEnabledChannelsByKind :many
SELECT * FROM channels WHERE company_id = @company_id AND enabled AND kind = @kind ORDER BY created_at, id;

-- name: UpdateChannel :one
UPDATE channels
SET name = @name, enabled = @enabled, persona_id = @persona_id, project_id = @project_id, token_ref = @token_ref,
    webhook_secret_ref = @webhook_secret_ref, config = @config, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteChannel :execrows
DELETE FROM channels WHERE company_id = @company_id AND id = @id;

-- name: ChannelSecretUsage :many
-- Canais que citam segredos (para o "usado por" de Segredos e a recusa de apagar).
SELECT id, name, token_ref, webhook_secret_ref FROM channels WHERE company_id = @company_id;

-- name: InsertChannelIdentity :one
INSERT INTO channel_identities (id, company_id, channel_id, user_id, external_id, external_label, status, code_hash, code_expires_at,
                                linked_at, created_at, updated_at)
VALUES (@id, @company_id, @channel_id, @user_id, @external_id, @external_label, @status, @code_hash, @code_expires_at,
        @linked_at, @now, @now)
RETURNING *;

-- name: GetChannelIdentityForUser :one
-- O vínculo vivo (pendente ou ativo) da pessoa no canal.
SELECT * FROM channel_identities WHERE channel_id = @channel_id AND user_id = @user_id AND status <> 'revoked';

-- name: GetChannelIdentityByCode :one
SELECT * FROM channel_identities WHERE code_hash = @code_hash AND status = 'pending';

-- name: GetChannelIdentityByExternal :one
SELECT * FROM channel_identities WHERE channel_id = @channel_id AND external_id = @external_id AND status = 'linked';

-- name: GetChannelIdentityByID :one
SELECT * FROM channel_identities WHERE company_id = @company_id AND id = @id;

-- name: ListChannelIdentities :many
SELECT i.*, u.email AS user_email, u.name AS user_name FROM channel_identities i
JOIN users u ON u.id = i.user_id
WHERE i.company_id = @company_id AND i.channel_id = @channel_id AND i.status <> 'revoked'
ORDER BY i.created_at, i.id;

-- name: ListLinkedIdentitiesForChannel :many
SELECT * FROM channel_identities WHERE channel_id = @channel_id AND status = 'linked' ORDER BY created_at, id;

-- name: SetChannelIdentityCode :one
UPDATE channel_identities SET code_hash = @code_hash, code_expires_at = @code_expires_at, updated_at = @now
WHERE id = @id RETURNING *;

-- name: LinkChannelIdentity :one
UPDATE channel_identities
SET external_id = @external_id, external_label = @external_label, status = 'linked', code_hash = '', code_expires_at = NULL,
    linked_at = @now, updated_at = @now
WHERE id = @id RETURNING *;

-- name: RevokeChannelIdentity :one
UPDATE channel_identities SET status = 'revoked', code_hash = '', code_expires_at = NULL, updated_at = @now
WHERE id = @id RETURNING *;

-- name: GetChannelThread :one
SELECT * FROM channel_threads WHERE channel_id = @channel_id AND peer = @peer;

-- name: GetChannelThreadByConversation :many
SELECT * FROM channel_threads WHERE conversation_id = @conversation_id;

-- name: GetChannelThreadsByTask :many
SELECT * FROM channel_threads WHERE task_id = @task_id;

-- name: UpsertChannelThread :one
INSERT INTO channel_threads (id, company_id, channel_id, peer, identity_id, conversation_id, task_id, created_at, updated_at)
VALUES (@id, @company_id, @channel_id, @peer, @identity_id, @conversation_id, @task_id, @now, @now)
ON CONFLICT (channel_id, peer) DO UPDATE
SET identity_id = EXCLUDED.identity_id, conversation_id = EXCLUDED.conversation_id, task_id = EXCLUDED.task_id, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: InsertChannelMessage :one
-- Sem linhas quando a entrada já existe (mesmo external_id).
INSERT INTO channel_messages (id, company_id, channel_id, direction, kind, peer, identity_id, external_id, body, payload,
                              subject_type, subject_id, status, status_reason, created_at, updated_at)
VALUES (@id, @company_id, @channel_id, @direction, @kind, @peer, @identity_id, @external_id, @body, @payload,
        @subject_type, @subject_id, @status, @status_reason, @now, @now)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetChannelMessage :one
SELECT * FROM channel_messages WHERE id = @id;

-- name: GetChannelMessageForUpdate :one
SELECT * FROM channel_messages WHERE id = @id FOR UPDATE;

-- name: ListChannelMessages :many
SELECT * FROM channel_messages WHERE company_id = @company_id AND channel_id = @channel_id ORDER BY created_at DESC, id DESC LIMIT @lim;

-- name: MarkChannelMessageSent :exec
UPDATE channel_messages
SET status = 'sent', attempts = attempts + 1, last_error = '', provider_msg_id = @provider_msg_id, sent_at = @now, updated_at = @now
WHERE id = @id;

-- name: MarkChannelMessageAttempt :exec
UPDATE channel_messages SET attempts = attempts + 1, last_error = @last_error, updated_at = @now WHERE id = @id;

-- name: MarkChannelMessageFailed :exec
UPDATE channel_messages SET status = 'failed', attempts = attempts + 1, last_error = @last_error, updated_at = @now WHERE id = @id;

-- name: SetChannelMessageStatus :exec
UPDATE channel_messages SET status = @status, status_reason = @status_reason, updated_at = @now WHERE id = @id;

-- name: RequeueChannelMessage :one
UPDATE channel_messages SET status = 'pending', last_error = '', updated_at = @now
WHERE company_id = @company_id AND id = @id AND direction = 'out' AND status = 'failed'
RETURNING *;

-- name: PruneChannelMessages :execrows
DELETE FROM channel_messages WHERE channel_id = @channel_id AND created_at < @before AND status IN ('processed', 'ignored', 'sent');

-- name: ChannelCounts :one
SELECT
    (SELECT count(*) FROM channel_identities ci WHERE ci.channel_id = @channel_id AND ci.status = 'linked')::bigint AS linked,
    (SELECT count(*) FROM channel_identities cp WHERE cp.channel_id = @channel_id AND cp.status = 'pending')::bigint AS pending,
    (SELECT count(*) FROM channel_messages cm WHERE cm.channel_id = @channel_id AND cm.direction = 'out' AND cm.status = 'failed')::bigint AS failed;

-- name: ListLinkedChannelIdentitiesWithUser :many
SELECT i.*, u.email AS user_email, u.name AS user_name FROM channel_identities i
JOIN users u ON u.id = i.user_id
WHERE i.channel_id = @channel_id AND i.status = 'linked' ORDER BY i.created_at, i.id;
