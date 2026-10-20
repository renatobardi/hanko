-- Apps, gateway de tools e perfis de ferramentas (ADR-0036).

-- ---------- catálogo ----------

-- name: GetToolCatalog :one
SELECT * FROM integration_tool_catalogs WHERE company_id = @company_id AND integration_id = @integration_id;

-- name: ListToolCatalogs :many
SELECT * FROM integration_tool_catalogs WHERE company_id = @company_id ORDER BY integration_id;

-- name: RecordToolCatalogSync :exec
INSERT INTO integration_tool_catalogs (integration_id, company_id, synced_at, last_error, tools_count, updated_at)
VALUES (@integration_id, @company_id, @now, '', @tools_count, @now)
ON CONFLICT (integration_id) DO UPDATE
SET synced_at = @now, last_error = '', tools_count = @tools_count, updated_at = @now;

-- name: RecordToolCatalogError :exec
-- A falha não apaga a data da última descoberta boa: o catálogo antigo segue valendo.
INSERT INTO integration_tool_catalogs (integration_id, company_id, last_error, updated_at)
VALUES (@integration_id, @company_id, @last_error, @now)
ON CONFLICT (integration_id) DO UPDATE SET last_error = @last_error, updated_at = @now;

-- name: LockToolCatalog :exec
-- Serializa descobertas concorrentes da mesma integração (a transação segura o lock).
SELECT pg_advisory_xact_lock(hashtextextended('tool_catalog:' || @integration_id::text, 0));

-- name: ListIntegrationTools :many
SELECT * FROM integration_tools WHERE company_id = @company_id AND integration_id = @integration_id ORDER BY name, id;

-- name: ListCompanyTools :many
SELECT t.*, i.slug AS integration_slug
FROM integration_tools t JOIN integrations i ON i.id = t.integration_id
WHERE t.company_id = @company_id
ORDER BY i.slug, t.name, t.id;

-- name: GetIntegrationToolByName :one
SELECT * FROM integration_tools WHERE company_id = @company_id AND integration_id = @integration_id AND name = @name;

-- name: GetIntegrationToolByNameForUpdate :one
SELECT * FROM integration_tools WHERE company_id = @company_id AND integration_id = @integration_id AND name = @name FOR UPDATE;

-- name: InsertIntegrationTool :one
INSERT INTO integration_tools (id, company_id, integration_id, name, title, description, input_schema, annotations,
                               schema_sha256, risk, status, first_seen_at, last_seen_at, updated_at)
VALUES (@id, @company_id, @integration_id, @name, @title, @description, @input_schema, @annotations,
        @schema_sha256, @risk, @status, @now, @now, @now)
RETURNING *;

-- name: UpdateIntegrationToolSeen :one
UPDATE integration_tools
SET title = @title, description = @description, input_schema = @input_schema, annotations = @annotations,
    schema_sha256 = @schema_sha256, risk = @risk, status = @status, last_seen_at = @now, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: SetIntegrationToolStatus :one
UPDATE integration_tools
SET status = @status, reviewed_by_type = @reviewed_by_type, reviewed_by_id = @reviewed_by_id, reviewed_at = @now, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: MarkMissingToolsRemoved :many
-- As tools que o servidor deixou de expor viram removed (a linha fica, para a auditoria e a revisão).
UPDATE integration_tools
SET status = 'removed', updated_at = @now
WHERE integration_id = @integration_id AND status <> 'removed' AND NOT (name = ANY(@names::text[]))
RETURNING *;

-- ---------- perfis ----------

-- name: CreateToolProfile :one
INSERT INTO tool_profiles (id, company_id, slug, name, description, default_action, rate_limit_per_min,
                           created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description, @default_action, @rate_limit_per_min,
        @actor_type, @actor_id, @now, @now)
RETURNING *;

-- name: GetToolProfileByID :one
SELECT * FROM tool_profiles WHERE company_id = @company_id AND id = @id;

-- name: GetToolProfileByIDForUpdate :one
SELECT * FROM tool_profiles WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetToolProfileBySlug :one
SELECT * FROM tool_profiles WHERE company_id = @company_id AND slug = @slug;

-- name: ListToolProfiles :many
SELECT * FROM tool_profiles WHERE company_id = @company_id ORDER BY name, id;

-- name: CountToolProfiles :one
SELECT count(*)::bigint FROM tool_profiles WHERE company_id = @company_id;

