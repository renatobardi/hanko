-- Tags de persona (ADR-0087). O service confere papel, limites e estado; aqui só leitura e escrita.

-- name: ListPersonaTags :many
SELECT * FROM persona_tags
WHERE company_id = @company_id AND (@include_archived::boolean OR archived_at IS NULL)
ORDER BY lower(name), id;

-- name: ListPersonaTagsWithCount :many
SELECT sqlc.embed(t), (SELECT count(*) FROM persona_tag_links l WHERE l.tag_id = t.id)::bigint AS persona_count
FROM persona_tags t
WHERE t.company_id = @company_id AND (@include_archived::boolean OR t.archived_at IS NULL)
ORDER BY lower(t.name), t.id;

-- name: GetPersonaTag :one
SELECT * FROM persona_tags WHERE company_id = @company_id AND id = @id;

-- name: GetPersonaTagForUpdate :one
SELECT * FROM persona_tags WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetPersonaTagByName :one
SELECT * FROM persona_tags WHERE company_id = @company_id AND lower(name) = lower(@name::text);

-- name: CountPersonaTags :one
SELECT count(*) FROM persona_tags WHERE company_id = @company_id;

-- name: CreatePersonaTag :one
INSERT INTO persona_tags (id, company_id, name, color, description, archived_at, created_at, updated_at)
VALUES (@id, @company_id, @name, @color, @description, sqlc.narg(archived_at), @now, @now)
RETURNING *;

-- name: UpdatePersonaTag :one
UPDATE persona_tags SET name = @name, color = @color, description = @description, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetPersonaTagArchived :one
UPDATE persona_tags SET archived_at = sqlc.narg(archived_at), updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeletePersonaTag :execrows
DELETE FROM persona_tags WHERE company_id = @company_id AND id = @id;

-- name: ListPersonaTagLinks :many
SELECT * FROM persona_tag_links WHERE company_id = @company_id ORDER BY persona_id, tag_id;

-- name: CountPersonaTagLinks :one
SELECT count(*) FROM persona_tag_links WHERE company_id = @company_id AND tag_id = @tag_id;

-- name: ListPersonaIDsWithTag :many
SELECT persona_id FROM persona_tag_links WHERE company_id = @company_id AND tag_id = @tag_id ORDER BY persona_id;

-- name: CountTagsOfPersona :one
SELECT count(*) FROM persona_tag_links WHERE company_id = @company_id AND persona_id = @persona_id;

-- name: InsertPersonaTagLink :execrows
INSERT INTO persona_tag_links (company_id, persona_id, tag_id, created_at) VALUES (@company_id, @persona_id, @tag_id, @now)
ON CONFLICT (persona_id, tag_id) DO NOTHING;

-- name: DeletePersonaTagLink :execrows
DELETE FROM persona_tag_links WHERE company_id = @company_id AND persona_id = @persona_id AND tag_id = @tag_id;

-- name: ListPersonaTagRefs :many
-- As tags de cada persona da empresa, para o chip (ficha, lista, organograma, MCP).
SELECT l.persona_id, sqlc.embed(t)
FROM persona_tag_links l JOIN persona_tags t ON t.id = l.tag_id
WHERE l.company_id = @company_id
ORDER BY l.persona_id, lower(t.name), t.id;

-- name: RefreshPersonaTagNames :exec
-- Recalcula personas.tag_names (ADR-0087 item 10) das personas dadas, na transação da escrita de tag.
UPDATE personas p SET tag_names = coalesce((
    SELECT string_agg(t.name, E'\n' ORDER BY lower(t.name), t.id)
    FROM persona_tag_links l JOIN persona_tags t ON t.id = l.tag_id
    WHERE l.persona_id = p.id), '')
WHERE p.company_id = @company_id AND p.id = ANY(@persona_ids::uuid[]);

-- name: LockPersonaTags :exec
-- Serializa as escritas de tag da empresa: os limites de 20 por persona e 200 por empresa contam e inserem sem corrida.
SELECT pg_advisory_xact_lock(hashtext('hanko:persona_tags:' || @company_id::text));
