-- Sala (ADR-0033): conversa com várias personas — participantes, entregas de mensagens por persona e runs.

-- name: CreateRoom :one
INSERT INTO conversations (id, company_id, persona_id, kind, title, status, created_by_type, created_by_id, room_settings,
                           created_at, updated_at, last_message_at)
VALUES (@id, @company_id, NULL, 'room', @title, 'open', @created_by_type, @created_by_id, @room_settings, @now, @now, @now)
RETURNING *;

-- name: SetRoomSettings :one
UPDATE conversations SET room_settings = @room_settings, updated_at = @now
WHERE company_id = @company_id AND id = @id AND kind = 'room'
RETURNING *;

-- name: AddRoomParticipant :execrows
-- Entra na sala, ou volta (a linha de quem saiu é reaproveitada); quem já está não muda.
INSERT INTO conversation_participants (company_id, conversation_id, persona_id, added_at, added_by_type, added_by_id, removed_at)
VALUES (@company_id, @conversation_id, @persona_id, @now, @added_by_type, @added_by_id, NULL)
ON CONFLICT (conversation_id, persona_id) DO UPDATE
SET added_at = EXCLUDED.added_at, added_by_type = EXCLUDED.added_by_type, added_by_id = EXCLUDED.added_by_id, removed_at = NULL
WHERE conversation_participants.removed_at IS NOT NULL;

-- name: RemoveRoomParticipant :execrows
UPDATE conversation_participants SET removed_at = @now
WHERE company_id = @company_id AND conversation_id = @conversation_id AND persona_id = @persona_id AND removed_at IS NULL;

-- name: ListRoomParticipants :many
-- Participantes (os que saíram também, com removed_at) das salas pedidas, com a run aberta e a fila de cada um.
SELECT cp.conversation_id, cp.persona_id, cp.added_at, cp.added_by_type, cp.added_by_id, cp.removed_at,
       p.name AS persona_name, p.slug AS persona_slug, p.title AS persona_title, p.icon AS persona_icon,
       p.status AS persona_status, ar.id AS active_run_id,
       (SELECT count(*) FROM conversation_message_runs mr
         WHERE mr.conversation_id = cp.conversation_id AND mr.persona_id = cp.persona_id AND mr.run_id IS NULL)::bigint AS pending
FROM conversation_participants cp
JOIN personas p ON p.id = cp.persona_id
LEFT JOIN runs ar ON ar.conversation_id = cp.conversation_id AND ar.persona_id = cp.persona_id AND ar.status IN ('queued', 'running')
WHERE cp.company_id = @company_id AND cp.conversation_id = ANY(@conversation_ids::uuid[])
ORDER BY cp.conversation_id, cp.added_at, p.name;

-- name: InsertRoomMessage :one
INSERT INTO conversation_messages (id, company_id, conversation_id, author_type, author_id, body, run_id, persona_id,
                                   reply_to_id, mentions, created_at)
VALUES (@id, @company_id, @conversation_id, @author_type, @author_id, @body, @run_id, @persona_id,
        @reply_to_id, COALESCE(@mentions::uuid[], '{}'::uuid[]), @now)
RETURNING *;

-- name: InsertMessageDelivery :exec
INSERT INTO conversation_message_runs (company_id, conversation_id, message_id, persona_id, run_id, origin_message_id, hop, created_at)
VALUES (@company_id, @conversation_id, @message_id, @persona_id, NULL, @origin_message_id, @hop, @now)
ON CONFLICT (message_id, persona_id) DO NOTHING;

-- name: CountPendingDeliveries :one
SELECT count(*) FROM conversation_message_runs
WHERE conversation_id = @conversation_id AND persona_id = @persona_id AND run_id IS NULL;

-- name: ClaimPendingDeliveries :execrows
UPDATE conversation_message_runs SET run_id = @run_id
WHERE conversation_id = @conversation_id AND persona_id = @persona_id AND run_id IS NULL;

-- name: DeletePendingDeliveries :execrows
DELETE FROM conversation_message_runs
WHERE conversation_id = @conversation_id AND persona_id = @persona_id AND run_id IS NULL;

-- name: ListRunDeliveries :many
-- O que uma run da sala recolheu, na ordem em que chegou.
SELECT * FROM conversation_message_runs WHERE run_id = @run_id ORDER BY created_at, message_id;

-- name: CountOriginRuns :one
-- Runs abertas (ou por abrir) a partir de uma mensagem humana: as que já recolheram uma entrega da cadeia e as
-- personas que ainda têm uma entrega dela na fila. Uma run que recolheu duas cadeias conta nas duas.
SELECT ((SELECT count(DISTINCT mr.run_id) FROM conversation_message_runs mr
          WHERE mr.origin_message_id = @origin_message_id AND mr.run_id IS NOT NULL)
      + (SELECT count(DISTINCT mq.persona_id) FROM conversation_message_runs mq
          WHERE mq.origin_message_id = @origin_message_id AND mq.run_id IS NULL))::bigint AS runs;

-- name: ListMessageDeliveries :many
SELECT message_id, persona_id, run_id FROM conversation_message_runs
WHERE company_id = @company_id AND message_id = ANY(@message_ids::uuid[])
ORDER BY message_id, created_at, persona_id;

-- name: ActiveRunForConversationPersona :one
SELECT * FROM runs
WHERE conversation_id = @conversation_id AND persona_id = @persona_id AND status IN ('queued', 'running');

-- name: LatestRunOfConversationPersona :one
-- A run mais recente da persona nesta sala, exceto @except_id: dela vem a sessão do harness a retomar.
SELECT * FROM runs
WHERE conversation_id = @conversation_id AND persona_id = @persona_id AND id <> @except_id
ORDER BY created_at DESC, id DESC LIMIT 1;
