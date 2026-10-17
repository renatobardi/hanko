-- +goose Up
-- Pipelines, cases, fila de revisão, aprendizados e exemplos de decisão (ADR-0034).
--
-- Pipeline: etapas ordenadas (working|review|done|cancelled) com transições permitidas, aprovação humana, automação
-- por persona e destinos de revisão. Case: um registro estruturado de trabalho (um lead, um incidente, um pedido),
-- solto (status livre) ou numa etapa de pipeline (status derivado do tipo da etapa). Toda escrita sobe a versão
-- (trava otimista) e deixa um evento imutável no histórico do case.

CREATE TABLE pipelines (
    id                  uuid PRIMARY KEY,
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    project_id          uuid REFERENCES projects(id) ON DELETE SET NULL,
    key                 text NOT NULL CHECK (key ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    name                text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description         text NOT NULL DEFAULT '' CHECK (octet_length(description) <= 8192),
    enforce_transitions boolean NOT NULL DEFAULT false,
    archived_at         timestamptz,
    created_by_type     text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id       uuid,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, key)
);
CREATE INDEX pipelines_company_idx ON pipelines (company_id, archived_at, name);

-- Etapa: config (jsonb) é validada no service (campos, aprovação, automação, revisão, gates).
CREATE TABLE pipeline_stages (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    pipeline_id uuid NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    key         text NOT NULL CHECK (key ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    position    integer NOT NULL CHECK (position >= 0),
    kind        text NOT NULL CHECK (kind IN ('working','review','done','cancelled')),
    config      jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 65536),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (pipeline_id, key)
);
CREATE INDEX pipeline_stages_pipeline_idx ON pipeline_stages (pipeline_id, position, id);

-- Arestas permitidas entre etapas (o modo estrito do pipeline só deixa passar por elas).
CREATE TABLE pipeline_transitions (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    pipeline_id   uuid NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    from_stage_id uuid NOT NULL REFERENCES pipeline_stages(id) ON DELETE CASCADE,
    to_stage_id   uuid NOT NULL REFERENCES pipeline_stages(id) ON DELETE CASCADE,
    label         text NOT NULL DEFAULT '' CHECK (length(label) <= 120),
    CHECK (from_stage_id <> to_stage_id),
    UNIQUE (pipeline_id, from_stage_id, to_stage_id)
);

-- Numeração dos cases por empresa (C1, C2…), separada da das tasks.
CREATE TABLE case_sequences (
    company_id  uuid PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    last_number bigint NOT NULL DEFAULT 0
);

CREATE TABLE cases (
    id                   uuid PRIMARY KEY,
    company_id           uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    number               bigint NOT NULL CHECK (number >= 1),
    case_type            text NOT NULL DEFAULT '' CHECK (length(case_type) <= 60),
    key                  text CHECK (key IS NULL OR length(key) BETWEEN 1 AND 200),
    title                text NOT NULL CHECK (length(title) BETWEEN 1 AND 300),
    summary              text NOT NULL DEFAULT '' CHECK (octet_length(summary) <= 65536),
    fields               jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(fields) = 'object' AND octet_length(fields::text) <= 65536),
    status               text NOT NULL CHECK (status IN ('draft','in_progress','in_review','approved','done','cancelled')),
    pipeline_id          uuid REFERENCES pipelines(id) ON DELETE RESTRICT,
    stage_id             uuid REFERENCES pipeline_stages(id) ON DELETE RESTRICT,
    parent_case_id       uuid REFERENCES cases(id) ON DELETE SET NULL,
    project_id           uuid REFERENCES projects(id) ON DELETE SET NULL,
    version              integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    pending_suggestion   jsonb,
    -- approved_version é a versão do case logo depois da última aprovação; reapproval_required liga quando o
    -- conteúdo muda depois dela (a próxima saída da etapa exige nova revisão).
    approved_version     integer,
    reapproval_required  boolean NOT NULL DEFAULT false,
    -- "atenção" na fila de revisão: um bloqueio resolvido ou os filhos terminaram; some na próxima movimentação.
    attention            text CHECK (attention IS NULL OR attention IN ('blockers_resolved','children_terminal')),
    attention_at         timestamptz,
    terminal_kind        text CHECK (terminal_kind IS NULL OR terminal_kind IN ('done','cancelled')),
    terminal_at          timestamptz,
    child_count          integer NOT NULL DEFAULT 0 CHECK (child_count >= 0),
    terminal_child_count integer NOT NULL DEFAULT 0 CHECK (terminal_child_count >= 0),
    origin_run_id        uuid REFERENCES runs(id) ON DELETE SET NULL,
    created_by_type      text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id        uuid,
    completed_at         timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, number),
    CHECK ((pipeline_id IS NULL) = (stage_id IS NULL)),
    CHECK (parent_case_id IS NULL OR parent_case_id <> id)
);
-- upsert por (empresa, tipo, key)
CREATE UNIQUE INDEX cases_type_key_uq ON cases (company_id, case_type, key) WHERE key IS NOT NULL;
CREATE INDEX cases_company_updated_idx ON cases (company_id, updated_at DESC, id DESC);
CREATE INDEX cases_pipeline_stage_idx ON cases (pipeline_id, stage_id, updated_at DESC) WHERE pipeline_id IS NOT NULL;
CREATE INDEX cases_parent_idx ON cases (parent_case_id) WHERE parent_case_id IS NOT NULL;
CREATE INDEX cases_project_idx ON cases (company_id, project_id) WHERE project_id IS NOT NULL;
-- fila de revisão: sugestões pendentes e itens de atenção
CREATE INDEX cases_suggestion_idx ON cases (company_id) WHERE pending_suggestion IS NOT NULL AND terminal_kind IS NULL;
CREATE INDEX cases_attention_idx ON cases (company_id, attention_at) WHERE attention IS NOT NULL;

