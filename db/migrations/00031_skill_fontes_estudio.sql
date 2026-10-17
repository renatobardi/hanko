-- +goose Up
-- Skills: versões imutáveis, versão fixada por persona, fontes git e estúdio de teste (ADR-0035).

-- Cada mudança de nome, descrição ou corpo de uma skill grava uma versão (revision = skills.version naquele
-- momento). Versões nunca mudam; somem só com a skill.
CREATE TABLE skill_versions (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    skill_id      uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    revision      integer NOT NULL CHECK (revision >= 1),
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    body          text NOT NULL,
    sha256        text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    source_commit text CHECK (source_commit IS NULL OR source_commit ~ '^[0-9a-f]{40}$'),
    author_type   text NOT NULL CHECK (author_type IN ('human','persona','system')),
    author_id     uuid,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (skill_id, revision)
);
CREATE INDEX skill_versions_company_idx ON skill_versions (company_id);

-- Backfill: o conteúdo atual de cada skill vira a versão corrente (as anteriores não foram guardadas).
INSERT INTO skill_versions (id, company_id, skill_id, revision, name, description, body, sha256, author_type, author_id, created_at)
SELECT gen_random_uuid(), s.company_id, s.id, s.version, s.name, s.description, s.body,
       encode(sha256(convert_to(s.body, 'UTF8')), 'hex'), s.updated_by_type, s.updated_by_id, s.updated_at
FROM skills s;

-- Versão fixada: a persona usa esta revisão em vez da mais nova. NULL = sempre a mais nova.
ALTER TABLE persona_skills ADD COLUMN pinned_revision integer;
ALTER TABLE persona_skills ADD CONSTRAINT persona_skills_pinned_fk
    FOREIGN KEY (skill_id, pinned_revision) REFERENCES skill_versions (skill_id, revision);

-- Fontes git de skills. A URL é sempre https de host público (sem credenciais); tracking_ref é um branch ou HEAD.
CREATE TABLE skill_sources (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    url             text NOT NULL,
    tracking_ref    text NOT NULL DEFAULT 'HEAD',
    path_prefix     text NOT NULL DEFAULT '',
    excluded_paths  text[] NOT NULL DEFAULT '{}',
    auto_update     boolean NOT NULL DEFAULT true,
    import_new      boolean NOT NULL DEFAULT false,
    enabled         boolean NOT NULL DEFAULT true,
    last_commit     text CHECK (last_commit IS NULL OR last_commit ~ '^[0-9a-f]{40}$'),
    last_tree_hash  text,
    status          text NOT NULL DEFAULT 'idle' CHECK (status IN ('idle','syncing','ok','error','paused')),
    last_error      text NOT NULL DEFAULT '',
    failure_count   integer NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    next_sync_at    timestamptz,
    last_attempt_at timestamptz,
    last_success_at timestamptz,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, url, tracking_ref)
);
CREATE INDEX skill_sources_due_idx ON skill_sources (next_sync_at) WHERE enabled AND auto_update;

-- O que a última sincronização encontrou em cada diretório com SKILL.md e a escolha do admin.
CREATE TABLE skill_source_entries (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_id   uuid NOT NULL REFERENCES skill_sources(id) ON DELETE CASCADE,
    path        text NOT NULL,
    name        text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    skill_id    uuid REFERENCES skills(id) ON DELETE SET NULL,
    selection   text NOT NULL CHECK (selection IN ('selected','excluded','new')),
    present     boolean NOT NULL DEFAULT true,
    has_scripts boolean NOT NULL DEFAULT false,
    error       text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_id, path)
);
CREATE INDEX skill_source_entries_company_idx ON skill_source_entries (company_id);
CREATE INDEX skill_source_entries_skill_idx ON skill_source_entries (skill_id);

-- Skill que veio de uma fonte. source_modified: editada aqui depois de importada; a sincronização não a
-- sobrescreve sem force.
ALTER TABLE skills ADD COLUMN source_id uuid REFERENCES skill_sources(id) ON DELETE SET NULL;
ALTER TABLE skills ADD COLUMN source_path text NOT NULL DEFAULT '';
ALTER TABLE skills ADD COLUMN source_commit text;
ALTER TABLE skills ADD COLUMN source_modified boolean NOT NULL DEFAULT false;
CREATE INDEX skills_source_idx ON skills (source_id) WHERE source_id IS NOT NULL;

-- Estúdio: entradas salvas e execuções de teste de uma skill.
CREATE TABLE skill_test_inputs (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    skill_id        uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    name            text NOT NULL,
    content         text NOT NULL,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (skill_id, name)
);
CREATE INDEX skill_test_inputs_company_idx ON skill_test_inputs (company_id);

CREATE TABLE skill_test_runs (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    skill_id        uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    skill_revision  integer NOT NULL,
    input_id        uuid REFERENCES skill_test_inputs(id) ON DELETE SET NULL,
    input           text NOT NULL,
    persona_id      uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    task_id         uuid REFERENCES tasks(id) ON DELETE SET NULL,
    run_id          uuid REFERENCES runs(id) ON DELETE SET NULL,
    status          text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed','cancelled')),
    output          text NOT NULL DEFAULT '',
    error           text NOT NULL DEFAULT '',
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    FOREIGN KEY (skill_id, skill_revision) REFERENCES skill_versions (skill_id, revision)
);
CREATE INDEX skill_test_runs_skill_idx ON skill_test_runs (skill_id, created_at DESC);
CREATE INDEX skill_test_runs_company_idx ON skill_test_runs (company_id, created_at DESC);
CREATE UNIQUE INDEX skill_test_runs_task_idx ON skill_test_runs (task_id) WHERE task_id IS NOT NULL;

-- +goose Down
DROP TABLE skill_test_runs;
DROP TABLE skill_test_inputs;
DROP INDEX skills_source_idx;
ALTER TABLE skills DROP COLUMN source_modified;
ALTER TABLE skills DROP COLUMN source_commit;
ALTER TABLE skills DROP COLUMN source_path;
ALTER TABLE skills DROP COLUMN source_id;
DROP TABLE skill_source_entries;
DROP TABLE skill_sources;
ALTER TABLE persona_skills DROP CONSTRAINT persona_skills_pinned_fk;
ALTER TABLE persona_skills DROP COLUMN pinned_revision;
DROP TABLE skill_versions;
