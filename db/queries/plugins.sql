-- Plugins de terceiros (ADR-0038).

-- name: InsertPlugin :one
INSERT INTO plugins (id, plugin_key, version, api_version, manifest, dir, status, granted_capabilities, events,
                     installed_by, installed_at, updated_at)
VALUES (@id, @plugin_key, @version, @api_version, @manifest, @dir, 'installed', @granted_capabilities, @events,
        @installed_by, @now, @now)
RETURNING *;

-- name: ReinstallPlugin :one
UPDATE plugins
SET version = @version, api_version = @api_version, manifest = @manifest, dir = @dir, status = 'installed',
    granted_capabilities = @granted_capabilities, pending_capabilities = '{}', pending_manifest = NULL,
    events = @events, last_error = '', installed_by = @installed_by, installed_at = @now, updated_at = @now
WHERE id = @id AND status = 'uninstalled'
RETURNING *;

-- name: GetPluginByKey :one
SELECT * FROM plugins WHERE plugin_key = @plugin_key;

-- name: GetPluginByID :one
SELECT * FROM plugins WHERE id = @id;

-- name: GetPluginForUpdate :one
SELECT * FROM plugins WHERE id = @id FOR UPDATE;

-- name: ListPlugins :many
SELECT * FROM plugins ORDER BY plugin_key;

-- name: SetPluginStatus :one
UPDATE plugins SET status = @status, last_error = @last_error, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: SetPluginError :exec
-- Registra o último erro do processo sem mudar o estado (queda com reinício em andamento).
UPDATE plugins SET last_error = @last_error, updated_at = @now WHERE id = @id;

-- name: ApplyPluginManifest :one
-- Atualização aplicada (sem capacidade nova, ou aprovada pelo dono).
UPDATE plugins
SET version = @version, api_version = @api_version, manifest = @manifest, granted_capabilities = @granted_capabilities,
    pending_capabilities = '{}', pending_manifest = NULL, events = @events, status = @status, last_error = '', updated_at = @now
WHERE id = @id
RETURNING *;

-- name: SetPluginPending :one
UPDATE plugins
SET status = 'upgrade_pending', pending_manifest = @pending_manifest, pending_capabilities = @pending_capabilities, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: CountPluginEnabledCompanies :many
-- Quantas empresas ligaram cada plugin.
SELECT plugin_id, count(*)::bigint AS companies
FROM plugin_company_settings WHERE enabled
GROUP BY plugin_id;

-- name: GetPluginSettings :one
SELECT * FROM plugin_company_settings WHERE plugin_id = @plugin_id AND company_id = @company_id;

-- name: ListPluginSettingsForCompany :many
SELECT * FROM plugin_company_settings WHERE company_id = @company_id;

-- name: UpsertPluginSettings :one
INSERT INTO plugin_company_settings (plugin_id, company_id, enabled, config, secret_enc, updated_by, created_at, updated_at)
VALUES (@plugin_id, @company_id, @enabled, @config, @secret_enc, @updated_by, @now, @now)
ON CONFLICT (plugin_id, company_id) DO UPDATE
SET enabled = EXCLUDED.enabled, config = EXCLUDED.config, secret_enc = EXCLUDED.secret_enc,
    updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: ListPluginEnabledCompanies :many
-- Empresas que ligaram o plugin.
SELECT c.id, c.slug, c.name
FROM plugin_company_settings s JOIN companies c ON c.id = s.company_id
WHERE s.plugin_id = @plugin_id AND s.enabled
ORDER BY c.slug;

-- name: DeletePluginSettings :exec
-- Desinstalar apaga a configuração (com os segredos) e o estado do plugin em todas as empresas.
DELETE FROM plugin_company_settings WHERE plugin_id = @plugin_id;

-- name: DeleteAllPluginState :exec
DELETE FROM plugin_state WHERE plugin_id = @plugin_id;

-- name: ListAllPluginSecrets :many
-- Para a rotação de chave.
SELECT plugin_id, company_id, secret_enc FROM plugin_company_settings WHERE secret_enc <> '' ORDER BY plugin_id, company_id;

-- name: SetPluginSecretEnc :exec
UPDATE plugin_company_settings SET secret_enc = @secret_enc WHERE plugin_id = @plugin_id AND company_id = @company_id;

-- name: ListPluginsForEvent :many
-- Plugins ativos, ligados na empresa e assinantes do verbo, com a capacidade events.subscribe concedida.
SELECT p.id, p.plugin_key
FROM plugins p JOIN plugin_company_settings s ON s.plugin_id = p.id
WHERE s.company_id = @company_id AND s.enabled AND p.status IN ('ready','upgrade_pending')
  AND @verb::text = ANY(p.events) AND 'events.subscribe' = ANY(p.granted_capabilities)
