-- Gerador de carga reprodutível (tools/seed-load). Tudo é função determinística do índice da linha:
-- mesma escala + mesmo @REF => mesmo banco. Placeholders @NAME@ são substituídos por inteiros/timestamps pelo Go.
-- Cada bloco "-- step: nome" roda como um comando (e é cronometrado).

-- step: funções auxiliares
CREATE FUNCTION pg_temp.h(i bigint, salt int) RETURNS double precision LANGUAGE sql IMMUTABLE AS $$
  SELECT ((hashint8(i * 7919 + salt)::bigint & 2147483647)::double precision / 2147483647.0)
$$;
-- UUID estilo v7: 48 bits de milissegundos + resto derivado de md5(salt); ordenável no tempo como os do app.
CREATE FUNCTION pg_temp.u7(ts timestamptz, salt text) RETURNS uuid LANGUAGE sql IMMUTABLE AS $$
  SELECT (lpad(to_hex((extract(epoch FROM ts) * 1000)::bigint), 12, '0') || '7' || substr(m, 1, 3) || '8' || substr(m, 4, 3) || substr(m, 7, 12))::uuid
  FROM (SELECT md5(salt) AS m) x
$$;

-- step: empresas, workspaces, projetos, assinaturas
INSERT INTO companies (id, slug, name, task_prefix, timezone, next_task_number, budget_monthly_cents, created_at, updated_at)
SELECT pg_temp.u7('@REF@'::timestamptz - interval '170 days', 'company' || c), 'perf-' || chr(97 + c), 'Perf ' || upper(chr(97 + c)),
       'P' || upper(chr(97 + c)), 'America/Sao_Paulo', 1, 500000, '@REF@'::timestamptz - interval '170 days', '@REF@'::timestamptz - interval '170 days'
FROM generate_series(0, 2) c;

INSERT INTO workspaces (id, company_id, slug, name, path, created_at, updated_at)
SELECT pg_temp.u7('@REF@'::timestamptz - interval '169 days', 'ws' || c), co.id, 'main', 'Main', '/srv/perf/' || chr(97 + c),
       '@REF@'::timestamptz - interval '169 days', '@REF@'::timestamptz - interval '169 days'
FROM generate_series(0, 2) c JOIN companies co ON co.slug = 'perf-' || chr(97 + c);

INSERT INTO projects (id, company_id, slug, name, workspace_id, created_at, updated_at)
SELECT pg_temp.u7('@REF@'::timestamptz - interval '168 days', 'pr' || c || '-' || p), co.id, 'proj-' || p, 'Projeto ' || p, w.id,
       '@REF@'::timestamptz - interval '168 days', '@REF@'::timestamptz - interval '168 days'
FROM generate_series(0, 2) c CROSS JOIN generate_series(0, 3) p
JOIN companies co ON co.slug = 'perf-' || chr(97 + c) JOIN workspaces w ON w.company_id = co.id;

INSERT INTO subscriptions (id, company_id, name, agent, monthly_usd, model_prefixes, weekly_limit_usd, weekly_guard_pct, active, created_at, updated_at)
SELECT pg_temp.u7('@REF@'::timestamptz - interval '167 days', 'sub' || c), co.id, 'plano-claude', 'claude', 200, '{claude-}', 400, 90, true,
       '@REF@'::timestamptz - interval '167 days', '@REF@'::timestamptz - interval '167 days'
FROM generate_series(0, 2) c JOIN companies co ON co.slug = 'perf-' || chr(97 + c);

INSERT INTO subscription_periods (subscription_id, started_at) SELECT id, created_at FROM subscriptions;

-- step: personas (30)
INSERT INTO personas (id, company_id, slug, name, title, agent, model, status, reports_to, workspace_id, heartbeat_interval_sec,
                      budget_monthly_cents, subscription_id, require_review, pause_reason, last_error, last_error_at,
                      consecutive_failures, created_at, updated_at)
