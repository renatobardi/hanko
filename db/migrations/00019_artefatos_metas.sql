-- +goose Up
-- Artefatos e metas (ADR-0028). Duas tabelas de artefatos (metadados e conteúdo separados, para que listar nunca
-- leia bytea) e três de metas.

-- Artefato: um arquivo produzido por uma persona (ou enviado por um humano). Cada linha é UMA VERSÃO: o mesmo nome
-- na mesma task (ou, sem task, na mesma empresa) cria a versão seguinte. O vínculo com task, run, projeto e persona é
-- opcional e some com ON DELETE SET NULL: apagar a task não apaga o arquivo.
CREATE TABLE artifacts (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    task_id     uuid REFERENCES tasks(id) ON DELETE SET NULL,
    run_id      uuid REFERENCES runs(id) ON DELETE SET NULL,
    project_id  uuid REFERENCES projects(id) ON DELETE SET NULL,
    persona_id  uuid REFERENCES personas(id) ON DELETE SET NULL,
    author_type text NOT NULL CHECK (author_type IN ('human','persona','system')),
    author_id   uuid,
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    mime        text NOT NULL CHECK (length(mime) BETWEEN 3 AND 127),
    size_bytes  bigint NOT NULL CHECK (size_bytes >= 0),
    sha256      text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    version     integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Cadeia de versões: (empresa, task ou "sem task", nome). O uuid zero faz o papel de "sem task" na unicidade.
CREATE UNIQUE INDEX artifacts_chain_version_uq
    ON artifacts (company_id, COALESCE(task_id, '00000000-0000-0000-0000-000000000000'::uuid), name, version);
CREATE INDEX artifacts_company_created_idx ON artifacts (company_id, created_at DESC, id DESC);
CREATE INDEX artifacts_task_idx ON artifacts (task_id, created_at DESC) WHERE task_id IS NOT NULL;
CREATE INDEX artifacts_run_idx ON artifacts (run_id) WHERE run_id IS NOT NULL;
CREATE INDEX artifacts_project_idx ON artifacts (company_id, project_id) WHERE project_id IS NOT NULL;
CREATE INDEX artifacts_persona_idx ON artifacts (company_id, persona_id) WHERE persona_id IS NOT NULL;

-- Conteúdo do artefato (1:1 com a versão). Separado para que nenhuma listagem leia o bytea.
CREATE TABLE artifact_blobs (
    artifact_id uuid PRIMARY KEY REFERENCES artifacts(id) ON DELETE CASCADE,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    content     bytea NOT NULL
);
CREATE INDEX artifact_blobs_company_idx ON artifact_blobs (company_id);

-- Meta: objetivo mensurável, opcionalmente ligado a um projeto. progresso = (atual - inicial) / (alvo - inicial),
-- limitado a [0, 1]; o alvo pode ser menor que o inicial (meta de redução).
CREATE TABLE goals (
    id               uuid PRIMARY KEY,
    company_id       uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    project_id       uuid REFERENCES projects(id) ON DELETE SET NULL,
    slug             text NOT NULL,
    title            text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    description      text NOT NULL DEFAULT '',
    metric           text NOT NULL DEFAULT '',
    unit             text NOT NULL DEFAULT '',
    start_value      double precision NOT NULL,
    target_value     double precision NOT NULL,
    current_value    double precision NOT NULL,
    due_date         date,
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','achieved','abandoned')),
    owner_persona_id uuid REFERENCES personas(id) ON DELETE SET NULL,
    created_by_type  text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id    uuid,
    achieved_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (start_value <> target_value),
    UNIQUE (company_id, slug)
);
CREATE INDEX goals_company_status_idx ON goals (company_id, status, created_at);
CREATE INDEX goals_project_idx ON goals (company_id, project_id) WHERE project_id IS NOT NULL;
CREATE INDEX goals_achieved_idx ON goals (company_id, achieved_at DESC) WHERE achieved_at IS NOT NULL;

-- Histórico de progresso: cada registro guarda o valor lido naquele momento.
CREATE TABLE goal_updates (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    goal_id     uuid NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    value       double precision NOT NULL,
    note        text NOT NULL DEFAULT '',
    author_type text NOT NULL CHECK (author_type IN ('human','persona','system')),
    author_id   uuid,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX goal_updates_goal_idx ON goal_updates (goal_id, created_at, id);

-- Task ligada a uma meta (opcional).
ALTER TABLE tasks ADD COLUMN goal_id uuid REFERENCES goals(id) ON DELETE SET NULL;
CREATE INDEX tasks_goal_status_idx ON tasks (company_id, goal_id, status) WHERE goal_id IS NOT NULL;

-- +goose Down
DROP INDEX tasks_goal_status_idx;
ALTER TABLE tasks DROP COLUMN goal_id;
DROP TABLE goal_updates;
DROP TABLE goals;
DROP TABLE artifact_blobs;
DROP TABLE artifacts;
