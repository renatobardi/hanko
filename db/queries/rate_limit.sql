-- name: TakeRateToken :one
-- Token bucket atômico (ADR-0058 §B): recarrega, decide e consome numa instrução; a linha é o lock. Os SET leem a
-- linha antiga, por isso a recarga se repete. last_allowed é a decisão desta chamada.
INSERT INTO rate_limit_buckets AS b (key, tokens, updated_at, last_allowed)
VALUES (@key, CASE WHEN sqlc.arg(capacity)::float8 >= sqlc.arg(cost)::float8 THEN sqlc.arg(capacity)::float8 - sqlc.arg(cost)::float8 ELSE sqlc.arg(capacity)::float8 END, sqlc.arg(now)::timestamptz,
        sqlc.arg(capacity)::float8 >= sqlc.arg(cost)::float8)
ON CONFLICT (key) DO UPDATE SET
    tokens = CASE
        WHEN LEAST(sqlc.arg(capacity)::float8, b.tokens + GREATEST(EXTRACT(EPOCH FROM (sqlc.arg(now)::timestamptz - b.updated_at)), 0) * sqlc.arg(refill_per_sec)::float8) >= sqlc.arg(cost)::float8
        THEN LEAST(sqlc.arg(capacity)::float8, b.tokens + GREATEST(EXTRACT(EPOCH FROM (sqlc.arg(now)::timestamptz - b.updated_at)), 0) * sqlc.arg(refill_per_sec)::float8) - sqlc.arg(cost)::float8
        ELSE LEAST(sqlc.arg(capacity)::float8, b.tokens + GREATEST(EXTRACT(EPOCH FROM (sqlc.arg(now)::timestamptz - b.updated_at)), 0) * sqlc.arg(refill_per_sec)::float8)
    END,
    last_allowed = LEAST(sqlc.arg(capacity)::float8, b.tokens + GREATEST(EXTRACT(EPOCH FROM (sqlc.arg(now)::timestamptz - b.updated_at)), 0) * sqlc.arg(refill_per_sec)::float8) >= sqlc.arg(cost)::float8,
    updated_at = GREATEST(b.updated_at, sqlc.arg(now)::timestamptz)
RETURNING tokens, last_allowed;

-- name: DeleteStaleRateBuckets :execrows
DELETE FROM rate_limit_buckets WHERE updated_at < @before;