ORDER BY p.plugin_key;

-- name: ListActivePluginsForCompany :many
-- Plugins ativos e ligados na empresa (tools, widgets).
SELECT p.*
FROM plugins p JOIN plugin_company_settings s ON s.plugin_id = p.id
WHERE s.company_id = @company_id AND s.enabled AND p.status IN ('ready','upgrade_pending')
ORDER BY p.plugin_key;

-- name: GetActivityByID :one
SELECT * FROM activity_events WHERE company_id = @company_id AND id = @id;

-- ---------- estado ----------

-- name: GetPluginState :one
SELECT * FROM plugin_state WHERE plugin_id = @plugin_id AND company_id = @company_id AND key = @key;

-- name: UpsertPluginState :exec
INSERT INTO plugin_state (plugin_id, company_id, key, value, updated_at)
VALUES (@plugin_id, @company_id, @key, @value, @now)
ON CONFLICT (plugin_id, company_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at;

-- name: DeletePluginState :execrows
DELETE FROM plugin_state WHERE plugin_id = @plugin_id AND company_id = @company_id AND key = @key;

-- name: CountPluginState :one
SELECT count(*)::bigint FROM plugin_state WHERE plugin_id = @plugin_id AND company_id = @company_id;

-- name: ListPluginState :many
SELECT * FROM plugin_state
WHERE plugin_id = @plugin_id AND company_id = @company_id AND key LIKE @prefix::text || '%' ESCAPE '\'
ORDER BY key
LIMIT @lim;

-- name: LockPluginStateScope :exec
-- Serializa as gravações de estado de um (plugin, empresa): o teto de chaves não pode ser furado por corrida.
SELECT pg_advisory_xact_lock(hashtextextended(@plugin_id::text || ':' || @company_id::text, 38));

-- ---------- logs ----------

-- name: InsertPluginLog :exec
INSERT INTO plugin_logs (id, plugin_id, company_id, level, message, created_at)
VALUES (@id, @plugin_id, @company_id, @level, @message, @now);

-- name: PrunePluginLogs :exec
-- Mantém só as últimas @keep linhas do plugin.
DELETE FROM plugin_logs l
WHERE l.plugin_id = @plugin_id
  AND l.id NOT IN (SELECT x.id FROM plugin_logs x WHERE x.plugin_id = @plugin_id ORDER BY x.created_at DESC, x.id DESC LIMIT @keep);

-- name: ListPluginLogs :many
SELECT * FROM plugin_logs WHERE plugin_id = @plugin_id ORDER BY created_at DESC, id DESC LIMIT @lim;

-- ---------- jobs ----------

-- name: LastPluginJobRuns :many
-- Última execução de cada job do plugin na empresa.
SELECT DISTINCT ON (job_key) * FROM plugin_job_runs
WHERE plugin_id = @plugin_id AND company_id = @company_id
ORDER BY job_key, started_at DESC, id DESC;

-- name: StartPluginJobRun :one
INSERT INTO plugin_job_runs (id, plugin_id, company_id, job_key, status, started_at)
VALUES (@id, @plugin_id, @company_id, @job_key, 'running', @now)
ON CONFLICT (plugin_id, company_id, job_key) WHERE status = 'running' DO NOTHING
RETURNING *;

-- name: GetPluginJobRun :one
SELECT * FROM plugin_job_runs WHERE id = @id;

-- name: FinishPluginJobRun :execrows
UPDATE plugin_job_runs SET status = @status, finished_at = @now::timestamptz, error = @error
WHERE id = @id AND status = 'running';

-- name: ExpirePluginJobRuns :execrows
-- Execuções em andamento há mais que o teto (o processo caiu no meio): falham para liberar o próximo ciclo.
UPDATE plugin_job_runs SET status = 'failed', finished_at = @now::timestamptz, error = 'sem resposta (expirada)'
WHERE status = 'running' AND started_at < @before;

-- name: PrunePluginJobRuns :exec
DELETE FROM plugin_job_runs j
WHERE j.plugin_id = @plugin_id AND j.status <> 'running'
  AND j.id NOT IN (SELECT x.id FROM plugin_job_runs x WHERE x.plugin_id = @plugin_id ORDER BY x.started_at DESC, x.id DESC LIMIT @keep);

-- name: ListPluginJobRuns :many
SELECT * FROM plugin_job_runs WHERE plugin_id = @plugin_id AND company_id = @company_id
ORDER BY started_at DESC, id DESC LIMIT @lim;

-- name: ListPluginSettingsForPlugin :many
SELECT * FROM plugin_company_settings WHERE plugin_id = @plugin_id ORDER BY company_id;
