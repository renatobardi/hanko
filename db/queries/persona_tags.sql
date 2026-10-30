-- Tags de persona (ADR-0087). Leituras do contrato da #118; as escritas entram na fatia 1.

-- name: ListPersonaTags :many
SELECT * FROM persona_tags
WHERE company_id = @company_id AND (sqlc.narg(include_archived)::boolean IS TRUE OR archived_at IS NULL)
ORDER BY lower(name), id;

-- name: GetPersonaTag :one
SELECT * FROM persona_tags WHERE company_id = @company_id AND id = @id;

-- name: ListPersonaTagLinks :many
SELECT * FROM persona_tag_links WHERE company_id = @company_id ORDER BY persona_id, tag_id;

-- name: CountPersonaTagLinks :one
SELECT count(*) FROM persona_tag_links WHERE company_id = @company_id AND tag_id = @tag_id;
