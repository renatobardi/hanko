-- Classificação da conversa externa (ADR-0079 itens 1 a 3 e 7). São os sinais que phase.py (oute-agent@845764f) lê, na
-- telemetria do Hanko. A passada refaz tudo por empresa; o serviço só grava o que mudou.

-- name: ListCompaniesWithExternalConversations :many
SELECT DISTINCT company_id FROM external_conversation ORDER BY company_id ASC;

-- name: ListConversationsToClassify :many
SELECT id, session_id, oute_repo, class, class_origin, class_confidence, classifier, tags
FROM external_conversation WHERE company_id = @company_id ORDER BY session_id ASC;

-- name: ClassifySessionFacts :many
-- Fatos da sessão: a hora do último fato, o oute.task.id (o primeiro que chegou), e se há papel de rodada, worker e passo.
SELECT session_id, max(time_unix_nano)::bigint AS last_at,
    COALESCE((array_agg(oute_task_id ORDER BY time_unix_nano) FILTER (WHERE oute_task_id IS NOT NULL))[1], '')::text AS task_id,
    bool_or(COALESCE(oute_swarm_round, resource_attributes->>'oute.swarm.round') IS NOT NULL) AS has_round,
    bool_or(resource_attributes->>'oute.swarm.session' IS NOT NULL) AS has_worker,
    bool_or(resource_attributes->>'oute.swarm.step' IS NOT NULL) AS has_step
FROM (
    SELECT session_id, time_unix_nano, oute_task_id, oute_swarm_round, resource_attributes FROM telemetry_spans
    WHERE telemetry_spans.company_id = @company_id AND session_id IS NOT NULL
    UNION ALL
    SELECT session_id, time_unix_nano, oute_task_id, oute_swarm_round, resource_attributes FROM telemetry_logs
    WHERE company_id = @company_id AND session_id IS NOT NULL
) f
GROUP BY session_id;

-- name: ClassifyOpenings :many
-- Eventos de abertura do oute-task, em ordem de hora (ADR-0079 item 2; phase.py `_openings`).
SELECT oute_task_id, time_unix_nano, COALESCE(attributes->>'oute.task.phase', '')::text AS phase, COALESCE(attributes->>'oute.task.origin', '')::text AS origin,
    COALESCE(attributes->>'oute.task.repo', '')::text AS repo, COALESCE(attributes->>'oute.task.slug', '')::text AS slug
FROM telemetry_logs
WHERE company_id = @company_id AND event_name IN ('oute.task.opened', 'oute.task.reopened') AND oute_task_id IS NOT NULL
ORDER BY time_unix_nano ASC;

-- name: ClassifyToolActions :many
-- Ações de ferramenta, contadas pelo que as regras leem (ADR-0079 item 2; phase.py `_actions`).
SELECT session_id, COALESCE(attributes->>'tool_name', '')::text AS tool, COALESCE(attributes->>'full_command', '')::text AS command,
    COALESCE(attributes->>'file_path', '')::text AS path, count(*)::bigint AS n
FROM telemetry_spans
WHERE company_id = @company_id AND name = 'claude_code.tool' AND session_id IS NOT NULL
GROUP BY session_id, tool, command, path;

-- name: ClassifySkillRows :many
-- Linhas de ferramenta que citam uma skill `oute-aidlc-` (ADR-0079 item 2; phase.py `_skills`). O tool_parameters vem como
-- texto e é lido no serviço, para um JSON malformado não derrubar a passada.
SELECT session_id, COALESCE(attributes->>'tool_name', '')::text AS tool, COALESCE(attributes->>'skill_name', '')::text AS skill,
    COALESCE(attributes->>'tool_parameters', '')::text AS tool_parameters, COALESCE(attributes->>'file_path', '')::text AS path,
    time_unix_nano
FROM (
    SELECT session_id, attributes, time_unix_nano FROM telemetry_spans
    WHERE telemetry_spans.company_id = @company_id AND name = 'claude_code.tool' AND session_id IS NOT NULL AND attributes::text LIKE '%oute-aidlc-%'
    UNION ALL
    SELECT session_id, attributes, time_unix_nano FROM telemetry_logs
    WHERE company_id = @company_id AND event_name IN ('tool_result', 'claude_code.tool_result') AND session_id IS NOT NULL
        AND attributes::text LIKE '%oute-aidlc-%'
) f;

-- name: ListManualClasses :many
-- A troca manual mais recente de cada conversa (ADR-0079 item 4 e passo 1 do phase.py): vale sobre tudo.
SELECT DISTINCT ON (conversation_id) conversation_id, class
FROM conversation_class_input
WHERE company_id = @company_id AND kind = 'manual'
ORDER BY conversation_id ASC, created_at DESC;

-- name: SetConversationClass :exec
UPDATE external_conversation
SET class = @class, class_origin = @class_origin, class_confidence = @class_confidence, classifier = @classifier,
    tags = @tags, updated_at = @now
WHERE id = @id;
