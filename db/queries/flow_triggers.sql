-- name: CreateFlowTrigger :one
INSERT INTO flow_triggers (id, company_id, flow_id, kind, name, enabled, secret_ref, rate_per_min, event_verb, event_filter,
                           mapping, created_at, updated_at)
VALUES (@id, @company_id, @flow_id, @kind, @name, @enabled, @secret_ref, @rate_per_min, @event_verb, @event_filter,
        @mapping, @now, @now)
RETURNING *;

-- name: GetFlowTrigger :one
SELECT * FROM flow_triggers WHERE company_id = @company_id AND flow_id = @flow_id AND id = @id;

-- name: GetFlowTriggerByID :one
-- Entrada pública do webhook: o gatilho é achado só pelo id da URL.
SELECT * FROM flow_triggers WHERE id = @id;

-- name: ListFlowTriggers :many
SELECT * FROM flow_triggers WHERE company_id = @company_id AND flow_id = @flow_id ORDER BY created_at, id;

-- name: ListEventTriggers :many
-- Gatilhos de evento ligados de fluxos ativos da empresa (avaliados a cada fato, na mesma transação).
SELECT t.* FROM flow_triggers t
JOIN flows f ON f.id = t.flow_id
WHERE t.company_id = @company_id AND t.kind = 'event' AND t.enabled AND f.enabled AND f.archived_at IS NULL
ORDER BY t.created_at, t.id;

-- name: UpdateFlowTrigger :one
UPDATE flow_triggers
SET name = @name, enabled = @enabled, rate_per_min = @rate_per_min, event_verb = @event_verb, event_filter = @event_filter,
    mapping = @mapping, updated_at = @now
WHERE company_id = @company_id AND flow_id = @flow_id AND id = @id
RETURNING *;

-- name: DeleteFlowTrigger :execrows
DELETE FROM flow_triggers WHERE company_id = @company_id AND flow_id = @flow_id AND id = @id;

-- name: MarkFlowTriggerFired :exec
UPDATE flow_triggers SET last_fired_at = @at WHERE id = @id;

-- name: InsertFlowRevision :one
INSERT INTO flow_revisions (id, company_id, flow_id, revision, snapshot, summary, actor_type, actor_id, origin, restored_from, created_at)
VALUES (@id, @company_id, @flow_id, @revision, @snapshot, @summary, @actor_type, @actor_id, @origin, @restored_from, @now)
RETURNING *;

-- name: ListFlowRevisions :many
SELECT * FROM flow_revisions WHERE company_id = @company_id AND flow_id = @flow_id ORDER BY revision DESC LIMIT @lim;

-- name: GetFlowRevision :one
SELECT * FROM flow_revisions WHERE company_id = @company_id AND flow_id = @flow_id AND revision = @revision;

-- name: ListFlowTriggerSecretRefs :many
SELECT t.id, t.name, t.secret_ref, f.slug AS flow_slug FROM flow_triggers t JOIN flows f ON f.id = t.flow_id
WHERE t.company_id = @company_id AND t.secret_ref <> '';
