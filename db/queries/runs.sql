-- name: CreateRun :one
INSERT INTO runs (id, company_id, task_id, persona_id, status, trigger, agent, model, model_source, subscription_id, name, created_at)
VALUES (@id, @company_id, @task_id, @persona_id, 'queued', @trigger, @agent, @model, @model_source, @subscription_id, @name, @now)
RETURNING *;

-- name: GetRunByID :one
SELECT * FROM runs WHERE company_id = @company_id AND id = @id;

-- name: GetRunByIDForUpdate :one
SELECT * FROM runs WHERE company_id = @company_id AND id = @id FOR UPDATE;

-- name: GetRunView :one
SELECT sqlc.embed(r), (c.task_prefix || '-' || t.number)::text AS task_key, t.title AS task_title, p.name AS persona_name
FROM runs r
JOIN tasks t ON t.id = r.task_id
JOIN personas p ON p.id = r.persona_id
JOIN companies c ON c.id = r.company_id
WHERE r.company_id = @company_id AND r.id = @id;

-- name: ListRunViews :many
SELECT sqlc.embed(r), (c.task_prefix || '-' || t.number)::text AS task_key, t.title AS task_title, p.name AS persona_name
FROM runs r
JOIN tasks t ON t.id = r.task_id
JOIN personas p ON p.id = r.persona_id
JOIN companies c ON c.id = r.company_id
WHERE r.company_id = @company_id
  AND (sqlc.narg(task_id)::uuid IS NULL OR r.task_id = sqlc.narg(task_id)::uuid)
  AND (sqlc.narg(persona_id)::uuid IS NULL OR r.persona_id = sqlc.narg(persona_id)::uuid)
  AND (sqlc.narg(statuses)::text[] IS NULL OR r.status = ANY(sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(name)::text IS NULL OR lower(r.name) = lower(sqlc.narg(name)::text))
ORDER BY r.created_at DESC, r.id DESC
LIMIT @lim;

-- name: ActiveRunForTask :one
SELECT * FROM runs WHERE task_id = @task_id AND status IN ('queued', 'running');

-- name: MarkRunStarted :one
UPDATE runs SET status = 'running', started_at = @now, trace_id = @trace_id, handoff_in = @handoff_in
WHERE company_id = @company_id AND id = @id AND status = 'queued'
RETURNING *;

-- name: FinishRun :one
UPDATE runs
SET status = @status, exit_code = @exit_code, error = @error, summary = @summary, cost_usd = @cost_usd,
    input_tokens = @input_tokens, output_tokens = @output_tokens,
    cache_read_tokens = @cache_read_tokens, cache_creation_tokens = @cache_creation_tokens,
    cost_source = @cost_source, list_cost_usd = @list_cost_usd,
    session_ref = @session_ref, model = @model, finished_at = @now, pending_result = NULL
WHERE company_id = @company_id AND id = @id
RETURNING *;

-- name: SetRunPendingResult :exec
-- Guarda o resultado antes de finalizar: uma finalização que falha pode ser refeita sem reexecutar o harness.
UPDATE runs SET pending_result = @pending_result
WHERE company_id = @company_id AND id = @id AND status = 'running';

-- name: CancelQueuedRun :one
UPDATE runs SET status = 'cancelled', finished_at = @now
WHERE company_id = @company_id AND id = @id AND status = 'queued'
RETURNING *;

-- name: InterruptActiveRuns :many
UPDATE runs SET status = 'interrupted', finished_at = @now,
    error = CASE WHEN error = '' THEN 'interrupted: process restarted' ELSE error END
WHERE status IN ('queued', 'running')
RETURNING *;

-- name: ListWakeCandidates :many
-- Tasks atribuídas, trabalháveis e sem run ativa nem gate pendente, de personas que podem trabalhar
-- (e que não tiveram o checkout recusado por reserva de orçamento nos últimos 10 min):
--  (a) o intervalo de heartbeat da persona (> 0) passou desde a última run da task; ou
--  (b) rede de segurança, independente do intervalo: houve uma mudança "não vista" (status/atribuição
--      por ator não-persona, ou comentário humano) antes de @stale_before (agora - 1 minuto) e nenhuma
--      run cobre o instante dela (nenhuma run terminou depois dele nem está aberta), ou seja, o wake se perdeu.
--      Escritas da própria persona e edições de título/descrição/prioridade não contam.
SELECT t.company_id, t.persona_id AS persona_id, t.id AS task_id
FROM tasks t
JOIN personas p ON p.id = t.persona_id
LEFT JOIN LATERAL (
    SELECT max(c.created_at) AS last_comment FROM task_comments c
    WHERE c.task_id = t.id AND c.author_type <> 'persona'
) hc ON true
WHERE t.status IN ('todo', 'in_progress')
  AND p.status IN ('active', 'idle', 'running')
  AND (p.budget_refused_at IS NULL OR p.budget_refused_at <= @budget_cutoff::timestamptz)
  AND NOT EXISTS (SELECT 1 FROM runs a WHERE a.task_id = t.id AND a.status IN ('queued', 'running'))
  AND NOT EXISTS (SELECT 1 FROM gates g WHERE g.task_id = t.id AND g.status = 'pending')
  AND (
    (p.heartbeat_interval_sec > 0
     AND COALESCE((SELECT max(COALESCE(r.finished_at, r.created_at)) FROM runs r WHERE r.task_id = t.id), 'epoch'::timestamptz)
         + make_interval(secs => p.heartbeat_interval_sec) <= @now::timestamptz)
    OR
    (GREATEST(t.last_human_change_at, hc.last_comment) < @stale_before::timestamptz
     AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.task_id = t.id
                     AND COALESCE(r.finished_at, @now::timestamptz) >= GREATEST(t.last_human_change_at, hc.last_comment)))
  )
