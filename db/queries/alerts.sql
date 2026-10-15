-- name: LockAlerts :exec
-- Serializa as avaliações de alertas (um job por vez, mesmo com mais de uma instância).
SELECT pg_advisory_xact_lock(hashtext('hanko:alerts'));

-- name: ListSilentPersonas :many
-- Personas que deveriam estar sendo acordadas (intervalo de heartbeat > 0, em condição de trabalhar,
-- com task trabalhável sem run ativa nem gate pendente) e cuja última atividade (criação da persona,
-- última run ou última mudança humana numa task dela) passou de @factor vezes o intervalo.
-- Persona barrada por reserva de orçamento fica de fora: o alerta de orçamento já a cobre.
SELECT p.id, p.company_id, p.slug, p.name, p.heartbeat_interval_sec, ref.at::timestamptz AS last_activity
FROM personas p
CROSS JOIN LATERAL (
    SELECT GREATEST(
        p.created_at,
        (SELECT max(COALESCE(r.finished_at, r.started_at, r.created_at)) FROM runs r WHERE r.persona_id = p.id),
        (SELECT max(t.last_human_change_at) FROM tasks t WHERE t.persona_id = p.id)
    ) AS at
) ref
WHERE p.heartbeat_interval_sec > 0
  AND p.status IN ('active', 'idle')
  AND p.budget_refused_at IS NULL
  AND ref.at + make_interval(secs => p.heartbeat_interval_sec::double precision * @factor::double precision) <= @now::timestamptz
  AND EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.persona_id = p.id AND t.status IN ('todo', 'in_progress')
        AND NOT EXISTS (SELECT 1 FROM runs a WHERE a.task_id = t.id AND a.status IN ('queued', 'running'))
        AND NOT EXISTS (SELECT 1 FROM gates g WHERE g.task_id = t.id AND g.status = 'pending')
  )
ORDER BY p.company_id, p.id;

-- name: ListStuckRuns :many
-- Runs em 'running' cujo último evento (ou, sem eventos, o início) é anterior a @cutoff.
SELECT r.id, r.company_id, r.persona_id, r.task_id, p.name AS persona_name, r.name AS run_name,
       (c.task_prefix || '-' || t.number)::text AS task_key,
       COALESCE(ev.last_at, r.started_at, r.created_at)::timestamptz AS last_event_at
FROM runs r
JOIN personas p ON p.id = r.persona_id
JOIN tasks t ON t.id = r.task_id
JOIN companies c ON c.id = r.company_id
LEFT JOIN LATERAL (SELECT max(e.received_at) AS last_at FROM run_events e WHERE e.run_id = r.id) ev ON true
WHERE r.status = 'running'
  AND COALESCE(ev.last_at, r.started_at, r.created_at) <= @cutoff::timestamptz
ORDER BY r.company_id, r.id;

-- name: ListBudgetedPersonas :many
SELECT id, company_id, slug, name, budget_monthly_cents FROM personas
WHERE budget_monthly_cents IS NOT NULL
ORDER BY company_id, id;

-- name: ListOpenAlerts :many
SELECT * FROM alerts WHERE closed_at IS NULL ORDER BY company_id, opened_at, id;

-- name: ListOpenAlertsForCompany :many
SELECT * FROM alerts WHERE company_id = @company_id AND closed_at IS NULL
ORDER BY CASE severity WHEN 'critical' THEN 0 ELSE 1 END, opened_at, id
LIMIT @lim;

-- name: CountOpenAlertsByKind :many
SELECT kind, count(*)::bigint AS n FROM alerts WHERE closed_at IS NULL GROUP BY kind;

-- name: InsertAlert :one
-- Devolve a linha só se o alerta é novo (ON CONFLICT DO NOTHING => sem linhas).
INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, data, opened_at, last_seen_at)
VALUES (@id, @company_id, @kind, @subject_type, @subject_id, @severity, @message, @data, @now, @now)
ON CONFLICT (company_id, kind, subject_id) WHERE closed_at IS NULL DO NOTHING
RETURNING *;

-- name: TouchAlert :exec
UPDATE alerts SET severity = @severity, message = @message, data = @data, last_seen_at = @now
WHERE id = @id AND closed_at IS NULL;

-- name: CloseAlert :one
UPDATE alerts SET closed_at = @now WHERE id = @id AND closed_at IS NULL
RETURNING *;
