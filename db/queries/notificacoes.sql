-- Notificações por pessoa (ADR-0042): chaves da instância, assinaturas de web push, preferências.

-- name: GetInstanceSecret :one
SELECT * FROM instance_secrets WHERE name = @name;

-- name: InsertInstanceSecret :execrows
-- DO NOTHING: duas instâncias do processo gerando a chave VAPID juntas ficam com a primeira; quem perdeu relê.
INSERT INTO instance_secrets (name, value_enc, created_at, updated_at) VALUES (@name, @value_enc, @now, @now)
ON CONFLICT (name) DO NOTHING;

-- name: UpsertPushSubscription :one
INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth, user_agent, created_at)
VALUES (@id, @user_id, @endpoint, @p256dh, @auth, @user_agent, @now)
ON CONFLICT (endpoint) DO UPDATE
SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, user_agent = EXCLUDED.user_agent, failures = 0
RETURNING *;

-- name: ListPushSubscriptions :many
SELECT * FROM push_subscriptions WHERE user_id = @user_id ORDER BY created_at, id;

-- name: ListPushSubscriptionsForUsers :many
SELECT * FROM push_subscriptions WHERE user_id = ANY(@user_ids::uuid[]) ORDER BY created_at, id;

-- name: DeletePushSubscription :execrows
DELETE FROM push_subscriptions WHERE user_id = @user_id AND (id::text = sqlc.arg(ref)::text OR endpoint = sqlc.arg(ref)::text);

-- name: DeletePushSubscriptionByID :exec
DELETE FROM push_subscriptions WHERE id = @id;

-- name: RecordPushSuccess :exec
UPDATE push_subscriptions SET last_success_at = @now, failures = 0 WHERE id = @id;

-- name: RecordPushFailure :one
UPDATE push_subscriptions SET failures = failures + 1 WHERE id = @id RETURNING failures;

-- name: ListNotificationPrefs :many
SELECT * FROM notification_prefs WHERE company_id = @company_id AND user_id = @user_id;

-- name: ListNotificationPrefsForUsers :many
SELECT * FROM notification_prefs WHERE company_id = @company_id AND user_id = ANY(@user_ids::uuid[]);

-- name: UpsertNotificationPref :exec
INSERT INTO notification_prefs (company_id, user_id, kind, toast, push, updated_at)
VALUES (@company_id, @user_id, @kind, @toast, @push, @now)
ON CONFLICT (company_id, user_id, kind) DO UPDATE SET toast = EXCLUDED.toast, push = EXCLUDED.push, updated_at = EXCLUDED.updated_at;

-- name: ListMemberIDsAtLeast :many
-- Os membros da empresa com papel de pelo menos member (os que decidem gates e recebem os avisos de trabalho).
SELECT user_id FROM company_members WHERE company_id = @company_id AND role IN ('member', 'admin', 'owner');
