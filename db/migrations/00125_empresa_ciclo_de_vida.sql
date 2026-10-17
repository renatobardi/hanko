-- +goose Up
-- Ciclo de vida da empresa (ADR-0057): descrição, arquivamento reversível e o registro das exclusões (com o export
-- automático). Aditivo: colunas com padrão e uma tabela nova.

ALTER TABLE companies ADD COLUMN description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000);
ALTER TABLE companies ADD COLUMN archived_at timestamptz;
-- Quem arquivou (id de usuário, sem FK: a conta pode sumir antes da empresa).
ALTER TABLE companies ADD COLUMN archived_by uuid;
-- O que o arquivamento mexeu ({"personas":[ids],"flows":[ids]}), para o "restaurar e retomar".
ALTER TABLE companies ADD COLUMN archive_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX companies_archived_idx ON companies (archived_at) WHERE archived_at IS NOT NULL;

-- Exclusões: sem FK para companies (a empresa deixa de existir). export_gz é o hanko.company.v1 gerado antes de apagar.
CREATE TABLE company_deletions (
    id           uuid PRIMARY KEY,
    company_id   uuid NOT NULL,
    slug         text NOT NULL,
    name         text NOT NULL,
    deleted_by   uuid,
    deleted_at   timestamptz NOT NULL DEFAULT now(),
    export_gz    bytea NOT NULL,
    export_bytes bigint NOT NULL CHECK (export_bytes >= 0),
    sha256       text NOT NULL
);
CREATE INDEX company_deletions_at_idx ON company_deletions (deleted_at DESC);

-- +goose Down
DROP TABLE company_deletions;
DROP INDEX companies_archived_idx;
ALTER TABLE companies DROP COLUMN archive_snapshot;
ALTER TABLE companies DROP COLUMN archived_by;
ALTER TABLE companies DROP COLUMN archived_at;
ALTER TABLE companies DROP COLUMN description;
