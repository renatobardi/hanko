-- Busca única (ADR-0024). @query é uma tsquery 'simple' montada por internal/search (palavras já dobradas por Fold);
-- o documento é a coluna search_tsv (GENERATED, migration 00015). Ranking = ts_rank × fator de recência
-- (1,0 para "agora" → 0,5 para algo muito antigo; meia-vida de ~30 dias). Todo SELECT filtra por company_id.
-- O texto longo vem cortado (left) só para montar o trecho no app.

-- name: SearchTasks :many
WITH q AS MATERIALIZED (SELECT to_tsquery('simple', sqlc.arg(query)::text) AS query)
SELECT t.id, t.number, c.task_prefix, t.title, left(t.description, 20000)::text AS description, t.status, t.updated_at,
       (ts_rank(t.search_tsv, q.query, 1)
        * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - t.updated_at)), 0) / 2592000.0)))::float8 AS score
FROM tasks t
JOIN companies c ON c.id = t.company_id
CROSS JOIN q
WHERE t.company_id = @company_id AND t.search_tsv @@ q.query
ORDER BY score DESC, t.updated_at DESC, t.id
LIMIT @lim;

-- name: SearchComments :many
WITH q AS MATERIALIZED (SELECT to_tsquery('simple', sqlc.arg(query)::text) AS query)
SELECT k.id, k.task_id, c.task_prefix, t.number, t.title AS task_title, k.author_type, COALESCE(p.name, '')::text AS author_name,
       left(k.body, 20000)::text AS body, k.created_at,
       (ts_rank(k.search_tsv, q.query, 1)
        * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - k.created_at)), 0) / 2592000.0)))::float8 AS score
FROM task_comments k
JOIN tasks t ON t.id = k.task_id
JOIN companies c ON c.id = k.company_id
LEFT JOIN personas p ON p.id = k.author_id AND k.author_type = 'persona'
CROSS JOIN q
WHERE k.company_id = @company_id AND k.search_tsv @@ q.query
ORDER BY score DESC, k.created_at DESC, k.id
LIMIT @lim;

-- name: SearchPersonas :many
WITH q AS MATERIALIZED (SELECT to_tsquery('simple', sqlc.arg(query)::text) AS query)
SELECT p.id, p.slug, p.name, p.title, left(p.instructions, 20000)::text AS instructions, p.status, p.updated_at,
       (ts_rank(p.search_tsv, q.query, 1)
        * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - p.updated_at)), 0) / 2592000.0)))::float8 AS score
FROM personas p
CROSS JOIN q
WHERE p.company_id = @company_id AND p.search_tsv @@ q.query
ORDER BY score DESC, p.updated_at DESC, p.id
LIMIT @lim;

-- name: SearchRuns :many
WITH q AS MATERIALIZED (SELECT to_tsquery('simple', sqlc.arg(query)::text) AS query)
SELECT r.id, r.name, left(r.summary, 20000)::text AS summary, r.status, r.created_at,
       c.task_prefix, t.number, t.title AS task_title, p.name AS persona_name,
       (ts_rank(r.search_tsv, q.query, 1)
        * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - r.created_at)), 0) / 2592000.0)))::float8 AS score
FROM runs r
JOIN tasks t ON t.id = r.task_id
JOIN personas p ON p.id = r.persona_id
JOIN companies c ON c.id = r.company_id
CROSS JOIN q
WHERE r.company_id = @company_id AND r.search_tsv @@ q.query
ORDER BY score DESC, r.created_at DESC, r.id
LIMIT @lim;

-- name: SearchFlows :many
WITH q AS MATERIALIZED (SELECT to_tsquery('simple', sqlc.arg(query)::text) AS query)
SELECT f.id, f.slug, f.name, f.description, f.enabled, f.updated_at,
       (ts_rank(f.search_tsv, q.query, 1)
        * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - f.updated_at)), 0) / 2592000.0)))::float8 AS score
FROM flows f
CROSS JOIN q
WHERE f.company_id = @company_id AND f.search_tsv @@ q.query
ORDER BY score DESC, f.updated_at DESC, f.id
LIMIT @lim;
