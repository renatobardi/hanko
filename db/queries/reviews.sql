-- name: CreateReview :one
INSERT INTO reviews (id, company_id, task_id, author_persona_id, author_run_id, reviewer_persona_id, agent, model,
                     requested_status, status, created_at)
VALUES (@id, @company_id, @task_id, @author_persona_id, @author_run_id, @reviewer_persona_id, @agent, @model,
        @requested_status, 'pending', @now)
RETURNING *;

-- name: GetReviewByIDForUpdate :one
SELECT * FROM reviews WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetReviewByRunIDForUpdate :one
SELECT * FROM reviews WHERE run_id = @run_id FOR UPDATE;

-- name: GetReviewByRunID :one
SELECT * FROM reviews WHERE run_id = @run_id;

-- name: OpenReviewForTask :one
SELECT * FROM reviews WHERE task_id = @task_id AND status IN ('pending', 'running');

-- name: MarkReviewRunning :one
UPDATE reviews SET status = 'running', run_id = @run_id, artifact = @artifact, artifact_sha256 = @artifact_sha256, started_at = @now
WHERE id = @id AND status = 'pending'
RETURNING *;

-- name: FinishReview :one
UPDATE reviews SET status = @status, verdict = @verdict, justification = @justification, gate_id = @gate_id, finished_at = @now
WHERE id = @id AND status IN ('pending', 'running')
RETURNING *;

-- name: CancelOpenReviewsForTask :many
UPDATE reviews SET status = 'cancelled', finished_at = @now
WHERE task_id = @task_id AND status IN ('pending', 'running')
RETURNING *;

-- name: ListStaleReviews :many
-- Revisões abertas que perderam o rumo: pending há mais que @pending_before (o job não conseguiu iniciá-las)
-- ou running cuja run já terminou (o fim não foi processado).
SELECT rv.* FROM reviews rv
LEFT JOIN runs r ON r.id = rv.run_id
WHERE (rv.status = 'pending' AND rv.created_at < @pending_before::timestamptz)
   OR (rv.status = 'running' AND (r.id IS NULL OR r.status NOT IN ('queued', 'running')))
ORDER BY rv.created_at
LIMIT @lim;

-- name: SetPersonaReviewer :one
UPDATE personas SET reviewer_persona_id = @reviewer_persona_id, reviewer_agent = @reviewer_agent,
                    reviewer_model = @reviewer_model, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;
