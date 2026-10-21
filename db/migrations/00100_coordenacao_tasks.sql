-- +goose Up
-- Coordenação de tasks (ADR-0040): dependências bloqueantes, árvore pai/filho, pausa herdada por subárvore e
-- política de revisão por task. Tudo em tabelas novas (nenhuma coluna nova em tasks/runs/projects), aditivo.

-- FK composta (company_id, id): uma relação entre tasks de empresas diferentes é impossível no banco.
CREATE UNIQUE INDEX tasks_company_id_id_uq ON tasks (company_id, id);

-- Árvore: o pai fica na mesma empresa, sem ciclo e a no máximo 10 níveis (inclui o peso da subárvore de quem muda de pai).
CREATE INDEX tasks_parent_idx ON tasks (parent_id) WHERE parent_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION tasks_parent_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    up_depth   integer;
    down_depth integer;
BEGIN
    IF NEW.parent_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.parent_id IS NOT DISTINCT FROM OLD.parent_id THEN
        RETURN NEW;
    END IF;
    IF NEW.parent_id = NEW.id THEN
        RAISE EXCEPTION 'task cannot be its own parent' USING ERRCODE = '23514', CONSTRAINT = 'tasks_parent_no_cycle';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('tasks_parent:' || NEW.company_id::text, 0));
    IF NOT EXISTS (SELECT 1 FROM tasks p WHERE p.id = NEW.parent_id AND p.company_id = NEW.company_id) THEN
        RAISE EXCEPTION 'parent task not found in company' USING ERRCODE = '23514', CONSTRAINT = 'tasks_parent_company';
    END IF;
    -- ancestrais do novo pai (inclusive ele): se NEW.id aparece, fecharia um ciclo
    IF EXISTS (
        WITH RECURSIVE up(id, parent_id, depth) AS (
            SELECT t.id, t.parent_id, 1 FROM tasks t WHERE t.id = NEW.parent_id
            UNION ALL
            SELECT t.id, t.parent_id, up.depth + 1 FROM tasks t JOIN up ON t.id = up.parent_id WHERE up.depth < 64
        ) SELECT 1 FROM up WHERE id = NEW.id
    ) THEN
        RAISE EXCEPTION 'task parent cycle' USING ERRCODE = '23514', CONSTRAINT = 'tasks_parent_no_cycle';
    END IF;
    WITH RECURSIVE up(id, parent_id, depth) AS (
        SELECT t.id, t.parent_id, 1 FROM tasks t WHERE t.id = NEW.parent_id
        UNION ALL
        SELECT t.id, t.parent_id, up.depth + 1 FROM tasks t JOIN up ON t.id = up.parent_id WHERE up.depth < 64
    ) SELECT COALESCE(max(depth), 0) INTO up_depth FROM up;
    down_depth := 0;
    IF TG_OP = 'UPDATE' THEN
        WITH RECURSIVE down(id, depth) AS (
            SELECT t.id, 1 FROM tasks t WHERE t.parent_id = NEW.id
            UNION ALL
            SELECT t.id, down.depth + 1 FROM tasks t JOIN down ON t.parent_id = down.id WHERE down.depth < 64
        ) SELECT COALESCE(max(depth), 0) INTO down_depth FROM down;
    END IF;
    IF up_depth + 1 + down_depth > 10 THEN
        RAISE EXCEPTION 'task tree too deep (max 10 levels)' USING ERRCODE = '23514', CONSTRAINT = 'tasks_parent_depth';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER tasks_parent_guard BEFORE INSERT OR UPDATE OF parent_id ON tasks
    FOR EACH ROW EXECUTE FUNCTION tasks_parent_guard();

-- Dependências bloqueantes: task_id só anda depois que depends_on_id fecha (done ou cancelled).
CREATE TABLE task_dependencies (
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    task_id         uuid NOT NULL,
    depends_on_id   uuid NOT NULL,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (task_id, depends_on_id),
    CONSTRAINT task_dependencies_not_self CHECK (task_id <> depends_on_id),
    FOREIGN KEY (company_id, task_id) REFERENCES tasks (company_id, id) ON DELETE CASCADE,
    FOREIGN KEY (company_id, depends_on_id) REFERENCES tasks (company_id, id) ON DELETE CASCADE
);
CREATE INDEX task_dependencies_dep_idx ON task_dependencies (depends_on_id);
CREATE INDEX task_dependencies_company_idx ON task_dependencies (company_id);

-- Sem ciclo: o lock por empresa serializa as inserções concorrentes (A→B e B→A não se enxergam antes do commit);
-- as consultas da função usam um snapshot novo a cada comando (READ COMMITTED), então enxergam o que o outro já gravou.
-- +goose StatementBegin
CREATE FUNCTION task_dependencies_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('task_dependencies:' || NEW.company_id::text, 0));
    IF EXISTS (
        WITH RECURSIVE reach(id) AS (
            SELECT NEW.depends_on_id
            UNION
            SELECT d.depends_on_id FROM task_dependencies d JOIN reach r ON d.task_id = r.id
        ) SELECT 1 FROM reach WHERE id = NEW.task_id
    ) THEN
        RAISE EXCEPTION 'task dependency cycle' USING ERRCODE = '23514', CONSTRAINT = 'task_dependencies_no_cycle';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER task_dependencies_guard BEFORE INSERT ON task_dependencies
    FOR EACH ROW EXECUTE FUNCTION task_dependencies_guard();

