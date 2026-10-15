-- +goose Up
-- Busca (ADR-0024, adendo da revisão): hanko_fold passa a REMOVER os caracteres de formatação (Unicode Cf: hífen suave,
-- ZWJ/ZWNJ/ZWSP, marcas de direção, BOM, tags). O tokenizador do Postgres trata alguns como letra e outros como
-- separador, e eles são invisíveis: "co\u00ADoperar" tem de achar "cooperar". A mesma lista fixa vive em Go
-- (internal/search.formatRunes) e um teste de integração as compara.
--
-- Alterar uma função usada por coluna gerada não recalcula as linhas existentes nem os índices GIN: por isso a migration
-- derruba índices e colunas geradas, troca a função e recria tudo. O custo é o mesmo da 00015 (reescreve tasks,
-- task_comments, personas, runs e flows, com bloqueio de escrita; ~25 s com 80 mil runs, 60 mil comentários e 30 mil tasks).
-- Em base grande, planeje a janela. O índice task_comments_company_created_idx não depende da função e fica.

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

-- +goose StatementBegin
CREATE FUNCTION hanko_fold(t text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
AS $fn$
    SELECT lower(translate(t,
        'ÁÀÂÃÄÅÉÈÊËÍÌÎÏÓÒÔÕÖÚÙÛÜÇÑÝáàâãäåéèêëíìîïóòôõöúùûüçñýÿ.,;:/\-_@#()[]{}<>"''!?*+=&%$|^~`' ||
        U&'\00AD\0600\0601\0602\0603\0604\0605\061C\06DD\070F\0890\0891' ||
        U&'\08E2\180E\200B\200C\200D\200E\200F\202A\202B\202C\202D\202E' ||
        U&'\2060\2061\2062\2063\2064\2066\2067\2068\2069\206A\206B\206C' ||
        U&'\206D\206E\206F\FEFF\FFF9\FFFA\FFFB\+0110BD\+0110CD\+013430\+013431\+013432' ||
        U&'\+013433\+013434\+013435\+013436\+013437\+013438\+013439\+01343A\+01343B\+01343C\+01343D\+01343E' ||
        U&'\+01343F\+01BCA0\+01BCA1\+01BCA2\+01BCA3\+01D173\+01D174\+01D175\+01D176\+01D177\+01D178\+01D179' ||
        U&'\+01D17A\+0E0001\+0E0020\+0E0021\+0E0022\+0E0023\+0E0024\+0E0025\+0E0026\+0E0027\+0E0028\+0E0029' ||
        U&'\+0E002A\+0E002B\+0E002C\+0E002D\+0E002E\+0E002F\+0E0030\+0E0031\+0E0032\+0E0033\+0E0034\+0E0035' ||
        U&'\+0E0036\+0E0037\+0E0038\+0E0039\+0E003A\+0E003B\+0E003C\+0E003D\+0E003E\+0E003F\+0E0040\+0E0041' ||
        U&'\+0E0042\+0E0043\+0E0044\+0E0045\+0E0046\+0E0047\+0E0048\+0E0049\+0E004A\+0E004B\+0E004C\+0E004D' ||
        U&'\+0E004E\+0E004F\+0E0050\+0E0051\+0E0052\+0E0053\+0E0054\+0E0055\+0E0056\+0E0057\+0E0058\+0E0059' ||
        U&'\+0E005A\+0E005B\+0E005C\+0E005D\+0E005E\+0E005F\+0E0060\+0E0061\+0E0062\+0E0063\+0E0064\+0E0065' ||
        U&'\+0E0066\+0E0067\+0E0068\+0E0069\+0E006A\+0E006B\+0E006C\+0E006D\+0E006E\+0E006F\+0E0070\+0E0071' ||
        U&'\+0E0072\+0E0073\+0E0074\+0E0075\+0E0076\+0E0077\+0E0078\+0E0079\+0E007A\+0E007B\+0E007C\+0E007D' ||
        U&'\+0E007E\+0E007F',
        'aaaaaaeeeeiiiiooooouuuucnyaaaaaaeeeeiiiiooooouuuucnyy                                '))
$fn$;
-- +goose StatementEnd

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

-- +goose StatementBegin
CREATE FUNCTION hanko_fold(t text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
AS $fn$
    SELECT lower(translate(t,
        'ÁÀÂÃÄÅÉÈÊËÍÌÎÏÓÒÔÕÖÚÙÛÜÇÑÝáàâãäåéèêëíìîïóòôõöúùûüçñýÿ.,;:/\-_@#()[]{}<>"''!?*+=&%$|^~`',
        'aaaaaaeeeeiiiiooooouuuucnyaaaaaaeeeeiiiiooooouuuucnyy                                '))
$fn$;
-- +goose StatementEnd

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
