-- +goose Up
-- Busca única da empresa (ADR-0024): full-text do Postgres sobre tasks, comentários, personas, execuções e fluxos.
--
-- Sem extensões: a função hanko_fold dobra caixa e acentos com translate(), que é IMMUTABLE e não depende do locale do
-- banco (o unaccent do contrib exige CREATE EXTENSION e sua função não é IMMUTABLE). Pontuação ASCII vira espaço, de modo
-- que "src/foo.go", "a@b.com" e "HNK-12" viram palavras simples dos dois lados (documento e consulta). A mesma tabela
-- existe em Go (internal/search.Fold) e é comparada com esta função por um teste de integração.

-- +goose StatementBegin
CREATE FUNCTION hanko_fold(t text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
AS $fn$
    SELECT lower(translate(t,
        'ÁÀÂÃÄÅÉÈÊËÍÌÎÏÓÒÔÕÖÚÙÛÜÇÑÝáàâãäåéèêëíìîïóòôõöúùûüçñýÿ.,;:/\-_@#()[]{}<>"''!?*+=&%$|^~`',
        'aaaaaaeeeeiiiiooooouuuucnyaaaaaaeeeeiiiiooooouuuucnyy                                '))
$fn$;
-- +goose StatementEnd

-- Colunas tsvector geradas (STORED) + GIN. left(…, 100000) mantém cada documento longe do teto de 1 MB do tsvector
-- (um INSERT nunca falha por causa de um texto enorme). Pesos: A título/nome, B descrição/resumo, C texto longo.
ALTER TABLE tasks ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(title, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(description, 100000))), 'B')
) STORED;
CREATE INDEX tasks_search_idx ON tasks USING gin (search_tsv);

ALTER TABLE task_comments ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', hanko_fold(left(body, 100000)))
) STORED;
CREATE INDEX task_comments_search_idx ON task_comments USING gin (search_tsv);

ALTER TABLE personas ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(name, 1000) || ' ' || left(slug, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(title, 1000))), 'B') ||
    setweight(to_tsvector('simple', hanko_fold(left(instructions, 100000))), 'C')
) STORED;
CREATE INDEX personas_search_idx ON personas USING gin (search_tsv);

ALTER TABLE runs ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(name, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(summary, 100000))), 'B')
) STORED;
CREATE INDEX runs_search_idx ON runs USING gin (search_tsv);

ALTER TABLE flows ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(name, 1000) || ' ' || left(slug, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(description, 100000))), 'B') ||
    setweight(to_tsvector('simple', hanko_fold(left(task_title, 1000) || ' ' || left(task_description, 100000))), 'C')
) STORED;
CREATE INDEX flows_search_idx ON flows USING gin (search_tsv);

-- +goose Down
DROP INDEX IF EXISTS flows_search_idx;
ALTER TABLE flows DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS runs_search_idx;
ALTER TABLE runs DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS personas_search_idx;
ALTER TABLE personas DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS task_comments_search_idx;
ALTER TABLE task_comments DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS tasks_search_idx;
ALTER TABLE tasks DROP COLUMN IF EXISTS search_tsv;
DROP FUNCTION IF EXISTS hanko_fold(text);