-- name: UpdateToolProfile :one
UPDATE tool_profiles
SET name = @name, description = @description, default_action = @default_action,
    rate_limit_per_min = @rate_limit_per_min, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: TouchToolProfile :exec
UPDATE tool_profiles SET updated_at = @now WHERE company_id = @company_id AND id = @id;

-- name: DeleteToolProfile :execrows
DELETE FROM tool_profiles WHERE company_id = @company_id AND id = @id;

-- name: ListToolProfileRules :many
SELECT r.*, COALESCE(i.slug, '')::text AS integration_slug
FROM tool_profile_rules r LEFT JOIN integrations i ON i.id = r.integration_id
WHERE r.company_id = @company_id AND r.profile_id = @profile_id
ORDER BY r.created_at, r.id;

-- name: ListCompanyToolRules :many
SELECT r.*, COALESCE(i.slug, '')::text AS integration_slug
FROM tool_profile_rules r LEFT JOIN integrations i ON i.id = r.integration_id
WHERE r.company_id = @company_id
ORDER BY r.profile_id, r.created_at, r.id;

-- name: UpsertToolProfileRule :one
INSERT INTO tool_profile_rules (id, company_id, profile_id, target, integration_id, tool_name, risk, effect, mode, created_at)
VALUES (@id, @company_id, @profile_id, @target, @integration_id, @tool_name, @risk, @effect, @mode, @now)
ON CONFLICT (profile_id, target, COALESCE(integration_id, '00000000-0000-0000-0000-000000000000'::uuid), tool_name, risk)
DO UPDATE SET effect = EXCLUDED.effect, mode = EXCLUDED.mode
RETURNING *;

-- name: DeleteToolProfileRule :execrows
DELETE FROM tool_profile_rules WHERE company_id = @company_id AND profile_id = @profile_id AND id = @id;

-- name: DeleteToolProfileRulesForIntegration :execrows
-- O assistente reescreve as regras de uma integração de uma vez (inteira e por tool); as de risco ficam.
DELETE FROM tool_profile_rules
WHERE company_id = @company_id AND profile_id = @profile_id AND integration_id = @integration_id AND target IN ('integration','tool');

-- name: CountToolProfileRules :one
SELECT count(*)::bigint FROM tool_profile_rules WHERE profile_id = @profile_id;

-- ---------- vínculos ----------

-- name: UpsertToolBinding :one
INSERT INTO tool_profile_bindings (id, company_id, profile_id, scope, project_id, persona_id, priority, created_at)
VALUES (@id, @company_id, @profile_id, @scope, @project_id, @persona_id, @priority, @now)
ON CONFLICT (profile_id, scope, COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid),
             COALESCE(persona_id, '00000000-0000-0000-0000-000000000000'::uuid))
DO UPDATE SET priority = EXCLUDED.priority
RETURNING *;

-- name: GetToolBinding :one
SELECT * FROM tool_profile_bindings WHERE company_id = @company_id AND profile_id = @profile_id AND id = @id;

-- name: DeleteToolBinding :execrows
DELETE FROM tool_profile_bindings WHERE company_id = @company_id AND profile_id = @profile_id AND id = @id;

-- name: ListToolBindings :many
SELECT b.*, p.slug AS profile_slug, COALESCE(pr.name, pe.name, '')::text AS target_name
FROM tool_profile_bindings b
JOIN tool_profiles p ON p.id = b.profile_id
LEFT JOIN projects pr ON pr.id = b.project_id
LEFT JOIN personas pe ON pe.id = b.persona_id
WHERE b.company_id = @company_id
ORDER BY b.profile_id, b.scope, b.priority DESC, b.created_at DESC, b.id;

-- name: ListToolBindingsFor :many
-- Os vínculos que podem valer para uma persona (num projeto, quando há): os da empresa, os do projeto e os dela.
SELECT b.*, p.slug AS profile_slug, COALESCE(pr.name, pe.name, '')::text AS target_name
FROM tool_profile_bindings b
JOIN tool_profiles p ON p.id = b.profile_id
LEFT JOIN projects pr ON pr.id = b.project_id
LEFT JOIN personas pe ON pe.id = b.persona_id
WHERE b.company_id = @company_id
  AND (b.scope = 'company'
       OR (b.scope = 'project' AND sqlc.narg(project_id)::uuid IS NOT NULL AND b.project_id = sqlc.narg(project_id)::uuid)
       OR (b.scope = 'persona' AND b.persona_id = @persona_id));

