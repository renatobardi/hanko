-- Readiness da persona e catálogo de modelos (ADR-0055).

-- name: UpsertPersonaReadiness :one
INSERT INTO persona_readiness (persona_id, company_id, status, agent, model, environment_id, environment_source, steps, reason,
                               trigger, duration_ms, checked_at, failing_since)
VALUES (@persona_id, @company_id, @status, @agent, @model, sqlc.narg(environment_id), @environment_source, @steps, @reason,
        @trigger, @duration_ms, @now, CASE WHEN @status::text = 'not_ready' THEN @now::timestamptz END)
ON CONFLICT (persona_id) DO UPDATE SET
    status = EXCLUDED.status, agent = EXCLUDED.agent, model = EXCLUDED.model, environment_id = EXCLUDED.environment_id,
    environment_source = EXCLUDED.environment_source, steps = EXCLUDED.steps, reason = EXCLUDED.reason,
    trigger = EXCLUDED.trigger, duration_ms = EXCLUDED.duration_ms, checked_at = EXCLUDED.checked_at,
    failing_since = CASE
        WHEN EXCLUDED.status = 'ready' THEN NULL
        ELSE COALESCE(persona_readiness.failing_since, EXCLUDED.checked_at)
    END
RETURNING *;

-- name: GetPersonaReadiness :one
SELECT * FROM persona_readiness WHERE company_id = @company_id AND persona_id = @persona_id;

-- name: ListPersonaReadiness :many
SELECT * FROM persona_readiness WHERE company_id = @company_id ORDER BY persona_id;

-- name: DeletePersonaReadiness :exec
DELETE FROM persona_readiness WHERE company_id = @company_id AND persona_id = @persona_id;

-- name: DeletePersonaReadinessByEnvironment :exec
-- Mudou a configuração (ou o arquivamento) do ambiente: os testes feitos nele não valem mais.
DELETE FROM persona_readiness WHERE company_id = @company_id AND environment_id = @environment_id;

-- name: DeletePersonaReadinessByDefault :exec
-- Mudou o ambiente padrão da empresa: valem de novo os testes das personas que dependiam dele (padrão ou local).
DELETE FROM persona_readiness WHERE company_id = @company_id AND environment_source IN ('default','local');

-- name: ListPersonasDueReadiness :many
-- Personas em condição de trabalhar sem teste ou com teste mais velho que @older_than (as nunca testadas primeiro).
SELECT p.id, p.company_id FROM personas p
LEFT JOIN persona_readiness r ON r.persona_id = p.id
WHERE p.status IN ('active','idle','running','awaiting_gate','error')
  AND (r.persona_id IS NULL OR r.checked_at <= @older_than::timestamptz OR r.agent <> p.agent OR r.model <> p.model)
ORDER BY (r.persona_id IS NULL) DESC, r.checked_at NULLS FIRST, p.id
LIMIT @lim;

-- name: ListNotReadyPersonas :many
-- Para o alerta persona_not_ready: persona que ainda existe e cujo último teste falhou com o mesmo harness e modelo
-- de hoje (um teste de um harness antigo não vale).
SELECT p.id, p.company_id, p.slug, p.name, r.reason, r.failing_since, r.checked_at, r.agent
FROM persona_readiness r
JOIN personas p ON p.id = r.persona_id
WHERE r.status = 'not_ready' AND r.agent = p.agent AND r.model = p.model
  AND p.status NOT IN ('off','paused')
ORDER BY p.company_id, p.id;

-- name: UpsertModelReport :exec
INSERT INTO model_reports (company_id, agent, model, source, last_seen_at)
VALUES (@company_id, @agent, @model, @source, @now)
ON CONFLICT (company_id, agent, model) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at,
    source = CASE WHEN model_reports.source = 'probe' THEN 'probe' ELSE EXCLUDED.source END;

-- name: ListModelReports :many
SELECT agent, model, source, last_seen_at FROM model_reports WHERE company_id = @company_id ORDER BY agent, model;

-- name: ListRunModels :many
-- Modelos que as runs da empresa realmente usaram (o harness informou) nos últimos 90 dias.
SELECT agent, model, max(created_at)::timestamptz AS last_seen_at FROM runs
WHERE company_id = @company_id AND model <> '' AND created_at >= @since::timestamptz
GROUP BY agent, model
ORDER BY agent, model;
