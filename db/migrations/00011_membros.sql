-- +goose Up
-- Multi-humano (ADR-0021): papéis por empresa e convites.
-- company_members liga um usuário a uma empresa com um papel (owner|admin|member|viewer).
-- company_invites guarda só o hash do token do convite (validade 7 dias).

CREATE TABLE company_members (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
    invited_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, user_id)
);
CREATE INDEX company_members_user_idx ON company_members (user_id);

CREATE TABLE company_invites (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    email       text NOT NULL CHECK (email <> ''),
    role        text NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
    token_hash  text NOT NULL UNIQUE,
    invited_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    accepted_by uuid REFERENCES users(id) ON DELETE SET NULL,
    revoked_at  timestamptz
);
-- no máximo um convite aberto por empresa e e-mail
CREATE UNIQUE INDEX company_invites_open_idx ON company_invites (company_id, lower(email))
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX company_invites_email_idx ON company_invites (lower(email));

-- invited: o usuário entrou por convite; no provider firebase com listas de admissão, isso o mantém
-- admitido mesmo fora das listas (ADR-0021). Os existentes ficam false: a regra do ADR-0018 não muda.
ALTER TABLE users ADD COLUMN invited boolean NOT NULL DEFAULT false;

-- Backfill: preserva o comportamento atual (todos os usuários acessam todas as empresas).
-- O papel do usuário na instalação vira o papel em cada empresa existente.
INSERT INTO company_members (company_id, user_id, role, created_at, updated_at)
SELECT c.id, u.id, u.role, now(), now() FROM companies c CROSS JOIN users u;

-- +goose Down
ALTER TABLE users DROP COLUMN invited;
DROP TABLE company_invites;
DROP TABLE company_members;
