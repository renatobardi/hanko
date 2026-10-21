-- Caixa por pessoa (ADR-0042). inbox_state guarda só o que é do usuário; os itens são derivados.

-- name: ListInboxStates :many
SELECT * FROM inbox_state WHERE company_id = @company_id AND user_id = @user_id;

-- name: UpsertInboxState :exec
INSERT INTO inbox_state (company_id, user_id, item_kind, item_id, fingerprint, read_at, archived_at, updated_at)
VALUES (@company_id, @user_id, @item_kind, @item_id, @fingerprint, @read_at, @archived_at, @now)
ON CONFLICT (company_id, user_id, item_kind, item_id) DO UPDATE
SET fingerprint = EXCLUDED.fingerprint, read_at = EXCLUDED.read_at, archived_at = EXCLUDED.archived_at, updated_at = EXCLUDED.updated_at;

-- name: PruneInboxState :exec
DELETE FROM inbox_state WHERE company_id = @company_id AND user_id = @user_id AND updated_at < @before;

-- name: CreateCommentMention :exec
INSERT INTO comment_mentions (id, company_id, comment_id, task_id, target_type, user_id, persona_id, author_type, author_id, created_at)
VALUES (@id, @company_id, @comment_id, @task_id, @target_type, @user_id, @persona_id, @author_type, @author_id, @now);

-- name: ListUserMentions :many
-- As menções a um usuário desde since, da mais nova para a mais antiga (a Caixa mostra as dos últimos dias).
SELECT m.id, m.comment_id, m.task_id, m.author_type, m.author_id, m.created_at,
       co.task_prefix, t.number AS task_number, t.title AS task_title,
       COALESCE(NULLIF(u.name, ''), u.email, p.name, '')::text AS author_name,
       left(k.body, 400)::text AS excerpt
FROM comment_mentions m
JOIN task_comments k ON k.id = m.comment_id
JOIN tasks t ON t.id = m.task_id
JOIN companies co ON co.id = m.company_id
LEFT JOIN users u ON m.author_type = 'human' AND u.id = m.author_id
LEFT JOIN personas p ON m.author_type = 'persona' AND p.id = m.author_id
WHERE m.company_id = @company_id AND m.user_id = @user_id AND m.created_at >= @since
ORDER BY m.created_at DESC, m.id DESC
LIMIT @lim;

-- name: ListMemberHandles :many
-- Os membros da empresa com o e-mail, de onde sai o handle de menção (domain.MentionHandle).
SELECT m.user_id, u.email, u.name
FROM company_members m JOIN users u ON u.id = m.user_id
WHERE m.company_id = @company_id;

-- name: ListTaskParticipation :many
-- Os motivos pelos quais as tasks são "minhas" (task_participation, migration 00123), só das tasks pedidas.
SELECT tp.task_id::uuid AS task_id, tp.created::boolean AS created, tp.assigned::boolean AS assigned,
       tp.mentioned::boolean AS mentioned, tp.commented::boolean AS commented, tp.gate::boolean AS gate
FROM task_participation(@company_id, @user_id) tp WHERE tp.task_id = ANY(@task_ids::uuid[]);

-- name: HasRecentPersonaMention :one
-- A persona foi citada por um humano nesta task há pouco (ADR-0042): o wake de menção pode abrir run mesmo que a
-- task seja de outra persona.
SELECT EXISTS (SELECT 1 FROM comment_mentions
               WHERE task_id = @task_id AND persona_id = @persona_id AND author_type = 'human' AND created_at >= @since);
