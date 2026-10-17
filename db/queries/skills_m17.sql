-- name: SetSkillFork :exec
UPDATE skills SET forked_from_id = @forked_from_id, forked_from_revision = @forked_from_revision
WHERE company_id = @company_id AND id = @id;

-- name: InsertRunSkill :exec
INSERT INTO run_skills (run_id, company_id, skill_id, slug, revision, sha256, pinned)
VALUES (@run_id, @company_id, sqlc.narg(skill_id), @slug, @revision, @sha256, @pinned)
ON CONFLICT (run_id, slug) DO NOTHING;

-- name: ListRunSkills :many
SELECT * FROM run_skills WHERE company_id = @company_id AND run_id = @run_id ORDER BY slug;

-- name: ListSkillForks :many
SELECT id, slug, name, version FROM skills WHERE company_id = @company_id AND forked_from_id = @skill_id ORDER BY slug;

-- name: MovePersonaSkillLinks :execrows
-- Passa os vínculos persona × skill de uma skill para outra (a versão fixada não vale na skill nova).
INSERT INTO persona_skills (company_id, persona_id, skill_id, enabled, updated_at)
SELECT ps.company_id, ps.persona_id, @to_skill_id, ps.enabled, @now FROM persona_skills ps
WHERE ps.company_id = @company_id AND ps.skill_id = @from_skill_id
ON CONFLICT (persona_id, skill_id) DO NOTHING;

-- name: InsertTeamInstall :one
INSERT INTO team_installs (id, company_id, package_slug, package_name, package_version, source, sha256, counts,
                           installed_by_type, installed_by_id, created_at)
VALUES (@id, @company_id, @package_slug, @package_name, @package_version, @source, @sha256, @counts, @installed_by_type,
        @installed_by_id, @now)
RETURNING *;

-- name: ListTeamInstalls :many
SELECT * FROM team_installs WHERE company_id = @company_id ORDER BY created_at DESC LIMIT @lim;
