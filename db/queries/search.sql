-- Busca única (ADR-0024). @query é uma tsquery 'simple' montada por internal/search (palavras já dobradas por Fold);
-- o documento é a coluna search_tsv (GENERATED, migration 00015). Ranking = ts_rank × fator de recência
-- (1,0 para "agora" → 0,75 aos 30 dias → 0,5 para algo muito antigo). Todo SELECT filtra por company_id.
-- O texto longo vem cortado (left) só para montar o trecho no app.
--
-- Forma das consultas grandes (tasks, comentários, execuções): "cand" escolhe até 2000 candidatas que casam, as mais
-- recentes primeiro (o planner usa o GIN quando o termo é raro e o índice por data quando é comum), "top" ranqueia só
-- elas e fica com as N melhores, e só então vêm os joins e o texto. Uma consulta que casa mais de 2000 documentos é
-- genérica demais para valer um ranking global: ranquear as 60 mil linhas de um termo onipresente custava ~0,5 s.
-- A tsquery entra como parâmetro direto (sem CTE): com ela o planner enxerga a frequência real do termo e escolhe o
-- índice por data nos comuns. O limite de 2000 é o mesmo nas três (ADR-0024).

-- name: SearchTasks :many
WITH cand AS MATERIALIZED (
    SELECT t.id, t.search_tsv, t.updated_at
    FROM tasks t
    WHERE t.company_id = @company_id AND t.search_tsv @@ to_tsquery('simple', sqlc.arg(query)::text)
    ORDER BY t.updated_at DESC, t.number DESC
    LIMIT 2000
),
top AS MATERIALIZED (
    SELECT c.id, c.updated_at,
           (ts_rank(c.search_tsv, to_tsquery('simple', sqlc.arg(query)::text), 1)
            * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - c.updated_at)), 0) / 2592000.0)))::float8 AS score
    FROM cand c
    ORDER BY score DESC, c.updated_at DESC, c.id
    LIMIT @lim
)
SELECT t.id, t.number, co.task_prefix, t.title, left(t.description, 20000)::text AS description, t.status, t.updated_at, top.score
FROM top
JOIN tasks t ON t.id = top.id
JOIN companies co ON co.id = t.company_id
ORDER BY top.score DESC, t.updated_at DESC, t.id;

-- name: SearchComments :many
WITH cand AS MATERIALIZED (
    SELECT k.id, k.search_tsv, k.created_at
    FROM task_comments k
    WHERE k.company_id = @company_id AND k.search_tsv @@ to_tsquery('simple', sqlc.arg(query)::text)
    ORDER BY k.created_at DESC, k.id
    LIMIT 2000
),
top AS MATERIALIZED (
    SELECT c.id, c.created_at,
           (ts_rank(c.search_tsv, to_tsquery('simple', sqlc.arg(query)::text), 1)
            * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - c.created_at)), 0) / 2592000.0)))::float8 AS score
    FROM cand c
    ORDER BY score DESC, c.created_at DESC, c.id
    LIMIT @lim
)
SELECT k.id, k.task_id, co.task_prefix, t.number, t.title AS task_title, k.author_type, COALESCE(p.name, '')::text AS author_name,
       left(k.body, 20000)::text AS body, k.created_at, top.score
FROM top
JOIN task_comments k ON k.id = top.id
JOIN tasks t ON t.id = k.task_id
JOIN companies co ON co.id = k.company_id
LEFT JOIN personas p ON p.id = k.author_id AND k.author_type = 'persona'
ORDER BY top.score DESC, k.created_at DESC, k.id;

-- name: SearchPersonas :many
SELECT p.id, p.slug, p.name, p.title, left(p.instructions, 20000)::text AS instructions, p.status, p.updated_at,
       (ts_rank(p.search_tsv, to_tsquery('simple', sqlc.arg(query)::text), 1)
        * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - p.updated_at)), 0) / 2592000.0)))::float8 AS score
FROM personas p
WHERE p.company_id = @company_id AND p.search_tsv @@ to_tsquery('simple', sqlc.arg(query)::text)
ORDER BY score DESC, p.updated_at DESC, p.id
LIMIT @lim;

-- name: SearchRuns :many
WITH cand AS MATERIALIZED (
    SELECT r.id, r.search_tsv, r.created_at
    FROM runs r
    WHERE r.company_id = @company_id AND r.search_tsv @@ to_tsquery('simple', sqlc.arg(query)::text)
    ORDER BY r.created_at DESC, r.id DESC
    LIMIT 2000
),
top AS MATERIALIZED (
    SELECT c.id, c.created_at,
           (ts_rank(c.search_tsv, to_tsquery('simple', sqlc.arg(query)::text), 1)
            * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - c.created_at)), 0) / 2592000.0)))::float8 AS score
    FROM cand c
    ORDER BY score DESC, c.created_at DESC, c.id
    LIMIT @lim
)
SELECT r.id, r.name, left(r.summary, 20000)::text AS summary, r.status, r.created_at,
       co.task_prefix, t.number, t.title AS task_title, p.name AS persona_name, top.score
FROM top
JOIN runs r ON r.id = top.id
LEFT JOIN tasks t ON t.id = r.task_id -- run de chat não tem task (ADR-0027)
JOIN personas p ON p.id = r.persona_id
JOIN companies co ON co.id = r.company_id
ORDER BY top.score DESC, r.created_at DESC, r.id;

-- name: SearchFlows :many
SELECT f.id, f.slug, f.name, f.description, f.enabled, f.updated_at,
       (ts_rank(f.search_tsv, to_tsquery('simple', sqlc.arg(query)::text), 1)
        * (0.5 + 0.5 / (1 + GREATEST(extract(epoch FROM (sqlc.arg(now)::timestamptz - f.updated_at)), 0) / 2592000.0)))::float8 AS score
FROM flows f
WHERE f.company_id = @company_id AND f.search_tsv @@ to_tsquery('simple', sqlc.arg(query)::text)
ORDER BY score DESC, f.updated_at DESC, f.id
LIMIT @lim;
