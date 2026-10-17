-- +goose Up
-- Segredos como recurso (ADR-0050). Escopo company (de qualquer admin da empresa) ou user (só do dono). O valor fica
-- cifrado (AES-256-GCM, secretbox) em value_enc; segredos de cofre externo guardam só a referência em external_ref.
CREATE TABLE secrets (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    scope           text NOT NULL DEFAULT 'company' CHECK (scope IN ('company','user')),
    owner_user_id   uuid REFERENCES users(id) ON DELETE CASCADE,
    name            text NOT NULL CHECK (name ~ '^[A-Za-z][A-Za-z0-9_.-]{0,63}$'),
    description     text NOT NULL DEFAULT '' CHECK (length(description) <= 300),
    provider        text NOT NULL DEFAULT 'local' CHECK (provider IN ('local','vault')),
    value_enc       text NOT NULL DEFAULT '',
    external_ref    text NOT NULL DEFAULT '' CHECK (length(external_ref) <= 500),
    version         integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    revealed_at     timestamptz,
    last_used_at    timestamptz,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'company' AND owner_user_id IS NULL) OR (scope = 'user' AND owner_user_id IS NOT NULL)),
    CHECK ((provider = 'local' AND value_enc <> '' AND external_ref = '') OR (provider = 'vault' AND value_enc = '' AND external_ref <> ''))
);
CREATE UNIQUE INDEX secrets_company_name_uq ON secrets (company_id, name) WHERE scope = 'company';
CREATE UNIQUE INDEX secrets_user_name_uq ON secrets (company_id, owner_user_id, name) WHERE scope = 'user';
CREATE INDEX secrets_company_idx ON secrets (company_id, scope, name);

-- +goose Down
DROP TABLE secrets;
