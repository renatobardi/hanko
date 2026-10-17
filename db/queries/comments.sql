-- name: CreateComment :one
INSERT INTO task_comments (id, company_id, task_id, author_type, author_id, run_id, body, event_id, anchor, created_at)
VALUES (@id, @company_id, @task_id, @author_type, @author_id, @run_id, @body, @event_id, @anchor, @now)
RETURNING *;

-- name: GetCommentByEventID :one
SELECT * FROM task_comments WHERE company_id = @company_id AND event_id = @event_id;

-- name: ListComments :many
SELECT * FROM task_comments WHERE company_id = @company_id AND task_id = @task_id ORDER BY created_at, id;

-- name: HasHumanCommentSince :one
SELECT EXISTS (
    SELECT 1 FROM task_comments
    WHERE company_id = @company_id AND task_id = @task_id AND author_type = 'human' AND created_at > @since
) AS found;

-- name: GetCommentByID :one
SELECT * FROM task_comments WHERE company_id = @company_id AND id = @id;
