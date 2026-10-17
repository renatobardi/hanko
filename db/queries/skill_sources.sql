-- Skills: versões, versão fixada, fontes git e estúdio (ADR-0035).

-- ---------- versões ----------

-- name: InsertSkillVersion :exec
INSERT INTO skill_versions (id, company_id, skill_id, revision, name, description, body, sha256, source_commit,
                            author_type, author_id, created_at)
VALUES (@id, @company_id, @skill_id, @revision, @name, @description, @body, @sha256, sqlc.narg(source_commit),
        @author_type, @author_id, @now)
ON CONFLICT (skill_id, revision) DO NOTHING;

-- name: ListSkillVersions :many
-- Versões de uma skill, da mais nova para a mais antiga, sem o corpo.
SELECT id, company_id, skill_id, revision, name, description, sha256, source_commit, author_type, author_id, created_at,
       octet_length(body)::integer AS body_bytes
FROM skill_versions
WHERE company_id = @company_id AND skill_id = @skill_id
ORDER BY revision DESC
LIMIT @lim;

-- name: GetSkillVersion :one
SELECT * FROM skill_versions WHERE company_id = @company_id AND skill_id = @skill_id AND revision = @revision;

-- name: SetSkillSourceModified :exec
UPDATE skills SET source_modified = @modified WHERE company_id = @company_id AND id = @id;

-- ---------- versão fixada ----------

-- name: SetPersonaSkillPin :execrows
UPDATE persona_skills SET pinned_revision = sqlc.narg(pinned_revision), updated_at = @now
WHERE company_id = @company_id AND persona_id = @persona_id AND skill_id = @skill_id;

-- name: EnsurePersonaSkill :exec
-- Cria o vínculo desligado quando ele ainda não existe (fixar uma versão antes de ligar a skill).
INSERT INTO persona_skills (company_id, persona_id, skill_id, enabled, updated_at)
VALUES (@company_id, @persona_id, @skill_id, false, @now)
ON CONFLICT (persona_id, skill_id) DO NOTHING;

-- name: ListEnabledPinnedSkillsForPersona :many
-- Skills habilitadas para a persona com o conteúdo efetivo: o da versão fixada quando há uma, senão o atual.
SELECT s.id, s.company_id, s.slug,
       COALESCE(v.name, s.name)::text               AS name,
       COALESCE(v.description, s.description)::text AS description,
       COALESCE(v.body, s.body)::text               AS body,
       COALESCE(v.revision, s.version)::integer     AS version,
       s.created_by_type, s.created_by_id, s.updated_by_type, s.updated_by_id, s.created_at, s.updated_at,
       (ps.pinned_revision IS NOT NULL)::boolean    AS pinned
FROM skills s
JOIN persona_skills ps ON ps.skill_id = s.id
LEFT JOIN skill_versions v ON v.skill_id = s.id AND v.revision = ps.pinned_revision
WHERE ps.persona_id = @persona_id AND ps.enabled AND s.company_id = @company_id
ORDER BY s.slug, s.id;

-- ---------- fontes ----------

-- name: CreateSkillSource :one
INSERT INTO skill_sources (id, company_id, url, tracking_ref, path_prefix, excluded_paths, auto_update, import_new,
                           enabled, status, next_sync_at, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @url, @tracking_ref, @path_prefix, @excluded_paths, @auto_update, @import_new,
        true, @status, sqlc.narg(next_sync_at), @actor_type, @actor_id, @now, @now)
RETURNING *;

-- name: GetSkillSource :one
SELECT * FROM skill_sources WHERE company_id = @company_id AND id = @id;

-- name: GetSkillSourceForUpdate :one
SELECT * FROM skill_sources WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: ListSkillSources :many
SELECT s.*,
       (SELECT count(*) FROM skill_source_entries e WHERE e.source_id = s.id AND e.present)::bigint AS entries,
       (SELECT count(*) FROM skills k WHERE k.source_id = s.id)::bigint AS imported
FROM skill_sources s
WHERE s.company_id = @company_id
ORDER BY s.created_at, s.id;

-- name: UpdateSkillSourceSettings :one
UPDATE skill_sources
SET path_prefix = @path_prefix, excluded_paths = @excluded_paths, auto_update = @auto_update, import_new = @import_new,
    enabled = @enabled, status = @status, next_sync_at = sqlc.narg(next_sync_at), updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: ClaimSkillSourceSync :one
-- Marca a fonte como sincronizando; uma sincronização presa há mais de stale_before é retomada.
UPDATE skill_sources
SET status = 'syncing', last_attempt_at = @now, updated_at = @now
WHERE company_id = @company_id AND id = @id
  AND (status <> 'syncing' OR last_attempt_at IS NULL OR last_attempt_at < @stale_before)
