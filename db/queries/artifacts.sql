-- name: LockArtifactChain :exec
-- Serializa a criação de versões do mesmo nome (a chave é "empresa|task|nome"): duas gravações simultâneas não
-- disputam o mesmo número de versão. A trava vale até o fim da transação.
SELECT pg_advisory_xact_lock(hashtextextended(@key::text, 0));

-- name: LatestArtifactInChain :one
SELECT * FROM artifacts
WHERE company_id = @company_id
  AND COALESCE(task_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE(sqlc.narg(task_id)::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  AND name = @name
ORDER BY version DESC
LIMIT 1;

-- name: CreateArtifact :one
INSERT INTO artifacts (id, company_id, task_id, run_id, project_id, persona_id, author_type, author_id, name, mime,
                       size_bytes, sha256, version, created_at)
VALUES (@id, @company_id, @task_id, @run_id, @project_id, @persona_id, @author_type, @author_id, @name, @mime,
        @size_bytes, @sha256, @version, @created_at)
RETURNING *;

-- name: CreateArtifactBlob :exec
INSERT INTO artifact_blobs (artifact_id, company_id, content) VALUES (@artifact_id, @company_id, @content);

-- name: GetArtifactByID :one
SELECT * FROM artifacts WHERE company_id = @company_id AND id = @id;

-- name: GetArtifactContent :one
SELECT content FROM artifact_blobs WHERE company_id = @company_id AND artifact_id = @artifact_id;

-- name: ArtifactCompanyUsage :one
-- Bytes e versões guardados pela empresa (a cota conta todas as versões).
SELECT COALESCE(sum(size_bytes), 0)::bigint AS used_bytes, count(*)::bigint AS versions
FROM artifacts WHERE company_id = @company_id;

-- name: ListArtifactChain :many
-- Todas as versões da cadeia (empresa, task, nome), da mais nova para a mais antiga.
SELECT * FROM artifacts
WHERE company_id = @company_id
  AND COALESCE(task_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE(sqlc.narg(task_id)::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  AND name = @name
ORDER BY version DESC;

-- name: DeleteArtifactChain :execrows
DELETE FROM artifacts
WHERE company_id = @company_id
  AND COALESCE(task_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE(sqlc.narg(task_id)::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  AND name = @name;

-- name: ListArtifactViews :many
-- Cada linha é uma versão. latest_only deixa só a mais nova de cada cadeia (a listagem geral); a lista de uma run
-- usa latest_only=false (o que aquela run publicou). kind: markdown | text | image | other.
SELECT sqlc.embed(a), c.task_prefix,
       COALESCE(t.number, 0)::bigint AS task_number, COALESCE(t.title, '')::text AS task_title,
       COALESCE(pr.slug, '')::text AS project_slug, COALESCE(pr.name, '')::text AS project_name,
       COALESCE(pe.slug, '')::text AS persona_slug, COALESCE(pe.name, '')::text AS persona_name,
       COALESCE(r.name, '')::text AS run_name,
       (SELECT count(*) FROM artifacts v
         WHERE v.company_id = a.company_id AND v.name = a.name
           AND COALESCE(v.task_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE(a.task_id, '00000000-0000-0000-0000-000000000000'::uuid))::bigint AS versions
FROM artifacts a
JOIN companies c ON c.id = a.company_id
LEFT JOIN tasks t ON t.id = a.task_id
LEFT JOIN projects pr ON pr.id = a.project_id
LEFT JOIN personas pe ON pe.id = a.persona_id
LEFT JOIN runs r ON r.id = a.run_id
WHERE a.company_id = @company_id
  AND (sqlc.narg(project_id)::uuid IS NULL OR a.project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(task_id)::uuid IS NULL OR a.task_id = sqlc.narg(task_id)::uuid)
  AND (sqlc.narg(persona_id)::uuid IS NULL OR a.persona_id = sqlc.narg(persona_id)::uuid)
  AND (sqlc.narg(run_id)::uuid IS NULL OR a.run_id = sqlc.narg(run_id)::uuid)
  AND (sqlc.narg(kind)::text IS NULL OR CASE sqlc.narg(kind)::text
        WHEN 'markdown' THEN a.mime = 'text/markdown'
        WHEN 'text' THEN a.mime LIKE 'text/%' AND a.mime <> 'text/markdown'
        WHEN 'image' THEN a.mime LIKE 'image/%'
        ELSE NOT (a.mime LIKE 'text/%' OR a.mime LIKE 'image/%') END)
  AND (sqlc.narg(q)::text IS NULL OR a.name ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (NOT @latest_only::bool OR a.version = (
        SELECT max(v.version) FROM artifacts v
         WHERE v.company_id = a.company_id AND v.name = a.name
           AND COALESCE(v.task_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE(a.task_id, '00000000-0000-0000-0000-000000000000'::uuid)))
ORDER BY a.created_at DESC, a.id DESC
LIMIT @lim OFFSET @off;

-- name: GetArtifactView :one
SELECT sqlc.embed(a), c.task_prefix,
       COALESCE(t.number, 0)::bigint AS task_number, COALESCE(t.title, '')::text AS task_title,
       COALESCE(pr.slug, '')::text AS project_slug, COALESCE(pr.name, '')::text AS project_name,
       COALESCE(pe.slug, '')::text AS persona_slug, COALESCE(pe.name, '')::text AS persona_name,
       COALESCE(r.name, '')::text AS run_name,
       (SELECT count(*) FROM artifacts v
         WHERE v.company_id = a.company_id AND v.name = a.name
           AND COALESCE(v.task_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE(a.task_id, '00000000-0000-0000-0000-000000000000'::uuid))::bigint AS versions
FROM artifacts a
JOIN companies c ON c.id = a.company_id
LEFT JOIN tasks t ON t.id = a.task_id
LEFT JOIN projects pr ON pr.id = a.project_id
LEFT JOIN personas pe ON pe.id = a.persona_id
LEFT JOIN runs r ON r.id = a.run_id
WHERE a.company_id = @company_id AND a.id = @id;

-- name: ExportArtifacts :many
-- Todas as versões da empresa em ordem de criação, com as chaves que o arquivo usa para referenciar (sem o conteúdo).
SELECT a.*, COALESCE(t.number, 0)::bigint AS task_number,
       COALESCE(pr.slug, '')::text AS project_slug, COALESCE(pe.slug, '')::text AS persona_slug
FROM artifacts a
LEFT JOIN tasks t ON t.id = a.task_id
LEFT JOIN projects pr ON pr.id = a.project_id
LEFT JOIN personas pe ON pe.id = a.persona_id
WHERE a.company_id = @company_id
ORDER BY a.created_at, a.version, a.id;
