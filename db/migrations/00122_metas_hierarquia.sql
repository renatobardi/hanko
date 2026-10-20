-- +goose Up
-- Metas em árvore (ADR-0042 §D): goals.parent_id, na mesma empresa, sem ciclo e com no máximo 4 níveis.

-- FK composta (company_id, id): um pai de outra empresa é impossível no banco.
CREATE UNIQUE INDEX goals_company_id_id_uq ON goals (company_id, id);
ALTER TABLE goals ADD COLUMN parent_id uuid;
ALTER TABLE goals ADD CONSTRAINT goals_parent_fk FOREIGN KEY (company_id, parent_id) REFERENCES goals (company_id, id);
ALTER TABLE goals ADD CONSTRAINT goals_parent_not_self CHECK (parent_id IS NULL OR parent_id <> id);
CREATE INDEX goals_parent_idx ON goals (company_id, parent_id) WHERE parent_id IS NOT NULL;

-- Sem ciclo e profundidade máxima 4 (raiz = nível 1; inclui o peso da subárvore de quem muda de pai). O lock por
-- empresa serializa mudanças concorrentes (A sob B e B sob A não se enxergam antes do commit).
-- +goose StatementBegin
CREATE FUNCTION goals_parent_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
    PERFORM pg_advisory_xact_lock(hashtextextended('goals_parent:' || NEW.company_id::text, 0));
    IF EXISTS (
        WITH RECURSIVE up(id, parent_id, depth) AS (
            SELECT g.id, g.parent_id, 1 FROM goals g WHERE g.id = NEW.parent_id
            UNION ALL
            SELECT g.id, g.parent_id, up.depth + 1 FROM goals g JOIN up ON g.id = up.parent_id WHERE up.depth < 64
        ) SELECT 1 FROM up WHERE id = NEW.id
    ) THEN
        RAISE EXCEPTION 'goal parent cycle' USING ERRCODE = '23514', CONSTRAINT = 'goals_parent_no_cycle';
    END IF;
    WITH RECURSIVE up(id, parent_id, depth) AS (
        SELECT g.id, g.parent_id, 1 FROM goals g WHERE g.id = NEW.parent_id
        UNION ALL
        SELECT g.id, g.parent_id, up.depth + 1 FROM goals g JOIN up ON g.id = up.parent_id WHERE up.depth < 64
    ) SELECT COALESCE(max(depth), 0) INTO up_depth FROM up;
    down_depth := 0;
    IF TG_OP = 'UPDATE' THEN
        WITH RECURSIVE down(id, depth) AS (
            SELECT g.id, 1 FROM goals g WHERE g.parent_id = NEW.id
            UNION ALL
            SELECT g.id, down.depth + 1 FROM goals g JOIN down ON g.parent_id = down.id WHERE down.depth < 64
        ) SELECT COALESCE(max(depth), 0) INTO down_depth FROM down;
    END IF;
    IF up_depth + 1 + down_depth > 4 THEN
        RAISE EXCEPTION 'goal tree too deep (max 4 levels)' USING ERRCODE = '23514', CONSTRAINT = 'goals_parent_depth';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER goals_parent_guard BEFORE INSERT OR UPDATE OF parent_id ON goals
    FOR EACH ROW EXECUTE FUNCTION goals_parent_guard();

-- +goose Down
DROP TRIGGER IF EXISTS goals_parent_guard ON goals;
DROP FUNCTION IF EXISTS goals_parent_guard();
DROP INDEX IF EXISTS goals_parent_idx;
ALTER TABLE goals DROP CONSTRAINT IF EXISTS goals_parent_not_self;
ALTER TABLE goals DROP CONSTRAINT IF EXISTS goals_parent_fk;
ALTER TABLE goals DROP COLUMN IF EXISTS parent_id;
DROP INDEX IF EXISTS goals_company_id_id_uq;