RETURNING *;

-- name: FinishSkillSourceSync :one
UPDATE skill_sources
SET status = @status, last_error = @last_error, failure_count = @failure_count, next_sync_at = sqlc.narg(next_sync_at),
    last_commit = COALESCE(sqlc.narg(last_commit), last_commit), last_tree_hash = COALESCE(sqlc.narg(last_tree_hash), last_tree_hash),
    last_success_at = COALESCE(sqlc.narg(last_success_at), last_success_at), updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteSkillSource :execrows
DELETE FROM skill_sources WHERE company_id = @company_id AND id = @id;

-- name: DueSkillSources :many
-- Fontes com atualização automática vencidas (o tick do river).
SELECT company_id, id FROM skill_sources
WHERE enabled AND auto_update AND next_sync_at IS NOT NULL AND next_sync_at <= @now
  AND (status <> 'syncing' OR last_attempt_at IS NULL OR last_attempt_at < @stale_before)
ORDER BY next_sync_at
LIMIT @lim;

-- name: ListSkillSourceEntries :many
SELECT e.*, COALESCE(k.slug, '')::text AS skill_slug, COALESCE(k.source_modified, false)::boolean AS skill_modified
FROM skill_source_entries e
LEFT JOIN skills k ON k.id = e.skill_id
WHERE e.company_id = @company_id AND e.source_id = @source_id
ORDER BY e.path;

-- name: UpsertSkillSourceEntry :one
INSERT INTO skill_source_entries (id, company_id, source_id, path, name, description, selection, present, has_scripts, error, updated_at)
VALUES (@id, @company_id, @source_id, @path, @name, @description, @selection, true, @has_scripts, @error, @now)
ON CONFLICT (source_id, path) DO UPDATE
SET name = EXCLUDED.name, description = EXCLUDED.description, present = true, has_scripts = EXCLUDED.has_scripts,
    error = EXCLUDED.error, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: MarkSkillSourceEntriesAbsent :exec
UPDATE skill_source_entries SET present = false, updated_at = @now
WHERE company_id = @company_id AND source_id = @source_id AND NOT (path = ANY(@paths::text[])) AND present;

-- name: SetSkillSourceEntryResult :exec
UPDATE skill_source_entries SET skill_id = sqlc.narg(skill_id), error = @error, updated_at = @now
WHERE company_id = @company_id AND id = @id;

-- name: SetSkillSourceEntrySelection :one
UPDATE skill_source_entries SET selection = @selection, updated_at = @now
WHERE company_id = @company_id AND source_id = @source_id AND path = @path
RETURNING *;

-- name: ExcludeSkillSourceEntriesOfSkill :exec
-- A skill importada foi apagada aqui: a entrada sai da seleção para a próxima sincronização não a recriar.
UPDATE skill_source_entries SET selection = 'excluded', skill_id = NULL, updated_at = @now
WHERE company_id = @company_id AND skill_id = @skill_id;

-- name: CreateSourceSkill :one
INSERT INTO skills (id, company_id, slug, name, description, body, version, created_by_type, created_by_id,
                    updated_by_type, updated_by_id, created_at, updated_at, source_id, source_path, source_commit)
VALUES (@id, @company_id, @slug, @name, @description, @body, 1, 'system', NULL, 'system', NULL, @now, @now,
        @source_id, @source_path, @source_commit)
RETURNING *;

-- name: UpdateSourceSkill :one
UPDATE skills
SET name = @name, description = @description, body = @body, version = @version, source_commit = @source_commit,
    source_modified = false, updated_by_type = 'system', updated_by_id = NULL, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: TouchSourceSkillCommit :exec
UPDATE skills SET source_commit = @source_commit WHERE company_id = @company_id AND id = @id;

-- name: DetachSkillFromSource :exec
UPDATE skills SET source_id = NULL, source_path = '', source_modified = false
WHERE company_id = @company_id AND id = @id;

-- name: DetachSourceSkills :execrows
UPDATE skills SET source_id = NULL, source_path = '', source_modified = false
WHERE company_id = @company_id AND source_id = @source_id;

-- name: DeleteSourceSkills :many
DELETE FROM skills WHERE company_id = @company_id AND source_id = @source_id RETURNING id, slug;

-- name: GetSkillSourceName :one
SELECT url, tracking_ref FROM skill_sources WHERE company_id = @company_id AND id = @id;

-- ---------- estúdio ----------

