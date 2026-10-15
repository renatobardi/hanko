-- Exportação e importação de empresa (ADR-0022). Leitura em bloco e inserção com carimbos de tempo
-- preservados; as regras de validação ficam no service.

-- name: ExportTasks :many
SELECT * FROM tasks WHERE company_id = @company_id ORDER BY number;

-- name: ExportComments :many
SELECT * FROM task_comments WHERE company_id = @company_id ORDER BY created_at, id;

-- name: ExportRuns :many
-- Só execuções encerradas: uma execução em voo ainda não é um fato histórico.
SELECT * FROM runs WHERE company_id = @company_id AND status NOT IN ('queued', 'running') ORDER BY created_at, id;

-- name: ExportRunEvents :many
SELECT * FROM run_events WHERE company_id = @company_id ORDER BY run_id, seq;

-- name: ImportWorkspace :one
INSERT INTO workspaces (id, company_id, slug, name, path, repo_url, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @path, @repo_url, @created_at, @updated_at)
RETURNING *;

-- name: ImportProject :one
INSERT INTO projects (id, company_id, slug, name, description, workspace_id, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description, @workspace_id, @created_at, @updated_at)
RETURNING *;

-- name: ImportSubscription :one
INSERT INTO subscriptions (id, company_id, name, agent, monthly_usd, model_prefixes, weekly_limit_usd,
                           weekly_guard_pct, active, ended_at, created_at, updated_at)
VALUES (@id, @company_id, @name, @agent, @monthly_usd, @model_prefixes, @weekly_limit_usd,
        @weekly_guard_pct, @active, @ended_at, @created_at, @updated_at)
RETURNING *;

-- name: ImportSubscriptionPeriod :exec
INSERT INTO subscription_periods (subscription_id, started_at, ended_at) VALUES (@subscription_id, @started_at, @ended_at);

-- name: ImportPersona :one
-- reports_to e reviewer_persona_id entram depois (LinkImportedPersona): apontam para personas do mesmo arquivo.
INSERT INTO personas (id, company_id, slug, name, title, instructions, agent, model, adapter_config, status,
                      workspace_id, heartbeat_interval_sec, budget_monthly_cents, subscription_id, require_review,
                      reviewer_agent, reviewer_model, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @title, @instructions, @agent, @model, @adapter_config, @status,
        @workspace_id, @heartbeat_interval_sec, @budget_monthly_cents, @subscription_id, @require_review,
        @reviewer_agent, @reviewer_model, @created_at, @updated_at)
RETURNING *;

-- name: LinkImportedPersona :exec
UPDATE personas SET reports_to = @reports_to, reviewer_persona_id = @reviewer_persona_id WHERE id = @id;

-- name: ImportTask :one
INSERT INTO tasks (id, company_id, number, title, description, status, priority, persona_id, project_id,
                   workspace_id, parent_id, created_by_type, created_by_id, model, created_at, updated_at, closed_at)
VALUES (@id, @company_id, @number, @title, @description, @status, @priority, @persona_id, @project_id,
        @workspace_id, @parent_id, @created_by_type, @created_by_id, @model, @created_at, @updated_at, @closed_at)
RETURNING *;

-- name: ImportComment :one
INSERT INTO task_comments (id, company_id, task_id, author_type, author_id, run_id, body, created_at)
VALUES (@id, @company_id, @task_id, @author_type, @author_id, @run_id, @body, @created_at)
RETURNING *;

-- name: ImportFlow :one
INSERT INTO flows (id, company_id, slug, name, description, cron, timezone, enabled, persona_id, project_id,
                   priority, task_title, task_description, next_fire_at, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description, @cron, @timezone, @enabled, @persona_id, @project_id,
        @priority, @task_title, @task_description, @next_fire_at, @created_at, @updated_at)
RETURNING *;

-- name: ImportRun :one
INSERT INTO runs (id, company_id, task_id, persona_id, status, trigger, agent, model, model_source, subscription_id, name,
                  exit_code, error, summary, cost_usd, cost_source, list_cost_usd, input_tokens, output_tokens,
                  cache_read_tokens, cache_creation_tokens, next_event_seq, created_at, started_at, finished_at)
VALUES (@id, @company_id, @task_id, @persona_id, @status, @trigger, @agent, @model, @model_source, @subscription_id, @name,
        @exit_code, @error, @summary, @cost_usd, @cost_source, @list_cost_usd, @input_tokens, @output_tokens,
        @cache_read_tokens, @cache_creation_tokens, @next_event_seq, @created_at, @started_at, @finished_at)
RETURNING *;

-- name: ImportRunEvent :exec
INSERT INTO run_events (id, company_id, run_id, seq, kind, text, payload, occurred_at, received_at)
VALUES (@id, @company_id, @run_id, @seq, @kind, @text, @payload, @occurred_at, @occurred_at);

-- name: SetCompanyImportState :exec
UPDATE companies SET next_task_number = @next_task_number, budget_monthly_cents = @budget_monthly_cents WHERE id = @id;