SELECT pg_temp.u7('@REF@'::timestamptz - interval '166 days', 'persona' || k), co.id, 'persona-' || k, 'Persona ' || k, 'Papel ' || (k % 10),
       CASE WHEN k % 10 < 6 THEN 'claude' WHEN k % 10 < 9 THEN 'codex' ELSE 'fake' END,
       CASE WHEN k % 10 < 6 THEN 'claude-sonnet-4' WHEN k % 10 < 9 THEN 'gpt-5' ELSE '' END,
       CASE WHEN k % 10 = 7 THEN 'error' WHEN k % 10 = 8 THEN 'paused'
            WHEN k % 10 = 5 THEN 'awaiting_gate' WHEN k % 3 = 0 THEN 'idle' ELSE 'active' END,
       NULL, w.id,
       CASE WHEN k % 10 < 6 THEN (ARRAY[300, 900, 3600])[1 + k % 3] ELSE 0 END,
       CASE WHEN k % 2 = 0 THEN 100000 END,
       CASE WHEN k % 10 < 6 THEN s.id END,
       k % 4 = 0,
       CASE WHEN k % 10 = 8 THEN 'budget' END,
       CASE WHEN k % 10 = 7 THEN 'falhou' END, CASE WHEN k % 10 = 7 THEN '@REF@'::timestamptz - interval '3 hours' END,
       CASE WHEN k % 10 = 7 THEN 3 ELSE 0 END,
       '@REF@'::timestamptz - interval '166 days', '@REF@'::timestamptz - interval '166 days'
FROM generate_series(0, 29) k
JOIN companies co ON co.slug = 'perf-' || chr(97 + k / 10)
JOIN workspaces w ON w.company_id = co.id
JOIN subscriptions s ON s.company_id = co.id;

UPDATE personas p SET reports_to = boss.id
FROM personas boss
WHERE boss.company_id = p.company_id AND boss.slug = 'persona-' || ((substr(p.slug, 9)::int / 10) * 10) AND boss.id <> p.id;

-- step: tasks
CREATE TEMP TABLE seed_tasks AS
SELECT i, company_idx, created_at, status, n_runs,
       row_number() OVER (PARTITION BY company_idx ORDER BY i) AS number
FROM (
  SELECT i, CASE WHEN pg_temp.h(i, 1) < 0.6 THEN 0 WHEN pg_temp.h(i, 1) < 0.9 THEN 1 ELSE 2 END AS company_idx,
         '@REF@'::timestamptz - interval '150 days' + (i::double precision / @N_TASKS@) * interval '150 days' AS created_at,
         CASE WHEN pg_temp.h(i, 2) < 0.55 THEN 'done' WHEN pg_temp.h(i, 2) < 0.60 THEN 'cancelled'
              WHEN pg_temp.h(i, 2) < 0.68 THEN 'backlog' WHEN pg_temp.h(i, 2) < 0.80 THEN 'todo'
              WHEN pg_temp.h(i, 2) < 0.88 THEN 'in_progress' WHEN pg_temp.h(i, 2) < 0.94 THEN 'in_review' ELSE 'blocked' END AS status
  FROM generate_series(1, @N_TASKS@) i
) x
CROSS JOIN LATERAL (
  SELECT CASE x.status WHEN 'backlog' THEN 0 WHEN 'done' THEN 4 WHEN 'cancelled' THEN 2 WHEN 'todo' THEN 2
                       WHEN 'in_progress' THEN 3 WHEN 'in_review' THEN 4 ELSE 3 END + floor(pg_temp.h(x.i, 3) * 3)::int
         * (x.status <> 'backlog')::int AS n_runs
) y;
CREATE UNIQUE INDEX ON seed_tasks (i);

INSERT INTO tasks (id, company_id, number, title, description, status, priority, persona_id, project_id, workspace_id, created_by_type,
                   model, created_at, updated_at, closed_at, last_human_change_at)