-- Pausa: só a raiz da pausa tem linha; os descendentes herdam por cálculo (ancestral mais próximo com linha).
CREATE TABLE task_holds (
    task_id      uuid PRIMARY KEY,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    reason       text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    held_by_type text NOT NULL CHECK (held_by_type IN ('human','persona','system')),
    held_by_id   uuid,
    held_at      timestamptz NOT NULL,
    FOREIGN KEY (company_id, task_id) REFERENCES tasks (company_id, id) ON DELETE CASCADE
);
CREATE INDEX task_holds_company_idx ON task_holds (company_id, held_at DESC);

-- Wakes recusados porque a task estava pausada (ou herdava a pausa). Retomar os reenvia e apaga.
CREATE TABLE held_wakes (
    task_id      uuid PRIMARY KEY,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    persona_id   uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    trigger      text NOT NULL,
    requested_at timestamptz NOT NULL,
    FOREIGN KEY (company_id, task_id) REFERENCES tasks (company_id, id) ON DELETE CASCADE
);

-- A task (ou algum ancestral) está pausada? Usada pelo heartbeat e pelas consultas; profundidade limitada.
-- +goose StatementBegin
CREATE FUNCTION task_effectively_held(tid uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
    WITH RECURSIVE up(id, parent_id, depth) AS (
        SELECT t.id, t.parent_id, 0 FROM tasks t WHERE t.id = tid
        UNION ALL
        SELECT t.id, t.parent_id, up.depth + 1 FROM tasks t JOIN up ON t.id = up.parent_id WHERE up.depth < 64
    ) SELECT EXISTS (SELECT 1 FROM up JOIN task_holds h ON h.task_id = up.id)
$$;
-- +goose StatementEnd

-- Política de revisão por task e por projeto (precedência task > projeto > persona). require_review NULL herda;
-- reviewer_mode: inherit | none (direto ao gate humano) | custom (persona revisora OU harness+modelo).
CREATE TABLE task_review_overrides (
    task_id             uuid PRIMARY KEY,
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    require_review      boolean,
    reviewer_mode       text NOT NULL DEFAULT 'inherit' CHECK (reviewer_mode IN ('inherit','none','custom')),
    reviewer_persona_id uuid REFERENCES personas(id) ON DELETE SET NULL,
    reviewer_agent      text NOT NULL DEFAULT '' CHECK (reviewer_agent IN ('', 'claude', 'codex', 'fake')),
    reviewer_model      text NOT NULL DEFAULT '',
    updated_by_type     text NOT NULL CHECK (updated_by_type IN ('human','persona','system')),
    updated_by_id       uuid,
    updated_at          timestamptz NOT NULL,
    CONSTRAINT task_review_overrides_exclusive CHECK (reviewer_persona_id IS NULL OR reviewer_agent = ''),
    FOREIGN KEY (company_id, task_id) REFERENCES tasks (company_id, id) ON DELETE CASCADE
);

CREATE TABLE project_review_policies (
    project_id          uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    require_review      boolean,
    reviewer_mode       text NOT NULL DEFAULT 'inherit' CHECK (reviewer_mode IN ('inherit','none','custom')),
    reviewer_persona_id uuid REFERENCES personas(id) ON DELETE SET NULL,
    reviewer_agent      text NOT NULL DEFAULT '' CHECK (reviewer_agent IN ('', 'claude', 'codex', 'fake')),
    reviewer_model      text NOT NULL DEFAULT '',
    updated_by_type     text NOT NULL CHECK (updated_by_type IN ('human','persona','system')),
    updated_by_id       uuid,
    updated_at          timestamptz NOT NULL,
    CONSTRAINT project_review_policies_exclusive CHECK (reviewer_persona_id IS NULL OR reviewer_agent = '')
);

-- Origem da política que valeu em cada run: de que camada veio a obrigatoriedade da revisão e a do revisor.
CREATE TABLE run_policy_origins (
    run_id                 uuid PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    company_id             uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    review_required        boolean NOT NULL DEFAULT false,
    review_required_source text NOT NULL DEFAULT 'persona' CHECK (review_required_source IN ('task','project','persona')),
    reviewer_source        text NOT NULL DEFAULT 'persona' CHECK (reviewer_source IN ('task','project','persona')),
    updated_at             timestamptz NOT NULL
);

-- +goose Down
DROP TABLE run_policy_origins;
DROP TABLE project_review_policies;
DROP TABLE task_review_overrides;
DROP FUNCTION task_effectively_held(uuid);
DROP TABLE held_wakes;
DROP TABLE task_holds;
DROP TRIGGER task_dependencies_guard ON task_dependencies;
DROP FUNCTION task_dependencies_guard();
DROP TABLE task_dependencies;
DROP TRIGGER tasks_parent_guard ON tasks;
DROP FUNCTION tasks_parent_guard();
DROP INDEX tasks_parent_idx;
DROP INDEX tasks_company_id_id_uq;
