-- Fila de follow-ups (ADR-0039): instruções do operador que esperam a run ativa de uma task.
-- Os follow-ups do chat são conversation_messages com run_id nulo; as consultas deles estão no fim.

-- name: InsertTaskFollowup :one
INSERT INTO task_followups (id, company_id, task_id, author_type, author_id, body, delivery, waiting_run_id, created_at)
VALUES (@id, @company_id, @task_id, @author_type, @author_id, @body, @delivery, @waiting_run_id, @now)
RETURNING *;

-- name: GetTaskFollowup :one
SELECT * FROM task_followups WHERE company_id = @company_id AND id = @id;

-- name: ListPendingTaskFollowups :many
SELECT * FROM task_followups WHERE task_id = @task_id AND status = 'pending' ORDER BY created_at, id;

-- name: UpdateTaskFollowupBody :one
UPDATE task_followups SET body = @body, edited_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: DiscardTaskFollowup :one
UPDATE task_followups SET status = 'discarded', discarded_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: ConsumeTaskFollowup :one
UPDATE task_followups SET status = 'consumed', consumed_run_id = @run_id, consumed_at = @now, comment_id = @comment_id
WHERE company_id = @company_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: GetConversationMessage :one
SELECT * FROM conversation_messages WHERE company_id = @company_id AND id = @id;

-- name: SetConversationMessageDelivery :exec
UPDATE conversation_messages SET delivery = @delivery WHERE id = @id AND author_type = 'human' AND run_id IS NULL;

-- name: UpdatePendingConversationMessage :one
UPDATE conversation_messages SET body = @body, edited_at = @now
WHERE company_id = @company_id AND id = @id AND author_type = 'human' AND run_id IS NULL
RETURNING *;

-- name: DeletePendingConversationMessage :execrows
DELETE FROM conversation_messages
WHERE company_id = @company_id AND id = @id AND author_type = 'human' AND run_id IS NULL;

-- name: SteerConversationMessage :one
-- Entrega ao vivo: a mensagem passa a pertencer à run em andamento.
UPDATE conversation_messages SET run_id = @run_id
WHERE company_id = @company_id AND id = @id AND author_type = 'human' AND run_id IS NULL
RETURNING *;
