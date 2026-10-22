-- Trilha contínua, onda 3 (ADR-0061): perfil, favoritos e recentes, itens dispensados, onboarding.

-- name: GetUserProfile :one
SELECT * FROM user_profiles WHERE user_id = @user_id;

-- name: UpsertUserProfile :one
INSERT INTO user_profiles (user_id, avatar_glyph, avatar_tone, timezone, locale, updated_at)
VALUES (@user_id, @avatar_glyph, @avatar_tone, @timezone, @locale, @now)
ON CONFLICT (user_id) DO UPDATE SET avatar_glyph = EXCLUDED.avatar_glyph, avatar_tone = EXCLUDED.avatar_tone,
    timezone = EXCLUDED.timezone, locale = EXCLUDED.locale, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: SetUserName :one
UPDATE users SET name = @name WHERE id = @id RETURNING *;

-- name: ListNavItems :many
SELECT * FROM nav_items WHERE company_id = @company_id AND subject = @subject ORDER BY kind, touched_at DESC, path;

-- name: UpsertNavItem :exec
INSERT INTO nav_items (company_id, subject, kind, path, title, touched_at)
VALUES (@company_id, @subject, @kind, @path, @title, @now)
ON CONFLICT (company_id, subject, kind, path) DO UPDATE SET title = EXCLUDED.title, touched_at = EXCLUDED.touched_at;

-- name: DeleteNavItem :execrows
DELETE FROM nav_items WHERE company_id = @company_id AND subject = @subject AND kind = @kind AND path = @path;

-- name: CountNavItems :one
SELECT count(*) FROM nav_items WHERE company_id = @company_id AND subject = @subject AND kind = @kind;

-- name: PruneRecentNavItems :exec
-- Mantém só os @keep recentes mais novos da pessoa na empresa.
DELETE FROM nav_items n
WHERE n.company_id = @company_id AND n.subject = @subject AND n.kind = 'recent'
  AND (n.company_id, n.subject, n.kind, n.path) NOT IN (
      SELECT r.company_id, r.subject, r.kind, r.path FROM nav_items r
      WHERE r.company_id = @company_id AND r.subject = @subject AND r.kind = 'recent'
      ORDER BY r.touched_at DESC, r.path LIMIT @keep::int);

-- name: DeleteNavItemsForSubject :exec
DELETE FROM nav_items WHERE subject = @subject;

-- name: GetDismissal :one
SELECT value FROM user_dismissals WHERE subject = @subject AND key = @key;

-- name: UpsertDismissal :exec
INSERT INTO user_dismissals (subject, key, value, updated_at) VALUES (@subject, @key, @value, @now)
ON CONFLICT (subject, key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at;

-- name: DeleteDismissalsForSubject :exec
DELETE FROM user_dismissals WHERE subject = @subject;

-- name: GetOnboardingState :one
SELECT * FROM onboarding_state WHERE company_id = @company_id;

-- name: SaveOnboardingHarness :one
INSERT INTO onboarding_state (company_id, harness_agent, harness_model, harness_status, harness_reason, harness_steps, harness_checked_at, updated_at)
VALUES (@company_id, @agent, @model, @status, @reason, @steps, @now, @now)
ON CONFLICT (company_id) DO UPDATE SET harness_agent = EXCLUDED.harness_agent, harness_model = EXCLUDED.harness_model,
    harness_status = EXCLUDED.harness_status, harness_reason = EXCLUDED.harness_reason, harness_steps = EXCLUDED.harness_steps,
    harness_checked_at = EXCLUDED.harness_checked_at, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: SetOnboardingSkipped :one
INSERT INTO onboarding_state (company_id, skipped, updated_at) VALUES (@company_id, @skipped::text[], @now)
ON CONFLICT (company_id) DO UPDATE SET skipped = EXCLUDED.skipped, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: SetOnboardingDismissed :one
INSERT INTO onboarding_state (company_id, dismissed_at, updated_at) VALUES (@company_id, sqlc.narg(dismissed_at), @now)
ON CONFLICT (company_id) DO UPDATE SET dismissed_at = EXCLUDED.dismissed_at, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: SetOnboardingFirstTask :one
INSERT INTO onboarding_state (company_id, first_task_id, updated_at) VALUES (@company_id, @task_id, @now)
ON CONFLICT (company_id) DO UPDATE SET first_task_id = EXCLUDED.first_task_id, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: CountCompanyPersonas :one
SELECT count(*) FROM personas WHERE company_id = @company_id AND status <> 'off';

-- name: CompanyHasRun :one
SELECT EXISTS (SELECT 1 FROM runs WHERE company_id = @company_id) AS has_run;