-- ---------- gateways nomeados ----------

-- name: CreateToolGateway :one
INSERT INTO tool_gateways (id, company_id, slug, name, profile_id, enabled, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @profile_id, @enabled, @actor_type, @actor_id, @now, @now)
RETURNING *;

-- name: GetToolGatewayByID :one
SELECT * FROM tool_gateways WHERE company_id = @company_id AND id = @id;

-- name: GetToolGatewayBySlug :one
SELECT * FROM tool_gateways WHERE company_id = @company_id AND slug = @slug;

-- name: ListToolGateways :many
SELECT g.*, p.slug AS profile_slug, p.name AS profile_name,
       COALESCE(t.active_tokens, 0)::bigint AS active_tokens, t.last_used_at
FROM tool_gateways g
JOIN tool_profiles p ON p.id = g.profile_id
LEFT JOIN (
    SELECT x.gateway_id, max(x.last_used_at) AS last_used_at,
           count(*) FILTER (WHERE x.revoked_at IS NULL AND x.expires_at > @now) AS active_tokens
    FROM tool_gateway_tokens x GROUP BY x.gateway_id
) t ON t.gateway_id = g.id
WHERE g.company_id = @company_id
ORDER BY g.name, g.id;

-- name: CountToolGateways :one
SELECT count(*)::bigint FROM tool_gateways WHERE company_id = @company_id;

-- name: CountToolGatewaysForProfile :one
SELECT count(*)::bigint FROM tool_gateways WHERE profile_id = @profile_id;

-- name: UpdateToolGateway :one
UPDATE tool_gateways SET name = @name, profile_id = @profile_id, enabled = @enabled, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteToolGateway :execrows
DELETE FROM tool_gateways WHERE company_id = @company_id AND id = @id;

-- name: CreateGatewayToken :one
INSERT INTO tool_gateway_tokens (id, company_id, gateway_id, label, prefix, token_hash, actions, expires_at,
                                 created_by_type, created_by_id, created_at)
VALUES (@id, @company_id, @gateway_id, @label, @prefix, @token_hash, @actions, @expires_at, @actor_type, @actor_id, @now)
RETURNING *;

-- name: ListGatewayTokens :many
SELECT * FROM tool_gateway_tokens WHERE company_id = @company_id AND gateway_id = @gateway_id ORDER BY created_at DESC, id DESC;

-- name: CountActiveGatewayTokens :one
SELECT count(*)::bigint FROM tool_gateway_tokens WHERE gateway_id = @gateway_id AND revoked_at IS NULL AND expires_at > @now;

-- name: GetGatewayTokenByHash :one
SELECT * FROM tool_gateway_tokens WHERE token_hash = @token_hash;

-- name: RevokeGatewayToken :one
UPDATE tool_gateway_tokens SET revoked_at = COALESCE(revoked_at, @now)
WHERE company_id = @company_id AND gateway_id = @gateway_id AND id = @id
RETURNING *;

-- name: TouchGatewayToken :exec
-- last_used_at com resolução de um minuto: não reescreve a linha a cada chamada.
UPDATE tool_gateway_tokens SET last_used_at = @now
WHERE id = @id AND (last_used_at IS NULL OR last_used_at < @stale_before);

-- name: GetCompanyBySlugForGateway :one
SELECT id, slug FROM companies WHERE slug = @slug;

-- ---------- aprovações (gate tool) ----------

-- name: CreateToolCallApproval :one
INSERT INTO tool_call_approvals (id, company_id, gate_id, integration_id, native_tool, tool, args_sha256, run_id, task_id, persona_id, gateway_id, created_at)
VALUES (@id, @company_id, @gate_id, @integration_id, @native_tool, @tool, @args_sha256, @run_id, @task_id, @persona_id, @gateway_id, @now)
RETURNING *;

