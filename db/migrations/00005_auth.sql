-- +goose Up
-- M5: autenticação. Usuários humanos (provider local ou firebase), sessões de cookie
-- (só o hash do token) e API keys de usuário (só o hash). ADR-0007, ADR-0014.

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    email         text NOT NULL CHECK (email <> ''),
    name          text NOT NULL DEFAULT '',
    provider      text NOT NULL CHECK (provider IN ('local','firebase')),
    external_id   text,
    password_hash text,
    role          text NOT NULL DEFAULT 'member' CHECK (role IN ('owner','admin','member')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CHECK (
        (provider = 'local'    AND password_hash IS NOT NULL AND external_id IS NULL) OR
        (provider = 'firebase' AND password_hash IS NULL     AND external_id IS NOT NULL AND external_id <> '')
    )
);
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));
CREATE UNIQUE INDEX users_provider_external_idx ON users (provider, external_id) WHERE external_id IS NOT NULL;

CREATE TABLE sessions (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    user_agent   text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE api_keys (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (name <> ''),
    prefix       text NOT NULL,
    hash         text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);
CREATE INDEX api_keys_user_idx ON api_keys (user_id);

-- +goose Down
DROP TABLE api_keys;
DROP TABLE sessions;
DROP TABLE users;
