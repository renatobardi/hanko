-- name: CreateIntegration :one
INSERT INTO integrations (id, company_id, kind, slug, name, enabled, all_personas, config, events, secret_enc,
                          secret_hint, created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @kind, @slug, @name, @enabled, @all_personas, @config, @events, @secret_enc,
        @secret_hint, @actor_type, @actor_id, @now, @now)
RETURNING *;

-- name: GetIntegrationByID :one
SELECT * FROM integrations WHERE company_id = @company_id AND id = @id;

-- name: GetIntegrationByIDForUpdate :one
SELECT * FROM integrations WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetIntegrationBySlug :one
SELECT * FROM integrations WHERE company_id = @company_id AND slug = @slug;

-- name: GetIntegrationAny :one
-- Para o worker de entrega, que só conhece o id.
SELECT * FROM integrations WHERE id = @id;

-- name: ListIntegrations :many
SELECT * FROM integrations WHERE company_id = @company_id ORDER BY kind, name, id;

-- name: UpdateIntegration :one
UPDATE integrations
SET name = @name, enabled = @enabled, all_personas = @all_personas, config = @config, events = @events,
    secret_enc = @secret_enc, secret_hint = @secret_hint, updated_at = @now
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: DeleteIntegration :execrows
DELETE FROM integrations WHERE company_id = @company_id AND id = @id;

-- name: ListAllSecretIntegrations :many
-- Para a rotação de chave: integrações com segredo guardado.
SELECT id, company_id, secret_enc FROM integrations WHERE secret_enc <> '' ORDER BY id;

-- name: SetIntegrationSecretEnc :exec
UPDATE integrations SET secret_enc = @secret_enc WHERE id = @id;

-- name: SetIntegrationPersona :exec
INSERT INTO integration_personas (company_id, integration_id, persona_id)
VALUES (@company_id, @integration_id, @persona_id)
ON CONFLICT DO NOTHING;

-- name: DeleteIntegrationPersona :exec
DELETE FROM integration_personas WHERE integration_id = @integration_id AND persona_id = @persona_id;

-- name: ClearIntegrationPersonas :exec
DELETE FROM integration_personas WHERE integration_id = @integration_id;

-- name: ListIntegrationPersonaLinks :many
-- Todas as personas da empresa com o estado de uma integração MCP.
SELECT p.id AS persona_id, p.slug, p.name, (ip.persona_id IS NOT NULL)::boolean AS enabled
FROM personas p
LEFT JOIN integration_personas ip ON ip.persona_id = p.id AND ip.integration_id = @integration_id
WHERE p.company_id = @company_id
ORDER BY p.name, p.id;

-- name: ListPersonaIntegrationLinks :many
-- Todos os servidores MCP da empresa com o estado para uma persona (vale para todas ou ligado só a ela).
SELECT i.id AS integration_id, i.slug, i.name, i.enabled AS integration_enabled, i.all_personas,
       (ip.persona_id IS NOT NULL)::boolean AS linked
FROM integrations i
LEFT JOIN integration_personas ip ON ip.integration_id = i.id AND ip.persona_id = @persona_id
WHERE i.company_id = @company_id AND i.kind = 'mcp'
ORDER BY i.name, i.id;

-- name: ListMCPIntegrationsForPersona :many
-- Servidores MCP ligados que uma persona usa: os de "todas" e os ligados a ela.
SELECT i.* FROM integrations i
WHERE i.company_id = @company_id AND i.kind = 'mcp' AND i.enabled
  AND (i.all_personas OR EXISTS (SELECT 1 FROM integration_personas ip WHERE ip.integration_id = i.id AND ip.persona_id = @persona_id))
ORDER BY i.slug, i.id;

-- name: ListWebhooksForEvent :many
SELECT * FROM integrations
WHERE company_id = @company_id AND kind = 'webhook' AND enabled AND @event::text = ANY(events)
ORDER BY id;

-- name: CreateWebhookDelivery :one
INSERT INTO webhook_deliveries (id, company_id, integration_id, event, activity_id, test, payload, status, created_at)
VALUES (@id, @company_id, @integration_id, @event, @activity_id, @test, @payload, 'pending', @now)
ON CONFLICT (integration_id, activity_id) WHERE activity_id IS NOT NULL DO NOTHING
RETURNING *;

-- name: GetWebhookDelivery :one
SELECT * FROM webhook_deliveries WHERE id = @id;

-- name: RecordWebhookAttempt :one
UPDATE webhook_deliveries
SET status = @status, attempts = attempts + 1, http_status = @http_status, latency_ms = @latency_ms,
    error = @error, next_attempt_at = @next_attempt_at, delivered_at = @delivered_at
WHERE id = @id
RETURNING *;

-- name: ListWebhookDeliveries :many
SELECT * FROM webhook_deliveries
WHERE company_id = @company_id AND integration_id = @integration_id
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: PruneWebhookDeliveries :exec
-- Mantém só as últimas @keep entregas da integração.
DELETE FROM webhook_deliveries d
WHERE d.integration_id = @integration_id
  AND d.id NOT IN (
    SELECT x.id FROM webhook_deliveries x WHERE x.integration_id = @integration_id
    ORDER BY x.created_at DESC, x.id DESC LIMIT @keep);

-- name: LastWebhookDeliveryByIntegration :many
-- Última entrega de cada webhook da empresa (para a listagem).
SELECT DISTINCT ON (integration_id) integration_id, status, http_status, created_at
FROM webhook_deliveries
WHERE company_id = @company_id
ORDER BY integration_id, created_at DESC, id DESC;
