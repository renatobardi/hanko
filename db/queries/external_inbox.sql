-- Caixa: agentes e hosts externos novos (ADR-0075 item 2; ADR-0077). Só os não arquivados e criados desde `since`.

-- name: ListNewExternalAgents :many
SELECT id, name, created_at FROM external_agent
WHERE company_id = @company_id AND archived_at IS NULL AND created_at >= @since
ORDER BY created_at DESC, id
LIMIT @lim::int;

-- name: ListNewExternalHosts :many
SELECT id, name, created_at FROM external_host
WHERE company_id = @company_id AND archived_at IS NULL AND created_at >= @since
ORDER BY created_at DESC, id
LIMIT @lim::int;