ORDER BY t.updated_at
LIMIT @lim;

-- name: BumpWakeRequest :exec
-- Registra um pedido de wake; o worker compara o contador antes e depois de processar.
INSERT INTO wake_requests (company_id, persona_id, task_id, seq, updated_at)
VALUES (@company_id, @persona_id, @task_id, 1, now())
ON CONFLICT (persona_id, task_id) DO UPDATE SET seq = wake_requests.seq + 1, updated_at = now();

-- name: GetWakeSeq :one
SELECT COALESCE((SELECT seq FROM wake_requests WHERE persona_id = @persona_id AND task_id = @task_id), 0)::bigint AS seq;

-- name: AppendRunEvent :one
-- O seq vem de runs.next_event_seq (UPDATE ... RETURNING serializa os escritores na linha da run).
WITH r AS (
    UPDATE runs SET next_event_seq = next_event_seq + 1
    WHERE runs.company_id = @company_id AND runs.id = @run_id
    RETURNING runs.id, runs.company_id, runs.next_event_seq - 1 AS seq
)
INSERT INTO run_events (id, company_id, run_id, seq, kind, text, payload, occurred_at, received_at)
SELECT @id, r.company_id, r.id, r.seq, @kind, @text, @payload, @occurred_at, @now
FROM r
RETURNING *;

-- name: ListRunEvents :many
SELECT * FROM run_events
WHERE company_id = @company_id AND run_id = @run_id AND seq > @after_seq
ORDER BY seq
LIMIT @lim;

-- name: ListRunEventsBefore :many
-- Página anterior do log (cursor por seq): os @lim eventos imediatamente antes de @before_seq, do mais novo para o mais antigo
-- (o service devolve em ordem crescente). Usa o índice único (run_id, seq).
SELECT * FROM run_events
WHERE company_id = @company_id AND run_id = @run_id AND seq < @before_seq
ORDER BY seq DESC
LIMIT @lim;

-- name: ListWorkableTasksForPersona :many
SELECT * FROM tasks WHERE persona_id = @persona_id AND status IN ('todo', 'in_progress') ORDER BY updated_at, id;

-- name: TakenRunNames :many
-- Quais dos nomes candidatos já existem na empresa.
SELECT name FROM runs WHERE company_id = @company_id AND name = ANY(@names::text[]);

-- name: ListRunsWithoutName :many
-- Runs anteriores aos nomes amigáveis (backfill no boot), mais antigas primeiro.
SELECT id, company_id FROM runs WHERE name = '' ORDER BY created_at, id LIMIT @lim;

-- name: SetRunName :execrows
UPDATE runs SET name = @name WHERE id = @id AND name = '';
