-- +goose Up
-- Tags de persona (ADR-0087): cadastro por empresa, ligação persona ↔ tag e coluna de nomes para a busca. Contrato da
-- #118: tabelas, colunas e constraints novas, sem mover dado. O service, as telas, a API, a CLI e o MCP vêm na fatia 1.
--
-- Os limites de 20 tags por persona e 200 por empresa (ADR-0087 item 2) ficam no service, que devolve erro de validação
-- sem cortar nada. A regra "tag arquivada não se aplica" (item 3) também fica no service: a ligação que já existe continua.

-- 1. Cadastro (ADR-0087 itens 1 e 2). O nome é único na empresa sem distinguir caixa (`lower`), aceita acento e espaço e
-- não tem espaço nas pontas. A cor é um nome da paleta fechada (`domain.PersonaTagColors`), nunca hex livre.
-- `archived_at` vazio = ativa.
CREATE TABLE persona_tags (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    name        text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40 AND name = btrim(name)),
    color       text NOT NULL DEFAULT 'gray'
                CHECK (color IN ('gray', 'red', 'amber', 'green', 'teal', 'blue', 'violet', 'pink')),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 200),
    archived_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, id)
);
CREATE UNIQUE INDEX persona_tags_name_uq ON persona_tags (company_id, lower(name));

-- 2. Ligação (ADR-0087 item 1): uma linha por par persona ↔ tag. As duas chaves compostas com `company_id` impedem ligar
-- a tag de uma empresa à persona de outra. Deletar a tag apaga as ligações (item 3); deletar a persona também.
CREATE UNIQUE INDEX personas_company_id_uq ON personas (company_id, id);
CREATE TABLE persona_tag_links (
    company_id uuid NOT NULL,
    persona_id uuid NOT NULL,
    tag_id     uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (persona_id, tag_id),
    FOREIGN KEY (company_id, persona_id) REFERENCES personas (company_id, id) ON DELETE CASCADE,
    FOREIGN KEY (company_id, tag_id) REFERENCES persona_tags (company_id, id) ON DELETE CASCADE
);
CREATE INDEX persona_tag_links_tag_idx ON persona_tag_links (tag_id);

-- 3. Busca (ADR-0087 item 10; ADR-0024). `search_tsv` é gerada e não lê outra tabela, então a persona ganha `tag_names`:
-- os nomes das tags ligadas, separados por quebra de linha, mantidos pelo service na mesma transação de aplicar, tirar,
-- renomear ou deletar. O teto cobre 20 nomes de 40 caracteres com o separador. A coluna gerada é recriada com os mesmos
-- pesos da 00024 e `tag_names` com peso B.
ALTER TABLE personas ADD COLUMN tag_names text NOT NULL DEFAULT '' CHECK (char_length(tag_names) <= 1000);
DROP INDEX personas_search_idx;
ALTER TABLE personas DROP COLUMN search_tsv;
ALTER TABLE personas ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(name, 1000) || ' ' || left(slug, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(title, 1000))), 'B') ||
    setweight(to_tsvector('simple', hanko_fold(tag_names)), 'B') ||
    setweight(to_tsvector('simple', hanko_fold(left(instructions, 100000))), 'C')
) STORED;
CREATE INDEX personas_search_idx ON personas USING gin (search_tsv);

-- +goose Down
DROP INDEX personas_search_idx;
ALTER TABLE personas DROP COLUMN search_tsv;
ALTER TABLE personas DROP COLUMN tag_names;
ALTER TABLE personas ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(name, 1000) || ' ' || left(slug, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(title, 1000))), 'B') ||
    setweight(to_tsvector('simple', hanko_fold(left(instructions, 100000))), 'C')
) STORED;
CREATE INDEX personas_search_idx ON personas USING gin (search_tsv);
DROP TABLE persona_tag_links;
DROP INDEX personas_company_id_uq;
DROP TABLE persona_tags;
