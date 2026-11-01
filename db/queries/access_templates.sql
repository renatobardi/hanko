-- Modelos de acesso do assistente Conectar (ADR-0089).

-- name: CreateAccessTemplate :one
INSERT INTO access_templates (id, company_id, slug, name, description,
                              read_mode, write_mode, destructive_mode, suggested,
                              created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description,
        @read_mode, @write_mode, @destructive_mode, @suggested,
        @actor_type, @actor_id, @now, @now)
RETURNING *;

-- name: GetAccessTemplateByID :one
SELECT * FROM access_templates WHERE company_id = @company_id AND id = @id;

-- name: GetAccessTemplateBySlug :one
SELECT * FROM access_templates WHERE company_id = @company_id AND slug = @slug;

-- name: ListAccessTemplates :many
SELECT * FROM access_templates WHERE company_id = @company_id ORDER BY suggested DESC, name, id;

-- name: CountAccessTemplates :one
SELECT count(*)::bigint FROM access_templates WHERE company_id = @company_id;

-- name: CountAccessTemplatesBySlug :one
SELECT count(*)::bigint FROM access_templates WHERE company_id = @company_id AND slug = @slug;

-- name: UpdateAccessTemplate :one
UPDATE access_templates
SET name = @name, description = @description, read_mode = @read_mode, write_mode = @write_mode,
    destructive_mode = @destructive_mode, suggested = @suggested, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteAccessTemplate :execrows
DELETE FROM access_templates WHERE company_id = @company_id AND id = @id;

-- name: ClearSuggestedAccessTemplate :exec
UPDATE access_templates SET suggested = false, updated_at = @now
WHERE company_id = @company_id AND suggested AND id <> @id;