-- name: CreateSkillTestInput :one
INSERT INTO skill_test_inputs (id, company_id, skill_id, name, content, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @skill_id, @name, @content, @actor_type, @actor_id, @now, @now)
RETURNING *;

-- name: ListSkillTestInputs :many
SELECT * FROM skill_test_inputs WHERE company_id = @company_id AND skill_id = @skill_id ORDER BY name, id;

-- name: CountSkillTestInputs :one
SELECT count(*)::bigint FROM skill_test_inputs WHERE company_id = @company_id AND skill_id = @skill_id;

-- name: GetSkillTestInput :one
SELECT * FROM skill_test_inputs WHERE company_id = @company_id AND skill_id = @skill_id AND id = @id;

-- name: UpdateSkillTestInput :one
UPDATE skill_test_inputs SET name = @name, content = @content, updated_at = @now
WHERE company_id = @company_id AND skill_id = @skill_id AND id = @id
RETURNING *;

-- name: DeleteSkillTestInput :execrows
DELETE FROM skill_test_inputs WHERE company_id = @company_id AND skill_id = @skill_id AND id = @id;

-- name: CreateSkillTestRun :one
INSERT INTO skill_test_runs (id, company_id, skill_id, skill_revision, input_id, input, persona_id, status,
                             created_by_type, created_by_id, created_at)
VALUES (@id, @company_id, @skill_id, @skill_revision, sqlc.narg(input_id), @input, @persona_id, 'queued',
        @actor_type, @actor_id, @now)
RETURNING *;

-- name: SetSkillTestRunTask :exec
UPDATE skill_test_runs SET task_id = @task_id WHERE id = @id;

-- name: ListSkillTestRuns :many
SELECT t.*, k.slug AS skill_slug, p.slug AS persona_slug, p.name AS persona_name,
       COALESCE(r.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS live_run_id,
       COALESCE(r.status, '')::text AS live_run_status, COALESCE(r.name, '')::text AS live_run_name,
       r.cost_usd AS run_cost_usd
FROM skill_test_runs t
JOIN skills k ON k.id = t.skill_id
JOIN personas p ON p.id = t.persona_id
LEFT JOIN LATERAL (SELECT ru.id, ru.status, ru.name, ru.cost_usd FROM runs ru
                   WHERE ru.task_id = t.task_id ORDER BY ru.created_at DESC, ru.id DESC LIMIT 1) r ON true
WHERE t.company_id = @company_id AND (sqlc.narg(skill_id)::uuid IS NULL OR t.skill_id = sqlc.narg(skill_id))
ORDER BY t.created_at DESC, t.id DESC
LIMIT @lim;

-- name: GetSkillTestRun :one
SELECT t.*, k.slug AS skill_slug, p.slug AS persona_slug, p.name AS persona_name,
       COALESCE(r.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS live_run_id,
       COALESCE(r.status, '')::text AS live_run_status, COALESCE(r.name, '')::text AS live_run_name,
       r.cost_usd AS run_cost_usd
FROM skill_test_runs t
JOIN skills k ON k.id = t.skill_id
JOIN personas p ON p.id = t.persona_id
LEFT JOIN LATERAL (SELECT ru.id, ru.status, ru.name, ru.cost_usd FROM runs ru
                   WHERE ru.task_id = t.task_id ORDER BY ru.created_at DESC, ru.id DESC LIMIT 1) r ON true
WHERE t.company_id = @company_id AND t.id = @id;

-- name: GetSkillTestRunForUpdate :one
SELECT * FROM skill_test_runs WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetSkillTestRunByTaskForUpdate :one
SELECT * FROM skill_test_runs WHERE task_id = @task_id FOR UPDATE;

-- name: GetSkillTestRunByTask :one
SELECT * FROM skill_test_runs WHERE company_id = @company_id AND task_id = @task_id;

-- name: IsSkillTestTask :one
SELECT EXISTS (SELECT 1 FROM skill_test_runs WHERE task_id = @task_id)::boolean;

-- name: FinishSkillTestRun :one
UPDATE skill_test_runs SET status = @status, run_id = COALESCE(sqlc.narg(run_id), run_id), output = @output, error = @error,
    finished_at = @now
WHERE id = @id AND status IN ('queued','running')
RETURNING *;

-- name: ListStaleSkillTestRuns :many
-- Testes na fila há tempo demais sem nenhuma run ativa (a persona não pôde trabalhar, orçamento estourado).
SELECT t.* FROM skill_test_runs t
WHERE t.status IN ('queued','running') AND t.created_at < @before
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.task_id = t.task_id AND r.status IN ('queued','running'))
ORDER BY t.created_at
LIMIT @lim;