-- name: FindToolCallApprovals :many
-- Os pedidos recentes da mesma chamada (mesma tool, mesmo sha256, mesmo chamador), do mais novo ao mais velho,
-- com o estado do gate.
SELECT a.*, g.status AS gate_status, g.decided_at AS gate_decided_at
FROM tool_call_approvals a JOIN gates g ON g.id = a.gate_id
WHERE a.company_id = @company_id AND a.integration_id = @integration_id AND a.tool = @tool AND a.args_sha256 = @args_sha256
  AND (sqlc.narg(persona_id)::uuid IS NULL OR a.persona_id = sqlc.narg(persona_id)::uuid)
  AND (sqlc.narg(gateway_id)::uuid IS NULL OR a.gateway_id = sqlc.narg(gateway_id)::uuid)
  AND a.created_at >= @since
ORDER BY a.created_at DESC, a.id DESC
LIMIT 10;

-- name: ConsumeToolCallApproval :execrows
UPDATE tool_call_approvals SET consumed_at = @now WHERE id = @id AND consumed_at IS NULL;

-- name: GetToolCallApprovalByGate :one
SELECT * FROM tool_call_approvals WHERE company_id = @company_id AND gate_id = @gate_id;

-- ---------- auditoria ----------

-- name: InsertToolInvocation :one
INSERT INTO tool_invocations (id, company_id, run_id, gateway_id, persona_id, integration_id, integration_slug, tool,
                              args_sha256, args_bytes, decision, gate_id, status, latency_ms, error, source, actor_type, actor_id,
                              result_preview, policy_source, created_at)
VALUES (@id, @company_id, @run_id, @gateway_id, @persona_id, @integration_id, @integration_slug, @tool,
        @args_sha256, @args_bytes, @decision, @gate_id, @status, @latency_ms, @error, @source, @actor_type, @actor_id,
        @result_preview, @policy_source, @now)
RETURNING *;

-- name: ListToolInvocations :many
SELECT v.*, COALESCE(p.name, '')::text AS persona_name, COALESCE(g.slug, '')::text AS gateway_slug
FROM tool_invocations v
LEFT JOIN personas p ON p.id = v.persona_id
LEFT JOIN tool_gateways g ON g.id = v.gateway_id
WHERE v.company_id = @company_id
  AND (sqlc.narg(integration_id)::uuid IS NULL OR v.integration_id = sqlc.narg(integration_id)::uuid)
  AND (sqlc.narg(persona_id)::uuid IS NULL OR v.persona_id = sqlc.narg(persona_id)::uuid)
  AND (sqlc.narg(gateway_id)::uuid IS NULL OR v.gateway_id = sqlc.narg(gateway_id)::uuid)
  AND (sqlc.narg(run_id)::uuid IS NULL OR v.run_id = sqlc.narg(run_id)::uuid)
  AND (sqlc.narg(decision)::text IS NULL OR v.decision = sqlc.narg(decision)::text)
  AND (sqlc.narg(tool)::text IS NULL OR v.tool = sqlc.narg(tool)::text)
  AND (sqlc.narg(source)::text IS NULL OR v.source = sqlc.narg(source)::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR v.created_at >= sqlc.narg(since)::timestamptz)
ORDER BY v.created_at DESC, v.id DESC
LIMIT @lim OFFSET @off;

-- name: CountToolCallsSince :one
-- Chamadas repassadas (allow) no intervalo, para o teto de taxa por (persona ou gateway, integração).
SELECT count(*)::bigint FROM tool_invocations
WHERE decision = 'allow' AND integration_id = @integration_id AND created_at >= @since
  AND ((sqlc.narg(persona_id)::uuid IS NOT NULL AND persona_id = sqlc.narg(persona_id)::uuid)
       OR (sqlc.narg(gateway_id)::uuid IS NOT NULL AND gateway_id = sqlc.narg(gateway_id)::uuid));

-- name: PruneToolInvocationsBefore :execrows
DELETE FROM tool_invocations WHERE created_at < @before;

-- name: PruneToolInvocationsOverCap :execrows
-- Mantém só as @keep mais recentes de cada empresa.
DELETE FROM tool_invocations t
USING (
    SELECT id FROM (
        SELECT id, row_number() OVER (PARTITION BY company_id ORDER BY created_at DESC, id DESC) AS rn
        FROM tool_invocations
    ) x WHERE x.rn > @keep::bigint
) d
WHERE t.id = d.id;

-- name: LockToolCallKey :exec
-- Serializa pedidos de aprovação concorrentes da mesma chamada (mesmo chamador, tool e argumentos).
SELECT pg_advisory_xact_lock(hashtextextended(@key::text, 0));