SELECT pg_temp.u7(t.created_at, 'task' || t.i), co.id, t.number,
       'Tarefa ' || t.i || ' ' || (ARRAY['migrar', 'revisar', 'corrigir', 'documentar', 'otimizar', 'implantar'])[1 + (t.i % 6)] || ' módulo ' || (t.i % 97),
       repeat('Descrição da tarefa ' || t.i || '. ', 3 + (t.i % 5)),
       t.status, (ARRAY['critical', 'high', 'medium', 'medium', 'low'])[1 + (t.i % 5)],
       CASE WHEN pg_temp.h(t.i, 4) < 0.92 THEN p.id END,
       pr.id, w.id, CASE WHEN t.i % 7 = 0 THEN 'persona' ELSE 'human' END, '',
       t.created_at, up.at, CASE WHEN t.status IN ('done', 'cancelled') THEN up.at END,
       CASE WHEN pg_temp.h(t.i, 4) < 0.92 THEN
            CASE WHEN t.status IN ('todo', 'in_progress') AND pg_temp.h(t.i, 5) < 0.04 THEN '@REF@'::timestamptz - interval '3 hours'
                 ELSE t.created_at END END
FROM seed_tasks t
CROSS JOIN LATERAL (SELECT t.created_at + (0.05 + 0.95 * pg_temp.h(t.i, 6)) * ('@REF@'::timestamptz - t.created_at) AS at) up
JOIN companies co ON co.slug = 'perf-' || chr(97 + t.company_idx)
JOIN personas p ON p.company_id = co.id AND p.slug = 'persona-' || (t.company_idx * 10 + floor(pg_temp.h(t.i, 7) * 10)::int)
JOIN projects pr ON pr.company_id = co.id AND pr.slug = 'proj-' || (t.i % 4)
JOIN workspaces w ON w.company_id = co.id;

UPDATE companies c SET next_task_number = COALESCE((SELECT max(number) + 1 FROM tasks t WHERE t.company_id = c.id), 1);

-- step: runs
CREATE TEMP TABLE seed_runs AS
SELECT row_number() OVER (ORDER BY t.i, j) AS ri, t.i AS ti, j, t.n_runs, t.company_idx, t.status AS task_status, t.created_at AS task_created,
       t.i * 100 + j AS key
FROM seed_tasks t CROSS JOIN LATERAL generate_series(1, t.n_runs) j;
CREATE UNIQUE INDEX ON seed_runs (ri);
ANALYZE seed_runs;

INSERT INTO runs (id, company_id, task_id, persona_id, status, trigger, agent, model, model_source, subscription_id, name, exit_code, error, summary,
                  cost_usd, cost_source, list_cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens,
                  next_event_seq, created_at, started_at, finished_at)
SELECT pg_temp.u7(r.created_at, 'run' || s.ri), t.company_id, t.id, COALESCE(t.persona_id, pe.id), r.status, r.trigger, COALESCE(pe.agent, 'claude'),
       pe.model, CASE WHEN pe.model = '' THEN 'harness_default' ELSE 'persona' END,
       CASE WHEN r.status IN ('done', 'failed') AND pe.subscription_id IS NOT NULL AND pg_temp.h(s.ri, 12) < 0.7 THEN pe.subscription_id END,
       'Run_' || s.ri,
       CASE WHEN r.status = 'done' THEN 0 WHEN r.status = 'failed' THEN 1 END,
       CASE WHEN r.status = 'failed' THEN 'harness saiu com erro' ELSE '' END,
       CASE WHEN r.status = 'done' THEN 'Resumo da run ' || s.ri ELSE '' END,
       r.cost, CASE WHEN r.cost IS NULL THEN 'none' WHEN pg_temp.h(s.ri, 13) < 0.6 THEN 'reported' ELSE 'estimated' END,
       CASE WHEN r.cost IS NOT NULL AND pg_temp.h(s.ri, 13) >= 0.6 THEN r.cost END,
       r.tokens, r.tokens / 4, r.tokens * 3, r.tokens / 8,
       r.n_ev + 1, r.created_at, r.started_at, r.finished_at
FROM seed_runs s
JOIN seed_tasks st ON st.i = s.ti
JOIN tasks t ON t.id = pg_temp.u7(st.created_at, 'task' || st.i)
JOIN personas pe ON pe.id = COALESCE(t.persona_id,
     pg_temp.u7('@REF@'::timestamptz - interval '166 days', 'persona' || (s.company_idx * 10 + floor(pg_temp.h(s.ri, 14) * 10)::int)))
