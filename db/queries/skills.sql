-- name: CreateSkill :one
INSERT INTO skills (id, company_id, slug, name, description, body, version, created_by_type, created_by_id,
                    updated_by_type, updated_by_id, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description, @body, 1, @actor_type, @actor_id, @actor_type, @actor_id, @now, @now)
RETURNING *;

-- name: GetSkillByID :one
SELECT * FROM skills WHERE company_id = @company_id AND id = @id;

-- name: GetSkillByIDForUpdate :one
SELECT * FROM skills WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetSkillBySlug :one
SELECT * FROM skills WHERE company_id = @company_id AND slug = @slug;

-- name: ListSkills :many
SELECT * FROM skills WHERE company_id = @company_id ORDER BY name, id;

-- name: UpdateSkill :one
UPDATE skills
SET name = @name, description = @description, body = @body, version = @version,
    updated_by_type = @actor_type, updated_by_id = @actor_id, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteSkill :execrows
DELETE FROM skills WHERE company_id = @company_id AND id = @id;

-- name: SetPersonaSkill :exec
INSERT INTO persona_skills (company_id, persona_id, skill_id, enabled, updated_at)
VALUES (@company_id, @persona_id, @skill_id, @enabled, @now)
ON CONFLICT (persona_id, skill_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at;

-- name: ListPersonaSkillLinks :many
-- Todas as skills da empresa com o estado de uma persona (false quando nunca ligada).
SELECT s.id AS skill_id, s.slug, s.name, s.description, COALESCE(ps.enabled, false)::boolean AS enabled
FROM skills s
LEFT JOIN persona_skills ps ON ps.skill_id = s.id AND ps.persona_id = @persona_id
WHERE s.company_id = @company_id
ORDER BY s.name, s.id;

-- name: ListSkillPersonaLinks :many
-- Todas as personas da empresa com o estado de uma skill.
SELECT p.id AS persona_id, p.slug, p.name, COALESCE(ps.enabled, false)::boolean AS enabled
FROM personas p
LEFT JOIN persona_skills ps ON ps.persona_id = p.id AND ps.skill_id = @skill_id
WHERE p.company_id = @company_id
ORDER BY p.name, p.id;

-- name: ListEnabledSkillsForPersona :many
SELECT s.* FROM skills s
JOIN persona_skills ps ON ps.skill_id = s.id
WHERE ps.persona_id = @persona_id AND ps.enabled AND s.company_id = @company_id
ORDER BY s.slug, s.id;

-- name: GetEnabledSkillForPersona :one
SELECT s.* FROM skills s
JOIN persona_skills ps ON ps.skill_id = s.id
WHERE ps.persona_id = @persona_id AND ps.enabled AND s.company_id = @company_id AND s.slug = @slug;

-- name: CountSkillPersonas :many
SELECT skill_id, count(*)::bigint AS personas
FROM persona_skills
WHERE company_id = @company_id AND enabled
GROUP BY skill_id;

-- name: ExportPersonaSkills :many
-- (persona, skill) ligados, por slug, para o arquivo de export.
SELECT p.slug AS persona_slug, s.slug AS skill_slug
FROM persona_skills ps
JOIN personas p ON p.id = ps.persona_id
JOIN skills s ON s.id = ps.skill_id
WHERE ps.company_id = @company_id AND ps.enabled
ORDER BY p.created_at, p.id, s.slug;
