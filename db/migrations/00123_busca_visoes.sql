-- +goose Up
-- Busca única estendida (ADR-0042 §C, sobre o ADR-0024): metas, projetos, gates e artefatos entram no full-text, com a
-- mesma função hanko_fold e a configuração 'simple'. Fluxos já estavam (00015). Mais os índices das visões "Minhas".

ALTER TABLE goals ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(title, 1000) || ' ' || left(slug, 1000) || ' ' || left(metric, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(description, 100000))), 'B')
) STORED;
CREATE INDEX goals_search_idx ON goals USING gin (search_tsv);

ALTER TABLE projects ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(name, 1000) || ' ' || left(slug, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(description, 100000))), 'B')
) STORED;
CREATE INDEX projects_search_idx ON projects USING gin (search_tsv);

ALTER TABLE gates ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(title, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(body, 100000))), 'B') ||
    setweight(to_tsvector('simple', hanko_fold(left(decision_note, 100000))), 'C')
) STORED;
CREATE INDEX gates_search_idx ON gates USING gin (search_tsv);

ALTER TABLE artifacts ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(name, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(mime, 200))), 'B')
) STORED;
CREATE INDEX artifacts_search_idx ON artifacts USING gin (search_tsv);

-- "Minhas" (ADR-0042 §B): quem criou a task e quem comentou, por pessoa.
CREATE INDEX tasks_created_by_idx ON tasks (company_id, created_by_id) WHERE created_by_type = 'human' AND created_by_id IS NOT NULL;
CREATE INDEX task_comments_author_idx ON task_comments (company_id, author_id, task_id) WHERE author_type = 'human' AND author_id IS NOT NULL;

-- Participação de uma pessoa nas tasks da empresa: criou, atribuiu (fato task.assigned com o ator dela), foi
-- mencionada, comentou, ou é da audiência de um gate (pediu/decidiu um gate da task, ou o gate está pendente numa task
-- em que ela já participa). Uma linha por task, com os motivos. Usada pela lista de tasks (?minhas=1) e pelos chips.
-- +goose StatementBegin
CREATE FUNCTION task_participation(p_company uuid, p_user uuid)
RETURNS TABLE (task_id uuid, created boolean, assigned boolean, mentioned boolean, commented boolean, gate boolean)
LANGUAGE sql STABLE AS $$
    WITH raw AS (
        SELECT t.id AS task_id, true AS created, false AS assigned, false AS mentioned, false AS commented, false AS gate
        FROM tasks t
        WHERE t.company_id = p_company AND t.created_by_type = 'human' AND t.created_by_id = p_user
        UNION ALL
        SELECT DISTINCT a.subject_id, false, true, false, false, false
        FROM activity_events a
        WHERE a.company_id = p_company AND a.verb = 'task.assigned' AND a.subject_type = 'task'
          AND a.actor_type = 'human' AND a.actor_id = p_user
        UNION ALL
        SELECT DISTINCT m.task_id, false, false, true, false, false
        FROM comment_mentions m
        WHERE m.company_id = p_company AND m.user_id = p_user
        UNION ALL
        SELECT DISTINCT k.task_id, false, false, false, true, false
        FROM task_comments k
        WHERE k.company_id = p_company AND k.author_type = 'human' AND k.author_id = p_user
        UNION ALL
        SELECT DISTINCT g.task_id, false, false, false, false, true
        FROM gates g
        WHERE g.company_id = p_company AND g.task_id IS NOT NULL
          AND ((g.requested_by_type = 'human' AND g.requested_by_id = p_user)
            OR (g.decided_by_type = 'human' AND g.decided_by_id = p_user))
    ), agg AS (
        SELECT r.task_id, bool_or(r.created) AS created, bool_or(r.assigned) AS assigned, bool_or(r.mentioned) AS mentioned,
               bool_or(r.commented) AS commented, bool_or(r.gate) AS gate
        FROM raw r GROUP BY r.task_id
    )
    SELECT a.task_id, a.created, a.assigned, a.mentioned, a.commented,
           (a.gate OR ((a.created OR a.assigned OR a.mentioned OR a.commented)
                       AND EXISTS (SELECT 1 FROM gates g WHERE g.task_id = a.task_id AND g.status = 'pending')))
    FROM agg a
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS task_participation(uuid, uuid);
DROP INDEX IF EXISTS task_comments_author_idx;
DROP INDEX IF EXISTS tasks_created_by_idx;
DROP INDEX IF EXISTS artifacts_search_idx;
ALTER TABLE artifacts DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS gates_search_idx;
ALTER TABLE gates DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS projects_search_idx;
ALTER TABLE projects DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS goals_search_idx;
ALTER TABLE goals DROP COLUMN IF EXISTS search_tsv;