CROSS JOIN LATERAL (
  SELECT x.status, x.created_at,
         CASE WHEN x.status IN ('done', 'failed', 'cancelled', 'interrupted', 'expired') THEN x.created_at + interval '1 minute' * (1 + 30 * pg_temp.h(s.ri, 15)) END AS finished_at,
         CASE WHEN x.status <> 'queued' THEN x.created_at + interval '1 second' * (1 + 59 * pg_temp.h(s.ri, 16)) END AS started_at,
         (ARRAY['assign', 'comment', 'manual', 'heartbeat', 'heartbeat', 'gate', 'flow'])[1 + (s.ri % 7)] AS trigger,
         CASE WHEN x.status IN ('done', 'failed') THEN round((0.05 + 3 * pg_temp.h(s.ri, 17))::numeric, 6) END AS cost,
         (20000 + 400000 * pg_temp.h(s.ri, 18))::bigint AS tokens,
         (5 + floor(pg_temp.h(s.ri, 19) * 40))::int AS n_ev
  FROM (
    SELECT CASE
             WHEN s.j = s.n_runs AND s.task_status = 'in_progress' AND pg_temp.h(s.ri, 20) < 0.15 THEN (CASE WHEN pg_temp.h(s.ri, 21) < 0.9 THEN 'running' ELSE 'queued' END)
             WHEN s.task_status = 'cancelled' AND s.j = s.n_runs THEN 'cancelled'
             WHEN pg_temp.h(s.ri, 22) < 0.80 THEN 'done' WHEN pg_temp.h(s.ri, 22) < 0.92 THEN 'failed'
             WHEN pg_temp.h(s.ri, 22) < 0.95 THEN 'cancelled' WHEN pg_temp.h(s.ri, 22) < 0.97 THEN 'interrupted' ELSE 'expired' END AS status,
           CASE WHEN s.j = s.n_runs AND s.task_status = 'in_progress' AND pg_temp.h(s.ri, 20) < 0.15
                THEN '@REF@'::timestamptz - interval '1 minute' * (2 + 28 * pg_temp.h(s.ri, 23))
                ELSE LEAST(st.created_at + (s.j::double precision / (s.n_runs + 1)) * (t.updated_at - st.created_at), '@REF@'::timestamptz - interval '1 hour') END AS created_at
  ) x
) r;
ANALYZE runs;

-- step: run_events
INSERT INTO run_events (id, company_id, run_id, seq, kind, text, payload, occurred_at, received_at)
SELECT pg_temp.u7(ts.at, 'ev' || r.id || e.seq), r.company_id, r.id, e.seq,
       CASE WHEN e.seq = 1 THEN 'system' WHEN e.seq = r.next_event_seq - 1 THEN 'result'
            ELSE (ARRAY['assistant', 'tool_use', 'tool_result', 'thinking', 'stdout', 'assistant', 'tool_use', 'tool_result'])[1 + (e.seq % 8)] END,
       repeat('x', (20 + (e.seq * 37 + r.next_event_seq) % 120)::int),
       CASE WHEN e.seq % 8 IN (1, 2, 7, 0) THEN jsonb_build_object('tool', 'Bash', 'ok', true, 'seq', e.seq) END,
       ts.at, ts.at + interval '5 milliseconds'
FROM runs r
CROSS JOIN LATERAL generate_series(1, r.next_event_seq - 1) e(seq)
CROSS JOIN LATERAL (SELECT COALESCE(r.started_at, r.created_at) + interval '1 second' * e.seq * 2 AS at) ts
WHERE r.ctid >= '(@CHUNK_LO@,0)'::tid AND r.ctid < '(@CHUNK_HI@,0)'::tid;

