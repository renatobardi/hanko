-- +goose Up
-- Workspace de execução com Git (ADR-0054, revisa o ADR-0025): clone espelho do repositório do workspace, worktree
-- + branch por task, padrões de projeto (instruções, meta, import de skills) e "Nova sessão" na task. Só tabelas
-- novas (nenhuma coluna nova em workspaces/projects/tasks/runs), aditivo.

-- Um workspace com linha aqui é um "workspace Git": o repositório (workspaces.repo_url) é espelhado no disco, em
-- <raiz>/<empresa>/git/<workspace>.git, e cada task ganha um worktree. A credencial é uma referência
-- ${secret:NOME} (ADR-0050); o valor nunca é gravado aqui.
CREATE TABLE workspace_git (
    workspace_id   uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    credential_ref text NOT NULL DEFAULT '' CHECK (length(credential_ref) <= 200),
    mirror_state   text NOT NULL DEFAULT 'idle' CHECK (mirror_state IN ('idle','syncing','ready','failed')),
    base_branch    text NOT NULL DEFAULT '',
    head_sha       text CHECK (head_sha IS NULL OR head_sha ~ '^[0-9a-f]{40}$'),
    last_error     text NOT NULL DEFAULT '',
    last_fetch_at  timestamptz,
    syncing_since  timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workspace_git_company_idx ON workspace_git (company_id);

-- O worktree (e a branch) de uma task. Uma linha por task: ao fim da task a pasta sai do disco (state removed) e a
-- branch fica no espelho; se a task for retomada, a mesma linha volta a provisionar.
CREATE TABLE task_worktrees (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    task_id        uuid NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE CASCADE,
    workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    branch         text NOT NULL CHECK (length(branch) BETWEEN 1 AND 100),
    base_branch    text NOT NULL DEFAULT '',
    base_sha       text CHECK (base_sha IS NULL OR base_sha ~ '^[0-9a-f]{40}$'),
    head_sha       text CHECK (head_sha IS NULL OR head_sha ~ '^[0-9a-f]{40}$'),
    path           text NOT NULL DEFAULT '',
    state          text NOT NULL CHECK (state IN ('provisioning','ready','failed','diverged','removed')),
    error          text NOT NULL DEFAULT '',
    removed_reason text NOT NULL DEFAULT '' CHECK (removed_reason IN ('','done','cancelled','merged','manual','workspace_deleted')),
    claimed_at     timestamptz,
    removed_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_worktrees_company_state_idx ON task_worktrees (company_id, state);
CREATE INDEX task_worktrees_workspace_idx ON task_worktrees (workspace_id);

-- Padrões do projeto (#34): instruções (briefing depois das da persona), meta padrão das tasks novas e import
-- automático das skills do repositório clonado. A política de revisão continua em project_review_policies (ADR-0040).
CREATE TABLE project_defaults (
    project_id          uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    instructions        text NOT NULL DEFAULT '' CHECK (length(instructions) <= 16384),
    goal_id             uuid REFERENCES goals(id) ON DELETE SET NULL,
    import_repo_skills  boolean NOT NULL DEFAULT false,
    skills_path         text NOT NULL DEFAULT '' CHECK (length(skills_path) <= 500),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX project_defaults_company_idx ON project_defaults (company_id);
CREATE INDEX project_defaults_goal_idx ON project_defaults (goal_id) WHERE goal_id IS NOT NULL;

-- "Nova sessão" na task (#16): o pedido vale para o próximo run, que o consome (consumed_run_id).
CREATE TABLE task_session_resets (
    id               uuid PRIMARY KEY,
    company_id       uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    task_id          uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    reason           text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
    requested_by_type text NOT NULL CHECK (requested_by_type IN ('human','persona','system')),
    requested_by_id  uuid,
    consumed_run_id  uuid REFERENCES runs(id) ON DELETE SET NULL,
    consumed_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_session_resets_task_idx ON task_session_resets (task_id, created_at DESC);
CREATE INDEX task_session_resets_pending_idx ON task_session_resets (task_id) WHERE consumed_at IS NULL;

-- +goose Down
DROP TABLE task_session_resets;
DROP TABLE project_defaults;
DROP TABLE task_worktrees;
DROP TABLE workspace_git;
