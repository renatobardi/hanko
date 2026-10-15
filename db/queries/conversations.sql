-- Chat (ADR-0027): conversas de um humano com uma persona, suas mensagens e as runs ligadas a elas.

-- name: CreateConversation :one
INSERT INTO conversations (id, company_id, persona_id, title, status, created_by_type, created_by_id, created_at, updated_at, last_message_at)
VALUES (@id, @company_id, @persona_id, @title, 'open', @created_by_type, @created_by_id, @now, @now, @now)
RETURNING *;

-- name: GetConversation :one
SELECT * FROM conversations WHERE company_id = @company_id AND id = @id;

-- name: GetConversationForUpdate :one
SELECT * FROM conversations WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetConversationView :one
SELECT sqlc.embed(c), p.name AS persona_name, p.slug AS persona_slug, ar.id AS active_run_id,
       (SELECT count(*) FROM conversation_messages pm
         WHERE pm.conversation_id = c.id AND pm.author_type = 'human' AND pm.run_id IS NULL)::bigint AS pending_messages,
       (SELECT COALESCE(SUM(cr.cost_usd), 0) FROM runs cr WHERE cr.conversation_id = c.id)::float8 AS cost_usd
FROM conversations c
JOIN personas p ON p.id = c.persona_id
LEFT JOIN runs ar ON ar.conversation_id = c.id AND ar.status IN ('queued', 'running')
WHERE c.company_id = @company_id AND c.id = @id;

-- name: ListConversationViews :many
SELECT sqlc.embed(c), p.name AS persona_name, p.slug AS persona_slug, ar.id AS active_run_id,
       (SELECT count(*) FROM conversation_messages pm
         WHERE pm.conversation_id = c.id AND pm.author_type = 'human' AND pm.run_id IS NULL)::bigint AS pending_messages,
       (SELECT COALESCE(SUM(cr.cost_usd), 0) FROM runs cr WHERE cr.conversation_id = c.id)::float8 AS cost_usd
FROM conversations c
JOIN personas p ON p.id = c.persona_id
LEFT JOIN runs ar ON ar.conversation_id = c.id AND ar.status IN ('queued', 'running')
WHERE c.company_id = @company_id
  AND (sqlc.narg(persona_id)::uuid IS NULL OR c.persona_id = sqlc.narg(persona_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR c.status = sqlc.narg(status)::text)
ORDER BY c.last_message_at DESC, c.id DESC
LIMIT @lim;

-- name: SetConversationStatus :one
UPDATE conversations SET status = @status, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: RenameConversation :one
UPDATE conversations SET title = @title, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: TouchConversation :exec
UPDATE conversations SET last_message_at = @now, updated_at = @now WHERE id = @id;

-- name: InsertConversationMessage :one
INSERT INTO conversation_messages (id, company_id, conversation_id, author_type, author_id, body, run_id, created_at)
VALUES (@id, @company_id, @conversation_id, @author_type, @author_id, @body, @run_id, @now)
RETURNING *;

-- name: ListConversationMessages :many
-- As @lim mensagens mais recentes, em ordem cronológica.
SELECT * FROM (
    SELECT m.* FROM conversation_messages m
    WHERE m.company_id = @company_id AND m.conversation_id = @conversation_id
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT @lim
) recent
ORDER BY created_at, id;

-- name: ListPendingConversationMessages :many
-- Mensagens humanas que nenhuma run recolheu ainda.
SELECT * FROM conversation_messages
WHERE conversation_id = @conversation_id AND author_type = 'human' AND run_id IS NULL
ORDER BY created_at, id;

-- name: ClaimPendingConversationMessages :execrows
UPDATE conversation_messages SET run_id = @run_id
WHERE conversation_id = @conversation_id AND author_type = 'human' AND run_id IS NULL;

-- name: ActiveRunForConversation :one
SELECT * FROM runs WHERE conversation_id = @conversation_id AND status IN ('queued', 'running');

-- name: LatestRunOfConversation :one
-- A run mais recente da conversa, exceto @except_id (a que está abrindo): dela vem a sessão do harness a retomar.
SELECT * FROM runs WHERE conversation_id = @conversation_id AND id <> @except_id
ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: DeleteConversation :exec
DELETE FROM conversations WHERE company_id = @company_id AND id = @id;
