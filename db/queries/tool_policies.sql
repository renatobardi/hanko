-- Política de tools (ADR-0053).

-- name: ListToolPolicies :many
SELECT p.*, COALESCE(i.slug, '')::text AS integration_slug, COALESCE(pe.name, '')::text AS persona_name
FROM tool_policies p
LEFT JOIN integrations i ON i.id = p.integration_id
LEFT JOIN personas pe ON pe.id = p.persona_id
WHERE p.company_id = @company_id
  AND (sqlc.narg(persona_id)::uuid IS NULL OR p.persona_id = sqlc.narg(persona_id)::uuid)
ORDER BY p.scope, pe.name, p.target, i.slug, p.tool_name;

-- name: ListToolPoliciesFor :many
-- As políticas que valem para uma persona: as da empresa e as dela (persona nula = só as da empresa).
SELECT p.*, COALESCE(i.slug, '')::text AS integration_slug, ''::text AS persona_name
FROM tool_policies p
LEFT JOIN integrations i ON i.id = p.integration_id
WHERE p.company_id = @company_id
  AND (p.scope = 'company' OR (sqlc.narg(persona_id)::uuid IS NOT NULL AND p.persona_id = sqlc.narg(persona_id)::uuid));

-- name: CountToolPolicies :one
SELECT count(*)::bigint FROM tool_policies WHERE company_id = @company_id;

-- name: HasNativeToolPolicies :one
-- Há política que alcança tools nativas do harness (alvo all ou native) para a persona?
SELECT EXISTS (
    SELECT 1 FROM tool_policies
    WHERE company_id = @company_id AND target IN ('all','native')
      AND (scope = 'company' OR persona_id = sqlc.narg(persona_id)::uuid)
)::boolean;

-- name: UpsertToolPolicy :one
INSERT INTO tool_policies (id, company_id, scope, persona_id, target, integration_id, tool_name, mode, origin,
                           created_by_type, created_by_id, created_at, updated_at)
VALUES (@id, @company_id, @scope, @persona_id, @target, @integration_id, @tool_name, @mode, @origin,
        @created_by_type, @created_by_id, @now, @now)
ON CONFLICT (company_id, scope, COALESCE(persona_id, '00000000-0000-0000-0000-000000000000'::uuid), target,
             COALESCE(integration_id, '00000000-0000-0000-0000-000000000000'::uuid), tool_name)
DO UPDATE SET mode = EXCLUDED.mode, origin = EXCLUDED.origin, created_by_type = EXCLUDED.created_by_type,
              created_by_id = EXCLUDED.created_by_id, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetToolPolicy :one
SELECT * FROM tool_policies WHERE company_id = @company_id AND id = @id;

-- name: DeleteToolPolicy :execrows
DELETE FROM tool_policies WHERE company_id = @company_id AND id = @id;

-- name: FindNativeToolApprovals :many
-- Os pedidos recentes da mesma chamada nativa (mesmo nome, mesmo sha256, mesma persona).
SELECT a.*, g.status AS gate_status
FROM tool_call_approvals a JOIN gates g ON g.id = a.gate_id
WHERE a.company_id = @company_id AND a.native_tool = @native_tool AND a.args_sha256 = @args_sha256
  AND a.persona_id = @persona_id AND a.created_at >= @since
ORDER BY a.created_at DESC, a.id DESC
LIMIT 10;

-- name: SetGateExpiry :exec
UPDATE gates SET expires_at = @expires_at WHERE company_id = @company_id AND id = @id;

-- name: ExpireDueGates :many
-- Vira expired o gate pendente cujo prazo passou (só os que têm prazo). Devolve o que expirou.
UPDATE gates SET status = 'expired', decided_at = @now
WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at <= @now AND kind = 'tool'
RETURNING *;

-- name: HasRunSince :one
-- Já abriu run desta persona nesta task depois do instante dado? (confere se um wake coalescido foi atendido)
SELECT EXISTS (SELECT 1 FROM runs WHERE persona_id = @persona_id AND task_id = @task_id AND created_at >= @since)::boolean;

-- name: ListQuarantinedTools :many
-- Tools em quarentena da empresa, para o aviso na Caixa de entrada.
SELECT t.*, i.slug AS integration_slug, i.name AS integration_name
FROM integration_tools t JOIN integrations i ON i.id = t.integration_id
WHERE t.company_id = @company_id AND t.status = 'quarantined' AND i.enabled
ORDER BY t.first_seen_at DESC, t.id
LIMIT 200;
