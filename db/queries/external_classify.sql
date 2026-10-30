-- Classificação da conversa externa (ADR-0079 itens 1 a 3 e 7). São os sinais que phase.py (oute-agent@845764f) lê, na
-- telemetria do Hanko. A passada relê só as conversas com fato novo (janela pela sessão), mudança de classificador ou troca
-- manual; as consultas de telemetria ficam restritas às sessões dessas conversas, exceto as aberturas (label entre sessões).

-- name: ListCompaniesWithExternalConversations :many
SELECT DISTINCT company_id FROM external_conversation ORDER BY company_id ASC;

-- name: LockExternalClassify :exec
-- Uma passada por vez, mesmo com mais de uma instância (como `hanko:alerts`).
SELECT pg_advisory_xact_lock(hashtext('hanko:classify_external'));

-- name: ListConversationsToClassify :many
-- Só as conversas que precisam: classificadas por outra versão do classificador, com fato da sessão depois da última
-- classificação, ou com troca manual que ainda não está na classe. Lote limitado por passada.
SELECT c.id, c.session_id, c.oute_repo, c.class, c.class_origin, c.class_confidence, c.classifier, c.tags,
    COALESCE(s.last_at, 0)::bigint AS session_last
FROM external_conversation c
LEFT JOIN external_session s ON s.id = c.external_session_id
WHERE c.company_id = @company_id AND (
    c.classifier <> @classifier::text
    OR COALESCE(s.last_at, 0) > c.classified_last_at
    OR (c.class_origin <> 'manual' AND EXISTS (SELECT 1 FROM conversation_class_input m WHERE m.conversation_id = c.id AND m.kind = 'manual'))
    OR (c.class_origin = 'manual' AND EXISTS (SELECT 1 FROM conversation_class_input m WHERE m.conversation_id = c.id AND m.kind = 'manual')
        AND (SELECT m.class FROM conversation_class_input m WHERE m.conversation_id = c.id AND m.kind = 'manual'
             ORDER BY m.created_at DESC LIMIT 1) IS DISTINCT FROM c.class)
)
ORDER BY c.id ASC
LIMIT @batch_limit::int;

-- name: ClassifySessionFacts :many
-- Fatos das sessões pedidas: a hora do último fato, o oute.task.id (o primeiro que chegou) e os papéis da rodada.
SELECT session_id, max(time_unix_nano)::bigint AS last_at,
    COALESCE((array_agg(oute_task_id ORDER BY time_unix_nano) FILTER (WHERE oute_task_id IS NOT NULL))[1], '')::text AS task_id,
    bool_or(COALESCE(oute_swarm_round, resource_attributes->>'oute.swarm.round') IS NOT NULL) AS has_round,
    bool_or(resource_attributes->>'oute.swarm.session' IS NOT NULL) AS has_worker,
    bool_or(resource_attributes->>'oute.swarm.step' IS NOT NULL) AS has_step
FROM (
    SELECT telemetry_spans.session_id, telemetry_spans.time_unix_nano, telemetry_spans.oute_task_id, telemetry_spans.oute_swarm_round,
        telemetry_spans.resource_attributes FROM telemetry_spans
    WHERE telemetry_spans.company_id = @company_id AND telemetry_spans.session_id = ANY(@sessions::text[])
    UNION ALL
    SELECT telemetry_logs.session_id, telemetry_logs.time_unix_nano, telemetry_logs.oute_task_id, telemetry_logs.oute_swarm_round,
        telemetry_logs.resource_attributes FROM telemetry_logs
    WHERE telemetry_logs.company_id = @company_id AND telemetry_logs.session_id = ANY(@sessions::text[])
) f
GROUP BY session_id;

-- name: ClassifyOpenings :many
-- Eventos de abertura do oute-task, em ordem de hora (ADR-0079 item 2; phase.py `_openings`). Todas as sessões da empresa,
-- porque o label vale entre sessões da mesma issue.
SELECT oute_task_id, time_unix_nano, COALESCE(attributes->>'oute.task.phase', '')::text AS phase,
    COALESCE(attributes->>'oute.task.origin', '')::text AS origin,
    COALESCE(attributes->>'oute.task.repo', '')::text AS repo, COALESCE(attributes->>'oute.task.slug', '')::text AS slug
FROM telemetry_logs
WHERE company_id = @company_id AND event_name IN ('oute.task.opened', 'oute.task.reopened') AND oute_task_id IS NOT NULL
ORDER BY time_unix_nano ASC;

-- name: ClassifyToolActions :many
-- Ações de ferramenta das sessões pedidas, contadas pelo que as regras leem (ADR-0079 item 2; phase.py `_actions`).
SELECT session_id, COALESCE(attributes->>'tool_name', '')::text AS tool, COALESCE(attributes->>'full_command', '')::text AS command,
    COALESCE(attributes->>'file_path', '')::text AS path, count(*)::bigint AS n
FROM telemetry_spans
WHERE company_id = @company_id AND name = 'claude_code.tool' AND session_id = ANY(@sessions::text[])
GROUP BY session_id, tool, command, path;

-- name: ClassifySkillRows :many
-- Linhas de ferramenta que citam uma skill `oute-aidlc-` nas sessões pedidas (phase.py `_skills`). O tool_parameters vem
-- como texto e é lido no serviço, para um JSON malformado não derrubar a passada.
SELECT session_id, COALESCE(attributes->>'tool_name', '')::text AS tool, COALESCE(attributes->>'skill_name', '')::text AS skill,
    COALESCE(attributes->>'tool_parameters', '')::text AS tool_parameters, COALESCE(attributes->>'file_path', '')::text AS path,
    time_unix_nano
FROM (
    SELECT telemetry_spans.session_id, telemetry_spans.attributes, telemetry_spans.time_unix_nano FROM telemetry_spans
    WHERE telemetry_spans.company_id = @company_id AND telemetry_spans.name = 'claude_code.tool'
        AND telemetry_spans.session_id = ANY(@sessions::text[]) AND telemetry_spans.attributes::text LIKE '%oute-aidlc-%'
    UNION ALL
    SELECT telemetry_logs.session_id, telemetry_logs.attributes, telemetry_logs.time_unix_nano FROM telemetry_logs
    WHERE telemetry_logs.company_id = @company_id AND telemetry_logs.event_name IN ('tool_result', 'claude_code.tool_result')
        AND telemetry_logs.session_id = ANY(@sessions::text[]) AND telemetry_logs.attributes::text LIKE '%oute-aidlc-%'
) f;

-- name: ListManualClasses :many
-- A troca manual mais recente de cada conversa (ADR-0079 item 4 e passo 1 do phase.py): vale sobre tudo.
SELECT DISTINCT ON (conversation_id) conversation_id, class
FROM conversation_class_input
WHERE company_id = @company_id AND kind = 'manual'
ORDER BY conversation_id ASC, created_at DESC;

-- name: SetConversationClass :exec
-- Grava a classe e a janela da classificação. A janela avança mesmo quando a classe não muda, para a conversa sair da lista.
UPDATE external_conversation
SET class = @class, class_origin = @class_origin, class_confidence = @class_confidence, classifier = @classifier,
    tags = @tags, classified_last_at = @classified_last_at::bigint, updated_at = @now
WHERE id = @id AND company_id = @company_id;
