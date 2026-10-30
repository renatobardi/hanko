-- Alerta host_no_data (ADR-0077 item 4; ADR-0083 item 2): hosts externos sempre ligados e não arquivados, com o limite
-- da empresa (`limit.host_no_data_minutes`; sem ajuste, 30 min). O serviço compara a hora do último fato (ou a da
-- criação, se nenhum fato chegou) com o limite.
-- name: ListAlwaysOnHostsLimits :many
SELECT h.id, h.company_id, h.name, h.created_at, h.last_seen_at,
    COALESCE(s.value, 30)::float8 AS limit_minutes
FROM external_host h
LEFT JOIN company_alert_settings s ON s.company_id = h.company_id AND s.key = 'limit.host_no_data_minutes'
WHERE h.always_on AND h.archived_at IS NULL
ORDER BY h.company_id, h.name;
