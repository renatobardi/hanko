-- name: GetTaskDraft :one
SELECT * FROM drafts WHERE company_id = @company_id AND user_key = @user_key AND task_id = @task_id;

-- name: GetConversationDraft :one
SELECT * FROM drafts WHERE company_id = @company_id AND user_key = @user_key AND conversation_id = @conversation_id;

-- name: UpsertTaskDraft :one
INSERT INTO drafts (id, company_id, user_key, target_type, task_id, body, updated_at)
VALUES (@id, @company_id, @user_key, 'task', @task_id, @body, @now)
ON CONFLICT (company_id, user_key, task_id) WHERE task_id IS NOT NULL
DO UPDATE SET body = EXCLUDED.body, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: UpsertConversationDraft :one
INSERT INTO drafts (id, company_id, user_key, target_type, conversation_id, body, updated_at)
VALUES (@id, @company_id, @user_key, 'conversation', @conversation_id, @body, @now)
ON CONFLICT (company_id, user_key, conversation_id) WHERE conversation_id IS NOT NULL
DO UPDATE SET body = EXCLUDED.body, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteTaskDraft :exec
DELETE FROM drafts WHERE company_id = @company_id AND user_key = @user_key AND task_id = @task_id;

-- name: DeleteConversationDraft :exec
DELETE FROM drafts WHERE company_id = @company_id AND user_key = @user_key AND conversation_id = @conversation_id;