-- step: activity_events
INSERT INTO activity_events (id, company_id, actor_type, actor_id, verb, subject_type, subject_id, data, event_id, occurred_at, received_at)
SELECT pg_temp.u7(at.at, 'act' || a), t.company_id, CASE WHEN a % 3 = 0 THEN 'human' ELSE 'persona' END, t.persona_id,
       (ARRAY['task.created', 'task.status_changed', 'run.started', 'run.finished', 'comment.added', 'gate.opened', 'task.assigned'])[1 + (a % 7)],
       (ARRAY['task', 'task', 'run', 'run', 'task', 'gate', 'task'])[1 + (a % 7)], t.id,
       jsonb_build_object('n', a), CASE WHEN a % 5 = 0 THEN 'evt-' || a END, at.at, at.at
FROM generate_series(1, @N_ACTIVITY@) a
CROSS JOIN LATERAL (SELECT '@REF@'::timestamptz - interval '150 days' + (a::double precision / @N_ACTIVITY@) * interval '150 days' AS at) at
JOIN seed_tasks st ON st.i = 1 + floor(pg_temp.h(a, 30) * @N_TASKS@)::int
JOIN tasks t ON t.id = pg_temp.u7(st.created_at, 'task' || st.i);

-- step: comentários
INSERT INTO task_comments (id, company_id, task_id, author_type, author_id, body, created_at)
SELECT pg_temp.u7(c.at, 'com' || a), t.company_id, t.id, CASE WHEN a % 4 = 0 THEN 'human' ELSE 'persona' END, t.persona_id,
       'Comentário ' || a || ' sobre a tarefa.', c.at
FROM generate_series(1, @N_COMMENTS@) a
JOIN seed_tasks st ON st.i = 1 + floor(pg_temp.h(a, 31) * @N_TASKS@)::int
JOIN tasks t ON t.id = pg_temp.u7(st.created_at, 'task' || st.i)
CROSS JOIN LATERAL (SELECT LEAST(st.created_at + interval '1 minute' * (a % 4000), '@REF@'::timestamptz - interval '10 minutes') AS at) c;

-- step: gates
INSERT INTO gates (id, company_id, kind, status, title, body, task_id, run_id, persona_id, requested_by_type, decided_by_type, created_at, decided_at)
SELECT pg_temp.u7(c.at, 'gate' || a), t.company_id, (ARRAY['approval', 'task_review', 'task_review', 'hire'])[1 + (a % 4)], s.status,
       'Gate ' || a, '', t.id, NULL, t.persona_id, 'persona', CASE WHEN s.status <> 'pending' THEN 'human' END, c.at,
       CASE WHEN s.status <> 'pending' THEN c.at + interval '1 hour' END
FROM generate_series(1, @N_GATES@) a
JOIN seed_tasks st ON st.i = 1 + floor(pg_temp.h(a, 32) * @N_TASKS@)::int
JOIN tasks t ON t.id = pg_temp.u7(st.created_at, 'task' || st.i)
CROSS JOIN LATERAL (SELECT st.created_at + interval '1 hour' * (a % 2000) AS at) c
CROSS JOIN LATERAL (SELECT CASE WHEN pg_temp.h(a, 33) < 0.03 THEN 'pending' WHEN pg_temp.h(a, 33) < 0.63 THEN 'approved'
                                WHEN pg_temp.h(a, 33) < 0.78 THEN 'rejected' WHEN pg_temp.h(a, 33) < 0.88 THEN 'changes_requested'
                                ELSE 'expired' END AS status) s
WHERE c.at < '@REF@'::timestamptz - interval '2 hours';

-- step: alertas
INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, opened_at, last_seen_at, closed_at)
SELECT pg_temp.u7(c.at, 'alert' || a), p.company_id, 'persona_silent', 'persona', p.id, 'warning', 'persona silenciosa', c.at,
       c.at + interval '1 hour', CASE WHEN a > 6 THEN c.at + interval '2 hours' END
FROM generate_series(1, 2000) a
CROSS JOIN LATERAL (SELECT '@REF@'::timestamptz - interval '150 days' + interval '1 hour' * a AS at) c
JOIN personas p ON p.id = pg_temp.u7('@REF@'::timestamptz - interval '166 days', 'persona' || (a % 30));

-- step: analyze
ANALYZE;