CREATE TABLE case_task_links (
    case_id           uuid NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    task_id           uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    role              text NOT NULL CHECK (role IN ('origin','work','automation','conversation')),
    created_by_run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (case_id, task_id)
);
CREATE INDEX case_task_links_task_idx ON case_task_links (task_id);

-- case_id fica bloqueado por blocker_case_id até o bloqueador chegar a done.
CREATE TABLE case_blockers (
    case_id         uuid NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    blocker_case_id uuid NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz,
    PRIMARY KEY (case_id, blocker_case_id),
    CHECK (case_id <> blocker_case_id)
);
CREATE INDEX case_blockers_blocker_idx ON case_blockers (blocker_case_id) WHERE resolved_at IS NULL;

-- Histórico do case: imutável (só a exclusão do case o apaga, em cascata).
CREATE TABLE case_events (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    case_id       uuid NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('created','updated','fields_changed','status_changed','transitioned',
                      'transition_forced','transition_suggested','suggestion_resolved','review_decided','task_linked',
                      'task_unlinked','automation_started','automation_failed','blockers_set','blockers_resolved',
                      'children_terminal')),
    from_stage_id uuid,
    to_stage_id   uuid,
    actor_type    text NOT NULL CHECK (actor_type IN ('human','persona','system')),
    actor_id      uuid,
    run_id        uuid,
    payload       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (actor_type <> 'persona' OR run_id IS NOT NULL)
);
CREATE INDEX case_events_case_idx ON case_events (case_id, created_at, id);
-- aprendizados: decisões de revisão e transições forçadas, por empresa e por etapa
CREATE INDEX case_events_learning_idx ON case_events (company_id, created_at DESC, id DESC)
    WHERE kind IN ('review_decided','transition_forced');
CREATE INDEX case_events_learning_stage_idx ON case_events (from_stage_id, created_at DESC)
    WHERE kind IN ('review_decided','transition_forced');

-- +goose StatementBegin
CREATE FUNCTION case_events_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'case_events is append-only';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER case_events_no_update BEFORE UPDATE ON case_events
    FOR EACH ROW EXECUTE FUNCTION case_events_immutable();

-- Exemplo de decisão: uma decisão humana (gate decidido ou revisão de case) com as notas de quem a salvou e o
-- contexto até o momento da decisão (snapshot), para avaliação e treino fora do Hanko.
CREATE TABLE decision_examples (
    id                 uuid PRIMARY KEY,
    company_id         uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind        text NOT NULL CHECK (source_kind IN ('gate','case_review')),
    source_id          uuid NOT NULL,
    task_id            uuid REFERENCES tasks(id) ON DELETE SET NULL,
    case_id            uuid REFERENCES cases(id) ON DELETE SET NULL,
    cutoff_at          timestamptz NOT NULL,
    decision_outcome   text NOT NULL CHECK (length(decision_outcome) BETWEEN 1 AND 40),
    notes              text NOT NULL DEFAULT '' CHECK (octet_length(notes) <= 16384),
    notes_history      jsonb NOT NULL DEFAULT '[]'::jsonb,
    snapshot           jsonb NOT NULL,
    created_by_user_id uuid REFERENCES users(id) ON DELETE CASCADE,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX decision_examples_author_uq
    ON decision_examples (source_kind, source_id, created_by_user_id) NULLS NOT DISTINCT;
CREATE INDEX decision_examples_company_idx ON decision_examples (company_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE decision_examples;
DROP TRIGGER case_events_no_update ON case_events;
DROP FUNCTION case_events_immutable();
DROP TABLE case_events;
DROP TABLE case_blockers;
DROP TABLE case_task_links;
DROP TABLE cases;
DROP TABLE case_sequences;
DROP TABLE pipeline_transitions;
DROP TABLE pipeline_stages;
DROP TABLE pipelines;
