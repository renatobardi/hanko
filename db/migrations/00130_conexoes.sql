-- +goose Up
-- Conexões OAuth para servidores MCP remotos (ADR-0043). A conexão guarda o estado da autorização (descoberta, cliente,
-- state/PKCE em voo, validade do token); o token em si mora nos segredos conn.<slug>.access|refresh (ADR-0050) e a
-- integração MCP correspondente (ADR-0029) referencia o segredo por ${secret:...}.
CREATE TABLE connections (
    id                     uuid PRIMARY KEY,
    company_id             uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    slug                   text NOT NULL CHECK (slug ~ '^[a-z][a-z0-9-]{0,62}$'),
    name                   text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    url                    text NOT NULL CHECK (length(url) BETWEEN 1 AND 2000),
    identity               text NOT NULL DEFAULT 'company' CHECK (identity IN ('company','user')),
    owner_user_id          uuid REFERENCES users(id) ON DELETE CASCADE,
    status                 text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending_auth','connected','expired','error','revoked')),
    integration_id         uuid REFERENCES integrations(id) ON DELETE SET NULL,
    -- descoberta (RFC 9728 / RFC 8414) e cliente registrado (RFC 7591)
    resource               text NOT NULL DEFAULT '',
    issuer                 text NOT NULL DEFAULT '',
    authorization_endpoint text NOT NULL DEFAULT '',
    token_endpoint         text NOT NULL DEFAULT '',
    registration_endpoint  text NOT NULL DEFAULT '',
    client_id              text NOT NULL DEFAULT '',
    client_secret_ref      text NOT NULL DEFAULT '',
    scopes                 text[] NOT NULL DEFAULT '{}',
    -- autorização em voo: só o hash do state; o verifier fica cifrado
    auth_state_hash        text,
    auth_verifier_enc      text NOT NULL DEFAULT '',
    auth_started_by_id     uuid,
    auth_expires_at        timestamptz,
    -- token
    expires_at             timestamptz,
    connected_at           timestamptz,
    refreshing_until       timestamptz,
    last_refresh_at        timestamptz,
    -- probe e último erro (sem segredo)
    last_probe_at          timestamptz,
    last_probe_status      text NOT NULL DEFAULT '',
    last_error             text NOT NULL DEFAULT '' CHECK (length(last_error) <= 500),
    gate_id                uuid REFERENCES gates(id) ON DELETE SET NULL,
    created_by_type        text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id          uuid,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CHECK ((identity = 'company' AND owner_user_id IS NULL) OR (identity = 'user' AND owner_user_id IS NOT NULL))
);
CREATE UNIQUE INDEX connections_company_slug_uq ON connections (company_id, slug);
CREATE UNIQUE INDEX connections_state_uq ON connections (auth_state_hash) WHERE auth_state_hash IS NOT NULL;
CREATE INDEX connections_due_idx ON connections (expires_at) WHERE status = 'connected' AND expires_at IS NOT NULL;
CREATE INDEX connections_integration_idx ON connections (integration_id) WHERE integration_id IS NOT NULL;

-- gates.kind: a lista COMPLETA vigente (00040) mais 'connection'.
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire','memory','tool','instruction','connection'));

-- +goose Down
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
DELETE FROM gates WHERE kind = 'connection';
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire','memory','tool','instruction'));
DROP TABLE connections;
